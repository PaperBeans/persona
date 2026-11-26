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
	"github.com/nwverse/persona/server/internal/assistant/repository"
	"github.com/nwverse/persona/server/internal/db"
)

const (
	maxTodoSplitChildren = 20
	maxTodoTopologyBody  = 128 << 10
)

type topologyBadRequestError struct{ err error }

func (e topologyBadRequestError) Error() string { return e.err.Error() }
func (e topologyBadRequestError) Unwrap() error { return e.err }

type topologyConflictError struct{ err error }

func (e topologyConflictError) Error() string { return e.err.Error() }
func (e topologyConflictError) Unwrap() error { return e.err }

type todoSplitChildInput struct {
	Title          string   `json:"title"`
	Notes          string   `json:"notes,omitempty"`
	Priority       string   `json:"priority,omitempty"`
	AfterIndices   []int    `json:"after_indices,omitempty"`
	After          []string `json:"after,omitempty"`
	MoveBlockerIDs []string `json:"move_blocker_ids,omitempty"`
	CopyRepoLinks  bool     `json:"copy_repo_links,omitempty"`
}

type todoSplitRequest struct {
	Children        []todoSplitChildInput `json:"children"`
	AppendAuditNote *bool                 `json:"append_audit_note,omitempty"`
}

type todoSplitChildResult struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	After []string `json:"after,omitempty"`
}

type todoSplitResponse struct {
	SourceID      string                 `json:"source_id"`
	Children      []todoSplitChildResult `json:"children"`
	MovedBlockers int                    `json:"moved_blockers"`
}

type todoMergeRequest struct {
	SourceID             string `json:"source_id"`
	ForceWithActiveAgent bool   `json:"force_with_active_agent,omitempty"`
}

type todoMergeResponse struct {
	TargetID            string   `json:"target_id"`
	SourceID            string   `json:"source_id"`
	ReparentedChildren  []string `json:"reparented_children"`
	TransferredBlockers int      `json:"transferred_blockers"`
	TransferredLinks    int      `json:"transferred_links"`
}

// todoTopologyOpsRoutes registers atomic split and merge endpoints strictly gated under todos_v2=true (#16433).
func (s *Service) todoTopologyOpsRoutes() {
	s.handle("POST /api/v1/todos/{id}/split", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot modify todos"))
			return
		}
		ctx := r.Context()
		if !s.settings(ctx).todosV2On() {
			apierr.Write(w, http.StatusConflict, errors.New("todo split requires todos_v2 to be enabled"))
			return
		}
		var in todoSplitRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTodoTopologyBody)).Decode(&in); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		res, err := s.splitTodo(ctx, strings.TrimSpace(r.PathValue("id")), in)
		if err != nil {
			writeTopologyError(w, err)
			return
		}
		apierr.WriteJSON(w, res)
	})

	s.handle("POST /api/v1/todos/{id}/merge", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot modify todos"))
			return
		}
		ctx := r.Context()
		if !s.settings(ctx).todosV2On() {
			apierr.Write(w, http.StatusConflict, errors.New("todo merge requires todos_v2 to be enabled"))
			return
		}
		var in todoMergeRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTodoTopologyBody)).Decode(&in); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		res, err := s.mergeTodos(ctx, strings.TrimSpace(r.PathValue("id")), in)
		if err != nil {
			writeTopologyError(w, err)
			return
		}
		apierr.WriteJSON(w, res)
	})
}

func writeTopologyError(w http.ResponseWriter, err error) {
	var badReq topologyBadRequestError
	var conflict topologyConflictError
	var pe *pieceError
	switch {
	case errors.As(err, &badReq), errors.As(err, &pe):
		apierr.Write(w, http.StatusBadRequest, err)
	case errors.As(err, &conflict):
		apierr.Write(w, http.StatusConflict, err)
	case errors.Is(err, db.ErrTodoNotFound):
		apierr.Write(w, http.StatusNotFound, err)
	case errors.Is(err, errTodosUnavailable):
		apierr.Write(w, http.StatusServiceUnavailable, err)
	default:
		apierr.Write(w, http.StatusInternalServerError, err)
	}
}

