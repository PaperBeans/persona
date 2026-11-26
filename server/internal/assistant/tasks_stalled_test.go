package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// stalledTaskFixture is a supervised task whose Persona-started terminal Agent s1 begins a no-progress stretch at
// base. The explicit supervision timestamp keeps supervisor action and session activity on the same fake clock.
func stalledTaskFixture(t *testing.T, expect int) (*Service, *fakeHub, Todo, proto.Session, time.Time) {
	t.Helper()
	return stalledTaskFixtureOn(t, nil, expect)
}

func stalledTaskFixtureOn(t *testing.T, st Store, expect int) (*Service, *fakeHub, Todo, proto.Session, time.Time) {
	t.Helper()
	var (
		s    *Service
		f    *fakeHub
		todo Todo
	)
	ctx := context.Background()
	if st == nil {
		s, f, todo = newSupervisedService(t)
	} else {
		s, f = newQuietServiceOn(t, st)
		created, err := s.createTodo(ctx, "Supervised task", "Check timeout handling", "conv1", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.superviseTodo(ctx, created.ID, "conv1", []string{"Verified"}, expect); err != nil {
			t.Fatal(err)
		}
		if err := s.linkTodoAgent(ctx, created.ID, "box", "agent-0001", "Supervised task", "/code/app", "claude"); err != nil {
			t.Fatal(err)
		}
		todo = todoNow(s, created.ID)
	}
	if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool {
		v.ExpectMinutes = expect
		return true
	}); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC()
	s.todos.mu.Lock()
	s.todos.todos[todo.ID].Supervision.UpdatedAt = base
	s.todos.mu.Unlock()
	todo = todoNow(s, todo.ID)
	agent := proto.Session{
		SessionID: todo.Agents[0].SessionID, MachineID: "box", Host: proto.EngineHerdr, PaneID: "w1:p1",
		Cwd: "/code/app", WorkspaceCwd: "/code/app", WorkspaceID: "workspace-1", Agent: "claude",
		Status: proto.StatusWorking, State: proto.StateWorking, UpdatedAt: base, StartedByPersona: true,
	}
	f.setSessions("box", agent)
	return s, f, todo, agent, base
}

// stalledPolling is agent at minute i of a stretch of sleeps and repeated status checks. Its transcript keeps moving,
// but none of this is real progress.
func stalledPolling(agent proto.Session, base time.Time, i int) proto.Session {
	at := base.Add(time.Duration(i) * time.Minute)
	agent.UpdatedAt, agent.ActivityAt = at, &at
	agent.Activity = "在等待" // i18n-ignore: the node's activity line for a sleep (transcript.Activity)
	if i%2 == 1 {
		agent.Activity = "在跑 ./scripts/status.sh" // i18n-ignore: the node's activity line for a shell command
	}
	return agent
}

func stalledTimeoutNotices(f *fakeHub, id string) (out []string) {
	for _, notice := range f.notices() {
		if strings.Contains(notice, id) && strings.Contains(notice, "signal=timed_out") {
			out = append(out, notice)
		}
	}
	return out
}

func stalledStopIDs(f *fakeHub) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.stopped)
}

// A terminal Agent stops once at max(60 minutes, 3 × expect), never just before it. Its open task, rather than a
// completed one, carries the public timed_out state, and the same stretch cannot stop it again on a later pass.
func TestTaskStalledClosesTerminalAtThreshold(t *testing.T) {
	for _, tc := range []struct {
		name   string
		expect int
		after  time.Duration
	}{
		{"one-hour floor", 0, time.Hour},
		{"estimate longer than floor", 30, 90 * time.Minute},
		{"long estimate", 90, 270 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f, todo, agent, base := stalledTaskFixture(t, tc.expect)
			ctx := context.Background()
			s.superviseWatch(ctx, base)
			s.superviseWatch(ctx, base.Add(tc.after-time.Second))
			if calls := retireCalls(f); len(calls) != 0 {
				t.Fatalf("stopped before %v: %v", tc.after, calls)
			}

			s.superviseWatch(ctx, base.Add(tc.after))
			calls := retireCalls(f)
			if len(calls) != 1 || calls[0] != deviceSessionStop+" stop_cause=timeout" {
				t.Fatalf("at %v: stop calls = %v, want one terminal-retaining session.stop with stop_cause=timeout", tc.after, calls)
			}
			if stopped := stalledStopIDs(f); len(stopped) != 1 || stopped[0] != agent.SessionID {
				t.Fatalf("stopped Agents = %v, want only %s", stopped, agent.SessionID)
			}
			got := todoNow(s, todo.ID)
			if got.DoneAt != nil || got.Supervision == nil || got.Supervision.state(got) != "timed_out" || taskState(got, nil) != "timed_out" {
				t.Fatalf("the open task is not marked timed_out: %+v; supervision: %+v; public state: %q", got, got.Supervision, taskState(got, nil))
			}

			s.superviseWatch(ctx, base.Add(tc.after+time.Hour))
			if calls := retireCalls(f); len(calls) != 1 {
				t.Fatalf("a later pass stopped the same stretch again: %v", calls)
			}
		})
	}
}

