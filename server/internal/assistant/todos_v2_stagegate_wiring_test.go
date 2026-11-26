package assistant

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/todo/stagegate"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

func setTodosV2Enabled(t *testing.T, s *Service, on bool) {
	t.Helper()
	setSettings(t, s, func(v *Settings) {
		v.TodosV2 = &on
	})
}

func TestTodosV2StageGateBypassWhenFlagOff(t *testing.T) {
	st := &memStore{}
	s, _ := newTodoReadyService(t, st)
	ctx := context.Background()

	// Default is todos_v2=false.
	if s.settings(ctx).todosV2On() {
		t.Fatal("expected todosV2On() == false by default")
	}

	root := createRequestAndPiece(t, s, "conv1", "Rollout", "", "", nil)
	pre := createRequestAndPiece(t, s, "conv1", "API contract", "[stage:contract_ready]\n[contract:api_contract]", root.ID, nil)
	dep := createRequestAndPiece(t, s, "conv1", "Web client", "unlock_stage: contract_ready\nunlock_contracts: api_contract", root.ID, []string{pre.ID})

	// Even with open PR + contract_ready markers, todos_v2=false must 100% bypass stagegate and wait for completion.
	s.todos.mu.Lock()
	s.todos.todos[pre.ID].Stage = "review"
	s.todos.todos[pre.ID].PRURL = "https://github.com/example/repo/pull/10"
	s.todos.todos[pre.ID].PRStatus = &TodoPRStatus{
		URL:   "https://github.com/example/repo/pull/10",
		State: "open",
	}
	s.todos.mu.Unlock()

	if _, ready, err := s.checkTodoAfterReady(ctx, dep); err != nil || ready {
		t.Fatalf("todos_v2=false checkTodoAfterReady = (%v, %v), want (false, nil)", ready, err)
	}

	// Completing the prerequisite unlocks under legacy rules.
	now := time.Now().UTC()
	s.todos.mu.Lock()
	s.todos.todos[pre.ID].DoneAt = &now
	s.todos.mu.Unlock()

	prereqs, ready, err := s.checkTodoAfterReady(ctx, dep)
	if err != nil || !ready || len(prereqs) != 1 || prereqs[0].Reason != "done" {
		t.Fatalf("todos_v2=false after done = (%+v, %v, %v), want ready/done", prereqs, ready, err)
	}
}

func TestTodosV2StageGateEarlyUnlockOnPROpenAndClosedPRRejected(t *testing.T) {
	st := &memStore{}
	s, f := newTodoReadyService(t, st)
	ctx := context.Background()
	setTodosV2Enabled(t, s, true)

	f.setSessions("server",
		proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
	)

	root := createRequestAndPiece(t, s, "conv1", "Feature", "", "", nil)
	pre := createRequestAndPiece(t, s, "conv1", "Backend PR", "", root.ID, nil)
	dep := createRequestAndPiece(t, s, "conv1", "E2E verification", "unlock_stage: pr_open", root.ID, []string{pre.ID})

	// While upstream is only in develop (linked agent, no PR), dep waits.
	s.todos.mu.Lock()
	s.todos.todos[pre.ID].Stage = "develop"
	s.todos.todos[pre.ID].Agents = []TodoAgent{{MachineID: "server", SessionID: "s1"}}
	s.todos.mu.Unlock()

	if _, ready, err := s.checkTodoAfterReady(ctx, dep); err != nil || ready {
		t.Fatalf("dep while upstream in develop = (%v, %v), want (false, nil)", ready, err)
	}

	// An unmerged closed PR must NOT satisfy pr_open.
	s.todos.mu.Lock()
	s.todos.todos[pre.ID].PRURL = "https://github.com/example/repo/pull/11"
	s.todos.todos[pre.ID].PRStatus = &TodoPRStatus{
		URL:   "https://github.com/example/repo/pull/11",
		State: "closed",
	}
	s.todos.mu.Unlock()

	if _, ready, err := s.checkTodoAfterReady(ctx, dep); err != nil || ready {
		t.Fatalf("dep with closed unmerged PR = (%v, %v), want (false, nil)", ready, err)
	}

	// Opening the PR reaches StagePROpen and unlocks dep early under todos_v2=true.
	s.todos.mu.Lock()
	s.todos.todos[pre.ID].PRStatus.State = "open"
	s.todos.mu.Unlock()

	prereqs, ready, err := s.checkTodoAfterReady(ctx, dep)
	if err != nil || !ready || len(prereqs) != 1 {
		t.Fatalf("dep with open PR = (%+v, %v, %v), want ready", prereqs, ready, err)
	}
	if prereqs[0].Reason != "stage:pr_open" {
		t.Fatalf("prereq reason = %q, want stage:pr_open", prereqs[0].Reason)
	}

	// evaluateTodoAfterDependents emits todo_ready wake to Main chat.
	s.evaluateTodoAfterDependents(ctx, pre.ID)
	waitFor(t, "todo_ready wake on pr_open", func() bool { return len(sendsToConversation(f, "conv1")) == 1 })
}