func normalizeTopologyPriority(raw string) (string, error) {
	p := strings.ToLower(strings.TrimSpace(raw))
	switch p {
	case "", "normal":
		return "", nil
	case "high", "low":
		return p, nil
	default:
		return "", fmt.Errorf("invalid priority %q: must be high, normal, or low", raw)
	}
}

// topoOrderSplitChildren validates after_indices bounds, self-references, and acyclicity, returning a
// topological creation order where every dependency index appears before its dependents.
func topoOrderSplitChildren(children []todoSplitChildInput) ([]int, error) {
	n := len(children)
	adj := make([][]int, n)
	inDeg := make([]int, n)
	for i, c := range children {
		seen := make(map[int]bool, len(c.AfterIndices))
		for _, dep := range c.AfterIndices {
			if dep < 0 || dep >= n {
				return nil, topologyBadRequestError{err: fmt.Errorf("child %d: after_indices contains out-of-range index %d", i, dep)}
			}
			if dep == i {
				return nil, topologyBadRequestError{err: fmt.Errorf("child %d: after_indices cannot reference itself", i)}
			}
			if seen[dep] {
				continue
			}
			seen[dep] = true
			adj[dep] = append(adj[dep], i)
			inDeg[i]++
		}
	}
	queue := make([]int, 0, n)
	for i := 0; i < n; i++ {
		if inDeg[i] == 0 {
			queue = append(queue, i)
		}
	}
	order := make([]int, 0, n)
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		order = append(order, u)
		for _, v := range adj[u] {
			inDeg[v]--
			if inDeg[v] == 0 {
				queue = append(queue, v)
			}
		}
	}
	if len(order) != n {
		return nil, topologyBadRequestError{err: errors.New("after_indices contains a cycle")}
	}
	return order, nil
}

