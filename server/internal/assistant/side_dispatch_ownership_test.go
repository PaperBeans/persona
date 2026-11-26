package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

type sideFeedStore struct {
	memStore
	fmu  sync.Mutex
	feed []db.FeedRow
}

func (m *sideFeedStore) AppendFeed(_ context.Context, r db.FeedRow) error {
	m.fmu.Lock()
	defer m.fmu.Unlock()
	m.feed = append(m.feed, r)
	return nil
}

func (m *sideFeedStore) Feed(_ context.Context, conversation string, since time.Time, limit int) ([]db.FeedRow, error) {
	m.fmu.Lock()
	defer m.fmu.Unlock()
	var out []db.FeedRow
	for _, r := range m.feed {
		if r.Conversation == conversation && (since.IsZero() || r.At.After(since)) {
			out = append(out, r)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (m *sideFeedStore) FeedItem(_ context.Context, id string) (*db.FeedRow, error) {
	m.fmu.Lock()
	defer m.fmu.Unlock()
	for _, r := range m.feed {
		if r.ID == id {
			cp := r
			return &cp, nil
		}
	}
	return nil, nil
}

// sideDispatchEnv creates a service with main chat conv1 and a live, titled side conversation side1.
func sideDispatchEnv(t *testing.T) (*Service, *fakeHub) {
	t.Helper()
	s, f := newQuietServiceOn(t, &sideFeedStore{})
	f.answerApp(AppFeishu, FeishuStatusGet, func(json.RawMessage) (any, error) {
		return FeishuStatus{PublicURL: "https://hub.example.com"}, nil
	})
	f.mu.Lock()
	f.hello.Locale = "en"
	f.known["side1"] = true
	f.known["side2"] = true
	f.mu.Unlock()
	f.setSessions("server",
		proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "side1", MachineID: "server", Assistant: true, Title: "Billing migration", Status: proto.StatusIdle},
		proto.Session{SessionID: "side2", MachineID: "server", Assistant: true, Title: "Other project", Status: proto.StatusIdle},
	)
	return s, f
}

// TestSideConversationAgentStartOwnedAndSignalledThere verifies that an agent_start from a live side conversation
// creates a task supervised by that side conversation and routes its signals back there, falling back to main only
// when the side conversation is gone, archived, or not an assistant conversation. It also verifies agent_supervise
// and agent_send(task) accept agents the side conversation started or tasks it already supervises.
func TestSideConversationAgentStartOwnedAndSignalledThere(t *testing.T) {
	s, f := sideDispatchEnv(t)
	f.startReply = func(_ map[string]any, sess *proto.Session) *proto.StartInstead {
		sess.SessionID = "agent1000001"
		return nil
	}
	ctx := context.Background()
	agentTasksOn(t, s)
	f.judge = `{"verdict":"done","reason":"tests pass"}`

	// Live side conversation is its own agentTaskSupervisor.
	if sup, ok := s.agentTaskSupervisor(ctx, "side1"); !ok || sup != "side1" {
		t.Fatalf("agentTaskSupervisor(side1) = %q, %v; want side1, true", sup, ok)
	}

	// agent_start from side1 creates a task owned and supervised by side1.
	startArgs := json.RawMessage(`{"machine":"box","cwd":"/code/app","prompt":"Fix billing timeout"}`)
	res, err := s.runTool(ctx, "server", "side1", "agent_start", startArgs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, startToldHere) {
		t.Fatalf("agent_start answer did not report local supervision: %s", res)
	}
	todo := onlyTodo(s)
	if todo.Origin != "side1" || todo.Supervision == nil || todo.Supervision.Supervisor != "side1" {
		t.Fatalf("task ownership = origin %q, supervision %+v; want side1", todo.Origin, todo.Supervision)
	}
	if got := feedKinds(s, "side1"); !strings.Contains(got, "task") {
		t.Fatalf("side1 feed = %q, want task card", got)
	}

	// Signals from the agent come back to side1, not conv1.
	agentSessionID := todo.Agents[0].SessionID
	f.setSessions("box", proto.Session{SessionID: agentSessionID, MachineID: "box", Cwd: "/code/app", Agent: "claude",
		Status: proto.StatusWorking, State: proto.StateWorking, UpdatedAt: time.Now()})
	s.taskWorking(ctx, "box", agentSessionID, startRow{})
	f.setSessions("box", proto.Session{SessionID: agentSessionID, MachineID: "box", Cwd: "/code/app", Agent: "claude",
		Status: proto.StatusDone, State: proto.StateUnread, LastText: "Fixed the timeout and added unit tests.", UpdatedAt: time.Now()})
	s.taskTurnEnded(ctx, "box", agentSessionID)
	waitFor(t, "supervision signal in side1", func() bool {
		return reviewsTold(f, "side1", todo.ID) == 1
	})
	if n := reviewsTold(f, "conv1", todo.ID); n != 0 {
		t.Fatalf("main chat received %d supervision message(s) for side1's task", n)
	}

	// agent_supervise accepts a task side1 already supervises and updates criteria without changing supervisor.
	f.setSessions("box", proto.Session{SessionID: agentSessionID, MachineID: "box", Cwd: "/code/app", Agent: "claude",
		Title: "Fix billing timeout", Status: proto.StatusIdle, State: proto.StateIdle, UpdatedAt: time.Now()})
	if _, err := runJSON(t, s, "side1", "agent_supervise", map[string]any{
		"machine": "box", "session_id": agentSessionID, "done_when": []string{"tests pass"},
	}); err != nil {
		t.Fatalf("side1 agent_supervise on own task failed: %v", err)
	}
	if v := todoNow(s, todo.ID).Supervision; v == nil || v.Supervisor != "side1" || len(v.DoneWhen) != 1 {
		t.Fatalf("supervision after side1 agent_supervise = %+v", v)
	}

	// agent_send(task) from side1 on its own task keeps side1 as supervisor.
	if _, err := runJSON(t, s, "side1", "agent_send", map[string]any{
		"machine": "box", "session_id": agentSessionID, "text": "Also check retries",
		"task": map[string]any{"done_when": []string{"tests pass", "retries checked"}},
	}); err != nil {
		t.Fatalf("side1 agent_send(task) on own task failed: %v", err)
	}
	if v := todoNow(s, todo.ID).Supervision; v == nil || v.Supervisor != "side1" || len(v.DoneWhen) != 2 {
		t.Fatalf("supervision after side1 agent_send(task) = %+v", v)
	}

	// agent_supervise from side1 refuses an unrelated manual agent it did not start.
	f.setSessions("box",
		proto.Session{SessionID: agentSessionID, MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusIdle},
		proto.Session{SessionID: "manual01-unrelated", MachineID: "box", Cwd: "/code/other", Agent: "claude", Status: proto.StatusIdle},
	)
	if _, err := runJSON(t, s, "side1", "agent_supervise", map[string]any{
		"machine": "box", "session_id": "manual01-unrelated",
	}); err == nil {
		t.Fatal("side1 adopted an unrelated manual agent via agent_supervise")
	}

	// Fall back to main when the conversation is gone, archived, or not an assistant conversation.
	if sup, ok := s.agentTaskSupervisor(ctx, "gone-side"); !ok || sup != "conv1" {
		t.Fatalf("agentTaskSupervisor(gone-side) = %q, %v; want conv1, true", sup, ok)
	}
	f.setSessions("server",
		proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "side1", MachineID: "server", Assistant: true, Title: "Billing migration", Status: proto.StatusIdle},
		proto.Session{SessionID: "archived-side", MachineID: "server", Assistant: true, Archived: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "not-assistant", MachineID: "server", Assistant: false, Status: proto.StatusIdle},
	)
	if sup, ok := s.agentTaskSupervisor(ctx, "archived-side"); !ok || sup != "conv1" {
		t.Fatalf("agentTaskSupervisor(archived-side) = %q, %v; want conv1, true", sup, ok)
	}
	if sup, ok := s.agentTaskSupervisor(ctx, "not-assistant"); !ok || sup != "conv1" {
		t.Fatalf("agentTaskSupervisor(not-assistant) = %q, %v; want conv1, true", sup, ok)
	}
	if _, err := s.runTool(ctx, "server", "archived-side", "agent_start", startArgs); err != nil {
		t.Fatal(err)
	}
	var archivedTodo Todo
	for _, td := range s.todosOf(ctx, "*") {
		if td.Origin == "archived-side" {
			archivedTodo = td
		}
	}
	if archivedTodo.Supervision == nil || archivedTodo.Supervision.Supervisor != "conv1" {
		t.Fatalf("archived side agent_start did not fall back to main: %+v", archivedTodo)
	}
}

// TestSideConversationSupervisionRoundPushesUserLevelOnly verifies that a side conversation's supervision round
// pushes the owner (with task and conversation links) for user-level signals and holds pushes for info-level signals.
func TestSideConversationSupervisionRoundPushesUserLevelOnly(t *testing.T) {
	setupFleet := func(t *testing.T) (*fleetTest, Todo) {
		t.Helper()
		ft := newFleetTestNoStore(t)
		fastDone(ft)
		ft.f.answerApp(AppFeishu, FeishuStatusGet, func(json.RawMessage) (any, error) {
			return FeishuStatus{PublicURL: "https://hub.example.com"}, nil
		})
		ft.f.hello.Assistant.MachineID = "mac"
		ft.f.hello.Locale = "en"
		ft.f.known["conv1"] = true
		ft.f.known["side1"] = true
		setSettings(t, ft.s, func(v *Settings) { v.MachineID, v.Main = "mac", "conv1" })
		main := proto.Session{SessionID: "conv1", MachineID: "mac", Assistant: true, Status: proto.StatusIdle}
		side := proto.Session{SessionID: "side1", MachineID: "mac", Assistant: true, Title: "Billing migration", Status: proto.StatusIdle}
		ft.f.setSessions("mac", main, side)
		ft.sessions("mac", main, side)
		ctx := context.Background()
		todo, err := ft.s.createTodo(ctx, "Fix billing timeout", "Fix it", "side1", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := ft.s.superviseTodo(ctx, todo.ID, "side1", []string{"tests pass"}, 30, "plan"); err != nil {
			t.Fatal(err)
		}
		return ft, todo
	}
	finishTurn := func(ft *fleetTest, conv, lastText string) {
		main := proto.Session{SessionID: "conv1", MachineID: "mac", Assistant: true, Status: proto.StatusIdle}
		working := proto.Session{SessionID: conv, MachineID: "mac", Assistant: true, Title: "Billing migration",
			Status: proto.StatusWorking, State: proto.StateWorking, UpdatedAt: time.Now()}
		ft.f.setSessions("mac", main, working)
		ft.sessions("mac", main, working)
		done := working
		done.Status, done.State, done.LastText = proto.StatusDone, proto.StateUnread, lastText
		done.UpdatedAt = time.Now().Add(time.Second)
		ft.f.setSessions("mac", main, done)
		ft.sessions("mac", main, done)
	}

	t.Run("user-level review pushes with task and conversation links", func(t *testing.T) {
		ft, todo := setupFleet(t)
		ctx := context.Background()
		v, _, err := ft.s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool {
			v.State = reviewPending
			return true
		})
		if err != nil {
			t.Fatal(err)
		}
		g := superviseSignal{Todo: todo.ID, Kind: sigReview, Key: reviewKey(&v), At: time.Now()}
		msg := relayMsg{From: superviseTool, FromTitle: "督办", Tool: superviseTool, Text: "Review ready", Signals: encodeDigestSignals([]superviseSignal{g}), At: time.Now()}
		if err := ft.s.deliverRelay(ctx, "mac", "side1", msg); err != nil {
			t.Fatal(err)
		}
		if ft.s.supervising("side1") {
			t.Fatal("user-level supervision round in side1 held pushes")
		}
		ft.f.mu.Lock()
		if len(ft.f.sends) == 0 {
			ft.f.mu.Unlock()
			t.Fatal("no DeviceAssistantSend call recorded")
		}
		lastSend := ft.f.sends[len(ft.f.sends)-1]
		ft.f.mu.Unlock()
		if lastSend[holdPushesParam] == true {
			t.Fatalf("deliverRelay set hold_pushes=true for side1 user-level signal: %+v", lastSend)
		}
		finishTurn(ft, "side1", "Billing timeout fix is ready for your approval.")
		got := ft.pushesOf(assistantwire.NotifyDone, 1, 2*time.Second)
		if len(got) != 1 || got[0].SessionID != "side1" {
			t.Fatalf("side1 user-level push = %+v, want 1 NotifyDone for side1", got)
		}
		wantTaskLink := "https://hub.example.com/todos/" + todo.ID
		wantConvLink := "https://hub.example.com/persona/side1"
		if !strings.Contains(got[0].Body, wantTaskLink) || !strings.Contains(got[0].Body, wantConvLink) {
			t.Fatalf("side1 push body lacks task/conversation links:\nbody=%q\nwant %q and %q", got[0].Body, wantTaskLink, wantConvLink)
		}
		if got[0].LongMS != 0 {
			t.Fatalf("side1 user-level supervision push LongMS = %d, want 0 (push regardless of turn length)", got[0].LongMS)
		}
	})

	t.Run("info-level signal stays unpushed", func(t *testing.T) {
		ft, todo := setupFleet(t)
		ctx := context.Background()
		g := superviseSignal{Todo: todo.ID, Kind: sigParkedSummary, Key: "2026-11-23|dep", To: "side1", At: time.Now()}
		msg := relayMsg{From: superviseTool, FromTitle: "督办", Tool: superviseTool, Text: "Daily parked summary", Signals: encodeDigestSignals([]superviseSignal{g}), At: time.Now()}
		if err := ft.s.deliverRelay(ctx, "mac", "side1", msg); err != nil {
			t.Fatal(err)
		}
		if !ft.s.supervising("side1") {
			t.Fatal("info-level supervision round in side1 did not hold pushes")
		}
		ft.f.mu.Lock()
		if len(ft.f.sends) == 0 {
			ft.f.mu.Unlock()
			t.Fatal("no DeviceAssistantSend call recorded")
		}
		lastSend := ft.f.sends[len(ft.f.sends)-1]
		ft.f.mu.Unlock()
		if lastSend[holdPushesParam] != true {
			t.Fatalf("deliverRelay did not set hold_pushes=true for side1 info-level signal: %+v", lastSend)
		}
		finishTurn(ft, "side1", "Daily parked summary acknowledged.")
		if got := ft.pushesOf(assistantwire.NotifyDone, 1, 300*time.Millisecond); len(got) != 0 {
			t.Fatalf("info-level supervision round pushed owner: %+v", got)
		}
	})
}

// TestMainChatShowsSideTaskWithOriginAndCannotReviewIt verifies that the main chat displays side-conversation tasks
// with their origin conversation title and link, SuperviseEntry carries origin attribution (including after handover),
// and main chat cannot call task_review on a task another conversation supervises.
func TestMainChatShowsSideTaskWithOriginAndCannotReviewIt(t *testing.T) {
	s, f := sideDispatchEnv(t)
	f.startReply = func(_ map[string]any, sess *proto.Session) *proto.StartInstead {
		sess.SessionID = "agent1000001"
		return nil
	}
	ctx := context.Background()
	startArgs := json.RawMessage(`{"machine":"box","cwd":"/code/app","prompt":"Fix billing timeout"}`)
	if _, err := s.runTool(ctx, "server", "side1", "agent_start", startArgs); err != nil {
		t.Fatal(err)
	}
	todo := onlyTodo(s)
	if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool {
		v.State = reviewPending
		return true
	}); err != nil {
		t.Fatal(err)
	}

	// Main chat's live snapshot shows the task with its origin conversation and link.
	s.snapshotStale()
	mainCtx := s.Context(ctx, ContextParams{MachineID: "server", SessionID: "conv1"}).Text
	wantConvLink := "https://hub.example.com/persona/side1"
	for _, want := range []string{todo.ID, "Billing migration", "side1", wantConvLink} {
		if !strings.Contains(mainCtx, want) {
			t.Fatalf("main chat snapshot lacks %q:\n%s", want, mainCtx)
		}
	}

	// SuperviseEntry carries origin conversation id, title, and link.
	entry, ok := s.digestEntry(ctx, superviseSignal{Todo: todo.ID, Kind: sigReview, Key: reviewKey(todoNow(s, todo.ID).Supervision)}, "https://hub.example.com")
	if !ok {
		t.Fatal("digestEntry returned ok=false")
	}
	if entry.OriginID != "side1" || entry.OriginTitle != "Billing migration" || entry.OriginLink != wantConvLink {
		t.Fatalf("SuperviseEntry origin = (%q, %q, %q), want (side1, Billing migration, %s)",
			entry.OriginID, entry.OriginTitle, entry.OriginLink, wantConvLink)
	}
	rendered := prompts.RenderSupervise(prompts.Supervise{Entries: []prompts.SuperviseEntry{entry}})
	for _, want := range []string{"Billing migration", "side1", wantConvLink} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered Supervise message lacks %q:\n%s", want, rendered)
		}
	}

	// Main chat and unrelated side2 CANNOT review a task supervised by side1.
	for _, reviewer := range []string{"conv1", "side2"} {
		for _, verdict := range []string{"accepted", "rework", "cancelled"} {
			args := map[string]any{"todo_id": todo.ID, "verdict": verdict, "note": "attempt by " + reviewer}
			if _, err := runJSON(t, s, reviewer, "task_review", args); err == nil {
				t.Fatalf("%s was allowed to task_review(%s) a task supervised by side1", reviewer, verdict)
			}
		}
	}
	if cur := todoNow(s, todo.ID); cur.DoneAt != nil || cur.Supervision.State != reviewPending {
		t.Fatalf("unauthorized review mutated task: done=%v supervision=%+v", cur.DoneAt, cur.Supervision)
	}

	// side1 CAN review and accept its own task.
	if _, err := runJSON(t, s, "side1", "task_review", map[string]any{
		"todo_id": todo.ID, "verdict": "accepted", "note": "Verified by side1",
	}); err != nil {
		t.Fatalf("side1 could not review its own task: %v", err)
	}
	if todoNow(s, todo.ID).DoneAt == nil {
		t.Fatal("side1 acceptance did not close the task")
	}
	_ = f
}