func TestTodosV2StageGateEarlyUnlockOnContractReadyAndTags(t *testing.T) {
	st := &memStore{}
	s, _ := newTodoReadyService(t, st)
	ctx := context.Background()
	setTodosV2Enabled(t, s, true)

	root := createRequestAndPiece(t, s, "conv1", "Platform", "", "", nil)
	pre := createRequestAndPiece(t, s, "conv1", "Schema spec", "Drafting schema", root.ID, nil)
	depStageOnly := createRequestAndPiece(t, s, "conv1", "Client A", "[unlock_stage: contract_ready]", root.ID, []string{pre.ID})
	depContracts := createRequestAndPiece(t, s, "conv1", "Client B", "unlock_stage: contract_ready\nunlock_contracts: api_contract, schema_frozen", root.ID, []string{pre.ID})

	// Before contract_ready marker: both wait.
	if _, ready, err := s.checkTodoAfterReady(ctx, depStageOnly); err != nil || ready {
		t.Fatalf("depStageOnly before marker = (%v, %v), want (false, nil)", ready, err)
	}
	if _, ready, err := s.checkTodoAfterReady(ctx, depContracts); err != nil || ready {
		t.Fatalf("depContracts before marker = (%v, %v), want (false, nil)", ready, err)
	}

	// Marking [contract:ready] + api_contract satisfies depStageOnly, while depContracts still waits for schema_frozen.
	s.todos.mu.Lock()
	s.todos.todos[pre.ID].Notes = "Spec frozen\n[contract:ready]\ncontracts: api_contract"
	s.todos.mu.Unlock()

	prereqs, ready, err := s.checkTodoAfterReady(ctx, depStageOnly)
	if err != nil || !ready || len(prereqs) != 1 || prereqs[0].Reason != "stage:contract_ready" {
		t.Fatalf("depStageOnly after [contract:ready] = (%+v, %v, %v), want ready/stage:contract_ready", prereqs, ready, err)
	}
	if _, ready, err := s.checkTodoAfterReady(ctx, depContracts); err != nil || ready {
		t.Fatalf("depContracts missing schema_frozen = (%v, %v), want (false, nil)", ready, err)
	}

	// Adding schema_frozen unlocks depContracts too.
	s.todos.mu.Lock()
	s.todos.todos[pre.ID].Notes = "Spec frozen\n[contract:ready]\ncontracts: api_contract, schema_frozen"
	s.todos.mu.Unlock()

	if _, ready, err := s.checkTodoAfterReady(ctx, depContracts); err != nil || !ready {
		t.Fatalf("depContracts with all contracts = (%v, %v), want (true, nil)", ready, err)
	}

	// Brief.Goal unlock declarations are also supported.
	depBrief := createRequestAndPiece(t, s, "conv1", "Client C", "", root.ID, []string{pre.ID})
	depBrief.Brief = &TaskBrief{briefRecord: briefRecord{Goal: "Start once upstream reaches [unlock_stage: contract_ready]"}}
	if _, ready, err := s.checkTodoAfterReady(ctx, depBrief); err != nil || !ready {
		t.Fatalf("depBrief with Brief.Goal unlock_stage = (%v, %v), want (true, nil)", ready, err)
	}
}

