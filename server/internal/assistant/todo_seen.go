package assistant

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// Seen to-dos (#5707, docs/assistant/todo-status-sop.md "Seen"): the owner looked at a to-do in Just finished, by Got it
// on its row or by opening it, so it is filed under Done (todo_triage.go) instead of waiting out the 24 hours. The mark is
// the to-do's seen_at, kept here so every device agrees. Marking it is not activity: updated_at stays, and a to-do
// that moves after it comes back to Just finished.

// seeTodo marks todo id seen now, or with seen false takes the mark off (Undo); db.ErrTodoNotFound when it is not there.
func (s *Service) seeTodo(ctx context.Context, id string, seen bool) (Todo, error) {
	cur, ok := s.todoNow(id)
	if !ok {
		return Todo{}, db.ErrTodoNotFound
	}
	var at *time.Time
	if seen {
		now := time.Now().UTC()
		at = &now
	}
	s.todos.writing.Lock()
	var row *db.TodoRow
	if st := s.todoDB(); st != nil {
		var err error
		if row, err = st.SeeTodo(ctx, id, at); err != nil {
			s.todos.writing.Unlock()
			return Todo{}, err
		}
	} else {
		r := todoRowOf(cur)
		r.SeenAt = at
		if at == nil {
			r.AutoSeenUndone = true
		}
		row = &r
	}
	out, _, applied := s.setTodoRow(id, *row, nil)
	s.todos.writing.Unlock()
	if !applied {
		return Todo{}, db.ErrTodoNotFound // deleted meanwhile
	}
	s.todosChanged(ctx) // the other devices read the list again: it moves there too
	return out, nil
}

// autoSeeTodo marks todo id seen only if the owner has not previously called Undo (AutoSeenUndone == false).
// Returns (true, nil) when seen_at was applied, (false, nil) when skipped due to Undo protection or missing to-do.
func (s *Service) autoSeeTodo(ctx context.Context, id string) (bool, error) {
	now := time.Now().UTC()
	s.todos.writing.Lock()
	var row *db.TodoRow
	if st := s.todoDB(); st != nil {
		r, updated, err := st.AutoSeeTodo(ctx, id, now)
		if err != nil {
			s.todos.writing.Unlock()
			return false, err
		}
		if !updated {
			s.todos.writing.Unlock()
			return false, nil
		}
		row = r
	} else {
		cur, ok := s.todoNow(id)
		if !ok || cur.AutoSeenUndone {
			s.todos.writing.Unlock()
			return false, nil
		}
		r := todoRowOf(cur)
		r.SeenAt = &now
		row = &r
	}
	_, _, applied := s.setTodoRow(id, *row, func(t *Todo) bool { return !t.AutoSeenUndone })
	s.todos.writing.Unlock()
	if !applied {
		return false, nil
	}
	s.todosChanged(ctx)
	return true, nil
}

// autoSeeAcceptedTodo settles an accepted autonomous to-do when todos_v2 is enabled (#14639):
// it sets seen_at on the to-do and marks its finished linked worker sessions as read.
// Never applies when todos_v2 is off, audience != autonomous, user_check is true, outcome != done,
// or the owner previously called Undo (DELETE /api/v1/todos/{id}/seen -> AutoSeenUndone == true).
func (s *Service) autoSeeAcceptedTodo(ctx context.Context, id, outcome string, userCheck bool) {
	if userCheck || outcome != outcomeDone || !s.settings(ctx).todosV2On() {
		return
	}
	cur, ok := s.todoNow(id)
	if !ok || cur.Audience != db.AudienceAutonomous || cur.AutoSeenUndone || cur.DoneAt == nil {
		return
	}
	applied, err := s.autoSeeTodo(ctx, id)
	if err != nil {
		s.logf("task %s: auto-seen: %v", id, err)
		return
	}
	if !applied {
		return
	}
	lists := machineSessions{}
	for _, a := range cur.Agents {
		if a.ReleasedAt != nil || a.DismissedAt != nil || a.MachineID == "" || a.SessionID == "" {
			continue
		}
		l, ok := lists[a.MachineID]
		if !ok {
			l.list, l.err = s.sessions(ctx, a.MachineID)
			lists[a.MachineID] = l
		}
		if l.err != nil {
			continue
		}
		for _, c := range l.list {
			if c.SessionID != a.SessionID {
				continue
			}
			if workerSessionFinishedForAutoSeen(c) {
				s.markSeen(ctx, a.MachineID, a.SessionID)
			}
			break
		}
	}
}

func workerSessionFinishedForAutoSeen(c proto.Session) bool {
	if c.Assistant {
		return false
	}
	if c.Status == proto.StatusWorking || c.Status == proto.StatusBlocked || c.Status == proto.StatusOffline {
		return false
	}
	if c.State == proto.StateWorking || c.State == proto.StateNeedsYou {
		return false
	}
	if c.BackgroundWorking {
		return false
	}
	return true
}

func (s *Service) todoSeenRoutes() {
	register := func(method string, seen bool) {
		// PUT /api/v1/todos/{id}/seen: the owner saw it (Got it, or its detail opened); DELETE: Undo.
		s.handle(method+" /api/v1/todos/{id}/seen", func(w http.ResponseWriter, r *http.Request) {
			if !fullRole(r) {
				apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot modify todos"))
				return
			}
			if err := s.ensureTodosLoaded(r.Context()); err != nil {
				apierr.Write(w, http.StatusServiceUnavailable, errTodosUnavailable)
				return
			}
			t, err := s.seeTodo(r.Context(), r.PathValue("id"), seen)
			if errors.Is(err, db.ErrTodoNotFound) {
				apierr.Write(w, http.StatusNotFound, errors.New("todo not found"))
				return
			}
			if err != nil {
				apierr.Write(w, http.StatusInternalServerError, err)
				return
			}
			apierr.WriteJSON(w, map[string]any{"todo": t})
		})
	}
	register("PUT", true)
	register("DELETE", false)
}