// splitTodo atomically splits an open source todo into 1-20 child todos under todos_v2=true.
func (s *Service) splitTodo(ctx context.Context, sourceID string, in todoSplitRequest) (todoSplitResponse, error) {
	if !s.settings(ctx).todosV2On() {
		return todoSplitResponse{}, topologyConflictError{err: errors.New("todo split requires todos_v2 to be enabled")}
	}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return todoSplitResponse{}, errTodosUnavailable
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return todoSplitResponse{}, topologyBadRequestError{err: errors.New("source todo id is required")}
	}
	source, ok := s.todoNow(sourceID)
	if !ok {
		return todoSplitResponse{}, db.ErrTodoNotFound
	}
	if source.DoneAt != nil || source.Cancelled() {
		return todoSplitResponse{}, topologyConflictError{err: db.ErrTodoCompleted}
	}
	if source.PartOf != "" {
		return todoSplitResponse{}, topologyConflictError{
			err: fmt.Errorf("cannot split %q: it is already a child piece of %q", source.ID, source.PartOf),
		}
	}
	if len(in.Children) == 0 || len(in.Children) > maxTodoSplitChildren {
		return todoSplitResponse{}, topologyBadRequestError{
			err: fmt.Errorf("children must contain between 1 and %d items", maxTodoSplitChildren),
		}
	}

	order, err := topoOrderSplitChildren(in.Children)
	if err != nil {
		return todoSplitResponse{}, err
	}

	titles := make([]string, len(in.Children))
	priorities := make([]string, len(in.Children))
	existingAfters := make([][]string, len(in.Children))
	moveBlockerIDs := make([][]string, len(in.Children))
	claimedBlockers := make(map[string]int)
	anyCopyLinks := false
	anyMoveBlockers := false

	for i, c := range in.Children {
		t := strings.TrimSpace(c.Title)
		if t == "" {
			return todoSplitResponse{}, topologyBadRequestError{err: fmt.Errorf("child %d: title is required", i)}
		}
		titles[i] = t
		p, err := normalizeTopologyPriority(c.Priority)
		if err != nil {
			return todoSplitResponse{}, topologyBadRequestError{err: fmt.Errorf("child %d: %w", i, err)}
		}
		priorities[i] = p
		after := cleanIDs(c.After)
		for _, depID := range after {
			dep, exists := s.todoNow(depID)
			if !exists {
				return todoSplitResponse{}, topologyBadRequestError{err: fmt.Errorf("child %d: after todo %q not found", i, depID)}
			}
			if dep.PartOf != source.ID {
				return todoSplitResponse{}, topologyBadRequestError{
					err: fmt.Errorf("child %d: after todo %q is not a sibling child of %q", i, depID, source.ID),
				}
			}
		}
		existingAfters[i] = after
		if c.CopyRepoLinks {
			anyCopyLinks = true
		}
		bids := cleanIDs(c.MoveBlockerIDs)
		for _, bid := range bids {
			if prev, dup := claimedBlockers[bid]; dup {
				return todoSplitResponse{}, topologyBadRequestError{
					err: fmt.Errorf("blocker %q cannot be moved to both child %d and child %d", bid, prev, i),
				}
			}
			claimedBlockers[bid] = i
		}
		moveBlockerIDs[i] = bids
		if len(bids) > 0 {
			anyMoveBlockers = true
		}
	}

	sourceBlockersByID := make(map[string]db.TodoBlockerRow)
	if anyMoveBlockers {
		st := s.todoBlockerDB()
		if st == nil {
			return todoSplitResponse{}, errors.New("todo blockers store unavailable")
		}
		rows, err := st.ListTodoBlockersByTodo(ctx, source.ID)
		if err != nil {
			return todoSplitResponse{}, err
		}
		for _, r := range rows {
			sourceBlockersByID[r.ID] = r
		}
		for bid := range claimedBlockers {
			row, found := sourceBlockersByID[bid]
			if !found {
				return todoSplitResponse{}, topologyBadRequestError{
					err: fmt.Errorf("move_blocker_ids: blocker %q not found on source todo %q", bid, source.ID),
				}
			}
			if row.State != db.TodoBlockerStateActive {
				return todoSplitResponse{}, topologyConflictError{
					err: fmt.Errorf("move_blocker_ids: blocker %q is not active (state=%s)", bid, row.State),
				}
			}
		}
	}

	var linksToCopy []repository.LinkInput
	if anyCopyLinks {
		linksToCopy = s.collectTodoRepoLinks(ctx, source.ID, true)
	}

	childIDs := make([]string, len(in.Children))
	for i := range in.Children {
		childIDs[i] = "todo_" + randomHex(8)
	}

	results := make([]todoSplitChildResult, len(in.Children))
	now := time.Now().UTC()
	for _, idx := range order {
		c := in.Children[idx]
		resolvedAfter := make([]string, 0, len(existingAfters[idx])+len(c.AfterIndices))
		resolvedAfter = append(resolvedAfter, existingAfters[idx]...)
		for _, depIdx := range c.AfterIndices {
			resolvedAfter = append(resolvedAfter, childIDs[depIdx])
		}
		resolvedAfter = cleanIDs(resolvedAfter)

		row := db.TodoRow{
			ID:          childIDs[idx],
			Title:       titles[idx],
			Notes:       strings.TrimSpace(c.Notes),
			Priority:    priorities[idx],
			PartOf:      source.ID,
			After:       resolvedAfter,
			DirectionID: source.DirectionID,
			Project:     source.Project,
			ItemType:    source.ItemType,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		created, err := s.insertTodo(ctx, row)
		if err != nil {
			return todoSplitResponse{}, err
		}
		if len(created.After) > 0 {
			s.syncAfterBlockers(ctx, created.ID, created.After, "", nil)
		}
		results[idx] = todoSplitChildResult{
			ID:    created.ID,
			Title: created.Title,
			After: slices.Clone(created.After),
		}
	}

	if anyCopyLinks && len(linksToCopy) > 0 {
		for i, c := range in.Children {
			if !c.CopyRepoLinks {
				continue
			}
			if _, err := s.applyTodoRepoLinks(ctx, childIDs[i], linksToCopy); err != nil {
				s.logf("todo_split %s -> %s: copy repo links: %v", source.ID, childIDs[i], err)
			}
		}
	}

	movedBlockers := 0
	for i, bids := range moveBlockerIDs {
		for _, bid := range bids {
			orig := sourceBlockersByID[bid]
			if orig.TargetRef != childIDs[i] {
				if _, err := s.AddTodoBlocker(ctx, childIDs[i], orig.Kind, orig.TargetRef, orig.ConditionJSON); err != nil {
					return todoSplitResponse{}, err
				}
			}
			if _, err := s.cancelTodoBlocker(ctx, orig.ID); err != nil {
				return todoSplitResponse{}, err
			}
			movedBlockers++
		}
	}

	appendAudit := in.AppendAuditNote == nil || *in.AppendAuditNote
	if appendAudit {
		snippet := fmt.Sprintf("[split] Split into %d child todos: %s", len(childIDs), strings.Join(childIDs, ", "))
		if cur, exists := s.todoNow(source.ID); exists {
			updatedNotes := appendProvenanceNote(cur.Notes, snippet)
			if _, err := s.editTodo(ctx, source.ID, db.TodoEdit{Notes: &updatedNotes}); err != nil {
				s.logf("todo_split %s: append audit note: %v", source.ID, err)
			}
		}
	}

	s.todosChanged(ctx)
	return todoSplitResponse{
		SourceID:      source.ID,
		Children:      results,
		MovedBlockers: movedBlockers,
	}, nil
}

