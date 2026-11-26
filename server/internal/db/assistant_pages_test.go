package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// pagesDB is a database without pages.
func pagesDB(t *testing.T) (*DB, context.Context) {
	t.Helper()
	d, ctx := open(t), context.Background()
	if _, err := d.pool.Exec(ctx, `TRUNCATE assistant_pages CASCADE`); err != nil {
		t.Fatal(err)
	}
	return d, ctx
}

var pagesBase = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

// draft is a new page of the main conversation drafted by conversation at at, offered on card "view-<id>".
func draft(n int, conversation, title string, at time.Time) NewAssistantPage {
	id := fmt.Sprintf("pg_%016x", n)
	return NewAssistantPage{ID: id, Title: title, OwnerKind: AssistantPageOwnerMain, CreatedMachine: "m1",
		CreatedSession: conversation, OfferView: "view-" + id, Why: "the owner asked", View: pageView("v1"), At: at}
}

func pageView(text string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"fallback":%q,"sections":[{"items":[{"type":"text","text":%q}]}]}`, text, text))
}

func mustCreate(t *testing.T, d *DB, p NewAssistantPage) *AssistantPageRow {
	t.Helper()
	row, err := d.CreateAssistantPage(context.Background(), p)
	if err != nil {
		t.Fatalf("create %s: %v", p.ID, err)
	}
	return row
}

func mustKeep(t *testing.T, d *DB, p *AssistantPageRow, at time.Time) *AssistantPageRow {
	t.Helper()
	row, changed, err := d.KeepAssistantPage(context.Background(), AssistantPageKeep{OfferView: p.OfferView, Device: "phone", At: at})
	if err != nil || !changed {
		t.Fatalf("keep %s: %v %v", p.ID, changed, err)
	}
	return row
}

func TestAssistantPageCreateDraft(t *testing.T) {
	d, ctx := pagesDB(t)
	p := mustCreate(t, d, draft(1, "c1", "Running", pagesBase))
	if p.Status != AssistantPageDraft || p.Version != 1 || p.Icon != "chart" || p.WritesWindowN != 1 || !p.WritesWindowStart.Equal(pagesBase) ||
		string(p.View) == "" || p.KeptAt != nil || p.Pinned || p.RemovedAt != nil {
		t.Fatalf("draft %+v", p)
	}
	got, err := d.AssistantPageByOffer(ctx, p.OfferView)
	if err != nil || got.ID != p.ID || len(got.View) == 0 {
		t.Fatalf("by offer %+v %v", got, err)
	}
	versions, err := d.AssistantPageVersions(ctx, p.ID)
	if err != nil || len(versions) != 1 || versions[0].Version != 1 || versions[0].By.Kind != AssistantPageByAgent ||
		versions[0].By.Session != "c1" || versions[0].Title != "Running" {
		t.Fatalf("versions %+v %v", versions, err)
	}
	bad := []NewAssistantPage{
		func() NewAssistantPage { b := draft(2, "c1", "x", pagesBase); b.ID = "pg_short"; return b }(),
		func() NewAssistantPage { b := draft(2, "c1", "", pagesBase); return b }(),
		func() NewAssistantPage { b := draft(2, "c1", " padded", pagesBase); return b }(),
		func() NewAssistantPage { b := draft(2, "c1", "x", pagesBase); b.View = json.RawMessage(`[]`); return b }(),
		func() NewAssistantPage { b := draft(2, "c1", "x", pagesBase); b.OwnerRef = "c1"; return b }(),
		func() NewAssistantPage {
			b := draft(2, "c1", "x", pagesBase)
			b.OwnerKind = AssistantPageOwnerConversation
			return b
		}(),
		func() NewAssistantPage { b := draft(2, "", "x", pagesBase); return b }(),
	}
	for i, b := range bad {
		if _, err := d.CreateAssistantPage(ctx, b); err == nil {
			t.Errorf("bad draft %d stored", i)
		}
	}
	if _, err := d.CreateAssistantPage(ctx, draft(1, "c2", "Again", pagesBase)); err == nil {
		t.Fatal("an id stored twice")
	}
	if _, err := d.CreateAssistantPage(ctx, func() NewAssistantPage { b := draft(3, "c2", "Other", pagesBase); b.OfferView = p.OfferView; return b }()); err == nil {
		t.Fatal("a card offering two pages")
	}
	if _, err := d.AssistantPage(ctx, "pg_ffffffffffffffff"); !errors.Is(err, ErrAssistantPageNotFound) {
		t.Fatal(err)
	}
}

// Drafts: 3 a conversation, 5 in any 24 hours (declined ones count), 10 waiting in all; and no new draft while 20 pages
// are kept.
func TestAssistantPageDraftLimits(t *testing.T) {
	d, ctx := pagesDB(t)
	n := 0
	next := func(conversation, title string, at time.Time) NewAssistantPage {
		n++
		return draft(n, conversation, title, at)
	}
	for i := range AssistantPageDraftsPerConversation {
		mustCreate(t, d, next("c1", fmt.Sprintf("Page %d", i), pagesBase))
	}
	if _, err := d.CreateAssistantPage(ctx, next("c1", "One more", pagesBase)); !errors.Is(err, ErrAssistantPageConversationDrafts) {
		t.Fatalf("a fourth draft in one conversation: %v", err)
	}
	declined := mustCreate(t, d, next("c2", "Declined", pagesBase))
	if _, changed, err := d.DeclineAssistantPage(ctx, declined.OfferView, pagesBase); err != nil || !changed {
		t.Fatal(changed, err)
	}
	mustCreate(t, d, next("c2", "Fifth", pagesBase))
	if _, err := d.CreateAssistantPage(ctx, next("c3", "Sixth", pagesBase.Add(23*time.Hour))); !errors.Is(err, ErrAssistantPageDailyDrafts) {
		t.Fatalf("a sixth draft in a day (a declined one counts): %v", err)
	}
	// A day later the window has room again, up to 10 drafts waiting.
	day2 := pagesBase.Add(24 * time.Hour)
	for i := range 5 {
		mustCreate(t, d, next(fmt.Sprintf("d%d", i), fmt.Sprintf("Day two %d", i), day2))
	}
	waiting, err := d.AssistantPages(ctx, AssistantPageQuery{Status: []string{AssistantPageDraft}})
	if err != nil || len(waiting) != 9 {
		t.Fatalf("waiting %d %v", len(waiting), err)
	}
	day3 := day2.Add(24 * time.Hour)
	mustCreate(t, d, next("e1", "Tenth", day3))
	if _, err := d.CreateAssistantPage(ctx, next("e2", "Eleventh", day3)); !errors.Is(err, ErrAssistantPageDrafts) {
		t.Fatalf("an eleventh draft waiting: %v", err)
	}

	// 20 kept pages: no more drafts, keeps or restores.
	d, ctx = pagesDB(t)
	var kept []*AssistantPageRow
	for i := range AssistantPagesKeptMost {
		at := pagesBase.Add(time.Duration(i) * 6 * time.Hour)
		kept = append(kept, mustKeep(t, d, mustCreate(t, d, next("k", fmt.Sprintf("Kept %d", i), at)), at))
	}
	late := pagesBase.Add(30 * 24 * time.Hour)
	if _, err := d.CreateAssistantPage(ctx, next("k", "Twenty-first", late)); !errors.Is(err, ErrAssistantPageKeptLimit) {
		t.Fatalf("a draft with 20 pages kept: %v", err)
	}
	if _, _, err := d.RemoveAssistantPage(ctx, kept[0].ID, AssistantPageRemovedByOwner, late); err != nil {
		t.Fatal(err)
	}
	extra := mustCreate(t, d, next("k", "Twenty-first", late))
	mustKeep(t, d, extra, late)
	if _, _, err := d.RestoreAssistantPage(ctx, kept[0].ID, late); !errors.Is(err, ErrAssistantPageKeptLimit) {
		t.Fatalf("a restore with 20 pages kept: %v", err)
	}
	if _, _, err := d.RemoveAssistantPage(ctx, extra.ID, AssistantPageRemovedByAgent, late); err != nil {
		t.Fatal(err)
	}
	another := mustCreate(t, d, next("k", "Twenty-second", late))
	if _, _, err := d.RestoreAssistantPage(ctx, kept[0].ID, late); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.KeepAssistantPage(ctx, AssistantPageKeep{OfferView: another.OfferView, At: late}); !errors.Is(err, ErrAssistantPageKeptLimit) {
		t.Fatalf("a keep with 20 pages kept: %v", err)
	}
}

// The limits hold for drafts created at once: the advisory lock orders them.
func TestAssistantPageDraftLimitConcurrent(t *testing.T) {
	d, ctx := pagesDB(t)
	var made, refused atomic.Int32
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := d.CreateAssistantPage(ctx, draft(i+1, "c1", fmt.Sprintf("Page %d", i), pagesBase))
			switch {
			case err == nil:
				made.Add(1)
			case errors.Is(err, ErrAssistantPageConversationDrafts):
				refused.Add(1)
			default:
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if made.Load() != AssistantPageDraftsPerConversation || refused.Load() != 8-AssistantPageDraftsPerConversation {
		t.Fatalf("made %d, refused %d", made.Load(), refused.Load())
	}
}

func TestAssistantPageUpdateVersions(t *testing.T) {
	d, ctx := pagesDB(t)
	p := mustCreate(t, d, draft(1, "c1", "Running", pagesBase))
	by := AssistantPageWriter{Kind: AssistantPageByAgent, Machine: "m1", Session: "c1", Note: "more"}
	update := func(expected int64, view json.RawMessage, at time.Time) (*AssistantPageRow, error) {
		return d.UpdateAssistantPage(ctx, AssistantPageUpdate{ID: p.ID, Expected: expected, View: view, By: by, At: at})
	}
	if _, err := update(2, pageView("stale"), pagesBase); !errors.Is(err, ErrAssistantPageVersion) {
		t.Fatalf("a wrong version: %v", err)
	}
	if _, err := d.UpdateAssistantPage(ctx, AssistantPageUpdate{ID: "pg_ffffffffffffffff", Expected: 1, View: pageView("x"), By: by, At: pagesBase}); !errors.Is(err, ErrAssistantPageNotFound) {
		t.Fatalf("a missing page: %v", err)
	}
	if _, err := d.UpdateAssistantPage(ctx, AssistantPageUpdate{ID: p.ID, Expected: 1, View: pageView("x"), By: AssistantPageWriter{Kind: "someone"}, At: pagesBase}); err == nil {
		t.Fatal("an unknown writer")
	}
	// Only one of two writers of the same version wins.
	var won, lost atomic.Int32
	var wg sync.WaitGroup
	for i := range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := update(1, pageView(fmt.Sprintf("racer %d", i)), pagesBase.Add(time.Second))
			switch {
			case err == nil:
				won.Add(1)
			case errors.Is(err, ErrAssistantPageVersion):
				lost.Add(1)
			default:
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if won.Load() != 1 || lost.Load() != 5 {
		t.Fatalf("won %d, lost %d", won.Load(), lost.Load())
	}
	// A draft's title and icon change with its contents.
	row, err := d.UpdateAssistantPage(ctx, AssistantPageUpdate{ID: p.ID, Expected: 2, View: pageView("v3"), Title: "Runs", Icon: "heart", By: by, At: pagesBase.Add(2 * time.Second)})
	if err != nil || row.Version != 3 || row.Title != "Runs" || row.Icon != "heart" || !row.ContentAt.Equal(pagesBase.Add(2*time.Second)) || row.WritesWindowN != 3 {
		t.Fatalf("%+v %v", row, err)
	}
	for v := int64(3); v < 30; v++ {
		if row, err = update(v, pageView(fmt.Sprintf("v%d", v+1)), pagesBase.Add(time.Duration(v)*time.Second)); err != nil {
			t.Fatal(v, err)
		}
	}
	versions, err := d.AssistantPageVersions(ctx, p.ID)
	if err != nil || len(versions) != AssistantPageVersionsKept || versions[0].Version != 30 || versions[len(versions)-1].Version != 11 {
		t.Fatalf("versions kept: %d %v", len(versions), err)
	}
	if string(versions[0].View) != string(row.View) || versions[0].By != by || versions[0].Title != "Runs" {
		t.Fatalf("latest version %+v", versions[0])
	}
	got, err := d.AssistantPage(ctx, p.ID)
	if err != nil || got.Version != 30 {
		t.Fatalf("%+v %v", got, err)
	}
	var stored map[string]any
	if err := json.Unmarshal(got.View, &stored); err != nil || stored["fallback"] != "v30" {
		t.Fatalf("view %s %v", got.View, err)
	}

	// Kept: the title and icon are the owner's; paused: no Agent writes; removed: no writes.
	mustKeep(t, d, got, pagesBase.Add(time.Minute))
	if _, err := d.UpdateAssistantPage(ctx, AssistantPageUpdate{ID: p.ID, Expected: 30, View: pageView("x"), Title: "Other", By: by, At: pagesBase.Add(time.Minute)}); !errors.Is(err, ErrAssistantPageKept) {
		t.Fatalf("an Agent renaming a kept page: %v", err)
	}
	if _, err := d.UpdateAssistantPage(ctx, AssistantPageUpdate{ID: p.ID, Expected: 30, View: pageView("x"), Icon: "star", By: by, At: pagesBase.Add(time.Minute)}); !errors.Is(err, ErrAssistantPageKept) {
		t.Fatalf("an Agent changing a kept page's icon: %v", err)
	}
	if row, err = d.UpdateAssistantPage(ctx, AssistantPageUpdate{ID: p.ID, Expected: 30, View: pageView("v31"), Title: "Runs", Icon: "heart", By: by, At: pagesBase.Add(time.Minute)}); err != nil {
		t.Fatalf("the same title and icon: %v", err)
	}
	if _, err := d.pool.Exec(ctx, `UPDATE assistant_pages SET paused = true, paused_by = 'owner' WHERE id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := update(31, pageView("x"), pagesBase.Add(time.Minute)); !errors.Is(err, ErrAssistantPagePaused) {
		t.Fatalf("a paused page: %v", err)
	}
	owner := AssistantPageWriter{Kind: AssistantPageByRestore, Device: "phone"}
	if _, err := d.UpdateAssistantPage(ctx, AssistantPageUpdate{ID: p.ID, Expected: 31, View: pageView("restored"), By: owner, At: pagesBase.Add(time.Minute)}); err != nil {
		t.Fatalf("a restore of a paused page: %v", err)
	}
	if _, _, err := d.RemoveAssistantPage(ctx, p.ID, AssistantPageRemovedByAgent, pagesBase.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.UpdateAssistantPage(ctx, AssistantPageUpdate{ID: p.ID, Expected: 32, View: pageView("x"), By: owner, At: pagesBase.Add(time.Minute)}); !errors.Is(err, ErrAssistantPageRemoved) {
		t.Fatalf("a removed page: %v", err)
	}
}

