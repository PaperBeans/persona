package assistant

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/assistant/todo/conflict"
)

func TestTodosV2ConflictOffBypass(t *testing.T) {
	ctx := context.Background()
	s, _ := transferEnv(t)

	// Default todos_v2=false: dispatch two same-repo tasks with overlapping write_prefixes and changed_files.
	out1, bad := dispatchToolFrom(t, s, "lead0001", "task_plan", dispatchTaskArgs("Off task A", map[string]any{
		"cwd":            "/code/app",
		"write_prefixes": []string{"server/internal/assistant"},
		"changed_files":  []string{"server/internal/assistant/tasks_plan.go"},
	}))
	if bad {
		t.Fatalf("first task_plan failed: %s", out1)
	}
	waitFor(t, "first dispatch", func() bool { return s.taskStarting.Load() == 0 })

	out2, bad := dispatchToolFrom(t, s, "lead0001", "task_plan", dispatchTaskArgs("Off task B", map[string]any{
		"cwd":            "/code/app",
		"write_prefixes": []string{"server/internal/assistant/todo"},
		"changed_files":  []string{"server/internal/assistant/tasks_plan.go"},
	}))
	if bad {
		t.Fatalf("second task_plan failed: %s", out2)
	}
	waitFor(t, "second dispatch", func() bool { return s.taskStarting.Load() == 0 })

	if strings.Contains(out1, "Write-scope conflicts") || strings.Contains(out2, "Write-scope conflicts") {
		t.Fatalf("todos_v2=false must 100%% bypass conflict advisories:\nout1=%s\nout2=%s", out1, out2)
	}

	got := s.detectPlannedWriteScopeConflicts(ctx, []plannedWriteScopeInput{
		{
			Task:          PlanTask{Title: "A", Machine: "box", Cwd: "/code/app"},
			WritePrefixes: []string{"server/internal/assistant"},
		},
		{
			Task:          PlanTask{Title: "B", Machine: "box", Cwd: "/code/app"},
			WritePrefixes: []string{"server/internal/assistant"},
		},
	})
	if len(got) != 0 {
		t.Fatalf("todos_v2=false must return nil conflicts, got %+v", got)
	}
}

func TestTodosV2ConflictSameRepoStaticOverlap(t *testing.T) {
	ctx := context.Background()
	s, _ := transferEnv(t)
	setTodosV2ForTest(t, s, true, false)

	// 1. Dispatch an active task declaring write_prefixes via JSON field.
	firstTodo := dispatchNewTodo(t, s, "lead0001", dispatchTaskArgs("Active assistant refactor", map[string]any{
		"cwd":            "/code/app",
		"write_prefixes": []string{"server/internal/assistant"},
	}))

	// 2. Plan a second task in the same repo with overlapping subdirectory prefix.
	out, bad := dispatchToolFrom(t, s, "lead0001", "task_plan", dispatchTaskArgs("Subdir todo wiring", map[string]any{
		"cwd":            "/code/app",
		"write_prefixes": []string{"server/internal/assistant/todo"},
	}))
	if bad {
		t.Fatalf("second task_plan failed: %s", out)
	}
	waitFor(t, "second dispatch", func() bool { return s.taskStarting.Load() == 0 })

	for _, want := range []string{
		"Write-scope conflicts (same repository):",
		firstTodo.ID,
		"layer=static",
		"action=serialize",
		"server/internal/assistant/todo",
		"recommend running serially",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected conflict advisory to contain %q, got:\n%s", want, out)
		}
	}

	// 3. Also verify prompt-tag extraction works without explicit JSON write_prefixes.
	conflicts := s.detectPlannedWriteScopeConflicts(ctx, []plannedWriteScopeInput{
		{
			Task: PlanTask{
				Title:   "Prompt-tagged task",
				Machine: "box",
				Cwd:     "/code/app",
				Prompt:  "Update conflict wiring.\nwrite_prefixes: server/internal/assistant/todo",
			},
		},
	})
	if len(conflicts) == 0 {
		t.Fatalf("expected conflict via prompt write_prefixes tag against %s, got none", firstTodo.ID)
	}
	found := false
	for _, c := range conflicts {
		if (c.TodoA == firstTodo.ID || c.TodoB == firstTodo.ID) &&
			c.Layer == conflict.LayerStatic &&
			c.Action == conflict.ActionSerialize &&
			slices.Contains(c.StaticOverlaps, "server/internal/assistant/todo") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected static ActionSerialize conflict against %s, got %+v", firstTodo.ID, conflicts)
	}
}

