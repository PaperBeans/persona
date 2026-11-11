package assistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestLeaderFollowupCadenceRunsEarlyAndRestoresTheOriginalSchedule(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	originalAt := now.Add(3 * time.Hour)
	f := newFakeHub()
	f.settings = &Settings{Main: "main", Leaders: []string{"leader"}, Timezone: "UTC"}
	s := newStorelessService(t, f)
	setTasks(s, Task{ID: "fu_leader", Prompt: "inspect", Repeat: repeatInterval, EveryMinutes: 180,
		At: originalAt, CreatedAt: now, Enabled: true, SessionID: "leader"})
	putTodo(s, cadenceWorkingTodo(f, "todo_open"))

	if got := s.nextDue(ctx, now); !got.Equal(now.Add(10 * time.Minute)) {
		t.Fatalf("open supervision should move the next run to 10 minutes: got %v", got)
	}
	s.runDue(ctx, now.Add(9*time.Minute))
	s.sched.inflight.Wait()
	if task, _ := taskByID(s, "fu_leader"); !task.LastRun.IsZero() {
		t.Fatalf("the accelerated run must not start early: %+v", task)
	}
	s.runDue(ctx, now.Add(10*time.Minute))
	s.sched.inflight.Wait()
	task, _ := taskByID(s, "fu_leader")
	if !task.LastRun.Equal(now.Add(10*time.Minute)) || !task.At.Equal(originalAt) || task.EveryMinutes != 180 {
		t.Fatalf("an early run must keep the original schedule: %+v", task)
	}
	if got := s.nextDue(ctx, now.Add(10*time.Minute)); !got.Equal(now.Add(20 * time.Minute)) {
		t.Fatalf("open supervision should repeat after 10 minutes: got %v", got)
	}

	s.runDue(ctx, now.Add(10*time.Minute))
	s.sched.inflight.Wait()
	if runs := len(s.loadSchedule(ctx).runs); runs != 1 {
		t.Fatalf("the same accelerated time must not dispatch twice: %d runs", runs)
	}

	s.runDue(ctx, now.Add(20*time.Minute))
	s.sched.inflight.Wait()
	task, _ = taskByID(s, "fu_leader")
	if !task.LastRun.Equal(now.Add(20*time.Minute)) || !task.At.Equal(originalAt) {
		t.Fatalf("consecutive early runs must keep the original next run: %+v", task)
	}
	if got := s.nextDue(ctx, now.Add(20*time.Minute)); !got.Equal(now.Add(30 * time.Minute)) {
		t.Fatalf("open supervision should continue every 10 minutes: got %v", got)
	}

	closed := now.Add(21 * time.Minute)
	putTodo(s, Todo{ID: "todo_open", DoneAt: &closed, Supervision: &Supervision{Supervisor: "leader"}})
	if got := s.nextDue(ctx, closed); !got.Equal(originalAt) {
		t.Fatalf("closing the last supervised task should restore the original run: got %v", got)
	}
	s.runDue(ctx, now.Add(30*time.Minute))
	s.sched.inflight.Wait()
	if task, _ = taskByID(s, "fu_leader"); !task.LastRun.Equal(now.Add(20 * time.Minute)) {
		t.Fatalf("the restored schedule must not run at the old accelerated time: %+v", task)
	}

	s.runDue(ctx, originalAt)
	s.sched.inflight.Wait()
	task, _ = taskByID(s, "fu_leader")
	if !task.LastRun.Equal(originalAt) || !task.At.Equal(originalAt.Add(3*time.Hour)) || task.EveryMinutes != 180 {
		t.Fatalf("the original interval should advance normally: %+v", task)
	}

	putTodo(s, cadenceWorkingTodo(f, "todo_new"))
	if got := s.nextDue(ctx, originalAt.Add(time.Minute)); !got.Equal(originalAt.Add(10 * time.Minute)) {
		t.Fatalf("a new open task should accelerate an existing future run: got %v", got)
	}
	s.runDue(ctx, originalAt.Add(10*time.Minute))
	s.sched.inflight.Wait()
	task, _ = taskByID(s, "fu_leader")
	if !task.LastRun.Equal(originalAt.Add(10*time.Minute)) || !task.At.Equal(originalAt.Add(3*time.Hour)) {
		t.Fatalf("the new accelerated run must not consume the next original run: %+v", task)
	}
}

