package stagegate

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// TestStageGateStaysPure guards the package against accidental IO, network, clock,
// database, or non-stdlib dependencies: stagegate must remain a pure function of its inputs.
func TestStageGateStaysPure(t *testing.T) {
	allowed := []string{"cmp", "slices", "sort", "strings"}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("expected non-empty Go source files in stagegate package")
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if !slices.Contains(allowed, p) {
				t.Errorf("%s imports %s: stagegate package must stay pure (allowed stdlib only: %v)", name, p, allowed)
			}
		}
	}
}

func TestStageNormalizationAndOrdering(t *testing.T) {
	ordered := []Stage{
		StagePlan,
		StageContractReady,
		StageDevelop,
		StagePROpen,
		StageMerged,
		StageDone,
	}
	for i, s := range ordered {
		norm, ok := NormalizeStage("  " + strings.ToUpper(string(s)) + "  ")
		if !ok || norm != s {
			t.Fatalf("NormalizeStage(%q) = (%q, %v), want (%q, true)", s, norm, ok, s)
		}
		for j, req := range ordered {
			want := i >= j
			if got := StageSatisfies(s, req); got != want {
				t.Errorf("StageSatisfies(%q, %q) = %v, want %v", s, req, got, want)
			}
		}
	}

	for _, bad := range []string{"", "   ", "unknown", "in_progress"} {
		if _, ok := NormalizeStage(bad); ok {
			t.Errorf("NormalizeStage(%q) unexpectedly succeeded", bad)
		}
		if StageSatisfies(Stage(bad), StagePlan) {
			t.Errorf("StageSatisfies(%q, plan) should be false", bad)
		}
		if StageSatisfies(StageDone, Stage(bad)) {
			t.Errorf("StageSatisfies(done, %q) should be false", bad)
		}
	}
}

func TestCompletionEdgesDefaultAndAcceptedUnlock(t *testing.T) {
	nodes := []Node{
		{TodoID: "upstream", Done: false, Accepted: false},
		{TodoID: "down_done", After: CompletionEdges([]string{" upstream ", "upstream", ""}, false)},
		{TodoID: "down_acc", After: CompletionEdges([]string{"upstream"}, true)},
	}

	// Upstream not done -> both wait.
	evDone := Evaluate(nodes, "down_done", false)
	if evDone.Status != StatusWaiting || evDone.Ready {
		t.Fatalf("down_done while upstream open = %+v, want waiting", evDone)
	}
	if len(evDone.Unsatisfied) != 1 || evDone.Unsatisfied[0].Reason != ReasonNotDone {
		t.Fatalf("down_done unsatisfied = %+v, want not_done", evDone.Unsatisfied)
	}

	evAcc := Evaluate(nodes, "down_acc", false)
	if evAcc.Status != StatusWaiting || evAcc.Ready {
		t.Fatalf("down_acc while upstream open = %+v, want waiting", evAcc)
	}
	if len(evAcc.Unsatisfied) != 1 || evAcc.Unsatisfied[0].Reason != ReasonNotAccepted {
		t.Fatalf("down_acc unsatisfied = %+v, want not_accepted", evAcc.Unsatisfied)
	}

	// Upstream Done=true, Accepted=false -> down_done unlocks, down_acc still waits.
	nodes[0].Done = true
	evDone = Evaluate(nodes, "down_done", false)
	if evDone.Status != StatusReady || !evDone.Ready || len(evDone.Unsatisfied) != 0 {
		t.Fatalf("down_done after Done=true = %+v, want ready", evDone)
	}
	evAcc = Evaluate(nodes, "down_acc", false)
	if evAcc.Status != StatusWaiting || evAcc.Ready {
		t.Fatalf("down_acc while Accepted=false = %+v, want waiting", evAcc)
	}

	// Upstream Accepted=true -> both ready.
	nodes[0].Accepted = true
	evAcc = Evaluate(nodes, "down_acc", false)
	if evAcc.Status != StatusReady || !evAcc.Ready {
		t.Fatalf("down_acc after Accepted=true = %+v, want ready", evAcc)
	}
}