// TestGoneOrArchivedSideConversationMovesTaskToMainOnceWithNotice verifies that when a side conversation is archived
// or gone, its open tasks move to main chat's follow-up exactly once, keep their origin attribution, and notify the
// owner explaining why.
func TestGoneOrArchivedSideConversationMovesTaskToMainOnceWithNotice(t *testing.T) {
	for _, mode := range []string{"archived", "gone"} {
		t.Run(mode, func(t *testing.T) {
			s, f := sideDispatchEnv(t)
			ctx := context.Background()
			startArgs := json.RawMessage(`{"machine":"box","cwd":"/code/app","prompt":"Fix billing timeout"}`)
			if _, err := s.runTool(ctx, "server", "side1", "agent_start", startArgs); err != nil {
				t.Fatal(err)
			}
			todo := onlyTodo(s)
			if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool {
				v.State = reviewPending
				return true
			}); err != nil {
				t.Fatal(err)
			}

			switch mode {
			case "archived":
				if _, err := s.setArchived(ctx, "server", "side1", true, titleByUser); err != nil {
					t.Fatal(err)
				}
			case "gone":
				// Remove side1 from server's session list while conv1 remains listed.
				f.setSessions("server",
					proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
				)
			}

			now := time.Now()
			s.superviseWatch(ctx, now)

			cur := todoNow(s, todo.ID)
			if cur.Origin != "side1" {
				t.Fatalf("handover lost Origin: got %q, want side1", cur.Origin)
			}
			if cur.Supervision == nil || cur.Supervision.Supervisor != "conv1" {
				t.Fatalf("task was not handed over to main: supervision=%+v", cur.Supervision)
			}

			// Notice in main chat feed and push explaining why, once.
			waitFor(t, "handover push notice", func() bool {
				f.mu.Lock()
				defer f.mu.Unlock()
				for _, p := range f.pushes {
					if p.SessionID == "conv1" && strings.Contains(p.Body, todo.Title) {
						return true
					}
				}
				return false
			})
			countMainNotices := func() int {
				items, _, _ := s.feed(ctx, "conv1", time.Time{})
				n := 0
				for _, it := range items {
					if it.Kind == feedNotice && strings.Contains(string(it.Body), todo.Title) {
						n++
					}
				}
				return n
			}
			if n := countMainNotices(); n != 1 {
				t.Fatalf("main feed handover notices = %d, want 1", n)
			}

			// Main chat receives the review signal WITH origin attribution preserved.
			waitFor(t, "main review signal after handover", func() bool {
				return reviewsTold(f, "conv1", todo.ID) == 1
			})
			mainNotice := strings.Join(f.noticesTo("conv1"), "\n")
			if !strings.Contains(mainNotice, "side1") || !strings.Contains(mainNotice, "https://hub.example.com/persona/side1") {
				t.Fatalf("handover supervision message lost origin attribution:\n%s", mainNotice)
			}

			// Repeated watches must NOT move or notify a second time.
			s.superviseWatch(ctx, now.Add(time.Minute))
			s.superviseWatch(ctx, now.Add(2*time.Minute))
			s.sendDigest(ctx)
			if n := countMainNotices(); n != 1 {
				t.Fatalf("repeated watches created %d handover notices, want 1", n)
			}
			if n := reviewsTold(f, "conv1", todo.ID); n != 1 {
				t.Fatalf("repeated watches sent %d review signals, want 1", n)
			}

			// After handover, side1 can no longer review; main can accept and close.
			if _, err := runJSON(t, s, "side1", "task_review", map[string]any{
				"todo_id": todo.ID, "verdict": "accepted", "note": "late side1 review",
			}); err == nil {
				t.Fatal("side1 reviewed the task after handover to main")
			}
			if _, err := runJSON(t, s, "conv1", "task_review", map[string]any{
				"todo_id": todo.ID, "verdict": "accepted", "note": "main verified after handover",
			}); err != nil {
				t.Fatalf("main could not review after handover: %v", err)
			}
			if todoNow(s, todo.ID).DoneAt == nil {
				t.Fatal("main acceptance after handover did not close task")
			}
		})
	}
}

