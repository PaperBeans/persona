package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nwverse/persona/server/internal/db"
)

// LatestModelRouting answers a to-do's latest active model_routing row from the rows written so far, as the database
// does (candidate_keep.go taskKeep).
func (m *memStore) LatestModelRouting(_ context.Context, todo string) (*db.ModelRoutingRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.routing) - 1; i >= 0; i-- {
		if r := m.routing[i]; r.Active && r.TodoID == todo {
			return &r, nil
		}
	}
	return nil, nil
}

// A start with the previous Agent's dispatch keeps that candidate while it is listed, present and healthy; a clear
// problem (two rework rounds, three failed starts in a row) re-picks among the others with the rule named; a candidate
// gone from the list, another tier, or one this computer cannot run now leaves the usual choice (#14701).
func TestTierPickKeepsThePreviousCandidate(t *testing.T) {
	s, _ := pickerService(t, map[string]ModelTier{
		"dev":   {Launcher: "fast", Model: "m1", Candidates: []TierCandidate{{Launcher: "fast", Model: "m1"}, {Launcher: "claude", Model: "m2"}}, Aliases: []string{"standard"}},
		"gone":  {Launcher: "missing", Model: "m0", Candidates: []TierCandidate{{Launcher: "missing", Model: "m0"}, {Launcher: "fast", Model: "m1"}}},
		"one":   {Launcher: "claude", Model: "m2"},
		"other": {Launcher: "claude", Model: "m3"},
	})
	ctx := context.Background()
	prev := func(tier string, i int, launcher, model string) TaskDispatch {
		return TaskDispatch{Tier: tier, Candidate: i, Of: 2, Launcher: launcher, Model: model, Reason: "first"}
	}
	cases := []struct {
		name      string
		tier      string
		keep      *tierKeep
		candidate int
		reason    string
		rule      string
	}{
		{name: "no previous dispatch: the usual choice", tier: "dev", candidate: 0, reason: dispatchFirst},
		{name: "kept", tier: "dev", keep: &tierKeep{Prev: prev("dev", 1, "claude", "m2")}, candidate: 1, reason: dispatchKept},
		{name: "kept where the list moved it", tier: "dev", keep: &tierKeep{Prev: prev("dev", 0, "claude", "m2")}, candidate: 1, reason: dispatchKept},
		{name: "kept under an alias of the tier", tier: "dev", keep: &tierKeep{Prev: prev("standard", 1, "claude", "m2")}, candidate: 1, reason: dispatchKept},
		{name: "no longer listed: picked again", tier: "dev", keep: &tierKeep{Prev: prev("dev", 1, "claude", "m9")}, candidate: 0, reason: dispatchFirst},
		{name: "another tier's dispatch is not this one's", tier: "dev", keep: &tierKeep{Prev: prev("other", 0, "claude", "m2")}, candidate: 0, reason: dispatchFirst},
		{name: "not on this computer: picked again", tier: "gone", keep: &tierKeep{Prev: prev("gone", 0, "missing", "m0")}, candidate: 1, reason: dispatchUnavailable},
		{name: "one rework keeps", tier: "dev", keep: &tierKeep{Prev: prev("dev", 1, "claude", "m2"), Rework: 1}, candidate: 1, reason: dispatchKept},
		{name: "two reworks switch", tier: "dev", keep: &tierKeep{Prev: prev("dev", 1, "claude", "m2"), Rework: 2}, candidate: 0, reason: dispatchSwitched, rule: switchRework},
		{name: "three failed starts switch", tier: "dev", keep: &tierKeep{Prev: func() TaskDispatch { d := prev("dev", 0, "fast", "m1"); d.Failed = 3; return d }()},
			candidate: 1, reason: dispatchSwitched, rule: switchFailing},
		{name: "one candidate is kept", tier: "one", keep: &tierKeep{Prev: prev("one", 0, "claude", "m2")}, candidate: 0, reason: dispatchKept},
		{name: "one candidate stays despite a rule", tier: "one", keep: &tierKeep{Prev: prev("one", 0, "claude", "m2"), Rework: 3}, candidate: 0, reason: dispatchKept, rule: switchRework},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pick, err := s.pickTierCandidate(ctx, c.tier, tierFacts{Machine: "server", Keep: c.keep})
			if err != nil {
				t.Fatal(err)
			}
			if pick.Candidate != c.candidate || pick.Reason != c.reason {
				t.Fatalf("picked %d (%s), want %d (%s): %+v", pick.Candidate, pick.Reason, c.candidate, c.reason, pick)
			}
			if got, _ := pick.Args["rule"].(string); got != c.rule {
				t.Fatalf("rule %q, want %q (%v)", got, c.rule, pick.Args)
			}
			d := pick.dispatch()
			if c.reason == dispatchKept || c.reason == dispatchSwitched {
				if _, ok := d.Args["from"]; !ok {
					t.Fatalf("a %s dispatch does not say where from: %+v", c.reason, d)
				}
			}
		})
	}
}