// mergeTodos atomically merges an open source todo into an open target todo under todos_v2=true.
func (s *Service) mergeTodos(ctx context.Context, targetID string, in todoMergeRequest) (todoMergeResponse, error) {
	if !s.settings(ctx).todosV2On() {
		return todoMergeResponse{}, topologyConflictError{err: errors.New("todo merge requires todos_v2 to be enabled")}
	}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return todoMergeResponse{}, errTodosUnavailable
	}
	targetID = strings.TrimSpace(targetID)
	sourceID := strings.TrimSpace(in.SourceID)
	if targetID == "" {
		return todoMergeResponse{}, topologyBadRequestError{err: errors.New("target todo id is required")}
	}
	if sourceID == "" {
		return todoMergeResponse{}, topologyBadRequestError{err: errors.New("source_id is required")}
	}
	if sourceID == targetID {
		return todoMergeResponse{}, topologyBadRequestError{err: errors.New("source_id and target_id must differ")}
	}

	target, ok := s.todoNow(targetID)
	if !ok {
		return todoMergeResponse{}, db.ErrTodoNotFound
	}
	source, ok := s.todoNow(sourceID)
	if !ok {
		return todoMergeResponse{}, db.ErrTodoNotFound
	}
	if target.DoneAt != nil || target.Cancelled() {
		return todoMergeResponse{}, topologyConflictError{err: errors.New("target todo is already closed")}
	}
	if source.DoneAt != nil || source.Cancelled() {
		return todoMergeResponse{}, topologyConflictError{err: errors.New("source todo is already closed")}
	}
	if todoHasActiveAgent(source) && !in.ForceWithActiveAgent {
		return todoMergeResponse{}, topologyConflictError{
			err: errors.New("source todo has an active agent; release/stop it first or pass force_with_active_agent=true"),
		}
	}

	// Cycle guard: reject if target is a descendant of source in PartOf.
	if s.isTodoDescendantOf(target.ID, source.ID) {
		return todoMergeResponse{}, topologyConflictError{
			err: fmt.Errorf("cannot merge %q into %q: target is a descendant of source (would create a part_of cycle)", source.ID, target.ID),
		}
	}

	// 1. Transfer saved repository/PR/issue links (deduplicated, preserving target's existing links).
	transferredLinks := 0
	sourceLinks := s.collectTodoRepoLinks(ctx, source.ID, false)
	if len(sourceLinks) > 0 {
		added, err := s.applyTodoRepoLinks(ctx, target.ID, sourceLinks)
		if err != nil {
			s.logf("todo_merge %s -> %s: transfer repo links: %v", source.ID, target.ID, err)
		} else {
			transferredLinks = added
		}
	}

	// 2. Transfer active todo_blockers from source to target (deduplicated, skipping self-blocker), then cancel old rows.
	transferredBlockers := 0
	if bst := s.todoBlockerDB(); bst != nil {
		sourceRows, err := bst.ListTodoBlockersByTodo(ctx, source.ID)
		if err != nil {
			return todoMergeResponse{}, err
		}
		targetRows, err := bst.ListTodoBlockersByTodo(ctx, target.ID)
		if err != nil {
			return todoMergeResponse{}, err
		}
		targetActive := make(map[string]bool, len(targetRows))
		for _, tr := range targetRows {
			if tr.State == db.TodoBlockerStateActive {
				targetActive[tr.ID] = true
			}
		}
		for _, r := range sourceRows {
			if r.State != db.TodoBlockerStateActive {
				continue
			}
			if r.TargetRef != target.ID {
				newID := todoBlockerID(target.ID, r.Kind, r.TargetRef)
				if _, err := s.AddTodoBlocker(ctx, target.ID, r.Kind, r.TargetRef, r.ConditionJSON); err != nil {
					return todoMergeResponse{}, err
				}
				if !targetActive[newID] {
					targetActive[newID] = true
					transferredBlockers++
				}
			}
			if _, err := s.cancelTodoBlocker(ctx, r.ID); err != nil {
				return todoMergeResponse{}, err
			}
		}
	}

	// 3. Copy active decisions from source to target when a decision store is present.
	transferredDecisions := 0
	if st := s.todoDB(); st != nil {
		if ds, err := st.ListTodoDecisions(ctx, source.ID, false); err == nil && len(ds) > 0 {
			for _, d := range ds {
				copied := d
				copied.ID = "dec_" + randomHex(8)
				copied.TodoID = target.ID
				copied.SupersedesID = ""
				copied.SupersededBy = ""
				if copied.Reason != "" {
					copied.Reason = copied.Reason + " (merged from " + source.ID + ")"
				} else {
					copied.Reason = "merged from " + source.ID
				}
				if inserted, ierr := st.InsertTodoDecision(ctx, copied); ierr == nil && inserted {
					transferredDecisions++
				}
			}
		}
	}

	// 4. Atomically reparent children, rewrite open todos' After edges (source -> target), and append provenance notes
	// under todos.writing lock so concurrent edits cannot race or introduce cycles.
	reparented := make([]string, 0)
	var afterChanged []Todo

	s.todos.writing.Lock()
	if s.isTodoDescendantOf(target.ID, source.ID) {
		s.todos.writing.Unlock()
		return todoMergeResponse{}, topologyConflictError{
			err: fmt.Errorf("cannot merge %q into %q: target is a descendant of source (would create a part_of cycle)", source.ID, target.ID),
		}
	}

	var childIDs []string
	var afterRewrites []string
	s.todos.mu.RLock()
	for _, td := range s.todos.todos {
		if td.ID == source.ID {
			continue
		}
		if td.PartOf == source.ID && td.ID != target.ID {
			childIDs = append(childIDs, td.ID)
		}
		if td.DoneAt == nil && !td.Cancelled() && slices.Contains(td.After, source.ID) {
			afterRewrites = append(afterRewrites, td.ID)
		}
	}
	s.todos.mu.RUnlock()
	slices.Sort(childIDs)
	slices.Sort(afterRewrites)

	now := time.Now().UTC()
	st := s.todoDB()

	editSet := make(map[string]*db.TodoEdit)
	ensureEdit := func(id string) *db.TodoEdit {
		e, ok := editSet[id]
		if !ok {
			e = &db.TodoEdit{}
			editSet[id] = e
		}
		return e
	}

	for _, cid := range childIDs {
		p := target.ID
		ensureEdit(cid).PartOf = &p
		reparented = append(reparented, cid)
	}

	s.todos.mu.RLock()
	for _, depID := range afterRewrites {
		td, ok := s.todos.todos[depID]
		if !ok {
			continue
		}
		rewritten := make([]string, 0, len(td.After))
		for _, a := range td.After {
			if a == source.ID {
				if depID == target.ID {
					continue // drop self-reference on target
				}
				if afterCycle(s.todos.todos, depID, []string{target.ID}) != "" {
					continue // avoid introducing an After cycle
				}
				rewritten = append(rewritten, target.ID)
				continue
			}
			rewritten = append(rewritten, a)
		}
		rewritten = cleanIDs(rewritten)
		ensureEdit(depID).After = &rewritten
	}
	s.todos.mu.RUnlock()

	// Append provenance snippet to target's notes.
	var prov strings.Builder
	fmt.Fprintf(&prov, "[merged from %s] %s", source.ID, source.Title)
	if trimmedNotes := strings.TrimSpace(source.Notes); trimmedNotes != "" {
		prov.WriteString("\n" + trimmedNotes)
	}
	if transferredDecisions > 0 {
		fmt.Fprintf(&prov, "\n(carried over %d active decision(s))", transferredDecisions)
	}
	curTarget, _ := s.todoNow(target.ID)
	mergedNotes := appendProvenanceNote(curTarget.Notes, prov.String())
	ensureEdit(target.ID).Notes = &mergedNotes

	orderedEditIDs := make([]string, 0, len(editSet))
	for id := range editSet {
		orderedEditIDs = append(orderedEditIDs, id)
	}
	slices.Sort(orderedEditIDs)

	for _, id := range orderedEditIDs {
		cur, exists := s.todoNow(id)
		if !exists {
			continue
		}
		edit := *editSet[id]
		var row *db.TodoRow
		if st != nil {
			var err error
			row, err = st.EditTodo(ctx, id, edit, now)
			if err != nil {
				s.todos.writing.Unlock()
				return todoMergeResponse{}, err
			}
		} else {
			r := todoRowOf(cur)
			edit.Apply(&r, now)
			row = &r
		}
		out, _, _ := s.setTodoRow(id, *row, nil)
		if edit.After != nil {
			afterChanged = append(afterChanged, out)
		}
	}
	s.todos.writing.Unlock()

	// 5. Sync mirrored After blockers for all rewritten downstream todos.
	for _, td := range afterChanged {
		s.syncAfterBlockers(ctx, td.ID, td.After, "", nil)
	}

	// 6. Close/cancel source todo with a clear provenance reason.
	cancelReason := "merged into " + target.ID
	closed, err := s.reviewDoneTodo(ctx, source.ID, cancelReason, false)
	if err != nil {
		return todoMergeResponse{}, err
	}
	if !closed {
		// Fallback (e.g. if source was a concern or store skipped ReviewDoneTodo): mark done cleanly.
		done := true
		if _, editErr := s.editTodo(ctx, source.ID, db.TodoEdit{Done: &done}); editErr != nil {
			return todoMergeResponse{}, editErr
		}
	}

	s.syncAfterBlockers(ctx, source.ID, nil, "", nil)
	s.todosChanged(ctx)
	_, _ = s.ReconcileTodoBlockers(ctx, source.ID)

	return todoMergeResponse{
		TargetID:            target.ID,
		SourceID:            source.ID,
		ReparentedChildren:  reparented,
		TransferredBlockers: transferredBlockers,
		TransferredLinks:    transferredLinks,
	}, nil
}