// A page is written at most 60 times an hour, counted on its row: the draft is the first.
func TestAssistantPageWriteWindow(t *testing.T) {
	d, ctx := pagesDB(t)
	p := mustCreate(t, d, draft(1, "c1", "Running", pagesBase))
	other := mustCreate(t, d, draft(2, "c2", "Reading", pagesBase))
	by := AssistantPageWriter{Kind: AssistantPageByAgent}
	version := int64(1)
	write := func(at time.Time) error {
		row, err := d.UpdateAssistantPage(ctx, AssistantPageUpdate{ID: p.ID, Expected: version, View: pageView(fmt.Sprint(version)), By: by, At: at})
		if err == nil {
			version = row.Version
		}
		return err
	}
	for i := 1; i < AssistantPageWritesPerHour; i++ {
		if err := write(pagesBase.Add(time.Duration(i) * time.Second)); err != nil {
			t.Fatal(i, err)
		}
	}
	if err := write(pagesBase.Add(59 * time.Minute)); !errors.Is(err, ErrAssistantPageRate) {
		t.Fatalf("the 61st write in an hour: %v", err)
	}
	if _, err := d.UpdateAssistantPage(ctx, AssistantPageUpdate{ID: other.ID, Expected: 1, View: pageView("x"), By: by, At: pagesBase.Add(59 * time.Minute)}); err != nil {
		t.Fatalf("another page has its own window: %v", err)
	}
	if err := write(pagesBase.Add(time.Hour)); err != nil {
		t.Fatalf("a new hour: %v", err)
	}
	got, err := d.AssistantPage(ctx, p.ID)
	if err != nil || got.WritesWindowN != 1 || !got.WritesWindowStart.Equal(pagesBase.Add(time.Hour)) {
		t.Fatalf("window %+v %v", got, err)
	}
}