func TestUnlockOnStageOrderedMilestonesUnderTodosV2(t *testing.T) {
	nodes := []Node{
		{TodoID: "api", Stage: StagePlan},
		{
			TodoID: "web",
			After: []Edge{{
				PrereqID:      "api",
				Mode:          UnlockOnStage,
				RequiredStage: StageContractReady,
			}},
		},
		{
			TodoID: "e2e",
			After: []Edge{{
				PrereqID:      "api",
				Mode:          UnlockOnStage,
				RequiredStage: StagePROpen,
			}},
		},
	}

	// StagePlan -> neither web nor e2e is ready.
	evWeb := Evaluate(nodes, "web", true)
	if evWeb.Status != StatusWaiting || evWeb.Ready {
		t.Fatalf("web at api=plan = %+v, want waiting", evWeb)
	}
	if len(evWeb.Unsatisfied) != 1 ||
		evWeb.Unsatisfied[0].Reason != ReasonStageNotReached ||
		evWeb.Unsatisfied[0].RequiredStage != StageContractReady ||
		evWeb.Unsatisfied[0].CurrentStage != StagePlan {
		t.Fatalf("unexpected web unsatisfied detail: %+v", evWeb.Unsatisfied)
	}

	// StageContractReady (with whitespace/casing) -> web unlocks early, e2e still waits.
	nodes[0].Stage = "  CONTRACT_READY "
	evWeb = Evaluate(nodes, "web", true)
	if evWeb.Status != StatusReady || !evWeb.Ready || evWeb.EffectiveMode != UnlockOnStage {
		t.Fatalf("web at api=contract_ready = %+v, want ready/stage", evWeb)
	}
	evE2E := Evaluate(nodes, "e2e", true)
	if evE2E.Status != StatusWaiting || evE2E.Ready {
		t.Fatalf("e2e at api=contract_ready = %+v, want waiting", evE2E)
	}

	// StageMerged exceeds StagePROpen -> both web and e2e are ready.
	nodes[0].Stage = StageMerged
	if got := ReadyIDs(EvaluateAll(nodes, true)); !reflect.DeepEqual(got, []string{"api", "e2e", "web"}) {
		t.Fatalf("ReadyIDs at api=merged = %v, want [api e2e web]", got)
	}
}

func TestUnlockOnStageExplicitContracts(t *testing.T) {
	nodes := []Node{
		{
			TodoID:    "schema",
			Stage:     StageDevelop,
			Contracts: []string{" API_Contract "},
		},
		{
			TodoID: "client",
			After: []Edge{{
				PrereqID:          "schema",
				Mode:              UnlockOnStage,
				RequiredStage:     StageContractReady,
				RequiredContracts: []string{"api_contract", "SCHEMA_FROZEN", "api_contract"},
			}},
		},
	}

	// Stage is satisfied (develop >= contract_ready), but schema_frozen is missing.
	ev := Evaluate(nodes, "client", true)
	if ev.Status != StatusWaiting || ev.Ready {
		t.Fatalf("client missing schema_frozen = %+v, want waiting", ev)
	}
	if len(ev.Unsatisfied) != 1 ||
		ev.Unsatisfied[0].Reason != ReasonMissingContracts ||
		!reflect.DeepEqual(ev.Unsatisfied[0].MissingContracts, []string{"schema_frozen"}) {
		t.Fatalf("unexpected unsatisfied contracts detail: %+v", ev.Unsatisfied)
	}

	// Once schema_frozen is declared, client unlocks in parallel.
	nodes[0].Contracts = append(nodes[0].Contracts, "schema_frozen")
	ev = Evaluate(nodes, "client", true)
	if ev.Status != StatusReady || !ev.Ready {
		t.Fatalf("client after all contracts satisfied = %+v, want ready", ev)
	}
}

