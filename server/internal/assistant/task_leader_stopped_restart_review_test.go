package assistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// A worker's completed turn is still undelivered when its stopped leader refuses
// the first send. A manager restart must not consume the persisted turn signal.
func TestStoppedLeaderReviewSurvivesManagerRestart(t *testing.T) {
	ctx := context.Background()
	database := &profileTaskStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}
	s, f := newQuietServiceOn(t, database)
	s.leaderResume = time.Hour // keep the in-memory retry out of the restart assertion
	f.judge = `{"verdict":"done","reason":"PR 已合并"}`
	startTaskLeader(t, s, f)
	w := planWorker(t, s, f, nil)
	worker := w.Agents[0].SessionID
	// Initialize the persisted turn ledger before the worker's turn, as the
	// manager's normal supervision sweep does.
	s.superviseWatch(ctx, time.Now())

	f.setSessions("box",
		proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusIdle},
		proto.Session{SessionID: worker, MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusWorking, State: proto.StateWorking, UpdatedAt: time.Now()})
	s.taskWorking(ctx, "box", worker)
	stopLeader(f, worker)
	f.setSessions("box",
		proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusInterrupted, Capabilities: &proto.SessionCapabilities{Read: true, Terminal: true}, UpdatedAt: time.Now()},
		proto.Session{SessionID: worker, MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusDone, State: proto.StateUnread, LastText: "做完了，PR https://github.com/o/r/pull/7 已合并", UpdatedAt: time.Now()})
	s.taskTurnEnded(ctx, "box", worker)
	turnsQuiet(t, s, w.ID)
	waitFor(t, "the stopped leader to refuse the review", func() bool { return len(sendsTo(f, "box", "s1")) > 0 })
	// Wait until the digest that made the refused RPC has either rolled its
	// reservation back (old behavior) or consumed it (new behavior).
	s.supervise.dedupMu.Lock()
	s.supervise.dedupMu.Unlock()
	if got := deliveredTo(f, "s1"); len(got) != 0 {
		t.Fatalf("setup: the refused leader received a message: %q", got)
	}

	// Stop the old manager's digest timer, leaving its process-local retry and
	// pending queue behind just as a process restart would.
	s.supervise.mu.Lock()
	if s.supervise.timer != nil {
		s.supervise.timer.Stop()
		s.supervise.timer = nil
	}
	s.supervise.mu.Unlock()
	s.supervise.dedupMu.Lock()
	s.supervise.dedupMu.Unlock()
	beforeTurn, beforeOK := s.turnOf(ctx, w.ID)
	s.supervise.mu.Lock()
	beforePending, beforePendingOK := s.supervise.pending[w.ID+"|review"]
	beforeSent := s.supervise.sent[w.ID+"|review"]
	s.supervise.mu.Unlock()
	t.Logf("before restart: turn=%+v ok=%v signal=%+v pending=%+v pendingOK=%v sent=%q", beforeTurn, beforeOK, beforeTurn.Signal, beforePending, beforePendingOK, beforeSent)

	restarted, hub := restartedOn(t, database)
	// profileTaskStore persists settings and plans but not the todo tables.
	// Seed the new manager with the same todo rows so this fixture exercises
	// the persisted turn and deduplication ledgers.
	s.todos.mu.RLock()
	restarted.todos.todos = make(map[string]*Todo, len(s.todos.todos))
	for id, row := range s.todos.todos {
		cloned := copyTodoLocked(row)
		restarted.todos.todos[id] = &cloned
	}
	restarted.todos.loaded = true
	s.todos.mu.RUnlock()
	hub.setSessions("box",
		proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusIdle, UpdatedAt: time.Now()},
		proto.Session{SessionID: worker, MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusDone, State: proto.StateUnread, LastText: "做完了，PR https://github.com/o/r/pull/7 已合并", UpdatedAt: time.Now()})
	restarted.superviseWatch(ctx, time.Now())
	turnsQuiet(t, restarted, w.ID)
	afterTurn, afterOK := restarted.turnOf(ctx, w.ID)
	restarted.supervise.mu.Lock()
	afterPending, afterPendingOK := restarted.supervise.pending[w.ID+"|review"]
	afterSent := restarted.supervise.sent[w.ID+"|review"]
	restarted.supervise.mu.Unlock()
	t.Logf("after restart: turn=%+v ok=%v signal=%+v pending=%+v pendingOK=%v sent=%q supervision=%+v", afterTurn, afterOK, afterTurn.Signal, afterPending, afterPendingOK, afterSent, todoNow(restarted, w.ID).Supervision)
	waitFor(t, "the restarted manager to deliver the undelivered review", func() bool {
		for _, m := range deliveredTo(hub, "s1") {
			if strings.Contains(m, "waits for your review") && strings.Contains(m, w.ID) {
				return true
			}
		}
		return false
	})
}