// All pages together are written at most 600 times an hour, counted in the setting assistant.pages_rate.
func TestAssistantPagesWriteWindow(t *testing.T) {
	d, ctx := pagesDB(t)
	p := mustCreate(t, d, draft(1, "c1", "Running", pagesBase))
	var rate assistantPagesRate
	if ok, err := d.Setting(ctx, AssistantPagesRateKey, &rate); err != nil || !ok || rate.N != 1 || !rate.Start.Equal(pagesBase) {
		t.Fatalf("the draft's write: %+v %v %v", rate, ok, err)
	}
	if err := d.PutSetting(ctx, AssistantPagesRateKey, assistantPagesRate{Start: pagesBase, N: AssistantPagesWritesPerHour - 1}); err != nil {
		t.Fatal(err)
	}
	by := AssistantPageWriter{Kind: AssistantPageByAgent}
	if _, err := d.UpdateAssistantPage(ctx, AssistantPageUpdate{ID: p.ID, Expected: 1, View: pageView("600th"), By: by, At: pagesBase.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.UpdateAssistantPage(ctx, AssistantPageUpdate{ID: p.ID, Expected: 2, View: pageView("601st"), By: by, At: pagesBase.Add(2 * time.Minute)}); !errors.Is(err, ErrAssistantPagesRate) {
		t.Fatalf("the 601st write of all pages: %v", err)
	}
	if _, err := d.CreateAssistantPage(ctx, draft(2, "c2", "Reading", pagesBase.Add(2*time.Minute))); !errors.Is(err, ErrAssistantPagesRate) {
		t.Fatalf("a draft past all pages' window: %v", err)
	}
	// The refused write left the page's own window alone.
	got, err := d.AssistantPage(ctx, p.ID)
	if err != nil || got.WritesWindowN != 2 || got.Version != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := d.UpdateAssistantPage(ctx, AssistantPageUpdate{ID: p.ID, Expected: 2, View: pageView("next hour"), By: by, At: pagesBase.Add(time.Hour)}); err != nil {
		t.Fatalf("a new hour: %v", err)
	}
	// A second handle on the database (another process, a restart) sees the same window.
	again, err := Open(ctx, d.pool.Config().ConnString(), AllSets)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if ok, err := again.Setting(ctx, AssistantPagesRateKey, &rate); err != nil || !ok || rate.N != 1 || !rate.Start.Equal(pagesBase.Add(time.Hour)) {
		t.Fatalf("window after the hour: %+v %v %v", rate, ok, err)
	}
}

func TestAssistantPageKeep(t *testing.T) {
	d, ctx := pagesDB(t)
	p := mustCreate(t, d, draft(1, "c1", "Running", pagesBase))
	var changes atomic.Int32
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			row, changed, err := d.KeepAssistantPage(ctx, AssistantPageKeep{OfferView: p.OfferView, Device: "phone", At: pagesBase.Add(time.Minute)})
			if err != nil || row.Status != AssistantPageActive {
				t.Error(row, err)
			}
			if changed {
				changes.Add(1)
			}
		}()
	}
	wg.Wait()
	if changes.Load() != 1 {
		t.Fatalf("kept %d times", changes.Load())
	}
	got, err := d.AssistantPage(ctx, p.ID)
	if err != nil || got.KeptDevice != "phone" || got.KeptAt == nil || !got.KeptAt.Equal(pagesBase.Add(time.Minute)) || got.Pinned || got.Version != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	if _, _, err := d.KeepAssistantPage(ctx, AssistantPageKeep{OfferView: "view-none", At: pagesBase}); !errors.Is(err, ErrAssistantPageNotFound) {
		t.Fatal(err)
	}
	if _, _, err := d.DeclineAssistantPage(ctx, p.OfferView, pagesBase); !errors.Is(err, ErrAssistantPageNotDraft) {
		t.Fatalf("declining a kept page: %v", err)
	}
	no := mustCreate(t, d, draft(2, "c1", "Reading", pagesBase))
	if row, changed, err := d.DeclineAssistantPage(ctx, no.OfferView, pagesBase.Add(time.Minute)); err != nil || !changed || row.RemovedBy != AssistantPageRemovedByDeclined || row.Listed() {
		t.Fatalf("%+v %v %v", row, changed, err)
	}
	if _, changed, err := d.DeclineAssistantPage(ctx, no.OfferView, pagesBase.Add(time.Minute)); err != nil || changed {
		t.Fatalf("declined twice: %v %v", changed, err)
	}
	if _, _, err := d.KeepAssistantPage(ctx, AssistantPageKeep{OfferView: no.OfferView, At: pagesBase}); !errors.Is(err, ErrAssistantPageRemoved) {
		t.Fatalf("keeping a declined draft: %v", err)
	}
	// Keep and pin: pinned while fewer than 3 are, else kept without.
	for i := range 4 {
		q := mustCreate(t, d, draft(10+i, fmt.Sprintf("p%d", i), fmt.Sprintf("Pinned %d", i), pagesBase.Add(48*time.Hour)))
		row, changed, err := d.KeepAssistantPage(ctx, AssistantPageKeep{OfferView: q.OfferView, Pin: true, At: pagesBase.Add(48 * time.Hour)})
		if err != nil || !changed || row.Pinned != (i < AssistantPagesPinnedMost) || row.Status != AssistantPageActive {
			t.Fatalf("keep and pin %d: %+v %v", i, row, err)
		}
	}
}

