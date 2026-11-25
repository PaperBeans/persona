package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

type scheduledRetryWorkStore struct {
	*delayedWaitTestStore
	workStore
	failRead atomic.Bool
}

func (st *scheduledRetryWorkStore) SessionWorks(ctx context.Context, since time.Time) ([]db.SessionWork, error) {
	if st.failRead.Load() {
		return nil, errors.New("session work unavailable")
	}
	return st.workStore.SessionWorks(ctx, since)
}

func TestRestoredScheduledRetryLoadsPendingWake(t *testing.T) {
	ctx := context.Background()
	st := &scheduledRetryWorkStore{delayedWaitTestStore: &delayedWaitTestStore{Store: &memStore{}}, workStore: &workMemStore{}}
	first, node, w := newDelayedRetryFixture(t, st)
	wakeAt := time.Now().Add(time.Minute)
	first.putWork(ctx, db.SessionWork{MachineID: "server", SessionID: "s1", TurnAt: w.Retry.LimitTurn, WakeAt: &wakeAt,
		Work: proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitTransient, TurnAt: w.Retry.LimitTurn, WakeAt: &wakeAt}})

	next := newServiceOn(t, node, st)
	if !next.restoreDelayedWaits(ctx) {
		t.Fatal("failed to restore delayed waits")
	}
	waitFor(t, "the scheduled retry to leave the backlog", func() bool {
		doc, err := next.readDelayedWaits(ctx)
		return err == nil && len(doc.Waits) == 0
	})
	if got := node.sent(); got != 0 {
		t.Fatalf("scheduled prompts with a persisted transient wake = %d, want 0", got)
	}
}

func TestRestoredScheduledRetryRearmsOnWorkReadError(t *testing.T) {
	for _, phase := range []string{"before claim", "after claim"} {
		t.Run(phase, func(t *testing.T) {
			ctx := context.Background()
			st := &scheduledRetryWorkStore{delayedWaitTestStore: &delayedWaitTestStore{Store: &memStore{}}, workStore: &workMemStore{}}
			_, node, w := newDelayedRetryFixture(t, st)
			next := newServiceOn(t, node, st)
			if phase == "after claim" {
				// The first validation sees a working turn. It ends before cancellation is revalidated under the claim.
				var firstRead atomic.Bool
				firstRead.Store(true)
				node.answerNode(DeviceSessionGet, func(string, json.RawMessage) (any, error) {
					status := proto.StatusDone
					if firstRead.Swap(false) {
						status = proto.StatusWorking
					}
					return map[string]any{"ok": true, "session": proto.Session{SessionID: "s1", MachineID: "server", Status: status, UpdatedAt: w.Retry.LimitTurn}}, nil
				})
			}
			st.failRead.Store(true)
			next.runScheduledLimitRetry(ctx, w, true)
			doc, err := next.readDelayedWaits(ctx)
			if err != nil || len(doc.Waits) != 1 || doc.Waits[0].ID != w.ID || doc.Waits[0].Claim != "" || !doc.Waits[0].At.After(time.Now()) || node.sent() != 0 {
				t.Fatalf("unreadable work: waits=%+v, error=%v, sends=%d; want one rearmed, unclaimed wait and no sends", doc.Waits, err, node.sent())
			}
			st.failRead.Store(false)
			<-time.After(time.Until(doc.Waits[0].At))
			waitFor(t, "the recovered retry to complete", func() bool {
				doc, err := next.readDelayedWaits(ctx)
				return err == nil && len(doc.Waits) == 0
			})
			doc, err = next.readDelayedWaits(ctx)
			if err != nil || len(doc.Waits) != 0 || node.sent() != 1 {
				t.Fatalf("after recovery: waits=%+v, error=%v, sends=%d; want the wait completed and one send", doc.Waits, err, node.sent())
			}
		})
	}
}