func TestTodosV2ConflictCrossRepoNoConflict(t *testing.T) {
	ctx := context.Background()
	s, _ := transferEnv(t)
	setTodosV2ForTest(t, s, true, false)

	_ = dispatchNewTodo(t, s, "lead0001", dispatchTaskArgs("Repo A task", map[string]any{
		"cwd":            "/code/repo-a",
		"write_prefixes": []string{"src/shared"},
		"changed_files":  []string{"src/shared/index.ts"},
	}))

	out, bad := dispatchToolFrom(t, s, "lead0001", "task_plan", dispatchTaskArgs("Repo B task", map[string]any{
		"cwd":            "/code/repo-b",
		"write_prefixes": []string{"src/shared"},
		"changed_files":  []string{"src/shared/index.ts"},
	}))
	if bad {
		t.Fatalf("cross-repo task_plan failed: %s", out)
	}
	waitFor(t, "cross-repo dispatch", func() bool { return s.taskStarting.Load() == 0 })

	if strings.Contains(out, "Write-scope conflicts") {
		t.Fatalf("cross-repo tasks must not conflict, got:\n%s", out)
	}
	if got := s.detectPlannedWriteScopeConflicts(ctx, []plannedWriteScopeInput{
		{
			Task:          PlanTask{Title: "Repo C", Machine: "box", Cwd: "/code/repo-c"},
			WritePrefixes: []string{"src/shared"},
			ChangedFiles:  []string{"src/shared/index.ts"},
		},
	}); len(got) != 0 {
		t.Fatalf("expected no conflicts for different repo, got %+v", got)
	}
}

func TestTodosV2ConflictForceParallelOverridesStatic(t *testing.T) {
	ctx := context.Background()
	s, _ := transferEnv(t)
	setTodosV2ForTest(t, s, true, false)

	firstTodo := dispatchNewTodo(t, s, "lead0001", dispatchTaskArgs("Web components task", map[string]any{
		"cwd":            "/code/app",
		"write_prefixes": []string{"web/src/components"},
	}))

	out, bad := dispatchToolFrom(t, s, "lead0001", "task_plan", dispatchTaskArgs("Parallel button tweak", map[string]any{
		"cwd":            "/code/app",
		"write_prefixes": []string{"web/src/components/button"},
		"force_parallel": true,
	}))
	if bad {
		t.Fatalf("force_parallel task_plan failed: %s", out)
	}
	waitFor(t, "force_parallel dispatch", func() bool { return s.taskStarting.Load() == 0 })

	for _, want := range []string{
		"Write-scope conflicts (same repository):",
		firstTodo.ID,
		"layer=static",
		"action=warn_overridden",
		"static overlap explicitly allowed via force_parallel",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected ActionWarnOverridden advisory containing %q, got:\n%s", want, out)
		}
	}

	got := s.detectPlannedWriteScopeConflicts(ctx, []plannedWriteScopeInput{
		{
			Task:          PlanTask{Title: "Parallel check", Machine: "box", Cwd: "/code/app"},
			WritePrefixes: []string{"web/src/components/button"},
			ForceParallel: true,
		},
	})
	if len(got) == 0 {
		t.Fatalf("expected overridden conflict record, got none")
	}
	matched := false
	for _, c := range got {
		if (c.TodoA == firstTodo.ID || c.TodoB == firstTodo.ID) &&
			c.Layer == conflict.LayerStatic &&
			c.Action == conflict.ActionWarnOverridden {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("expected ActionWarnOverridden for static overlap with force_parallel, got %+v", got)
	}
}

func TestTodosV2ConflictRuntimeIntersectionAlwaysSerializes(t *testing.T) {
	ctx := context.Background()
	s, _ := transferEnv(t)
	setTodosV2ForTest(t, s, true, false)

	// Active task declares disjoint static prefix "server/a" but has actually modified "server/shared/util.go".
	activeTodo := dispatchNewTodo(t, s, "lead0001", dispatchTaskArgs("Active runtime editor", map[string]any{
		"cwd":            "/code/app",
		"write_prefixes": []string{"server/a"},
	}))
	s.recordTodoRuntimeChangedFiles(ctx, activeTodo.ID, []string{"server/shared/util.go"})

	// Planned task declares disjoint static prefix "server/b", sets force_parallel=true,
	// yet touches the same runtime file "server/shared/util.go".
	out, bad := dispatchToolFrom(t, s, "lead0001", "task_plan", dispatchTaskArgs("Conflicting runtime editor", map[string]any{
		"cwd":            "/code/app",
		"write_prefixes": []string{"server/b"},
		"changed_files":  []string{"server/shared/util.go"},
		"force_parallel": true,
	}))
	if bad {
		t.Fatalf("runtime conflict task_plan failed: %s", out)
	}
	waitFor(t, "runtime conflict dispatch", func() bool { return s.taskStarting.Load() == 0 })

	for _, want := range []string{
		"Write-scope conflicts (same repository):",
		activeTodo.ID,
		"layer=runtime",
		"action=serialize",
		"server/shared/util.go",
		"force_parallel does not override runtime intersections",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected runtime ActionSerialize advisory containing %q, got:\n%s", want, out)
		}
	}

	got := s.detectPlannedWriteScopeConflicts(ctx, []plannedWriteScopeInput{
		{
			Task:          PlanTask{Title: "Check runtime", Machine: "box", Cwd: "/code/app"},
			WritePrefixes: []string{"server/b"},
			ChangedFiles:  []string{"server/shared/util.go"},
			ForceParallel: true,
		},
	})
	matched := false
	for _, c := range got {
		if (c.TodoA == activeTodo.ID || c.TodoB == activeTodo.ID) &&
			c.Layer == conflict.LayerRuntime &&
			c.Action == conflict.ActionSerialize &&
			slices.Equal(c.RuntimeFiles, []string{"server/shared/util.go"}) {
			matched = true
			break
		}
	}
	if !matched {
		t.Fatalf("expected runtime ActionSerialize even with force_parallel=true, got %+v", got)
	}
}