func TestLeaderFollowupCadenceLeavesOtherSchedulesAlone(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	closed := now.Add(-time.Hour)
	tests := []struct {
		name    string
		leaders []string
		task    Task
		todo    Todo
		want    time.Time
		wantRun bool
	}{
		{name: "nonleader", leaders: []string{"leader"}, task: Task{ID: "task", Prompt: "inspect", Repeat: repeatInterval, EveryMinutes: 180,
			At: now.Add(3 * time.Hour), Enabled: true, SessionID: "other"}, todo: Todo{ID: "todo", Supervision: &Supervision{Supervisor: "other"}}, want: now.Add(3 * time.Hour)},
		{name: "leader removed from settings", task: Task{ID: "task", Prompt: "inspect", Repeat: repeatInterval, EveryMinutes: 180,
			At: now.Add(3 * time.Hour), Enabled: true, SessionID: "leader"}, todo: Todo{ID: "todo", Supervision: &Supervision{Supervisor: "leader"}}, want: now.Add(3 * time.Hour)},
		{name: "all supervised tasks closed", leaders: []string{"leader"}, task: Task{ID: "task", Prompt: "inspect", Repeat: repeatInterval, EveryMinutes: 180,
			At: now.Add(3 * time.Hour), Enabled: true, SessionID: "leader"}, todo: Todo{ID: "todo", DoneAt: &closed, Supervision: &Supervision{Supervisor: "leader"}}, want: now.Add(3 * time.Hour)},
		{name: "open todo without supervision", leaders: []string{"leader"}, task: Task{ID: "task", Prompt: "inspect", Repeat: repeatInterval, EveryMinutes: 180,
			At: now.Add(3 * time.Hour), Enabled: true, SessionID: "leader"}, todo: Todo{ID: "todo"}, want: now.Add(3 * time.Hour)},
		{name: "main conversation", leaders: []string{"leader"}, task: Task{ID: "task", Prompt: "inspect", Repeat: repeatInterval, EveryMinutes: 180,
			At: now.Add(3 * time.Hour), Enabled: true, SessionID: "main"}, todo: Todo{ID: "todo", Supervision: &Supervision{Supervisor: "main"}}, want: now.Add(3 * time.Hour)},
		{name: "once", leaders: []string{"leader"}, task: Task{ID: "task", Prompt: "inspect", Repeat: repeatOnce,
			At: now.Add(3 * time.Hour), Enabled: true, SessionID: "leader"}, todo: Todo{ID: "todo", Supervision: &Supervision{Supervisor: "leader"}}, want: now.Add(3 * time.Hour)},
		{name: "daily", leaders: []string{"leader"}, task: Task{ID: "task", Prompt: "inspect", Repeat: repeatDaily, Time: "09:00",
			Enabled: true, SessionID: "leader"}, todo: Todo{ID: "todo", Supervision: &Supervision{Supervisor: "leader"}}, want: time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)},
		{name: "another leader's open todo", leaders: []string{"leader", "other"}, task: Task{ID: "task", Prompt: "inspect", Repeat: repeatInterval, EveryMinutes: 180,
			At: now.Add(3 * time.Hour), Enabled: true, SessionID: "leader"}, todo: Todo{ID: "todo", Supervision: &Supervision{Supervisor: "other"}}, want: now.Add(3 * time.Hour)},
		{name: "no source conversation", leaders: []string{"leader"}, task: Task{ID: "task", Prompt: "inspect", Repeat: repeatInterval, EveryMinutes: 180,
			At: now.Add(3 * time.Hour), Enabled: true}, todo: Todo{ID: "todo", Supervision: &Supervision{Supervisor: "leader"}}, want: now.Add(3 * time.Hour)},
		{name: "disabled", leaders: []string{"leader"}, task: Task{ID: "task", Prompt: "inspect", Repeat: repeatInterval, EveryMinutes: 180,
			At: now.Add(3 * time.Hour), Enabled: false, SessionID: "leader"}, todo: Todo{ID: "todo", Supervision: &Supervision{Supervisor: "leader"}}, want: time.Time{}},
		{name: "eleven minute interval", leaders: []string{"leader"}, task: Task{ID: "task", Prompt: "inspect", Repeat: repeatInterval, EveryMinutes: 11,
			At: now.Add(11 * time.Minute), Enabled: true, SessionID: "leader"}, todo: Todo{ID: "todo", Supervision: &Supervision{Supervisor: "leader"}}, want: now.Add(10 * time.Minute), wantRun: true},
		{name: "ten minute interval", leaders: []string{"leader"}, task: Task{ID: "task", Prompt: "inspect", Repeat: repeatInterval, EveryMinutes: 10,
			At: now.Add(10 * time.Minute), Enabled: true, SessionID: "leader"}, todo: Todo{ID: "todo", Supervision: &Supervision{Supervisor: "leader"}}, want: now.Add(10 * time.Minute), wantRun: true},
		{name: "five minute interval", leaders: []string{"leader"}, task: Task{ID: "task", Prompt: "inspect", Repeat: repeatInterval, EveryMinutes: 5,
			At: now.Add(5 * time.Minute), Enabled: true, SessionID: "leader"}, todo: Todo{ID: "todo", Supervision: &Supervision{Supervisor: "leader"}}, want: now.Add(5 * time.Minute), wantRun: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeHub()
			f.settings = &Settings{Main: "main", Leaders: tt.leaders, Timezone: "UTC"}
			s := newStorelessService(t, f)
			tt.task.CreatedAt = now
			setTasks(s, tt.task)
			tt.todo.Agents = cadenceWorkingTodo(f, "").Agents
			putTodo(s, tt.todo)
			if got := s.nextDue(ctx, now); !got.Equal(tt.want) {
				t.Fatalf("next run: got %v, want %v", got, tt.want)
			}
			s.runDue(ctx, now.Add(10*time.Minute))
			s.sched.inflight.Wait()
			task, _ := taskByID(s, "task")
			if got := !task.LastRun.IsZero(); got != tt.wantRun {
				t.Fatalf("run at 10 minutes: got %v, want %v; task %+v", got, tt.wantRun, task)
			}
		})
	}
}

