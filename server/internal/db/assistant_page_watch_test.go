package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func watchJSON(question, display, unit string, alert any) json.RawMessage {
	m := map[string]any{"question": question, "display": display}
	if unit != "" {
		m["unit"] = unit
	}
	if alert != nil {
		m["alert"] = alert
	}
	raw, _ := json.Marshal(m)
	return raw
}

func numReading(v float64) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"value":%g}`, v))
}

// Creating and updating a watch page: watch is stored as raw JSON (NULL when absent), view is optional with a watch,
// a kept page's watch display is fixed, changing the alert rule turns Alert.On off, and Keep can set AlertOn and
// OnOverview in the same tap.
func TestAssistantPageWatchCreateUpdateAndKeep(t *testing.T) {
	d, ctx := pagesDB(t)

	// Watch draft with omitted View defaults View to "{}".
	w1 := watchJSON("Rent on Elm St", "number", "USD", map[string]any{"when": "below", "value": 2000})
	p := draft(1, "c1", "Elm Rent", pagesBase)
	p.View = nil
	p.Watch = w1
	created := mustCreate(t, d, p)
	if len(created.Watch) == 0 || string(created.View) != "{}" || created.OnOverview || created.RoutineID != "" ||
		created.Alert.On || created.Alert.Tripped || created.Alert.PushedAt != nil ||
		created.RefreshAskedAt != nil || created.RefreshDevice != "" {
		t.Fatalf("created watch draft: %+v", created)
	}
	// Invalid watch (non-object) is refused.
	bad := draft(2, "c1", "Bad Watch", pagesBase)
	bad.Watch = json.RawMessage(`[]`)
	if _, err := d.CreateAssistantPage(ctx, bad); err == nil {
		t.Fatal("non-object watch accepted")
	}

	// On a draft, changing the display is allowed, and watch-only update preserves View.
	wTrend := watchJSON("Rent on Elm St", "trend", "USD", map[string]any{"when": "below", "value": 2000})
	by := AssistantPageWriter{Kind: AssistantPageByAgent, Machine: "m1", Session: "c1"}
	updated, err := d.UpdateAssistantPage(ctx, AssistantPageUpdate{ID: created.ID, Expected: 1, Watch: wTrend, By: by, At: pagesBase.Add(time.Minute)})
	if err != nil || updated.Version != 2 || watchDisplay(updated.Watch) != "trend" || string(updated.View) != "{}" {
		t.Fatalf("draft watch update: %+v %v", updated, err)
	}

	// Keep with AlertOn and OnOverview switches both on atomically.
	kept, changed, err := d.KeepAssistantPage(ctx, AssistantPageKeep{
		OfferView:  created.OfferView,
		Device:     "web",
		AlertOn:    true,
		OnOverview: true,
		At:         pagesBase.Add(2 * time.Minute),
	})
	if err != nil || !changed || !kept.OnOverview || !kept.Alert.On || kept.Alert.Tripped || kept.Alert.PushedAt != nil {
		t.Fatalf("keep with switches: %+v %v %v", kept, changed, err)
	}

	// Once kept, changing the watch display (or clearing the watch) is refused.
	if _, err := d.UpdateAssistantPage(ctx, AssistantPageUpdate{
		ID:       kept.ID,
		Expected: kept.Version,
		Watch:    watchJSON("Rent", "status", "", nil),
		By:       by,
		At:       pagesBase.Add(3 * time.Minute),
	}); !errors.Is(err, ErrAssistantPageWatchDisplay) {
		t.Fatalf("changing kept watch display: %v", err)
	}
	if _, err := d.UpdateAssistantPage(ctx, AssistantPageUpdate{
		ID:         kept.ID,
		Expected:   kept.Version,
		ClearWatch: true,
		By:         by,
		At:         pagesBase.Add(3 * time.Minute),
	}); !errors.Is(err, ErrAssistantPageWatchDisplay) {
		t.Fatalf("clearing kept watch: %v", err)
	}

	// Updating question/unit with the SAME alert rule preserves Alert.On.
	sameRule := watchJSON("1-bed rent on Elm St", "trend", "EUR", map[string]any{"when": "below", "value": 2000})
	row, err := d.UpdateAssistantPage(ctx, AssistantPageUpdate{ID: kept.ID, Expected: kept.Version, Watch: sameRule, By: by, At: pagesBase.Add(4 * time.Minute)})
	if err != nil || !row.Alert.On {
		t.Fatalf("same alert rule should keep Alert.On: %+v %v", row, err)
	}

	// Changing the alert rule turns Alert.On off while preserving PushedAt.
	pushed := pagesBase.Add(5 * time.Minute)
	if _, err := d.RecordAssistantPageAlertState(ctx, kept.ID, true, &pushed, pushed); err != nil {
		t.Fatal(err)
	}
	newRule := watchJSON("1-bed rent on Elm St", "trend", "EUR", map[string]any{"when": "below", "value": 2200})
	row, err = d.UpdateAssistantPage(ctx, AssistantPageUpdate{ID: kept.ID, Expected: row.Version, Watch: newRule, By: by, At: pagesBase.Add(6 * time.Minute)})
	if err != nil || row.Alert.On || row.Alert.Tripped || row.Alert.PushedAt == nil || !row.Alert.PushedAt.Equal(pushed) {
		t.Fatalf("changed alert rule should turn Alert.On off and preserve PushedAt: %+v %v", row, err)
	}

	// SetAssistantPageOverview toggles on_overview idempotently.
	row, changed, err = d.SetAssistantPageOverview(ctx, kept.ID, false, pagesBase.Add(7*time.Minute))
	if err != nil || !changed || row.OnOverview {
		t.Fatalf("overview off: %+v %v %v", row, changed, err)
	}
	row, changed, err = d.SetAssistantPageOverview(ctx, kept.ID, false, pagesBase.Add(8*time.Minute))
	if err != nil || changed || row.OnOverview {
		t.Fatalf("overview off again: %+v %v %v", row, changed, err)
	}

	// Draft refuses SetAssistantPageOverview.
	d2 := mustCreate(t, d, draft(3, "c1", "Draft", pagesBase))
	if _, _, err := d.SetAssistantPageOverview(ctx, d2.ID, true, pagesBase); !errors.Is(err, ErrAssistantPageNotKept) {
		t.Fatalf("overview on draft: %v", err)
	}
}

// Readings are trimmed to the latest 30 in the writing transaction, returned newest-first, and AddAssistantPageReading
// returns the updated page row and the immediately preceding reading.
func TestAssistantPageReadingsTrimmedTo30(t *testing.T) {
	d, ctx := pagesDB(t)
	p := mustKeep(t, d, mustCreate(t, d, draft(1, "c1", "Weight", pagesBase)), pagesBase)
	other := mustKeep(t, d, mustCreate(t, d, draft(2, "c2", "Other", pagesBase)), pagesBase)

	// First reading on other page to ensure trimming is scoped per page.
	if _, prev, err := d.AddAssistantPageReading(ctx, NewAssistantPageReading{
		PageID: other.ID, Reading: numReading(100), ByKind: AssistantPageByAgent, ByMachine: "m1", BySession: "c2", At: pagesBase.Add(time.Minute),
	}); err != nil || prev != nil {
		t.Fatalf("other page first reading: %+v %v", prev, err)
	}

	// Write 35 readings; verify prev chain and final count trimmed to 30.
	var prevID int64
	for i := 1; i <= 35; i++ {
		at := pagesBase.Add(time.Duration(i) * time.Minute)
		row, prev, err := d.AddAssistantPageReading(ctx, NewAssistantPageReading{
			PageID:    p.ID,
			Reading:   numReading(float64(i)),
			AlertText: fmt.Sprintf("alert %d", i),
			Note:      fmt.Sprintf("note %d", i),
			ByKind:    AssistantPageByRoutine,
			ByMachine: "m1",
			BySession: "rt-sess",
			At:        at,
		})
		if err != nil {
			t.Fatalf("reading %d: %v", i, err)
		}
		if row.ID != p.ID || !row.ContentAt.Equal(at) || len(row.View) == 0 {
			t.Fatalf("reading %d row: %+v", i, row)
		}
		if i == 1 {
			if prev != nil {
				t.Fatalf("first reading had prev: %+v", prev)
			}
		} else {
			if prev == nil || prev.ID != prevID || string(prev.Reading) != fmt.Sprintf(`{"value": %d}`, i-1) && string(prev.Reading) != fmt.Sprintf(`{"value":%d}`, i-1) {
				t.Fatalf("reading %d prev: %+v (want id %d)", i, prev, prevID)
			}
		}
		latest, err := d.AssistantPageReadings(ctx, p.ID, 1)
		if err != nil || len(latest) != 1 {
			t.Fatalf("latest after %d: %+v %v", i, latest, err)
		}
		prevID = latest[0].ID
	}

	all, err := d.AssistantPageReadings(ctx, p.ID, 100)
	if err != nil || len(all) != AssistantPageReadingsKept {
		t.Fatalf("readings count = %d, err = %v, want %d", len(all), err, AssistantPageReadingsKept)
	}
	if n := countRows(t, d, `SELECT count(*) FROM assistant_page_readings WHERE page_id = $1`, p.ID); n != AssistantPageReadingsKept {
		t.Fatalf("stored rows = %d, want %d", n, AssistantPageReadingsKept)
	}
	// Newest is 35, oldest retained is 6.
	var newest, oldest struct{ Value float64 }
	_ = json.Unmarshal(all[0].Reading, &newest)
	_ = json.Unmarshal(all[len(all)-1].Reading, &oldest)
	if newest.Value != 35 || oldest.Value != 6 || all[0].AlertText != "alert 35" || all[0].Note != "note 35" ||
		all[0].ByKind != AssistantPageByRoutine || all[0].ByMachine != "m1" || all[0].BySession != "rt-sess" {
		t.Fatalf("newest=%v oldest=%v row=%+v", newest, oldest, all[0])
	}

	// Limit respected and batch AssistantPagesReadings works across pages.
	top5, err := d.AssistantPageReadings(ctx, p.ID, 5)
	if err != nil || len(top5) != 5 || top5[0].ID != all[0].ID {
		t.Fatalf("top5: %d %v", len(top5), err)
	}
	batch, err := d.AssistantPagesReadings(ctx, []string{p.ID, other.ID}, 3)
	if err != nil || len(batch[p.ID]) != 3 || len(batch[other.ID]) != 1 {
		t.Fatalf("batch: %+v %v", batch, err)
	}
}

// Readings count against both the page's hourly write window and all pages' hourly write window, and are refused on
// paused, removed and draft pages with Phase 1's error kinds.
func TestAssistantPageReadingWindowAndRefusals(t *testing.T) {
	d, ctx := pagesDB(t)

	// Refusals: missing, draft, paused, removed.
	if _, _, err := d.AddAssistantPageReading(ctx, NewAssistantPageReading{
		PageID: "pg_ffffffffffffffff", Reading: numReading(1), ByKind: AssistantPageByAgent, At: pagesBase,
	}); !errors.Is(err, ErrAssistantPageNotFound) {
		t.Fatalf("missing page: %v", err)
	}

	draftPage := mustCreate(t, d, draft(1, "c1", "Draft Watch", pagesBase))
	if _, _, err := d.AddAssistantPageReading(ctx, NewAssistantPageReading{
		PageID: draftPage.ID, Reading: numReading(1), ByKind: AssistantPageByAgent, At: pagesBase.Add(time.Minute),
	}); !errors.Is(err, ErrAssistantPageNotKept) {
		t.Fatalf("draft page reading: %v", err)
	}

	p := mustKeep(t, d, draftPage, pagesBase.Add(time.Minute))
	// Pause by owner -> refused with ErrAssistantPagePaused.
	pausedRow, changed, err := d.PauseAssistantPage(ctx, p.ID, true, pagesBase.Add(2*time.Minute))
	if err != nil || !changed || !pausedRow.Paused || pausedRow.PausedBy != AssistantPagePausedByOwner {
		t.Fatalf("pause: %+v %v %v", pausedRow, changed, err)
	}
	if _, changed, err := d.PauseAssistantPage(ctx, p.ID, true, pagesBase.Add(2*time.Minute)); err != nil || changed {
		t.Fatalf("pause idempotent: %v %v", changed, err)
	}
	if _, _, err := d.AddAssistantPageReading(ctx, NewAssistantPageReading{
		PageID: p.ID, Reading: numReading(1), ByKind: AssistantPageByRoutine, At: pagesBase.Add(3 * time.Minute),
	}); !errors.Is(err, ErrAssistantPagePaused) {
		t.Fatalf("paused page reading: %v", err)
	}
	// Resume by owner -> readings work again.
	resumed, changed, err := d.PauseAssistantPage(ctx, p.ID, false, pagesBase.Add(4*time.Minute))
	if err != nil || !changed || resumed.Paused || resumed.PausedBy != "" {
		t.Fatalf("resume: %+v %v %v", resumed, changed, err)
	}

	// Removed page -> refused with ErrAssistantPageRemoved.
	q := mustKeep(t, d, mustCreate(t, d, draft(2, "c2", "Removed Watch", pagesBase)), pagesBase)
	if _, _, err := d.RemoveAssistantPage(ctx, q.ID, AssistantPageRemovedByOwner, pagesBase.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.AddAssistantPageReading(ctx, NewAssistantPageReading{
		PageID: q.ID, Reading: numReading(1), ByKind: AssistantPageByRoutine, At: pagesBase.Add(2 * time.Minute),
	}); !errors.Is(err, ErrAssistantPageRemoved) {
		t.Fatalf("removed page reading: %v", err)
	}

	// Bad inputs refused.
	if _, _, err := d.AddAssistantPageReading(ctx, NewAssistantPageReading{
		PageID: p.ID, Reading: numReading(1), ByKind: AssistantPageByOwner, At: pagesBase.Add(5 * time.Minute),
	}); err == nil {
		t.Fatal("owner by_kind accepted for reading")
	}
	if _, _, err := d.AddAssistantPageReading(ctx, NewAssistantPageReading{
		PageID: p.ID, Reading: json.RawMessage(`[]`), ByKind: AssistantPageByAgent, At: pagesBase.Add(5 * time.Minute),
	}); err == nil {
		t.Fatal("non-object reading accepted")
	}
	if _, _, err := d.AddAssistantPageReading(ctx, NewAssistantPageReading{
		PageID: p.ID, Reading: numReading(1), AlertText: strings.Repeat("a", AssistantPageAlertTextMost+1), ByKind: AssistantPageByAgent, At: pagesBase.Add(5 * time.Minute),
	}); err == nil {
		t.Fatal("too long alert_text accepted")
	}

	// Per-page write window: draft was write #1; 59 readings fill the 60/hour limit, 60th reading fails.
	for i := 1; i < AssistantPageWritesPerHour; i++ {
		if _, _, err := d.AddAssistantPageReading(ctx, NewAssistantPageReading{
			PageID: p.ID, Reading: numReading(float64(i)), ByKind: AssistantPageByRoutine, At: pagesBase.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("write %d: %v", i+1, err)
		}
	}
	if _, _, err := d.AddAssistantPageReading(ctx, NewAssistantPageReading{
		PageID: p.ID, Reading: numReading(999), ByKind: AssistantPageByRoutine, At: pagesBase.Add(59 * time.Minute),
	}); !errors.Is(err, ErrAssistantPageRate) {
		t.Fatalf("61st write via reading: %v", err)
	}
	// An UpdateAssistantPage also sees the same filled window.
	if _, err := d.UpdateAssistantPage(ctx, AssistantPageUpdate{
		ID: p.ID, Expected: p.Version, View: pageView("v2"), By: AssistantPageWriter{Kind: AssistantPageByAgent}, At: pagesBase.Add(59 * time.Minute),
	}); !errors.Is(err, ErrAssistantPageRate) {
		t.Fatalf("update after reading-filled window: %v", err)
	}

	// Next hour resets page window; test global 600/hour window.
	nextHour := pagesBase.Add(time.Hour)
	if err := d.PutSetting(ctx, AssistantPagesRateKey, assistantPagesRate{Start: nextHour, N: AssistantPagesWritesPerHour}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.AddAssistantPageReading(ctx, NewAssistantPageReading{
		PageID: p.ID, Reading: numReading(1), ByKind: AssistantPageByRoutine, At: nextHour.Add(time.Minute),
	}); !errors.Is(err, ErrAssistantPagesRate) {
		t.Fatalf("global rate limit on reading: %v", err)
	}
}

// Routine binding (one per page, one page per routine), data grants, refresh mark/clear, and cascade deletion
// returning dropped routine_id(s) on permanent delete and Recently removed purge.
func TestAssistantPageRoutineGrantsRefreshAndCascade(t *testing.T) {
	d, ctx := pagesDB(t)
	p1 := mustCreate(t, d, draft(1, "c1", "Rent", pagesBase))
	p2 := mustKeep(t, d, mustCreate(t, d, draft(2, "c2", "Stock", pagesBase)), pagesBase)

	// Bind works on a draft.
	bound, changed, err := d.BindAssistantPageRoutine(ctx, p1.ID, "rt_rent", pagesBase.Add(time.Minute))
	if err != nil || !changed || bound.RoutineID != "rt_rent" {
		t.Fatalf("bind draft: %+v %v %v", bound, changed, err)
	}
	// Re-binding same routine is idempotent.
	if _, changed, err := d.BindAssistantPageRoutine(ctx, p1.ID, "rt_rent", pagesBase.Add(time.Minute)); err != nil || changed {
		t.Fatalf("rebind same: %v %v", changed, err)
	}
	// Binding a second routine on the same page returns *AssistantPageRoutineBoundError naming the first.
	var already *AssistantPageRoutineBoundError
	if _, _, err := d.BindAssistantPageRoutine(ctx, p1.ID, "rt_other", pagesBase.Add(time.Minute)); !errors.As(err, &already) || already.RoutineID != "rt_rent" || !errors.Is(err, ErrAssistantPageHasRoutine) {
		t.Fatalf("second routine on same page: %v", err)
	}
	// Binding the same routine to another page fails with ErrAssistantPageRoutineTaken.
	if _, _, err := d.BindAssistantPageRoutine(ctx, p2.ID, "rt_rent", pagesBase.Add(time.Minute)); !errors.Is(err, ErrAssistantPageRoutineTaken) {
		t.Fatalf("same routine on second page: %v", err)
	}
	// AssistantPageByRoutine looks up the page.
	if byRT, err := d.AssistantPageByRoutine(ctx, "rt_rent"); err != nil || byRT.ID != p1.ID {
		t.Fatalf("by routine: %+v %v", byRT, err)
	}

	// Grants refused on draft; allowed once kept.
	if _, err := d.GrantAssistantPageData(ctx, AssistantPageGrantRow{
		PageID: p1.ID, GranteeKind: AssistantPageGrantRoutine, GranteeRef: "rt_rent", GrantedBy: "owner:phone", CreatedAt: pagesBase.Add(2 * time.Minute),
	}); !errors.Is(err, ErrAssistantPageNotKept) {
		t.Fatalf("grant on draft: %v", err)
	}
	p1 = mustKeep(t, d, p1, pagesBase.Add(2*time.Minute))
	g, err := d.GrantAssistantPageData(ctx, AssistantPageGrantRow{
		PageID: p1.ID, GranteeKind: AssistantPageGrantRoutine, GranteeRef: "rt_rent", GrantedBy: "owner:phone", CreatedAt: pagesBase.Add(2 * time.Minute),
	})
	if err != nil || g.Rights != AssistantPageRightData || g.GrantedBy != "owner:phone" {
		t.Fatalf("grant: %+v %v", g, err)
	}
	if ok, err := d.HasAssistantPageDataGrant(ctx, p1.ID, AssistantPageGrantRoutine, "rt_rent"); err != nil || !ok {
		t.Fatalf("has grant: %v %v", ok, err)
	}
	if ok, err := d.HasAssistantPageDataGrant(ctx, p1.ID, AssistantPageGrantRoutine, "rt_other"); err != nil || ok {
		t.Fatalf("unrelated grant: %v %v", ok, err)
	}
	if list, err := d.AssistantPageGrants(ctx, p1.ID); err != nil || len(list) != 1 || list[0].GranteeRef != "rt_rent" {
		t.Fatalf("grants list: %+v %v", list, err)
	}
	// Todo agent grant works too and can be revoked explicitly.
	if _, err := d.GrantAssistantPageData(ctx, AssistantPageGrantRow{
		PageID: p1.ID, GranteeKind: AssistantPageGrantTodoAgent, GranteeRef: "todo_1", GrantedBy: "owner:web", CreatedAt: pagesBase.Add(3 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if revoked, err := d.RevokeAssistantPageData(ctx, p1.ID, AssistantPageGrantTodoAgent, "todo_1"); err != nil || !revoked {
		t.Fatalf("revoke: %v %v", revoked, err)
	}
	if ok, err := d.HasAssistantPageDataGrant(ctx, p1.ID, AssistantPageGrantTodoAgent, "todo_1"); err != nil || ok {
		t.Fatalf("still granted after revoke: %v %v", ok, err)
	}

	// Unbind by routine clears both routine_id and the routine's grant.
	unbound, err := d.UnbindAssistantPageByRoutine(ctx, "rt_rent", pagesBase.Add(4*time.Minute))
	if err != nil || unbound == nil || unbound.ID != p1.ID || unbound.RoutineID != "" {
		t.Fatalf("unbind by routine: %+v %v", unbound, err)
	}
	if ok, err := d.HasAssistantPageDataGrant(ctx, p1.ID, AssistantPageGrantRoutine, "rt_rent"); err != nil || ok {
		t.Fatalf("grant remained after unbind: %v %v", ok, err)
	}
	if none, err := d.UnbindAssistantPageByRoutine(ctx, "rt_none", pagesBase.Add(4*time.Minute)); err != nil || none != nil {
		t.Fatalf("unbind unknown routine: %+v %v", none, err)
	}

	// Re-bind and test UnbindAssistantPageRoutine by page ID.
	if _, _, err := d.BindAssistantPageRoutine(ctx, p1.ID, "rt_rent_2", pagesBase.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.GrantAssistantPageData(ctx, AssistantPageGrantRow{
		PageID: p1.ID, GranteeKind: AssistantPageGrantRoutine, GranteeRef: "rt_rent_2", GrantedBy: "agent", CreatedAt: pagesBase.Add(5 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if row, oldRT, err := d.UnbindAssistantPageRoutine(ctx, p1.ID, pagesBase.Add(6*time.Minute)); err != nil || oldRT != "rt_rent_2" || row.RoutineID != "" {
		t.Fatalf("unbind by page: %+v %q %v", row, oldRT, err)
	}
	if ok, err := d.HasAssistantPageDataGrant(ctx, p1.ID, AssistantPageGrantRoutine, "rt_rent_2"); err != nil || ok {
		t.Fatalf("grant remained after unbind by page: %v %v", ok, err)
	}

	// Mark / clear manual refresh request.
	askAt := pagesBase.Add(7 * time.Minute)
	marked, err := d.MarkAssistantPageRefresh(ctx, p1.ID, "phone", askAt)
	if err != nil || marked.RefreshAskedAt == nil || !marked.RefreshAskedAt.Equal(askAt) || marked.RefreshDevice != "phone" {
		t.Fatalf("mark refresh: %+v %v", marked, err)
	}
	cleared, changed, err := d.ClearAssistantPageRefresh(ctx, p1.ID, pagesBase.Add(8*time.Minute))
	if err != nil || !changed || cleared.RefreshAskedAt != nil || cleared.RefreshDevice != "" {
		t.Fatalf("clear refresh: %+v %v %v", cleared, changed, err)
	}
	if _, changed, err := d.ClearAssistantPageRefresh(ctx, p1.ID, pagesBase.Add(9*time.Minute)); err != nil || changed {
		t.Fatalf("clear refresh twice: %v %v", changed, err)
	}

	// Cascade delete + routine_id return on DeleteAssistantPageWithRoutine.
	if _, _, err := d.BindAssistantPageRoutine(ctx, p1.ID, "rt_del", pagesBase.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.GrantAssistantPageData(ctx, AssistantPageGrantRow{
		PageID: p1.ID, GranteeKind: AssistantPageGrantRoutine, GranteeRef: "rt_del", GrantedBy: "owner", CreatedAt: pagesBase.Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.AddAssistantPageReading(ctx, NewAssistantPageReading{
		PageID: p1.ID, Reading: numReading(42), ByKind: AssistantPageByRoutine, At: pagesBase.Add(11 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.RemoveAssistantPage(ctx, p1.ID, AssistantPageRemovedByOwner, pagesBase.Add(12*time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Removing keeps routine_id so restore works; binding on a removed page is refused.
	if _, _, err := d.BindAssistantPageRoutine(ctx, p1.ID, "rt_new", pagesBase.Add(12*time.Minute)); !errors.Is(err, ErrAssistantPageRemoved) {
		t.Fatalf("bind on removed page: %v", err)
	}
	deleted, droppedRT, err := d.DeleteAssistantPageWithRoutine(ctx, p1.ID)
	if err != nil || !deleted || droppedRT != "rt_del" {
		t.Fatalf("delete with routine: deleted=%v rt=%q err=%v", deleted, droppedRT, err)
	}
	if n := countRows(t, d, `SELECT (SELECT count(*) FROM assistant_page_readings WHERE page_id = $1) + (SELECT count(*) FROM assistant_page_grants WHERE page_id = $1)`, p1.ID); n != 0 {
		t.Fatalf("%d readings/grants left after delete", n)
	}

	// Cascade delete + routine_id list on PurgeAssistantPagesWithRoutines.
	p3 := mustKeep(t, d, mustCreate(t, d, draft(3, "c3", "Plain", pagesBase)), pagesBase)
	if _, _, err := d.BindAssistantPageRoutine(ctx, p2.ID, "rt_purge", pagesBase.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.GrantAssistantPageData(ctx, AssistantPageGrantRow{
		PageID: p2.ID, GranteeKind: AssistantPageGrantRoutine, GranteeRef: "rt_purge", GrantedBy: "owner", CreatedAt: pagesBase.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.AddAssistantPageReading(ctx, NewAssistantPageReading{
		PageID: p2.ID, Reading: numReading(7), ByKind: AssistantPageByRoutine, At: pagesBase.Add(2 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	removedAt := pagesBase.Add(time.Hour)
	if _, _, err := d.RemoveAssistantPage(ctx, p2.ID, AssistantPageRemovedByOwner, removedAt); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.RemoveAssistantPage(ctx, p3.ID, AssistantPageRemovedByOwner, removedAt); err != nil {
		t.Fatal(err)
	}
	purged, routines, err := d.PurgeAssistantPagesWithRoutines(ctx, removedAt.Add(AssistantPageRemovedLife))
	if err != nil || purged != 2 || !slices.Equal(routines, []string{"rt_purge"}) {
		t.Fatalf("purge: n=%d routines=%v err=%v", purged, routines, err)
	}
	if n := countRows(t, d, `SELECT (SELECT count(*) FROM assistant_page_readings WHERE page_id = $1) + (SELECT count(*) FROM assistant_page_grants WHERE page_id = $1)`, p2.ID); n != 0 {
		t.Fatalf("%d readings/grants left after purge", n)
	}
}

// Alert state round trip: owner switch preserves tripped/pushed_at; manager state updates preserve owner switch;
// EvaluateAssistantPageAlert enforces edge-triggering, re-arming and the 12-hour cooldown atomically.
func TestAssistantPageAlertStateRoundTrip(t *testing.T) {
	d, ctx := pagesDB(t)
	p := mustKeep(t, d, mustCreate(t, d, draft(1, "c1", "Rent", pagesBase)), pagesBase)

	// Starts off, not tripped, never pushed.
	if p.Alert.On || p.Alert.Tripped || p.Alert.PushedAt != nil {
		t.Fatalf("initial alert: %+v", p.Alert)
	}

	// Tripped while off: records tripped=true, does not push.
	t0 := pagesBase.Add(time.Hour)
	row, push, err := d.EvaluateAssistantPageAlert(ctx, p.ID, true, AssistantPageAlertCooldown, t0)
	if err != nil || push || row.Alert.On || !row.Alert.Tripped || row.Alert.PushedAt != nil {
		t.Fatalf("tripped while off: %+v push=%v err=%v", row, push, err)
	}

	// Turning switch ON while already tripped preserves tripped=true; next tripped reading does NOT push.
	row, changed, err := d.SetAssistantPageAlert(ctx, p.ID, true, t0.Add(30*time.Minute))
	if err != nil || !changed || !row.Alert.On || !row.Alert.Tripped || row.Alert.PushedAt != nil {
		t.Fatalf("switch on while tripped: %+v changed=%v err=%v", row, changed, err)
	}
	row, push, err = d.EvaluateAssistantPageAlert(ctx, p.ID, true, AssistantPageAlertCooldown, t0.Add(time.Hour))
	if err != nil || push || !row.Alert.On || !row.Alert.Tripped || row.Alert.PushedAt != nil {
		t.Fatalf("still tripped after switch on: %+v push=%v err=%v", row, push, err)
	}

	// Recovery re-arms; next trip pushes and records pushed_at.
	row, push, err = d.EvaluateAssistantPageAlert(ctx, p.ID, false, AssistantPageAlertCooldown, t0.Add(2*time.Hour))
	if err != nil || push || !row.Alert.On || row.Alert.Tripped {
		t.Fatalf("recovery: %+v push=%v err=%v", row, push, err)
	}
	tPush := t0.Add(3 * time.Hour)
	row, push, err = d.EvaluateAssistantPageAlert(ctx, p.ID, true, AssistantPageAlertCooldown, tPush)
	if err != nil || !push || !row.Alert.On || !row.Alert.Tripped || row.Alert.PushedAt == nil || !row.Alert.PushedAt.Equal(tPush) {
		t.Fatalf("first push: %+v push=%v err=%v", row, push, err)
	}

	// Recover and re-trip within 12h cooldown -> no push, preserves pushed_at.
	if _, _, err := d.EvaluateAssistantPageAlert(ctx, p.ID, false, AssistantPageAlertCooldown, tPush.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	row, push, err = d.EvaluateAssistantPageAlert(ctx, p.ID, true, AssistantPageAlertCooldown, tPush.Add(2*time.Hour))
	if err != nil || push || !row.Alert.Tripped || !row.Alert.PushedAt.Equal(tPush) {
		t.Fatalf("within 12h cooldown: %+v push=%v err=%v", row, push, err)
	}

	// Recover and re-trip after 12h cooldown -> pushes again and advances pushed_at.
	if _, _, err := d.EvaluateAssistantPageAlert(ctx, p.ID, false, AssistantPageAlertCooldown, tPush.Add(11*time.Hour)); err != nil {
		t.Fatal(err)
	}
	tPush2 := tPush.Add(12 * time.Hour)
	row, push, err = d.EvaluateAssistantPageAlert(ctx, p.ID, true, AssistantPageAlertCooldown, tPush2)
	if err != nil || !push || !row.Alert.PushedAt.Equal(tPush2) {
		t.Fatalf("after 12h cooldown: %+v push=%v err=%v", row, push, err)
	}

	// Direct RecordAssistantPageAlertState preserves Alert.On and preserves PushedAt when pushedAt is nil.
	row, err = d.RecordAssistantPageAlertState(ctx, p.ID, false, nil, tPush2.Add(time.Minute))
	if err != nil || !row.Alert.On || row.Alert.Tripped || row.Alert.PushedAt == nil || !row.Alert.PushedAt.Equal(tPush2) {
		t.Fatalf("record state nil pushedAt: %+v %v", row, err)
	}
	// Reload from DB (both single fetch and list) to confirm round trip.
	got, err := d.AssistantPage(ctx, p.ID)
	if err != nil || !got.Alert.On || got.Alert.Tripped || got.Alert.PushedAt == nil || !got.Alert.PushedAt.Equal(tPush2) {
		t.Fatalf("reload single: %+v %v", got, err)
	}
	list, err := d.AssistantPages(ctx, AssistantPageQuery{Status: []string{AssistantPageActive}})
	if err != nil || len(list) != 1 || !list[0].Alert.On || list[0].Alert.PushedAt == nil || !list[0].Alert.PushedAt.Equal(tPush2) {
		t.Fatalf("reload list: %+v %v", list, err)
	}
}