// Real progress and a supervisor message each begin a fresh full timeout stretch. Merely sleeping and polling keeps
// the old one running even though UpdatedAt and ActivityAt advance every minute.
func TestTaskStalledProgressAndSupervisorActionResetClock(t *testing.T) {
	for _, tc := range []struct {
		name string
		at20 func(*Service, *proto.Session, time.Time)
		stop int
	}{
		{"real progress", func(_ *Service, agent *proto.Session, _ time.Time) {
			agent.Activity = "在改 login.swift" // i18n-ignore: node activity for an edit
		}, 80},
		{"supervisor action", func(s *Service, agent *proto.Session, now time.Time) {
			s.progressMade(agent.MachineID, agent.SessionID, now)
		}, 80},
		{"only waits and polls", nil, 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f, _, agent, base := stalledTaskFixture(t, 0)
			ctx := context.Background()
			for i := 0; i <= tc.stop+1; i++ {
				now := base.Add(time.Duration(i) * time.Minute)
				row := stalledPolling(agent, base, i)
				if i == 20 && tc.at20 != nil {
					tc.at20(s, &row, now)
				}
				f.setSessions("box", row)
				s.superviseWatch(ctx, now)
				calls := retireCalls(f)
				switch {
				case i < tc.stop && len(calls) != 0:
					t.Fatalf("minute %d: stopped before a fresh full threshold: %v", i, calls)
				case i == tc.stop && len(calls) != 1:
					t.Fatalf("minute %d: stop calls = %v, want the first stop at the threshold", i, calls)
				case i > tc.stop && len(calls) != 1:
					t.Fatalf("minute %d: repeated stop: %v", i, calls)
				}
			}
		})
	}
}

// Timeout keeps the task's only Agent link and the node's transcript/workspace. Retry continues that same session;
// it does not release the link, start a replacement Agent, archive the session, or remove its workspace.
func TestTaskStalledContinuationResumesOriginalSession(t *testing.T) {
	s, f, todo, agent, base := stalledTaskFixture(t, 0)
	// The production transcript reader requires a full session ID, unlike the short IDs sufficient for node-call fakes.
	s.todos.mu.Lock()
	delete(s.todos.activeBySession, [2]string{"box", agent.SessionID})
	agent.SessionID = "agent-0001"
	s.todos.todos[todo.ID].Agents[0].SessionID = agent.SessionID
	s.todos.activeBySession[[2]string{"box", agent.SessionID}] = todo.ID
	s.todos.mu.Unlock()
	todo = todoNow(s, todo.ID)
	f.setSessions("box", agent)
	ctx := context.Background()
	s.superviseWatch(ctx, base)
	s.superviseWatch(ctx, base.Add(time.Hour))
	if calls := retireCalls(f); len(calls) != 1 {
		t.Fatalf("timeout did not stop the original Agent: %v", calls)
	}
	original := todoNow(s, todo.ID)
	if len(original.Agents) != 1 || original.Agents[0].SessionID != agent.SessionID || original.Agents[0].Cwd != agent.Cwd ||
		original.Agents[0].ReleasedAt != nil || original.Agents[0].DismissedAt != nil {
		t.Fatalf("timeout released or replaced the original Agent link: %+v", original.Agents)
	}

	stopped := agent
	stopped.Status, stopped.State = proto.StatusOffline, proto.StateIdle
	stopped.Capabilities = &proto.SessionCapabilities{Read: true, Terminal: true, StopCause: proto.StopTimeout}
	stopped.ReadOnlyReason = "stopped"
	f.setSessions("box", stopped)
	f.setItems(proto.Item{Seq: 1, Role: "assistant", Kind: proto.KindText, Text: "the original transcript and diagnosis"})
	resumingNode(f)
	before := len(startCalls(f))
	if _, err := s.retryTask(ctx, todo.ID); err != nil {
		t.Fatalf("continue timed-out task: %v", err)
	}
	waitFor(t, "the original session's resume", func() bool { return len(startCalls(f)) > before })
	starts := startCalls(f)
	if len(starts) != before+1 || starts[before]["resume_session_id"] != agent.SessionID || starts[before]["resume_automatic"] == true {
		t.Fatalf("continuation did not resume %s exactly once through the user's Resume: %v", agent.SessionID, starts[before:])
	}

	got := todoNow(s, todo.ID)
	if todoCount(s) != 1 || len(got.Agents) != 1 || got.Agents[0].SessionID != agent.SessionID || got.Agents[0].Cwd != agent.Cwd ||
		got.Agents[0].ReleasedAt != nil || got.Agents[0].DismissedAt != nil {
		t.Fatalf("continuation replaced the task or its Agent: count=%d task=%+v", todoCount(s), got)
	}
	read, err := s.toolAgentRead(ctx, "box", agent.SessionID, 1, s.agentCallerSpace(ctx, "box", "conv1"))
	if err != nil || !strings.Contains(read, "the original transcript and diagnosis") {
		t.Fatalf("the original transcript was not retained: %q, %v", read, err)
	}
	f.mu.Lock()
	calls := append([]DeviceCall(nil), f.calls...)
	f.mu.Unlock()
	for _, call := range calls {
		switch call.Method {
		case deviceSessionRetire, "session.close", deviceSessionArchive, deviceWorkspaceClose, "worktree.remove":
			t.Fatalf("timeout or continuation discarded retained state with %s", call.Method)
		}
	}
}