func TestTodosV2StageGatePlainAfterStillRequiresCompletionAndAcceptance(t *testing.T) {
	st := &memStore{}
	s, _ := newTodoReadyService(t, st)
	ctx := context.Background()
	setTodosV2Enabled(t, s, true)

	// 1. Plain after without RequireAcceptedAfter: does NOT unlock on open PR or contract_ready.
	root := createRequestAndPiece(t, s, "conv1", "Plain batch", "", "", nil)
	pre := createRequestAndPiece(t, s, "conv1", "Step 1", "[contract:ready]", root.ID, nil)
	dep := createRequestAndPiece(t, s, "conv1", "Step 2", "No unlock_stage rule here", root.ID, []string{pre.ID})

	s.todos.mu.Lock()
	s.todos.todos[pre.ID].PRURL = "https://github.com/example/repo/pull/20"
	s.todos.todos[pre.ID].PRStatus = &TodoPRStatus{
		URL:   "https://github.com/example/repo/pull/20",
		State: "open",
	}
	s.todos.mu.Unlock()

	if _, ready, err := s.checkTodoAfterReady(ctx, dep); err != nil || ready {
		t.Fatalf("plain after with open PR = (%v, %v), want (false, nil)", ready, err)
	}

	// Merging the delivery PR unlocks plain after (preserving legacy pr_merged behavior).
	s.todos.mu.Lock()
	s.todos.todos[pre.ID].PRStatus.State = "merged"
	s.todos.mu.Unlock()

	prereqs, ready, err := s.checkTodoAfterReady(ctx, dep)
	if err != nil || !ready || len(prereqs) != 1 || prereqs[0].Reason != "pr_merged" {
		t.Fatalf("plain after with merged PR = (%+v, %v, %v), want ready/pr_merged", prereqs, ready, err)
	}

	// 2. Plain after under RequireAcceptedAfter root: must wait for reviewAccepted.
	gatedRoot := createRequestAndPiece(t, s, "conv1", "Gated batch", "", "", nil)
	s.todos.mu.Lock()
	s.todos.todos[gatedRoot.ID].RequireAcceptedAfter = true
	s.todos.mu.Unlock()

	gPre := createRequestAndPiece(t, s, "conv1", "Gated Step 1", "", gatedRoot.ID, nil)
	gDep := createRequestAndPiece(t, s, "conv1", "Gated Step 2", "", gatedRoot.ID, []string{gPre.ID})

	now := time.Now().UTC()
	s.todos.mu.Lock()
	s.todos.todos[gPre.ID].DoneAt = &now
	s.todos.todos[gPre.ID].DoneBy = db.DoneByAuto
	s.todos.mu.Unlock()

	if _, ready, err := s.checkTodoAfterReady(ctx, gDep); err != nil || ready {
		t.Fatalf("gated plain after with DoneByAuto = (%v, %v), want (false, nil)", ready, err)
	}

	s.todos.mu.Lock()
	s.todos.todos[gPre.ID].DoneBy = db.DoneByReview
	s.todos.todos[gPre.ID].Supervision = &Supervision{
		State:   reviewAccepted,
		Reviews: []TaskReview{{Verdict: reviewAccepted, At: now}},
	}
	s.todos.mu.Unlock()

	if _, ready, err := s.checkTodoAfterReady(ctx, gDep); err != nil || !ready {
		t.Fatalf("gated plain after with reviewAccepted = (%v, %v), want (true, nil)", ready, err)
	}
}

