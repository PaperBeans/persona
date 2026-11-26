package assistant

import (
	"context"
	"encoding/json"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// TestScheduledProviderFailureRetriesAtAdvertisedRecoveryTime verifies that terminal provider failures carrying a
// trustworthy future recovery time defer the single retry until that recovery window (with the existing safety margin)
// instead of retrying after the fixed 3-minute delay, and never retry more than once.
func TestScheduledProviderFailureRetriesAtAdvertisedRecoveryTime(t *testing.T) {
	const recoveryOffset = 30 * time.Minute
	for _, tc := range []struct {
		name      string
		errText   func(turnAt, recovery time.Time) (string, time.Time)
		setupSess func(c *proto.Session, turnAt, recovery time.Time)
	}{
		{
			name: "gateway 503 available again dated utc",
			errText: func(_, recovery time.Time) (string, time.Time) {
				r := recovery.UTC().Truncate(time.Minute)
				return "API Error: 503 · no upstream account available; an account becomes available again at " +
					r.Format("2006-01-02 15:04") + " UTC", r
			},
		},
		{
			name: "503 retry-after seconds",
			errText: func(turnAt, _ time.Time) (string, time.Time) {
				return "API Error: 503 Service Unavailable (Retry-After: 1800)", turnAt.Add(30 * time.Minute).UTC()
			},
		},
		{
			name: "503 retry-after http date",
			errText: func(_, recovery time.Time) (string, time.Time) {
				r := recovery.UTC().Truncate(time.Second)
				return "API Error: 503 Service Unavailable; Retry-After: " + r.Format("Mon, 02 Jan 2006 15:04:05 GMT"), r
			},
		},
		{
			name: "503 try again in duration",
			errText: func(turnAt, _ time.Time) (string, time.Time) {
				return "API Error: 503 overloaded. Please try again in 30m.", turnAt.Add(30 * time.Minute).UTC()
			},
		},
		{
			name: "503 json exponent",
			errText: func(turnAt, _ time.Time) (string, time.Time) {
				return `API Error: 503 {"error":{"retry_after":1.8e3}}`, turnAt.Add(30 * time.Minute)
			},
		},
		{
			name: "503 duration with a conjunction",
			errText: func(turnAt, _ time.Time) (string, time.Time) {
				return "API Error: 503 overloaded. Please try again in 1 hour and 30 minutes.", turnAt.Add(90 * time.Minute)
			},
		},
		{
			name: "503 json resets_at",
			errText: func(_, recovery time.Time) (string, time.Time) {
				r := recovery.UTC().Truncate(time.Second)
				return `API Error: 503 {"error":{"type":"overloaded_error","resets_at":"` + r.Format(time.RFC3339) + `"}}`, r
			},
		},
		{
			name: "structured TurnErrorResetsAt on transient error",
			errText: func(_, recovery time.Time) (string, time.Time) {
				return "API Error: 503 no eligible upstream", recovery.UTC()
			},
			setupSess: func(c *proto.Session, _, recovery time.Time) {
				r := recovery.UTC()
				c.TurnErrorKind = proto.TurnErrorTransient
				c.TurnErrorResetsAt = &r
			},
		},
		{
			name: "exhausted ModelFallback RecoveryReset",
			errText: func(_, recovery time.Time) (string, time.Time) {
				return "API Error: 503 no eligible upstream", recovery.UTC()
			},
			setupSess: func(c *proto.Session, _, recovery time.Time) {
				r := recovery.UTC()
				c.TurnErrorKind = proto.TurnErrorTransient
				c.ModelFallback = &proto.ModelRefusal{RecoveryExhausted: true, RecoveryReset: &r}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				st := withDelayedWaitCAS(&memStore{})
				f := newFakeHub().withMachine()
				s := New(Options{Hub: f, Store: func() Store { return st }, Logf: testLogf(t)})
				s.turnPoll = time.Second
				now := time.Now().UTC()

				task := Task{ID: "p", Title: "Example check", Prompt: "Check the example", Repeat: repeatInterval,
					EveryMinutes: 60, At: now.Add(2 * time.Hour), Enabled: true, MachineID: "server", CreatedBy: "assistant"}
				sc := setTasks(s, task)
				sc.mu.Lock()
				if err := s.saveLocked(sc, sc.tasks); err != nil {
					sc.mu.Unlock()
					t.Fatal(err)
				}
				sc.mu.Unlock()

				f.items = []proto.Item{{ID: "prompt-1", Role: "user", Kind: proto.KindText, Text: "Check the example"}}
				sess := proto.Session{SessionID: "s1", MachineID: "server", Status: proto.StatusWorking, State: proto.StateWorking,
					Assistant: true, Cwd: "/home/me/.persona/assistant", UpdatedAt: now}
				f.setSessions("server", sess)

				// Ensure no pending transient-recovery wake blocks the terminal watcher.
				s.putWork(context.Background(), db.SessionWork{MachineID: "server", SessionID: "s1", TurnAt: now,
					Work: proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitTransient, TurnAt: now, At: now}})

				run := ScheduleRun{RefID: task.ID, Status: runOK, MachineID: "server", SessionID: "s1", Due: now, At: now, of: now}
				run.ID = s.recordRun(run)

				done := make(chan struct{})
				go func() {
					s.watchScheduledRun(run, task, Device{ID: "server", Name: "Example computer"})
					close(done)
				}()
				synctest.Wait()
				// Let waitTurnEnd observe the working turn before it ends with the terminal error.
				<-time.After(2 * s.turnPoll)
				synctest.Wait()

				turnAt := time.Now().UTC()
				errText, wantRecovery := tc.errText(turnAt, turnAt.Add(recoveryOffset))
				f.items = []proto.Item{{ID: "err-1", Role: "assistant", Kind: proto.KindText, Text: errText}}
				sess.Status, sess.State = proto.StatusDone, proto.StateIdle
				sess.LastText, sess.TurnError = errText, errText
				sess.UpdatedAt = turnAt
				if tc.setupSess != nil {
					tc.setupSess(&sess, turnAt, wantRecovery)
				}
				f.setSessions("server", sess)
				<-time.After(3 * s.turnPoll)
				synctest.Wait()

				// Confirm the delayed wait was persisted for the recovery window (+ safety margin), not 3 minutes.
				doc, err := s.readDelayedWaits(context.Background())
				if err != nil || len(doc.Waits) != 1 {
					t.Fatalf("persisted delayed waits = %+v, err=%v; want 1", doc.Waits, err)
				}
				w := doc.Waits[0]
				minAt := wantRecovery.Add(limitContinueAfter)
				maxAt := wantRecovery.Add(limitContinueAfter + limitContinueSpread)
				if w.At.Before(minAt) || w.At.After(maxAt) {
					t.Fatalf("delayed wait deadline = %v, want in [%v, %v]", w.At, minAt, maxAt)
				}

				// Past the 3-minute fixed delay (and even 10 minutes in): still must NOT have retried yet.
				<-time.After(10 * time.Minute)
				synctest.Wait()
				if got := f.sent(); got != 0 || retried(s) {
					t.Fatalf("retried prematurely before recovery window: sent=%d retried=%v", got, retried(s))
				}

				// Still before recovery + safety margin: must NOT have retried yet.
				if remaining := time.Until(wantRecovery); remaining > 0 {
					<-time.After(remaining)
					synctest.Wait()
				}
				if got := f.sent(); got != 0 || retried(s) {
					t.Fatalf("retried before safety margin elapsed: sent=%d retried=%v", got, retried(s))
				}

				// Advance past recovery + safety margin + spread: exactly one retry dispatches.
				<-time.After(limitContinueAfter + limitContinueSpread + time.Second)
				synctest.Wait()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("watcher did not finish after recovery-window retry")
				}
				if got := f.sent(); got != 1 || !retried(s) {
					t.Fatalf("expected 1 recovery-window retry, got sent=%d retried=%v", got, retried(s))
				}

				// If the single retried run fails again with another future recovery time, its watcher must record
				// failure and exit without scheduling a second retry.
				f.items = []proto.Item{{ID: "err-2", Role: "assistant", Kind: proto.KindText, Text: errText}}
				sess.Status, sess.State = proto.StatusDone, proto.StateIdle
				sess.UpdatedAt = time.Now().UTC()
				f.setSessions("server", sess)
				<-time.After(3 * s.turnPoll)
				synctest.Wait()
				if got := f.sent(); got != 1 {
					t.Fatalf("failed retry triggered another send: total sent=%d, want 1", got)
				}
				doc, err = s.readDelayedWaits(context.Background())
				if err != nil || len(doc.Waits) != 0 {
					t.Fatalf("failed retry persisted another delayed wait: %+v, err=%v", doc.Waits, err)
				}
			})
		})
	}
}

