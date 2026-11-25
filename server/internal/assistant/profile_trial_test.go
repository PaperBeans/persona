package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// ModelRoutingTrialSummaries mirrors db.DB.ModelRoutingTrialSummaries in memory for assistant tests.
func (m *memStore) ModelRoutingTrialSummaries(_ context.Context, since time.Time) ([]db.ModelRoutingTrialSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	decisionKey := func(r db.ModelRoutingRow) string {
		if r.SessionID != "" {
			return "s:" + r.SessionID
		}
		return fmt.Sprintf("r:%s:%s:%d:%d", r.TodoID, r.PlanID, r.TaskIndex, r.At.UnixNano())
	}
	activeByKey := map[string]db.ModelRoutingRow{}
	for _, r := range m.routing {
		if r.Active && !r.At.Before(since.Add(-time.Minute)) {
			activeByKey[decisionKey(r)] = r
		}
	}
	type profKey struct{ profile, decision string }
	trials := map[profKey]db.ModelRoutingRow{}
	for _, r := range m.routing {
		if !r.Active && !r.At.Before(since) {
			trials[profKey{r.ProfileID, decisionKey(r)}] = r
		}
	}
	counts := map[string]*db.ModelRoutingTrialSummary{}
	for _, r := range trials {
		sum := counts[r.ProfileID]
		if sum == nil {
			sum = &db.ModelRoutingTrialSummary{ProfileID: r.ProfileID}
			counts[r.ProfileID] = sum
		}
		sum.Dispatches++
		if a, ok := activeByKey[decisionKey(r)]; ok && (r.Launcher != a.Launcher || r.Model != a.Model) {
			sum.WouldSwitch++
		}
	}
	out := make([]db.ModelRoutingTrialSummary, 0, len(counts))
	for _, sum := range counts {
		out = append(out, *sum)
	}
	slices.SortFunc(out, func(a, b db.ModelRoutingTrialSummary) int { return cmp.Compare(a.ProfileID, b.ProfileID) })
	return out, nil
}

// Trials run on their own goroutine in production; the package's tests run them in place so rows are there to read.
func init() { startTrial = func(f func()) { f() } }

// trialTestStore wraps memStore with programmable failure on trial writes and summaries, and optional 14-day
// efficiency samples.
type trialTestStore struct {
	*memStore
	failTrialWrite error
	failSummaries  error
	samples        map[string][]db.ModelRoutingSample
}

func (ts *trialTestStore) ModelRoutingTrialSummaries(ctx context.Context, since time.Time) ([]db.ModelRoutingTrialSummary, error) {
	if ts.failSummaries != nil {
		return nil, ts.failSummaries
	}
	return ts.memStore.ModelRoutingTrialSummaries(ctx, since)
}

func (ts *trialTestStore) PutModelRouting(ctx context.Context, r db.ModelRoutingRow) error {
	if !r.Active && ts.failTrialWrite != nil {
		return ts.failTrialWrite
	}
	return ts.memStore.PutModelRouting(ctx, r)
}

func (ts *trialTestStore) ModelRoutingSamples(_ context.Context, tier string, _, _ time.Time, _ int) ([]db.ModelRoutingSample, error) {
	return ts.samples[tier], nil
}

