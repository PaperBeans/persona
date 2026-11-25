package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/db"
)

// A continuation keeps its candidate (docs/assistant/model-profiles.md "Continuations keep the candidate"): the
// next Agent of a task (task_plan continues, task_retry, the card's Retry, a queued or restarted start read back)
// starts on the launcher + model the previous Agent ran on, so it reuses that Agent's context and cache, while the
// candidate is still listed in the tier, present on the computer and healthy. Only a clear problem re-picks:
//
//   - quota: the candidate has no headroom and none recovers within keepQuotaWait;
//   - rework: the to-do has been sent back for rework keepReworkRounds times;
//   - failed starts: keepFailStreak starts in a row failed.
//
// Explicit caller fields still win field by field.

const (
	keepQuotaWait    = 2 * time.Hour
	keepReworkRounds = 2
	keepFailStreak   = 3
)

// Why a continuation re-picks (dispatch args "rule").
const (
	switchQuota   = "quota"
	switchRework  = "rework"
	switchFailing = "failed_starts"
)

// keepFacts is what the re-pick rule reads about the kept candidate and the to-do.
type keepFacts struct {
	Headroom     candidateHeadroom
	ReworkRounds int
	FailStreak   int
}

// keepSwitch is the §6.2 rule: "" keeps the candidate, otherwise the rule that re-picks and its arguments. Pure.
func keepSwitch(f keepFacts, now time.Time) (string, map[string]any) {
	if f.ReworkRounds >= keepReworkRounds {
		return switchRework, map[string]any{"rule": switchRework, "rework_rounds": f.ReworkRounds}
	}
	if f.FailStreak >= keepFailStreak {
		return switchFailing, map[string]any{"rule": switchFailing, "failed_starts": f.FailStreak}
	}
	h := f.Headroom
	if h.Known && h.H <= 0 && (h.RecoversAt == nil || h.RecoversAt.Sub(now) > keepQuotaWait) {
		args := map[string]any{"rule": switchQuota, "headroom": roundScore(h.Percent)}
		if h.RecoversAt != nil {
			args["recovers_at"] = h.RecoversAt.UTC().Format(time.RFC3339)
		}
		return switchQuota, args
	}
	return "", nil
}

// dispatchPresetArg marks a dispatch whose tier a start-defaults preset chose (dispatch args, model_routing detail):
// the policy decides that tier again on every start, so it is never kept.
const dispatchPresetArg = "preset"

// presetDispatch is whether args mark a preset's dispatch.
func presetDispatch(args map[string]any) bool {
	v, _ := args[dispatchPresetArg].(bool)
	return v
}

// tierKeep is the previous Agent's dispatch of the same task, with what the re-pick rule reads of the to-do.
type tierKeep struct {
	Prev   TaskDispatch
	Rework int // the to-do's rework rounds
}

// keepIndex is the candidate of list the previous dispatch ran on (its launcher and model, the same position first);
// -1 when it is no longer listed.
func keepIndex(list []TierCandidate, prev TaskDispatch) int {
	same := func(c TierCandidate) bool { return c.Launcher == prev.Launcher && c.Model == prev.Model }
	if prev.Candidate >= 0 && prev.Candidate < len(list) && same(list[prev.Candidate]) {
		return prev.Candidate
	}
	for i, c := range list {
		if same(c) {
			return i
		}
	}
	return -1
}