// The timed-out continuation path must apply the same permanent-turn refusal as an ordinary cut-off turn. Neither a
// provider-specific wire kind nor reliance on recognizing the raw sealed-compaction text may permit a resume.
func TestTaskStalledInvalidConversationRejectsManualContinue(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		kind string
		code string
	}{
		{"wire unrecoverable with unrecognized text", "provider-specific terminal context failure", proto.TurnErrorUnrecoverable, ""},
		{"wire compaction-sealed code with unrecognized text", "provider-specific terminal context failure", "", proto.TurnErrorCompactionSealed},
		{"raw sealed compaction marker", sealedCompaction, proto.TurnErrorTransient, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f, todo, agent, base := stalledTaskFixture(t, 0)
			ctx := context.Background()
			s.superviseWatch(ctx, base)
			s.superviseWatch(ctx, base.Add(time.Hour))
			if got := todoNow(s, todo.ID); got.Supervision == nil || got.Supervision.state(got) != reviewTimedOut {
				t.Fatalf("setup did not produce a timed-out task: %+v", got.Supervision)
			}

			stopped := agent
			stopped.Status, stopped.State = proto.StatusOffline, proto.StateIdle
			stopped.Capabilities = &proto.SessionCapabilities{Read: true, Terminal: true, StopCause: proto.StopTimeout}
			stopped.ReadOnlyReason = "stopped"
			stopped.TurnError, stopped.TurnErrorKind, stopped.TurnErrorCode = tc.text, tc.kind, tc.code
			f.setSessions("box", stopped)
			resumingNode(f)
			startsBefore, sendsBefore := len(startCalls(f)), sessionSends(f)
			_, err := s.retryTask(ctx, todo.ID)

			if starts := startCalls(f)[startsBefore:]; len(starts) != 0 {
				t.Errorf("manual Continue resumed the invalid conversation with %s: %v", DeviceSessionStart, starts)
			}
			if sends := sessionSends(f) - sendsBefore; sends != 0 {
				t.Errorf("manual Continue sent %d ordinary Continue messages to the invalid conversation", sends)
			}
			if err == nil {
				t.Error("manual Continue did not refuse the invalid conversation")
			}
		})
	}
}

// A successful stop produces one timeout entry through the ordinary supervision digest, never one per watch pass.
func TestTaskStalledTimeoutNotifiesSupervisorOnce(t *testing.T) {
	s, f, todo, _, base := stalledTaskFixture(t, 0)
	ctx := context.Background()
	s.superviseWatch(ctx, base)
	s.superviseWatch(ctx, base.Add(time.Hour))
	if calls := retireCalls(f); len(calls) != 1 {
		t.Fatalf("timeout did not stop the Agent: %v", calls)
	}
	waitFor(t, "the supervisor's timeout notification", func() bool { return len(stalledTimeoutNotices(f, todo.ID)) == 1 })

	s.superviseWatch(ctx, base.Add(2*time.Hour))
	seen := supervisionOf(s, f, todo.ID)
	if slices.Contains(seen.pending, "timed_out") || len(stalledTimeoutNotices(f, todo.ID)) != 1 {
		t.Fatalf("timeout notification was repeated: pending=%v notices=%v", seen.pending, stalledTimeoutNotices(f, todo.ID))
	}
}

func TestTaskStalledTimeoutRendered(t *testing.T) {
	e := prompts.SuperviseEntry{ID: "todo_1", Title: "Fix the stall", Kind: sigTimedOut}
	if got := prompts.RenderSupervise(prompts.Supervise{Entries: []prompts.SuperviseEntry{e}}); !strings.Contains(got, "Agent 已停止") ||
		!strings.Contains(got, "原会话和工作区保留") {
		t.Fatalf("supervise:\n%s", got)
	}
	if got := prompts.RenderWorkerSupervise([]prompts.SuperviseEntry{e}); !strings.Contains(got, "the Agent was stopped") ||
		!strings.Contains(got, "session and workspace are retained") {
		t.Fatalf("worker supervise:\n%s", got)
	}
}