func TestTodosV2StageGateFailsClosedOnCancelledFailedStartCyclesAndStoreError(t *testing.T) {
	repoStore := &todoReadyRepoStore{memStore: &memStore{}}
	s, _ := newTodoReadyService(t, repoStore)
	ctx := context.Background()
	setTodosV2Enabled(t, s, true)

	root := createRequestAndPiece(t, s, "conv1", "Safety", "", "", nil)

	// 1. Cancelled upstream must NEVER unlock even with stage/contracts/PR/Done set.
	cancelledPre := createRequestAndPiece(t, s, "conv1", "Cancelled upstream", "[contract:ready]\n[stage:done]", root.ID, nil)
	depCancelled := createRequestAndPiece(t, s, "conv1", "Downstream of cancelled", "unlock_stage: contract_ready", root.ID, []string{cancelledPre.ID})
	now := time.Now().UTC()
	s.todos.mu.Lock()
	s.todos.todos[cancelledPre.ID].DoneAt = &now
	s.todos.todos[cancelledPre.ID].DoneBy = db.DoneByCancelled
	s.todos.todos[cancelledPre.ID].CancelledAt = &now
	s.todos.todos[cancelledPre.ID].PRURL = "https://github.com/example/repo/pull/30"
	s.todos.todos[cancelledPre.ID].PRStatus = &TodoPRStatus{URL: "https://github.com/example/repo/pull/30", State: "merged"}
	s.todos.mu.Unlock()

	if _, ready, err := s.checkTodoAfterReady(ctx, depCancelled); err != nil || ready {
		t.Fatalf("cancelled upstream checkTodoAfterReady = (%v, %v), want (false, nil)", ready, err)
	}

	// 2. Failed-start upstream must NEVER unlock.
	failedPre := createRequestAndPiece(t, s, "conv1", "Failed start", "[contract:ready]", root.ID, nil)
	depFailed := createRequestAndPiece(t, s, "conv1", "Downstream of failed", "unlock_stage: contract_ready", root.ID, []string{failedPre.ID})
	s.todos.mu.Lock()
	s.todos.todos[failedPre.ID].DoneAt = &now
	s.todos.todos[failedPre.ID].StartError = "agent failed to launch"
	s.todos.mu.Unlock()

	if _, ready, err := s.checkTodoAfterReady(ctx, depFailed); err != nil || ready {
		t.Fatalf("failed-start upstream checkTodoAfterReady = (%v, %v), want (false, nil)", ready, err)
	}

	// 3. Multi-hop dependency cycle (a -> b -> c -> a) must fail closed even if nodes claim contract_ready.
	cycA := createRequestAndPiece(t, s, "conv1", "Cycle A", "[contract:ready]\nunlock_stage: contract_ready", root.ID, nil)
	cycB := createRequestAndPiece(t, s, "conv1", "Cycle B", "[contract:ready]\nunlock_stage: contract_ready", root.ID, []string{cycA.ID})
	cycC := createRequestAndPiece(t, s, "conv1", "Cycle C", "[contract:ready]\nunlock_stage: contract_ready", root.ID, []string{cycB.ID})
	s.todos.mu.Lock()
	s.todos.todos[cycA.ID].After = []string{cycC.ID}
	cycA = copyTodoLocked(s.todos.todos[cycA.ID])
	s.todos.mu.Unlock()

	if _, ready, err := s.checkTodoAfterReady(ctx, cycA); err != nil || ready {
		t.Fatalf("cyclic dependency checkTodoAfterReady = (%v, %v), want (false, nil)", ready, err)
	}

	// 4. Self-loop must fail closed.
	selfLoop := createRequestAndPiece(t, s, "conv1", "Self loop", "[contract:ready]\nunlock_stage: contract_ready", root.ID, nil)
	s.todos.mu.Lock()
	s.todos.todos[selfLoop.ID].After = []string{selfLoop.ID}
	selfLoop = copyTodoLocked(s.todos.todos[selfLoop.ID])
	s.todos.mu.Unlock()

	if _, ready, err := s.checkTodoAfterReady(ctx, selfLoop); err != nil || ready {
		t.Fatalf("self-loop checkTodoAfterReady = (%v, %v), want (false, nil)", ready, err)
	}

	// 5. Transient repository-store read error must propagate and fail closed.
	openPre := createRequestAndPiece(t, s, "conv1", "Open upstream", "[contract:ready]", root.ID, nil)
	depStoreErr := createRequestAndPiece(t, s, "conv1", "Downstream store err", "unlock_stage: contract_ready", root.ID, []string{openPre.ID})
	repoStore.setErr(errors.New("store_unavailable"))
	if _, ready, err := s.checkTodoAfterReady(ctx, depStoreErr); err == nil || ready {
		t.Fatalf("transient store error checkTodoAfterReady = (%v, %v), want (false, non-nil err)", ready, err)
	}
}