// Every active dispatch trials each inactive profile (capped at 20, stable ID order), resolves by name or alias,
// writes active=false rows with per-candidate scores when multiple candidates exist, skips profiles without the tier,
// and never starts anything extra (#14702).
func TestDispatchTrialsInactiveProfiles(t *testing.T) {
	f := newFakeHub().withMachine()
	f.launcherList = []proto.Launcher{{Name: "claude"}, {Name: "fast", Model: "m-fast"}}
	st := &memStore{}
	s := newServiceOn(t, f, st)
	ctx := context.Background()

	profiles := []ModelProfile{
		{ID: "active-prof", Name: "Active", Tiers: map[string]ModelTier{
			"dev": {Launcher: "claude", Model: "m-active"},
		}},
		// Resolved by alias "dev", with two candidates so scores/headroom detail is recorded.
		{ID: "z-alias", Name: "Z Alias", Tiers: map[string]ModelTier{
			"coding": {
				Launcher:   "fast",
				Model:      "m-fast",
				Aliases:    []string{"dev"},
				Candidates: []TierCandidate{{Launcher: "fast", Model: "m-fast"}, {Launcher: "claude", Model: "m-alt"}},
			},
		}},
		// Has no "dev" tier: must write nothing.
		{ID: "a-notier", Name: "No Tier", Tiers: map[string]ModelTier{
			"deploy": {Launcher: "claude", Model: "m-deploy"},
		}},
	}
	// Add 22 more inactive profiles with "dev" so total inactive profiles with/without tier exceeds maxModelProfiles (20).
	for i := 1; i <= 22; i++ {
		id := fmt.Sprintf("p-%02d", i)
		profiles = append(profiles, ModelProfile{
			ID:   id,
			Name: id,
			Tiers: map[string]ModelTier{
				"dev": {Launcher: "fast", Model: fmt.Sprintf("m-%02d", i)},
			},
		})
	}
	if err := st.PutSetting(ctx, modelProfilesKey, modelProfiles{Revision: 1, Profiles: profiles}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutSetting(ctx, modelProfileStateKey, modelProfileState{Active: "active-prof"}); err != nil {
		t.Fatal(err)
	}

	activePick, err := s.pickTierCandidate(ctx, "dev", tierFacts{Machine: "server"})
	if err != nil {
		t.Fatal(err)
	}
	row := modelRoutingOf(activePick, "server")
	row.TodoID, row.PlanID, row.TaskIndex, row.SessionID = "todo-1", "plan-1", 0, "sess-1"
	s.recordModelRouting(ctx, row)

	rows := st.modelRoutings()
	// 1 active row + at most 20 inactive profiles considered: "a-notier" is #1 in sorted ID order (writes nothing),
	// followed by p-01..p-19 (19 rows). Total = 1 active + 19 trial rows = 20 rows; z-alias and p-20..p-22 are beyond the 20-profile cap.
	if len(rows) != 20 {
		t.Fatalf("got %d model_routing rows, want 20 (1 active + 19 trials)", len(rows))
	}
	if !rows[0].Active || rows[0].ProfileID != "active-prof" {
		t.Fatalf("first row must be active: %+v", rows[0])
	}
	for i, tr := range rows[1:] {
		wantID := fmt.Sprintf("p-%02d", i+1)
		if tr.Active || tr.ProfileID != wantID || tr.TodoID != "todo-1" || tr.PlanID != "plan-1" || tr.SessionID != "sess-1" || !tr.At.Equal(rows[0].At) {
			t.Fatalf("trial[%d] = %+v, want inactive %s matching active decision", i, tr, wantID)
		}
	}

	// Now test with just active-prof, a-notier, and z-alias to verify alias resolution and multi-candidate score detail.
	st.mu.Lock()
	st.routing = nil
	st.mu.Unlock()
	if err := st.PutSetting(ctx, modelProfilesKey, modelProfiles{Revision: 2, Profiles: profiles[:3]}); err != nil {
		t.Fatal(err)
	}
	s.recordModelRouting(ctx, row)
	rows = st.modelRoutings()
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (active + z-alias): %+v", len(rows), rows)
	}
	tr := rows[1]
	if tr.Active || tr.ProfileID != "z-alias" || tr.Tier != "coding" || tr.Candidate != 0 || tr.Of != 2 || tr.Launcher != "fast" || tr.Model != "m-fast" {
		t.Fatalf("alias trial row = %+v", tr)
	}
	var detail modelRoutingDetail
	if err := json.Unmarshal(tr.Detail, &detail); err != nil || len(detail.Scores) != 2 || detail.Scores[0].Score == nil {
		t.Fatalf("trial detail missing per-candidate scores: %s (%v)", tr.Detail, err)
	}
	if len(startsOf(f)) != 0 {
		t.Fatalf("trials must never start sessions, got %d starts", len(startsOf(f)))
	}
}