// A node refusal leaves the task open and actively supervised: no timed_out marker and no notification that claims a
// successful closure. The attempted stop itself is visible in the fake hub's calls.
func TestTaskStalledStopFailureDoesNotMarkTimedOut(t *testing.T) {
	s, f, todo, _, base := stalledTaskFixture(t, 0)
	f.retireErr = errors.New("node refused stop")
	ctx := context.Background()
	s.superviseWatch(ctx, base)
	s.superviseWatch(ctx, base.Add(time.Hour))
	if calls := retireCalls(f); len(calls) != 1 {
		t.Fatalf("the threshold did not attempt one stop: %v", calls)
	}
	got := todoNow(s, todo.ID)
	if got.DoneAt != nil || got.Supervision == nil || got.Supervision.state(got) == "timed_out" || taskState(got, nil) == "timed_out" {
		t.Fatalf("failed stop marked the task timed out: %+v", got)
	}
	seen := supervisionOf(s, f, todo.ID)
	if slices.Contains(seen.pending, "timed_out") || seen.told[todo.ID+"|timed_out"] != "" || len(stalledTimeoutNotices(f, todo.ID)) != 0 {
		t.Fatalf("failed stop sent a successful timeout notice: pending=%v told=%v notices=%v", seen.pending, seen.told, stalledTimeoutNotices(f, todo.ID))
	}
}

func TestTaskStalledPollingStopFailureRetriesWithoutReset(t *testing.T) {
	s, f, todo, agent, base := stalledTaskFixture(t, 0)
	ctx := context.Background()
	for i := 0; i <= 61; i++ {
		now := base.Add(time.Duration(i) * time.Minute)
		f.setSessions("box", stalledPolling(agent, base, i))
		if i == 60 {
			f.retireErr = errors.New("transient stop failure")
		} else if i == 61 {
			f.retireErr = nil
		}
		s.superviseWatch(ctx, now)
		calls := retireCalls(f)
		switch i {
		case 60:
			if len(calls) != 1 {
				t.Fatalf("minute %d: first stop calls = %v, want one", i, calls)
			}
			if got := todoNow(s, todo.ID); got.Supervision.state(got) != "timeout_closing" {
				t.Fatalf("failed stop did not retain its close intent: %+v", got.Supervision)
			}
		case 61:
			if len(calls) != 2 {
				t.Fatalf("minute %d: stop calls = %v, polling restarted the full timeout", i, calls)
			}
			if got := todoNow(s, todo.ID); got.Supervision.state(got) != "timed_out" {
				t.Fatalf("successful retry did not mark the task timed out: %+v", got.Supervision)
			}
		}
	}
}

// Runtime-owned sessions retain the existing stalled reminder, but this mechanism neither stops them nor marks their
// tasks timed out.
func TestTaskStalledRuntimeOnlyReminds(t *testing.T) {
	s, f, todo, agent, base := stalledTaskFixture(t, 0)
	agent.Host, agent.SourceKind, agent.SourceInstanceID = proto.EnginePaseo, proto.SourceRuntime, "paseo"
	f.setSessions("box", agent)
	ctx := context.Background()
	s.superviseWatch(ctx, base)
	s.superviseWatch(ctx, base.Add(time.Hour))
	if calls := retireCalls(f); len(calls) != 0 {
		t.Fatalf("runtime-owned session was stopped: %v", calls)
	}
	got := todoNow(s, todo.ID)
	if got.Supervision == nil || got.Supervision.state(got) == "timed_out" || taskState(got, nil) == "timed_out" {
		t.Fatalf("runtime-owned session's task was marked timed out: %+v", got)
	}
	waitFor(t, "the runtime session's existing stalled reminder", func() bool {
		for _, notice := range f.notices() {
			if strings.Contains(notice, todo.ID) && strings.Contains(notice, "signal=stalled") {
				return true
			}
		}
		return false
	})
}

