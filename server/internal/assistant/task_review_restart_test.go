package assistant

import (
	"context"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// A restored outbox may reach the main conversation before its node has listed the Agent.
// The next sweep reads updated_at from SQL. It is the same review the old JSON receipt identifies.
func TestRestoredTaskReviewBeforeFirstNodeList(t *testing.T) {
	for _, when := range []string{"pending", "delivered"} {
		t.Run(when, func(t *testing.T) {
			ctx := context.Background()
			store := taskRecoveryDatabase(t)
			s, f, todo := newSupervisedServiceOn(t, store)
			setSuperviseTimingsForTest(s, superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour})
			nodeListed(s, f, turnRow(proto.StatusIdle, 1, ""))
			sweeps(t, s, todo.ID, 1)
			done, page := turnRow(proto.StatusDone, 3, "fixed"), turnPage("turn-1", "fixed", true)
			nodeListed(s, f, turnRow(proto.StatusWorking, 2, ""))
			f.setItems(page...)
			nodeListed(s, f, done)
			wantTurns(t, "first judgment", turnCounts{receipts: 1, judgments: 1}, s, todo.ID, f)
			turnsQuiet(t, s, todo.ID)

			// A pre-upgrade manager used all of the observation's nanoseconds in its outbox and turn ledger.
			// Give it a real sub-microsecond value even on a clock that only supplies whole microseconds.
			entry, ok := s.turnOf(ctx, todo.ID)
			if !ok || entry.Signal == nil || entry.Signal.Kind != sigReview {
				t.Fatalf("missing judged review: %+v", entry.Signal)
			}
			at := todoNow(s, todo.ID).Supervision.UpdatedAt.Truncate(time.Microsecond).Add(time.Microsecond + 437*time.Nanosecond)
			v, _, err := s.updateSupervisionAt(ctx, todo.ID, at, func(*Supervision) bool { return true })
			if err != nil {
				t.Fatal(err)
			}
			old := superviseSignal{Todo: todo.ID, Kind: sigReview, Key: "review@" + v.UpdatedAt.UTC().Format(time.RFC3339Nano), Detail: entry.Signal.Detail}
			s.signal(old)
			if when == "delivered" {
				setSuperviseTimingsForTest(s, shortSuperviseTimings)
				s.sendDigest(ctx)
				wantTurns(t, "delivered before restart", oneEach, s, todo.ID, f)
			}
			stopCardlessManager(s)

			again, f2 := restartedOn(t, store)
			f2.setItems(page...)
			if err := again.ensureTodosLoaded(ctx); err != nil {
				t.Fatal(err)
			}
			loaded, ok := again.turnOf(ctx, todo.ID)
			if !ok || loaded.Signal == nil || loaded.Signal.Key != old.Key || (loaded.Signal.Told != nil) != (when == "delivered") {
				t.Fatalf("wrong durable legacy signal after restart: %+v", loaded.Signal)
			}
			again.restoreSupervisePending(ctx)
			again.sendDigest(ctx)
			wantTurns(t, "before the node list", oneEach, again, todo.ID, f, f2)
			nodeListed(again, f2, done)
			again.superviseWatch(ctx, time.Now())
			again.TurnEnded(ctx, SessionRef{MachineID: "box", SessionID: "s1"})
			nodeListed(again, f2, done)
			again.superviseWatch(ctx, time.Now())
			stillTurns(t, "same review after the node list and turn end", oneEach, again, todo.ID, f, f2)
		})
	}
}

func TestOrdinaryTaskReviewKeyKeepsDistinctEvents(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 123456437, time.UTC)
	stored := at.Truncate(time.Microsecond)
	key := func(at time.Time) string { return reviewKey(&Supervision{UpdatedAt: at}) }
	if key(at) != key(stored) || key(at) == key(stored.Add(time.Microsecond)) {
		t.Fatal("review identity changed across SQL precision or merged the next review")
	}
	legacy := &turnSignal{Kind: sigReview, Key: "review@" + at.Format(time.RFC3339Nano)}
	for _, g := range []superviseSignal{
		{Kind: sigReview, Key: key(stored.Add(time.Microsecond))},
		{Kind: sigReview, Key: "review@" + at.Add(time.Nanosecond).Format(time.RFC3339Nano)},
		{Kind: sigReview, Key: key(stored), Recipient: "new-supervisor"},
		{Kind: sigReview, Key: key(stored), To: "other-conversation"},
		{Kind: sigReview, Key: externalPRReviewPrefix + stored.Format(time.RFC3339Nano)},
		{Kind: sigUnjudged, Key: key(stored)},
	} {
		if legacy.matches(g) {
			t.Errorf("legacy review matched distinct signal %+v", g)
		}
	}
}