func TestAssistantPagePin(t *testing.T) {
	d, ctx := pagesDB(t)
	var kept []*AssistantPageRow
	for i := range 5 {
		at := pagesBase.Add(time.Duration(i) * 24 * time.Hour)
		kept = append(kept, mustKeep(t, d, mustCreate(t, d, draft(i+1, "c1", fmt.Sprintf("Page %d", i), at)), at))
	}
	at := pagesBase.Add(10 * 24 * time.Hour)
	for i, p := range []*AssistantPageRow{kept[3], kept[0], kept[4]} {
		row, changed, err := d.PinAssistantPage(ctx, p.ID, true, at.Add(time.Duration(i)*time.Minute))
		if err != nil || !changed || !row.Pinned || row.PinnedAt == nil {
			t.Fatalf("pin %d: %+v %v", i, row, err)
		}
	}
	if _, _, err := d.PinAssistantPage(ctx, kept[1].ID, true, at); !errors.Is(err, ErrAssistantPagePinLimit) {
		t.Fatalf("a fourth pin: %v", err)
	}
	if _, changed, err := d.PinAssistantPage(ctx, kept[0].ID, true, at); err != nil || changed {
		t.Fatalf("pinned twice: %v %v", changed, err)
	}
	list, err := d.AssistantPages(ctx, AssistantPageQuery{Status: []string{AssistantPageActive}})
	if err != nil || len(list) != 5 {
		t.Fatal(len(list), err)
	}
	// pinned first in the order they were pinned, then the latest written first
	want := []string{kept[3].ID, kept[0].ID, kept[4].ID, kept[2].ID, kept[1].ID}
	for i, p := range list {
		if p.ID != want[i] || p.View != nil {
			t.Fatalf("order %d: %s, want %s", i, p.ID, want[i])
		}
	}
	if row, changed, err := d.PinAssistantPage(ctx, kept[0].ID, false, at); err != nil || !changed || row.Pinned || row.PinnedAt != nil {
		t.Fatalf("unpin: %+v %v", row, err)
	}
	if _, _, err := d.PinAssistantPage(ctx, kept[1].ID, true, at); err != nil {
		t.Fatalf("a pin after an unpin: %v", err)
	}
	draftPage := mustCreate(t, d, draft(9, "c2", "Draft", at))
	if _, _, err := d.PinAssistantPage(ctx, draftPage.ID, true, at); !errors.Is(err, ErrAssistantPageNotKept) {
		t.Fatalf("pinning a draft: %v", err)
	}
	// A removed page is unpinned, and comes back unpinned.
	if row, _, err := d.RemoveAssistantPage(ctx, kept[3].ID, AssistantPageRemovedByOwner, at); err != nil || row.Pinned {
		t.Fatalf("%+v %v", row, err)
	}
	if _, _, err := d.PinAssistantPage(ctx, kept[3].ID, true, at); !errors.Is(err, ErrAssistantPageRemoved) {
		t.Fatalf("pinning a removed page: %v", err)
	}
	if row, _, err := d.RestoreAssistantPage(ctx, kept[3].ID, at); err != nil || row.Pinned {
		t.Fatalf("%+v %v", row, err)
	}
	if _, _, err := d.PinAssistantPage(ctx, kept[3].ID, true, at); err != nil {
		t.Fatalf("three pinned again: %v", err)
	}
}

