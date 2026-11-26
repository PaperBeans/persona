package assistant

import (
	"go/parser"
	"go/token"
	"reflect"
	"slices"
	"strconv"
	"testing"
)

// TestTodoBlockersEngineStaysPure guards todo_blockers_engine.go against accidental IO,
// network, clock, database, or non-stdlib dependencies.
func TestTodoBlockersEngineStaysPure(t *testing.T) {
	allowed := []string{"cmp", "encoding/json", "errors", "fmt", "slices", "strings", "sync"}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "todo_blockers_engine.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if !slices.Contains(allowed, p) {
			t.Errorf("todo_blockers_engine.go imports %s: engine must stay pure (allowed stdlib only: %v)", p, allowed)
		}
	}
}

func TestTodoBlockersEngineBuiltinResolvers(t *testing.T) {
	reg := defaultBlockerRegistry()

	t.Run("todo_done", func(t *testing.T) {
		facts := todoBlockerFactSnapshot{
			Todos: map[string]todoBlockerTodoFact{
				"t_open":      {ID: "t_open"},
				"t_done":      {ID: "t_done", Done: true},
				"t_accepted":  {ID: "t_accepted", Done: true, Accepted: true},
				"t_cancelled": {ID: "t_cancelled", Done: true, Cancelled: true},
			},
		}
		r, _ := reg.lookup(todoBlockerKindTodoDone)

		res, err := r.Resolve(todoBlockerRecord{ID: "b1", TodoID: "child", Kind: todoBlockerKindTodoDone, TargetRef: "t_open"}, facts)
		if err != nil || res.Resolved || res.Escalate {
			t.Fatalf("open todo: got (%+v, %v)", res, err)
		}
		res, err = r.Resolve(todoBlockerRecord{ID: "b2", TodoID: "child", Kind: todoBlockerKindTodoDone, TargetRef: "t_done"}, facts)
		if err != nil || !res.Resolved {
			t.Fatalf("done todo: got (%+v, %v)", res, err)
		}
		res, err = r.Resolve(todoBlockerRecord{ID: "b3", TodoID: "child", Kind: todoBlockerKindTodoDone, TargetRef: "t_done", ConditionJSON: `{"require_accepted":true}`}, facts)
		if err != nil || res.Resolved {
			t.Fatalf("done-not-accepted todo with require_accepted: got (%+v, %v)", res, err)
		}
		res, err = r.Resolve(todoBlockerRecord{ID: "b4", TodoID: "child", Kind: todoBlockerKindTodoDone, TargetRef: "t_accepted", ConditionJSON: `{"require_accepted":true}`}, facts)
		if err != nil || !res.Resolved {
			t.Fatalf("accepted todo: got (%+v, %v)", res, err)
		}
		res, err = r.Resolve(todoBlockerRecord{ID: "b5", TodoID: "child", Kind: todoBlockerKindTodoDone, TargetRef: "t_cancelled"}, facts)
		if err != nil || res.Resolved || !res.Escalate {
			t.Fatalf("cancelled todo should fail closed and escalate: got (%+v, %v)", res, err)
		}
	})

	t.Run("todo_stage", func(t *testing.T) {
		facts := todoBlockerFactSnapshot{
			Todos: map[string]todoBlockerTodoFact{
				"t_api":       {ID: "t_api", Stage: "contract_ready", Contracts: []string{"api_v1"}},
				"t_cancelled": {ID: "t_cancelled", Stage: "done", Contracts: []string{"api_v1"}, Cancelled: true},
			},
		}
		r, _ := reg.lookup(todoBlockerKindTodoStage)

		res, err := r.Resolve(todoBlockerRecord{
			ID: "s1", TodoID: "web", Kind: todoBlockerKindTodoStage, TargetRef: "t_api",
			ConditionJSON: `{"required_stage":"contract_ready","required_contracts":["api_v1"]}`,
		}, facts)
		if err != nil || !res.Resolved {
			t.Fatalf("matching stage+contracts: got (%+v, %v)", res, err)
		}
		res, err = r.Resolve(todoBlockerRecord{
			ID: "s2", TodoID: "web", Kind: todoBlockerKindTodoStage, TargetRef: "t_api",
			ConditionJSON: `{"required_stage":"pr_open"}`,
		}, facts)
		if err != nil || res.Resolved {
			t.Fatalf("stage not reached: got (%+v, %v)", res, err)
		}
		res, err = r.Resolve(todoBlockerRecord{
			ID: "s3", TodoID: "web", Kind: todoBlockerKindTodoStage, TargetRef: "t_api",
			ConditionJSON: `{"required_contracts":["api_v1","schema_frozen"]}`,
		}, facts)
		if err != nil || res.Resolved {
			t.Fatalf("missing contract: got (%+v, %v)", res, err)
		}
		res, err = r.Resolve(todoBlockerRecord{
			ID: "s4", TodoID: "web", Kind: todoBlockerKindTodoStage, TargetRef: "t_cancelled",
			ConditionJSON: `{"required_stage":"contract_ready"}`,
		}, facts)
		if err != nil || res.Resolved || !res.Escalate {
			t.Fatalf("cancelled stage prereq: got (%+v, %v)", res, err)
		}
	})

	t.Run("external_pr", func(t *testing.T) {
		prMerged := "https://github.com/example/repo/pull/101"
		prClosed := "https://github.com/example/repo/pull/102"
		prOpen := "https://github.com/example/repo/pull/103"
		facts := todoBlockerFactSnapshot{
			PRs: map[string]todoBlockerPRFact{
				prMerged: {URL: prMerged, State: "merged", Merged: true},
				prClosed: {URL: prClosed, State: "closed"},
				prOpen:   {URL: prOpen, State: "open"},
			},
		}
		r, _ := reg.lookup(todoBlockerKindExternalPR)

		res, err := r.Resolve(todoBlockerRecord{ID: "p1", TodoID: "t1", Kind: todoBlockerKindExternalPR, TargetRef: prMerged + "/"}, facts)
		if err != nil || !res.Resolved {
			t.Fatalf("merged PR: got (%+v, %v)", res, err)
		}
		res, err = r.Resolve(todoBlockerRecord{ID: "p2", TodoID: "t2", Kind: todoBlockerKindExternalPR, TargetRef: prClosed}, facts)
		if err != nil || !res.Resolved {
			t.Fatalf("closed PR default: got (%+v, %v)", res, err)
		}
		res, err = r.Resolve(todoBlockerRecord{ID: "p3", TodoID: "t3", Kind: todoBlockerKindExternalPR, TargetRef: prClosed, ConditionJSON: `{"require_merged":true}`}, facts)
		if err != nil || res.Resolved || !res.Escalate {
			t.Fatalf("closed PR with require_merged: got (%+v, %v)", res, err)
		}
		res, err = r.Resolve(todoBlockerRecord{ID: "p4", TodoID: "t4", Kind: todoBlockerKindExternalPR, TargetRef: prOpen}, facts)
		if err != nil || res.Resolved || res.Escalate {
			t.Fatalf("open PR: got (%+v, %v)", res, err)
		}
	})

	t.Run("write_conflict", func(t *testing.T) {
		facts := todoBlockerFactSnapshot{
			Todos: map[string]todoBlockerTodoFact{
				"t_running": {ID: "t_running", HasActiveAgent: true},
				"t_done":    {ID: "t_done", Done: true},
			},
		}
		r, _ := reg.lookup(todoBlockerKindWriteConflict)
		res, err := r.Resolve(todoBlockerRecord{ID: "w1", TodoID: "t2", Kind: todoBlockerKindWriteConflict, TargetRef: "t_running"}, facts)
		if err != nil || res.Resolved {
			t.Fatalf("running conflict: got (%+v, %v)", res, err)
		}
		res, err = r.Resolve(todoBlockerRecord{ID: "w2", TodoID: "t2", Kind: todoBlockerKindWriteConflict, TargetRef: "t_done"}, facts)
		if err != nil || !res.Resolved {
			t.Fatalf("done conflict: got (%+v, %v)", res, err)
		}
	})

	t.Run("merge_train", func(t *testing.T) {
		facts := todoBlockerFactSnapshot{
			MergeTrains: map[string]todoBlockerMergeTrainFact{
				"github.com/example/repo": {
					RepoKey:       "github.com/example/repo",
					HeadTodoID:    "t_head",
					HeadState:     "active",
					ActiveTodoIDs: []string{"t_head", "t_next"},
				},
				"github.com/example/done-repo": {
					RepoKey:    "github.com/example/done-repo",
					HeadTodoID: "t_old",
					HeadState:  "merged",
				},
			},
		}
		r, _ := reg.lookup(todoBlockerKindMergeTrain)

		res, err := r.Resolve(todoBlockerRecord{
			ID: "m1", TodoID: "t_next", Kind: todoBlockerKindMergeTrain, TargetRef: "t_head",
			ConditionJSON: `{"repo_key":"github.com/example/repo"}`, Attempts: 1,
		}, facts)
		if err != nil || res.Resolved || res.Escalate {
			t.Fatalf("waiting merge train: got (%+v, %v)", res, err)
		}
		res, err = r.Resolve(todoBlockerRecord{
			ID: "m2", TodoID: "t_next", Kind: todoBlockerKindMergeTrain, TargetRef: "t_head",
			ConditionJSON: `{"repo_key":"github.com/example/repo"}`, Attempts: 2,
		}, facts)
		if err != nil || res.Resolved || !res.Escalate {
			t.Fatalf("escalated merge train: got (%+v, %v)", res, err)
		}
		res, err = r.Resolve(todoBlockerRecord{
			ID: "m3", TodoID: "t_next", Kind: todoBlockerKindMergeTrain, TargetRef: "t_old",
			ConditionJSON: `{"repo_key":"github.com/example/done-repo"}`, Attempts: 0,
		}, facts)
		if err != nil || !res.Resolved {
			t.Fatalf("cleared merge train: got (%+v, %v)", res, err)
		}
	})

	t.Run("owner_decision", func(t *testing.T) {
		facts := todoBlockerFactSnapshot{
			Todos: map[string]todoBlockerTodoFact{
				"t_wait":    {ID: "t_wait", HasOpenDecision: true},
				"t_cleared": {ID: "t_cleared", HasOpenDecision: false},
			},
		}
		r, _ := reg.lookup(todoBlockerKindOwnerDecision)
		res, err := r.Resolve(todoBlockerRecord{ID: "d1", TodoID: "t_wait", Kind: todoBlockerKindOwnerDecision, TargetRef: "t_wait"}, facts)
		if err != nil || res.Resolved {
			t.Fatalf("pending decision: got (%+v, %v)", res, err)
		}
		res, err = r.Resolve(todoBlockerRecord{ID: "d2", TodoID: "t_cleared", Kind: todoBlockerKindOwnerDecision, TargetRef: "t_cleared"}, facts)
		if err != nil || !res.Resolved {
			t.Fatalf("cleared decision: got (%+v, %v)", res, err)
		}
	})
}

