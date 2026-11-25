package assistant

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/todo/projection"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
)

func TestTodosV2SettingsLifecycle(t *testing.T) {
	s, _, _ := briefService(t, "Prepare the release checklist")
	if out, bad := tool(t, s, "todo_create", map[string]any{"title": "Release checklist"}); bad {
		t.Fatal(out)
	}
	item, ok := todoByTitle(s, "Release checklist")
	if !ok {
		t.Fatal("missing checklist")
	}

	put := func(manager, role, body string, status int) string {
		t.Helper()
		w, _ := permissionSettingsRequest(t, s, "PUT", "/api/v1/assistant", manager, role, body)
		if w.Code != status {
			t.Fatalf("PUT %s (manager=%q role=%q): got %d, want %d: %s", body, manager, role, w.Code, status, w.Body.String())
		}
		return w.Body.String()
	}

	// Default is false.
	w, state := permissionSettingsRequest(t, s, "GET", "/api/v1/assistant", s.managerIdentity(), "full", "")
	if w.Code != http.StatusOK || state.TodosV2 {
		t.Fatalf("GET default: code=%d todos_v2=%t", w.Code, state.TodosV2)
	}

	// Cross-manager and read-only role are rejected with 403; missing manager target returns 400.
	put("another", "full", `{"todos_v2":true}`, http.StatusForbidden)
	put("", "full", `{"todos_v2":true}`, http.StatusBadRequest)
	put(s.managerIdentity(), "read", `{"todos_v2":true}`, http.StatusForbidden)

	// Enable with matching optimistic lock.
	put(s.managerIdentity(), "full", `{"todos_v2":true,"expected_todos_v2":false}`, http.StatusOK)
	w, state = permissionSettingsRequest(t, s, "GET", "/api/v1/assistant", s.managerIdentity(), "full", "")
	if w.Code != http.StatusOK || !state.TodosV2 {
		t.Fatalf("GET after enable: code=%d todos_v2=%t", w.Code, state.TodosV2)
	}

	// Stale expected_todos_v2 returns 409 configuration_conflict without changing state.
	conflictBody := put(s.managerIdentity(), "full", `{"todos_v2":false,"expected_todos_v2":false}`, http.StatusConflict)
	var errResp struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(conflictBody), &errResp); err != nil || errResp.Code != "configuration_conflict" {
		t.Fatalf("unexpected conflict response: %s", conflictBody)
	}

	// Lossless rollback to false with matching optimistic lock.
	put(s.managerIdentity(), "full", `{"todos_v2":false,"expected_todos_v2":true}`, http.StatusOK)
	w, state = permissionSettingsRequest(t, s, "GET", "/api/v1/assistant", s.managerIdentity(), "full", "")
	if w.Code != http.StatusOK || state.TodosV2 {
		t.Fatalf("GET after rollback: code=%d todos_v2=%t", w.Code, state.TodosV2)
	}

	// Toggling performs zero destructive migration on existing to-dos.
	after, ok := todoByTitle(s, "Release checklist")
	if !ok || !reflect.DeepEqual(after, item) {
		t.Fatalf("setting change mutated saved to-do: got %+v, want %+v", after, item)
	}
}

func TestTodosV2PersistsForSelectedManager(t *testing.T) {
	st := &memStore{}
	f := newFakeHub().withMachine()
	s := newServiceOn(t, f, st)
	s.id = "example-manager"

	w, _ := permissionSettingsRequest(t, s, "PUT", "/api/v1/assistant", s.id, "full", `{"todos_v2":true,"expected_todos_v2":false}`)
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}

	reloaded := newServiceOn(t, f, st)
	reloaded.id = s.id
	w, view := permissionSettingsRequest(t, reloaded, "GET", "/api/v1/assistant", s.id, "full", "")
	if w.Code != http.StatusOK || view.Manager != s.id || !view.TodosV2 {
		t.Fatalf("lost manager preference on reload: code=%d manager=%q todos_v2=%t", w.Code, view.Manager, view.TodosV2)
	}
}