func TestTodosV2ConflictRollbackToOff(t *testing.T) {
	ctx := context.Background()
	s, _ := transferEnv(t)
	setTodosV2ForTest(t, s, true, false)

	// 1. With todos_v2=true, static overlap emits conflict advisory.
	_ = dispatchNewTodo(t, s, "lead0001", dispatchTaskArgs("Rollback base task", map[string]any{
		"cwd":            "/code/app",
		"write_prefixes": []string{"server/internal/assistant"},
	}))
	outOn, bad := dispatchToolFrom(t, s, "lead0001", "task_plan", dispatchTaskArgs("Rollback overlapping ON", map[string]any{
		"cwd":            "/code/app",
		"write_prefixes": []string{"server/internal/assistant"},
	}))
	if bad {
		t.Fatalf("task_plan with todos_v2=true failed: %s", outOn)
	}
	waitFor(t, "rollback on dispatch", func() bool { return s.taskStarting.Load() == 0 })
	if !strings.Contains(outOn, "Write-scope conflicts (same repository):") {
		t.Fatalf("expected conflict advisory while todos_v2=true, got:\n%s", outOn)
	}

	// 2. Toggle todos_v2 back to false: immediate lossless rollback, zero conflict detection.
	setTodosV2ForTest(t, s, false, true)

	outOff, bad := dispatchToolFrom(t, s, "lead0001", "task_plan", dispatchTaskArgs("Rollback overlapping OFF", map[string]any{
		"cwd":            "/code/app",
		"write_prefixes": []string{"server/internal/assistant"},
	}))
	if bad {
		t.Fatalf("task_plan after rollback failed: %s", outOff)
	}
	waitFor(t, "rollback off dispatch", func() bool { return s.taskStarting.Load() == 0 })
	if strings.Contains(outOff, "Write-scope conflicts") {
		t.Fatalf("expected zero conflict advisory after rolling back to todos_v2=false, got:\n%s", outOff)
	}
	if got := s.detectPlannedWriteScopeConflicts(ctx, []plannedWriteScopeInput{
		{
			Task:          PlanTask{Title: "Check off", Machine: "box", Cwd: "/code/app"},
			WritePrefixes: []string{"server/internal/assistant"},
		},
	}); len(got) != 0 {
		t.Fatalf("expected nil conflicts after rollback to todos_v2=false, got %+v", got)
	}
}

func TestTodosV2ConflictParseHintsAndConcurrentSafety(t *testing.T) {
	hints := parseWriteScopeHints(
		"- write_prefixes: `server/internal/assistant`, web/src/lib\n" +
			"* changed_files: [server/internal/assistant/tasks_plan.go, web/src/lib/a.ts]\n" +
			"force_parallel: true\n" +
			"repo_key: https://github.com/nwverse/persona.git",
	)
	if !slices.Equal(hints.WritePrefixes, []string{"server/internal/assistant", "web/src/lib"}) {
		t.Fatalf("unexpected write_prefixes: %#v", hints.WritePrefixes)
	}
	if !slices.Equal(hints.ChangedFiles, []string{"server/internal/assistant/tasks_plan.go", "web/src/lib/a.ts"}) {
		t.Fatalf("unexpected changed_files: %#v", hints.ChangedFiles)
	}
	if !hints.ForceParallel {
		t.Fatalf("expected force_parallel=true")
	}
	if hints.RepoKey != "github.com/nwverse/persona" {
		t.Fatalf("unexpected repo_key: %q", hints.RepoKey)
	}

	// Concurrent reads/writes under -race.
	ctx := context.Background()
	s, _ := transferEnv(t)
	setTodosV2ForTest(t, s, true, false)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.recordTodoRuntimeChangedFiles(ctx, "todo_race", []string{"server/internal/assistant/tasks_plan.go"})
			_ = s.detectPlannedWriteScopeConflicts(ctx, []plannedWriteScopeInput{
				{
					Task:          PlanTask{Title: "Concurrent A", Machine: "box", Cwd: "/code/app"},
					WritePrefixes: []string{"server/internal/assistant"},
				},
				{
					Task:          PlanTask{Title: "Concurrent B", Machine: "box", Cwd: "/code/app"},
					WritePrefixes: []string{"server/internal/assistant/todo"},
				},
			})
		}()
	}
	wg.Wait()
}
