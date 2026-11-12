package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// The to-do list as it grows: what GET /api/v1/todos and the supervision pass cost with n finished supervised tasks
// (#5995), and how many {"type":"todos"} events one task's life sends the apps.

// manyTodos is a manager holding n finished supervised tasks from the main conversation, each on a brief of its own,
// except that every fourth one took over the one before it (two to-dos on one brief, a lineage of two steps). All its
// times are fixed: the same n gives the same list, byte for byte.
func manyTodos(n int) *Service {
	s := New(Options{Hub: newFakeHub(), Logf: func(string, ...any) {}})
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	b := &s.todos
	b.mu.Lock()
	b.todos = make(map[string]*Todo, n)
	b.activeBySession, b.spacesByKey = map[[2]string]string{}, map[string]string{}
	b.loaded = true
	tb := &s.tbriefs
	tb.mu.Lock()
	tb.init()
	brief := ""
	for i := range n {
		id := fmt.Sprintf("todo_bench%06d", i)
		at := base.Add(time.Duration(i) * time.Minute)
		done := at.Add(40 * time.Minute)
		b.todos[id] = &Todo{ID: id, Title: fmt.Sprintf("fix the login crash %d", i), Notes: "the login page crashes in landscape",
			CreatedAt: at, UpdatedAt: done, DoneAt: &done, Origin: "conv1", DoneBy: "review", DoneReason: "merged and covered by a UI test",
			Spaces: []TodoSpace{}, Stage: "merge", PRURL: fmt.Sprintf("https://github.com/o/r/pull/%d", i), StageKind: "code",
			Agents: []TodoAgent{{MachineID: "box", SessionID: fmt.Sprintf("s%06d", i), LinkedAt: at, Title: "fix the login crash",
				Cwd: "/code/app", Agent: "claude", Source: "plan"}},
			Check: &TodoCheck{Verdict: "done", Reason: "PR merged, test added", At: done, WorkSince: at},
			Supervision: &Supervision{Supervisor: "conv1", DoneWhen: []string{"PR merged to main", "a test covers landscape"},
				ExpectMinutes: 30, State: reviewAccepted, UpdatedAt: done, Reviews: []TaskReview{{At: done, Verdict: reviewAccepted,
					Note: "merged and covered by a UI test", Checks: []ReviewCheck{{Criterion: "PR merged to main", Met: true,
						Evidence: "merged at 3f2c1ab"}, {Criterion: "a test covers landscape", Met: true, Evidence: "LoginLandscapeUITests"}}}}}}
		link := taskBriefLink{At: at}
		if i%4 == 3 {
			link.Brief, link.Continues, link.Reason = brief, fmt.Sprintf("todo_bench%06d", i-1), "the first agent stopped"
		} else {
			brief = fmt.Sprintf("brief_bench%06d", i)
			link.Brief = brief
			req := &BriefRequest{Text: "the login page crashes in landscape on the iPad, please fix it and add a test", At: at}
			tb.briefs[brief] = &taskBrief{rec: briefRecord{ID: brief, CreatedAt: at, StartedBy: briefByOwner, Conversation: "conv1",
				Request: req, Goal: "the login page no longer crashes in landscape",
				DoneWhen: []string{"PR merged to main", "a test covers landscape"}, ExpectMinutes: 30}}
		}
		tb.links[id] = link
	}
	tb.mu.Unlock()
	b.mu.Unlock()
	return s
}

