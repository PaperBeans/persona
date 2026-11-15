package assistant

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// TodoPRStatus keeps forge observations beside the existing stage/delivery. A ready,
// open PR with no working Agent is a view of that state, not another task lifecycle.
type TodoPRStatus struct {
	URL       string    `json:"url"`
	State     string    `json:"state"`
	Draft     bool      `json:"draft"`
	Review    string    `json:"review"`
	Checks    string    `json:"checks"`
	Conflicts bool      `json:"conflicts"`
	CheckedAt time.Time `json:"checked_at"`
}

type prStatusForge interface {
	PRStatus(context.Context, string, int) (TodoPRStatus, error)
}

func (g githubForge) PRStatus(ctx context.Context, repo string, number int) (TodoPRStatus, error) {
	path := fmt.Sprintf("/repos/%s/pulls/%d", repo, number)
	var pr struct {
		State          string `json:"state"`
		Draft          bool   `json:"draft"`
		Merged         bool   `json:"merged"`
		MergeableState string `json:"mergeable_state"`
		Head           struct {
			SHA string `json:"sha"`
		} `json:"head"`
		Reviewers []any `json:"requested_reviewers"`
		Teams     []any `json:"requested_teams"`
	}
	if err := g.get(ctx, path, &pr); err != nil {
		return TodoPRStatus{}, err
	}
	out := TodoPRStatus{State: pr.State, Draft: pr.Draft, Review: "pending", Checks: "none", Conflicts: pr.MergeableState == "dirty"}
	if pr.Merged {
		out.State = "merged"
	}
	if out.State != "open" {
		return out, nil
	}
	if pr.Head.SHA == "" {
		return TodoPRStatus{}, fmt.Errorf("PR has no head commit")
	}
	var reviews []struct {
		State string `json:"state"`
		User  struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	// An incomplete review page cannot prove approval; retain a conservative label.
	if err := g.get(ctx, path+"/reviews?per_page=100", &reviews); err != nil {
		return TodoPRStatus{}, err
	}
	latest := map[string]string{}
	for _, r := range reviews {
		switch r.State {
		case "APPROVED", "CHANGES_REQUESTED", "DISMISSED":
			latest[r.User.Login] = r.State
		}
	}
	for _, state := range latest {
		if state == "APPROVED" {
			out.Review = "approved"
		}
	}
	for _, state := range latest {
		if state == "CHANGES_REQUESTED" {
			out.Review = "changes_requested"
		}
	}
	if out.Review != "changes_requested" && len(pr.Reviewers)+len(pr.Teams) > 0 {
		out.Review = "required"
	}
	if len(reviews) == 100 && out.Review == "approved" {
		out.Review = "pending"
	}
	var status struct {
		State string `json:"state"`
		Total int    `json:"total_count"`
	}
	commit := fmt.Sprintf("/repos/%s/commits/%s", repo, pr.Head.SHA)
	if err := g.get(ctx, commit+"/status", &status); err != nil {
		return TodoPRStatus{}, err
	}
	if status.Total > 0 {
		out.Checks = checkState(status.State)
	}
	var checks struct {
		Total int `json:"total_count"`
		Runs  []struct {
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"check_runs"`
	}
	if err := g.get(ctx, commit+"/check-runs?per_page=100&filter=latest", &checks); err != nil {
		return TodoPRStatus{}, err
	}
	if checks.Total > len(checks.Runs) {
		out.Checks = joinCheckState(out.Checks, "pending")
	}
	for _, run := range checks.Runs {
		state := "pending"
		if run.Status == "completed" {
			state = checkState(run.Conclusion)
		}
		out.Checks = joinCheckState(out.Checks, state)
	}
	return out, nil
}

func checkState(state string) string {
	switch strings.ToLower(state) {
	case "success", "neutral", "skipped":
		return "success"
	case "failure", "error", "cancelled", "timed_out", "action_required", "startup_failure", "stale":
		return "failure"
	default:
		return "pending"
	}
}

func joinCheckState(a, b string) string {
	for _, state := range []string{"failure", "pending", "success"} {
		if a == state || b == state {
			return state
		}
	}
	return "none"
}

// The existing minute deploy pass owns polling. Reads never block a task list or
// snapshot, and failures leave the last observation to expire rather than invent success.
func (s *Service) sweepPRStatuses(ctx context.Context, now time.Time) {
	f, ok := s.forge().(prStatusForge)
	if !ok {
		return
	}
	if s.ensureTodosLoaded(ctx) != nil {
		return
	}
	s.todos.mu.RLock()
	var tasks []Todo
	for _, t := range s.todos.todos {
		if t.DoneAt == nil && t.PRURL != "" && !isAskTodo(t.ID) {
			tasks = append(tasks, copyTodoLocked(t))
		}
	}
	s.todos.mu.RUnlock()
	for _, t := range tasks {
		m := ciPRURL.FindStringSubmatch(t.PRURL)
		if t.DoneAt != nil || t.Cancelled() || m == nil {
			continue
		}
		if old := t.PRStatus; old != nil && old.URL == t.PRURL && now.Sub(old.CheckedAt) < deployRecheck {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		p := &s.deploy
		p.mu.Lock()
		last := p.seen[t.ID]
		if last.statusPR == t.PRURL && now.Sub(last.statusAt) < deployRecheck {
			p.mu.Unlock()
			continue
		}
		if p.seen == nil || len(p.seen) > 2000 {
			p.seen = map[string]prSeen{}
		}
		last.statusPR, last.statusAt = t.PRURL, now
		p.seen[t.ID] = last // failures are attempts too, not fresh evidence
		p.mu.Unlock()
		number, _ := strconv.Atoi(m[2])
		status, err := f.PRStatus(ctx, m[1], number)
		if err != nil {
			s.logf("task %s: PR status: %v", t.ID, err)
			continue
		}
		status.URL, status.CheckedAt = t.PRURL, now.UTC()
		if err := s.applyPRStatus(ctx, t, status); err != nil {
			s.logf("task %s: keep PR status: %v", t.ID, err)
		}
	}
}

func (s *Service) applyPRStatus(ctx context.Context, t Todo, status TodoPRStatus) error {
	current, ok := s.todoNow(t.ID)
	if !ok || current.DoneAt != nil || current.PRURL != status.URL {
		return nil
	}
	t = current
	// Returning from a known open PR (or a legacy PR-stage hold) wakes the same supervisor, read before the new facts
	// are kept. A merged PR alone is never acceptance, and unrelated later parks stay put.
	wasOpen := t.PRStatus != nil && t.PRStatus.URL == status.URL && t.PRStatus.State == "open"
	legacyHold := t.PRStatus == nil && (t.Delivery != nil && t.Delivery.State == deliveryPR || t.Stage == "review")
	terminal := status.State == "merged" || status.State == "closed"
	// The stage's PR is checked under the stage lock first: an observation of a PR an Agent reply replaced while the
	// request was in flight changes nothing, neither the facts nor the supervision.
	b := &s.reports
	b.stageWriting.Lock()
	b.mu.Lock()
	if !s.loadStagesLocked(ctx) {
		b.mu.Unlock()
		b.stageWriting.Unlock()
		return fmt.Errorf("stages could not be read")
	}
	cur := b.stages[t.ID]
	if cur.PR != status.URL {
		b.mu.Unlock()
		b.stageWriting.Unlock()
		return nil // a new Agent reply replaced the PR while the request was in flight
	}
	cur.PRStatus = &status
	if store := s.store(); store != nil {
		stages := make(map[string]todoStage, len(b.stages))
		for id, stage := range b.stages {
			stages[id] = stage
		}
		stages[t.ID] = cur
		if err := store.PutSetting(ctx, reportStagesKey, stages); err != nil {
			b.mu.Unlock()
			b.stageWriting.Unlock()
			return err
		}
	}
	b.stages[t.ID] = cur
	b.mu.Unlock()
	s.todos.mu.Lock()
	if todo := s.todos.todos[t.ID]; todo != nil && todo.PRURL == status.URL {
		todo.PRStatus = &status
	}
	s.todos.mu.Unlock()
	b.stageWriting.Unlock()
	s.todosChanged(ctx)
	if terminal && (wasOpen || legacyHold) && t.Supervision != nil {
		// Only the hold that waits on this PR wakes: a park waiting on an outside event or someone else whose reason
		// names this PR (parkWaitsOnPR), the same park (its review's time) as when the PR was read, and the stage's PR
		// still this one. Anything else (ongoing work, rework, a review in progress, a park for something else, a later
		// park, a replaced PR) stays as it is, and a park comes due at its own time.
		held := t.Supervision.parking()
		if parkWaitsOnPR(held, status.URL) {
			at := held.At
			v, changed, err := s.updateSupervision(ctx, t.ID, func(v *Supervision) bool {
				p := v.parking()
				if p == nil || !p.At.Equal(at) || !parkWaitsOnPR(p, status.URL) {
					return false
				}
				if now, ok := s.todoNow(t.ID); !ok || now.DoneAt != nil || now.PRURL != status.URL || s.stagePR(t.ID) != status.URL {
					return false
				}
				v.State = reviewPending
				return true
			})
			if err != nil {
				return err
			}
			if changed {
				s.signal(superviseSignal{Todo: t.ID, Kind: sigReview, Key: reviewKey(&v), Detail: "PR " + status.State + ": " + status.URL})
			}
		}
	}
	if status.State == "merged" && stageIndex(t.Stage) < stageIndex("merge") {
		s.setTaskStageOn(ctx, t.ID, "merge", "", "", false, status.URL)
	}
	return nil
}

// stagePR is the PR task id's stage holds now ("" when none).
func (s *Service) stagePR(id string) string {
	b := &s.reports
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stages[id].PR
}

// parkWaitsOnPR says whether park r waits on the PR at url: it waits on an outside event or someone else, and its
// reason names that PR, by its link or as "#N" with its number. A park for anything else (a design decision, another
// PR, a release) is not this PR's merge wait, even while the PR is open.
func parkWaitsOnPR(r *TaskReview, url string) bool {
	if r == nil || r.WaitingOn != parkExternalEvent && r.WaitingOn != parkSomeoneElse {
		return false
	}
	m := ciPRURL.FindStringSubmatch(url)
	if m == nil {
		return false
	}
	reason := strings.ToLower(r.Reason)
	for _, link := range ciPRURL.FindAllStringSubmatch(reason, -1) {
		if strings.EqualFold(link[1], m[1]) && link[2] == m[2] {
			return true
		}
	}
	for _, ref := range prBareRef.FindAllStringSubmatch(reason, -1) {
		if ref[1] == m[2] {
			return true
		}
	}
	return false
}