// TestScheduledProviderRecoveryRetrySurvivesRestartAndDispatchesOnce verifies restart survival, exact deadline
// preservation, briefing occurrence Due preservation, and at-most-once dispatch across replacement managers.
func TestScheduledProviderRecoveryRetrySurvivesRestartAndDispatchesOnce(t *testing.T) {
	t.Run("shared store", func(t *testing.T) {
		testScheduledProviderRecoveryRetrySurvivesRestart(t, withDelayedWaitCAS(&memStore{}))
	})
	t.Run("postgresql", func(t *testing.T) {
		testScheduledProviderRecoveryRetrySurvivesRestart(t, taskRecoveryDatabase(t))
	})
}

func testScheduledProviderRecoveryRetrySurvivesRestart(t *testing.T, st Store) {
	t.Helper()
	n := newFakeHub().withMachine()
	first := newServiceOn(t, n, st)
	first.runRetry = time.Hour
	first.turnPoll = 50 * time.Millisecond

	recovery := time.Now().UTC().Add(45 * time.Minute).Truncate(time.Minute)
	errText := "API Error: 503 · no account available; an account becomes available again at " +
		recovery.Format("2006-01-02 15:04") + " UTC"

	var waitMu sync.Mutex
	var recordedRecovery time.Time
	first.sched.limitWait = func(reset, _ time.Time) time.Duration {
		waitMu.Lock()
		recordedRecovery = reset
		waitMu.Unlock()
		return 0 // persist overdue deadline so restart test can drive restore immediately
	}

	// Abandon manager #1 right after reaching the deadline, before it can claim or dispatch.
	oldAtDeadline := make(chan struct{})
	releaseOld := make(chan struct{})
	oldExited := make(chan struct{})
	var once sync.Once
	first.retryNow = func() time.Time {
		once.Do(func() { close(oldAtDeadline) })
		<-releaseOld
		defer close(oldExited)
		runtime.Goexit()
		panic("unreachable")
	}
	t.Cleanup(func() {
		close(releaseOld)
		<-oldExited
	})

	due := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	task := Task{ID: "morning", Title: "Morning check", Prompt: "Check status", Repeat: repeatDaily, Time: "09:00",
		Enabled: true, MachineID: "server", CreatedBy: "assistant", Facts: true}
	sc := setTasks(first, task)
	sc.mu.Lock()
	if err := first.saveLocked(sc, sc.tasks); err != nil {
		sc.mu.Unlock()
		t.Fatal(err)
	}
	sc.mu.Unlock()

	turnAt := time.Now().UTC().Add(-time.Minute)
	n.items = []proto.Item{{ID: "p1", Role: "user", Kind: proto.KindText, Text: "Check status"}}
	n.setSessions("server", proto.Session{SessionID: "s1", MachineID: "server", Status: proto.StatusWorking, State: proto.StateWorking,
		Assistant: true, Cwd: "/home/me/.persona/assistant", UpdatedAt: turnAt})
	// Mark transient recovery as exhausted (no pending wake).
	first.putWork(context.Background(), db.SessionWork{MachineID: "server", SessionID: "s1", TurnAt: turnAt,
		Work: proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitTransient, TurnAt: turnAt, At: turnAt}})

	run := ScheduleRun{RefID: task.ID, Status: runOK, MachineID: "server", SessionID: "s1", Due: due, At: turnAt, of: turnAt}
	run.ID = first.recordRun(run)

	go first.watchScheduledRun(run, task, Device{ID: "server", Name: "Example computer"})
	go func() {
		<-time.After(2 * first.turnPoll)
		n.setItems(proto.Item{ID: "e1", Role: "assistant", Kind: proto.KindText, Text: errText})
		n.setSessions("server", proto.Session{SessionID: "s1", MachineID: "server", Status: proto.StatusDone, State: proto.StateIdle,
			Assistant: true, Cwd: "/home/me/.persona/assistant", LastText: errText, TurnError: errText,
			TurnErrorKind: proto.TurnErrorTransient, UpdatedAt: turnAt})
	}()

	select {
	case <-oldAtDeadline:
	case <-time.After(3 * time.Second):
		t.Fatal("first manager did not persist and reach the recovery retry deadline")
	}
	waitMu.Lock()
	gotRec := recordedRecovery
	waitMu.Unlock()
	if !gotRec.Equal(recovery) {
		t.Fatalf("scheduled recovery time = %v, want %v", gotRec, recovery)
	}
	if got := n.sent(); got != 0 {
		t.Fatalf("first manager dispatched before abandonment: sent=%d", got)
	}

	// Verify the persisted wait exists in the durable store before manager #2 starts.
	doc, err := first.readDelayedWaits(context.Background())
	if err != nil || len(doc.Waits) != 1 || doc.Waits[0].Retry == nil || !doc.Waits[0].Retry.Run.Due.Equal(due) {
		t.Fatalf("persisted wait = %+v, err=%v", doc.Waits, err)
	}

	sent := make(chan map[string]any, 4)
	n.mu.Lock()
	n.onAssistantSend = func(_ *fakeHub, p map[string]any) { sent <- p }
	n.mu.Unlock()

	runCtx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	second := newServiceOn(t, n, st)
	second.runRetry = time.Hour
	go second.Run(runCtx)

	select {
	case <-sent:
	case <-time.After(3 * time.Second):
		t.Fatalf("replacement manager did not dispatch the restored recovery retry; sends=%d", n.sent())
	}
	waitFor(t, "the restored retry run to record with preserved Due", func() bool {
		sc := second.loadSchedule(context.Background())
		sc.mu.Lock()
		defer sc.mu.Unlock()
		return slices.ContainsFunc(sc.runs, func(r ScheduleRun) bool {
			return r.RefID == task.ID && r.Retried && r.Due.Equal(due)
		})
	})
	waitFor(t, "the completed retry to leave delayed_waits", func() bool {
		doc, err := second.readDelayedWaits(context.Background())
		return err == nil && len(doc.Waits) == 0
	})
	if got := n.sent(); got != 1 {
		t.Fatalf("restored retry dispatched %d times, want 1", got)
	}

	// Manager #3 starting from the same store must never dispatch again.
	third := newServiceOn(t, n, st)
	third.runRetry = time.Hour
	third.restoreDelayedWaits(context.Background())
	if got := n.sent(); got != 1 {
		t.Fatalf("third manager dispatched again: sends=%d, want 1", got)
	}
}