// isTodoDescendantOf reports whether nodeID is sourceID or a descendant of sourceID via PartOf.
func (s *Service) isTodoDescendantOf(nodeID, ancestorID string) bool {
	s.todos.mu.RLock()
	defer s.todos.mu.RUnlock()
	seen := make(map[string]bool)
	for cur := nodeID; cur != "" && !seen[cur]; {
		if cur == ancestorID {
			return true
		}
		seen[cur] = true
		td, ok := s.todos.todos[cur]
		if !ok {
			break
		}
		cur = td.PartOf
	}
	return false
}

func appendProvenanceNote(existing, snippet string) string {
	existing = strings.TrimSpace(existing)
	snippet = strings.TrimSpace(snippet)
	if snippet == "" {
		return existing
	}
	if existing == "" {
		return snippet
	}
	return existing + "\n\n" + snippet
}

// collectTodoRepoLinks reads saved repository links for todoID without making network calls.
// When asReference is true, every returned link is marked with LinkRoleReference so copied links
// on split children do not hijack the source todo's delivery gate.
func (s *Service) collectTodoRepoLinks(ctx context.Context, todoID string, asReference bool) []repository.LinkInput {
	if _, ok := s.store().(todoRepositoryStore); !ok {
		return nil
	}
	set, err := s.savedTodoRepositories(ctx, todoID)
	if err != nil {
		return nil
	}
	seen := make(map[string]bool)
	var out []repository.LinkInput
	for _, row := range set.Repositories {
		for _, link := range row.Links {
			u := strings.TrimSpace(link.CanonicalURL)
			if u == "" {
				continue
			}
			key := normalizePRKey(u)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			role := link.Role
			if asReference {
				role = repository.LinkRoleReference
			} else if role == "" {
				role = repository.LinkRoleDelivery
			}
			in := repository.LinkInput{
				URL:    u,
				Kind:   link.Kind,
				Number: link.Number,
				Title:  link.Title,
				Role:   role,
			}
			if repository.IsExtensionProvider(row.SourceSnapshot.Identity.Provider) {
				linkCopy := link
				linkCopy.Role = role
				in.Resolved = &repository.ResolvedLink{
					Source: row.SourceSnapshot,
					Link:   linkCopy,
				}
			}
			out = append(out, in)
		}
	}
	return out
}