func TestTodoBlockersEngineCustomResolverAndReconcile(t *testing.T) {
	reg := defaultBlockerRegistry()
	const kindCustomCI = "custom_ci_gate"
	if err := reg.register(kindCustomCI, blockerResolverFunc(func(b todoBlockerRecord, facts todoBlockerFactSnapshot) (todoBlockerResolution, error) {
		if facts.Todos[b.TargetRef].Done {
			return todoBlockerResolution{Resolved: true, Reason: "custom_green"}, nil
		}
		return todoBlockerResolution{Resolved: false, Reason: "custom_pending"}, nil
	})); err != nil {
		t.Fatalf("register custom resolver: %v", err)
	}

	facts := todoBlockerFactSnapshot{
		Todos: map[string]todoBlockerTodoFact{
			"dep_done": {ID: "dep_done", Done: true},
			"dep_open": {ID: "dep_open", Done: false},
		},
		PRs: map[string]todoBlockerPRFact{
			"https://github.com/example/repo/pull/9": {URL: "https://github.com/example/repo/pull/9", State: "merged", Merged: true},
		},
	}

	active := []todoBlockerRecord{
		{ID: "b_c1", TodoID: "todo_c", Kind: todoBlockerKindMergeTrain, TargetRef: "dep_open", State: todoBlockerStateActive, Attempts: 2},
		{ID: "b_b2", TodoID: "todo_b", Kind: todoBlockerKindTodoDone, TargetRef: "dep_open", State: todoBlockerStateActive},
		{ID: "b_a2", TodoID: "todo_a", Kind: kindCustomCI, TargetRef: "dep_done", State: todoBlockerStateActive},
		{ID: "b_b1", TodoID: "todo_b", Kind: todoBlockerKindExternalPR, TargetRef: "https://github.com/example/repo/pull/9", State: todoBlockerStateActive},
		{ID: "b_a1", TodoID: "todo_a", Kind: todoBlockerKindExternalPR, TargetRef: "https://github.com/example/repo/pull/9", State: todoBlockerStateActive},
		{ID: "b_ignored", TodoID: "todo_done_already", Kind: todoBlockerKindTodoDone, TargetRef: "dep_done", State: todoBlockerStateResolved},
	}

	got := reconcileTodoBlockers(active, facts, reg)
	wantResolved := []string{"b_a1", "b_a2", "b_b1"}
	wantEscalated := []string{"b_c1"}
	wantUnblocked := []string{"todo_a"}

	if !reflect.DeepEqual(got.ResolvedIDs, wantResolved) {
		t.Fatalf("ResolvedIDs = %v, want %v", got.ResolvedIDs, wantResolved)
	}
	if !reflect.DeepEqual(got.EscalatedIDs, wantEscalated) {
		t.Fatalf("EscalatedIDs = %v, want %v", got.EscalatedIDs, wantEscalated)
	}
	if !reflect.DeepEqual(got.UnblockedTodoIDs, wantUnblocked) {
		t.Fatalf("UnblockedTodoIDs = %v, want %v", got.UnblockedTodoIDs, wantUnblocked)
	}
}