// Review, owner input and intentional parking hold closure. Sessions Persona does not own, assistant conversations,
// and sessions outside a Persona terminal are never this mechanism's stop targets.
func TestTaskStalledRegressionExclusions(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state string
		edit  func(*proto.Session)
	}{
		{"waiting for review", reviewPending, nil},
		{"waiting for owner input", reviewNeedsUser, nil},
		{"waiting for personal verification", reviewUserCheck, nil},
		{"intentionally parked", reviewParked, nil},
		{"assistant conversation", "", func(agent *proto.Session) { agent.Assistant = true }},
		{"not started by Persona", "", func(agent *proto.Session) { agent.StartedByPersona = false }},
		{"outside a terminal", "", func(agent *proto.Session) { agent.PaneID = "" }},
		{"another tool's session", "", func(agent *proto.Session) { agent.SourceKind = "codex_app_server" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f, todo, agent, base := stalledTaskFixture(t, 0)
			ctx := context.Background()
			switch tc.state {
			case reviewPending, reviewNeedsUser, reviewUserCheck:
				if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool { v.State = tc.state; return true }); err != nil {
					t.Fatal(err)
				}
			case reviewParked:
				seedLegacyPark(t, s, todo.ID, "wait for the vendor's scheduled answer", parkSomeoneElse, "3d")
			}
			if tc.edit != nil {
				tc.edit(&agent)
				f.setSessions("box", agent)
			}
			s.superviseWatch(ctx, base)
			s.superviseWatch(ctx, base.Add(2*time.Hour))
			if calls := retireCalls(f); len(calls) != 0 {
				t.Fatalf("excluded session was stopped: %v", calls)
			}
			got := todoNow(s, todo.ID)
			if got.Supervision == nil || got.Supervision.state(got) == "timed_out" || taskState(got, nil) == "timed_out" {
				t.Fatalf("excluded task was marked timed out: %+v", got)
			}
		})
	}
}

func stalledSignalsFor(f *fakeHub, id, kind string) (out []string) {
	want := "signal=" + kind
	for _, notice := range f.notices() {
		if strings.Contains(notice, id) && strings.Contains(notice, want) {
			out = append(out, notice)
		}
	}
	return out
}

// Live background workflows (with advancing phase and completed counts), running subagents, and active background
// tasks count as real progress and never raise stalled or trigger a timeout stop. Once a workflow stalls dead,
// timeout counts from the last observed progress rather than from when the main turn ended.
func TestTaskStalledDoesNotStopAliveBackgroundWorkflowOrTasks(t *testing.T) {
	t.Run("two running workflows advancing over 90 minutes", func(t *testing.T) {
		s, f, todo, agent, base := stalledTaskFixture(t, 0)
		ctx := context.Background()
		agent.Status = proto.StatusDone
		agent.State = proto.StateWorking
		agent.BackgroundWorking = true
		agent.UpdatedAt = base
		for i := 0; i <= 90; i++ {
			now := base.Add(time.Duration(i) * time.Minute)
			row := agent
			phase1, idx1, done1 := "Scan", 1, i/10
			if i >= 45 {
				phase1, idx1 = "Verify", 2
			}
			row.Workflows = []proto.Workflow{
				{
					RunID: "wf_a", TaskID: "wa", Name: "audit-a", Status: proto.WorkflowRunning, Running: true,
					Phase: phase1, PhaseIndex: idx1, AgentsStarted: done1 + 2, AgentsDone: done1,
					StartedAt: base, UpdatedAt: now,
				},
				{
					RunID: "wf_b", TaskID: "wb", Name: "audit-b", Status: proto.WorkflowRunning, Running: true,
					Phase: "Analyze", PhaseIndex: 1, AgentsStarted: i/15 + 1, AgentsDone: i / 15,
					StartedAt: base, UpdatedAt: now,
				},
			}
			f.setSessions("box", row)
			s.superviseWatch(ctx, now)
			if calls := retireCalls(f); len(calls) != 0 {
				t.Fatalf("minute %d: live workflows were stopped: %v", i, calls)
			}
			seen := supervisionOf(s, f, todo.ID)
			if slices.Contains(seen.pending, sigStalled) || seen.told[todo.ID+"|"+sigStalled] != "" ||
				slices.Contains(seen.pending, sigNoProgress) || seen.told[todo.ID+"|"+sigNoProgress] != "" ||
				slices.Contains(seen.pending, sigAllStopped) || seen.told[todo.ID+"|"+sigAllStopped] != "" {
				t.Fatalf("minute %d: live workflows raised stall signal: pending=%v told=%v", i, seen.pending, seen.told)
			}
		}
		if got := todoNow(s, todo.ID); got.Supervision.state(got) == reviewTimedOut || got.Supervision.state(got) == reviewClosing {
			t.Fatalf("task with live workflows entered timeout state: %+v", got.Supervision)
		}

		// When both workflows stall dead after minute 90, the timeout clock starts from their last real progress (90m),
		// not from base (0m): still not stopped at minute 149, then stopped at minute 150.
		dead := agent
		dead.Status = proto.StatusWorking
		lastAt := base.Add(90 * time.Minute)
		dead.Workflows = []proto.Workflow{
			{RunID: "wf_a", TaskID: "wa", Status: proto.WorkflowStalled, Running: false, Phase: "Verify", PhaseIndex: 2, AgentsStarted: 11, AgentsDone: 9, StartedAt: base, UpdatedAt: lastAt},
			{RunID: "wf_b", TaskID: "wb", Status: proto.WorkflowStalled, Running: false, Phase: "Analyze", PhaseIndex: 1, AgentsStarted: 7, AgentsDone: 6, StartedAt: base, UpdatedAt: lastAt},
		}
		f.setSessions("box", dead)
		s.superviseWatch(ctx, base.Add(149*time.Minute))
		if calls := retireCalls(f); len(calls) != 0 {
			t.Fatalf("stopped before full threshold after workflows stalled: %v", calls)
		}
		s.superviseWatch(ctx, base.Add(150*time.Minute))
		if calls := retireCalls(f); len(calls) != 1 || calls[0] != deviceSessionStop+" stop_cause=timeout" {
			t.Fatalf("stalled workflow fallback stop = %v, want one timeout stop", calls)
		}
	})

	t.Run("workflow advancing counts without UpdatedAt keeps all_stopped and stalled away", func(t *testing.T) {
		s, f, todo, agent, base := stalledTaskFixture(t, 0)
		ctx := context.Background()
		agent.Status = proto.StatusDone
		agent.State = proto.StateWorking
		agent.BackgroundWorking = true
		agent.UpdatedAt = base
		for i := 0; i <= 75; i += 15 {
			now := base.Add(time.Duration(i) * time.Minute)
			row := agent
			row.Workflows = []proto.Workflow{{
				RunID: "wf_no_ts", TaskID: "w1", Name: "audit", Status: proto.WorkflowRunning, Running: true,
				Phase: "Phase", PhaseIndex: 1, AgentsStarted: i/15 + 1, AgentsDone: i / 15,
			}}
			f.setSessions("box", row)
			s.superviseWatch(ctx, now)
			if calls := retireCalls(f); len(calls) != 0 {
				t.Fatalf("minute %d: running workflow was stopped: %v", i, calls)
			}
			seen := supervisionOf(s, f, todo.ID)
			if slices.Contains(seen.pending, sigStalled) || slices.Contains(seen.pending, sigAllStopped) {
				t.Fatalf("minute %d: advancing workflow raised unexpected signal: pending=%v", i, seen.pending)
			}
		}
	})

	t.Run("running subagent and active background shell", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			edit func(*proto.Session, time.Time)
		}{
			{"subagent", func(c *proto.Session, now time.Time) {
				c.Subagents = []proto.Subagent{{ID: "sub1", Running: true, Steps: 4, StartedAt: now.Add(-time.Hour), UpdatedAt: now}}
			}},
			{"active shell", func(c *proto.Session, now time.Time) {
				c.Background = []proto.BackgroundTask{{ID: "sh1", Kind: "shell", StartedAt: now.Add(-time.Hour)}}
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				s, f, todo, agent, base := stalledTaskFixture(t, 0)
				ctx := context.Background()
				row := agent
				tc.edit(&row, base.Add(70*time.Minute))
				f.setSessions("box", row)
				s.superviseWatch(ctx, base)
				s.superviseWatch(ctx, base.Add(70*time.Minute))
				if calls := retireCalls(f); len(calls) != 0 {
					t.Fatalf("%s was stopped: %v", tc.name, calls)
				}
				seen := supervisionOf(s, f, todo.ID)
				if slices.Contains(seen.pending, sigStalled) || seen.told[todo.ID+"|"+sigStalled] != "" {
					t.Fatalf("%s raised stalled: pending=%v told=%v", tc.name, seen.pending, seen.told)
				}
			})
		}
	})
}