func todosListBody(tb testing.TB, s *Service) []byte {
	r := httptest.NewRequest("GET", "/api/v1/todos", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 {
		tb.Fatalf("GET /api/v1/todos = %d %s", w.Code, w.Body.String())
	}
	return w.Body.Bytes()
}

// BenchmarkTodosList: GET /api/v1/todos with n finished supervised tasks.
func BenchmarkTodosList(b *testing.B) {
	for _, n := range []int{250, 1000, 4000} {
		b.Run(fmt.Sprintf("todos=%d", n), func(b *testing.B) {
			s := manyTodos(n)
			b.ReportAllocs()
			for b.Loop() {
				todosListBody(b, s)
			}
		})
	}
}

// BenchmarkSuperviseWatch: one pass of the supervision loop (every superviseTick) over n finished supervised tasks.
func BenchmarkSuperviseWatch(b *testing.B) {
	for _, n := range []int{250, 4000} {
		b.Run(fmt.Sprintf("todos=%d", n), func(b *testing.B) {
			s := manyTodos(n)
			ctx, now := context.Background(), time.Now()
			b.ReportAllocs()
			for b.Loop() {
				s.superviseWatch(ctx, now)
			}
		})
	}
}

// Every to-do in the list carries the brief and lineage it has on its own (GET /api/v1/todos/{id} reads them one by
// one), take-overs included.
func TestTodosListLineageAsEachHasIt(t *testing.T) {
	s := manyTodos(40)
	var list struct {
		Todos []Todo `json:"todos"`
	}
	if err := json.Unmarshal(todosListBody(t, s), &list); err != nil || len(list.Todos) != 40 {
		t.Fatalf("list: %d to-dos, %v", len(list.Todos), err)
	}
	steps := 0
	for _, got := range list.Todos {
		raw, err := s.todoView(context.Background(), got.ID)
		if err != nil {
			t.Fatal(err)
		}
		var one Todo
		if err := json.Unmarshal(raw, &one); err != nil {
			t.Fatal(err)
		}
		a, _ := json.Marshal(struct {
			B *TaskBrief
			L []LineageStep
		}{got.Brief, got.Lineage})
		w, _ := json.Marshal(struct {
			B *TaskBrief
			L []LineageStep
		}{one.Brief, one.Lineage})
		if string(a) != string(w) {
			t.Fatalf("%s in the list:\n%s\non its own:\n%s", got.ID, a, w)
		}
		if got.Brief == nil {
			t.Fatalf("%s has no brief", got.ID)
		}
		steps += len(got.Lineage)
	}
	// 20 to-dos alone on their brief (one step each), 10 pairs on one (two steps for each of the two).
	if steps != 20+10*2*2 {
		t.Fatalf("%d lineage steps in all", steps)
	}
}

// ---- one task's {"type":"todos"} events ----

// taskStepEvents runs one supervised task through the real tools (task_plan with a brief, the agent's turns, one
// rework round, accepted) and answers how many {"type":"todos"} events the apps got for each step, each counted once
// the step's events stopped coming.
func taskStepEvents(tb testing.TB) (steps []string, events []int) {
	tb.Helper()
	s, f, _ := briefService(tb, "the login page crashes in landscape on the iPad, please fix it and add a test")
	presenceTimes(tb, 0, 0)
	f.judge = `{"verdict":"done","reason":"PR merged, test added"}`
	todosSent := func() int {
		f.mu.Lock()
		defer f.mu.Unlock()
		n := 0
		for _, e := range f.events {
			if e == "todos" {
				n++
			}
		}
		return n
	}
	last := 0
	step := func(name string) {
		// The step's writes go on in the background (the leader's link, the judge's answer): its events are in once
		// none came for a while.
		n, quiet := todosSent(), time.Now()
		for time.Since(quiet) < 400*time.Millisecond {
			time.Sleep(10 * time.Millisecond) // sleep-ok: an event count is final only after a quiet stretch
			if m := todosSent(); m != n {
				n, quiet = m, time.Now()
			}
		}
		steps, events = append(steps, name), append(events, n-last)
		last = n
	}
	if out, bad := tool(tb, s, "task_plan", briefTask(nil)); bad {
		tb.Fatalf("task_plan: %s", out)
	}
	waitFor(tb, "the link", func() bool { return len(onlyTodo(s).Agents) == 1 })
	todo := onlyTodo(s)
	f.setItems()
	step("task_plan: to-do made, agent started and linked")
	agentTurn(s, f, proto.StatusWorking, "", 0)
	step("the agent starts its turn")
	agentTurn(s, f, proto.StatusDone, "Fixed the crash. PR https://github.com/o/r/pull/901 is open with a landscape UI test.", 0)
	waitFor(tb, "pending review", func() bool { v := todoNow(s, todo.ID).Supervision; return v != nil && v.State == reviewPending })
	step("its turn ends: judged, waits for review")
	if out, bad := tool(tb, s, "task_review", map[string]any{"todo_id": todo.ID, "verdict": "rework", "note": "merge it",
		"checks": []ReviewCheck{{Criterion: "PR merged to main", Met: false, Evidence: "open"}}}); bad {
		tb.Fatalf("rework: %s", out)
	}
	step("task_review rework")
	agentTurn(s, f, proto.StatusWorking, "", 0)
	step("the agent starts its turn")
	agentTurn(s, f, proto.StatusDone, "Merged https://github.com/o/r/pull/901 into main.", 0)
	waitFor(tb, "pending review again", func() bool { return todoNow(s, todo.ID).Supervision.State == reviewPending })
	step("its turn ends: judged, waits for review")
	if out, bad := tool(tb, s, "task_review", map[string]any{"todo_id": todo.ID, "verdict": "accepted", "note": "merged"}); bad {
		tb.Fatalf("accepted: %s", out)
	}
	step("task_review accepted: to-do closed")
	if todoNow(s, todo.ID).DoneAt == nil {
		tb.Fatal("the task did not close")
	}
	return steps, events
}

// BenchmarkTodosEventsOneTask: the {"type":"todos"} events one supervised task sends every app (each one a full read
// of GET /api/v1/todos), as todos-events/op. Run with -benchtime=1x: one task takes a few seconds.
func BenchmarkTodosEventsOneTask(b *testing.B) {
	total := 0
	for b.Loop() {
		_, events := taskStepEvents(b)
		for _, n := range events {
			total += n
		}
	}
	b.ReportMetric(float64(total)/float64(b.N), "todos-events/op")
}

// Each step of a task changes the list once as the apps see it, and sends them one {"type":"todos"} event: the writes
// of a step (the to-do, its report, its check, its supervision, its leader) are told together.
func TestTodosEventsOnePerTaskStep(t *testing.T) {
	steps, events := taskStepEvents(t)
	for i, n := range events {
		t.Logf("  %-48s %d", steps[i], n)
		if n != 1 {
			t.Errorf("%s: %d todos events, want 1", steps[i], n)
		}
	}
}

// The {"type":"todos"} of a write waits todosSettle for the rest of it; a change from the moment it goes out, even
// while it does, sends another: every change is followed by an event sent after it.
func TestTodosEventWaitsForTheRestOfTheWrite(t *testing.T) {
	f := newFakeHub()
	s := New(Options{Hub: f, Logf: testLogf(t)})
	clock := newFakeClock()
	s.tpush.after = clock.After
	sent := func() int {
		f.mu.Lock()
		defer f.mu.Unlock()
		n := 0
		for _, e := range f.events {
			if e == "todos" {
				n++
			}
		}
		return n
	}
	ctx := context.Background()
	for range 5 {
		s.todosChanged(ctx)
	}
	if n := sent(); n != 0 {
		t.Fatalf("%d todos events before the write settled, want 0", n)
	}
	clock.advance(todosSettle - time.Millisecond)
	if n := sent(); n != 0 {
		t.Fatalf("%d todos events before todosSettle, want 0", n)
	}
	clock.advance(time.Millisecond)
	if n := sent(); n != 1 {
		t.Fatalf("five changes in one write sent %d todos events, want 1", n)
	}
	clock.advance(time.Hour)
	if n := sent(); n != 1 {
		t.Fatalf("%d todos events with nothing changed since, want 1", n)
	}

	// A change made while the event goes out (the hub's Broadcast running) is told by one of its own after it.
	s.todosChanged(ctx)
	f.mu.Lock()
	f.onBroadcast = func() { s.todosChanged(ctx) }
	f.mu.Unlock()
	clock.advance(todosSettle)
	f.mu.Lock()
	f.onBroadcast = nil
	f.mu.Unlock()
	if n := sent(); n != 2 {
		t.Fatalf("%d todos events, want 2", n)
	}
	if clock.pending() != 1 {
		t.Fatalf("%d events waiting after a change made while one went out, want 1", clock.pending())
	}
	clock.advance(todosSettle)
	if n := sent(); n != 3 {
		t.Fatalf("%d todos events, want 3", n)
	}
}