// A real dispatch start is completely unaffected when trial scoring panics or trial writes fail (#14702).
func TestDispatchStartUnaffectedByTrialErrors(t *testing.T) {
	cases := []struct {
		name           string
		panicOnTrial   bool
		failTrialWrite error
	}{
		{name: "trial scorer panic", panicOnTrial: true},
		{name: "trial store write error", failTrialWrite: errors.New("disk full")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeHub().withMachine()
			f.launcherList = []proto.Launcher{{Name: "claude"}, {Name: "fast"}}
			ts := &trialTestStore{memStore: &memStore{}, failTrialWrite: tc.failTrialWrite}
			s := newServiceOn(t, f, ts)
			ctx := context.Background()
			ps := modelProfiles{Revision: 1, Profiles: []ModelProfile{
				{ID: "perf", Name: "Perf", Tiers: map[string]ModelTier{"dev": {Launcher: "claude", Model: "m-perf"}}},
				{ID: "econ", Name: "Econ", Tiers: map[string]ModelTier{
					"dev": {Launcher: "fast", Model: "m-econ", Candidates: []TierCandidate{{Launcher: "fast", Model: "m-econ"}, {Launcher: "claude", Model: "m-fb"}}},
				}},
			}}
			if err := ts.PutSetting(ctx, modelProfilesKey, ps); err != nil {
				t.Fatal(err)
			}
			if err := ts.PutSetting(ctx, modelProfileStateKey, modelProfileState{Active: "perf"}); err != nil {
				t.Fatal(err)
			}
			if tc.panicOnTrial {
				s.tierRank = func(_ context.Context, in tierRankInput) (int, string, bool) {
					if !in.Active {
						panic("simulated trial scoring panic")
					}
					return 0, dispatchFirst, true
				}
			}

			pick, err := s.pickTierCandidate(ctx, "dev", tierFacts{Machine: "server"})
			if err != nil {
				t.Fatal(err)
			}
			row := modelRoutingOf(pick, "server")
			row.TodoID, row.SessionID = "todo-ok", "sess-ok"
			s.recordModelRouting(ctx, row)

			rows := ts.modelRoutings()
			if len(rows) != 1 || !rows[0].Active || rows[0].ProfileID != "perf" {
				t.Fatalf("active row must still be recorded cleanly: %+v", rows)
			}
		})
	}
}