// Several candidates: the scorer chooses and its lines (score, headroom, efficiency) go to the model_routing detail;
// with no data the first usable candidate wins, as without scoring.
func TestScoredPickRecordsItsDetail(t *testing.T) {
	s, _ := pickerService(t, map[string]ModelTier{
		"dev": {Launcher: "fast", Model: "m1", Candidates: []TierCandidate{{Launcher: "fast", Model: "m1"}, {Launcher: "claude", Model: "m2"}}},
	})
	pick, err := s.pickTierCandidate(context.Background(), "dev", tierFacts{Machine: "server"})
	if err != nil {
		t.Fatal(err)
	}
	if pick.Candidate != 0 || pick.Reason != dispatchFirst || len(pick.Scores) != 2 || pick.Scores[0].Score == nil || pick.Scores[1].Score == nil {
		t.Fatalf("pick: %+v", pick)
	}
	var detail modelRoutingDetail
	if err := json.Unmarshal(modelRoutingOf(pick, "server").Detail, &detail); err != nil || len(detail.Scores) != 2 || detail.Scores[1].Launcher != "claude" {
		t.Fatalf("detail: %+v %v", detail, err)
	}
	// A score that differs lands on the plan card with the candidates' lines.
	pick.Candidate, pick.Reason = 1, dispatchScore
	if d := pick.dispatch(); d.Reason != dispatchScore || d.Args["candidates"] == nil {
		t.Fatalf("dispatch: %+v", d)
	}
}

func TestKeepsTier(t *testing.T) {
	p := &ModelProfile{Tiers: map[string]ModelTier{"core": {Launcher: "claude", Aliases: []string{"strong"}}, "dev": {Launcher: "codex"}}}
	prev := TaskDispatch{Tier: "core", Launcher: "claude"}
	for _, c := range []struct {
		params map[string]any
		want   bool
	}{
		{map[string]any{}, true},
		{map[string]any{"tier": "core"}, true},
		{map[string]any{"tier": "strong"}, true},
		{map[string]any{"tier": "dev"}, false},
		{map[string]any{"launcher": "claude"}, false},
		{map[string]any{"model": "m"}, false},
		{map[string]any{"effort": "high"}, true}, // effort alone lays over the kept candidate field by field
	} {
		if got := keepsTier(p, c.params, prev); got != c.want {
			t.Errorf("keepsTier(%v) = %v, want %v", c.params, got, c.want)
		}
	}
}

func TestFailedStartCounts(t *testing.T) {
	if !failedStart(errors.New("the example start failed")) || failedStart(nil) ||
		failedStart(&gatewayQuotaWaitError{}) || failedStart(errGatewayTaskHandled) {
		t.Fatal("failedStart")
	}
}

// candidateRetryHub is restartHub with tier deep listing two candidates of box's launchers: first (model-one, high)
// then second (model-two, low).
func candidateRetryHub(t *testing.T) (*Service, *fakeHub) {
	t.Helper()
	s, f := restartHub(t)
	saveTier(t, s, "deep", ModelTier{Launcher: "first", Model: "model-one", Effort: "high", Candidates: []TierCandidate{
		{Launcher: "first", Model: "model-one", Effort: "high"}, {Launcher: "second", Model: "model-two", Effort: "low"}}})
	return s, f
}