func TestTodosV2FalseLosslessFallback(t *testing.T) {
	nodes := []Node{
		{
			TodoID:    "up",
			Stage:     StagePROpen,
			Contracts: []string{"api_contract", "schema_frozen"},
			Done:      false,
			Accepted:  false,
		},
		{
			TodoID: "down_fallback_done",
			After: []Edge{{
				PrereqID:          "up",
				Mode:              UnlockOnStage,
				RequiredStage:     StageContractReady,
				RequiredContracts: []string{"api_contract"},
			}},
		},
		{
			TodoID: "down_fallback_acc",
			After: []Edge{{
				PrereqID:        "up",
				Mode:            UnlockOnStage,
				RequiredStage:   StageContractReady,
				RequireAccepted: true,
			}},
		},
	}

	// With todosV2=true, both unlock early because stage and contracts are satisfied.
	if got := ReadyIDs(EvaluateAll(nodes, true)); !reflect.DeepEqual(got, []string{"down_fallback_acc", "down_fallback_done", "up"}) {
		t.Fatalf("todosV2=true ReadyIDs = %v, want all three ready", got)
	}

	// With todosV2=false, both MUST fall back to completion/acceptance unlock (zero drift).
	evDone := Evaluate(nodes, "down_fallback_done", false)
	if evDone.Status != StatusWaiting || evDone.Ready || evDone.EffectiveMode != UnlockOnDone {
		t.Fatalf("todosV2=false fallback done = %+v, want waiting/done", evDone)
	}
	if len(evDone.Unsatisfied) != 1 || evDone.Unsatisfied[0].Reason != ReasonNotDone {
		t.Fatalf("todosV2=false fallback done reason = %+v, want not_done", evDone.Unsatisfied)
	}

	evAcc := Evaluate(nodes, "down_fallback_acc", false)
	if evAcc.Status != StatusWaiting || evAcc.Ready || evAcc.EffectiveMode != UnlockOnAccepted {
		t.Fatalf("todosV2=false fallback accepted = %+v, want waiting/accepted", evAcc)
	}
	if len(evAcc.Unsatisfied) != 1 || evAcc.Unsatisfied[0].Reason != ReasonNotAccepted {
		t.Fatalf("todosV2=false fallback accepted reason = %+v, want not_accepted", evAcc.Unsatisfied)
	}

	// Mark upstream Done=true, Accepted=false -> only down_fallback_done unlocks under todosV2=false.
	nodes[0].Done = true
	if got := ReadyIDs(EvaluateAll(nodes, false)); !reflect.DeepEqual(got, []string{"down_fallback_done", "up"}) {
		t.Fatalf("todosV2=false after Done=true ReadyIDs = %v, want [down_fallback_done up]", got)
	}

	// Mark upstream Accepted=true -> down_fallback_acc also unlocks.
	nodes[0].Accepted = true
	if got := ReadyIDs(EvaluateAll(nodes, false)); !reflect.DeepEqual(got, []string{"down_fallback_acc", "down_fallback_done", "up"}) {
		t.Fatalf("todosV2=false after Accepted=true ReadyIDs = %v, want all three ready", got)
	}
}

func TestMixedPrerequisitesCompletionAndStage(t *testing.T) {
	nodes := []Node{
		{TodoID: "db_migration", Done: false},
		{TodoID: "api_spec", Stage: StageContractReady, Contracts: []string{"openapi_v1"}},
		{
			TodoID: "frontend",
			After: []Edge{
				{PrereqID: "db_migration", Mode: UnlockOnDone},
				{PrereqID: "api_spec", Mode: UnlockOnStage, RequiredStage: StageContractReady, RequiredContracts: []string{"openapi_v1"}},
			},
		},
	}

	// api_spec is satisfied via stage+contract, but db_migration is not done -> still waiting.
	ev := Evaluate(nodes, "frontend", true)
	if ev.Status != StatusWaiting || ev.Ready {
		t.Fatalf("frontend with mixed prereqs = %+v, want waiting", ev)
	}
	if len(ev.Unsatisfied) != 1 || ev.Unsatisfied[0].PrereqID != "db_migration" || ev.Unsatisfied[0].Reason != ReasonNotDone {
		t.Fatalf("frontend unsatisfied = %+v, want only db_migration/not_done", ev.Unsatisfied)
	}

	// Completing db_migration unlocks frontend while api_spec is still only at contract_ready.
	nodes[0].Done = true
	ev = Evaluate(nodes, "frontend", true)
	if ev.Status != StatusReady || !ev.Ready {
		t.Fatalf("frontend after db_migration done = %+v, want ready", ev)
	}
}