func TestLeaderFollowupCadenceViewsMatchTheScheduler(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	f := newFakeHub()
	f.settings = &Settings{Main: "main", Leaders: []string{"leader"}, Timezone: "UTC"}
	s := newStorelessService(t, f)
	originalAt := now.Add(3 * time.Hour)
	setTasks(s, Task{ID: "fu_leader", Prompt: "inspect", Repeat: repeatInterval, EveryMinutes: 180,
		At: originalAt, LastRun: now, Enabled: true, SessionID: "leader"})
	putTodo(s, cadenceWorkingTodo(f, "todo_open"))
	want := now.Add(10 * time.Minute)

	views := s.tasksView(ctx, nil)
	if len(views) != 1 || !views[0].NextRun.Equal(want) || !views[0].At.Equal(originalAt) || views[0].EveryMinutes != 180 {
		t.Fatalf("the API should show the accelerated next run and the original rule: %+v", views)
	}
	snapshots := s.taskViews(ctx)
	if len(snapshots) != 1 || !snapshots[0].At.Equal(want) || snapshots[0].EveryMinutes != 180 {
		t.Fatalf("the snapshot should show the same next run: %+v", snapshots)
	}
}

func TestLeaderFollowupCadenceStillHonorsUntil(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	originalAt := now.Add(3 * time.Hour)
	f := newFakeHub()
	f.settings = &Settings{Main: "main", Leaders: []string{"leader"}, Timezone: "UTC"}
	s := newStorelessService(t, f)
	setTasks(s, Task{ID: "fu_leader", Prompt: "inspect", Repeat: repeatInterval, EveryMinutes: 180,
		At: originalAt, Until: now.Add(5 * time.Minute), Enabled: true, SessionID: "leader"})
	putTodo(s, cadenceWorkingTodo(f, "todo_open"))

	s.runDue(ctx, now.Add(5*time.Minute))
	s.sched.inflight.Wait()
	task, _ := taskByID(s, "fu_leader")
	if task.Enabled || task.LastStatus != runSkipped || !task.At.Equal(originalAt) || task.EveryMinutes != 180 {
		t.Fatalf("the end time must win over the accelerated cadence: %+v", task)
	}
	if got := s.nextDue(ctx, now.Add(5*time.Minute)); !got.IsZero() {
		t.Fatalf("an ended task has no next run: %v", got)
	}
}

