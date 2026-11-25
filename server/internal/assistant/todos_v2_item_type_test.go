package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/repository"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestTodosV2ItemTypeInferenceAndExplicit(t *testing.T) {
	cases := []struct {
		name         string
		todo         Todo
		hasRepoLinks bool
		want         string
	}{
		{
			name: "plain personal reminder",
			todo: Todo{ID: "t1", Title: "Buy coffee"},
			want: db.ItemTypePersonal,
		},
		{
			name: "explicit personal overrides task signals",
			todo: Todo{ID: "t2", Title: "Personal note", ItemType: db.ItemTypePersonal, Origin: "conv_1", PlanID: "plan_1"},
			want: db.ItemTypePersonal,
		},
		{
			name: "explicit task overrides plain personal",
			todo: Todo{ID: "t3", Title: "Milestone rollup", ItemType: db.ItemTypeTask},
			want: db.ItemTypeTask,
		},
		{
			name: "inferred task from bound agent",
			todo: Todo{ID: "t4", Title: "Work", Agents: []TodoAgent{{MachineID: "m1", SessionID: "s1"}}},
			want: db.ItemTypeTask,
		},
		{
			name: "inferred task from plan_id",
			todo: Todo{ID: "t5", Title: "Planned", PlanID: "plan_42"},
			want: db.ItemTypeTask,
		},
		{
			name: "inferred task from supervision done_when",
			todo: Todo{ID: "t6", Title: "Criteria", Supervision: &Supervision{DoneWhen: []string{"tests pass"}}},
			want: db.ItemTypeTask,
		},
		{
			name: "inferred task from supervisor",
			todo: Todo{ID: "t7", Title: "Supervised", Supervision: &Supervision{Supervisor: "lead_1"}},
			want: db.ItemTypeTask,
		},
		{
			name: "inferred task from brief done_when",
			todo: Todo{ID: "t8", Title: "Briefed", Brief: &TaskBrief{DoneWhen: []string{"merged"}}},
			want: db.ItemTypeTask,
		},
		{
			name: "inferred task from part_of",
			todo: Todo{ID: "t9", Title: "Subtask", PartOf: "todo_parent"},
			want: db.ItemTypeTask,
		},
		{
			name: "inferred task from origin",
			todo: Todo{ID: "t10", Title: "From chat", Origin: "conv_main"},
			want: db.ItemTypeTask,
		},
		{
			name:         "inferred task from repository binding",
			todo:         Todo{ID: "t11", Title: "Repo bound"},
			hasRepoLinks: true,
			want:         db.ItemTypeTask,
		},
		{
			name: "inferred task from kind=task",
			todo: Todo{ID: "t12", Title: "Explicit kind", Kind: db.ItemTypeTask},
			want: db.ItemTypeTask,
		},
		{
			name: "concern without task signals resolves to personal without mutating kind",
			todo: Todo{ID: "t13", Title: "Watch deployment", Kind: db.KindConcern},
			want: db.ItemTypePersonal,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveTodoItemType(tc.todo, tc.hasRepoLinks); got != tc.want {
				t.Fatalf("effectiveTodoItemType(%+v, %t) = %q, want %q", tc.todo, tc.hasRepoLinks, got, tc.want)
			}
		})
	}
}