func TestTodoBlockersEngineSharedActiveAggregation(t *testing.T) {
	active := []todoBlockerRecord{
		{ID: "b1", TodoID: "t3", Kind: todoBlockerKindExternalPR, TargetRef: "pr/1", State: todoBlockerStateActive},
		{ID: "b2", TodoID: "t1", Kind: todoBlockerKindExternalPR, TargetRef: "pr/1", State: todoBlockerStateActive},
		{ID: "b3", TodoID: "t2", Kind: todoBlockerKindExternalPR, TargetRef: "pr/1", State: todoBlockerStateActive},
		{ID: "b4", TodoID: "t2", Kind: todoBlockerKindMergeTrain, TargetRef: "pr/1", State: todoBlockerStateActive},
		{ID: "b5", TodoID: "t9", Kind: todoBlockerKindTodoDone, TargetRef: "solo", State: todoBlockerStateActive},
		{ID: "b6", TodoID: "t8", Kind: todoBlockerKindTodoDone, TargetRef: "solo", State: todoBlockerStateResolved},
		{ID: "b7", TodoID: "t2", Kind: todoBlockerKindTodoDone, TargetRef: "dep_b", State: todoBlockerStateActive},
		{ID: "b8", TodoID: "t1", Kind: todoBlockerKindTodoDone, TargetRef: "dep_b", State: todoBlockerStateActive},
		{ID: "b9", TodoID: "t2", Kind: todoBlockerKindTodoDone, TargetRef: "dep_a", State: todoBlockerStateActive},
		{ID: "b10", TodoID: "t1", Kind: todoBlockerKindTodoDone, TargetRef: "dep_a", State: todoBlockerStateActive},
	}

	got := sharedActiveTodoBlockers(active)
	if len(got) != 3 {
		t.Fatalf("expected 3 shared groups, got %+v", got)
	}
	if got[0].TargetRef != "pr/1" || got[0].Count != 3 || !reflect.DeepEqual(got[0].TodoIDs, []string{"t1", "t2", "t3"}) {
		t.Fatalf("group[0] mismatch: %+v", got[0])
	}
	if got[1].TargetRef != "dep_a" || got[1].Count != 2 {
		t.Fatalf("group[1] mismatch: %+v", got[1])
	}
	if got[2].TargetRef != "dep_b" || got[2].Count != 2 {
		t.Fatalf("group[2] mismatch: %+v", got[2])
	}
}