// continueOn plans a continuation of todo with extra and starts it, answering what its session.start carried.
func continueOn(t *testing.T, s *Service, f *fakeHub, todo string, extra map[string]any) [3]string {
	t.Helper()
	args := map[string]any{"continues": todo, "continues_why": "the first agent stopped"}
	for k, v := range extra {
		args[k] = v
	}
	before := len(startsOf(f))
	id := planRestartTask(t, s, args)
	if _, err := s.startPlan(context.Background(), id, nil, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the continuation's start", func() bool { return len(startsOf(f)) > before && s.taskStarting.Load() == 0 })
	return launchOf(startsOf(f)[before])
}

// A continuation keeps the candidate its previous Agent ran on although the tier's order changed; one that names a
// launcher or another tier runs on that; once the tier (another profile) no longer lists the candidate it re-picks
// among what the tier lists now (#14701, reversing #12618).
func TestContinuationKeepsTheCandidate(t *testing.T) {
	s, f := candidateRetryHub(t)
	todo, first := startRestartPlan(t, s, f, planRestartTask(t, s, map[string]any{"tier": "deep"}), nil)
	if got := launchOf(first); got != [3]string{"first", "model-one", "high"} {
		t.Fatalf("first start %v", got)
	}
	saveTier(t, s, "deep", ModelTier{Launcher: "second", Model: "model-two", Effort: "low", Candidates: []TierCandidate{
		{Launcher: "second", Model: "model-two", Effort: "low"}, {Launcher: "first", Model: "model-one", Effort: "high"}}})
	if got := continueOn(t, s, f, todo, nil); got != [3]string{"first", "model-one", "high"} {
		t.Fatalf("continuation started %v, want the kept candidate [first model-one high]", got)
	}
	rows := s.store().(restartHistoryStore).memStore.modelRoutings()
	if last := rows[len(rows)-1]; last.Reason != dispatchKept || last.Launcher != "first" || last.TodoID != todo {
		t.Fatalf("the continuation's model_routing row: %+v", last)
	}
	if got := continueOn(t, s, f, todo, map[string]any{"launcher": "other"}); got[0] != "other" {
		t.Fatalf("a continuation naming a launcher started %v", got)
	}
	// The previous Agent ran on other (no tier): nothing to keep, the tier is not adopted.
	if got := continueOn(t, s, f, todo, nil); got != [3]string{"first", "model-one", "high"} {
		t.Fatalf("after an Agent off the tier the continuation started %v, want the latest tier dispatch kept", got)
	}
	saveTier(t, s, "deep", ModelTier{Launcher: "other", Model: "model-zero", Candidates: []TierCandidate{
		{Launcher: "other", Model: "model-zero"}, {Launcher: "second", Model: "model-two", Effort: "low"}}})
	if got := continueOn(t, s, f, todo, nil); got[0] != "other" || got[1] != "model-zero" {
		t.Fatalf("continuation started %v, want the tier's first candidate now [other model-zero]", got)
	}
}

// A failed start keeps its dispatch with the candidate's failed starts counted, for the next try's re-pick rule.
func TestFailedStartCountsOnTheDispatch(t *testing.T) {
	s, f := candidateRetryHub(t)
	todo, _ := startRestartPlan(t, s, f, planRestartTask(t, s, map[string]any{"tier": "deep"}), errors.New("the example start failed"))
	_, p, i, err := s.remedyOf(context.Background(), todo)
	if err != nil {
		t.Fatal(err)
	}
	if d := p.Tasks[i].Dispatch; d == nil || d.Tier != "deep" || d.Launcher != "first" || d.Failed != 1 {
		t.Fatalf("the failed start's dispatch: %+v", d)
	}
}

// An explicit launcher or model of the retry wins over the kept candidate, field by field (#12032, #2661 kept).
func TestTaskRetryExplicitFieldsWinOverTheKeptCandidate(t *testing.T) {
	s, f := candidateRetryHub(t)
	todo, _ := startRestartPlan(t, s, f, planRestartTask(t, s, map[string]any{"tier": "deep"}), errors.New("the example start failed"))
	if got := launchOf(retryRestart(t, s, f, todo, map[string]any{"launcher": "other", "model": "model-three"})); got != [3]string{"other", "model-three", ""} {
		t.Fatalf("task_retry started %v, want the caller's [other model-three]", got)
	}
}