// GET /api/v1/model-profiles includes trial{days:7,dispatches,would_switch} on inactive profiles only and omits it on
// the active profile (#14702).
func TestModelProfilesViewTrialSummary(t *testing.T) {
	s, _, st := modelService(t)
	ctx := context.Background()
	storeProfiles(t, st,
		ModelProfile{ID: "perf", Name: "Performance", Tiers: map[string]ModelTier{"dev": {Launcher: "claude", Model: "m1"}}},
		ModelProfile{ID: "econ", Name: "Economy", Tiers: map[string]ModelTier{"dev": {Launcher: "codex", Model: "m2"}}},
		ModelProfile{ID: "idle", Name: "Idle"},
	)
	if err := st.PutSetting(ctx, modelProfileStateKey, modelProfileState{Active: "perf"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, r := range []db.ModelRoutingRow{
		// Decision 1: econ differs -> would_switch = 1
		{At: now.Add(-time.Hour), SessionID: "s-1", ProfileID: "perf", Active: true, Tier: "dev", Launcher: "claude", Model: "m1", Reason: "only"},
		{At: now.Add(-time.Hour), SessionID: "s-1", ProfileID: "econ", Active: false, Tier: "dev", Launcher: "codex", Model: "m2", Reason: "only"},
		// Decision 2: econ matches -> would_switch unchanged
		{At: now.Add(-30 * time.Minute), SessionID: "s-2", ProfileID: "perf", Active: true, Tier: "dev", Launcher: "claude", Model: "m1", Reason: "only"},
		{At: now.Add(-30 * time.Minute), SessionID: "s-2", ProfileID: "econ", Active: false, Tier: "dev", Launcher: "claude", Model: "m1", Reason: "only"},
		// Old decision (9 days ago): ignored
		{At: now.AddDate(0, 0, -9), SessionID: "s-old", ProfileID: "perf", Active: true, Tier: "dev", Launcher: "claude", Model: "m1", Reason: "only"},
		{At: now.AddDate(0, 0, -9), SessionID: "s-old", ProfileID: "econ", Active: false, Tier: "dev", Launcher: "codex", Model: "m2", Reason: "only"},
	} {
		if err := st.PutModelRouting(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	w := request(s, http.MethodGet, "/api/v1/model-profiles", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/model-profiles: %d %s", w.Code, w.Body.String())
	}
	var raw struct {
		Profiles []map[string]json.RawMessage `json:"profiles"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	byID := map[string]map[string]json.RawMessage{}
	for _, p := range raw.Profiles {
		var id string
		_ = json.Unmarshal(p["id"], &id)
		byID[id] = p
	}
	if _, hasTrial := byID["perf"]["trial"]; hasTrial {
		t.Fatalf("active profile must omit trial, got %s", byID["perf"]["trial"])
	}
	checkTrial := func(id string, want ModelProfileTrial) {
		t.Helper()
		rawTrial, ok := byID[id]["trial"]
		if !ok {
			t.Fatalf("inactive profile %s missing trial", id)
		}
		var got ModelProfileTrial
		if err := json.Unmarshal(rawTrial, &got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("profile %s trial = %+v, want %+v", id, got, want)
		}
	}
	checkTrial("econ", ModelProfileTrial{Days: 7, Dispatches: 2, WouldSwitch: 1})
	checkTrial("idle", ModelProfileTrial{Days: 7, Dispatches: 0, WouldSwitch: 0})
}

// GET /api/v1/model-profiles/{id}/candidates returns the exact contract shape (0-based index, known/stale/unknown
// headroom never shown as full, 14-day stats) and 404 on an unknown profile id (#14702).
func TestModelProfileCandidatesEndpoint(t *testing.T) {
	now := time.Now().UTC()
	f := newFakeHub().withMachine()
	ts := &trialTestStore{
		memStore: &memStore{},
		samples: map[string][]db.ModelRoutingSample{
			"dev": {
				{Tier: "dev", Launcher: "gw-claude", Model: "m-a", ActualMinutes: 10, FirstPass: true},
				{Tier: "dev", Launcher: "gw-claude", Model: "m-a", ActualMinutes: 20, FirstPass: false},
				{Tier: "dev", Launcher: "gw-claude", Model: "m-a", ActualMinutes: 30, FirstPass: true},
			},
		},
	}
	s := newServiceOn(t, f, ts)
	ctx := context.Background()
	setSettings(t, s, func(v *Settings) { v.MachineID = "server" })

	// Managed launchers map launcher names to their upstream providers (claude, codex, unknown-api).
	if err := ts.PutSetting(ctx, launchersManagedKey, []storedLauncher{
		{ManagedLauncher: proto.ManagedLauncher{Name: "gw-claude", Agent: "claude"}},
		{ManagedLauncher: proto.ManagedLauncher{Name: "gw-codex", Agent: "codex"}},
		{ManagedLauncher: proto.ManagedLauncher{Name: "third-party", Agent: ""}},
	}); err != nil {
		t.Fatal(err)
	}
	// Fresh claude account (17% headroom) and stale codex account (must report known=false, stale=true, never full).
	staleAcct := proto.UsageAccount{
		ID: "codex-1", Provider: "codex", Stale: true, SampledAt: now.Add(-2 * time.Hour),
		Windows: []proto.UsageWindow{paceAt(now, "5h", 300, time.Hour, 0, "")},
	}
	freshAcct := proto.UsageAccount{
		ID: "claude-1", Provider: "claude", SampledAt: now,
		Windows: []proto.UsageWindow{paceAt(now, "5h", 300, time.Hour, 0, "")},
	}
	s.devs = &fleetDevs{accounts: []proto.UsageAccount{freshAcct, staleAcct}}

	ps := modelProfiles{Revision: 1, Profiles: []ModelProfile{
		{
			ID:   "economy",
			Name: "Economy",
			Tiers: map[string]ModelTier{
				"dev": {
					Launcher: "gw-claude",
					Model:    "m-a",
					Candidates: []TierCandidate{
						{Launcher: "gw-claude", Model: "m-a"},
						{Launcher: "gw-codex", Model: "m-b"},
						{Launcher: "third-party", Model: "m-c"},
					},
				},
			},
		},
	}}
	if err := ts.PutSetting(ctx, modelProfilesKey, ps); err != nil {
		t.Fatal(err)
	}

	// 404 on unknown profile id.
	if w := request(s, http.MethodGet, "/api/v1/model-profiles/nonexistent/candidates", ""); w.Code != http.StatusNotFound {
		t.Fatalf("unknown profile status = %d, want 404 (%s)", w.Code, w.Body.String())
	}

	w := request(s, http.MethodGet, "/api/v1/model-profiles/economy/candidates", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET candidates: %d %s", w.Code, w.Body.String())
	}
	var got ModelProfileCandidatesView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Days != 14 {
		t.Fatalf("days = %d, want 14", got.Days)
	}
	dev := got.Tiers["dev"]
	if len(dev) != 3 {
		t.Fatalf("dev candidates = %+v, want 3", dev)
	}
	// Candidate 0: fresh claude -> known=true, ~17%, stale=false, 3 samples (p50=20, first_pass=2/3).
	if dev[0].Index != 0 || dev[0].Launcher != "gw-claude" || dev[0].Model != "m-a" ||
		!dev[0].Headroom.Known || dev[0].Headroom.Stale || math.Abs(dev[0].Headroom.Percent-17.0) > 0.2 ||
		dev[0].Stats.N != 3 || dev[0].Stats.MinutesP50 == nil || *dev[0].Stats.MinutesP50 != 20 ||
		dev[0].Stats.FirstPass == nil || math.Abs(*dev[0].Stats.FirstPass-2.0/3.0) > 1e-6 {
		t.Fatalf("candidate 0 = %+v", dev[0])
	}
	// Candidate 1: stale codex -> known=false, percent=0, stale=true (never shown as full).
	if dev[1].Index != 1 || dev[1].Launcher != "gw-codex" || dev[1].Headroom.Known || !dev[1].Headroom.Stale || dev[1].Headroom.Percent != 0 || dev[1].Stats.N != 0 ||
		dev[1].Stats.MinutesP50 != nil || dev[1].Stats.FirstPass != nil {
		t.Fatalf("candidate 1 = %+v", dev[1])
	}
	// Candidate 2: unreadable provider -> known=false, percent=0, stale=false (never shown as full).
	if dev[2].Index != 2 || dev[2].Launcher != "third-party" || dev[2].Headroom.Known || dev[2].Headroom.Stale || dev[2].Headroom.Percent != 0 {
		t.Fatalf("candidate 2 = %+v", dev[2])
	}
}

func TestModelProfileCandidatesRouteRegistered(t *testing.T) {
	const route = "GET /api/v1/model-profiles/{id}/candidates"
	if !slices.Contains(assistantwire.Routes, route) {
		t.Fatalf("assistantwire.Routes missing %q", route)
	}
	s, _, _ := modelService(t)
	if !slices.Contains(s.Routes(), route) {
		t.Fatalf("Service.Routes missing %q", route)
	}
}

// A dispatch does not wait for its trial: the active row is written, and the trial runs on its own (#14702).
func TestDispatchDoesNotWaitForTheTrial(t *testing.T) {
	var pending []func()
	startTrial = func(f func()) { pending = append(pending, f) }
	t.Cleanup(func() { startTrial = func(f func()) { f() } })
	f := newFakeHub().withMachine()
	f.launcherList = []proto.Launcher{{Name: "claude"}, {Name: "fast"}}
	st := &memStore{}
	s := newServiceOn(t, f, st)
	ctx := context.Background()
	ps := modelProfiles{Revision: 1, Profiles: []ModelProfile{
		{ID: "perf", Name: "Perf", Tiers: map[string]ModelTier{"dev": {Launcher: "claude", Model: "m-perf"}}},
		{ID: "econ", Name: "Econ", Tiers: map[string]ModelTier{"dev": {Launcher: "fast", Model: "m-econ"}}},
	}}
	if err := st.PutSetting(ctx, modelProfilesKey, ps); err != nil {
		t.Fatal(err)
	}
	if err := st.PutSetting(ctx, modelProfileStateKey, modelProfileState{Active: "perf"}); err != nil {
		t.Fatal(err)
	}
	pick, err := s.pickTierCandidate(ctx, "dev", tierFacts{Machine: "server"})
	if err != nil {
		t.Fatal(err)
	}
	row := modelRoutingOf(pick, "server")
	row.SessionID = "sess-async"
	s.recordModelRouting(ctx, row)
	if rows := st.modelRoutings(); len(rows) != 1 || !rows[0].Active || len(pending) != 1 {
		t.Fatalf("before the trial runs: rows %+v, pending trials %d; want the active row only and one trial queued", rows, len(pending))
	}
	pending[0]()
	if rows := st.modelRoutings(); len(rows) != 2 || rows[1].Active || rows[1].ProfileID != "econ" || rows[1].SessionID != "sess-async" {
		t.Fatalf("after the trial: %+v", rows)
	}
}

// A trial also finds another profile's tier by the active tier's aliases, not only by its own name (#14702).
func TestTrialMatchesTheActiveTiersAliases(t *testing.T) {
	f := newFakeHub().withMachine()
	f.launcherList = []proto.Launcher{{Name: "claude"}, {Name: "fast"}}
	st := &memStore{}
	s := newServiceOn(t, f, st)
	ctx := context.Background()
	ps := modelProfiles{Revision: 1, Profiles: []ModelProfile{
		{ID: "perf", Name: "Perf", Tiers: map[string]ModelTier{"review": {Launcher: "claude", Model: "m-perf", Aliases: []string{"check"}}}},
		{ID: "old", Name: "Old", Tiers: map[string]ModelTier{"check": {Launcher: "fast", Model: "m-old"}}},
	}}
	if err := st.PutSetting(ctx, modelProfilesKey, ps); err != nil {
		t.Fatal(err)
	}
	if err := st.PutSetting(ctx, modelProfileStateKey, modelProfileState{Active: "perf"}); err != nil {
		t.Fatal(err)
	}
	pick, err := s.pickTierCandidate(ctx, "check", tierFacts{Machine: "server"})
	if err != nil {
		t.Fatal(err)
	}
	row := modelRoutingOf(pick, "server")
	row.SessionID = "sess-alias"
	s.recordModelRouting(ctx, row)
	rows := st.modelRoutings()
	if len(rows) != 2 || rows[0].Tier != "review" || rows[1].ProfileID != "old" || rows[1].Tier != "check" || rows[1].Model != "m-old" {
		t.Fatalf("rows = %+v, want the active review row and a trial on old's check tier", rows)
	}
}

// When the trial counts cannot be read, no profile shows a trial rather than a wrong 0 (#14702).
func TestTrialSummariesUnknownWhenTheStoreFails(t *testing.T) {
	ts := &trialTestStore{memStore: &memStore{}, failSummaries: errors.New("db down")}
	s := newServiceOn(t, newFakeHub(), ts)
	if got, ok := s.modelProfileTrialSummaries(context.Background(), time.Now().UTC()); ok || got != nil {
		t.Fatalf("summaries = %v, %v; want nil, false", got, ok)
	}
	ok := &trialTestStore{memStore: &memStore{}}
	s = newServiceOn(t, newFakeHub(), ok)
	if got, known := s.modelProfileTrialSummaries(context.Background(), time.Now().UTC()); !known || len(got) != 0 {
		t.Fatalf("summaries on an empty store = %v, %v; want empty, true", got, known)
	}
}