func TestLeaderFollowupCadenceRunPromptUsesTheEffectiveInterval(t *testing.T) {
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	f := readyHub()
	f.settings = &Settings{Main: "main", Leaders: []string{"leader"}, Timezone: "UTC"}
	s := newStorelessService(t, f)
	setTasks(s, Task{ID: "fu_leader", Title: "巡检", Prompt: "巡检", Repeat: repeatInterval, EveryMinutes: 180,
		At: now.Add(3 * time.Hour), Enabled: true, MachineID: "server", SessionID: "leader"})
	putTodo(s, cadenceWorkingTodo(f, "todo_open"))
	task, _ := taskByID(s, "fu_leader")
	if _, err := s.runTask(task, now, now, false, false); err != nil {
		t.Fatal(err)
	}
	text := f.lastSend(t)["text"].(string)
	if !strings.Contains(text, "每 10 分钟") || strings.Contains(text, "每 3 小时") ||
		!strings.Contains(text, "没有变化就立即结束，不做长汇报") {
		t.Fatalf("the run should be told its effective interval and to stop when nothing changed: %q", text)
	}
	if got := stripScheduleHeader(text[strings.Index(text, "⏰"):]); got != "巡检" {
		t.Fatalf("the run's own prompt must survive without accumulated schedule notes: %q", got)
	}
}

func TestLeaderFollowupCadenceWithoutRunHistoryRunsNow(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	originalAt := now.Add(3 * time.Hour)
	f := newFakeHub()
	f.settings = &Settings{Main: "main", Leaders: []string{"leader"}, Timezone: "UTC"}
	s := newStorelessService(t, f)
	setTasks(s, Task{ID: "fu_leader", Prompt: "inspect", Repeat: repeatInterval, EveryMinutes: 180,
		At: originalAt, Enabled: true, SessionID: "leader"})
	putTodo(s, cadenceWorkingTodo(f, "todo_open"))

	if got := s.nextDue(ctx, now); !got.Equal(now) {
		t.Fatalf("an existing task with no timing history should run now, not postpone its deadline: %v", got)
	}
	s.runDue(ctx, now)
	s.sched.inflight.Wait()
	task, _ := taskByID(s, "fu_leader")
	if !task.LastRun.Equal(now) || !task.At.Equal(originalAt) {
		t.Fatalf("the immediate run must keep the original next run: %+v", task)
	}
	if got := s.nextDue(ctx, now); !got.Equal(now.Add(10 * time.Minute)) {
		t.Fatalf("the next run should use the recorded run time: %v", got)
	}
}