// Kept pages' titles are unique after NFKC and case folding.
func TestAssistantPageTitles(t *testing.T) {
	d, ctx := pagesDB(t)
	run := mustKeep(t, d, mustCreate(t, d, draft(1, "c1", "Running", pagesBase)), pagesBase)
	var taken *AssistantPageTitleTakenError
	for i, title := range []string{"running", "ＲＵＮＮＩＮＧ", "RUNNING", "Ｒunning"} {
		_, err := d.CreateAssistantPage(ctx, draft(10+i, "c2", title, pagesBase))
		if !errors.As(err, &taken) || taken.ID != run.ID || !errors.Is(err, ErrAssistantPageTitleTaken) {
			t.Fatalf("%q: %v", title, err)
		}
	}
	// Drafts may share a title; the second to be kept may not.
	a := mustCreate(t, d, draft(2, "c2", "Reading", pagesBase))
	b := mustCreate(t, d, draft(3, "c3", "READING", pagesBase))
	mustKeep(t, d, a, pagesBase)
	if _, _, err := d.KeepAssistantPage(ctx, AssistantPageKeep{OfferView: b.OfferView, At: pagesBase}); !errors.As(err, &taken) || taken.ID != a.ID {
		t.Fatalf("keeping a second Reading: %v", err)
	}
	if _, _, err := d.RenameAssistantPage(ctx, a.ID, "Ｒunning", "phone", pagesBase); !errors.As(err, &taken) || taken.ID != run.ID {
		t.Fatalf("renaming onto Running: %v", err)
	}
	row, changed, err := d.RenameAssistantPage(ctx, a.ID, "reading", "phone", pagesBase.Add(time.Minute))
	if err != nil || !changed || row.Title != "reading" || row.Version != 2 || row.View != nil {
		t.Fatalf("a page renamed in another case: %+v %v", row, err)
	}
	versions, err := d.AssistantPageVersions(ctx, a.ID)
	if err != nil || len(versions) != 2 || versions[0].By.Kind != AssistantPageByOwner || versions[0].By.Device != "phone" || versions[0].Title != "reading" ||
		string(versions[0].View) != string(versions[1].View) {
		t.Fatalf("rename version %+v %v", versions, err)
	}
	if _, changed, err := d.RenameAssistantPage(ctx, a.ID, "reading", "phone", pagesBase); err != nil || changed {
		t.Fatalf("the same title: %v %v", changed, err)
	}
	if _, _, err := d.RenameAssistantPage(ctx, a.ID, "", "phone", pagesBase); err == nil {
		t.Fatal("an empty title")
	}
	if _, _, err := d.RenameAssistantPage(ctx, b.ID, "Books", "phone", pagesBase); !errors.Is(err, ErrAssistantPageNotKept) {
		t.Fatalf("renaming a draft: %v", err)
	}
	// A removed page's title is free; restoring it while another page has it is refused.
	if _, _, err := d.RemoveAssistantPage(ctx, run.ID, AssistantPageRemovedByOwner, pagesBase); err != nil {
		t.Fatal(err)
	}
	again := mustKeep(t, d, mustCreate(t, d, draft(4, "c4", "running", pagesBase.Add(48*time.Hour))), pagesBase.Add(48*time.Hour))
	if _, _, err := d.RestoreAssistantPage(ctx, run.ID, pagesBase.Add(48*time.Hour)); !errors.As(err, &taken) || taken.ID != again.ID {
		t.Fatalf("restoring onto a kept title: %v", err)
	}
	if AssistantPageTitleKey(" Ｒｕｎ ") != AssistantPageTitleKey("run") || AssistantPageTitleKey("Straße") != AssistantPageTitleKey("STRASSE") {
		t.Fatal("title keys")
	}
}

