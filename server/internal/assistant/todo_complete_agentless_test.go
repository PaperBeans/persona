package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// todo_complete asks on the phone only when completing the to-do has a real side effect (crossing conversation or
// supervisor ownership, or stopping/closing a live or hanging linked Agent). A timeout turns an unexpected card in
// the direct cases into a failure instead of a hanging test.
func TestTodoCompleteAgentlessSupervisor(t *testing.T) {
	direct := func(t *testing.T, e *todoEnv, self, tool, id string) (string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		raw, _ := json.Marshal(map[string]any{"id": id})
		return e.s.toolTodoDone(ctx, "server", self, tool, raw)
	}

	released := time.Now().UTC().Add(-time.Minute)
	dismissed := time.Now().UTC().Add(-2 * time.Minute)
	for _, tc := range []struct {
		name     string
		todo     Todo
		sessions []proto.Session
	}{
		{
			name: "supervised memo with default settings",
			todo: Todo{ID: "todo_supervised", Title: "Memo", Origin: "conv1", Supervision: &Supervision{Supervisor: "conv1"}},
		},
		{
			name: "originating conversation without a supervision ledger",
			todo: Todo{ID: "todo_origin", Title: "Memo", Origin: "conv1"},
		},
		{
			name: "planned task without an attached Agent",
			todo: Todo{ID: "todo_planned", Title: "Task", Origin: "conv1", PlanID: "plan_1", Supervision: &Supervision{Supervisor: "conv1"}},
		},
		{
			name: "task with only released, dismissed, offline, or stopped Agents",
			todo: Todo{
				ID:     "todo_ended_agents",
				Title:  "Finished work",
				Origin: "conv1",
				Agents: []TodoAgent{
					{MachineID: "server", SessionID: "agent_released", ReleasedAt: &released},
					{MachineID: "server", SessionID: "agent_dismissed", DismissedAt: &dismissed},
					{MachineID: "server", SessionID: "agent_offline"},
					{MachineID: "server", SessionID: "agent_stopped"},
				},
			},
			sessions: []proto.Session{
				{SessionID: "agent_offline", MachineID: "server", Status: proto.StatusOffline, State: proto.StateOffline},
				{SessionID: "agent_stopped", MachineID: "server", Status: proto.StatusDone, ReadOnlyReason: "stopped"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTodoEnv(t, nil)
			if len(tc.sessions) > 0 {
				e.f.setSessions("server", tc.sessions...)
			}
			putTodo(e.s, tc.todo)
			res, err := direct(t, e, "conv1", "todo_complete", tc.todo.ID)
			if err != nil || !strings.HasPrefix(res, "Completed: ") {
				t.Fatalf("direct completion: %s %v", res, err)
			}
			if got := todoNow(e.s, tc.todo.ID); got.DoneAt == nil {
				t.Fatal("not done")
			}
			if len(e.s.pendingQuestions()) != 0 {
				t.Fatal("same-session completion without active agents showed a card")
			}
		})
	}

	done := time.Now().UTC()
	for _, tc := range []struct {
		name, self, tool string
		todo             Todo
		sessions         []proto.Session
		sessionsFail     bool
		health           bool
	}{
		{
			name: "another supervisor",
			self: "conv1",
			tool: "todo_complete",
			todo: Todo{ID: "todo_other_supervisor", Title: "Memo", Origin: "conv1", Supervision: &Supervision{Supervisor: "conv2"}},
		},
		{
			name: "another originating conversation",
			self: "conv1",
			tool: "todo_complete",
			todo: Todo{ID: "todo_other_origin", Title: "Memo", Origin: "conv2"},
		},
		{
			name: "unknown caller on originated task",
			self: "",
			tool: "todo_complete",
			todo: Todo{ID: "todo_unknown_caller", Title: "Memo", Origin: "conv1"},
		},
		{
			name:     "live running Agent",
			self:     "conv1",
			tool:     "todo_complete",
			todo:     Todo{ID: "todo_live_agent", Title: "Task", Origin: "conv1", Supervision: &Supervision{Supervisor: "conv1"}, Agents: []TodoAgent{{MachineID: "server", SessionID: "agent_live"}}},
			sessions: []proto.Session{{SessionID: "agent_live", MachineID: "server", Status: proto.StatusWorking, State: proto.StateWorking}},
		},
		{
			name:     "hanging blocked Agent",
			self:     "conv1",
			tool:     "todo_complete",
			todo:     Todo{ID: "todo_blocked_agent", Title: "Task", Origin: "conv1", Agents: []TodoAgent{{MachineID: "server", SessionID: "agent_blocked"}}},
			sessions: []proto.Session{{SessionID: "agent_blocked", MachineID: "server", Status: proto.StatusBlocked, State: proto.StateNeedsYou}},
		},
		{
			name:     "hanging idle Agent with open pane",
			self:     "conv1",
			tool:     "todo_complete",
			todo:     Todo{ID: "todo_idle_agent", Title: "Task", Origin: "conv1", Agents: []TodoAgent{{MachineID: "server", SessionID: "agent_idle"}}},
			sessions: []proto.Session{{SessionID: "agent_idle", MachineID: "server", Status: proto.StatusDone, PaneID: "%1"}},
		},
		{
			name:         "unreachable machine with unreleased Agent",
			self:         "conv1",
			tool:         "todo_complete",
			todo:         Todo{ID: "todo_unreachable_agent", Title: "Task", Origin: "conv1", Agents: []TodoAgent{{MachineID: "server", SessionID: "agent_remote"}}},
			sessionsFail: true,
		},
		{
			name: "reopen",
			self: "conv1",
			tool: "todo_reopen",
			todo: Todo{ID: "todo_reopen", Title: "Memo", Origin: "conv1", DoneAt: &done, Supervision: &Supervision{Supervisor: "conv1"}},
		},
		{
			name: "delete",
			self: "conv1",
			tool: "todo_delete",
			todo: Todo{ID: "todo_delete_confirm", Title: "Memo", Origin: "conv1"},
		},
		{
			name:   "health-space outbound confirmation",
			self:   "conv1",
			tool:   "todo_complete",
			todo:   Todo{ID: "todo_health", Title: "Memo", Origin: "conv1", Supervision: &Supervision{Supervisor: "conv1"}},
			health: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTodoEnv(t, nil)
			e.f.hello.Assistant.AutoApproveReversibleSettings = true
			sessions := append([]proto.Session(nil), tc.sessions...)
			if tc.health {
				sessions = append(sessions, proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Space: SpaceHealth})
			}
			if len(sessions) > 0 {
				e.f.setSessions("server", sessions...)
			}
			if tc.sessionsFail {
				e.f.mu.Lock()
				if e.f.sessionsFail == nil {
					e.f.sessionsFail = map[string]int{}
				}
				e.f.sessionsFail["server"] = 1
				e.f.mu.Unlock()
			}
			putTodo(e.s, tc.todo)
			shown := make(chan proto.Interaction, 1)
			go answerArchiveCard(t, e.s, false, shown)
			res, err := runJSON(t, e.s, tc.self, tc.tool, map[string]any{"id": tc.todo.ID})
			if err != nil || !strings.Contains(res, "nothing changed") {
				t.Fatalf("declined change: %s %v", res, err)
			}
			if card := <-shown; card.ToolName != tc.tool {
				t.Fatalf("card: %+v", card)
			}
			got, ok := e.s.todoNow(tc.todo.ID)
			if !ok {
				t.Fatalf("todo %s vanished after refusal", tc.todo.ID)
			}
			if tc.todo.DoneAt == nil && got.DoneAt != nil || tc.todo.DoneAt != nil && got.DoneAt == nil {
				t.Fatalf("done state changed after refusal: before=%v after=%v", tc.todo.DoneAt, got.DoneAt)
			}
		})
	}
}