// Once the supervisor handles a stalled or no_progress supervision wake-up (including judging it a normal wait via
// supervision_finish), that stall stretch is no longer unattended and must not proceed to timed_out. An unhandled
// stall still times out, and a later new stall stretch on the same task is supervised normally.
func TestTaskStalledHandledBySupervisorDoesNotTimeOut(t *testing.T) {
	for _, tc := range []struct {
		name     string
		spinning bool
		handle   func(t *testing.T, s *Service)
		wantStop bool
	}{
		{
			name: "supervision_finish silent on stalled",
			handle: func(t *testing.T, s *Service) {
				t.Helper()
				if _, err := s.toolSupervisionFinish(context.Background(), "conv1", json.RawMessage(`{"visibility":"silent","summary":"Agent is legitimately waiting; no nudge needed."}`)); err != nil {
					t.Fatalf("supervision_finish silent: %v", err)
				}
			},
		},
		{
			name: "supervision_finish user on stalled",
			handle: func(t *testing.T, s *Service) {
				t.Helper()
				if _, err := s.toolSupervisionFinish(context.Background(), "conv1", json.RawMessage(`{"visibility":"user","summary":"Noted the long wait for the owner."}`)); err != nil {
					t.Fatalf("supervision_finish user: %v", err)
				}
			},
		},
		{
			name: "promoteSupervision on stalled",
			handle: func(t *testing.T, s *Service) {
				t.Helper()
				if err := s.promoteSupervision("conv1"); err != nil {
					t.Fatalf("promoteSupervision: %v", err)
				}
			},
		},
		{
			name:     "supervision_finish silent on no_progress",
			spinning: true,
			handle: func(t *testing.T, s *Service) {
				t.Helper()
				if _, err := s.toolSupervisionFinish(context.Background(), "conv1", json.RawMessage(`{"visibility":"silent","summary":"Waiting on external polling check."}`)); err != nil {
					t.Fatalf("supervision_finish silent: %v", err)
				}
			},
		},
		{
			name:     "unhandled stalled still times out",
			wantStop: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f, todo, agent, base := stalledTaskFixture(t, 0)
			ctx := context.Background()
			if tc.spinning {
				for i := 0; i <= 30; i++ {
					now := base.Add(time.Duration(i) * time.Minute)
					f.setSessions("box", stalledPolling(agent, base, i))
					s.superviseWatch(ctx, now)
				}
				waitFor(t, "no_progress supervision digest", func() bool {
					return len(stalledSignalsFor(f, todo.ID, sigNoProgress)) == 1
				})
			} else {
				s.superviseWatch(ctx, base)
				s.superviseWatch(ctx, base.Add(21*time.Minute))
				waitFor(t, "stalled supervision digest", func() bool {
					return len(stalledSignalsFor(f, todo.ID, sigStalled)) == 1
				})
			}
			if tc.handle != nil {
				tc.handle(t, s)
			}

			if tc.spinning {
				for i := 31; i <= 90; i++ {
					now := base.Add(time.Duration(i) * time.Minute)
					f.setSessions("box", stalledPolling(agent, base, i))
					s.superviseWatch(ctx, now)
				}
			} else {
				s.superviseWatch(ctx, base.Add(60*time.Minute))
				s.superviseWatch(ctx, base.Add(180*time.Minute))
			}

			calls := retireCalls(f)
			got := todoNow(s, todo.ID)
			if tc.wantStop {
				if len(calls) != 1 || calls[0] != deviceSessionStop+" stop_cause=timeout" || got.Supervision.state(got) != reviewTimedOut {
					t.Fatalf("unhandled stall did not time out: calls=%v supervision=%+v", calls, got.Supervision)
				}
				return
			}
			if len(calls) != 0 || got.Supervision.state(got) == reviewTimedOut || got.Supervision.state(got) == reviewClosing {
				t.Fatalf("handled stall was timed out: calls=%v supervision=%+v", calls, got.Supervision)
			}

			if tc.spinning {
				return
			}
			// A subsequent new stall stretch after later activity is still supervised and times out if unattended.
			movedAt := base.Add(190 * time.Minute)
			later := agent
			later.UpdatedAt = movedAt
			later.LastText = "Started phase 2 work"
			later.Activity = "Running integration tests"
			f.setSessions("box", later)
			s.superviseWatch(ctx, movedAt.Add(21*time.Minute))
			waitFor(t, "second stalled signal for new stretch", func() bool {
				return len(stalledSignalsFor(f, todo.ID, sigStalled)) == 2
			})
			s.superviseWatch(ctx, movedAt.Add(60*time.Minute))
			if calls := retireCalls(f); len(calls) != 1 || calls[0] != deviceSessionStop+" stop_cause=timeout" {
				t.Fatalf("new unattended stall did not time out: calls=%v", calls)
			}
		})
	}
}