func TestCycleDetectionSelfLoopAndMultiHop(t *testing.T) {
	nodes := []Node{
		{TodoID: "self", After: CompletionEdges([]string{"self"}, false)},
		{TodoID: "a", After: CompletionEdges([]string{"b"}, false)},
		{TodoID: "b", After: CompletionEdges([]string{"c"}, false)},
		{TodoID: "c", After: CompletionEdges([]string{"a"}, false)},
		{TodoID: "downstream_of_cycle", After: CompletionEdges([]string{"a"}, false)},
	}

	evSelf := Evaluate(nodes, "self", true)
	if evSelf.Status != StatusCycleDetected || evSelf.Ready {
		t.Fatalf("self-loop eval = %+v, want StatusCycleDetected", evSelf)
	}
	if !reflect.DeepEqual(evSelf.CyclePath, []string{"self", "self"}) {
		t.Fatalf("self-loop CyclePath = %v, want [self self]", evSelf.CyclePath)
	}

	evA := Evaluate(nodes, "a", true)
	if evA.Status != StatusCycleDetected || evA.Ready {
		t.Fatalf("multi-hop cycle eval = %+v, want StatusCycleDetected", evA)
	}
	if !reflect.DeepEqual(evA.CyclePath, []string{"a", "b", "c", "a"}) {
		t.Fatalf("multi-hop CyclePath = %v, want [a b c a]", evA.CyclePath)
	}

	evDown := Evaluate(nodes, "downstream_of_cycle", true)
	if evDown.Status != StatusCycleDetected || evDown.Ready {
		t.Fatalf("downstream of cycle eval = %+v, want StatusCycleDetected", evDown)
	}
	if !reflect.DeepEqual(evDown.CyclePath, []string{"a", "b", "c", "a"}) {
		t.Fatalf("downstream CyclePath = %v, want [a b c a]", evDown.CyclePath)
	}
}

func TestCancelledPrerequisiteAlwaysBlocks(t *testing.T) {
	// Even if Done, Accepted, Stage, and Contracts are all set, Cancelled=true MUST block.
	nodes := []Node{
		{
			TodoID:    "cancelled_up",
			Stage:     StageDone,
			Contracts: []string{"api_contract"},
			Done:      true,
			Accepted:  true,
			Cancelled: true,
		},
		{TodoID: "open_up", Done: false},
		{
			TodoID: "down",
			After: []Edge{
				{PrereqID: "open_up", Mode: UnlockOnDone},
				{PrereqID: "cancelled_up", Mode: UnlockOnStage, RequiredStage: StageContractReady, RequiredContracts: []string{"api_contract"}},
			},
		},
	}

	for _, todosV2 := range []bool{false, true} {
		ev := Evaluate(nodes, "down", todosV2)
		if ev.Status != StatusBlockedByCancelled || ev.Ready {
			t.Fatalf("todosV2=%v cancelled prereq = %+v, want StatusBlockedByCancelled", todosV2, ev)
		}
		if len(ev.Unsatisfied) != 2 ||
			ev.Unsatisfied[0].PrereqID != "cancelled_up" || ev.Unsatisfied[0].Reason != ReasonPrereqCancelled ||
			ev.Unsatisfied[1].PrereqID != "open_up" || ev.Unsatisfied[1].Reason != ReasonNotDone {
			t.Fatalf("todosV2=%v unexpected unsatisfied list: %+v", todosV2, ev.Unsatisfied)
		}
	}
}

