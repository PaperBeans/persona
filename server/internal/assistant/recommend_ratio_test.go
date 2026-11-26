package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func ratioTestFleet(t *testing.T, ids ...string) (*Service, *fakeHub, *resourceDevs) {
	t.Helper()
	f := newFakeHub()
	f.devicesFull = true
	f.launcherList = []proto.Launcher{{Name: "work"}}
	for _, id := range ids {
		f.devices = append(f.devices, Device{ID: id, Name: id, Home: "/home/me", Online: true, Reachable: true})
		f.sessions[id] = nil
	}
	s := newStorelessService(t, f)
	d := &resourceDevs{
		fleetDevs: newFleetDevs(),
		machines:  map[string]*proto.Machine{},
		samples:   map[string]*proto.MetricSample{},
		versions:  map[string]*bool{},
	}
	for _, id := range ids {
		d.add(id, id)
	}
	s.devs = d
	on := true
	s.admission.cached = &AdmissionSettings{Enabled: &on}
	s.admission.at = s.placementNow()
	s.admission.machineClaims = map[string]int{}
	s.admission.directionClaims = map[string]int{}
	return s, f, d
}

func workingFleetRows(n int) []proto.Session {
	rows := make([]proto.Session, 0, n)
	for i := range n {
		rows = append(rows, proto.Session{
			SessionID: fmt.Sprintf("sess-%d", i),
			Status:    proto.StatusWorking,
		})
	}
	return rows
}

func TestRecommendMachineLowestRatioBeatsLowerLoad(t *testing.T) {
	s, _, d := ratioTestFleet(t, "low-load-crowded", "higher-load-open")
	const gib = int64(1024 * 1024 * 1024)

	d.machines["low-load-crowded"] = admissionMachine(8)
	d.samples["low-load-crowded"] = admissionSample(0.5, 32*gib, 16*gib, 4) // 16 GiB free (6.4 slots), ratio = 4/6.4 = 0.625
	s.fleetKeepPrevious("low-load-crowded", workingFleetRows(4))

	d.machines["higher-load-open"] = admissionMachine(8)
	d.samples["higher-load-open"] = admissionSample(8.0, 32*gib, 16*gib, 1) // 16 GiB free (6.4 slots), ratio = 1/6.4 = 0.15625
	s.fleetKeepPrevious("higher-load-open", workingFleetRows(1))

	pl := newPlacement("auto", "enforce")
	ctx := context.WithValue(context.Background(), placementKey{}, pl)
	got, ok := s.recommendMachine(ctx, "", nil)
	if !ok {
		t.Fatal("expected a healthy recommendation")
	}
	if got.ID != "higher-load-open" {
		t.Fatalf("picked %q, want higher-load-open (lowest working-to-slot ratio)", got.ID)
	}
	c := pl.candidate("higher-load-open")
	if c.SlotRatio == nil || math.Abs(*c.SlotRatio-0.15625) > 1e-6 {
		t.Fatalf("slot_ratio = %v, want ~0.15625", c.SlotRatio)
	}
}

func TestRecommendMachineRatioTiePrefersMoreAvailableMemory(t *testing.T) {
	t.Run("exact-ratio-tie", func(t *testing.T) {
		s, _, d := ratioTestFleet(t, "small-free", "large-free")
		const gib = int64(1024 * 1024 * 1024)

		// Both have ratio = 0.25: 1/(10/2.5) == 2/(20/2.5).
		d.machines["small-free"] = admissionMachine(8)
		d.samples["small-free"] = admissionSample(0.2, 16*gib, 6*gib, 1) // 10 GiB free
		s.fleetKeepPrevious("small-free", workingFleetRows(1))

		d.machines["large-free"] = admissionMachine(8)
		d.samples["large-free"] = admissionSample(4.0, 32*gib, 12*gib, 2) // 20 GiB free
		s.fleetKeepPrevious("large-free", workingFleetRows(2))

		got, ok := s.recommendMachine(context.Background(), "", nil)
		if !ok || got.ID != "large-free" {
			t.Fatalf("got (%q, %v), want large-free", got.ID, ok)
		}
	})

	t.Run("near-equal-ratio-prefers-more-memory", func(t *testing.T) {
		s, _, d := ratioTestFleet(t, "smaller-10g", "larger-20g")
		const gib = int64(1024 * 1024 * 1024)

		// 2 / (10.082 / 2.5) = 0.496 -> bucket 50; 4 / (20.0 / 2.5) = 0.500 -> bucket 50.
		d.machines["smaller-10g"] = admissionMachine(8)
		smallerFree := 10*gib + 84*1024*1024
		d.samples["smaller-10g"] = admissionSample(0.5, 16*gib, 16*gib-smallerFree, 2)
		s.fleetKeepPrevious("smaller-10g", workingFleetRows(2))

		d.machines["larger-20g"] = admissionMachine(8)
		d.samples["larger-20g"] = admissionSample(2.0, 32*gib, 12*gib, 4) // 20 GiB free
		s.fleetKeepPrevious("larger-20g", workingFleetRows(4))

		got, ok := s.recommendMachine(context.Background(), "", nil)
		if !ok || got.ID != "larger-20g" {
			t.Fatalf("got (%q, %v), want larger-20g", got.ID, ok)
		}
	})
}