// applyTodoRepoLinks imports links onto destTodoID, deduplicating against already-saved links on destTodoID,
// and returns the count of newly added links.
func (s *Service) applyTodoRepoLinks(ctx context.Context, destTodoID string, links []repository.LinkInput) (int, error) {
	if len(links) == 0 {
		return 0, nil
	}
	existing := make(map[string]bool)
	if _, ok := s.store().(todoRepositoryStore); ok {
		if set, err := s.savedTodoRepositories(ctx, destTodoID); err == nil {
			for _, row := range set.Repositories {
				for _, l := range row.Links {
					if key := normalizePRKey(l.CanonicalURL); key != "" {
						existing[key] = true
					}
				}
			}
		}
	}
	toAdd := make([]repository.LinkInput, 0, len(links))
	for _, l := range links {
		key := normalizePRKey(l.URL)
		if key == "" || existing[key] {
			continue
		}
		existing[key] = true
		toAdd = append(toAdd, l)
	}
	if len(toAdd) == 0 {
		return 0, nil
	}
	if _, ok := s.store().(todoRepositoryLinkDetailsStore); ok {
		if err := s.importTodoRepositoryLinkDetails(ctx, destTodoID, toAdd, "explicit"); err != nil {
			return 0, err
		}
		return len(toAdd), nil
	}
	if _, ok := s.store().(todoRepositoryImportStore); ok {
		urls := make([]string, 0, len(toAdd))
		for _, l := range toAdd {
			urls = append(urls, l.URL)
		}
		if err := s.importTodoRepositoryLinks(ctx, destTodoID, urls, "explicit"); err != nil {
			return 0, err
		}
		return len(toAdd), nil
	}
	return 0, nil
}