func TestTodosV2ItemTypeHTTPAndToolGatingAndRollback(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	st := &fakeTodoRepoStore{
		planMemStore: &planMemStore{memStore: &memStore{}},
		repos:        map[string][]repository.TaskRepository{},
	}
	f := newFakeHub().withMachine()
	s := newServiceOn(t, f, st)

	plainID := "todo_it_plain"
	repoID := "todo_it_repo"
	originID := "todo_it_origin"

	s.todos.mu.Lock()
	s.todos.loaded = true
	s.todos.todos = map[string]*Todo{
		plainID: {
			ID:        plainID,
			Title:     "Call dentist",
			CreatedAt: now.Add(-3 * time.Minute),
			UpdatedAt: now.Add(-3 * time.Minute),
		},
		repoID: {
			ID:        repoID,
			Title:     "Repo-linked item",
			CreatedAt: now.Add(-2 * time.Minute),
			UpdatedAt: now.Add(-2 * time.Minute),
		},
		originID: {
			ID:        originID,
			Title:     "Milestone rollup",
			Origin:    "conv_main",
			CreatedAt: now.Add(-1 * time.Minute),
			UpdatedAt: now.Add(-1 * time.Minute),
		},
	}
	s.todos.mu.Unlock()

	st.repos[repoID] = []repository.TaskRepository{
		{
			RepoID: "repo_1",
			SourceSnapshot: repository.SourceSnapshot{
				Identity: repository.Identity{
					Provider:     "github",
					InstanceURL:  "https://github.com",
					Path:         "org/app",
					CanonicalURL: "https://github.com/org/app",
				},
			},
		},
	}

	getTodosRaw := func() map[string]map[string]any {
		t.Helper()
		r := httptest.NewRequest("GET", "/api/v1/todos", nil)
		r.Header.Set(RoleHeader, "full")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/todos: code=%d body=%s", w.Code, w.Body.String())
		}
		var payload struct {
			Todos []map[string]any `json:"todos"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode GET /api/v1/todos: %v", err)
		}
		byID := make(map[string]map[string]any, len(payload.Todos))
		for _, row := range payload.Todos {
			id, _ := row["id"].(string)
			byID[id] = row
		}
		return byID
	}

	getTodoItemRaw := func(id string) map[string]any {
		t.Helper()
		r := httptest.NewRequest("GET", "/api/v1/todos/"+id, nil)
		r.Header.Set(RoleHeader, "full")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
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

	setTodosV2 := func(want, expected bool) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"todos_v2": want, "expected_todos_v2": expected})
		w, _ := permissionSettingsRequest(t, s, "PUT", "/api/v1/assistant", s.managerIdentity(), "full", string(body))
		if w.Code != http.StatusOK {
			t.Fatalf("PUT todos_v2=%t: code=%d body=%s", want, w.Code, w.Body.String())
		}
	}

	// Phase 1: todos_v2=false (default) -> item_type must be completely omitted on wire.
	before := getTodosRaw()
	for _, id := range []string{plainID, repoID, originID} {
		if _, has := before[id]["item_type"]; has {
			t.Fatalf("todos_v2=false: todo %s unexpectedly emitted item_type: %v", id, before[id]["item_type"])
		}
	}
	if _, has := getTodoItemRaw(originID)["item_type"]; has {
		t.Fatalf("todos_v2=false: GET /api/v1/todos/%s unexpectedly emitted item_type", originID)
	}

	// Phase 2: enable todos_v2=true -> inferred & explicit item_type are returned, tools accept & validate item_type.
	setTodosV2(true, false)
	enabled := getTodosRaw()
	if got := enabled[plainID]["item_type"]; got != db.ItemTypePersonal {
		t.Fatalf("plainID item_type = %v, want %q", got, db.ItemTypePersonal)
	}
	if got := enabled[repoID]["item_type"]; got != db.ItemTypeTask {
		t.Fatalf("repoID item_type = %v, want %q", got, db.ItemTypeTask)
	}
	if got := enabled[originID]["item_type"]; got != db.ItemTypeTask {
		t.Fatalf("originID item_type = %v, want %q", got, db.ItemTypeTask)
	}
	if got := getTodoItemRaw(originID)["item_type"]; got != db.ItemTypeTask {
		t.Fatalf("GET /api/v1/todos/%s item_type = %v, want %q", originID, got, db.ItemTypeTask)
	}

	// todo_create with explicit item_type="task" and invalid item_type error.
	res, err := runJSON(t, s, "conv_main", "todo_create", map[string]any{
		"title":     "Explicit task without agents",
		"item_type": "task",
	})
	if err != nil || !strings.HasPrefix(res, "Created: ") {
		t.Fatalf("todo_create explicit task: res=%q err=%v", res, err)
	}
	var createdID string
	for id, row := range getTodosRaw() {
		if row["title"] == "Explicit task without agents" {
			createdID = id
			if row["item_type"] != db.ItemTypeTask {
				t.Fatalf("created todo item_type = %v, want task", row["item_type"])
			}
		}
	}
	if createdID == "" {
		t.Fatal("created todo not found in list")
	}

	if _, err := runJSON(t, s, "conv_main", "todo_create", map[string]any{
		"title":     "Bad type",
		"item_type": "epic",
	}); err == nil {
		t.Fatal("expected todo_create with invalid item_type to fail")
	}

	// todo_update changing item_type to personal, rejecting invalid, and clearing back to null.
	if res, err := runJSON(t, s, "conv_main", "todo_update", map[string]any{
		"id":        createdID,
		"item_type": "personal",
	}); err != nil || !strings.HasPrefix(res, "Updated: ") {
		t.Fatalf("todo_update to personal: res=%q err=%v", res, err)
	}
	if got := getTodoItemRaw(createdID)["item_type"]; got != db.ItemTypePersonal {
		t.Fatalf("after update to personal: item_type = %v, want personal", got)
	}
	if _, err := runJSON(t, s, "conv_main", "todo_update", map[string]any{
		"id":        createdID,
		"item_type": "bogus",
	}); err == nil {
		t.Fatal("expected todo_update with invalid item_type to fail")
	}

	// Phase 3: lossless rollback off -> on -> off.
	setTodosV2(false, true)
	rolledBack := getTodosRaw()
	for _, id := range []string{plainID, repoID, originID, createdID} {
		if _, has := rolledBack[id]["item_type"]; has {
			t.Fatalf("after rollback todos_v2=false: todo %s unexpectedly emitted item_type: %v", id, rolledBack[id]["item_type"])
		}
	}
	// Re-enabling todos_v2 restores the stored explicit value.
	setTodosV2(true, false)
	if got := getTodoItemRaw(createdID)["item_type"]; got != db.ItemTypePersonal {
		t.Fatalf("after re-enabling todos_v2: item_type = %v, want personal", got)
	}
}

func TestTodosV2TodoCompleteDirectAndLiveAgentConfirmation(t *testing.T) {
	directComplete := func(t *testing.T, e *todoEnv, self, id string) (string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		raw, _ := json.Marshal(map[string]any{"id": id})
		return e.s.toolTodoDone(ctx, "server", self, "todo_complete", raw)
	}

	setTodosV2 := func(t *testing.T, s *Service, want, expected bool) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"todos_v2": want, "expected_todos_v2": expected})
		w, _ := permissionSettingsRequest(t, s, "PUT", "/api/v1/assistant", s.managerIdentity(), "full", string(body))
		if w.Code != http.StatusOK {
			t.Fatalf("PUT todos_v2=%t: code=%d body=%s", want, w.Code, w.Body.String())
		}
	}

	t.Run("todos_v2=false completes same-session agent-free task directly and confirms cross-session", func(t *testing.T) {
		e := newTodoEnv(t, nil)
		putTodo(e.s, Todo{
			ID:          "todo_default_rollup",
			Title:       "Phase 3 milestone rollup",
			Origin:      "conv1",
			PlanID:      "plan_1",
			Supervision: &Supervision{Supervisor: "conv1"},
		})
		res, err := directComplete(t, e, "conv1", "todo_default_rollup")
		if err != nil || !strings.HasPrefix(res, "Completed: ") {
			t.Fatalf("same-session direct completion: res=%q err=%v", res, err)
		}
		if got := todoNow(e.s, "todo_default_rollup"); got.DoneAt == nil {
			t.Fatal("expected todo_default_rollup to be marked done")
		}
		if len(e.s.pendingQuestions()) != 0 {
			t.Fatal("unexpected phone confirmation card for same-session agent-free task")
		}

		putTodo(e.s, Todo{
			ID:          "todo_default_cross_session",
			Title:       "Another session's rollup",
			Origin:      "conv2",
			PlanID:      "plan_1",
			Supervision: &Supervision{Supervisor: "conv2"},
		})
		shown := make(chan proto.Interaction, 1)
		go answerArchiveCard(t, e.s, false, shown)
		res, err = e.tool("todo_complete", map[string]any{"id": "todo_default_cross_session"})
		if err != nil || !strings.Contains(res, "declined") {
			t.Fatalf("cross-session completion: res=%q err=%v", res, err)
		}
		if card := <-shown; card.ToolName != "todo_complete" {
			t.Fatalf("unexpected card: %+v", card)
		}
		if got := todoNow(e.s, "todo_default_cross_session"); got.DoneAt != nil {
			t.Fatal("completed cross-session task despite decline")
		}
	})

	t.Run("todos_v2=true completes agent-free task directly without phone card", func(t *testing.T) {
		e := newTodoEnv(t, nil)
		setTodosV2(t, e.s, true, false)
		putTodo(e.s, Todo{
			ID:          "todo_v2_rollup",
			Title:       "Phase 3 milestone rollup",
			Origin:      "conv1",
			PlanID:      "plan_1",
			Supervision: &Supervision{Supervisor: "conv1"},
			ItemType:    db.ItemTypeTask,
		})
		res, err := directComplete(t, e, "conv1", "todo_v2_rollup")
		if err != nil || !strings.HasPrefix(res, "Completed: ") {
			t.Fatalf("v2 direct completion: res=%q err=%v", res, err)
		}
		if got := todoNow(e.s, "todo_v2_rollup"); got.DoneAt == nil {
			t.Fatal("expected todo_v2_rollup to be marked done")
		}
		if len(e.s.pendingQuestions()) != 0 {
			t.Fatal("unexpected phone confirmation card for agent-free task")
		}
	})

	t.Run("todos_v2=true completes task whose linked agents are all released or offline", func(t *testing.T) {
		e := newTodoEnv(t, nil)
		setTodosV2(t, e.s, true, false)
		released := time.Now().UTC().Add(-time.Minute)
		e.f.setSessions("server", proto.Session{
			SessionID: "agent_done",
			MachineID: "server",
			Status:    proto.StatusOffline,
			State:     proto.StateOffline,
		})
		putTodo(e.s, Todo{
			ID:          "todo_v2_ended_agents",
			Title:       "Task with finished agents",
			Origin:      "conv1",
			Supervision: &Supervision{Supervisor: "conv1"},
			Agents: []TodoAgent{
				{MachineID: "server", SessionID: "agent_released", ReleasedAt: &released},
				{MachineID: "server", SessionID: "agent_done"},
			},
		})
		res, err := directComplete(t, e, "conv1", "todo_v2_ended_agents")
		if err != nil || !strings.HasPrefix(res, "Completed: ") {
			t.Fatalf("v2 ended-agent completion: res=%q err=%v", res, err)
		}
		if got := todoNow(e.s, "todo_v2_ended_agents"); got.DoneAt == nil {
			t.Fatal("expected todo_v2_ended_agents to be marked done")
		}
	})

	t.Run("todos_v2=true still requires phone confirmation when a live agent is running", func(t *testing.T) {
		e := newTodoEnv(t, nil)
		setTodosV2(t, e.s, true, false)
		e.f.setSessions("server", proto.Session{
			SessionID: "agent_live",
			MachineID: "server",
			Status:    proto.StatusWorking,
			State:     proto.StateWorking,
		})
		putTodo(e.s, Todo{
			ID:          "todo_v2_live_agent",
			Title:       "Task with live agent",
			Origin:      "conv1",
			Supervision: &Supervision{Supervisor: "conv1"},
			Agents:      []TodoAgent{{MachineID: "server", SessionID: "agent_live"}},
		})
		shown := make(chan proto.Interaction, 1)
		go answerArchiveCard(t, e.s, false, shown)
		res, err := e.tool("todo_complete", map[string]any{"id": "todo_v2_live_agent"})
		if err != nil || !strings.Contains(res, "declined") {
			t.Fatalf("v2 live-agent completion: res=%q err=%v", res, err)
		}
		if card := <-shown; card.ToolName != "todo_complete" {
			t.Fatalf("expected todo_complete confirmation card, got: %+v", card)
		}
		if got := todoNow(e.s, "todo_v2_live_agent"); got.DoneAt != nil {
			t.Fatal("completed live-agent task despite decline")
		}
	})

	t.Run("todos_v2=true still requires phone confirmation for cross-session completion", func(t *testing.T) {
		e := newTodoEnv(t, nil)
		setTodosV2(t, e.s, true, false)
		putTodo(e.s, Todo{
			ID:          "todo_v2_cross_session",
			Title:       "Owned by another conversation",
			Origin:      "conv2",
			Supervision: &Supervision{Supervisor: "conv2"},
			ItemType:    db.ItemTypeTask,
		})
		shown := make(chan proto.Interaction, 1)
		go answerArchiveCard(t, e.s, false, shown)
		res, err := e.tool("todo_complete", map[string]any{"id": "todo_v2_cross_session"})
		if err != nil || !strings.Contains(res, "declined") {
			t.Fatalf("v2 cross-session completion: res=%q err=%v", res, err)
		}
		if card := <-shown; card.ToolName != "todo_complete" {
			t.Fatalf("expected todo_complete confirmation card, got: %+v", card)
		}
		if got := todoNow(e.s, "todo_v2_cross_session"); got.DoneAt != nil {
			t.Fatal("completed cross-session task despite decline")
		}
	})

	t.Run("todos_v2=true still refuses todo_complete for concern", func(t *testing.T) {
		e := newTodoEnv(t, nil)
		setTodosV2(t, e.s, true, false)
		putTodo(e.s, Todo{
			ID:    "todo_v2_concern",
			Title: "Check production alert",
			Kind:  db.KindConcern,
		})
		_, err := directComplete(t, e, "conv1", "todo_v2_concern")
		if err == nil || !strings.Contains(err.Error(), "concern_report") {
			t.Fatalf("expected concern_report refusal, got err=%v", err)
		}
		if got := todoNow(e.s, "todo_v2_concern"); got.DoneAt != nil {
			t.Fatal("concern was completed via todo_complete")
		}
	})

	t.Run("rollback off->on->off keeps side-effect confirmation boundaries", func(t *testing.T) {
		e := newTodoEnv(t, nil)
		setTodosV2(t, e.s, true, false)
		setTodosV2(t, e.s, false, true)
		putTodo(e.s, Todo{
			ID:          "todo_rollback_direct",
			Title:       "Same-session rollup after rollback",
			Origin:      "conv1",
			Supervision: &Supervision{Supervisor: "conv1"},
		})
		res, err := directComplete(t, e, "conv1", "todo_rollback_direct")
		if err != nil || !strings.HasPrefix(res, "Completed: ") {
			t.Fatalf("post-rollback same-session completion: res=%q err=%v", res, err)
		}
		if got := todoNow(e.s, "todo_rollback_direct"); got.DoneAt == nil {
			t.Fatal("expected todo_rollback_direct to be marked done")
		}

		putTodo(e.s, Todo{
			ID:          "todo_rollback_cross",
			Title:       "Cross-session rollup after rollback",
			Origin:      "conv2",
			Supervision: &Supervision{Supervisor: "conv2"},
		})
		shown := make(chan proto.Interaction, 1)
		go answerArchiveCard(t, e.s, false, shown)
		res, err = e.tool("todo_complete", map[string]any{"id": "todo_rollback_cross"})
		if err != nil || !strings.Contains(res, "declined") {
			t.Fatalf("post-rollback cross-session completion: res=%q err=%v", res, err)
		}
		if card := <-shown; card.ToolName != "todo_complete" {
			t.Fatalf("expected todo_complete card after rollback, got: %+v", card)
		}
		if got := todoNow(e.s, "todo_rollback_cross"); got.DoneAt != nil {
			t.Fatal("completed cross-session task after rollback despite decline")
		}
	})
}
