package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/todo/projection"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

type todosListSnapshot struct {
	rawBody string
	top     map[string]json.RawMessage
	byID    map[string]map[string]any
	views   map[string]assistantwire.TodoSummary
	counts  map[string]any
}

func fetchTodosListSnapshot(t *testing.T, s *Service) todosListSnapshot {
	t.Helper()
	w := doTodoHTTP(s, http.MethodGet, "/api/v1/todos", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/todos: code=%d body=%s", w.Code, w.Body.String())
	}
	rawBytes := w.Body.Bytes()

	var top map[string]json.RawMessage
	if err := json.Unmarshal(rawBytes, &top); err != nil {
		t.Fatalf("decode top-level GET /api/v1/todos: %v", err)
	}

	var generic struct {
		Todos  []map[string]any `json:"todos"`
		Counts map[string]any   `json:"counts"`
	}
	if err := json.Unmarshal(rawBytes, &generic); err != nil {
		t.Fatalf("decode generic GET /api/v1/todos: %v", err)
	}
	byID := make(map[string]map[string]any, len(generic.Todos))
	for _, item := range generic.Todos {
		id, _ := item["id"].(string)
		byID[id] = item
	}

	var typed struct {
		Todos []struct {
			ID   string                     `json:"id"`
			View *assistantwire.TodoSummary `json:"view"`
		} `json:"todos"`
	}
	if err := json.Unmarshal(rawBytes, &typed); err != nil {
		t.Fatalf("decode typed GET /api/v1/todos: %v", err)
	}
	views := make(map[string]assistantwire.TodoSummary, len(typed.Todos))
	for _, td := range typed.Todos {
		if td.View != nil {
			views[td.ID] = *td.View
		}
	}

	return todosListSnapshot{
		rawBody: string(rawBytes),
		top:     top,
		byID:    byID,
		views:   views,
		counts:  generic.Counts,
	}
}

func stableViewFields(v any) any {
	m := maps.Clone(v.(map[string]any))
	delete(m, "next_change_at")
	delete(m, "rank")
	return m
}

func assertTodosV2Flag(t *testing.T, s *Service, want bool) {
	t.Helper()
	w, state := permissionSettingsRequest(t, s, http.MethodGet, "/api/v1/assistant", s.managerIdentity(), "full", "")
	if w.Code != http.StatusOK || state.TodosV2 != want {
		t.Fatalf("GET /api/v1/assistant: code=%d todos_v2=%t, want %t", w.Code, state.TodosV2, want)
	}
}