// ---- Assistant MCP tool handlers ----

func (s *Service) toolTodoSplit(ctx context.Context, args json.RawMessage) (string, error) {
	if !s.settings(ctx).todosV2On() {
		return "", errors.New("todo_split requires todos_v2 to be enabled")
	}
	var a struct {
		ID              string                `json:"id"`
		Children        []todoSplitChildInput `json:"children"`
		AppendAuditNote *bool                 `json:"append_audit_note,omitempty"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("arguments: %w", err)
	}
	res, err := s.splitTodo(ctx, a.ID, todoSplitRequest{
		Children:        a.Children,
		AppendAuditNote: a.AppendAuditNote,
	})
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(res)
	return string(raw), err
}

func (s *Service) toolTodoMerge(ctx context.Context, args json.RawMessage) (string, error) {
	if !s.settings(ctx).todosV2On() {
		return "", errors.New("todo_merge requires todos_v2 to be enabled")
	}
	var a struct {
		TargetID             string `json:"target_id"`
		SourceID             string `json:"source_id"`
		ForceWithActiveAgent bool   `json:"force_with_active_agent,omitempty"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("arguments: %w", err)
	}
	res, err := s.mergeTodos(ctx, a.TargetID, todoMergeRequest{
		SourceID:             a.SourceID,
		ForceWithActiveAgent: a.ForceWithActiveAgent,
	})
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(res)
	return string(raw), err
}