// TestScheduledProviderRecoveryPendingTransientWakeNeverReplays verifies that an active transient-recovery wake keeps
// watchScheduledRun from failing or scheduling a second prompt even when the error carries a future recovery time, and
// that a pending transient wake appearing during a delayed retry also cancels dispatch.
func TestScheduledProviderRecoveryPendingTransientWakeNeverReplays(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st := withDelayedWaitCAS(&memStore{})
		f := newFakeHub().withMachine()
		s := New(Options{Hub: f, Store: func() Store { return st }, Logf: testLogf(t)})
		s.turnPoll = time.Second
		now := time.Now().UTC()
		errText := "API Error: 503 Service Unavailable (Retry-After: 1800)"

		task := Task{ID: "p", Title: "Example check", Prompt: "Check the example", Repeat: repeatInterval,
			EveryMinutes: 60, At: now.Add(2 * time.Hour), Enabled: true, MachineID: "server"}
		setTasks(s, task)

		wakeAt := now.Add(2 * time.Minute)
		s.putWork(context.Background(), db.SessionWork{MachineID: "server", SessionID: "s1", TurnAt: now, WakeAt: &wakeAt,
			Work: proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitTransient, TurnAt: now, At: now, WakeAt: &wakeAt}})

		f.items = []proto.Item{{ID: "err-1", Role: "assistant", Kind: proto.KindText, Text: errText}}
		f.setSessions("server", proto.Session{SessionID: "s1", MachineID: "server", Status: proto.StatusDone, State: proto.StateUnread,
			Assistant: true, TurnError: errText, TurnErrorKind: proto.TurnErrorTransient, UpdatedAt: now})

		run := ScheduleRun{RefID: task.ID, Status: runOK, MachineID: "server", SessionID: "s1", Due: now, At: now, of: now}
		run.ID = s.recordRun(run)

		done := make(chan struct{})
		go func() {
			s.watchScheduledRun(run, task, Device{ID: "server", Name: "Example computer"})
			close(done)
		}()
		synctest.Wait()

		// Wait well past 3 minutes while transient recovery is still pending: no failure, no delayed wait, no replay.
		<-time.After(5 * time.Minute)
		synctest.Wait()
		doc, err := s.readDelayedWaits(context.Background())
		if err != nil || len(doc.Waits) != 0 {
			t.Fatalf("pending transient wake still created delayed wait: %+v, err=%v", doc.Waits, err)
		}
		if f.sent() != 0 || retried(s) {
			t.Fatalf("pending transient wake caused a scheduled prompt replay: sent=%d retried=%v", f.sent(), retried(s))
		}

		// Once transient recovery succeeds, the original run completes cleanly with no extra scheduled prompt.
		f.items = []proto.Item{{ID: "ok-1", Role: "assistant", Kind: proto.KindText, Text: "Recovered answer"}}
		f.setSessions("server", proto.Session{SessionID: "s1", MachineID: "server", Status: proto.StatusDone, State: proto.StateIdle,
			Assistant: true, LastText: "Recovered answer", UpdatedAt: time.Now().UTC()})
		select {
		case <-done:
		case <-time.After(10 * s.turnPoll):
			t.Fatal("watcher did not exit after transient recovery succeeded")
		}
		synctest.Wait()
		if f.sent() != 0 || retried(s) {
			t.Fatalf("recovered run sent a second scheduled prompt: sent=%d retried=%v", f.sent(), retried(s))
		}
	})

	// Also verify that if a pending transient wake exists when a persisted delayed retry reaches its deadline,
	// scheduledLimitRetryDue cancels it instead of sending a second scheduled prompt.
	t.Run("cancels persisted delayed retry if transient wake is pending", func(t *testing.T) {
		s, n, w := newDelayedRetryFixture(t, withDelayedWaitCAS(&memStore{}))
		wakeAt := time.Now().UTC().Add(time.Minute)
		s.putWork(context.Background(), db.SessionWork{MachineID: "server", SessionID: "s1", TurnAt: w.Retry.LimitTurn, WakeAt: &wakeAt,
			Work: proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitTransient, TurnAt: w.Retry.LimitTurn, At: w.Retry.LimitTurn, WakeAt: &wakeAt}})
		s.runScheduledLimitRetry(context.Background(), w, true)
		if got := n.sent(); got != 0 {
			t.Fatalf("persisted retry dispatched %d times despite pending transient wake", got)
		}
		doc, err := s.readDelayedWaits(context.Background())
		if err != nil || len(doc.Waits) != 0 {
			t.Fatalf("cancelled delayed wait remained in store: %+v, err=%v", doc.Waits, err)
		}
	})
}

