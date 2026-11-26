package assistant

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/todo/conflict"
	"github.com/nwverse/persona/server/internal/assistant/todo/mergetrain"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

func runMergeTrainRestartPersistenceScenario(t *testing.T, newBoot1, newRestarted func() *Service, st todoBlockerStore, requireTodoReload bool) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()

	// Boot 1: enable todos_v2 and record strike 1 on head-1.
	s1 := newBoot1()
	setMergeTrainTodosV2Flag(t, s1, true, false)

	prURL := "https://github.com/example/hardening-repo/pull/101"
	task := newMergeTrainTask(t, s1, "Persistent merge train PR", prURL)
	blockerID := mergeTrainBlockerID(task.ID, triageNormalizePRURL(prURL))

	if err := s1.applyPRStatus(ctx, task, TodoPRStatus{
		URL:       prURL,
		State:     "open",
		HeadOID:   "head-1",
		Review:    "approved",
		Checks:    "failure",
		Conflicts: true,
		CheckedAt: now,
	}); err != nil {
		t.Fatalf("strike 1 applyPRStatus: %v", err)
	}

	rows, err := st.ListTodoBlockersByTodo(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListTodoBlockersByTodo after strike 1: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 merge_train blocker row after strike 1, got %d: %+v", len(rows), rows)
	}
	if rows[0].ID != blockerID || rows[0].Kind != todoBlockerKindMergeTrain || rows[0].State != db.TodoBlockerStateActive || rows[0].Attempts != 1 {
		t.Fatalf("unexpected blocker row after strike 1: %+v", rows[0])
	}
	var cond mergeTrainBlockerCondition
	if err := json.Unmarshal([]byte(rows[0].ConditionJSON), &cond); err != nil {
		t.Fatalf("unmarshal condition_json: %v", err)
	}
	if cond.LastHeadOID != "head-1" || cond.RepoKey != "github.com/example/hardening-repo" {
		t.Fatalf("unexpected condition after strike 1: %+v", cond)
	}

	// Pull-based blocker reconciliation must not falsely resolve an active merge_train conflict blocker.
	recSum, err := s1.ReconcileTodoBlockers(ctx, "")
	if err != nil {
		t.Fatalf("ReconcileTodoBlockers after strike 1: %v", err)
	}
	if slices.Contains(recSum.ResolvedIDs, blockerID) {
		t.Fatalf("ReconcileTodoBlockers falsely resolved active merge_train blocker %s", blockerID)
	}

	// Simulate Hub restart: shut down Service 1 and construct a brand-new Service attached to the same persistent store.
	s1.stopSuperviseTimers()
	s2 := newRestarted()
	if !s2.settings(ctx).todosV2On() {
		t.Fatal("expected todos_v2=true to persist across Service restart")
	}
	if err := s2.ensureTodosLoaded(ctx); err != nil {
		t.Fatalf("s2.ensureTodosLoaded: %v", err)
	}
	reloadedTask := todoNow(s2, task.ID)
	if reloadedTask.ID == "" {
		if requireTodoReload {
			t.Fatalf("restarted Service did not reload task %s", task.ID)
		}
		reloadedTask = task
	}

	// Re-polling the exact same head-1 after restart must hydrate Attempts=1 without incrementing.
	sameHead := s2.onPRStatusMergeTrain(ctx, reloadedTask, TodoPRStatus{
		URL:       prURL,
		State:     "open",
		HeadOID:   "head-1",
		Review:    "approved",
		Checks:    "failure",
		Conflicts: true,
		CheckedAt: now.Add(time.Second),
	})
	if sameHead.Escalate || sameHead.Decision.State == mergetrain.StateEscalated {
		t.Fatalf("re-polling same head-1 after restart unexpectedly escalated: %+v", sameHead)
	}
	repoSnap, ok := findRepoSnapshot(s2.mergeTrainSnapshot(ctx), "github.com/example/hardening-repo")
	if !ok || repoSnap.Head == nil || repoSnap.Head.RebaseFailures != 1 {
		t.Fatalf("expected hydrated RebaseFailures=1 after restart, got %+v", repoSnap.Head)
	}

	// Strike 2 on a new head-2 after restart must increment durable Attempts to 2 and trigger StateEscalated.
	strike2 := s2.onPRStatusMergeTrain(ctx, reloadedTask, TodoPRStatus{
		URL:       prURL,
		State:     "open",
		HeadOID:   "head-2",
		Review:    "approved",
		Checks:    "failure",
		Conflicts: true,
		CheckedAt: now.Add(2 * time.Second),
	})
	if !strike2.Escalate || strike2.Decision.Action != mergetrain.ActionEscalateNeedsUser || strike2.Decision.State != mergetrain.StateEscalated {
		t.Fatalf("strike 2 after restart did not escalate: %+v", strike2)
	}
	sig, hasSig := pendingPRConflict(s2, reloadedTask.ID)
	if !hasSig || !strings.Contains(sig.Detail, "circuit breaker") || !strings.Contains(sig.Detail, "needs_user") {
		t.Fatalf("expected escalated sigPRConflict after restart strike 2, got (%v, %+v)", hasSig, sig)
	}

	rows, err = st.ListTodoBlockersByTodo(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListTodoBlockersByTodo after strike 2: %v", err)
	}
	if len(rows) != 1 || rows[0].State != db.TodoBlockerStateActive || rows[0].Attempts != 2 {
		t.Fatalf("expected active blocker with Attempts=2 after strike 2, got %+v", rows)
	}
	cond = mergeTrainBlockerCondition{}
	if err := json.Unmarshal([]byte(rows[0].ConditionJSON), &cond); err != nil {
		t.Fatalf("unmarshal condition_json after strike 2: %v", err)
	}
	if cond.LastHeadOID != "head-2" {
		t.Fatalf("expected last_head_oid=head-2 after strike 2, got %+v", cond)
	}

	// Repeated poll at head-2 must deduplicate escalation.
	dup := s2.onPRStatusMergeTrain(ctx, reloadedTask, TodoPRStatus{
		URL:       prURL,
		State:     "open",
		HeadOID:   "head-2",
		Review:    "approved",
		Checks:    "failure",
		Conflicts: true,
		CheckedAt: now.Add(3 * time.Second),
	})
	if dup.Escalate {
		t.Fatalf("duplicate escalation emitted for head-2: %+v", dup)
	}

	// Clearing conflicts resolves the persistent merge_train blocker row.
	_ = s2.onPRStatusMergeTrain(ctx, reloadedTask, TodoPRStatus{
		URL:       prURL,
		State:     "open",
		HeadOID:   "head-3",
		Review:    "approved",
		Checks:    "success",
		Conflicts: false,
		CheckedAt: now.Add(4 * time.Second),
	})
	rows, err = st.ListTodoBlockersByTodo(ctx, task.ID)
	if err != nil {
		t.Fatalf("ListTodoBlockersByTodo after conflict clear: %v", err)
	}
	if len(rows) != 1 || rows[0].State != db.TodoBlockerStateResolved || rows[0].ResolvedAt == nil {
		t.Fatalf("expected resolved blocker after conflicts cleared, got %+v", rows)
	}

	// Verify PR merge after a restart also resolves an active merge_train blocker row.
	prURL2 := "https://github.com/example/hardening-repo/pull/102"
	task2 := newMergeTrainTask(t, s2, "Merged after restart PR", prURL2)
	_ = s2.onPRStatusMergeTrain(ctx, task2, TodoPRStatus{
		URL:       prURL2,
		State:     "open",
		HeadOID:   "m-head-1",
		Review:    "approved",
		Checks:    "failure",
		Conflicts: true,
		CheckedAt: now.Add(5 * time.Second),
	})

	s2.stopSuperviseTimers()
	s3 := newRestarted()
	if err := s3.ensureTodosLoaded(ctx); err != nil {
		t.Fatalf("s3.ensureTodosLoaded: %v", err)
	}
	reloadedTask2 := todoNow(s3, task2.ID)
	if reloadedTask2.ID == "" {
		if requireTodoReload {
			t.Fatalf("restarted Service did not reload task %s", task2.ID)
		}
		reloadedTask2 = task2
	}
	_ = s3.onPRStatusMergeTrain(ctx, reloadedTask2, TodoPRStatus{
		URL:       prURL2,
		State:     "merged",
		HeadOID:   "m-head-1",
		CheckedAt: now.Add(6 * time.Second),
	})
	rows2, err := st.ListTodoBlockersByTodo(ctx, task2.ID)
	if err != nil {
		t.Fatalf("ListTodoBlockersByTodo after merge: %v", err)
	}
	if len(rows2) != 1 || rows2[0].State != db.TodoBlockerStateResolved || rows2[0].ResolvedAt == nil {
		t.Fatalf("expected resolved blocker after PR merged across restart, got %+v", rows2)
	}
}

