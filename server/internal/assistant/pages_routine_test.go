package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

func callToolText(s *Service, machine, session, name string, args map[string]any) (string, error) {
	raw, _ := json.Marshal(args)
	res := s.Tool(context.Background(), ToolCall{Name: name, Arguments: raw, MachineID: machine, SessionID: session})
	var b strings.Builder
	for _, c := range res.Content {
		b.WriteString(c.Text)
	}
	if res.IsError {
		return "", errors.New(b.String())
	}
	return b.String(), nil
}

func postCardActionValues(t *testing.T, s *Service, conversation, card, action string, values map[string]string) (int, map[string]any) {
	t.Helper()
	payload := map[string]any{
		"action":        action,
		"client_msg_id": "tap-" + randomHex(4),
	}
	if values != nil {
		payload["values"] = values
	}
	body, _ := json.Marshal(payload)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/assistant/conversations/"+conversation+"/views/"+card+"/actions", strings.NewReader(string(body)))
	r.Header.Set(extwire.HeaderDevice, "phone")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	out := map[string]any{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func findTaskByPage(t *testing.T, s *Service, pageID string) (Task, bool) {
	t.Helper()
	sc, err := s.lockedSchedule(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer sc.mu.Unlock()
	for _, task := range sc.tasks {
		if task.PageID == pageID {
			return task, true
		}
	}
	return Task{}, false
}

func countTasksByPage(t *testing.T, s *Service, pageID string) int {
	t.Helper()
	sc, err := s.lockedSchedule(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer sc.mu.Unlock()
	n := 0
	for _, task := range sc.tasks {
		if task.PageID == pageID {
			n++
		}
	}
	return n
}

func readPreviewCard(t *testing.T, database *db.DB, session, cardID string) *wire.View {
	t.Helper()
	row, err := database.AssistantView(context.Background(), session, cardID)
	if err != nil {
		t.Fatal(err)
	}
	v, err := wire.ParseAssistantView(row.View)
	if err != nil {
		t.Fatalf("preview card invalid: %v", err)
	}
	return v
}

func sectionItemByKey(v *wire.View, key string) (wire.Component, bool) {
	for _, sec := range v.Sections {
		for _, item := range sec.Items {
			if item.Key == key {
				return item, true
			}
		}
	}
	return wire.Component{}, false
}

// End-to-end watch draft flow: page create (watch without view) -> preview shows Checks / How often / Shows /
// Alert switch (off) / Overview switch (off) -> schedule_routine(page_id) stores held routine and redraws preview
// with frequency -> Keep applies switches, grants data-only right, and arms routine -> remove/restore toggle
// routine -> deleting routine in Scheduled tasks unbinds page -> rebinding on kept page grants from conversation ->
// owner Delete now & 30-day purge leave no routine.
func TestPageWatchDraftPreviewKeepAndRoutineLifecycle(t *testing.T) {
	s, _, database := pagesToolService(t)
	ctx := context.Background()

	watch := map[string]any{
		"question": "Monthly cloud spend",
		"display":  "number",
		"unit":     "USD",
		"alert":    map[string]any{"when": "above", "value": 100},
	}
	created := mustCallPage(t, s, "conv-1", map[string]any{
		"action": "create",
		"title":  "Cloud Spend",
		"icon":   "chart",
		"why":    "Keep an eye on monthly spend.",
		"watch":  watch,
	})
	pageID := created["id"].(string)
	p, err := database.AssistantPage(ctx, pageID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != db.AssistantPageDraft || string(p.View) != "{}" {
		t.Fatalf("draft: status=%q view=%s", p.Status, p.View)
	}

	preview := readPreviewCard(t, database, "conv-1", p.OfferView)
	checks, ok := sectionItemByKey(preview, "persona.watch.checks")
	if !ok || checks.Title != "Checks" || checks.Subtitle != "Monthly cloud spend" {
		t.Fatalf("checks item: %+v ok=%v", checks, ok)
	}
	often, ok := sectionItemByKey(preview, "persona.watch.often")
	if !ok || often.Title != "How often" || often.Subtitle != "Not scheduled yet" {
		t.Fatalf("often item before routine: %+v ok=%v", often, ok)
	}
	shows, ok := sectionItemByKey(preview, "persona.watch.shows")
	if !ok || shows.Title != "Shows" || shows.Subtitle != "A number (USD)" {
		t.Fatalf("shows item: %+v ok=%v", shows, ok)
	}
	alertSwitch, ok := sectionItemByKey(preview, "persona.offer.alert")
	if !ok || alertSwitch.Type != wire.ComponentField || alertSwitch.Kind != wire.FieldKindToggle || alertSwitch.Name != pageOfferFieldAlert || alertSwitch.Value != "false" || !strings.Contains(alertSwitch.Label, "100 USD") {
		t.Fatalf("alert switch: %+v ok=%v", alertSwitch, ok)
	}
	ovSwitch, ok := sectionItemByKey(preview, "persona.offer.overview")
	if !ok || ovSwitch.Type != wire.ComponentField || ovSwitch.Kind != wire.FieldKindToggle || ovSwitch.Name != pageOfferFieldOverview || ovSwitch.Value != "false" || ovSwitch.Label != "Show on Overview" {
		t.Fatalf("overview switch: %+v ok=%v", ovSwitch, ok)
	}
	for _, btnKey := range []string{pageOfferKeep, pageOfferKeepPin, pageOfferNotNow} {
		btn, ok := sectionItemByKey(preview, btnKey)
		if !ok || btn.Action == nil || !btn.Action.Submit {
			t.Fatalf("button %s must have submit=true: %+v", btnKey, btn)
		}
	}

	// Bind routine to the draft: stored held (Enabled: false), preview redrawn with frequency.
	reply, err := callToolText(s, "server", "conv-1", "schedule_routine", map[string]any{
		"page_id":  pageID,
		"title":    "Cloud Spend Refresh",
		"prompt":   "Check billing API and patch Cloud Spend.",
		"time":     "09:00",
		"weekdays": []int{1, 2, 3, 4, 5},
	})
	if err != nil {
		t.Fatalf("schedule_routine on draft: %v", err)
	}
	if !strings.Contains(reply, "held until the owner taps Keep") {
		t.Fatalf("expected held message, got: %s", reply)
	}
	rt, found := findTaskByPage(t, s, pageID)
	if !found {
		t.Fatal("routine not stored")
	}
	if rt.Enabled || rt.Push != pushAsked || rt.Inbox != inboxOff || rt.Report || rt.TodoID != "" || rt.SessionID != "" {
		t.Fatalf("draft routine invariants violated: %+v", rt)
	}
	// Preview redrawn with weekday frequency.
	preview = readPreviewCard(t, database, "conv-1", p.OfferView)
	often, _ = sectionItemByKey(preview, "persona.watch.often")
	if often.Subtitle != "Weekdays at 09:00" {
		t.Fatalf("expected preview frequency 'Weekdays at 09:00', got %q", often.Subtitle)
	}

	// Second routine without id refused naming the first's id.
	if _, err := callToolText(s, "server", "conv-1", "schedule_routine", map[string]any{
		"page_id": pageID,
		"title":   "Duplicate",
		"prompt":  "Check again.",
		"time":    "10:00",
	}); err == nil || !strings.Contains(err.Error(), rt.ID) {
		t.Fatalf("expected duplicate routine refusal naming %s, got: %v", rt.ID, err)
	}
	// Editing existing routine updates preview frequency and stays held on draft.
	if _, err := callToolText(s, "server", "conv-1", "schedule_routine", map[string]any{
		"id":      rt.ID,
		"cron":    "@hourly",
		"enabled": true, // must stay held until Keep
	}); err != nil {
		t.Fatalf("edit draft routine: %v", err)
	}
	rt, _ = findTaskByPage(t, s, pageID)
	if rt.Enabled || rt.Repeat != repeatCron || rt.Cron != "@hourly" {
		t.Fatalf("edited draft routine should remain held with @hourly: %+v", rt)
	}
	preview = readPreviewCard(t, database, "conv-1", p.OfferView)
	often, _ = sectionItemByKey(preview, "persona.watch.often")
	if often.Subtitle == "Not scheduled yet" || often.Subtitle == "" {
		t.Fatalf("preview frequency not updated for @hourly: %q", often.Subtitle)
	}

	// Changing page_id on an existing routine is refused.
	if _, err := callToolText(s, "server", "conv-1", "schedule_routine", map[string]any{
		"id":      rt.ID,
		"page_id": db.NewAssistantPageID(),
	}); err == nil || !strings.Contains(err.Error(), "page_id") {
		t.Fatalf("expected page_id immutable error, got: %v", err)
	}

	// Owner taps Keep with alert=true and on_overview=true.
	code, _ := postCardActionValues(t, s, "conv-1", p.OfferView, pageOfferKeep, map[string]string{
		pageOfferFieldAlert:    "true",
		pageOfferFieldOverview: "true",
	})
	if code != http.StatusOK {
		t.Fatalf("Keep returned HTTP %d", code)
	}
	kept, err := database.AssistantPage(ctx, pageID)
	if err != nil {
		t.Fatal(err)
	}
	if kept.Status != db.AssistantPageActive || !kept.Alert.On || !kept.OnOverview || kept.RoutineID != rt.ID {
		t.Fatalf("kept page mismatch: status=%q alert=%+v overview=%v routine=%q", kept.Status, kept.Alert, kept.OnOverview, kept.RoutineID)
	}
	if countTasksByPage(t, s, pageID) != 1 {
		t.Fatalf("expected exactly 1 routine for page, got %d", countTasksByPage(t, s, pageID))
	}
	rt, _ = findTaskByPage(t, s, pageID)
	if !rt.Enabled {
		t.Fatalf("routine should be armed/enabled after Keep: %+v", rt)
	}
	grants, err := database.AssistantPageGrants(ctx, pageID)
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 1 || grants[0].GranteeKind != db.AssistantPageGrantRoutine || grants[0].GranteeRef != rt.ID || grants[0].GrantedBy != "phone" {
		t.Fatalf("grants after Keep: %+v", grants)
	}

	// Remove page -> routine turned off; Restore page -> routine turned back on.
	if _, err := callPage(s, "server", "conv-1", map[string]any{"action": "remove", "id": pageID, "reason": "No longer tracking"}); err != nil {
		t.Fatalf("page remove: %v", err)
	}
	rt, _ = findTaskByPage(t, s, pageID)
	if rt.Enabled {
		t.Fatal("routine should be off after page remove")
	}
	if _, _, err := database.RestoreAssistantPage(ctx, pageID, time.Now()); err != nil {
		t.Fatal(err)
	}
	s.pageRoutineRestored(ctx, pageID, rt.ID)
	rt, _ = findTaskByPage(t, s, pageID)
	if !rt.Enabled {
		t.Fatal("routine should be back on after restore")
	}

	// Deleting routine in Scheduled tasks unbinds page and revokes grant, page stays kept.
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/assistant/tasks/"+rt.ID, nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE task returned %d: %s", rec.Code, rec.Body.String())
	}
	afterUnbind, err := database.AssistantPage(ctx, pageID)
	if err != nil {
		t.Fatal(err)
	}
	if afterUnbind.Status != db.AssistantPageActive || afterUnbind.RoutineID != "" {
		t.Fatalf("page should remain kept and unbound: status=%q routine=%q", afterUnbind.Status, afterUnbind.RoutineID)
	}
	if has, err := database.HasAssistantPageDataGrant(ctx, pageID, db.AssistantPageGrantRoutine, rt.ID); err != nil || has {
		t.Fatalf("grant should be revoked after routine deletion: has=%v err=%v", has, err)
	}

	// Binding a new routine on a kept page grants data right from that conversation and enables immediately.
	if _, err := callToolText(s, "server", "conv-1", "schedule_routine", map[string]any{
		"page_id": pageID,
		"title":   "Cloud Spend Daily",
		"prompt":  "Check spend daily.",
		"time":    "08:00",
	}); err != nil {
		t.Fatalf("bind routine on kept page: %v", err)
	}
	rt2, found := findTaskByPage(t, s, pageID)
	if !found || !rt2.Enabled {
		t.Fatalf("kept-page routine should be enabled: found=%v task=%+v", found, rt2)
	}
	grants, err = database.AssistantPageGrants(ctx, pageID)
	if err != nil || len(grants) != 1 || grants[0].GrantedBy != "server/conv-1" {
		t.Fatalf("expected conversation grant on kept page, got %+v err=%v", grants, err)
	}

	// Owner Delete now removes page and deletes routine.
	if _, _, err := database.RemoveAssistantPage(ctx, pageID, db.AssistantPageRemovedByOwner, time.Now()); err != nil {
		t.Fatal(err)
	}
	s.pageRoutineRemoved(ctx, pageID, rt2.ID)
	deleted, rid, err := database.DeleteAssistantPageWithRoutine(ctx, pageID)
	if err != nil || !deleted || rid != rt2.ID {
		t.Fatalf("DeleteAssistantPageWithRoutine: deleted=%v rid=%q err=%v", deleted, rid, err)
	}
	s.pageRoutineDeleted(ctx, pageID, rid)
	if _, found := findTaskByPage(t, s, pageID); found {
		t.Fatal("routine should be deleted after owner Delete now")
	}

	// 30-day purge deletes routine too.
	out2 := mustCallPage(t, s, "conv-1", map[string]any{
		"action": "create", "title": "Purge Watch", "watch": watch,
	})
	page2 := out2["id"].(string)
	p2, _ := database.AssistantPage(ctx, page2)
	if _, err := callToolText(s, "server", "conv-1", "schedule_routine", map[string]any{
		"page_id": page2, "title": "Purge Check", "prompt": "Check.", "time": "09:00",
	}); err != nil {
		t.Fatal(err)
	}
	rtPurge, _ := findTaskByPage(t, s, page2)
	code, _ = postCardActionValues(t, s, "conv-1", p2.OfferView, pageOfferKeep, map[string]string{
		pageOfferFieldAlert: "false", pageOfferFieldOverview: "false",
	})
	if code != http.StatusOK {
		t.Fatalf("Keep page2: %d", code)
	}
	removedAt := time.Now().Add(-31 * 24 * time.Hour)
	if _, _, err := database.RemoveAssistantPage(ctx, page2, db.AssistantPageRemovedByOwner, removedAt); err != nil {
		t.Fatal(err)
	}
	nPurged, purgedRoutines, err := database.PurgeAssistantPagesWithRoutines(ctx, time.Now())
	if err != nil || nPurged != 1 || !slices.Equal(purgedRoutines, []string{rtPurge.ID}) {
		t.Fatalf("purge: n=%d routines=%v err=%v", nPurged, purgedRoutines, err)
	}
	for _, r := range purgedRoutines {
		s.pageRoutineDeleted(ctx, "", r)
	}
	if _, found := findTaskByPage(t, s, page2); found {
		t.Fatal("routine should be deleted after purge")
	}
}

// Not now, draft expiry, Agent page remove of draft, and start-up orphan cleanup all leave no routine behind.
func TestPageWatchDraftDiscardAndOrphanCleanup(t *testing.T) {
	s, _, database := pagesToolService(t)
	ctx := context.Background()
	watch := map[string]any{"question": "Build green?", "display": "status"}

	makeDraftWithRoutine := func(title string) (string, string, string) {
		out := mustCallPage(t, s, "conv-1", map[string]any{"action": "create", "title": title, "watch": watch})
		id := out["id"].(string)
		p, err := database.AssistantPage(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := callToolText(s, "server", "conv-1", "schedule_routine", map[string]any{
			"page_id": id, "title": title + " check", "prompt": "Check CI.", "time": "09:00",
		}); err != nil {
			t.Fatal(err)
		}
		rt, ok := findTaskByPage(t, s, id)
		if !ok {
			t.Fatalf("routine missing for %s", title)
		}
		return id, p.OfferView, rt.ID
	}

	// 1. Not now deletes the held routine.
	id1, offer1, _ := makeDraftWithRoutine("Draft Not Now")
	code, _ := postCardActionValues(t, s, "conv-1", offer1, pageOfferNotNow, map[string]string{
		pageOfferFieldOverview: "false",
	})
	if code != http.StatusOK {
		t.Fatalf("Not now HTTP %d", code)
	}
	if _, found := findTaskByPage(t, s, id1); found {
		t.Fatal("Not now left routine behind")
	}

	// 2. Draft expiry deletes the held routine.
	id2, _, _ := makeDraftWithRoutine("Draft Expire")
	expired, err := database.ExpireAssistantDrafts(ctx, time.Now().Add(db.AssistantPageDraftLife+time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	s.expirePageOffers(ctx, expired)
	if _, found := findTaskByPage(t, s, id2); found {
		t.Fatal("draft expiry left routine behind")
	}

	// 3. Agent page remove of draft deletes the held routine.
	id3, _, _ := makeDraftWithRoutine("Draft Remove")
	if _, err := callPage(s, "server", "conv-1", map[string]any{"action": "remove", "id": id3, "reason": "Never mind"}); err != nil {
		t.Fatalf("draft remove: %v", err)
	}
	if _, found := findTaskByPage(t, s, id3); found {
		t.Fatal("draft remove left routine behind")
	}

	// 4. Start-up orphan cleanup removes routines whose page no longer exists, keeping live ones.
	liveID, _, liveRT := makeDraftWithRoutine("Live Draft")
	orphanPageID := db.NewAssistantPageID()
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sc.tasks = append(sc.tasks, Task{
		ID: "rt_orphan1", Title: "Orphan", Prompt: "Check.", Repeat: repeatDaily, Time: "09:00",
		Push: pushAsked, PageID: orphanPageID, Enabled: true,
	})
	if err := s.saveLocked(sc, sc.tasks); err != nil {
		sc.mu.Unlock()
		t.Fatal(err)
	}
	sc.mu.Unlock()

	s.cleanOrphanPageRoutines(ctx)
	if _, found := findTaskByPage(t, s, orphanPageID); found {
		t.Fatal("cleanOrphanPageRoutines did not delete orphan routine")
	}
	if rt, found := findTaskByPage(t, s, liveID); !found || rt.ID != liveRT {
		t.Fatalf("cleanOrphanPageRoutines removed live routine: found=%v rt=%+v", found, rt)
	}
}

// Data-only grant on routine runs: listed page narrowed to get/patch (non-deferred) and no notify_user;
// every call re-checks binding, grant, kept/unpaused status, latest run conversation, and 2-hour window.
func TestPageRoutineDataGrantAndRefusals(t *testing.T) {
	s, hub, database := pagesToolService(t)
	ctx := context.Background()
	hub.fakeHub.setSessions("server",
		proto.Session{SessionID: "main-1", Assistant: true},
		proto.Session{SessionID: "conv-1", Assistant: true},
		proto.Session{SessionID: "conv-2", Assistant: true},
		proto.Session{SessionID: "rt-run-1", Assistant: true},
		proto.Session{SessionID: "rt-run-2", Assistant: true},
		proto.Session{SessionID: "plain-rt", Assistant: true},
	)

	watch := map[string]any{
		"question": "Open incidents",
		"display":  "status",
		"alert":    map[string]any{"when": "status", "value": "warning"},
	}
	// Create watch page WITH a custom view (root + detail page) so we test both reading and items patching.
	out := mustCallPage(t, s, "conv-1", map[string]any{
		"action": "create", "title": "Service Uptime", "watch": watch, "view": runView(),
	})
	pageID := out["id"].(string)
	p, _ := database.AssistantPage(ctx, pageID)
	// Also create a second kept page to test cross-page refusal.
	otherID, otherOffer := createPage(t, s, database, "conv-1", "Other Page")
	if code, _ := postCardAction(t, s, "conv-1", otherOffer, pageOfferKeep); code != http.StatusOK {
		t.Fatalf("keep other page: %d", code)
	}

	if _, err := callToolText(s, "server", "conv-1", "schedule_routine", map[string]any{
		"page_id": pageID, "title": "Health Poll", "prompt": "Check status.", "cron": "@hourly",
	}); err != nil {
		t.Fatal(err)
	}
	rt, _ := findTaskByPage(t, s, pageID)
	if code, _ := postCardActionValues(t, s, "conv-1", p.OfferView, pageOfferKeep, map[string]string{
		pageOfferFieldAlert: "false", pageOfferFieldOverview: "false",
	}); code != http.StatusOK {
		t.Fatalf("keep watch page: %d", code)
	}

	// Record an active run of rt in rt-run-1 started 5 minutes ago, plus a non-page routine in plain-rt.
	runStart := time.Now().Add(-5 * time.Minute).UTC()
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		t.Fatal(err)
	}
	idx := sc.indexLocked(rt.ID)
	sc.tasks[idx].RunMachineID = "server"
	sc.tasks[idx].RunSessionID = "rt-run-1"
	sc.tasks[idx].LastSessionID = "rt-run-1"
	sc.tasks[idx].LastRun = runStart
	sc.tasks = append(sc.tasks, Task{ID: "rt_plain", Title: "Plain", Prompt: "Hi", Repeat: repeatDaily, Time: "09:00", RunMachineID: "server", RunSessionID: "plain-rt"})
	sc.runs = append(sc.runs, ScheduleRun{
		ID: "run_1", Kind: "routine", RefID: rt.ID, Title: rt.Title,
		Due: runStart, At: runStart, Status: runOK, MachineID: "server", SessionID: "rt-run-1", Conversation: "new",
	})
	sc.mu.Unlock()
	s.originSet(ctx, "server", "rt-run-1", OriginTask, rt.ID, "")

	// Tool listing for rt-run-1: has narrowed page (get/patch only), no notify_user, no inbox_post, page not deferred.
	tools := s.withToolLoading(ctx, "rt-run-1", s.ToolsFor(ctx, "server", "rt-run-1"), true)
	var pageTool *proto.Tool
	for i := range tools {
		if tools[i].Name == "notify_user" {
			t.Fatal("notify_user must not be listed to a page-bound routine run")
		}
		if tools[i].Name == "inbox_post" {
			t.Fatal("inbox_post must not be listed to a page-bound routine run")
		}
		if tools[i].Name == pageToolName {
			pageTool = &tools[i]
		}
	}
	if pageTool == nil {
		t.Fatal("page tool not listed to active page-bound routine run")
	}
	if pageTool.DeferLoading {
		t.Fatal("page tool should not be deferred for a page-bound routine run")
	}
	if strings.Contains(string(pageTool.InputSchema), `"create"`) || !strings.Contains(string(pageTool.InputSchema), `"patch"`) {
		t.Fatalf("routine page schema should allow only get/patch: %s", pageTool.InputSchema)
	}
	// Direct call to notify_user / inbox_post refused.
	if _, err := callToolText(s, "server", "rt-run-1", "notify_user", map[string]any{"title": "hi", "body": "alert"}); err == nil {
		t.Fatal("notify_user should be refused for page routine run")
	}
	if _, err := callToolText(s, "server", "rt-run-1", "inbox_post", map[string]any{"title": "hi", "summary": "x", "reason": "y"}); err == nil {
		t.Fatal("inbox_post should be refused for page routine run")
	}

	// Routine run: page get and page patch on own page succeed!
	got, err := callPage(s, "server", "rt-run-1", map[string]any{"action": "get", "id": pageID})
	if err != nil {
		t.Fatalf("routine page get: %v", err)
	}
	if got["routine_id"] != rt.ID || got["watch"] == nil {
		t.Fatalf("routine page get missing watch/routine_id: %v", got)
	}
	patched, err := callPage(s, "server", "rt-run-1", map[string]any{
		"action":     "patch",
		"id":         pageID,
		"reading":    map[string]any{"status": "ok", "text": "All systems nominal"},
		"alert_text": "Check service dashboard",
		"note":       "Hourly check",
		"items": []map[string]any{
			{"type": "metric", "key": "streak", "label": "Uptime", "value": "99.9%"},
			{"type": "text", "key": "h1", "text": "Updated detail line"},
		},
	})
	if err != nil {
		t.Fatalf("routine page patch: %v", err)
	}
	if patched["reading"] != true || patched["patched"] != float64(2) {
		t.Fatalf("unexpected patch output: %v", patched)
	}
	readings, err := database.AssistantPageReadings(ctx, pageID, 5)
	if err != nil || len(readings) != 1 || readings[0].ByKind != db.AssistantPageByRoutine || readings[0].AlertText != "Check service dashboard" {
		t.Fatalf("stored readings: %+v err=%v", readings, err)
	}
	got2, err := callPage(s, "server", "rt-run-1", map[string]any{"action": "get", "id": pageID})
	if err != nil {
		t.Fatal(err)
	}
	if rList, ok := got2["readings"].([]any); !ok || len(rList) != 1 {
		t.Fatalf("page get should include latest readings: %v", got2["readings"])
	}

	// Routine run: other page refused; create/update/list/remove refused.
	if _, err := callPage(s, "server", "rt-run-1", map[string]any{"action": "get", "id": otherID}); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("other page get should be forbidden: %v", err)
	}
	if _, err := callPage(s, "server", "rt-run-1", map[string]any{"action": "patch", "id": otherID, "items": []map[string]any{{"type": "metric", "key": "streak", "label": "X", "value": "1"}}}); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("other page patch should be forbidden: %v", err)
	}
	for _, act := range []string{"create", "update", "list", "remove"} {
		if _, err := callPage(s, "server", "rt-run-1", map[string]any{"action": act, "id": pageID}); err == nil || !strings.Contains(err.Error(), "forbidden") {
			t.Fatalf("action %s should be forbidden for routine run: %v", act, err)
		}
	}

	// Non-page routine run stays refused as in phase 1.
	if pageListed(s, "server", "plain-rt") {
		t.Fatal("non-page routine should not be listed page")
	}
	if _, err := callPage(s, "server", "plain-rt", map[string]any{"action": "get", "id": pageID}); err == nil || !strings.Contains(err.Error(), "routine's run") {
		t.Fatalf("non-page routine refusal: %v", err)
	}

	// Newer run starts in rt-run-2: old run rt-run-1 is now refused (not_current_run), rt-run-2 works.
	run2Start := time.Now().Add(-1 * time.Minute).UTC()
	sc, _ = s.lockedSchedule(ctx)
	sc.tasks[idx].RunSessionID = "rt-run-2"
	sc.tasks[idx].LastSessionID = "rt-run-2"
	sc.tasks[idx].LastRun = run2Start
	sc.runs = append(sc.runs, ScheduleRun{
		ID: "run_2", Kind: "routine", RefID: rt.ID, Title: rt.Title,
		Due: run2Start, At: run2Start, Status: runOK, MachineID: "server", SessionID: "rt-run-2", Conversation: "new",
	})
	sc.mu.Unlock()
	s.originSet(ctx, "server", "rt-run-2", OriginTask, rt.ID, "")

	if _, err := callPage(s, "server", "rt-run-1", map[string]any{"action": "patch", "id": pageID, "reading": map[string]any{"status": "ok"}}); err == nil || !strings.Contains(err.Error(), "not_current_run") {
		t.Fatalf("stale run should fail with not_current_run, got: %v", err)
	}
	if pageListed(s, "server", "rt-run-1") {
		t.Fatal("stale run should no longer be listed page")
	}
	if _, err := callPage(s, "server", "rt-run-2", map[string]any{"action": "patch", "id": pageID, "reading": map[string]any{"status": "ok"}}); err != nil {
		t.Fatalf("current run rt-run-2 should succeed: %v", err)
	}

	// After 2 hours: rt-run-2 is refused (not_current_run).
	sc, _ = s.lockedSchedule(ctx)
	sc.runs[len(sc.runs)-1].At = time.Now().Add(-2*time.Hour - time.Minute).UTC()
	sc.tasks[idx].LastRun = sc.runs[len(sc.runs)-1].At
	sc.mu.Unlock()
	if _, err := callPage(s, "server", "rt-run-2", map[string]any{"action": "patch", "id": pageID, "reading": map[string]any{"status": "ok"}}); err == nil || !strings.Contains(err.Error(), "not_current_run") {
		t.Fatalf("expired run window should fail with not_current_run, got: %v", err)
	}
	if pageListed(s, "server", "rt-run-2") {
		t.Fatal("expired run window should not list page")
	}

	// Restore run window, then check paused, revoked grant, removed, unbound.
	sc, _ = s.lockedSchedule(ctx)
	sc.runs[len(sc.runs)-1].At = time.Now().Add(-time.Minute).UTC()
	sc.tasks[idx].LastRun = sc.runs[len(sc.runs)-1].At
	sc.mu.Unlock()

	if _, _, err := database.PauseAssistantPage(ctx, pageID, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := callPage(s, "server", "rt-run-2", map[string]any{"action": "patch", "id": pageID, "reading": map[string]any{"status": "ok"}}); err == nil || !strings.Contains(err.Error(), "paused_by_owner") {
		t.Fatalf("paused page should fail with paused_by_owner, got: %v", err)
	}
	if _, _, err := database.PauseAssistantPage(ctx, pageID, false, time.Now()); err != nil {
		t.Fatal(err)
	}

	if _, err := database.RevokeAssistantPageData(ctx, pageID, db.AssistantPageGrantRoutine, rt.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := callPage(s, "server", "rt-run-2", map[string]any{"action": "patch", "id": pageID, "reading": map[string]any{"status": "ok"}}); err == nil || !strings.Contains(err.Error(), "no_grant") {
		t.Fatalf("revoked grant should fail with no_grant, got: %v", err)
	}
	if _, err := database.GrantAssistantPageData(ctx, db.AssistantPageGrantRow{
		PageID: pageID, GranteeKind: db.AssistantPageGrantRoutine, GranteeRef: rt.ID, Rights: db.AssistantPageRightData, GrantedBy: "phone", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := database.RemoveAssistantPage(ctx, pageID, db.AssistantPageRemovedByOwner, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := callPage(s, "server", "rt-run-2", map[string]any{"action": "patch", "id": pageID, "reading": map[string]any{"status": "ok"}}); err == nil || !strings.Contains(err.Error(), "removed") {
		t.Fatalf("removed page should fail with removed, got: %v", err)
	}
}

// page patch items validation (types preserved, unknown keys/duplicate keys/>50/floor violations refused) and
// watch update rules (display fixed once kept, alert rule change turns Alert.On off).
func TestPagePatchValidationAndWatchUpdate(t *testing.T) {
	s, _, database := pagesToolService(t)
	ctx := context.Background()

	watch := map[string]any{
		"question": "Errors per hour",
		"display":  "number",
		"alert":    map[string]any{"when": "above", "value": 10},
	}
	out := mustCallPage(t, s, "conv-1", map[string]any{
		"action": "create", "title": "Error Rate", "watch": watch, "view": runView(),
	})
	pageID := out["id"].(string)
	p, _ := database.AssistantPage(ctx, pageID)

	// Draft patch is refused (not_kept).
	if _, err := callPage(s, "server", "conv-1", map[string]any{
		"action": "patch", "id": pageID, "reading": map[string]any{"value": 5},
	}); err == nil || !strings.Contains(err.Error(), "not_kept") {
		t.Fatalf("draft patch should fail with not_kept, got: %v", err)
	}

	// Keep with alert on.
	if code, _ := postCardActionValues(t, s, "conv-1", p.OfferView, pageOfferKeep, map[string]string{
		pageOfferFieldAlert: "true", pageOfferFieldOverview: "false",
	}); code != http.StatusOK {
		t.Fatalf("keep failed: %d", code)
	}

	// Patch validation errors:
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"neither reading nor items", map[string]any{"action": "patch", "id": pageID}, "at least one"},
		{"empty items only", map[string]any{"action": "patch", "id": pageID, "items": []any{}}, "at least one"},
		{"unknown key", map[string]any{"action": "patch", "id": pageID, "items": []map[string]any{{"type": "text", "key": "nope", "text": "hi"}}}, "unknown key"},
		{"wrong component type", map[string]any{"action": "patch", "id": pageID, "items": []map[string]any{{"type": "text", "key": "streak", "text": "hi"}}}, "does not match stored type"},
		{"duplicate key", map[string]any{"action": "patch", "id": pageID, "items": []map[string]any{
			{"type": "metric", "key": "streak", "label": "A", "value": "1"},
			{"type": "metric", "key": "streak", "label": "B", "value": "2"},
		}}, "duplicate key"},
		{"page floor violation (reserved action)", map[string]any{"action": "patch", "id": pageID, "items": []map[string]any{
			{"type": "button", "key": "done", "title": "Hack", "action": map[string]any{"id": "persona.hack", "reply": "Hack"}},
		}}, "page floor"},
		{"invalid reading shape", map[string]any{"action": "patch", "id": pageID, "reading": map[string]any{"status": "ok"}}, "invalid reading"},
	}
	for _, tc := range cases {
		if _, err := callPage(s, "server", "conv-1", tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.name, err, tc.want)
		}
	}
	// More than 50 items refused.
	many := make([]map[string]any, 51)
	for i := range many {
		many[i] = map[string]any{"type": "text", "key": fmt.Sprintf("k%d", i), "text": "x"}
	}
	if _, err := callPage(s, "server", "conv-1", map[string]any{"action": "patch", "id": pageID, "items": many}); err == nil || !strings.Contains(err.Error(), "at most 50") {
		t.Fatalf(">50 items should fail: %v", err)
	}

	// Writer patch succeeds (ByKind agent).
	if _, err := callPage(s, "server", "conv-1", map[string]any{
		"action": "patch", "id": pageID, "reading": map[string]any{"value": 7},
	}); err != nil {
		t.Fatalf("writer patch: %v", err)
	}
	readings, _ := database.AssistantPageReadings(ctx, pageID, 5)
	if len(readings) != 1 || readings[0].ByKind != db.AssistantPageByAgent {
		t.Fatalf("writer reading: %+v", readings)
	}

	// Rate limit on patch returns rate_limited.
	pageSQL(t, `UPDATE assistant_pages SET writes_window_n = $1, writes_window_start = $2 WHERE id = $3`,
		db.AssistantPageWritesPerHour, time.Now().UTC(), pageID)
	if _, err := callPage(s, "server", "conv-1", map[string]any{
		"action": "patch", "id": pageID, "reading": map[string]any{"value": 8},
	}); err == nil || !strings.Contains(err.Error(), "rate_limited") {
		t.Fatalf("rate limited patch: %v", err)
	}
	pageSQL(t, `UPDATE assistant_pages SET writes_window_n = 0 WHERE id = $1`, pageID)

	// Changing watch.display on kept page refused (watch_display_fixed).
	if _, err := callPage(s, "server", "conv-1", map[string]any{
		"action": "update", "id": pageID,
		"watch": map[string]any{"question": "Errors per hour", "display": "status"},
	}); err == nil || !strings.Contains(err.Error(), "watch_display_fixed") {
		t.Fatalf("changing display on kept page: %v", err)
	}
	// Clearing watch on kept page refused (watch_display_fixed).
	if _, err := callPage(s, "server", "conv-1", map[string]any{
		"action": "update", "id": pageID, "watch": nil,
	}); err == nil || !strings.Contains(err.Error(), "watch_display_fixed") {
		t.Fatalf("clearing watch on kept page: %v", err)
	}

	// Changing alert rule on kept page turns Alert.On off.
	if _, err := callPage(s, "server", "conv-1", map[string]any{
		"action": "update", "id": pageID,
		"watch": map[string]any{
			"question": "Errors per hour",
			"display":  "number",
			"alert":    map[string]any{"when": "above", "value": 25},
		},
	}); err != nil {
		t.Fatalf("updating alert rule: %v", err)
	}
	updated, err := database.AssistantPage(ctx, pageID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Alert.On {
		t.Fatal("changing watch alert rule must turn Alert.On off")
	}
}

// Page-bound routine rate limit (too_often in tool and app task editor), forbidden options,
// no push on failure/skip/streak, and skipped runs while draft/paused/removed.
func TestPageRoutineRateLimitAndNoPush(t *testing.T) {
	s, hub, database := pagesToolService(t)
	ctx := context.Background()

	watch := map[string]any{"question": "Queue depth", "display": "number"}
	out := mustCallPage(t, s, "conv-1", map[string]any{"action": "create", "title": "Queue Depth", "watch": watch})
	pageID := out["id"].(string)
	p, _ := database.AssistantPage(ctx, pageID)

	// Non-writer refused.
	if _, err := callToolText(s, "server", "conv-2", "schedule_routine", map[string]any{
		"page_id": pageID, "title": "Sneaky", "prompt": "Check.", "time": "09:00",
	}); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("non-writer binding should be forbidden: %v", err)
	}
	// Forbidden options in schedule_routine.
	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"sub-hourly cron 30m", map[string]any{"page_id": pageID, "title": "Fast", "prompt": "Check.", "cron": "*/30 * * * *"}, "too_often"},
		{"sub-hourly cron 1m", map[string]any{"page_id": pageID, "title": "Fast", "prompt": "Check.", "cron": "* * * * *"}, "too_often"},
		{"report true", map[string]any{"page_id": pageID, "title": "Rep", "prompt": "Check.", "time": "09:00", "report": true}, "report"},
		{"todo_id set", map[string]any{"page_id": pageID, "title": "Todo", "prompt": "Check.", "time": "09:00", "todo_id": "td_1"}, "todo"},
		{"push every", map[string]any{"page_id": pageID, "title": "Push", "prompt": "Check.", "time": "09:00", "push": "every"}, "push"},
		{"inbox worthy", map[string]any{"page_id": pageID, "title": "Inb", "prompt": "Check.", "time": "09:00", "inbox": "worthy"}, "inbox"},
		{"inbox every", map[string]any{"page_id": pageID, "title": "Inb2", "prompt": "Check.", "time": "09:00", "inbox": "every"}, "inbox"},
	} {
		if _, err := callToolText(s, "server", "conv-1", "schedule_routine", tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.name, err, tc.want)
		}
	}

	// Bind valid @hourly routine to draft.
	if _, err := callToolText(s, "server", "conv-1", "schedule_routine", map[string]any{
		"page_id": pageID, "title": "Queue Check", "prompt": "Check queue.", "cron": "@hourly",
	}); err != nil {
		t.Fatalf("hourly bind: %v", err)
	}
	rt, _ := findTaskByPage(t, s, pageID)

	// App task editor HTTP routes: too_often and fixed options.
	putTask := func(body map[string]any) (int, map[string]any) {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(http.MethodPut, "/api/v1/assistant/tasks/"+rt.ID, strings.NewReader(string(raw)))
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		resp := map[string]any{}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		return w.Code, resp
	}
	if code, resp := putTask(map[string]any{"repeat": "interval", "every_minutes": 30, "cron": ""}); code != http.StatusBadRequest || resp["code"] != "too_often" {
		t.Fatalf("PUT interval 30m: code=%d resp=%v", code, resp)
	}
	if code, resp := putTask(map[string]any{"repeat": "cron", "cron": "*/15 * * * *"}); code != http.StatusBadRequest || resp["code"] != "too_often" {
		t.Fatalf("PUT cron 15m: code=%d resp=%v", code, resp)
	}
	if code, resp := putTask(map[string]any{"repeat": "cron", "cron": "* * * * *"}); code != http.StatusBadRequest || resp["code"] != "too_often" {
		t.Fatalf("PUT cron 1m: code=%d resp=%v", code, resp)
	}
	if code, _ := putTask(map[string]any{"push": ""}); code != http.StatusBadRequest {
		t.Fatalf("PUT push every should return 400, got %d", code)
	}
	if code, _ := putTask(map[string]any{"inbox": "every"}); code != http.StatusBadRequest {
		t.Fatalf("PUT inbox every should return 400, got %d", code)
	}
	if code, _ := putTask(map[string]any{"report": true}); code != http.StatusBadRequest {
		t.Fatalf("PUT report true should return 400, got %d", code)
	}
	if code, _ := putTask(map[string]any{"page_id": db.NewAssistantPageID()}); code != http.StatusBadRequest {
		t.Fatalf("PUT changing page_id should return 400, got %d", code)
	}
	if code, resp := putTask(map[string]any{"repeat": "interval", "every_minutes": 60, "cron": ""}); code != http.StatusOK {
		t.Fatalf("PUT interval 60m should succeed: code=%d resp=%v", code, resp)
	}
	// POST /api/v1/assistant/tasks with page_id refused.
	postRaw, _ := json.Marshal(map[string]any{"title": "T", "prompt": "P", "repeat": "daily", "time": "09:00", "page_id": pageID})
	postReq := httptest.NewRequest(http.MethodPost, "/api/v1/assistant/tasks", strings.NewReader(string(postRaw)))
	postRec := httptest.NewRecorder()
	s.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusBadRequest {
		t.Fatalf("POST with page_id should return 400, got %d", postRec.Code)
	}

	// Skipped without push while draft.
	pushesBefore := len(hub.fakeHub.pushed())
	rt, _ = findTaskByPage(t, s, pageID)
	if _, err := s.runTask(rt, time.Now(), time.Now(), false, false); err == nil {
		t.Fatal("runTask on draft should skip")
	}
	if len(hub.fakeHub.pushed()) != pushesBefore {
		t.Fatal("draft skip must not push")
	}

	// Keep page, then pause -> skipped without push.
	if code, _ := postCardActionValues(t, s, "conv-1", p.OfferView, pageOfferKeep, map[string]string{
		pageOfferFieldOverview: "false",
	}); code != http.StatusOK {
		t.Fatalf("keep: %d", code)
	}
	if _, _, err := database.PauseAssistantPage(ctx, pageID, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	rt, _ = findTaskByPage(t, s, pageID)
	if _, err := s.runTask(rt, time.Now(), time.Now(), false, false); err == nil {
		t.Fatal("runTask on paused page should skip")
	}
	if len(hub.fakeHub.pushed()) != pushesBefore {
		t.Fatal("paused skip must not push")
	}

	// Unpause, check preparePageRoutineRun renders header and taskPrompt omits notify_user line.
	if _, _, err := database.PauseAssistantPage(ctx, pageID, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	header, skipErr := s.preparePageRoutineRun(ctx, rt)
	if skipErr != nil {
		t.Fatalf("preparePageRoutineRun on active page: %v", skipErr)
	}
	if !strings.Contains(header, pageID) || !strings.Contains(header, "Queue Depth") || !strings.Contains(header, "Queue depth") || !strings.Contains(header, `{"value":`) {
		t.Fatalf("unexpected run header: %s", header)
	}
	rt.pageHeader = header
	fullPrompt := taskPrompt(rt, time.Now(), time.Now(), time.UTC, false, true, false)
	if strings.Contains(fullPrompt, "notify_user") {
		t.Fatalf("page routine taskPrompt must not instruct notify_user: %s", fullPrompt)
	}

	// Remove page -> skipped without push.
	if _, _, err := database.RemoveAssistantPage(ctx, pageID, db.AssistantPageRemovedByOwner, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.runTask(rt, time.Now(), time.Now(), false, false); err == nil {
		t.Fatal("runTask on removed page should skip")
	}
	if len(hub.fakeHub.pushed()) != pushesBefore {
		t.Fatal("removed skip must not push")
	}

	// Failure paths: notifyScheduleFailed, tellWaitingSkip, pushStreak, streakToldLocked never push for page routines.
	s.notifyScheduleFailed(ctx, rt, ScheduleRun{ID: "run_f1", RefID: rt.ID}, &Device{ID: "server", Name: "Server"}, "upstream 500")
	s.tellWaitingSkip(ctx, rt, Device{ID: "server", Name: "Server"})
	if s.pushStreak(rt, 3, ScheduleRun{ID: "run_f3", RefID: rt.ID, Error: "upstream 500"}) {
		t.Fatal("pushStreak should return false for page routine")
	}
	sc, _ := s.lockedSchedule(ctx)
	for i := range 3 {
		sc.runs = append(sc.runs, ScheduleRun{ID: fmt.Sprintf("fail_%d", i), RefID: rt.ID, Status: runFailed, Error: "err", At: time.Now()})
	}
	_, _, tell := streakToldLocked(sc, len(sc.runs)-1, false)
	sc.mu.Unlock()
	if tell {
		t.Fatal("streakToldLocked must not trigger for page routine")
	}
	if len(hub.fakeHub.pushed()) != pushesBefore {
		t.Fatalf("page routine failures pushed %d notifications", len(hub.fakeHub.pushed())-pushesBefore)
	}
}

// Prompt goldens for assistant/page_routine_run.md in all three languages (en, zh-Hans, ja).
func TestGoldenPageRoutineRun(t *testing.T) {
	samples := []struct {
		name string
		data prompts.PageRoutineRun
	}{
		{
			name: "number",
			data: prompts.PageRoutineRun{
				PageID: "pg_0123456789abcdef", Title: "Cloud Spend",
				Question: "Monthly cloud spend", Display: "number", Unit: "USD", HasView: true,
			},
		},
		{
			name: "status",
			data: prompts.PageRoutineRun{
				PageID: "pg_0123456789abcdef", Title: "CI Status",
				Question: "Is main green?", Display: "status",
			},
		},
		{
			name: "table",
			data: prompts.PageRoutineRun{
				PageID: "pg_0123456789abcdef", Title: "Open PRs",
				Question: "PRs waiting for review", Display: "table", Columns: []string{"PR", "Author", "Age"},
			},
		},
		{
			name: "view_only",
			data: prompts.PageRoutineRun{
				PageID: "pg_0123456789abcdef", Title: "Sprint Board", HasView: true,
			},
		},
	}
	for _, lang := range []string{"en", "zh-Hans", "ja"} {
		var b strings.Builder
		for i, s := range samples {
			if i > 0 {
				b.WriteString("\n---\n")
			}
			d := s.data
			d.Language = lang
			fmt.Fprintf(&b, "[%s]\n%s", s.name, prompts.RenderPageRoutineRun(d))
		}
		golden(t, "page_routine_run."+lang, b.String())
	}
}

// Preview watch schedule & display wording in all three languages.
func TestPageWatchPreviewLocalization(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.En, i18n.ZhHans, i18n.Ja} {
		w := &wire.PageWatch{
			Question: "Open issues",
			Display:  wire.PageWatchTable,
			Columns:  []string{"ID", "Title"},
			Alert:    &wire.PageWatchAlert{When: wire.PageWatchAlertRows, Rows: 3},
		}
		task := &Task{Repeat: repeatDaily, Time: "09:00", Weekdays: []int{1, 3, 5}}
		v := pagePreviewWatch(nil, w, task, "Issues", "Track incoming issues.", lang)
		if _, err := wire.ParseAssistantView(pageJSON(v)); err != nil {
			t.Fatalf("%s preview invalid: %v", lang, err)
		}
		// Interval wording
		if got := pageWatchScheduleText(&Task{Repeat: repeatInterval, EveryMinutes: 120}, lang); got == "" || strings.Contains(got, "assistant-page-routine.") {
			t.Fatalf("%s interval text: %q", lang, got)
		}
		if got := pageWatchScheduleText(&Task{Repeat: repeatInterval, EveryMinutes: 90}, lang); got == "" || strings.Contains(got, "assistant-page-routine.") {
			t.Fatalf("%s 90m text: %q", lang, got)
		}
		// Status & below alerts
		wStatus := &wire.PageWatch{Question: "Up?", Display: wire.PageWatchStatus, Alert: &wire.PageWatchAlert{When: wire.PageWatchAlertStatus, Status: wire.PageWatchStatusProblem}}
		if got := pageWatchAlertLabel(wStatus, lang); got == "" || strings.Contains(got, "assistant-page-routine.") {
			t.Fatalf("%s status alert: %q", lang, got)
		}
		wTrend := &wire.PageWatch{Question: "Temp", Display: wire.PageWatchTrend, Unit: "C", Alert: &wire.PageWatchAlert{When: wire.PageWatchAlertBelow, Number: 10}}
		if got := pageWatchAlertLabel(wTrend, lang); got == "" || strings.Contains(got, "assistant-page-routine.") {
			t.Fatalf("%s below alert: %q", lang, got)
		}
	}
}