// TestScheduledProviderFailureFallbackOnMissingMalformedOrPastMetadata verifies that missing, malformed, past,
// far-future, or non-provider user-error metadata retains the existing bounded 3-minute fixed-delay fallback.
func TestScheduledProviderFailureFallbackOnMissingMalformedOrPastMetadata(t *testing.T) {
	for _, tc := range []struct {
		name      string
		errText   func(now time.Time) string
		setupSess func(c *proto.Session, now time.Time)
	}{
		{
			name: "missing metadata",
			errText: func(time.Time) string {
				return "API Error: 503 no eligible upstream"
			},
		},
		{
			name: "malformed retry-after",
			errText: func(time.Time) string {
				return "API Error: 503 Service Unavailable; Retry-After: soon"
			},
		},
		{
			name: "malformed utc clock",
			errText: func(time.Time) string {
				return "API Error: 503 available again at 25:99 UTC"
			},
		},
		{
			name: "past dated utc availability",
			errText: func(now time.Time) string {
				return "API Error: 503 available again at " + now.Add(-10*time.Minute).UTC().Format("2006-01-02 15:04") + " UTC"
			},
		},
		{
			name: "past retry-after http date",
			errText: func(now time.Time) string {
				return "API Error: 503 Retry-After: " + now.Add(-5*time.Minute).UTC().Format("Mon, 02 Jan 2006 15:04:05 GMT")
			},
		},
		{
			name: "past structured TurnErrorResetsAt",
			errText: func(time.Time) string {
				return "API Error: 503 no eligible upstream"
			},
			setupSess: func(c *proto.Session, now time.Time) {
				past := now.Add(-5 * time.Minute).UTC()
				c.TurnErrorKind = proto.TurnErrorTransient
				c.TurnErrorResetsAt = &past
			},
		},
		{
			name: "far-future corrupt resets_at",
			errText: func(time.Time) string {
				return `API Error: 503 {"error":{"resets_at":"2099-01-01T00:00:00Z"}}`
			},
		},
		{
			name: "user error with retry-after stays on fixed fallback",
			errText: func(time.Time) string {
				return "API Error: 401 Unauthorized; Retry-After: 1800"
			},
			setupSess: func(c *proto.Session, _ time.Time) {
				c.TurnErrorKind = proto.TurnErrorUser
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				st := withDelayedWaitCAS(&memStore{})
				f := newFakeHub().withMachine()
				s := New(Options{Hub: f, Store: func() Store { return st }, Logf: testLogf(t)})
				s.turnPoll = time.Second
				now := time.Now().UTC()
				errText := tc.errText(now)

				task := Task{ID: "p", Title: "Example check", Prompt: "Check the example", Repeat: repeatInterval,
					EveryMinutes: 60, At: now.Add(2 * time.Hour), Enabled: true, MachineID: "server"}
				setTasks(s, task)

				f.items = []proto.Item{{ID: "err-1", Role: "assistant", Kind: proto.KindText, Text: errText}}
				sess := proto.Session{SessionID: "s1", MachineID: "server", Status: proto.StatusWorking, State: proto.StateWorking,
					Assistant: true, Cwd: "/home/me/.persona/assistant", UpdatedAt: now}
				f.setSessions("server", sess)
				s.putWork(context.Background(), db.SessionWork{MachineID: "server", SessionID: "s1", TurnAt: now,
					Work: proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitTransient, TurnAt: now, At: now}})

				run := ScheduleRun{RefID: task.ID, Status: runOK, MachineID: "server", SessionID: "s1", Due: now, At: now, of: now}
				run.ID = s.recordRun(run)

				done := make(chan struct{})
				go func() {
					s.watchScheduledRun(run, task, Device{ID: "server", Name: "Example computer"})
					close(done)
				}()
				synctest.Wait()
				<-time.After(2 * s.turnPoll)
				synctest.Wait()

				sess.Status, sess.State = proto.StatusDone, proto.StateIdle
				sess.LastText, sess.TurnError = errText, errText
				sess.UpdatedAt = time.Now().UTC()
				if tc.setupSess != nil {
					tc.setupSess(&sess, sess.UpdatedAt)
				}
				f.setSessions("server", sess)
				<-time.After(3 * s.turnPoll)
				synctest.Wait()

				// No durable recovery-time wait should have been created.
				doc, err := s.readDelayedWaits(context.Background())
				if err != nil || len(doc.Waits) != 0 {
					t.Fatalf("unexpected durable delayed wait for fallback case: %+v, err=%v", doc.Waits, err)
				}

				// Before 3 minutes: not retried yet.
				<-time.After(runRetryDelay - 10*time.Second)
				synctest.Wait()
				if got := f.sent(); got != 0 || retried(s) {
					t.Fatalf("retried before fixed runRetryDelay: sent=%d retried=%v", got, retried(s))
				}

				// At 3 minutes: fixed-delay fallback dispatches once.
				<-time.After(15 * time.Second)
				synctest.Wait()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("watcher did not finish fixed-delay fallback")
				}
				if got := f.sent(); got != 1 || !retried(s) {
					t.Fatalf("expected 1 fixed-delay fallback retry, got sent=%d retried=%v", got, retried(s))
				}
				// Let the retried run's watcher observe completion and exit cleanly within the synctest bubble.
				f.items = []proto.Item{{ID: "ok-2", Role: "assistant", Kind: proto.KindText, Text: "Done"}}
				sess.Status, sess.State = proto.StatusDone, proto.StateIdle
				sess.LastText, sess.TurnError, sess.TurnErrorKind, sess.TurnErrorResetsAt = "Done", "", "", nil
				sess.UpdatedAt = time.Now().UTC()
				f.setSessions("server", sess)
				<-time.After(3 * s.turnPoll)
				synctest.Wait()
			})
		})
	}
}