func newRestartedMergeTrainService(t *testing.T, st Store) *Service {
	t.Helper()
	f := newFakeHub()
	f.inbox = newFakeInbox()
	f.sessions["box"] = nil
	f.hello.Assistant.MachineID = "server"
	f.known["conv1"] = true
	// Leave f.settings == nil so the restarted Service loads persisted Settings (including todos_v2=true) from st.
	s := newServiceOn(t, f, st)
	t.Cleanup(s.stopQuietTimers)
	s.stopSuperviseTimers()
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	return s
}

func TestTodosV2RuntimeHardeningMergeTrainRestartPersistence(t *testing.T) {
	store := newMemBlockerStore()
	newBoot1 := func() *Service {
		s, _ := newQuietServiceOn(t, store)
		s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
		return s
	}
	newRestarted := func() *Service {
		return newRestartedMergeTrainService(t, store)
	}
	runMergeTrainRestartPersistenceScenario(t, newBoot1, newRestarted, store, false)
}

func TestTodosV2RuntimeHardeningMergeTrainRestartPersistencePostgres(t *testing.T) {
	dsn := newDatabase(t)
	ctx := t.Context()

	database, err := db.Open(ctx, dsn, db.ManagerSet)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	newBoot1 := func() *Service {
		s, _ := newQuietServiceOn(t, database)
		s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
		return s
	}
	newRestarted := func() *Service {
		return newRestartedMergeTrainService(t, database)
	}
	runMergeTrainRestartPersistenceScenario(t, newBoot1, newRestarted, database, true)
}