// Both the active supervision hold (carrying the stalled stretch ref) and the resulting handled-stall record survive
// manager restarts so a restart neither blocks supervision_finish nor lets the handled stall later time out.
func TestTaskStalledHandledSurvivesManagerRestart(t *testing.T) {
	store := &memStore{}
	s, f, todo, agent, base := stalledTaskFixtureOn(t, store, 0)
	ctx := context.Background()
	s.superviseWatch(ctx, base)
	s.superviseWatch(ctx, base.Add(21*time.Minute))
	waitFor(t, "stalled digest delivered", func() bool {
		return len(stalledSignalsFor(f, todo.ID, sigStalled)) == 1
	})

	// Restart 1: before supervision_finish. The persisted hold keeps the stalled reference across restart.
	restarted1, _ := newQuietServiceOn(t, store)
	if _, err := restarted1.toolSupervisionFinish(ctx, "conv1", json.RawMessage(`{"visibility":"silent","summary":"Normal wait confirmed after restart."}`)); err != nil {
		t.Fatalf("supervision_finish after restart 1: %v", err)
	}

	// Restart 2: after supervision_finish. The persisted handled-stall record prevents timed_out and duplicate stalled.
	restarted2, f2 := newQuietServiceOn(t, store)
	restarted2.todos.mu.Lock()
	cp := todo
	cp.Supervision = todo.Supervision.clone()
	restarted2.todos.todos[cp.ID] = &cp
	restarted2.todos.activeBySession[[2]string{agent.MachineID, agent.SessionID}] = cp.ID
	restarted2.todos.loaded = true
	restarted2.todos.mu.Unlock()
	f2.setSessions("box", agent)

	restarted2.superviseWatch(ctx, base.Add(60*time.Minute))
	restarted2.superviseWatch(ctx, base.Add(120*time.Minute))
	if calls := retireCalls(f2); len(calls) != 0 {
		t.Fatalf("handled stall was stopped after restart: %v", calls)
	}
	if got := todoNow(restarted2, todo.ID); got.Supervision.state(got) == reviewTimedOut || got.Supervision.state(got) == reviewClosing {
		t.Fatalf("handled stall marked timed_out after restart: %+v", got.Supervision)
	}
	seen := supervisionOf(restarted2, f2, todo.ID)
	if slices.Contains(seen.pending, sigStalled) || seen.told[todo.ID+"|"+sigStalled] != "" {
		t.Fatalf("handled stall re-emitted after restart: pending=%v told=%v", seen.pending, seen.told)
	}
}