// TestScheduledProviderRecoveryRetainsTaskRevalidationOnMissingSession verifies that even when the node's session
// lookup returns no row right as the failure occurs, a dated future recovery time in the error text still schedules
// a durable retry and cancels it if the conversation advances before dispatch.
func TestScheduledProviderRecoveryRetainsTaskRevalidationOnMissingSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st := withDelayedWaitCAS(&memStore{})
		f := newFakeHub().withMachine()
		s := New(Options{Hub: f, Store: func() Store { return st }, Logf: testLogf(t)})
		s.turnPoll = time.Second
		now := time.Now().UTC().Truncate(time.Minute)
		recovery := now.Add(30 * time.Minute)
		errText := "API Error: 503 available again at " + recovery.Format("2006-01-02 15:04") + " UTC"

		task := Task{ID: "p", Title: "Example check", Prompt: "Check the example", Repeat: repeatInterval,
			EveryMinutes: 60, At: now.Add(2 * time.Hour), Enabled: true, MachineID: "server"}
		setTasks(s, task)

		hide := false
		f.items = []proto.Item{{ID: "err-1", Role: "assistant", Kind: proto.KindText, Text: errText}}
		f.setSessions("server", proto.Session{SessionID: "s1", MachineID: "server", Status: proto.StatusWorking, State: proto.StateWorking,
			Assistant: true, Cwd: "/home/me/.persona/assistant", UpdatedAt: now})
		f.answerNode(DeviceSessionItems, func(_ string, params json.RawMessage) (any, error) {
			var p struct {
				Limit int `json:"limit"`
			}
			_ = json.Unmarshal(params, &p)
			if p.Limit == 8 {
				hide = true
			}
			return map[string]any{"items": f.items}, nil
		})
		f.answerNode(DeviceSessionGet, func(machine string, params json.RawMessage) (any, error) {
			if hide {
				return map[string]any{"ok": false}, nil
			}
			var p struct {
				SessionID string `json:"session_id"`
			}
			_ = json.Unmarshal(params, &p)
			for _, c := range f.sessions[machine] {
				if c.SessionID == p.SessionID {
					return map[string]any{"session": c, "ok": true}, nil
				}
			}
			return map[string]any{"ok": false}, nil
		})

		s.putWork(context.Background(), db.SessionWork{MachineID: "server", SessionID: "s1", TurnAt: now,
			Work: proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitTransient, TurnAt: now, At: now}})
		run := ScheduleRun{RefID: task.ID, Status: runOK, MachineID: "server", SessionID: "s1", Due: now, At: now, of: now}
		run.ID = s.recordRun(run)

		done := make(chan struct{})
		go func() {
			s.watchScheduledRun(run, task, Device{ID: "server", Name: "Example computer"})
			close(done)
		}()
		synctest.Wait()
		<-time.After(2 * s.turnPoll)
		synctest.Wait()

		f.setSessions("server", proto.Session{SessionID: "s1", MachineID: "server", Status: proto.StatusDone, State: proto.StateIdle,
			Assistant: true, LastText: errText, TurnError: errText, UpdatedAt: now})
		<-time.After(3 * s.turnPoll)
		synctest.Wait()

		// Confirm durable wait was created even though session lookup was hidden.
		doc, err := s.readDelayedWaits(context.Background())
		if err != nil || len(doc.Waits) != 1 {
			t.Fatalf("delayed waits = %+v, err=%v; want 1", doc.Waits, err)
		}

		// User manually advances the conversation during the wait; retry must cancel rather than duplicate work.
		f.mu.Lock()
		hide = false
		for i := range f.sessions["server"] {
			f.sessions["server"][i].UpdatedAt = time.Now().UTC().Add(time.Minute)
			f.sessions["server"][i].LastText = "Manual reply"
			f.sessions["server"][i].TurnError = ""
		}
		f.mu.Unlock()

		<-time.After(35 * time.Minute)
		synctest.Wait()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("watcher did not cancel after conversation advanced")
		}
		if got := f.sent(); got != 0 || retried(s) {
			t.Fatalf("advanced conversation still retried: sent=%d retried=%v", got, retried(s))
		}
		doc, err = s.readDelayedWaits(context.Background())
		if err != nil || len(doc.Waits) != 0 {
			t.Fatalf("cancelled wait remained in store: %+v, err=%v", doc.Waits, err)
		}
	})
}

