package assistant

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/db"
)

// applyChecks records only the criteria actually checked, without deriving any
// task verdict from their count. Unmatched checks remain in the review history.
func (v *Supervision) applyChecks(checks []ReviewCheck, at time.Time, by string) {
	v.Criteria = db.AlignTodoCriteria(v.DoneWhen, v.Criteria)
	for _, check := range checks {
		for i, text := range v.DoneWhen {
			if !sameCriterion(check.Criterion, text) {
				continue
			}
			state := "not_met"
			if check.Met {
				state = "met"
			}
			v.Criteria[i] = db.TodoCriterion{Criterion: text, State: state, Evidence: check.Evidence, At: &at, By: by}
			break
		}
	}
}

// criterionReport uses the one-based position the agent saw in done_when. Text
// matching remains the persisted identity, so later reordering cannot move evidence.
type criterionReport struct {
	Number   int    `json:"number"`
	State    string `json:"state"`
	Evidence string `json:"evidence"`
}

func cleanCriterionReports(reports []criterionReport, doneWhen []string) ([]criterionReport, error) {
	out := make([]criterionReport, 0, len(reports))
	seen := map[int]bool{}
	for _, r := range reports {
		if r.Number < 1 || r.Number > len(doneWhen) {
			return nil, fmt.Errorf("criteria: number must be between 1 and %d", len(doneWhen))
		}
		if seen[r.Number] {
			return nil, fmt.Errorf("criteria: number %d appears twice", r.Number)
		}
		seen[r.Number] = true
		switch r.State {
		case "pending", "met", "not_met":
		default:
			return nil, fmt.Errorf("criteria: state must be pending, met or not_met")
		}
		r.Evidence = clip(strings.TrimSpace(r.Evidence), 600)
		if r.State != "pending" && r.Evidence == "" {
			return nil, fmt.Errorf("criteria: number %d needs evidence", r.Number)
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *Service) recordCriterionReports(ctx context.Context, t Todo, machine, session string, reports []criterionReport, at time.Time) error {
	if len(reports) == 0 {
		return nil
	}
	_, changed, err := s.updateSupervision(ctx, t.ID, func(v *Supervision) bool {
		// A concurrent criteria edit must not attach this report to a different entry.
		cur, ok := s.taskOf(ctx, machine, session)
		if !ok || cur.ID != t.ID || cur.DoneAt != nil || !slices.Equal(v.DoneWhen, t.Supervision.DoneWhen) {
			return false
		}
		v.Criteria = db.AlignTodoCriteria(v.DoneWhen, v.Criteria)
		for _, r := range reports {
			text := v.DoneWhen[r.Number-1]
			v.Criteria[r.Number-1] = db.TodoCriterion{Criterion: text, State: r.State, Evidence: r.Evidence,
				At: &at, By: "agent:" + machine + "/" + session}
		}
		return true
	})
	if err == nil && !changed {
		return fmt.Errorf("task or done_when changed: read the current task and report again")
	}
	return err
}