func TestMissingPrereqUnknownStagesAndInvalidMode(t *testing.T) {
	nodes := []Node{
		{TodoID: "bad_cur_stage", Stage: "mystery_stage"},
		{TodoID: "good_up", Stage: StageDevelop},
		{
			TodoID: "missing_dep_node",
			After:  []Edge{{PrereqID: "does_not_exist", Mode: UnlockOnStage, RequiredStage: StageContractReady}},
		},
		{
			TodoID: "unknown_req_stage_node",
			After:  []Edge{{PrereqID: "good_up", Mode: UnlockOnStage, RequiredStage: "bogus"}},
		},
		{
			TodoID: "unknown_cur_stage_node",
			After:  []Edge{{PrereqID: "bad_cur_stage", Mode: UnlockOnStage, RequiredStage: StageContractReady}},
		},
		{
			TodoID: "invalid_mode_node",
			After:  []Edge{{PrereqID: "good_up", Mode: "yolo"}},
		},
	}

	evMissing := Evaluate(nodes, "missing_dep_node", true)
	if evMissing.Status != StatusWaiting || evMissing.Ready || len(evMissing.Unsatisfied) != 1 || evMissing.Unsatisfied[0].Reason != ReasonMissingPrerequisite {
		t.Fatalf("missing prereq = %+v, want waiting/missing_prerequisite", evMissing)
	}

	evUnknownReq := Evaluate(nodes, "unknown_req_stage_node", true)
	if evUnknownReq.Status != StatusWaiting || evUnknownReq.Ready || len(evUnknownReq.Unsatisfied) != 1 || evUnknownReq.Unsatisfied[0].Reason != ReasonUnknownRequiredStage {
		t.Fatalf("unknown required stage = %+v, want waiting/unknown_required_stage", evUnknownReq)
	}

	evUnknownCur := Evaluate(nodes, "unknown_cur_stage_node", true)
	if evUnknownCur.Status != StatusWaiting || evUnknownCur.Ready || len(evUnknownCur.Unsatisfied) != 1 || evUnknownCur.Unsatisfied[0].Reason != ReasonUnknownCurrentStage {
		t.Fatalf("unknown current stage = %+v, want waiting/unknown_current_stage", evUnknownCur)
	}

	evInvalidMode := Evaluate(nodes, "invalid_mode_node", true)
	if evInvalidMode.Status != StatusWaiting || evInvalidMode.Ready || len(evInvalidMode.Unsatisfied) != 1 || evInvalidMode.Unsatisfied[0].Reason != ReasonInvalidUnlockMode {
		t.Fatalf("invalid mode = %+v, want waiting/invalid_unlock_mode", evInvalidMode)
	}

	evNotFound := Evaluate(nodes, "no_such_target", true)
	if evNotFound.Status != StatusWaiting || evNotFound.Ready || len(evNotFound.Unsatisfied) != 1 || evNotFound.Unsatisfied[0].Reason != ReasonTargetNotFound {
		t.Fatalf("missing target = %+v, want waiting/target_not_found", evNotFound)
	}
}

func TestDeterministicOrderingAndConcurrentSafety(t *testing.T) {
	nodes := []Node{
		{
			TodoID: "todo_z",
			After: []Edge{
				{PrereqID: "todo_b", Mode: UnlockOnStage, RequiredStage: StagePROpen, RequiredContracts: []string{"z_contract", "a_contract"}},
				{PrereqID: "todo_a", Mode: UnlockOnDone},
			},
		},
		{TodoID: "todo_b", Stage: StageDevelop, Contracts: []string{}},
		{TodoID: "todo_a", Done: false},
		{TodoID: "todo_c", Done: true},
	}

	want := EvaluateAll(nodes, true)
	if len(want) != 4 {
		t.Fatalf("EvaluateAll len = %d, want 4", len(want))
	}
	gotIDs := []string{want[0].TodoID, want[1].TodoID, want[2].TodoID, want[3].TodoID}
	if !reflect.DeepEqual(gotIDs, []string{"todo_a", "todo_b", "todo_c", "todo_z"}) {
		t.Fatalf("EvaluateAll order = %v, want sorted ascending", gotIDs)
	}

	// Reversed node order and reversed edge order must yield identical output.
	rev := slices.Clone(nodes)
	slices.Reverse(rev)
	rev[len(rev)-1].After = slices.Clone(rev[len(rev)-1].After)
	slices.Reverse(rev[len(rev)-1].After)
	if gotRev := EvaluateAll(rev, true); !reflect.DeepEqual(gotRev, want) {
		t.Fatalf("EvaluateAll not deterministic on reversed input:\ngot:  %+v\nwant: %+v", gotRev, want)
	}

	// Concurrent evaluations over shared input slice must be race-free.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(v2 bool) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				evals := EvaluateAll(nodes, v2)
				_ = ReadyIDs(evals)
				_ = Evaluate(nodes, "todo_z", v2)
			}
		}(i%2 == 0)
	}
	wg.Wait()
}