func TestLeaderFollowupCadenceRequiresActiveAgents(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	wake := now.Add(time.Hour)
	stopped := proto.Session{SessionID: "agent", Status: proto.StatusIdle, State: proto.StateIdle}
	working := proto.Session{SessionID: "agent", Status: proto.StatusWorking, State: proto.StateWorking}
	tests := []struct {
		name                                                string
		session                                             proto.Session
		review                                              string
		delivery                                            *TodoDelivery
		released, dismissed, absent, noAgent, daily, active bool
	}{
		{name: "working", session: working, active: true},
		{name: "background command", session: proto.Session{SessionID: "agent", Status: proto.StatusIdle, State: proto.StateWorking, BackgroundWorking: true, BackgroundWaiting: true}, active: true},
		{name: "hub wake", session: proto.Session{SessionID: "agent", Status: proto.StatusIdle, State: proto.StateIdle, Work: &proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitExternal, Wait: "CI", WakeAt: &wake}}, active: true},
		{name: "user question", session: proto.Session{SessionID: "agent", Status: proto.StatusBlocked, State: proto.StateNeedsYou}},
		{name: "awaits reply", session: proto.Session{SessionID: "agent", Status: proto.StatusDone, State: proto.StateUnread, AwaitsReply: true}},
		{name: "user work", session: proto.Session{SessionID: "agent", Status: proto.StatusIdle, State: proto.StateIdle, Work: &proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitUser, WakeAt: &wake}}},
		{name: "acceptance", session: stopped, review: reviewNeedsUser},
		{name: "pending review", session: stopped, review: reviewPending},
		{name: "merged delivery gate", session: stopped, delivery: &TodoDelivery{State: deliveryMerged, Gate: "Await deployment approval", Unblock: unblockUser}},
		{name: "stopped", session: stopped},
		{name: "unread", session: proto.Session{SessionID: "agent", Status: proto.StatusDone, State: proto.StateUnread}},
		{name: "closed", session: proto.Session{SessionID: "agent", Status: proto.StatusOffline, State: proto.StateOffline}},
		{name: "missing session", session: working, absent: true},
		{name: "no agents", session: working, noAgent: true},
		{name: "released", session: working, released: true},
		{name: "dismissed", session: working, dismissed: true},
		{name: "daily working", session: working, daily: true},
		{name: "daily stopped", session: stopped, daily: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeHub()
			f.settings = &Settings{Main: "main", Leaders: []string{"leader"}, Timezone: "UTC"}
			s := newStorelessService(t, f)
			task := Task{ID: "fu_leader", Prompt: "inspect", Repeat: repeatInterval, EveryMinutes: 180, At: now.Add(3 * time.Hour), CreatedAt: now, Enabled: true, SessionID: "leader"}
			want := now.Add(3 * time.Hour)
			if tt.active {
				want = now.Add(10 * time.Minute)
			}
			if tt.daily {
				task.Repeat, task.Time = repeatDaily, "09:00"
				want = now.Add(9 * time.Hour)
			}
			setTasks(s, task)
			a := TodoAgent{MachineID: "box", SessionID: "agent"}
			if tt.released {
				a.ReleasedAt = &now
			}
			if tt.dismissed {
				a.DismissedAt = &now
			}
			todo := Todo{ID: "todo", Supervision: &Supervision{Supervisor: "leader", State: tt.review}, Delivery: tt.delivery, Agents: []TodoAgent{a}}
			if tt.noAgent {
				todo.Agents = nil
			}
			putTodo(s, todo)
			if !tt.absent {
				f.setSessions("box", tt.session)
			}
			if got := s.nextDue(ctx, now); !got.Equal(want) {
				t.Errorf("next run = %v, want %v", got, want)
			}
			views, snapshots := s.tasksView(ctx, nil), s.taskViews(ctx)
			if !tt.daily && (len(views) != 1 || !views[0].NextRun.Equal(want) || len(snapshots) != 1 || !snapshots[0].At.Equal(want)) {
				t.Errorf("views disagree: %+v / %+v; want %v", views, snapshots, want)
			}
			s.runDue(ctx, now.Add(10*time.Minute))
			s.sched.inflight.Wait()
			after, _ := taskByID(s, task.ID)
			if got := !after.LastRun.IsZero(); got != tt.active {
				t.Errorf("ran at ten minutes = %v, want %v", got, tt.active)
			}
			if !after.At.Equal(task.At) || after.EveryMinutes != 180 {
				t.Errorf("original rule changed: %+v", after)
			}
		})
	}
}

func TestLeaderFollowupCadenceMixedAgentsRestoreOriginalInterval(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	f := newFakeHub()
	f.settings = &Settings{Main: "main", Leaders: []string{"leader"}, Timezone: "UTC"}
	s := newStorelessService(t, f)
	setTasks(s, Task{ID: "fu_leader", Prompt: "inspect", Repeat: repeatInterval, EveryMinutes: 180, At: now.Add(3 * time.Hour), CreatedAt: now, Enabled: true, SessionID: "leader"})
	putTodo(s, Todo{ID: "user", Supervision: &Supervision{Supervisor: "leader"}, Agents: []TodoAgent{{MachineID: "box", SessionID: "user"}}})
	putTodo(s, Todo{ID: "mixed", Supervision: &Supervision{Supervisor: "leader"}, Agents: []TodoAgent{{MachineID: "box", SessionID: "stopped"}, {MachineID: "box", SessionID: "active"}}})
	for _, active := range []bool{true, false} {
		state := proto.StateIdle
		want := now.Add(3 * time.Hour)
		if active {
			state, want = proto.StateWorking, now.Add(10*time.Minute)
		}
		f.setSessions("box", proto.Session{SessionID: "user", State: proto.StateNeedsYou}, proto.Session{SessionID: "stopped", State: proto.StateIdle}, proto.Session{SessionID: "active", State: state})
		if got := s.nextDue(ctx, now); !got.Equal(want) {
			t.Fatalf("active=%v: next = %v, want %v", active, got, want)
		}
	}
}

