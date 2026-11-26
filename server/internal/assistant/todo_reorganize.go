package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/db"
)

const maxReorganizeIDs = 100

// todoReorganizeRequest is the body of POST /api/v1/todos/reorganize (#14704).
// It allows reorganizing any to-do (or parent/child subtree) in any state without interrupting running Agents
// or mutating origin / todo_agents.
type todoReorganizeRequest struct {
	IDs                 []string `json:"ids"`
	PartOf              *string  `json:"part_of"`
	DirectionID         *string  `json:"direction_id"`
	IncludeChildren     bool     `json:"include_children"`
	TransferSupervision bool     `json:"transfer_supervision"`
}

// todoReorganizeResponse is the answer of POST /api/v1/todos/reorganize (#14704).
type todoReorganizeResponse struct {
	Updated        []Todo   `json:"updated"`
	TransferredIDs []string `json:"transferred_ids,omitempty"`
}

type reorganizeBadRequestError struct{ err error }

func (e reorganizeBadRequestError) Error() string { return e.err.Error() }
func (e reorganizeBadRequestError) Unwrap() error { return e.err }

// todoReorganizeRoute registers POST /api/v1/todos/reorganize (#14704).
func (s *Service) todoReorganizeRoute() {
	s.handle("POST /api/v1/todos/reorganize", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot modify todos"))
			return
		}
		if err := s.ensureTodosLoaded(r.Context()); err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, errTodosUnavailable)
			return
		}
		var in todoReorganizeRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		res, err := s.reorganizeTodos(r.Context(), in)
		if err != nil {
			var badReq reorganizeBadRequestError
			switch {
			case errors.As(err, &badReq):
				apierr.Write(w, http.StatusBadRequest, err)
			case errors.Is(err, db.ErrTodoNotFound):
				apierr.Write(w, http.StatusNotFound, err)
			default:
				apierr.Write(w, http.StatusInternalServerError, err)
			}
			return
		}
		apierr.WriteJSON(w, res)
	})
}

// reorganizeTodos validates and applies a batch part_of / direction_id reorganization, then optionally hands over
// runtime supervision for open supervised tasks when explicitly requested and targeting an active direction leader.
func (s *Service) reorganizeTodos(ctx context.Context, in todoReorganizeRequest) (todoReorganizeResponse, error) {
	ids := cleanIDs(in.IDs)
	if len(ids) == 0 || len(ids) > maxReorganizeIDs {
		return todoReorganizeResponse{}, reorganizeBadRequestError{
			err: fmt.Errorf("ids must contain between 1 and %d todo ids", maxReorganizeIDs),
		}
	}
	if in.PartOf == nil && in.DirectionID == nil {
		return todoReorganizeResponse{}, reorganizeBadRequestError{
			err: errors.New("at least one of part_of or direction_id must be specified"),
		}
	}

	var targetPartOf *string
	if in.PartOf != nil {
		p := strings.TrimSpace(*in.PartOf)
		targetPartOf = &p
	}
	var targetDirection *string
	if in.DirectionID != nil {
		d := strings.TrimSpace(*in.DirectionID)
		targetDirection = &d
	}

	idSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}

	// Phase 1: serialize graph validation and persistence under todos.writing so concurrent edits cannot
	// introduce a part_of cycle between validation and write.
	s.todos.writing.Lock()
	affectedIDs, err := s.validateReorganizeLocked(ids, idSet, targetPartOf, targetDirection != nil && in.IncludeChildren)
	if err != nil {
		s.todos.writing.Unlock()
		return todoReorganizeResponse{}, err
	}

	now := time.Now().UTC()
	st := s.todoDB()
	updated := make([]Todo, 0, len(affectedIDs))
	updatedIdx := make(map[string]int, len(affectedIDs))

	for _, tid := range affectedIDs {
		cur, ok := s.todoNow(tid)
		if !ok {
			s.todos.writing.Unlock()
			return todoReorganizeResponse{}, db.ErrTodoNotFound
		}
		var edit db.TodoEdit
		if targetPartOf != nil && idSet[tid] {
			p := *targetPartOf
			edit.PartOf = &p
			if cur.PartOf != p && len(cur.After) > 0 {
				kept := s.filterValidReorganizeAfter(tid, cur.After)
				if !slices.Equal(kept, cur.After) {
					edit.After = &kept
				}
			}
		}
		if targetDirection != nil {
			d := *targetDirection
			edit.DirectionID = &d
		}

		var row *db.TodoRow
		if st != nil {
			row, err = st.EditTodo(ctx, tid, edit, now)
			if err != nil {
				s.todos.writing.Unlock()
				return todoReorganizeResponse{}, err
			}
		} else {
			r := todoRowOf(cur)
			edit.Apply(&r, now)
			row = &r
		}
		out, _, _ := s.setTodoRow(tid, *row, nil)
		updatedIdx[tid] = len(updated)
		updated = append(updated, out)
	}
	s.todos.writing.Unlock()

	s.todosChanged(ctx)

	// Phase 2: optional supervision handover. Only when explicitly requested and targeting a non-empty active
	// direction leader that passes the existing transfer validation. Never interrupts or messages running Agents;
	// personal or completed to-dos are silently skipped.
	var transferredIDs []string
	if in.TransferSupervision && targetDirection != nil && *targetDirection != "" {
		lead := *targetDirection
		cfg := s.settings(ctx)
		if s.isLeader(ctx, lead) && s.transferTarget(ctx, cfg, lead) == nil {
			for _, tid := range affectedIDs {
				moved, ok := s.transferReorganizedTodo(ctx, tid, lead)
				if !ok {
					continue
				}
				transferredIDs = append(transferredIDs, tid)
				if idx, exists := updatedIdx[tid]; exists {
					updated[idx] = formatTodo(moved)
				}
			}
		}
	}

	return todoReorganizeResponse{
		Updated:        updated,
		TransferredIDs: transferredIDs,
	}, nil
}