func TestRecommendMachineRejectsBelow4GiBAndHighLoad(t *testing.T) {
	s, _, d := ratioTestFleet(t, "low-mem", "high-load", "healthy")
	const gib = int64(1024 * 1024 * 1024)

	// 3.5 GiB free (< 4 GiB floor) even with 0 working.
	d.machines["low-mem"] = admissionMachine(8)
	d.samples["low-mem"] = admissionSample(0.1, 16*gib, 16*gib-int64(3.5*float64(gib)), 0)
	s.fleetKeepPrevious("low-mem", nil)

	// Over 4x load-per-core redline (40 > 8*4).
	d.machines["high-load"] = admissionMachine(8)
	d.samples["high-load"] = admissionSample(40.0, 32*gib, 8*gib, 0)
	s.fleetKeepPrevious("high-load", nil)

	// Healthy candidate with 10 GiB free and 2 working (ratio 0.50).
	d.machines["healthy"] = admissionMachine(8)
	d.samples["healthy"] = admissionSample(2.0, 16*gib, 6*gib, 2)
	s.fleetKeepPrevious("healthy", workingFleetRows(2))

	got, ok := s.recommendMachine(context.Background(), "", nil)
	if !ok || got.ID != "healthy" {
		t.Fatalf("got (%q, %v), want healthy", got.ID, ok)
	}
}

func TestRecommendMachineCountsClaimsAndBalancesBatchWithinTwoX(t *testing.T) {
	s, _, d := ratioTestFleet(t, "m32-a", "m32-b", "m16-a", "m16-b")
	const gib = int64(1024 * 1024 * 1024)

	freeByMachine := map[string]int64{
		"m32-a": 25 * gib, // 10.0 slots
		"m32-b": 25 * gib, // 10.0 slots
		"m16-a": 12 * gib, // 4.8 slots
		"m16-b": 12 * gib, // 4.8 slots
	}
	for id, free := range freeByMachine {
		d.machines[id] = admissionMachine(8)
		d.samples[id] = admissionSample(1.0, 32*gib, 32*gib-free, 0)
		s.fleetKeepPrevious(id, nil)
	}

	ctx := context.Background()
	counts := map[string]int{}
	var releases []func()
	defer func() {
		for _, rel := range releases {
			rel()
		}
	}()

	for i := range 10 {
		got, ok := s.recommendMachine(ctx, "", nil)
		if !ok {
			t.Fatalf("task %d: expected recommendation", i)
		}
		lim := s.limitsFor(ctx, got.ID, "dir-batch", "", s.admissionSettings(ctx))
		lim.directionCap = 20
		rel, violation := s.claimIfAllowed(ctx, got.ID, "dir-batch", lim)
		if violation != nil {
			t.Fatalf("task %d on %s: unexpected violation %v", i, got.ID, violation)
		}
		releases = append(releases, rel)
		counts[got.ID]++
	}

	minRatio := math.Inf(1)
	maxRatio := 0.0
	for id, free := range freeByMachine {
		if counts[id] == 0 {
			t.Fatalf("machine %s received 0 tasks in batch: %v", id, counts)
		}
		slots := float64(free) / float64(placementSlotMemoryBytes)
		r := float64(counts[id]) / slots
		if r < minRatio {
			minRatio = r
		}
		if r > maxRatio {
			maxRatio = r
		}
	}
	if maxRatio/minRatio > 2.0 {
		t.Fatalf("batch ratio spread max/min = %.3f > 2.0 (counts=%v)", maxRatio/minRatio, counts)
	}
}

func TestPlacementFallbackUsesLowestSlotRatio(t *testing.T) {
	e := newPlacementTestEnv(t)
	const gib = int64(1024 * 1024 * 1024)

	// Give linux-idle lower CPU load (0.5) but 6 working agents on 15 GiB free (ratio = 6/6.0 = 1.0),
	// and linux-heavy higher CPU load (6.0) but 1 working agent on 20 GiB free (ratio = 1/8.0 = 0.125).
	e.d.samples["linux-idle"] = admissionSample(0.5, 32*gib, 17*gib, 6)
	e.s.fleetKeepPrevious("linux-idle", workingFleetRows(6))
	e.d.samples["linux-heavy"] = admissionSample(6.0, 32*gib, 12*gib, 1)
	e.s.fleetKeepPrevious("linux-heavy", workingFleetRows(1))

	ctx := context.Background()
	_, err := e.s.toolTaskPlan(ctx, "conv_pub", json.RawMessage(`{
		"tasks":[{
			"title":"Lowest ratio fallback",
			"prompt":"Verify fallback picks lowest slot ratio",
			"cwd":"~/repo",
			"role":"worker",
			"readonly":true,
			"goal":"Verify fallback picks lowest slot ratio",
			"user_requested":true,
			"why":"Ratio placement check",
			"placement_constraints":{"os":"linux"}
		}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	e.s.sweepPendingPlacement(ctx)
	waitFor(t, "fallback starts on linux-heavy", func() bool { return len(taskStartMachines(e.f.fakeHub)) == 1 })
	if starts := taskStartMachines(e.f.fakeHub); starts[0] != "linux-heavy" {
		t.Fatalf("fallback picked %v, want [linux-heavy]", starts)
	}
}