func TestLeaderFollowupCadencePostgresRestoresAfterAgentsStop(t *testing.T) {
	ctx := context.Background()
	database := scheduleStore(t)
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	seedTodos(t, database, db.TodoRow{ID: "cadence_todo", Title: "Inspect work"})
	if err := database.PutTodoSupervision(ctx, db.TodoSupervisionRow{TodoID: "cadence_todo", Supervisor: "leader", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.LinkTodoAgentWithSource(ctx, "cadence_todo", "box", "agent", "Worker", "", "", "", "manual", "", now); err != nil {
		t.Fatal(err)
	}
	f := newFakeHub()
	f.settings = &Settings{Main: "main", Leaders: []string{"leader"}, Timezone: "UTC"}
	f.setSessions("box", proto.Session{SessionID: "agent", Status: proto.StatusWorking, State: proto.StateWorking})
	s := newServiceOn(t, f, database)
	sc := s.loadSchedule(ctx)
	sc.mu.Lock()
	sc.tasks = []Task{{ID: "fu_leader", Prompt: "inspect", Repeat: repeatInterval, EveryMinutes: 180, At: now.Add(3 * time.Hour), CreatedAt: now, Enabled: true, SessionID: "leader"}}
	err := s.saveLocked(sc, sc.tasks)
	sc.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	s.runDue(ctx, now.Add(10*time.Minute))
	s.sched.inflight.Wait()
	restarted := newServiceOn(t, f, database)
	if got := restarted.nextDue(ctx, now.Add(10*time.Minute)); !got.Equal(now.Add(20 * time.Minute)) {
		t.Fatalf("active after restart: %v", got)
	}

	wake := now.Add(time.Hour)
	for _, tt := range []struct {
		name    string
		session proto.Session
		active  bool
	}{
		{"working", proto.Session{Status: proto.StatusWorking, State: proto.StateWorking}, true},
		{"background command", proto.Session{Status: proto.StatusIdle, State: proto.StateWorking, BackgroundWorking: true, BackgroundWaiting: true}, true},
		{"hub wake", proto.Session{Status: proto.StatusIdle, State: proto.StateIdle, Work: &proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitExternal, Wait: "CI", WakeAt: &wake}}, true},
		{"user question", proto.Session{Status: proto.StatusBlocked, State: proto.StateNeedsYou}, false},
		{"stopped", proto.Session{Status: proto.StatusIdle, State: proto.StateIdle}, false},
		{"closed", proto.Session{Status: proto.StatusOffline, State: proto.StateOffline}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tt.session.SessionID = "agent"
			f.setSessions("box", tt.session)
			want := now.Add(3 * time.Hour)
			if tt.active {
				want = now.Add(20 * time.Minute)
			}
			views := restarted.tasksView(ctx, nil)
			if len(views) != 1 || !views[0].NextRun.Equal(want) {
				t.Errorf("next run after restart = %+v, want %v", views, want)
			}
		})
	}
	restarted.runDue(ctx, now.Add(20*time.Minute))
	restarted.sched.inflight.Wait()
	task, _ := taskByID(restarted, "fu_leader")
	if !task.LastRun.Equal(now.Add(10*time.Minute)) || !task.At.Equal(now.Add(3*time.Hour)) || task.EveryMinutes != 180 {
		t.Fatalf("restored persisted rule: %+v", task)
	}
}

func cadenceWorkingTodo(f *fakeHub, id string) Todo {
	f.setSessions("box", proto.Session{SessionID: "agent", Status: proto.StatusWorking, State: proto.StateWorking})
	return Todo{ID: id, Supervision: &Supervision{Supervisor: "leader"}, Agents: []TodoAgent{{MachineID: "box", SessionID: "agent"}}}
}