// validateReorganizeLocked checks that all requested ids exist, validates part_of existence and cycle-freeness
// (both self-loops and multi-hop ancestor/descendant cycles), and expands part_of descendants when requested.
// Caller must hold s.todos.writing.
func (s *Service) validateReorganizeLocked(ids []string, idSet map[string]bool, targetPartOf *string, expandChildren bool) ([]string, error) {
	s.todos.mu.RLock()
	defer s.todos.mu.RUnlock()

	book := s.todos.todos
	for _, id := range ids {
		if _, ok := book[id]; !ok {
			return nil, fmt.Errorf("%w: %s", db.ErrTodoNotFound, id)
		}
	}

	if targetPartOf != nil && *targetPartOf != "" {
		parent := *targetPartOf
		if _, ok := book[parent]; !ok {
			return nil, reorganizeBadRequestError{
				err: fmt.Errorf("part_of: target todo %q not found", parent),
			}
		}
		// Walk ancestors of parent (cycle-safe) to reject self-cycles and multi-hop ancestor/descendant cycles.
		seen := make(map[string]bool)
		for cur := parent; cur != "" && !seen[cur]; {
			if idSet[cur] {
				if cur == parent {
					return nil, reorganizeBadRequestError{
						err: fmt.Errorf("part_of: cannot attach todo %q to itself", cur),
					}
				}
				return nil, reorganizeBadRequestError{
					err: fmt.Errorf("part_of: attaching %q under %q would create a cycle", cur, parent),
				}
			}
			seen[cur] = true
			node, ok := book[cur]
			if !ok {
				break
			}
			cur = node.PartOf
		}
	}

	affected := make([]string, 0, len(ids))
	affected = append(affected, ids...)
	if !expandChildren {
		return affected, nil
	}

	childrenByParent := make(map[string][]string)
	for _, t := range book {
		if t.PartOf != "" {
			childrenByParent[t.PartOf] = append(childrenByParent[t.PartOf], t.ID)
		}
	}
	for p := range childrenByParent {
		slices.Sort(childrenByParent[p])
	}

	visited := make(map[string]bool, len(ids))
	queue := make([]string, 0, len(ids))
	for _, id := range ids {
		visited[id] = true
		queue = append(queue, id)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, child := range childrenByParent[cur] {
			if visited[child] {
				continue
			}
			visited[child] = true
			affected = append(affected, child)
			queue = append(queue, child)
		}
	}
	return affected, nil
}

// filterValidReorganizeAfter keeps every existing After dependency that still points to an existing, non-self
// to-do without introducing a dependency cycle, pruning only self-references, missing targets, or cyclic edges.
// Caller must hold s.todos.writing.
func (s *Service) filterValidReorganizeAfter(tid string, after []string) []string {
	s.todos.mu.RLock()
	defer s.todos.mu.RUnlock()

	book := s.todos.todos
	kept := make([]string, 0, len(after))
	seen := make(map[string]bool, len(after))
	for _, raw := range after {
		dep := strings.TrimSpace(raw)
		if dep == "" || dep == tid || seen[dep] {
			continue
		}
		if _, exists := book[dep]; !exists {
			continue
		}
		if afterCycle(book, tid, []string{dep}) != "" {
			continue
		}
		seen[dep] = true
		kept = append(kept, dep)
	}
	return kept
}

// transferReorganizedTodo hands over supervision of tid to active leader lead if and only if tid is an open,
// non-personal task with an existing supervision record not already supervised by lead.
func (s *Service) transferReorganizedTodo(ctx context.Context, tid, lead string) (Todo, bool) {
	s.supervise.routingMu.Lock()
	defer s.supervise.routingMu.Unlock()

	cur, ok := s.todoNow(tid)
	if !ok || cur.closedForGood() || cur.Origin == "" || cur.Supervision == nil {
		return Todo{}, false
	}
	if cur.Supervision.Supervisor == lead {
		return Todo{}, false
	}
	moved, _, err := s.transferTaskLocked(ctx, cur, lead, "")
	if err != nil {
		s.logf("todo %s: reorganize supervision transfer to %s: %v", tid, short(lead), err)
		return Todo{}, false
	}
	if latest, ok := s.todoNow(tid); ok {
		return latest, true
	}
	return moved, true
}