func TestTodosV2StageGateLosslessRollbackToggle(t *testing.T) {
	st := &memStore{}
	s, _ := newTodoReadyService(t, st)
	ctx := context.Background()

	root := createRequestAndPiece(t, s, "conv1", "Rollback test", "", "", nil)
	pre := createRequestAndPiece(t, s, "conv1", "Upstream", "[stage:contract_ready]", root.ID, nil)
	dep := createRequestAndPiece(t, s, "conv1", "Downstream", "unlock_stage: contract_ready", root.ID, []string{pre.ID})

	// Flag off -> not ready.
	setTodosV2Enabled(t, s, false)
	if _, ready, err := s.checkTodoAfterReady(ctx, dep); err != nil || ready {
		t.Fatalf("flag=false initial = (%v, %v), want (false, nil)", ready, err)
	}

	// Flag on -> early unlock ready.
	setTodosV2Enabled(t, s, true)
	if _, ready, err := s.checkTodoAfterReady(ctx, dep); err != nil || !ready {
		t.Fatalf("flag=true = (%v, %v), want (true, nil)", ready, err)
	}

	// Rollback flag off -> immediately reverts to not ready without side effects.
	setTodosV2Enabled(t, s, false)
	if _, ready, err := s.checkTodoAfterReady(ctx, dep); err != nil || ready {
		t.Fatalf("flag=false after rollback = (%v, %v), want (false, nil)", ready, err)
	}
}

func TestTodoStageGateNodeMapping(t *testing.T) {
	node := todoStageGateNode(Todo{
		ID:    "t1",
		Notes: "notes\n[contract:api_v2]\nunlock_stage: pr_open\nunlock_contracts: schema_v1",
		After: []string{"p2", "p1"},
		PRStatus: &TodoPRStatus{
			URL:   "https://github.com/example/repo/pull/99",
			State: "open",
		},
	}, true, nil)

	if node.Stage != stagegate.StagePROpen {
		t.Fatalf("Stage = %q, want %q", node.Stage, stagegate.StagePROpen)
	}
	if len(node.Contracts) != 1 || node.Contracts[0] != "api_v2" {
		t.Fatalf("Contracts = %v, want [api_v2]", node.Contracts)
	}
	if len(node.After) != 2 || node.After[0].PrereqID != "p1" || node.After[1].PrereqID != "p2" {
		t.Fatalf("After = %+v, want sorted edges [p1 p2]", node.After)
	}
	if node.After[0].Mode != stagegate.UnlockOnStage || node.After[0].RequiredStage != stagegate.StagePROpen || !node.After[0].RequireAccepted {
		t.Fatalf("After[0] = %+v, want UnlockOnStage/pr_open/RequireAccepted=true", node.After[0])
	}
}