// keepTierCandidate decides a start that has a previous Agent's dispatch on this tier: it keeps that candidate (reason
// kept) while it is listed, present and healthy and no §6.2 rule holds, and answers true; when a rule holds it re-picks
// among the others (reason switched, with the rule) and answers true too. False leaves the pick to the usual choice: no
// previous dispatch, another tier, the candidate gone from the list or unusable here.
func (s *Service) keepTierCandidate(ctx context.Context, p *ModelProfile, machine string, keep *tierKeep, pick *tierPick, list []TierCandidate) bool {
	if keep == nil {
		return false
	}
	if name, ok := p.lookupTier(keep.Prev.Tier); !ok || name != pick.Name {
		return false
	}
	i := keepIndex(list, keep.Prev)
	if i < 0 {
		return false
	}
	c := list[i]
	if len(list) > 1 {
		if ok, _, _ := s.availabilityOn(machine).check(ctx, c.Launcher); !ok {
			return false
		}
	}
	now := time.Now()
	facts := keepFacts{ReworkRounds: keep.Rework, FailStreak: keep.Prev.Failed,
		Headroom: s.candidateHeadroomAt(ctx, s.usageAccounts(ctx), machine, c.Launcher, c.Model, now)}
	rule, args := keepSwitch(facts, now)
	if rule == "" || len(list) == 1 {
		pick.Candidate, pick.Reason = i, dispatchKept
		pick.Args = map[string]any{"from": keep.Prev.Candidate}
		if rule != "" {
			pick.Args["rule"] = rule // nothing else is listed: the one candidate is kept despite the rule
		}
		return true
	}
	s.chooseTierCandidate(ctx, machine, pick, list, i)
	args["from"] = i
	pick.Reason, pick.Args = dispatchSwitched, args
	return true
}

// modelRoutingLatest reads a to-do's latest dispatch of the profile in use (internal/db model_routing_samples.go).
type modelRoutingLatest interface {
	LatestModelRouting(ctx context.Context, todoID string) (*db.ModelRoutingRow, error)
}

var _ modelRoutingLatest = (*db.DB)(nil)

// taskKeep is what a start of t keeps of its previous Agent: the dispatch its plan recorded (a retry, a queued or
// restarted start read back), else the latest dispatch recorded for its to-do (a task_plan continuation, which shares
// the to-do it continues). nil for a first start, or when the previous Agent did not run on a tier.
func (s *Service) taskKeep(ctx context.Context, t *PlanTask) *tierKeep {
	todo := cmp.Or(t.TodoID, t.ReuseTodo, t.Continues)
	var prev *TaskDispatch
	if t.Dispatch != nil {
		d := *t.Dispatch
		prev = &d
	} else if st, ok := s.store().(modelRoutingLatest); ok && todo != "" {
		read, cancel := context.WithTimeout(ctx, estimateReadTimeout)
		row, err := st.LatestModelRouting(read, todo)
		cancel()
		if err != nil {
			s.logf("task %s: previous dispatch: %v", todo, err)
		}
		if row != nil {
			var detail modelRoutingDetail
			_ = json.Unmarshal(row.Detail, &detail)
			prev = &TaskDispatch{Tier: row.Tier, Candidate: row.Candidate, Of: row.Of, Launcher: row.Launcher, Model: row.Model, Reason: row.Reason, Args: detail.Args}
		}
	}
	if prev == nil || strings.TrimSpace(prev.Tier) == "" || presetDispatch(prev.Args) {
		return nil
	}
	keep := &tierKeep{Prev: *prev}
	if todo != "" {
		if td, ok, err := s.todo(ctx, todo); err == nil && ok && td.Supervision != nil {
			keep.Rework = td.Supervision.Round
		}
	}
	return keep
}

// keepsTier is whether a start whose params name launcher, model and tier as they are now (after a start-defaults
// preset) may keep prev's candidate: the start names no launcher or model of its own, and either no tier or prev's.
func keepsTier(p *ModelProfile, params map[string]any, prev TaskDispatch) bool {
	launcher, _ := params["launcher"].(string)
	model, _ := params["model"].(string)
	tier, _ := params["tier"].(string)
	if strings.TrimSpace(launcher) != "" || strings.TrimSpace(model) != "" {
		return false
	}
	if strings.TrimSpace(tier) == "" {
		return true
	}
	if p == nil {
		return tier == prev.Tier
	}
	a, okA := p.lookupTier(tier)
	b, okB := p.lookupTier(prev.Tier)
	return okA && okB && a == b
}

// failedStart is whether a start's error counts against its candidate: a start that waits in a queue (admission, a
// workspace, a gateway's quota) or was handled elsewhere did not fail.
func failedStart(err error) bool {
	var quota *gatewayQuotaWaitError
	var wsWait *workspaceBusyQueueError
	return err != nil && !errors.As(err, &quota) && !errors.As(err, &wsWait) && !isAdmissionDenied(err) &&
		!errors.Is(err, errGatewayTaskHandled)
}