func TestAssistantPageRemoveRestoreDelete(t *testing.T) {
	d, ctx := pagesDB(t)
	p := mustKeep(t, d, mustCreate(t, d, draft(1, "c1", "Running", pagesBase)), pagesBase)
	if _, _, err := d.RemoveAssistantPage(ctx, p.ID, "someone", pagesBase); err == nil {
		t.Fatal("an unknown remover")
	}
	if _, err := d.DeleteAssistantPage(ctx, p.ID); !errors.Is(err, ErrAssistantPageNotGone) {
		t.Fatalf("deleting a kept page: %v", err)
	}
	removedAt := pagesBase.Add(time.Hour)
	row, changed, err := d.RemoveAssistantPage(ctx, p.ID, AssistantPageRemovedByOwner, removedAt)
	if err != nil || !changed || row.Status != AssistantPageRemoved || row.RemovedBy != AssistantPageRemovedByOwner || !row.Listed() {
		t.Fatalf("%+v %v", row, err)
	}
	if _, changed, err := d.RemoveAssistantPage(ctx, p.ID, AssistantPageRemovedByAgent, removedAt); err != nil || changed {
		t.Fatalf("removed twice: %v %v", changed, err)
	}
	listed, err := d.RecentlyRemovedAssistantPages(ctx, removedAt)
	if err != nil || len(listed) != 1 || listed[0].ID != p.ID {
		t.Fatalf("recently removed %+v %v", listed, err)
	}
	if row, changed, err := d.RestoreAssistantPage(ctx, p.ID, removedAt.Add(time.Hour)); err != nil || !changed || row.Status != AssistantPageActive ||
		row.RemovedAt != nil || row.RemovedBy != "" || row.OwnerKind != AssistantPageOwnerMain {
		t.Fatalf("restore %+v %v", row, err)
	}
	if _, changed, err := d.RestoreAssistantPage(ctx, p.ID, removedAt); err != nil || changed {
		t.Fatalf("restored twice: %v %v", changed, err)
	}
	if listed, err := d.RecentlyRemovedAssistantPages(ctx, removedAt); err != nil || len(listed) != 0 {
		t.Fatalf("%+v %v", listed, err)
	}
	// After 30 days a removed page cannot come back, and is purged.
	if _, _, err := d.RemoveAssistantPage(ctx, p.ID, AssistantPageRemovedByAgent, removedAt); err != nil {
		t.Fatal(err)
	}
	month := removedAt.Add(AssistantPageRemovedLife)
	if listed, err := d.RecentlyRemovedAssistantPages(ctx, month); err != nil || len(listed) != 0 {
		t.Fatalf("listed after 30 days: %+v %v", listed, err)
	}
	if _, _, err := d.RestoreAssistantPage(ctx, p.ID, month); !errors.Is(err, ErrAssistantPageNotFound) {
		t.Fatalf("restoring after 30 days: %v", err)
	}
	// Permanent delete takes the versions and taps along.
	q := mustKeep(t, d, mustCreate(t, d, draft(2, "c1", "Reading", pagesBase)), pagesBase)
	tap := pageTap(q, 1, "done", nil, pagesBase.Add(time.Minute))
	if _, _, err := d.BeginAssistantPageAction(ctx, tap); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.RemoveAssistantPage(ctx, q.ID, AssistantPageRemovedByOwner, pagesBase.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if deleted, err := d.DeleteAssistantPage(ctx, q.ID); err != nil || !deleted {
		t.Fatal(deleted, err)
	}
	if deleted, err := d.DeleteAssistantPage(ctx, q.ID); err != nil || deleted {
		t.Fatalf("deleted twice: %v %v", deleted, err)
	}
	if n := countRows(t, d, `SELECT (SELECT count(*) FROM assistant_page_versions WHERE page_id = $1) + (SELECT count(*) FROM assistant_page_actions WHERE page_id = $1)`, q.ID); n != 0 {
		t.Fatalf("%d versions and taps left", n)
	}
	// A draft is declined, not removed.
	r := mustCreate(t, d, draft(3, "c1", "Draft", pagesBase))
	if _, _, err := d.RemoveAssistantPage(ctx, r.ID, AssistantPageRemovedByAgent, pagesBase); !errors.Is(err, ErrAssistantPageNotKept) {
		t.Fatalf("removing a draft: %v", err)
	}
	if _, _, err := d.RestoreAssistantPage(ctx, r.ID, pagesBase); !errors.Is(err, ErrAssistantPageNotFound) {
		t.Fatalf("restoring a draft: %v", err)
	}
	// Purge: pages removed 30 days ago or more go, with what refers to them.
	if n, err := d.PurgeAssistantPages(ctx, month.Add(-time.Second)); err != nil || n != 0 {
		t.Fatalf("purged early: %d %v", n, err)
	}
	if n, err := d.PurgeAssistantPages(ctx, month); err != nil || n != 1 {
		t.Fatalf("purged %d %v", n, err)
	}
	if _, err := d.AssistantPage(ctx, p.ID); !errors.Is(err, ErrAssistantPageNotFound) {
		t.Fatal(err)
	}
	if n := countRows(t, d, `SELECT count(*) FROM assistant_page_versions WHERE page_id = $1`, p.ID); n != 0 {
		t.Fatalf("%d versions left", n)
	}
}

// Recently removed keeps the latest 100: removing one more deletes the oldest.
func TestAssistantPageRecentlyRemovedMost(t *testing.T) {
	d, ctx := pagesDB(t)
	for i := range AssistantPagesRemovedMost {
		at := pagesBase.Add(time.Duration(i) * time.Minute)
		if _, err := d.pool.Exec(ctx, `INSERT INTO assistant_pages (id, status, title, title_key, owner_kind, created_machine,
			created_session, view, kept_at, writes_window_start, created_at, updated_at, content_at, removed_at, removed_by)
			VALUES ($1, 'removed', $2, $2, 'main', 'm1', 'c1', '{}', $3, $3, $3, $3, $3, $3, 'owner')`,
			fmt.Sprintf("pg_%016x", 1000+i), fmt.Sprintf("old %d", i), at); err != nil {
			t.Fatal(err)
		}
	}
	at := pagesBase.Add(48 * time.Hour)
	p := mustKeep(t, d, mustCreate(t, d, draft(1, "c1", "Running", at)), at)
	if _, _, err := d.RemoveAssistantPage(ctx, p.ID, AssistantPageRemovedByOwner, at); err != nil {
		t.Fatal(err)
	}
	listed, err := d.RecentlyRemovedAssistantPages(ctx, at)
	if err != nil || len(listed) != AssistantPagesRemovedMost || listed[0].ID != p.ID {
		t.Fatalf("%d listed, %v", len(listed), err)
	}
	if _, err := d.AssistantPage(ctx, fmt.Sprintf("pg_%016x", 1000)); !errors.Is(err, ErrAssistantPageNotFound) {
		t.Fatalf("the oldest is kept: %v", err)
	}
	if _, err := d.AssistantPage(ctx, fmt.Sprintf("pg_%016x", 1001)); err != nil {
		t.Fatal(err)
	}
}

func TestAssistantPageDraftExpiry(t *testing.T) {
	d, ctx := pagesDB(t)
	old := mustCreate(t, d, draft(1, "c1", "Old", pagesBase))
	fresh := mustCreate(t, d, draft(2, "c1", "Fresh", pagesBase.Add(2*24*time.Hour)))
	kept := mustKeep(t, d, mustCreate(t, d, draft(3, "c1", "Kept", pagesBase)), pagesBase)
	now := pagesBase.Add(AssistantPageDraftLife)
	expired, err := d.ExpireAssistantDrafts(ctx, now)
	if err != nil || len(expired) != 1 || expired[0].ID != old.ID || expired[0].RemovedBy != AssistantPageRemovedByExpired || expired[0].Listed() {
		t.Fatalf("expired %+v %v", expired, err)
	}
	if again, err := d.ExpireAssistantDrafts(ctx, now); err != nil || len(again) != 0 {
		t.Fatalf("expired twice: %+v %v", again, err)
	}
	if listed, err := d.RecentlyRemovedAssistantPages(ctx, now); err != nil || len(listed) != 0 {
		t.Fatalf("an expired draft listed: %+v %v", listed, err)
	}
	if _, _, err := d.KeepAssistantPage(ctx, AssistantPageKeep{OfferView: old.OfferView, At: now}); !errors.Is(err, ErrAssistantPageRemoved) {
		t.Fatalf("keeping an expired draft: %v", err)
	}
	for _, id := range []string{fresh.ID, kept.ID} {
		if p, err := d.AssistantPage(ctx, id); err != nil || p.Status == AssistantPageRemoved {
			t.Fatalf("%+v %v", p, err)
		}
	}
	// Expired drafts still count in the day's drafts: the window is counted by when a page was drafted.
	d, ctx = pagesDB(t)
	for i := range AssistantPageDraftsPerDay {
		mustCreate(t, d, draft(10+i, fmt.Sprintf("x%d", i), fmt.Sprintf("Page %d", i), pagesBase))
	}
	if _, err := d.pool.Exec(ctx, `UPDATE assistant_pages SET status = 'removed', removed_at = $1, removed_by = 'expired'`, pagesBase); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CreateAssistantPage(ctx, draft(20, "y", "Sixth", pagesBase.Add(time.Hour))); !errors.Is(err, ErrAssistantPageDailyDrafts) {
		t.Fatalf("expired drafts left the day's window: %v", err)
	}
}

// A deleted conversation's pages go to Recently removed and come back as the main conversation's; its drafts are
// removed unlisted; the main conversation's pages it drafted stay.
func TestAssistantPageConversationDeleted(t *testing.T) {
	d, ctx := pagesDB(t)
	mine := draft(1, "c1", "Mine", pagesBase)
	mine.OwnerKind, mine.OwnerMachine, mine.OwnerRef = AssistantPageOwnerConversation, "m1", "c1"
	owned := mustKeep(t, d, mustCreate(t, d, mine), pagesBase)
	if _, _, err := d.PinAssistantPage(ctx, owned.ID, true, pagesBase); err != nil {
		t.Fatal(err)
	}
	waiting := mustCreate(t, d, draft(2, "c1", "Waiting", pagesBase))
	mains := mustKeep(t, d, mustCreate(t, d, draft(3, "c1", "Main's", pagesBase)), pagesBase)
	theirs := draft(4, "c2", "Theirs", pagesBase)
	theirs.OwnerKind, theirs.OwnerMachine, theirs.OwnerRef = AssistantPageOwnerConversation, "m1", "c2"
	other := mustKeep(t, d, mustCreate(t, d, theirs), pagesBase)
	mineList, err := d.AssistantPages(ctx, AssistantPageQuery{OwnerKind: AssistantPageOwnerConversation, OwnerMachine: "m1", OwnerRef: "c1"})
	if err != nil || len(mineList) != 1 || mineList[0].ID != owned.ID {
		t.Fatalf("pages by owner %+v %v", mineList, err)
	}
	at := pagesBase.Add(time.Hour)
	var removed []AssistantPageRow
	err = pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		var err error
		removed, err = RemoveConversationAssistantPagesTx(ctx, tx, "m1", "c1", at)
		return err
	})
	if err != nil || len(removed) != 2 {
		t.Fatalf("removed %+v %v", removed, err)
	}
	for _, p := range removed {
		if p.Status != AssistantPageRemoved || p.RemovedBy != AssistantPageRemovedByConversation || p.Pinned || p.Listed() != (p.ID == owned.ID) {
			t.Fatalf("%+v", p)
		}
	}
	for _, id := range []string{mains.ID, other.ID} {
		if p, err := d.AssistantPage(ctx, id); err != nil || p.Status != AssistantPageActive {
			t.Fatalf("%s: %+v %v", id, p, err)
		}
	}
	listed, err := d.RecentlyRemovedAssistantPages(ctx, at)
	if err != nil || len(listed) != 1 || listed[0].ID != owned.ID {
		t.Fatalf("recently removed %+v %v", listed, err)
	}
	if _, _, err := d.RestoreAssistantPage(ctx, waiting.ID, at); !errors.Is(err, ErrAssistantPageNotFound) {
		t.Fatalf("restoring a draft of a deleted conversation: %v", err)
	}
	row, changed, err := d.RestoreAssistantPage(ctx, owned.ID, at)
	if err != nil || !changed || row.OwnerKind != AssistantPageOwnerMain || row.OwnerMachine != "" || row.OwnerRef != "" || row.Pinned {
		t.Fatalf("restored %+v %v", row, err)
	}
	if again, err := d.RemoveConversationAssistantPages(ctx, "m1", "c1", at); err != nil || len(again) != 0 {
		t.Fatalf("the restored page is the main conversation's now: %+v %v", again, err)
	}
	if _, err := d.RemoveConversationAssistantPages(ctx, "", "c1", at); err == nil {
		t.Fatal("no machine")
	}
}

