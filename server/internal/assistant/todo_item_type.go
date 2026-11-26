package assistant

import (
	"context"
	"strings"

	"github.com/nwverse/persona/server/internal/assistant/repository"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// todoItemType validates an optional explicit item_type: "", "personal", or "task" (#15758).
func todoItemType(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if err := db.ValidateTodoItemType(trimmed); err != nil {
		return "", err
	}
	return trimmed, nil
}

// effectiveTodoItemType returns the explicit 2-type classification ("personal" or "task") of t (#15758).
// When t.ItemType is explicitly set to "personal" or "task", that value wins.
// Otherwise, it deterministically infers "task" when t carries task signals (bound agents, a plan, done_when criteria,
// a part_of parent, a task origin conversation, bound repositories, task kind, or a supervisor), and "personal"
// otherwise (leaving internal Kind values such as "concern" intact).
func effectiveTodoItemType(t Todo, hasRepoLinks bool) string {
	switch strings.TrimSpace(t.ItemType) {
	case db.ItemTypePersonal:
		return db.ItemTypePersonal
	case db.ItemTypeTask:
		return db.ItemTypeTask
	}
	if len(t.Agents) > 0 ||
		t.PlanID != "" ||
		t.PartOf != "" ||
		t.Origin != "" ||
		hasRepoLinks ||
		t.Kind == db.ItemTypeTask ||
		(t.Supervision != nil && (len(t.Supervision.DoneWhen) > 0 || strings.TrimSpace(t.Supervision.Supervisor) != "")) ||
		(t.Brief != nil && len(t.Brief.DoneWhen) > 0) {
		return db.ItemTypeTask
	}
	return db.ItemTypePersonal
}

// populateTodoItemTypes fills listedTodo.ItemType for every row in list using the preloaded repository map.
// Callers must invoke this only when todos_v2 is enabled so todos_v2=false omits item_type on the wire.
func populateTodoItemTypes(list []listedTodo, reposByTodo map[string][]repository.TaskRepository) {
	for i := range list {
		list[i].ItemType = effectiveTodoItemType(list[i].Todo, len(reposByTodo[list[i].ID]) > 0)
	}
}

// todoSessionActive reports whether session c is currently running/live (not offline, interrupted, or stopped).
func todoSessionActive(c proto.Session) bool {
	if c.Status == proto.StatusOffline || c.Status == proto.StatusInterrupted || c.State == proto.StateOffline {
		return false
	}
	if c.ReadOnlyReason == "stopped" || stoppedAgent(c) {
		return false
	}
	return true
}

// todoHasActiveAgents reports whether completing t would stop or close any still-running linked Agent (#15758).
// Released and dismissed agents do not count. If a machine with an unreleased linked agent cannot be reached,
// this conservatively returns true so the owner is still asked to confirm.
func (s *Service) todoHasActiveAgents(ctx context.Context, t Todo) bool {
	want := map[string]map[string]bool{}
	for _, a := range t.Agents {
		if a.ReleasedAt != nil || a.DismissedAt != nil {
			continue
		}
		if want[a.MachineID] == nil {
			want[a.MachineID] = map[string]bool{}
		}
		want[a.MachineID][a.SessionID] = true
	}
	if len(want) == 0 {
		return false
	}
	for machine, ids := range want {
		sessions, err := s.todoRunSessions(ctx, machine, ids)
		if err != nil {
			return true
		}
		for _, c := range sessions {
			if todoSessionActive(c) {
				return true
			}
		}
	}
	return false
}