// TestTodosV2AudienceLosslessRollback verifies the full additive audience lifecycle (#14619):
// todos_v2 off (baseline) -> enable todos_v2 -> write human/autonomous via HTTP API & MCP tools
// (todo_create, todo_update, task_plan) -> disable todos_v2 -> assert zero data loss, untouched legacy
// wire fields & identical projection/counts -> re-enable todos_v2 -> assert audience values survive intact.
func TestTodosV2AudienceLosslessRollback(t *testing.T) {
	s, _, _ := briefService(t, "Prepare the architecture memo and run CI cleanup")

	req := func(s *Service, method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set(RoleHeader, "full")
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}

	setTodosV2 := func(want bool, expected bool) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"todos_v2": want, "expected_todos_v2": expected})
		w, _ := permissionSettingsRequest(t, s, "PUT", "/api/v1/assistant", s.managerIdentity(), "full", string(body))
		if w.Code != http.StatusOK {
			t.Fatalf("PUT todos_v2=%t: code=%d body=%s", want, w.Code, w.Body.String())
		}
	}
	getTodos := func() (map[string]map[string]any, map[string]any) {
		t.Helper()
		w := req(s, "GET", "/api/v1/todos", "")
		if w.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/todos: code=%d body=%s", w.Code, w.Body.String())
		}
		var payload struct {
			Todos  []map[string]any `json:"todos"`
			Counts map[string]any   `json:"counts"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode GET /api/v1/todos: %v", err)
		}
		byID := make(map[string]map[string]any, len(payload.Todos))
		for _, item := range payload.Todos {
			id, _ := item["id"].(string)
			byID[id] = item
		}
		return byID, payload.Counts
	}
	getTodoItem := func(id string) map[string]any {
		t.Helper()
		w := req(s, "GET", "/api/v1/todos/"+id, "")
		if w.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/todos/%s: code=%d body=%s", id, w.Code, w.Body.String())
		}
		var payload struct {
			Todo map[string]any `json:"todo"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode GET /api/v1/todos/%s: %v", id, err)
		}
		return payload.Todo
	}

	// 1. Baseline with todos_v2=false: legacy to-do omits "audience" on the wire.
	w := req(s, "POST", "/api/v1/todos", `{"title":"Legacy baseline item","notes":"created before todos_v2"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST legacy todo: code=%d body=%s", w.Code, w.Body.String())
	}
	var legacyResp struct {
		Todo Todo `json:"todo"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &legacyResp); err != nil {
		t.Fatal(err)
	}
	legacyID := legacyResp.Todo.ID
	beforeRows, _ := getTodos()
	if _, hasKey := beforeRows[legacyID]["audience"]; hasKey {
		t.Fatalf("legacy todo should omit empty audience key on wire, got: %v", beforeRows[legacyID])
	}

	// 2. Enable todos_v2.
	setTodosV2(true, false)

	// 3a. HTTP POST with audience="human" and invalid audience rejection.
	w = req(s, "POST", "/api/v1/todos", `{"title":"Bad audience","audience":"robot"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("POST invalid audience: got %d, want 400: %s", w.Code, w.Body.String())
	}
	w = req(s, "POST", "/api/v1/todos", `{"title":"HTTP human deliverable","audience":"human"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST human todo: code=%d body=%s", w.Code, w.Body.String())
	}
	var httpHumanResp struct {
		Todo Todo `json:"todo"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &httpHumanResp); err != nil {
		t.Fatal(err)
	}
	httpHumanID := httpHumanResp.Todo.ID
	if httpHumanResp.Todo.Audience != db.AudienceHuman {
		t.Fatalf("POST human audience = %q, want %q", httpHumanResp.Todo.Audience, db.AudienceHuman)
	}

	// 3b. HTTP PATCH updates audience and rejects invalid value.
	w = req(s, "PATCH", "/api/v1/todos/"+httpHumanID, `{"audience":"invalid"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("PATCH invalid audience: got %d, want 400: %s", w.Code, w.Body.String())
	}
	w = req(s, "PATCH", "/api/v1/todos/"+httpHumanID, `{"audience":"autonomous"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH autonomous: code=%d body=%s", w.Code, w.Body.String())
	}
	w = req(s, "PATCH", "/api/v1/todos/"+httpHumanID, `{"audience":"human"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH back to human: code=%d body=%s", w.Code, w.Body.String())
	}

	// 3c. MCP todo_create & todo_update (including clearing with "" and rejecting invalid values).
	if out, bad := tool(t, s, "todo_create", map[string]any{"title": "Invalid MCP audience", "audience": "unknown"}); !bad {
		t.Fatalf("todo_create invalid audience should fail, got: %s", out)
	}
	if out, bad := tool(t, s, "todo_create", map[string]any{"title": "MCP autonomous task", "audience": "autonomous"}); bad {
		t.Fatalf("todo_create autonomous: %s", out)
	}
	mcpAuto, ok := todoByTitle(s, "MCP autonomous task")
	if !ok || mcpAuto.Audience != db.AudienceAutonomous {
		t.Fatalf("todo_create autonomous saved = %+v (ok=%t)", mcpAuto, ok)
	}
	if out, bad := tool(t, s, "todo_create", map[string]any{"title": "MCP cleared later", "audience": "human"}); bad {
		t.Fatalf("todo_create clear target: %s", out)
	}
	mcpCleared, ok := todoByTitle(s, "MCP cleared later")
	if !ok || mcpCleared.Audience != db.AudienceHuman {
		t.Fatalf("todo_create clear target saved = %+v (ok=%t)", mcpCleared, ok)
	}
	if out, bad := tool(t, s, "todo_update", map[string]any{"id": mcpCleared.ID, "audience": "invalid"}); !bad {
		t.Fatalf("todo_update invalid audience should fail, got: %s", out)
	}
	if out, bad := tool(t, s, "todo_update", map[string]any{"id": mcpCleared.ID, "audience": ""}); bad {
		t.Fatalf("todo_update clear audience: %s", out)
	}
	mcpClearedAfter, _ := s.todoNow(mcpCleared.ID)
	if mcpClearedAfter.Audience != "" {
		t.Fatalf("todo_update clear audience left %q, want empty", mcpClearedAfter.Audience)
	}

	// 3d. MCP task_plan with audience (valid & invalid).
	if out, bad := tool(t, s, "task_plan", map[string]any{
		"tasks": []map[string]any{{
			"title": "Bad plan audience", "prompt": "inspect", "machine": "box", "cwd": "/tmp",
			"readonly": true, "role": "worker", "goal": "check", "audience": "bogus",
		}},
	}); !bad || !strings.Contains(out, "audience") {
		t.Fatalf("task_plan invalid audience should fail, bad=%t out=%s", bad, out)
	}
	if out, bad := tool(t, s, "task_plan", map[string]any{
		"tasks": []map[string]any{{
			"title": "Audit release pipeline status", "prompt": "inspect release pipeline status and report findings", "machine": "box", "cwd": "/tmp",
			"readonly": true, "role": "worker", "goal": "verify release pipeline state", "audience": "autonomous",
		}},
	}); bad {
		t.Fatalf("task_plan autonomous failed: %s", out)
	}
	var plannedTodo Todo
	var foundPlanned bool
	s.todos.mu.RLock()
	for _, td := range s.todos.todos {
		if td.PlanID != "" {
			plannedTodo = copyTodoLocked(td)
			foundPlanned = true
			break
		}
	}
	s.todos.mu.RUnlock()
	if !foundPlanned || plannedTodo.Audience != db.AudienceAutonomous {
		t.Fatalf("task_plan autonomous todo = %+v (found=%t)", plannedTodo, foundPlanned)
	}

	// Snapshot list, views, and counts while todos_v2=true.
	onRows, onCounts := getTodos()
	if len(onRows) != 5 {
		t.Fatalf("expected 5 todos while todos_v2=true, got %d", len(onRows))
	}

	// 4. Disable todos_v2 (rollback).
	setTodosV2(false, true)

	// 5. Assert zero data loss, identical projection/counts, untouched legacy wire fields, and clean single-item read.
	offRows, offCounts := getTodos()
	if len(offRows) != len(onRows) {
		t.Fatalf("rollback changed todo count: off=%d on=%d", len(offRows), len(onRows))
	}
	if !reflect.DeepEqual(offCounts, onCounts) {
		t.Fatalf("rollback changed counts: off=%v on=%v", offCounts, onCounts)
	}
	for id, onRow := range onRows {
		offRow, exists := offRows[id]
		if !exists {
			t.Fatalf("todo %s lost after rollback", id)
		}
		// A time-derived cursor can move between two reads; compare placement and content, not that cursor or rank.
		stable := func(v any) any {
			m := maps.Clone(v.(map[string]any))
			delete(m, "next_change_at")
			delete(m, "rank")
			return m
		}
		if !reflect.DeepEqual(stable(offRow["view"]), stable(onRow["view"])) {
			t.Fatalf("todo %s view changed on rollback: off=%v on=%v", id, offRow["view"], onRow["view"])
		}
		for _, legacyKey := range []string{"id", "title", "notes", "created_at", "updated_at", "spaces", "agents", "quiet", "view"} {
			if _, ok := offRow[legacyKey]; !ok {
				t.Errorf("todo %s missing legacy wire key %q after rollback", id, legacyKey)
			}
		}
	}
	if _, hasKey := offRows[legacyID]["audience"]; hasKey {
		t.Fatalf("legacy todo unexpectedly gained audience key after rollback: %v", offRows[legacyID])
	}
	if _, hasKey := offRows[mcpCleared.ID]["audience"]; hasKey {
		t.Fatalf("cleared todo unexpectedly kept audience key after rollback: %v", offRows[mcpCleared.ID])
	}
	singleHuman := getTodoItem(httpHumanID)
	if singleHuman["audience"] != db.AudienceHuman {
		t.Fatalf("GET /api/v1/todos/%s audience = %v, want %q", httpHumanID, singleHuman["audience"], db.AudienceHuman)
	}

	// 6. Re-enable todos_v2 and confirm all stored audience values remain intact.
	setTodosV2(true, false)
	reEnabledRows, reEnabledCounts := getTodos()
	if !reflect.DeepEqual(reEnabledCounts, onCounts) {
		t.Fatalf("counts changed after re-enable: got=%v want=%v", reEnabledCounts, onCounts)
	}
	wantAudience := map[string]string{
		legacyID:       "",
		mcpCleared.ID:  "",
		httpHumanID:    db.AudienceHuman,
		mcpAuto.ID:     db.AudienceAutonomous,
		plannedTodo.ID: db.AudienceAutonomous,
	}
	for id, want := range wantAudience {
		td, ok := s.todoNow(id)
		if !ok {
			t.Fatalf("todo %s missing after re-enable", id)
		}
		if td.Audience != want {
			t.Fatalf("todo %s audience after re-enable = %q, want %q", id, td.Audience, want)
		}
		gotWire, _ := reEnabledRows[id]["audience"].(string)
		if gotWire != want {
			t.Fatalf("todo %s wire audience after re-enable = %q, want %q", id, gotWire, want)
		}
	}
	// Ensure context-based read also sees todos_v2 enabled cleanly.
	if !s.settings(context.Background()).todosV2On() {
		t.Fatalf("expected todosV2On to be true after re-enable")
	}
}

func TestTodosV2CriticalPathRankingAndRollback(t *testing.T) {
	s, _, _ := briefService(t, "Critical path ranking regression")
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	older, err := s.createPieceTodo(ctx, "conv1", "Isolated older ask", "", db.TodoEdit{})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.createPieceTodo(ctx, "conv1", "Release rollout", "", db.TodoEdit{})
	if err != nil {
		t.Fatal(err)
	}
	partOf := parent.ID
	blocker, err := s.createPieceTodo(ctx, "conv1", "Critical path blocker", "", db.TodoEdit{PartOf: &partOf})
	if err != nil {
		t.Fatal(err)
	}
	afterBlocker := []string{blocker.ID}
	mid, err := s.createPieceTodo(ctx, "conv1", "Downstream step 1", "", db.TodoEdit{PartOf: &partOf, After: &afterBlocker})
	if err != nil {
		t.Fatal(err)
	}
	afterMid := []string{mid.ID}
	if _, err := s.createPieceTodo(ctx, "conv1", "Downstream step 2", "", db.TodoEdit{PartOf: &partOf, After: &afterMid}); err != nil {
		t.Fatal(err)
	}

	// Mark both older (-3h) and blocker (-1h) as needing the owner via needs_user review.
	s.todos.mu.Lock()
	olderAt := now.Add(-3 * time.Hour)
	s.todos.todos[older.ID].Supervision = &Supervision{
		State:     reviewNeedsUser,
		UpdatedAt: olderAt,
		Reviews:   []TaskReview{{At: olderAt, Verdict: reviewNeedsUser, Reason: "Approve isolated task?"}},
	}
	blockerAt := now.Add(-1 * time.Hour)
	s.todos.todos[blocker.ID].Supervision = &Supervision{
		State:     reviewNeedsUser,
		UpdatedAt: blockerAt,
		Reviews:   []TaskReview{{At: blockerAt, Verdict: reviewNeedsUser, Reason: "Unblock downstream chain?"}},
	}
	s.todos.mu.Unlock()

	setTodosV2 := func(want, expected bool) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"todos_v2": want, "expected_todos_v2": expected})
		w, _ := permissionSettingsRequest(t, s, "PUT", "/api/v1/assistant", s.managerIdentity(), "full", string(body))
		if w.Code != http.StatusOK {
			t.Fatalf("PUT todos_v2=%t: code=%d body=%s", want, w.Code, w.Body.String())
		}
	}
	getViews := func() map[string]assistantwire.TodoSummary {
		t.Helper()
		r := httptest.NewRequest("GET", "/api/v1/todos", nil)
		r.Header.Set(RoleHeader, "full")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/todos: code=%d body=%s", w.Code, w.Body.String())
		}
		var resp struct {
			Todos []struct {
				ID   string                     `json:"id"`
				View *assistantwire.TodoSummary `json:"view"`
			} `json:"todos"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		out := make(map[string]assistantwire.TodoSummary, len(resp.Todos))
		for _, td := range resp.Todos {
			if td.View != nil {
				out[td.ID] = *td.View
			}
		}
		return out
	}

	// Default (todos_v2=false): older ask ranks ahead of newer blocker.
	legacyViews := getViews()
	if legacyViews[older.ID].Section != assistantwire.TodoListSectionNeedsYou ||
		legacyViews[blocker.ID].Section != assistantwire.TodoListSectionNeedsYou {
		t.Fatalf("expected both in needs_you: older=%+v blocker=%+v", legacyViews[older.ID], legacyViews[blocker.ID])
	}
	if !(legacyViews[older.ID].Rank < legacyViews[blocker.ID].Rank) {
		t.Fatalf("legacy order mismatch: older.rank=%d blocker.rank=%d",
			legacyViews[older.ID].Rank, legacyViews[blocker.ID].Rank)
	}

	// Enable todos_v2: blocker (2 downstream open tasks) ranks ahead of older (0 downstream).
	setTodosV2(true, false)
	v2Views := getViews()
	if !(v2Views[blocker.ID].Rank < v2Views[older.ID].Rank) {
		t.Fatalf("todos_v2 critical-path order mismatch: blocker.rank=%d older.rank=%d",
			v2Views[blocker.ID].Rank, v2Views[older.ID].Rank)
	}

	// Disable todos_v2: legacy order and views are restored instantaneously.
	setTodosV2(false, true)
	rollbackViews := getViews()
	if !reflect.DeepEqual(rollbackViews, legacyViews) {
		t.Fatalf("rollback views mismatch:\ngot:  %+v\nwant: %+v", rollbackViews, legacyViews)
	}
}

func TestTodosV2SharedBlockersAndRollback(t *testing.T) {
	s, _, _ := briefService(t, "Shared blockers aggregation and rollback")
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	prA, err := s.createPieceTodo(ctx, "conv1", "Waiting on PR 88 A", "", db.TodoEdit{})
	if err != nil {
		t.Fatal(err)
	}
	prB, err := s.createPieceTodo(ctx, "conv1", "Waiting on PR 88 B", "", db.TodoEdit{})
	if err != nil {
		t.Fatal(err)
	}
	prClosed, err := s.createPieceTodo(ctx, "conv1", "Closed PR 88 task", "", db.TodoEdit{})
	if err != nil {
		t.Fatal(err)
	}
	targetA, err := s.createPieceTodo(ctx, "conv1", "Waiting on vendor A", "", db.TodoEdit{})
	if err != nil {
		t.Fatal(err)
	}
	targetB, err := s.createPieceTodo(ctx, "conv1", "Waiting on vendor B", "", db.TodoEdit{})
	if err != nil {
		t.Fatal(err)
	}
	singleton, err := s.createPieceTodo(ctx, "conv1", "Singleton blocker task", "", db.TodoEdit{})
	if err != nil {
		t.Fatal(err)
	}

	s.todos.mu.Lock()
	doneAt := now.Add(-time.Hour)
	parkedAt := now.Add(-2 * time.Hour)
	until := now.Add(4 * time.Hour)
	makeParked := func(reason, waitingOn string, ev *ParkEvidence) *Supervision {
		return &Supervision{
			State:     reviewParked,
			UpdatedAt: parkedAt,
			Reviews: []TaskReview{{
				At:        parkedAt,
				Verdict:   reviewParked,
				Reason:    reason,
				Until:     until,
				WaitingOn: waitingOn,
				Evidence:  ev,
			}},
		}
	}
	s.todos.todos[prA.ID].Supervision = makeParked("Waiting on upstream PR", parkExternalEvent, &ParkEvidence{
		Kind:   parkExternalEvent,
		Target: "PR 88",
		URL:    "https://GitHub.com/Acme/Core/pull/88/?w=1#discussion",
	})
	s.todos.todos[prB.ID].PRURL = "https://github.com/acme/core/pull/88/"
	s.todos.todos[prB.ID].PRStatus = &TodoPRStatus{
		URL:       "https://github.com/acme/core/pull/88/",
		State:     "open",
		CheckedAt: now,
	}
	s.todos.todos[prClosed.ID].PRURL = "https://github.com/acme/core/pull/88"
	s.todos.todos[prClosed.ID].DoneAt = &doneAt

	s.todos.todos[targetA.ID].Supervision = makeParked("Waiting on vendor response", parkSomeoneElse, &ParkEvidence{
		Kind:   "person_reply",
		Target: "  Vendor   Ops  ",
	})
	s.todos.todos[targetB.ID].Supervision = makeParked("Waiting on vendor response", parkSomeoneElse, &ParkEvidence{
		Kind:   "person_reply",
		Target: "vendor ops",
	})
	s.todos.todos[singleton.ID].Supervision = makeParked("Waiting on solo issue", parkExternalEvent, &ParkEvidence{
		Kind: parkExternalEvent,
		URL:  "https://github.com/acme/core/issues/501",
	})
	s.todos.mu.Unlock()

	setTodosV2 := func(want, expected bool) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"todos_v2": want, "expected_todos_v2": expected})
		w, _ := permissionSettingsRequest(t, s, "PUT", "/api/v1/assistant", s.managerIdentity(), "full", string(body))
		if w.Code != http.StatusOK {
			t.Fatalf("PUT todos_v2=%t: code=%d body=%s", want, w.Code, w.Body.String())
		}
	}
	getRawList := func() map[string]json.RawMessage {
		t.Helper()
		r := httptest.NewRequest("GET", "/api/v1/todos", nil)
		r.Header.Set(RoleHeader, "full")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/todos: code=%d body=%s", w.Code, w.Body.String())
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}

	// 1. Default (todos_v2=false): shared_blockers key is completely omitted.
	legacyRaw := getRawList()
	if _, has := legacyRaw["shared_blockers"]; has {
		t.Fatalf("todos_v2=false must omit shared_blockers key, got: %s", legacyRaw["shared_blockers"])
	}

	// 2. Enable todos_v2: shared_blockers returns exact deterministic groups (count >= 2 only).
	setTodosV2(true, false)
	v2Raw := getRawList()
	rawBlockers, has := v2Raw["shared_blockers"]
	if !has {
		t.Fatalf("todos_v2=true expected shared_blockers key in response")
	}
	var got []projection.SharedBlocker
	if err := json.Unmarshal(rawBlockers, &got); err != nil {
		t.Fatalf("decode shared_blockers: %v", err)
	}
	prIDs := []string{prA.ID, prB.ID}
	if prIDs[0] > prIDs[1] {
		prIDs[0], prIDs[1] = prIDs[1], prIDs[0]
	}
	tgtIDs := []string{targetA.ID, targetB.ID}
	if tgtIDs[0] > tgtIDs[1] {
		tgtIDs[0], tgtIDs[1] = tgtIDs[1], tgtIDs[0]
	}
	want := []projection.SharedBlocker{
		{
			Key:     "pull_request:https://github.com/acme/core/pull/88",
			Kind:    projection.SharedBlockerKindPullRequest,
			Label:   "acme/core#88",
			URL:     "https://github.com/acme/core/pull/88",
			TodoIDs: prIDs,
			Count:   2,
		},
		{
			Key:     "target:vendor ops",
			Kind:    projection.SharedBlockerKindExternalTarget,
			Label:   "Vendor Ops",
			TodoIDs: tgtIDs,
			Count:   2,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("shared_blockers mismatch:\ngot:  %+v\nwant: %+v", got, want)
	}

	// 3. Disable todos_v2 (rollback): shared_blockers key is immediately omitted again.
	setTodosV2(false, true)
	rollbackRaw := getRawList()
	if _, has := rollbackRaw["shared_blockers"]; has {
		t.Fatalf("rollback to todos_v2=false must omit shared_blockers key, got: %s", rollbackRaw["shared_blockers"])
	}
}

// TestTodosV2DirectionIDLosslessRollback verifies the full additive direction_id lifecycle (#14660):
// todos_v2 off (baseline) -> enable todos_v2 -> write/change/clear direction_id on personal, working
// supervised (with active agent), and completed to-dos via HTTP POST/PATCH and MCP todo_create/todo_update/task_plan
// (asserting supervision.supervisor, active agents, and origin never change) -> disable todos_v2 ->
// assert zero data loss and identical legacy projection/counts -> re-enable todos_v2 -> assert direction_id preserved.
func TestTodosV2DirectionIDLosslessRollback(t *testing.T) {
	s, _, _ := briefService(t, "Organize directions without interrupting running tasks")

	req := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set(RoleHeader, "full")
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}

	setTodosV2 := func(want, expected bool) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"todos_v2": want, "expected_todos_v2": expected})
		w, _ := permissionSettingsRequest(t, s, "PUT", "/api/v1/assistant", s.managerIdentity(), "full", string(body))
		if w.Code != http.StatusOK {
			t.Fatalf("PUT todos_v2=%t: code=%d body=%s", want, w.Code, w.Body.String())
		}
	}
	getTodos := func() (map[string]map[string]any, map[string]any) {
		t.Helper()
		w := req("GET", "/api/v1/todos", "")
		if w.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/todos: code=%d body=%s", w.Code, w.Body.String())
		}
		var payload struct {
			Todos  []map[string]any `json:"todos"`
			Counts map[string]any   `json:"counts"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode GET /api/v1/todos: %v", err)
		}
		byID := make(map[string]map[string]any, len(payload.Todos))
		for _, item := range payload.Todos {
			id, _ := item["id"].(string)
			byID[id] = item
		}
		return byID, payload.Counts
	}
	getTodoItem := func(id string) map[string]any {
		t.Helper()
		w := req("GET", "/api/v1/todos/"+id, "")
		if w.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/todos/%s: code=%d body=%s", id, w.Code, w.Body.String())
		}
		var payload struct {
			Todo map[string]any `json:"todo"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode GET /api/v1/todos/%s: %v", id, err)
		}
		return payload.Todo
	}

	// 1. Baseline with todos_v2=false: legacy to-do omits "direction_id" on the wire.
	w := req("POST", "/api/v1/todos", `{"title":"Legacy baseline reminder","notes":"before todos_v2"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST legacy todo: code=%d body=%s", w.Code, w.Body.String())
	}
	var legacyResp struct {
		Todo Todo `json:"todo"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &legacyResp); err != nil {
		t.Fatal(err)
	}
	legacyID := legacyResp.Todo.ID
	beforeRows, _ := getTodos()
	if _, hasKey := beforeRows[legacyID]["direction_id"]; hasKey {
		t.Fatalf("legacy todo should omit empty direction_id key on wire, got: %v", beforeRows[legacyID])
	}

	// 2. Enable todos_v2.
	setTodosV2(true, false)

	// 3a. Personal reminder via HTTP POST/PATCH: set (trimmed), change, clear (""), and reassign.
	w = req("POST", "/api/v1/todos", `{"title":"Personal reminder","direction_id":"  dir_personal  "}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST personal todo: code=%d body=%s", w.Code, w.Body.String())
	}
	var personalResp struct {
		Todo Todo `json:"todo"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &personalResp); err != nil {
		t.Fatal(err)
	}
	personalID := personalResp.Todo.ID
	if personalResp.Todo.DirectionID != "dir_personal" {
		t.Fatalf("POST personal direction_id = %q, want %q", personalResp.Todo.DirectionID, "dir_personal")
	}
	if w := req("PATCH", "/api/v1/todos/"+personalID, `{"direction_id":"dir_personal_v2"}`); w.Code != http.StatusOK {
		t.Fatalf("PATCH personal direction_id: code=%d body=%s", w.Code, w.Body.String())
	}
	if w := req("PATCH", "/api/v1/todos/"+personalID, `{"direction_id":""}`); w.Code != http.StatusOK {
		t.Fatalf("PATCH clear personal direction_id: code=%d body=%s", w.Code, w.Body.String())
	}
	if got, _ := s.todoNow(personalID); got.DirectionID != "" {
		t.Fatalf("expected cleared personal direction_id, got %q", got.DirectionID)
	}
	if w := req("PATCH", "/api/v1/todos/"+personalID, `{"direction_id":"dir_personal_final"}`); w.Code != http.StatusOK {
		t.Fatalf("PATCH reassign personal direction_id: code=%d body=%s", w.Code, w.Body.String())
	}

	// 3b. MCP todo_create & todo_update (including clearing with "").
	if out, bad := tool(t, s, "todo_create", map[string]any{"title": "MCP direction cleared", "direction_id": "  dir_mcp  "}); bad {
		t.Fatalf("todo_create direction_id: %s", out)
	}
	mcpCleared, ok := todoByTitle(s, "MCP direction cleared")
	if !ok || mcpCleared.DirectionID != "dir_mcp" {
		t.Fatalf("todo_create direction_id saved = %+v (ok=%t)", mcpCleared, ok)
	}
	if out, bad := tool(t, s, "todo_update", map[string]any{"id": mcpCleared.ID, "direction_id": ""}); bad {
		t.Fatalf("todo_update clear direction_id: %s", out)
	}
	if after, _ := s.todoNow(mcpCleared.ID); after.DirectionID != "" {
		t.Fatalf("todo_update clear direction_id left %q, want empty", after.DirectionID)
	}

	// 3c. Working supervised task via MCP task_plan with active Agent + supervision.supervisor.
	if out, bad := tool(t, s, "task_plan", map[string]any{
		"tasks": []map[string]any{{
			"title": "Run live architecture audit", "prompt": "inspect architecture modules and report findings",
			"machine": "box", "cwd": "/tmp", "readonly": true, "role": "worker",
			"goal": "verify architecture modules", "direction_id": "  dir_running  ",
		}},
	}); bad {
		t.Fatalf("task_plan direction_id failed: %s", out)
	}
	// Wait for background automatic titling to finish so UpdatedAt / view.next_change_at stays stable across snapshots.
	s.todos.titling.Wait()
	var workingTodo Todo
	var foundWorking bool
	s.todos.mu.RLock()
	for _, td := range s.todos.todos {
		if td.PlanID != "" {
			workingTodo = copyTodoLocked(td)
			foundWorking = true
			break
		}
	}
	s.todos.mu.RUnlock()
	if !foundWorking || workingTodo.DirectionID != "dir_running" {
		t.Fatalf("task_plan direction_id todo = %+v (found=%t)", workingTodo, foundWorking)
	}

	// Ensure the working task has an explicit supervisor and an active un-released agent so we can assert
	// changing/clearing direction_id never interrupts the running agent or mutates supervision.supervisor / origin.
	const wantSupervisor = "conv_runtime_supervisor"
	s.todos.mu.Lock()
	wt := s.todos.todos[workingTodo.ID]
	if wt.Supervision == nil {
		wt.Supervision = &Supervision{}
	}
	wt.Supervision.Supervisor = wantSupervisor
	if len(wt.Agents) == 0 {
		wt.Agents = []TodoAgent{{
			MachineID: "box",
			SessionID: "sess_live_worker",
			LinkedAt:  time.Now().UTC(),
			Title:     "Live Worker",
			Cwd:       "/tmp",
			Agent:     "claude",
			Source:    "plan",
		}}
		s.todos.activeBySession[[2]string{"box", "sess_live_worker"}] = wt.ID
	}
	wantOrigin := wt.Origin
	wantAgentSession := wt.Agents[0].SessionID
	s.todos.mu.Unlock()

	assertWorkingUntouched := func(step, wantDir string) {
		t.Helper()
		cur, ok := s.todoNow(workingTodo.ID)
		if !ok {
			t.Fatalf("%s: working todo missing", step)
		}
		if cur.DirectionID != wantDir {
			t.Fatalf("%s: direction_id=%q, want %q", step, cur.DirectionID, wantDir)
		}
		if cur.Origin != wantOrigin {
			t.Fatalf("%s: origin mutated: got %q, want %q", step, cur.Origin, wantOrigin)
		}
		if cur.Supervision == nil || cur.Supervision.Supervisor != wantSupervisor {
			t.Fatalf("%s: supervision.supervisor mutated: got %+v, want %q", step, cur.Supervision, wantSupervisor)
		}
		if len(cur.Agents) == 0 || cur.Agents[0].SessionID != wantAgentSession || cur.Agents[0].ReleasedAt != nil {
			t.Fatalf("%s: active agent interrupted or released: %+v", step, cur.Agents)
		}
	}

	if w := req("PATCH", "/api/v1/todos/"+workingTodo.ID, `{"direction_id":"dir_reorg"}`); w.Code != http.StatusOK {
		t.Fatalf("PATCH working direction_id: code=%d body=%s", w.Code, w.Body.String())
	}
	assertWorkingUntouched("PATCH reorg", "dir_reorg")

	if out, bad := tool(t, s, "todo_update", map[string]any{"id": workingTodo.ID, "direction_id": ""}); bad {
		t.Fatalf("todo_update clear working direction_id: %s", out)
	}
	assertWorkingUntouched("MCP clear", "")

	if out, bad := tool(t, s, "todo_update", map[string]any{"id": workingTodo.ID, "direction_id": "dir_running_final"}); bad {
		t.Fatalf("todo_update reassign working direction_id: %s", out)
	}
	assertWorkingUntouched("MCP reassign", "dir_running_final")

	// 3d. Completed to-do: assign, change, clear, and reassign direction_id without reopening.
	w = req("POST", "/api/v1/todos", `{"title":"Completed milestone"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST completed todo: code=%d body=%s", w.Code, w.Body.String())
	}
	var completedResp struct {
		Todo Todo `json:"todo"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &completedResp); err != nil {
		t.Fatal(err)
	}
	completedID := completedResp.Todo.ID
	if w := req("PATCH", "/api/v1/todos/"+completedID, `{"done":true}`); w.Code != http.StatusOK {
		t.Fatalf("PATCH mark completed: code=%d body=%s", w.Code, w.Body.String())
	}
	if w := req("PATCH", "/api/v1/todos/"+completedID, `{"direction_id":"dir_done"}`); w.Code != http.StatusOK {
		t.Fatalf("PATCH completed direction_id: code=%d body=%s", w.Code, w.Body.String())
	}
	if out, bad := tool(t, s, "todo_update", map[string]any{"id": completedID, "direction_id": ""}); bad {
		t.Fatalf("todo_update clear completed direction_id: %s", out)
	}
	if out, bad := tool(t, s, "todo_update", map[string]any{"id": completedID, "direction_id": "dir_done_final"}); bad {
		t.Fatalf("todo_update reassign completed direction_id: %s", out)
	}
	if doneTodo, _ := s.todoNow(completedID); doneTodo.DoneAt == nil || doneTodo.DirectionID != "dir_done_final" {
		t.Fatalf("completed todo state unexpected: done_at=%v direction_id=%q", doneTodo.DoneAt, doneTodo.DirectionID)
	}

	// Snapshot list, views, and counts while todos_v2=true.
	onRows, onCounts := getTodos()
	if len(onRows) != 5 {
		t.Fatalf("expected 5 todos while todos_v2=true, got %d", len(onRows))
	}

	// 4. Disable todos_v2 (rollback).
	setTodosV2(false, true)

	// 5. Assert zero data loss, identical projection/counts, untouched legacy wire fields, and clean single-item read.
	offRows, offCounts := getTodos()
	if len(offRows) != len(onRows) {
		t.Fatalf("rollback changed todo count: off=%d on=%d", len(offRows), len(onRows))
	}
	if !reflect.DeepEqual(offCounts, onCounts) {
		t.Fatalf("rollback changed counts: off=%v on=%v", offCounts, onCounts)
	}
	for id, onRow := range onRows {
		offRow, exists := offRows[id]
		if !exists {
			t.Fatalf("todo %s lost after rollback", id)
		}
		// The time-derived cursor and rank can move between two reads; compare placement and content instead.
		stable := func(v any) any {
			m := maps.Clone(v.(map[string]any))
			delete(m, "next_change_at")
			delete(m, "rank")
			return m
		}
		if !reflect.DeepEqual(stable(offRow["view"]), stable(onRow["view"])) {
			t.Fatalf("todo %s view changed on rollback: off=%v on=%v", id, offRow["view"], onRow["view"])
		}
		for _, legacyKey := range []string{"id", "title", "notes", "created_at", "updated_at", "spaces", "agents", "quiet", "view"} {
			if _, ok := offRow[legacyKey]; !ok {
				t.Errorf("todo %s missing legacy wire key %q after rollback", id, legacyKey)
			}
		}
	}
	if _, hasKey := offRows[legacyID]["direction_id"]; hasKey {
		t.Fatalf("legacy todo unexpectedly gained direction_id key after rollback: %v", offRows[legacyID])
	}
	if _, hasKey := offRows[mcpCleared.ID]["direction_id"]; hasKey {
		t.Fatalf("cleared todo unexpectedly kept direction_id key after rollback: %v", offRows[mcpCleared.ID])
	}
	singleWorking := getTodoItem(workingTodo.ID)
	if singleWorking["direction_id"] != "dir_running_final" {
		t.Fatalf("GET /api/v1/todos/%s direction_id = %v, want %q", workingTodo.ID, singleWorking["direction_id"], "dir_running_final")
	}
	assertWorkingUntouched("rollback off", "dir_running_final")

	// 6. Re-enable todos_v2 and confirm all stored direction_id values remain intact.
	setTodosV2(true, false)
	reEnabledRows, reEnabledCounts := getTodos()
	if !reflect.DeepEqual(reEnabledCounts, onCounts) {
		t.Fatalf("counts changed after re-enable: got=%v want=%v", reEnabledCounts, onCounts)
	}
	wantDirection := map[string]string{
		legacyID:       "",
		mcpCleared.ID:  "",
		personalID:     "dir_personal_final",
		workingTodo.ID: "dir_running_final",
		completedID:    "dir_done_final",
	}
	for id, want := range wantDirection {
		td, ok := s.todoNow(id)
		if !ok {
			t.Fatalf("todo %s missing after re-enable", id)
		}
		if td.DirectionID != want {
			t.Fatalf("todo %s direction_id after re-enable = %q, want %q", id, td.DirectionID, want)
		}
		gotWire, _ := reEnabledRows[id]["direction_id"].(string)
		if gotWire != want {
			t.Fatalf("todo %s wire direction_id after re-enable = %q, want %q", id, gotWire, want)
		}
	}
	assertWorkingUntouched("re-enable", "dir_running_final")
}