// Deleting a conversation (PutConversationDeletion) hands its pages over in the same transaction: the kept one goes to
// Recently removed and comes back as the main conversation's, its draft goes unlisted, another's stays.
func TestConversationDeletionRemovesItsPages(t *testing.T) {
	d, ctx := pagesDB(t)
	mine := draft(11, "c1", "Mine", pagesBase)
	mine.OwnerKind, mine.OwnerMachine, mine.OwnerRef = AssistantPageOwnerConversation, "m1", "c1"
	owned := mustKeep(t, d, mustCreate(t, d, mine), pagesBase)
	waiting := mustCreate(t, d, draft(12, "c1", "Waiting", pagesBase))
	theirs := draft(13, "c2", "Theirs", pagesBase)
	theirs.OwnerKind, theirs.OwnerMachine, theirs.OwnerRef = AssistantPageOwnerConversation, "m1", "c2"
	other := mustKeep(t, d, mustCreate(t, d, theirs), pagesBase)
	if err := d.PutConversationDeletion(ctx, "m1", "c1", json.RawMessage(`{"session_id":"c1"}`)); err != nil {
		t.Fatal(err)
	}
	if p, err := d.AssistantPage(ctx, owned.ID); err != nil || p.RemovedBy != AssistantPageRemovedByConversation || !p.Listed() {
		t.Fatalf("its kept page: %+v %v", p, err)
	}
	if p, err := d.AssistantPage(ctx, waiting.ID); err != nil || p.Status != AssistantPageRemoved || p.Listed() {
		t.Fatalf("its draft: %+v %v", p, err)
	}
	if p, err := d.AssistantPage(ctx, other.ID); err != nil || p.Status != AssistantPageActive {
		t.Fatalf("another's page: %+v %v", p, err)
	}
	row, changed, err := d.RestoreAssistantPage(ctx, owned.ID, time.Now())
	if err != nil || !changed || row.OwnerKind != AssistantPageOwnerMain || row.OwnerRef != "" {
		t.Fatalf("restored as the main conversation's: %+v %v", row, err)
	}
	// a deletion that names no machine (an old row) touches no page
	if err := d.PutConversationDeletion(ctx, "", "c2", json.RawMessage(`{"session_id":"c2"}`)); err != nil {
		t.Fatal(err)
	}
	if p, _ := d.AssistantPage(ctx, other.ID); p.Status != AssistantPageActive {
		t.Fatalf("another's page: %+v", p)
	}
}