func TestTodosV2RuntimeHardeningLiveChangedFilesLayer2AndCache(t *testing.T) {
	ctx := context.Background()
	s, _ := newQuietServiceOn(t, nil)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	setMergeTrainTodosV2Flag(t, s, true, false)

	// Create two open supervised tasks in the same repo with disjoint static prefixes and force_parallel=true.
	taskA, err := s.createTodo(ctx, "Live task A", "write_prefixes: server/a\nforce_parallel: true", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	s.superviseTodo(ctx, taskA.ID, "conv1", []string{"done"}, 0)
	_ = s.linkTodoAgent(ctx, taskA.ID, "box", "sess-a", "Live task A", "/code/app", "claude")
	taskA = todoNow(s, taskA.ID)

	taskB, err := s.createTodo(ctx, "Live task B", "write_prefixes: server/b\nforce_parallel: true", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	s.superviseTodo(ctx, taskB.ID, "conv1", []string{"done"}, 0)
	_ = s.linkTodoAgent(ctx, taskB.ID, "box", "sess-b", "Live task B", "/code/app", "claude")
	taskB = todoNow(s, taskB.ID)

	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	s.setRuntimeChangedFilesClock(func() time.Time { return now })

	var fetchCalls atomic.Int32
	s.setRuntimeChangedFilesFetcher(func(_ context.Context, machineID, cwd string) ([]string, error) {
		fetchCalls.Add(1)
		if machineID != "box" || cwd != "/code/app" {
			t.Errorf("unexpected workspace query (%q, %q)", machineID, cwd)
		}
		return []string{"./server/shared/util.go", "server/shared/util.go", "/abs/ignored.go"}, nil
	})

	agentsA := []proto.Session{{MachineID: "box", SessionID: "sess-a", Cwd: "/code/app", State: proto.StateWorking}}
	agentsB := []proto.Session{{MachineID: "box", SessionID: "sess-b", Cwd: "/code/app", State: proto.StateWorking}}

	// First collection for task A fetches once and records normalized file path.
	if got := s.refreshTodoRuntimeChangedFiles(ctx, taskA, agentsA); len(got) != 0 {
		t.Fatalf("expected no runtime conflict with only task A collected, got %+v", got)
	}
	if fetchCalls.Load() != 1 {
		t.Fatalf("fetchCalls = %d, want 1", fetchCalls.Load())
	}

	// Second call within TTL (30s) must hit cache and NOT invoke git fetcher again.
	now = now.Add(10 * time.Second)
	_ = s.refreshTodoRuntimeChangedFiles(ctx, taskA, agentsA)
	if fetchCalls.Load() != 1 {
		t.Fatalf("cache TTL missed: fetchCalls = %d, want 1", fetchCalls.Load())
	}

	// Advancing past TTL (30s) allows a fresh collection.
	now = now.Add(25 * time.Second)
	_ = s.refreshTodoRuntimeChangedFiles(ctx, taskA, agentsA)
	if fetchCalls.Load() != 2 {
		t.Fatalf("expected refresh after TTL expiry: fetchCalls = %d, want 2", fetchCalls.Load())
	}

	// Collecting for task B touches the same file -> Layer-2 runtime intersection fires even with force_parallel=true!
	liveConflicts := s.refreshTodoRuntimeChangedFiles(ctx, taskB, agentsB)
	if len(liveConflicts) != 1 {
		t.Fatalf("expected 1 live runtime conflict between task A and task B, got %+v", liveConflicts)
	}
	c := liveConflicts[0]
	if c.Layer != conflict.LayerRuntime || c.Action != conflict.ActionSerialize || !slices.Equal(c.RuntimeFiles, []string{"server/shared/util.go"}) {
		t.Fatalf("unexpected live runtime conflict: %+v", c)
	}

	sig, hasSig := pendingPRConflict(s, taskB.ID)
	if !hasSig || !strings.Contains(sig.Detail, "Runtime write-scope conflict") || !strings.Contains(sig.Detail, "server/shared/util.go") {
		t.Fatalf("expected deduplicated runtime write-scope conflict signal, got (%v, %+v)", hasSig, sig)
	}

	// A subsequent task_plan with force_parallel=true touching the same file is also intercepted as ActionSerialize.
	planned := s.detectPlannedWriteScopeConflicts(ctx, []plannedWriteScopeInput{
		{
			Task:          PlanTask{Title: "Planned C", Machine: "box", Cwd: "/code/app"},
			WritePrefixes: []string{"server/c"},
			ChangedFiles:  []string{"server/shared/util.go"},
			ForceParallel: true,
		},
	})
	matched := false
	for _, pc := range planned {
		if (pc.TodoA == taskA.ID || pc.TodoB == taskA.ID) &&
			pc.Layer == conflict.LayerRuntime &&
			pc.Action == conflict.ActionSerialize &&
			slices.Equal(pc.RuntimeFiles, []string{"server/shared/util.go"}) {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("expected planned Layer-2 ActionSerialize against %s even with force_parallel=true, got %+v", taskA.ID, planned)
	}
}

func TestTodosV2RuntimeHardeningBypassWhenFlagOff(t *testing.T) {
	ctx := context.Background()
	store := newMemBlockerStore()
	s, _ := newQuietServiceOn(t, store)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	if s.settings(ctx).todosV2On() {
		t.Fatal("expected todos_v2 default false")
	}

	prURL := "https://github.com/example/off-repo/pull/55"
	task := newMergeTrainTask(t, s, "Off flag task", prURL)
	_ = s.linkTodoAgent(ctx, task.ID, "box", "sess-off", "Off flag task", "/code/app", "claude")
	task = todoNow(s, task.ID)

	// 1. mergetrain conflicts under todos_v2=false must write zero rows to todo_blockers.
	now := time.Now().UTC()
	for i, head := range []string{"off-1", "off-2"} {
		if err := s.applyPRStatus(ctx, task, TodoPRStatus{
			URL:       prURL,
			State:     "open",
			HeadOID:   head,
			Review:    "approved",
			Checks:    "failure",
			Conflicts: true,
			CheckedAt: now.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := store.ListTodoBlockersByTodo(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("todos_v2=false wrote todo_blockers rows: %+v", rows)
	}

	// 2. Runtime changed-files collector under todos_v2=false must perform zero git calls.
	var fetchCalls atomic.Int32
	s.setRuntimeChangedFilesFetcher(func(_ context.Context, _, _ string) ([]string, error) {
		fetchCalls.Add(1)
		return []string{"server/shared/util.go"}, nil
	})
	agents := []proto.Session{{MachineID: "box", SessionID: "sess-off", Cwd: "/code/app", State: proto.StateWorking}}
	if got := s.refreshTodoRuntimeChangedFiles(ctx, task, agents); len(got) != 0 {
		t.Fatalf("todos_v2=false returned runtime conflicts: %+v", got)
	}
	if fetchCalls.Load() != 0 {
		t.Fatalf("todos_v2=false invoked runtime changed-files fetcher %d times, want 0", fetchCalls.Load())
	}
}