func TestProviderRecoveryEligibleRejectsUnrecoverableAndUserErrors(t *testing.T) {
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	future := now.Add(30 * time.Minute)
	textWithRetryAfter := "API Error: 503 Service Unavailable (Retry-After: 1800)"
	if got, ok := providerRecoveryAt(proto.Session{}, proto.Session{TurnError: textWithRetryAfter, TurnErrorKind: proto.TurnErrorTransient}, textWithRetryAfter, now, now); !ok || !got.Equal(future) {
		t.Fatalf("transient 503 = (%v, %v), want (%v, true)", got, ok, future)
	}
	for _, tc := range []struct {
		name string
		sess proto.Session
		text string
	}{
		{"user kind", proto.Session{TurnErrorKind: proto.TurnErrorUser}, textWithRetryAfter},
		{"unrecoverable kind", proto.Session{TurnErrorKind: proto.TurnErrorUnrecoverable}, textWithRetryAfter},
		{"no_output kind", proto.Session{TurnErrorKind: proto.TurnErrorNoOutput}, textWithRetryAfter},
		{"stopped kind", proto.Session{TurnErrorKind: proto.TurnErrorStopped}, textWithRetryAfter},
		{"400 bad request", proto.Session{}, "API Error: 400 invalid_request_error (Retry-After: 1800)"},
		{"401 unauthorized", proto.Session{}, "API Error: 401 authentication_error (Retry-After: 1800)"},
		{"403 forbidden", proto.Session{}, "API Error: 403 forbidden (Retry-After: 1800)"},
		{"user aborted", proto.Session{}, "Request was aborted by user (Retry-After: 1800)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := tc.sess
			raw.TurnError = tc.text
			r := future
			raw.TurnErrorResetsAt = &r
			if got, ok := providerRecoveryAt(raw, raw, tc.text, now, now); ok || !strings.Contains(tc.text, "1800") {
				t.Fatalf("%s unexpectedly returned (%v, %v)", tc.name, got, ok)
			}
		})
	}
}