// An Agent's removal keeps its reason (at most 200 characters) until the page is restored; the owner's has none.
func TestAssistantPageRemovedReason(t *testing.T) {
	d, ctx := pagesDB(t)
	p := mustKeep(t, d, mustCreate(t, d, draft(21, "c1", "Runs", pagesBase)), pagesBase)
	if _, _, err := d.RemoveAssistantPageWithReason(ctx, p.ID, AssistantPageRemovedByAgent, strings.Repeat("x", AssistantPageReasonMost+1), pagesBase); err == nil {
		t.Fatal("a reason too long")
	}
	row, changed, err := d.RemoveAssistantPageWithReason(ctx, p.ID, AssistantPageRemovedByAgent, "You finished.", pagesBase)
	if err != nil || !changed || row.RemovedReason != "You finished." {
		t.Fatalf("removed %+v %v", row, err)
	}
	if listed, err := d.RecentlyRemovedAssistantPages(ctx, pagesBase); err != nil || len(listed) != 1 || listed[0].RemovedReason != "You finished." {
		t.Fatalf("listed %+v %v", listed, err)
	}
	if row, _, err := d.RestoreAssistantPage(ctx, p.ID, pagesBase); err != nil || row.RemovedReason != "" {
		t.Fatalf("restored %+v %v", row, err)
	}
	if row, _, err := d.RemoveAssistantPage(ctx, p.ID, AssistantPageRemovedByOwner, pagesBase); err != nil || row.RemovedReason != "" {
		t.Fatalf("the owner's removal %+v %v", row, err)
	}
}

func countRows(t *testing.T, d *DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := d.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