func TestTaskStalledHandledStoreReadFailureFailsSafe(t *testing.T) {
	store := &memStore{}
	s, f, todo, _, base := stalledTaskFixtureOn(t, store, 0)
	ctx := context.Background()
	store.failKey(supervisionStallsHandledKey, errors.New("synthetic store read failure"))
	s.superviseWatch(ctx, base)
	s.superviseWatch(ctx, base.Add(time.Hour))
	if calls := retireCalls(f); len(calls) != 0 {
		t.Fatalf("stopped Agent when handled-stall store could not be read: %v", calls)
	}
	if got := todoNow(s, todo.ID); got.Supervision.state(got) == reviewTimedOut || got.Supervision.state(got) == reviewClosing {
		t.Fatalf("marked timed_out on unreadable handled-stall store: %+v", got.Supervision)
	}
}

// A supervision timeout stop records stop_cause=timeout (never user). Automatic event routing still leaves resuming
// to the supervisor, while the assistant can deliberately resume the stopped session with agent_send (resume: true),
// clearing timed_out and retaining the original task and Agent link.
func TestTaskStalledTimeoutStopAllowsAssistantResume(t *testing.T) {
	s, f, todo, agent, base := stalledTaskFixture(t, 0)
	s.todos.mu.Lock()
	delete(s.todos.activeBySession, [2]string{"box", agent.SessionID})
	agent.SessionID = "agent-0001"
	s.todos.todos[todo.ID].Agents[0].SessionID = agent.SessionID
	s.todos.activeBySession[[2]string{"box", agent.SessionID}] = todo.ID
	s.todos.mu.Unlock()
	todo = todoNow(s, todo.ID)
	f.setSessions("box", agent)
	ctx := context.Background()
	s.superviseWatch(ctx, base)
	s.superviseWatch(ctx, base.Add(time.Hour))
	calls := retireCalls(f)
	if len(calls) != 1 || calls[0] != deviceSessionStop+" stop_cause=timeout" {
		t.Fatalf("timeout stop calls = %v, want stop_cause=timeout", calls)
	}
	if got := todoNow(s, todo.ID); got.Supervision.state(got) != reviewTimedOut {
		t.Fatalf("task not timed_out: %+v", got.Supervision)
	}

	stopped := agent
	stopped.Status, stopped.State = proto.StatusOffline, proto.StateIdle
	stopped.Capabilities = &proto.SessionCapabilities{Read: true, Terminal: true, StopCause: proto.StopTimeout}
	stopped.ReadOnlyReason = "stopped"
	f.setSessions("box", stopped)
	resumingNode(f)

	if err := eventMayResume(stopped); err == nil {
		t.Fatal("automatic event routing was allowed to resume a supervision-timeout stop")
	}

	startsBefore := len(startCalls(f))
	_, err := runJSON(t, s, "conv1", "agent_send", map[string]any{
		"machine": "box", "session_id": agent.SessionID, "text": "Continue from where you left off",
		"resume": true, "resume_why": "Resume the timed-out Agent with its existing context",
	})
	if err != nil {
		t.Fatalf("assistant agent_send resume after timeout failed: %v", err)
	}
	wantStart := map[string]any{
		"resume_session_id": agent.SessionID,
		"prompt":            "Continue from where you left off",
		"resume_automatic":  true,
	}
	gotStarts := startCalls(f)[startsBefore:]
	if len(gotStarts) != 1 || !maps.Equal(gotStarts[0], wantStart) {
		t.Fatalf("session.start = %v, want %v", gotStarts, wantStart)
	}
	got := todoNow(s, todo.ID)
	if got.Supervision == nil || got.Supervision.state(got) == reviewTimedOut || got.Supervision.state(got) == reviewClosing {
		t.Fatalf("assistant resume did not clear timed_out: %+v", got.Supervision)
	}
	if len(got.Agents) != 1 || got.Agents[0].SessionID != agent.SessionID {
		t.Fatalf("assistant resume altered task Agent links: %+v", got.Agents)
	}
}