// TestExactlyOneConversationReviewsTaskDuringHandover verifies that across concurrent review attempts from the side
// conversation and the main conversation racing with a handover, at most one conversation succeeds in reviewing.
func TestExactlyOneConversationReviewsTaskDuringHandover(t *testing.T) {
	ctx := context.Background()
	for iter := range 20 {
		s, f := sideDispatchEnv(t)
		startArgs := json.RawMessage(`{"machine":"box","cwd":"/code/app","prompt":"Fix billing timeout"}`)
		if _, err := s.runTool(ctx, "server", "side1", "agent_start", startArgs); err != nil {
			t.Fatal(err)
		}
		todo := onlyTodo(s)
		if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool {
			v.State = reviewPending
			return true
		}); err != nil {
			t.Fatal(err)
		}

		// Mark side1 gone so handover races with both reviewers.
		f.setSessions("server",
			proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		)

		gate := make(chan struct{})
		var sideOK, mainOK atomic.Int32
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			<-gate
			if _, err := runJSON(t, s, "side1", "task_review", map[string]any{
				"todo_id": todo.ID, "verdict": "accepted", "note": "side1 race",
			}); err == nil {
				sideOK.Add(1)
			}
		}()
		go func() {
			defer wg.Done()
			<-gate
			s.superviseWatch(ctx, time.Now())
		}()
		go func() {
			defer wg.Done()
			<-gate
			if _, err := runJSON(t, s, "conv1", "task_review", map[string]any{
				"todo_id": todo.ID, "verdict": "accepted", "note": "conv1 race",
			}); err == nil {
				mainOK.Add(1)
			}
		}()
		close(gate)
		wg.Wait()

		total := sideOK.Load() + mainOK.Load()
		if total > 1 {
			t.Fatalf("iter %d: both conversations reviewed the task (side=%d, main=%d)", iter, sideOK.Load(), mainOK.Load())
		}
		// If neither succeeded during the race (e.g. conv1 ran before handover and side1 ran after handover),
		// exactly one of them (now conv1) must still be able to review, and the other must be refused.
		if total == 0 {
			if _, err := runJSON(t, s, "side1", "task_review", map[string]any{
				"todo_id": todo.ID, "verdict": "accepted", "note": "side1 after handover",
			}); err == nil {
				t.Fatalf("iter %d: side1 succeeded after handover", iter)
			}
			if _, err := runJSON(t, s, "conv1", "task_review", map[string]any{
				"todo_id": todo.ID, "verdict": "accepted", "note": "conv1 after handover",
			}); err != nil {
				t.Fatalf("iter %d: conv1 failed after handover: %v", iter, err)
			}
		}
		cur := todoNow(s, todo.ID)
		if cur.DoneAt == nil {
			t.Fatalf("iter %d: task was not closed", iter)
		}
		accepted := 0
		for _, r := range cur.Supervision.Reviews {
			if r.Verdict == reviewAccepted {
				accepted++
			}
		}
		if accepted != 1 {
			t.Fatalf("iter %d: recorded %d accepted reviews, want 1: %+v", iter, accepted, cur.Supervision.Reviews)
		}
	}
}