// TestTodosV2CombinedRollbackSuite exercises the full combined todos_v2 lifecycle in one
// end-to-end HTTP + projection suite (#14759, graduation gate for #14538):
//  1. Legacy baseline (todos_v2=false)
//  2. Enable todos_v2=true and combine all additive writes & projection features
//  3. Roll back todos_v2=false and verify lossless rollback + unmodified mobile wire compatibility
//  4. Re-enable todos_v2=true and verify zero data loss across all additive fields and hierarchy
func TestTodosV2CombinedRollbackSuite(t *testing.T) {
	ctx := context.Background()
	s, f := transferEnv(t)
	now := time.Now().UTC().Truncate(time.Second)

	// Register additional worker sessions needed for pending_review and autonomous auto-seen tasks.
	f.mu.Lock()
	f.sessions["box"] = append(f.sessions["box"],
		proto.Session{
			SessionID: "auto_w1",
			MachineID: "box",
			Cwd:       "/code/app",
			Agent:     "claude",
			Status:    proto.StatusDone,
			State:     proto.StateUnread,
			UpdatedAt: now,
		},
		proto.Session{
			SessionID:  "pend_w1",
			MachineID:  "box",
			Cwd:        "/code/app",
			Agent:      "claude",
			Status:     proto.StatusDone,
			State:      proto.StateIdle,
			StateSince: now.Add(-3 * time.Hour),
			UpdatedAt:  now.Add(-3 * time.Hour),
		},
	)
	f.mu.Unlock()

	// --- Build combined initial scenario while todos_v2=false ---
	// 1. Personal to-do and goal parent for reorganization.
	personal, err := s.createTodo(ctx, "Personal checklist", "bring charger", "", "")
	if err != nil {
		t.Fatal(err)
	}
	goal, err := s.createTodo(ctx, "Q4 Release Initiative", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	childPersonal, err := s.createTodo(ctx, "Update release notes", "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// 2. Running supervised task with active working agent, origin, and supervision.
	running, err := s.createTodo(ctx, "Ship rollback telemetry", "", "lead0001", "plan_run_1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.linkTodoAgent(ctx, running.ID, "box", "leadw001", "Lead Worker", "/repo", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := s.superviseTodo(ctx, running.ID, "lead0001", []string{"tests pass"}, 30); err != nil {
		t.Fatal(err)
	}

	// 3. Supervised task ready for audience="autonomous" + auto-seen + DELETE /seen undo.
	autoTask, err := s.createTodo(ctx, "Cleanup stale feature flags", "", "conv1", "plan_auto_1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.linkTodoAgent(ctx, autoTask.ID, "box", "auto_w1", "Auto Worker", "/code/app", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := s.superviseTodo(ctx, autoTask.ID, "conv1", []string{"PR 合并到 master", "浅色 / 深色截图贴进 PR"}, 30); err != nil {
		t.Fatal(err)
	}

	// 4. Supervised task in pending_review within the 24h SLA window (3h ago).
	pendingReview, err := s.createTodo(ctx, "Verify staging migration", "", "conv1", "plan_pend_1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.linkTodoAgent(ctx, pendingReview.ID, "box", "pend_w1", "Pending Worker", "/code/app", "claude"); err != nil {
		t.Fatal(err)
	}
	pendingAt := now.Add(-3 * time.Hour)
	s.todos.mu.Lock()
	s.todos.todos[pendingReview.ID].Supervision = &Supervision{
		Supervisor:   "conv1",
		State:        reviewPending,
		PendingSince: &pendingAt,
		UpdatedAt:    pendingAt,
	}
	s.todos.mu.Unlock()

	// 5. Critical-path dependency chain vs older isolated ask.
	olderAsk, err := s.createPieceTodo(ctx, "conv1", "Isolated older decision", "", db.TodoEdit{})
	if err != nil {
		t.Fatal(err)
	}
	chainParent, err := s.createPieceTodo(ctx, "conv1", "Platform rollout", "", db.TodoEdit{})
	if err != nil {
		t.Fatal(err)
	}
	chainPartOf := chainParent.ID
	blocker, err := s.createPieceTodo(ctx, "conv1", "Critical path blocker", "", db.TodoEdit{PartOf: &chainPartOf})
	if err != nil {
		t.Fatal(err)
	}
	afterBlocker := []string{blocker.ID}
	downstream1, err := s.createPieceTodo(ctx, "conv1", "Downstream step 1", "", db.TodoEdit{PartOf: &chainPartOf, After: &afterBlocker})
	if err != nil {
		t.Fatal(err)
	}
	afterDownstream1 := []string{downstream1.ID}
	downstream2, err := s.createPieceTodo(ctx, "conv1", "Downstream step 2", "", db.TodoEdit{PartOf: &chainPartOf, After: &afterDownstream1})
	if err != nil {
		t.Fatal(err)
	}

	olderAt := now.Add(-3 * time.Hour)
	blockerAt := now.Add(-1 * time.Hour)
	s.todos.mu.Lock()
	s.todos.todos[olderAsk.ID].Supervision = &Supervision{
		Supervisor: "conv1",
		State:      reviewNeedsUser,
		UpdatedAt:  olderAt,
		Reviews:    []TaskReview{{At: olderAt, Verdict: reviewNeedsUser, Reason: "Approve isolated ask?"}},
	}
	s.todos.todos[blocker.ID].Supervision = &Supervision{
		Supervisor: "conv1",
		State:      reviewNeedsUser,
		UpdatedAt:  blockerAt,
		Reviews:    []TaskReview{{At: blockerAt, Verdict: reviewNeedsUser, Reason: "Unblock critical path?"}},
	}
	s.todos.mu.Unlock()

	// 6. Shared external blocker tasks (two open tasks waiting on the same upstream PR #99).
	sharedPrA, err := s.createPieceTodo(ctx, "conv1", "Waiting on PR 99 A", "", db.TodoEdit{})
	if err != nil {
		t.Fatal(err)
	}
	sharedPrB, err := s.createPieceTodo(ctx, "conv1", "Waiting on PR 99 B", "", db.TodoEdit{})
	if err != nil {
		t.Fatal(err)
	}
	parkedAt := now.Add(-2 * time.Hour)
	parkUntil := now.Add(4 * time.Hour)
	s.todos.mu.Lock()
	s.todos.todos[sharedPrA.ID].Supervision = &Supervision{
		Supervisor: "conv1",
		State:      reviewParked,
		UpdatedAt:  parkedAt,
		Reviews: []TaskReview{{
			At:        parkedAt,
			Verdict:   reviewParked,
			Reason:    "Waiting on upstream PR",
			Until:     parkUntil,
			WaitingOn: parkExternalEvent,
			Evidence: &ParkEvidence{
				Kind:   parkExternalEvent,
				Target: "PR 99",
				URL:    "https://GitHub.com/Acme/Core/pull/99/?w=1#discussion",
			},
		}},
	}
	s.todos.todos[sharedPrB.ID].PRURL = "https://github.com/acme/core/pull/99/"
	s.todos.todos[sharedPrB.ID].PRStatus = &TodoPRStatus{
		URL:       "https://github.com/acme/core/pull/99/",
		State:     "open",
		CheckedAt: now,
	}
	s.todos.mu.Unlock()

	wantSharedPRIDs := []string{sharedPrA.ID, sharedPrB.ID}
	slices.Sort(wantSharedPRIDs)
	wantSharedBlockers := []projection.SharedBlocker{{
		Key:     "pull_request:https://github.com/acme/core/pull/99",
		Kind:    projection.SharedBlockerKindPullRequest,
		Label:   "acme/core#99",
		URL:     "https://github.com/acme/core/pull/99",
		TodoIDs: wantSharedPRIDs,
		Count:   2,
	}}

	allIDs := []string{
		personal.ID, goal.ID, childPersonal.ID, running.ID, autoTask.ID,
		pendingReview.ID, olderAsk.ID, chainParent.ID, blocker.ID,
		downstream1.ID, downstream2.ID, sharedPrA.ID, sharedPrB.ID,
	}

	var baseline todosListSnapshot

	// --- Step 1: Legacy baseline (todos_v2=false) ---
	t.Run("step1_legacy_baseline", func(t *testing.T) {
		assertTodosV2Flag(t, s, false)
		baseline = fetchTodosListSnapshot(t, s)

		if len(baseline.byID) != len(allIDs) {
			t.Fatalf("baseline todo count = %d, want %d", len(baseline.byID), len(allIDs))
		}
		if _, has := baseline.top["shared_blockers"]; has {
			t.Fatalf("todos_v2=false must omit shared_blockers, got: %s", baseline.top["shared_blockers"])
		}
		if got := baseline.views[pendingReview.ID]; got.Section != assistantwire.TodoListSectionJustFinished {
			t.Fatalf("legacy pending_review section = %q, want %q (view=%+v)", got.Section, assistantwire.TodoListSectionJustFinished, got)
		}
		if baseline.views[olderAsk.ID].Section != assistantwire.TodoListSectionNeedsYou ||
			baseline.views[blocker.ID].Section != assistantwire.TodoListSectionNeedsYou {
			t.Fatalf("legacy needs_you sections mismatch: older=%+v blocker=%+v", baseline.views[olderAsk.ID], baseline.views[blocker.ID])
		}
		if !(baseline.views[olderAsk.ID].Rank < baseline.views[blocker.ID].Rank) {
			t.Fatalf("legacy needs_you order mismatch: older.rank=%d blocker.rank=%d",
				baseline.views[olderAsk.ID].Rank, baseline.views[blocker.ID].Rank)
		}
	})

	// --- Step 2: Enable todos_v2=true + combined additive writes & features ---
	t.Run("step2_enable_todos_v2_and_exercise_features", func(t *testing.T) {
		setTodosV2ForTest(t, s, true, false)
		assertTodosV2Flag(t, s, true)

		// 2a. Write audience="autonomous", trigger auto-seen on review accept, then DELETE /seen -> AutoSeenUndone.
		if w := doTodoHTTP(s, http.MethodPatch, "/api/v1/todos/"+autoTask.ID, `{"audience":"autonomous"}`); w.Code != http.StatusOK {
			t.Fatalf("PATCH audience: code=%d body=%s", w.Code, w.Body.String())
		}
		acceptReviewForTest(t, s, autoTask.ID, nil)
		gotAuto, _ := s.todoNow(autoTask.ID)
		if gotAuto.Audience != db.AudienceAutonomous || gotAuto.SeenAt == nil || gotAuto.DoneAt == nil || gotAuto.AutoSeenUndone {
			t.Fatalf("after auto-seen accept: audience=%q seen=%v done=%v undone=%t",
				gotAuto.Audience, gotAuto.SeenAt, gotAuto.DoneAt, gotAuto.AutoSeenUndone)
		}
		if s.seenAt("box", "auto_w1").IsZero() {
			t.Fatal("expected finished worker auto_w1 to be auto-seen")
		}

		wUndo := doTodoHTTP(s, http.MethodDelete, "/api/v1/todos/"+autoTask.ID+"/seen", "")
		if wUndo.Code != http.StatusOK {
			t.Fatalf("DELETE /seen: code=%d body=%s", wUndo.Code, wUndo.Body.String())
		}
		if strings.Contains(wUndo.Body.String(), "auto_seen_undone") {
			t.Fatalf("internal auto_seen_undone leaked on DELETE /seen wire response: %s", wUndo.Body.String())
		}
		gotAuto, _ = s.todoNow(autoTask.ID)
		if gotAuto.SeenAt != nil || !gotAuto.AutoSeenUndone {
			t.Fatalf("after DELETE /seen: seen=%v undone=%t", gotAuto.SeenAt, gotAuto.AutoSeenUndone)
		}
		// Re-accept must respect persisted AutoSeenUndone.
		acceptReviewForTest(t, s, autoTask.ID, nil)
		if gotAuto, _ = s.todoNow(autoTask.ID); gotAuto.SeenAt != nil || !gotAuto.AutoSeenUndone {
			t.Fatalf("re-accept overwrote manual undo: seen=%v undone=%t", gotAuto.SeenAt, gotAuto.AutoSeenUndone)
		}

		// 2b. Write direction_id on personal to-do + batch reorganize subtree with include_children=true
		// (default transfer_supervision=false must leave running agent, origin, and supervision untouched).
		if w := doTodoHTTP(s, http.MethodPatch, "/api/v1/todos/"+personal.ID, `{"direction_id":"dir_personal"}`); w.Code != http.StatusOK {
			t.Fatalf("PATCH personal direction_id: code=%d body=%s", w.Code, w.Body.String())
		}
		if w := reorganizeRequest(s, "full", fmt.Sprintf(`{"ids":[%q],"part_of":%q}`, childPersonal.ID, running.ID)); w.Code != http.StatusOK {
			t.Fatalf("attach childPersonal under running: code=%d body=%s", w.Code, w.Body.String())
		}

		sentBefore := f.sent()
		f.mu.Lock()
		callsBefore := len(f.calls)
		f.mu.Unlock()

		reorgBody := fmt.Sprintf(`{"ids":[%q],"part_of":%q,"direction_id":"lead0002","include_children":true}`, running.ID, goal.ID)
		wReorg := reorganizeRequest(s, "full", reorgBody)
		if wReorg.Code != http.StatusOK {
			t.Fatalf("POST /api/v1/todos/reorganize: code=%d body=%s", wReorg.Code, wReorg.Body.String())
		}
		var reorgResp todoReorganizeResponse
		if err := json.Unmarshal(wReorg.Body.Bytes(), &reorgResp); err != nil {
			t.Fatalf("decode reorganize response: %v", err)
		}
		if len(reorgResp.Updated) != 2 {
			t.Fatalf("reorganize updated = %d, want 2 (running + childPersonal)", len(reorgResp.Updated))
		}
		if len(reorgResp.TransferredIDs) != 0 {
			t.Fatalf("default reorganize must not transfer supervision, got %v", reorgResp.TransferredIDs)
		}

		gotRunning, _ := s.todoNow(running.ID)
		if gotRunning.PartOf != goal.ID || gotRunning.DirectionID != "lead0002" {
			t.Fatalf("running after reorganize: part_of=%q direction_id=%q", gotRunning.PartOf, gotRunning.DirectionID)
		}
		if gotRunning.Origin != "lead0001" {
			t.Fatalf("running origin mutated: %q", gotRunning.Origin)
		}
		if gotRunning.Supervision == nil || gotRunning.Supervision.Supervisor != "lead0001" {
			t.Fatalf("running supervision mutated: %+v", gotRunning.Supervision)
		}
		if len(gotRunning.Agents) != 1 || gotRunning.Agents[0].SessionID != "leadw001" || gotRunning.Agents[0].ReleasedAt != nil {
			t.Fatalf("running active agent mutated: %+v", gotRunning.Agents)
		}
		s.todos.mu.RLock()
		activeTodo := s.todos.activeBySession[[2]string{"box", "leadw001"}]
		s.todos.mu.RUnlock()
		if activeTodo != running.ID {
			t.Fatalf("activeBySession mutated: got %q, want %q", activeTodo, running.ID)
		}
		if f.sent() != sentBefore {
			t.Fatalf("reorganize sent unexpected agent messages: before=%d after=%d", sentBefore, f.sent())
		}
		assertNoAgentControlCalls(t, f, callsBefore)

		gotChild, _ := s.todoNow(childPersonal.ID)
		if gotChild.PartOf != running.ID || gotChild.DirectionID != "lead0002" {
			t.Fatalf("childPersonal after include_children reorganize: part_of=%q direction_id=%q", gotChild.PartOf, gotChild.DirectionID)
		}

		// 2c. Assert GET /api/v1/todos reflects all todos_v2 projections together.
		v2Snap := fetchTodosListSnapshot(t, s)
		if strings.Contains(v2Snap.rawBody, "auto_seen_undone") {
			t.Fatal("internal auto_seen_undone leaked on GET /api/v1/todos")
		}
		rawBlockers, hasBlockers := v2Snap.top["shared_blockers"]
		if !hasBlockers {
			t.Fatal("todos_v2=true expected shared_blockers key in GET /api/v1/todos")
		}
		var gotBlockers []projection.SharedBlocker
		if err := json.Unmarshal(rawBlockers, &gotBlockers); err != nil {
			t.Fatalf("decode shared_blockers: %v", err)
		}
		if !reflect.DeepEqual(gotBlockers, wantSharedBlockers) {
			t.Fatalf("shared_blockers mismatch:\ngot:  %+v\nwant: %+v", gotBlockers, wantSharedBlockers)
		}

		if gotPend := v2Snap.views[pendingReview.ID]; gotPend.Section != assistantwire.TodoListSectionWorking {
			t.Fatalf("todos_v2 pending_review (<24h) section = %q, want %q (view=%+v)",
				gotPend.Section, assistantwire.TodoListSectionWorking, gotPend)
		}
		if !(v2Snap.views[blocker.ID].Rank < v2Snap.views[olderAsk.ID].Rank) {
			t.Fatalf("todos_v2 critical-path order mismatch: blocker.rank=%d older.rank=%d",
				v2Snap.views[blocker.ID].Rank, v2Snap.views[olderAsk.ID].Rank)
		}
	})

	// --- Step 3: Rollback todos_v2=false + unmodified mobile wire compatibility ---
	t.Run("step3_rollback_todos_v2_off_and_mobile_wire_compat", func(t *testing.T) {
		setTodosV2ForTest(t, s, false, true)
		assertTodosV2Flag(t, s, false)

		offSnap := fetchTodosListSnapshot(t, s)
		if len(offSnap.byID) != len(allIDs) {
			t.Fatalf("rollback todo count = %d, want %d", len(offSnap.byID), len(allIDs))
		}
		if strings.Contains(offSnap.rawBody, "auto_seen_undone") {
			t.Fatal("internal auto_seen_undone leaked on rollback GET /api/v1/todos")
		}

		// shared_blockers must be completely omitted on rollback.
		if _, has := offSnap.top["shared_blockers"]; has {
			t.Fatalf("rollback todos_v2=false must omit shared_blockers, got: %s", offSnap.top["shared_blockers"])
		}

		// pending_review and needs_you ordering must immediately revert to legacy semantics.
		if gotPend := offSnap.views[pendingReview.ID]; gotPend.Section != assistantwire.TodoListSectionJustFinished {
			t.Fatalf("rollback pending_review section = %q, want %q", gotPend.Section, assistantwire.TodoListSectionJustFinished)
		}
		if !reflect.DeepEqual(stableViewFields(offSnap.byID[pendingReview.ID]["view"]), stableViewFields(baseline.byID[pendingReview.ID]["view"])) {
			t.Fatalf("rollback pending_review view diverged from baseline:\ngot:  %v\nwant: %v",
				offSnap.byID[pendingReview.ID]["view"], baseline.byID[pendingReview.ID]["view"])
		}
		if !(offSnap.views[olderAsk.ID].Rank < offSnap.views[blocker.ID].Rank) {
			t.Fatalf("rollback needs_you order did not revert: older.rank=%d blocker.rank=%d",
				offSnap.views[olderAsk.ID].Rank, offSnap.views[blocker.ID].Rank)
		}
		for _, id := range []string{olderAsk.ID, blocker.ID} {
			if !reflect.DeepEqual(stableViewFields(offSnap.byID[id]["view"]), stableViewFields(baseline.byID[id]["view"])) {
				t.Fatalf("rollback view for %s diverged from baseline:\ngot:  %v\nwant: %v",
					id, offSnap.byID[id]["view"], baseline.byID[id]["view"])
			}
		}

		// Top-level counts structure must remain valid and intact for unmodified mobile clients.
		if offSnap.counts == nil {
			t.Fatal("rollback GET /api/v1/todos missing counts object")
		}
		if _, ok := offSnap.counts["needs_you"].(float64); !ok {
			t.Fatalf("rollback counts missing numeric needs_you: %v", offSnap.counts)
		}
		if _, ok := offSnap.counts["sections"].(map[string]any); !ok {
			t.Fatalf("rollback counts missing sections map: %v", offSnap.counts)
		}

		// Every to-do row must retain all required legacy mobile wire keys with valid types.
		for _, id := range allIDs {
			row, ok := offSnap.byID[id]
			if !ok {
				t.Fatalf("todo %s missing from rollback GET /api/v1/todos", id)
			}
			for _, key := range []string{"id", "title", "notes", "done_at", "created_at", "updated_at", "spaces", "agents", "quiet", "view"} {
				if _, exists := row[key]; !exists {
					t.Errorf("todo %s missing legacy mobile wire key %q after rollback", id, key)
				}
			}
			viewMap, ok := row["view"].(map[string]any)
			if !ok {
				t.Fatalf("todo %s view is not an object: %T", id, row["view"])
			}
			for _, vKey := range []string{"phase", "section", "reason", "rank"} {
				if _, exists := viewMap[vKey]; !exists {
					t.Errorf("todo %s view missing legacy key %q after rollback", id, vKey)
				}
			}
		}

		// Detailed mobile wire value checks for origin, supervision, agents, done_at, part_of, and after.
		runRow := offSnap.byID[running.ID]
		if runRow["origin"] != "lead0001" || runRow["part_of"] != goal.ID {
			t.Fatalf("rollback running row origin/part_of corrupted: origin=%v part_of=%v", runRow["origin"], runRow["part_of"])
		}
		if runRow["done_at"] != nil {
			t.Fatalf("rollback running row unexpectedly has non-nil done_at: %v", runRow["done_at"])
		}
		runSup, ok := runRow["supervision"].(map[string]any)
		if !ok || runSup["supervisor"] != "lead0001" {
			t.Fatalf("rollback running supervision corrupted: %v", runRow["supervision"])
		}
		runAgents, ok := runRow["agents"].([]any)
		if !ok || len(runAgents) != 1 {
			t.Fatalf("rollback running agents corrupted: %v", runRow["agents"])
		}
		runAgent0, _ := runAgents[0].(map[string]any)
		if runAgent0["session_id"] != "leadw001" {
			t.Fatalf("rollback running agent session_id corrupted: %v", runAgent0)
		}
		if _, released := runAgent0["released_at"]; released {
			t.Fatalf("rollback running agent unexpectedly released: %v", runAgent0)
		}

		autoRow := offSnap.byID[autoTask.ID]
		if autoRow["origin"] != "conv1" {
			t.Fatalf("rollback autoTask origin corrupted: %v", autoRow["origin"])
		}
		if doneAt, _ := autoRow["done_at"].(string); doneAt == "" {
			t.Fatalf("rollback autoTask missing done_at: %v", autoRow["done_at"])
		}
		if _, hasSeen := autoRow["seen_at"]; hasSeen {
			t.Fatalf("rollback autoTask should remain unseen after undo, got seen_at=%v", autoRow["seen_at"])
		}
		autoSup, ok := autoRow["supervision"].(map[string]any)
		if !ok || autoSup["supervisor"] != "conv1" {
			t.Fatalf("rollback autoTask supervision corrupted: %v", autoRow["supervision"])
		}

		if offSnap.byID[childPersonal.ID]["part_of"] != running.ID {
			t.Fatalf("rollback childPersonal part_of = %v, want %q", offSnap.byID[childPersonal.ID]["part_of"], running.ID)
		}
		if offSnap.byID[downstream1.ID]["part_of"] != chainParent.ID {
			t.Fatalf("rollback downstream1 part_of = %v, want %q", offSnap.byID[downstream1.ID]["part_of"], chainParent.ID)
		}
		if gotAfter, _ := offSnap.byID[downstream1.ID]["after"].([]any); len(gotAfter) != 1 || gotAfter[0] != blocker.ID {
			t.Fatalf("rollback downstream1 after corrupted: %v", offSnap.byID[downstream1.ID]["after"])
		}
		if gotAfter2, _ := offSnap.byID[downstream2.ID]["after"].([]any); len(gotAfter2) != 1 || gotAfter2[0] != downstream1.ID {
			t.Fatalf("rollback downstream2 after corrupted: %v", offSnap.byID[downstream2.ID]["after"])
		}

		// Single-item endpoints must also succeed cleanly with zero errors on rollback.
		for _, id := range []string{running.ID, autoTask.ID, personal.ID} {
			w := doTodoHTTP(s, http.MethodGet, "/api/v1/todos/"+id, "")
			if w.Code != http.StatusOK {
				t.Fatalf("rollback GET /api/v1/todos/%s: code=%d body=%s", id, w.Code, w.Body.String())
			}
		}
	})

	// --- Step 4: Re-enable todos_v2=true and verify zero data loss ---
	t.Run("step4_reenable_todos_v2_data_survival", func(t *testing.T) {
		setTodosV2ForTest(t, s, true, false)
		assertTodosV2Flag(t, s, true)
		if !s.settings(ctx).todosV2On() {
			t.Fatal("expected todosV2On() == true after re-enable")
		}

		reSnap := fetchTodosListSnapshot(t, s)

		// Verify stored & wire additive fields survived intact without re-entry.
		gotAuto, ok := s.todoNow(autoTask.ID)
		if !ok || gotAuto.Audience != db.AudienceAutonomous || !gotAuto.AutoSeenUndone || gotAuto.SeenAt != nil {
			t.Fatalf("autoTask additive state lost after re-enable: ok=%t audience=%q undone=%t seen=%v",
				ok, gotAuto.Audience, gotAuto.AutoSeenUndone, gotAuto.SeenAt)
		}
		if reSnap.byID[autoTask.ID]["audience"] != db.AudienceAutonomous {
			t.Fatalf("autoTask wire audience after re-enable = %v, want %q", reSnap.byID[autoTask.ID]["audience"], db.AudienceAutonomous)
		}
		// Persisted AutoSeenUndone must still block auto-seen on subsequent re-accept.
		acceptReviewForTest(t, s, autoTask.ID, nil)
		if gotAuto, _ = s.todoNow(autoTask.ID); gotAuto.SeenAt != nil || !gotAuto.AutoSeenUndone {
			t.Fatalf("after re-enable, re-accept overwrote AutoSeenUndone: seen=%v undone=%t", gotAuto.SeenAt, gotAuto.AutoSeenUndone)
		}

		gotPersonal, ok := s.todoNow(personal.ID)
		if !ok || gotPersonal.DirectionID != "dir_personal" {
			t.Fatalf("personal direction_id lost after re-enable: ok=%t dir=%q", ok, gotPersonal.DirectionID)
		}
		if reSnap.byID[personal.ID]["direction_id"] != "dir_personal" {
			t.Fatalf("personal wire direction_id after re-enable = %v", reSnap.byID[personal.ID]["direction_id"])
		}

		gotRunning, ok := s.todoNow(running.ID)
		if !ok || gotRunning.PartOf != goal.ID || gotRunning.DirectionID != "lead0002" ||
			gotRunning.Origin != "lead0001" || gotRunning.Supervision == nil || gotRunning.Supervision.Supervisor != "lead0001" ||
			len(gotRunning.Agents) != 1 || gotRunning.Agents[0].SessionID != "leadw001" || gotRunning.Agents[0].ReleasedAt != nil {
			t.Fatalf("running state lost or corrupted after re-enable: ok=%t todo=%+v", ok, gotRunning)
		}
		if reSnap.byID[running.ID]["part_of"] != goal.ID || reSnap.byID[running.ID]["direction_id"] != "lead0002" {
			t.Fatalf("running wire hierarchy/direction lost after re-enable: part_of=%v direction_id=%v",
				reSnap.byID[running.ID]["part_of"], reSnap.byID[running.ID]["direction_id"])
		}

		gotChild, ok := s.todoNow(childPersonal.ID)
		if !ok || gotChild.PartOf != running.ID || gotChild.DirectionID != "lead0002" {
			t.Fatalf("childPersonal hierarchy/direction lost after re-enable: ok=%t part_of=%q dir=%q",
				ok, gotChild.PartOf, gotChild.DirectionID)
		}
		if reSnap.byID[childPersonal.ID]["part_of"] != running.ID || reSnap.byID[childPersonal.ID]["direction_id"] != "lead0002" {
			t.Fatalf("childPersonal wire hierarchy/direction lost after re-enable: part_of=%v direction_id=%v",
				reSnap.byID[childPersonal.ID]["part_of"], reSnap.byID[childPersonal.ID]["direction_id"])
		}

		// Verify todos_v2 projections (shared_blockers, pending_review SLA, critical-path ranking) resume cleanly.
		rawBlockers, hasBlockers := reSnap.top["shared_blockers"]
		if !hasBlockers {
			t.Fatal("re-enabled todos_v2 missing shared_blockers")
		}
		var gotBlockers []projection.SharedBlocker
		if err := json.Unmarshal(rawBlockers, &gotBlockers); err != nil {
			t.Fatalf("decode re-enabled shared_blockers: %v", err)
		}
		if !reflect.DeepEqual(gotBlockers, wantSharedBlockers) {
			t.Fatalf("re-enabled shared_blockers mismatch:\ngot:  %+v\nwant: %+v", gotBlockers, wantSharedBlockers)
		}
		if gotPend := reSnap.views[pendingReview.ID]; gotPend.Section != assistantwire.TodoListSectionWorking {
			t.Fatalf("re-enabled pending_review section = %q, want %q", gotPend.Section, assistantwire.TodoListSectionWorking)
		}
		if !(reSnap.views[blocker.ID].Rank < reSnap.views[olderAsk.ID].Rank) {
			t.Fatalf("re-enabled critical-path order mismatch: blocker.rank=%d older.rank=%d",
				reSnap.views[blocker.ID].Rank, reSnap.views[olderAsk.ID].Rank)
		}
	})
}
