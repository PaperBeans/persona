package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/rpc"
)

type exitingWriteCloser struct {
	closes atomic.Int32
	once   sync.Once
	onExit func()
}

func (w *exitingWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (w *exitingWriteCloser) Close() error {
	w.closes.Add(1)
	if w.onExit != nil {
		w.once.Do(w.onExit)
	}
	return nil
}

func isNodeRestartingErr(err error) bool {
	var coded *rpc.Error
	return errors.As(err, &coded) && coded.Code == nodeproto.CodeRestarting
}

// Regression test for #14449: a long-running assistant background task during
// ResidentReload.Prepare must not leave the resident node permanently wedged in
// node_restarting past the handoff grace window. Read-only RPCs, stop/interrupt
// RPCs, and unrelated conversations must remain servable until the task ends,
// and once it clears Prepare must finish cleanly and drain the old node.
func TestResidentReloadLongRunningBackgroundTaskDoesNotWedgeNode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &ResidentReload{}
		n := assistantNode(t)
		n.useBotmux(false, false)
		n.SetResidentReload(r)

		const (
			blockingID = "11111111-2222-3333-4444-555555555555"
			otherID    = "22222222-2222-3333-4444-555555555555"
		)
		stdin := &handoffTrackingWriteCloser{}
		p := &assistantProc{
			id:     blockingID,
			agent:  "claude",
			busy:   false,
			tasks:  []string{"long-running watch"},
			exited: make(chan struct{}),
			stdin:  stdin,
			cmd:    &exec.Cmd{},
		}
		n.procs[blockingID] = p
		n.state.put(blockingID, sessionState{
			Launcher: "claude",
			Cwd:      "/home/u/.persona/assistant",
			Tasks:    []string{"long-running watch"},
		})
		n.state.put(otherID, sessionState{
			Launcher: "claude",
			Cwd:      "/home/u/.persona/assistant",
		})
		liveAssistants.Store(p, struct{}{})
		t.Cleanup(func() { liveAssistants.Delete(p) })

		ctx, cancel := context.WithCancel(context.Background())
		prepared := make(chan error, 1)
		prepareDone := make(chan struct{})
		go func() {
			defer close(prepareDone)
			prepared <- r.Prepare(ctx)
		}()
		defer func() {
			cancel()
			<-prepareDone
		}()

		synctest.Wait()
		select {
		case err := <-prepared:
			t.Fatalf("reload finished before the background task ended: %v", err)
		default:
		}

		// Advance well past DefaultNodeUpdateHandoffGraceSeconds (60s) and any bounded drain window.
		synctest.Sleep(5 * time.Minute)
		synctest.Wait()

		select {
		case err := <-prepared:
			t.Fatalf("reload proceeded while background task was still running: %v", err)
		default:
		}
		if got := stdin.closes.Load(); got != 0 {
			t.Fatalf("reload closed the assistant process while its background task was running (closes=%d)", got)
		}

		if _, err := n.handle(ctx, "ping", json.RawMessage(`{}`)); err != nil {
			t.Errorf("ping failed while background task deferred reload: %v", err)
		}
		if _, err := n.handle(ctx, "sessions.list", json.RawMessage(`{}`)); err != nil {
			t.Errorf("sessions.list failed while background task deferred reload: %v", err)
		}
		if _, err := n.handle(ctx, "herdr.server.status", json.RawMessage(`{}`)); err != nil {
			t.Errorf("herdr.server.status failed while background task deferred reload: %v", err)
		}
		itemsParams, _ := json.Marshal(map[string]any{"session_id": blockingID, "limit": 20})
		if _, err := n.handle(ctx, "session.items", itemsParams); isNodeRestartingErr(err) {
			t.Errorf("session.items was wedged in node_restarting while background task deferred reload: %v", err)
		}
		stopParams, _ := json.Marshal(map[string]any{"session_id": otherID})
		if _, err := n.handle(ctx, "session.stop", stopParams); isNodeRestartingErr(err) {
			t.Errorf("session.stop was wedged in node_restarting while background task deferred reload: %v", err)
		}
		interruptParams, _ := json.Marshal(map[string]any{"session_id": otherID})
		if _, err := n.handle(ctx, "session.interrupt", interruptParams); err != nil {
			t.Errorf("session.interrupt on unrelated assistant failed while background task deferred reload: %v", err)
		}
		if done, err := r.enter(); err != nil {
			t.Errorf("r.enter() rejected new RPCs while background task deferred reload: %v", err)
		} else {
			done()
		}
		if err := r.enterHeadless(); err != nil {
			t.Errorf("r.enterHeadless() locked out unrelated assistant turns while background task deferred reload: %v", err)
		}

		// Once the background task completes, Prepare must finish promptly, reap idle procs,
		// and leave the drained old node rejecting new work with node_restarting.
		p.mu.Lock()
		p.tasks = nil
		p.mu.Unlock()

		select {
		case err := <-prepared:
			if err != nil {
				t.Fatalf("reload failed after background task ended: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("reload did not complete promptly after background task ended")
		}
		if got := stdin.closes.Load(); got == 0 {
			t.Errorf("reload finished without reaping the now-idle assistant process (closes=%d)", got)
		}
		if done, err := r.enter(); err == nil {
			done()
			t.Error("the drained old resident still admitted new work after Prepare completed")
		} else if !isNodeRestartingErr(err) {
			t.Errorf("the drained old resident returned %v, want %s", err, nodeproto.CodeRestarting)
		}
	})
}

// Regression test for #14449: if a user interrupts the blocking assistant conversation
// from the UI via session.interrupt after Prepare has waited past grace, the interrupt
// must be admitted, stop the blocking process, and allow the pending reload to complete.
func TestResidentReloadBackgroundTaskUnblockedBySessionInterrupt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &ResidentReload{}
		n := assistantNode(t)
		n.useBotmux(false, false)
		n.SetResidentReload(r)

		const blockingID = "11111111-2222-3333-4444-666666666666"
		var p *assistantProc
		stdin := &exitingWriteCloser{
			onExit: func() {
				liveAssistants.Delete(p)
				close(p.exited)
			},
		}
		p = &assistantProc{
			id:     blockingID,
			agent:  "claude",
			busy:   false,
			tasks:  []string{"long-running watch"},
			exited: make(chan struct{}),
			stdin:  stdin,
			cmd:    &exec.Cmd{},
		}
		n.procs[blockingID] = p
		n.state.put(blockingID, sessionState{
			Launcher: "claude",
			Cwd:      "/home/u/.persona/assistant",
			Tasks:    []string{"long-running watch"},
		})
		liveAssistants.Store(p, struct{}{})
		t.Cleanup(func() { liveAssistants.Delete(p) })

		ctx, cancel := context.WithCancel(context.Background())
		prepared := make(chan error, 1)
		prepareDone := make(chan struct{})
		go func() {
			defer close(prepareDone)
			prepared <- r.Prepare(ctx)
		}()
		defer func() {
			cancel()
			<-prepareDone
		}()

		synctest.Wait()
		synctest.Sleep(5 * time.Minute)
		synctest.Wait()

		select {
		case err := <-prepared:
			t.Fatalf("reload finished before the user interrupted the blocking assistant: %v", err)
		default:
		}
		if !p.alive() || stdin.closes.Load() != 0 {
			t.Fatalf("blocking assistant was stopped before session.interrupt (alive=%t closes=%d)", p.alive(), stdin.closes.Load())
		}

		params, _ := json.Marshal(map[string]any{"session_id": blockingID})
		if _, err := n.handle(ctx, "session.interrupt", params); err != nil {
			t.Fatalf("session.interrupt on blocking assistant was rejected instead of unblocking reload: %v", err)
		}
		if p.alive() || stdin.closes.Load() == 0 {
			t.Fatalf("session.interrupt did not stop the blocking assistant (alive=%t closes=%d)", p.alive(), stdin.closes.Load())
		}

		select {
		case err := <-prepared:
			if err != nil {
				t.Fatalf("reload failed after session.interrupt stopped the blocking assistant: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("reload did not complete promptly after session.interrupt stopped the blocking assistant")
		}
		if done, err := r.enter(); err == nil {
			done()
			t.Error("the drained old resident still admitted new work after Prepare completed")
		} else if !isNodeRestartingErr(err) {
			t.Errorf("the drained old resident returned %v, want %s", err, nodeproto.CodeRestarting)
		}
	})
}

// Baseline check: a short busy turn or short background task that finishes within the
// handoff grace window must still complete reload promptly right after it ends.
func TestResidentReloadShortWorkCompletesBeforeGrace(t *testing.T) {
	cases := []struct {
		name  string
		busy  bool
		tasks []string
	}{
		{name: "short-background-task", busy: false, tasks: []string{"short watch"}},
		{name: "short-busy-turn", busy: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := &ResidentReload{}
				n := assistantNode(t)
				n.useBotmux(false, false)
				n.SetResidentReload(r)

				const id = "11111111-2222-3333-4444-777777777777"
				stdin := &handoffTrackingWriteCloser{}
				p := &assistantProc{
					id:     id,
					agent:  "claude",
					busy:   tc.busy,
					tasks:  append([]string(nil), tc.tasks...),
					exited: make(chan struct{}),
					stdin:  stdin,
					cmd:    &exec.Cmd{},
				}
				n.procs[id] = p
				liveAssistants.Store(p, struct{}{})
				t.Cleanup(func() { liveAssistants.Delete(p) })

				ctx, cancel := context.WithCancel(context.Background())
				prepared := make(chan error, 1)
				prepareDone := make(chan struct{})
				go func() {
					defer close(prepareDone)
					prepared <- r.Prepare(ctx)
				}()
				defer func() {
					cancel()
					<-prepareDone
				}()

				synctest.Wait()
				synctest.Sleep(10 * time.Second)
				synctest.Wait()

				select {
				case err := <-prepared:
					t.Fatalf("reload finished before short work ended: %v", err)
				default:
				}
				if stdin.closes.Load() != 0 {
					t.Fatalf("reload stopped the process during short work (closes=%d)", stdin.closes.Load())
				}
				if _, err := n.handle(ctx, "ping", json.RawMessage(`{}`)); err != nil {
					t.Fatalf("ping failed within grace while short work was running: %v", err)
				}

				p.mu.Lock()
				p.busy = false
				p.tasks = nil
				p.mu.Unlock()
				clearedAt := time.Now()

				select {
				case err := <-prepared:
					if err != nil {
						t.Fatalf("reload failed after short work ended: %v", err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("reload did not complete promptly after short work ended")
				}
				if elapsed := time.Since(clearedAt); elapsed >= 2*time.Second {
					t.Fatalf("reload took too long after short work ended: %s", elapsed)
				}
				if stdin.closes.Load() == 0 {
					t.Error("reload did not reap the idle process after short work ended")
				}
				if done, err := r.enter(); err == nil {
					done()
					t.Error("the drained old resident still admitted new work")
				} else if !isNodeRestartingErr(err) {
					t.Errorf("the drained old resident returned %v, want %s", err, nodeproto.CodeRestarting)
				}
			})
		})
	}
}

// Regression test for #14449: Prepare logs the blocking conversation id and task count
// (without leaking raw task descriptions) and rate-limits repeated reminders.
func TestResidentReloadLogsBlockingAssistantRedactedAndRateLimited(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var logMu sync.Mutex
		var logBuf bytes.Buffer
		r := &ResidentReload{}
		r.SetLogger(log.New(&syncLogWriter{mu: &logMu, buf: &logBuf}, "", 0))

		const (
			blockingID = "abcdef12-2222-3333-4444-555555555555"
			secretTask = "synthetic-secret-command-arg-do-not-log"
		)
		p := &assistantProc{
			id:     blockingID,
			agent:  "claude",
			tasks:  []string{secretTask, "second synthetic task"},
			exited: make(chan struct{}),
			stdin:  &handoffTrackingWriteCloser{},
			cmd:    &exec.Cmd{},
		}
		liveAssistants.Store(p, struct{}{})
		t.Cleanup(func() { liveAssistants.Delete(p) })

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = r.Prepare(ctx)
		}()
		defer func() {
			cancel()
			<-done
		}()

		synctest.Wait()
		synctest.Sleep(2 * time.Minute)
		synctest.Wait()

		// Simulate unrelated conversations starting and finishing turns while the background task
		// continues: log rate limiting must stay bounded by reloadBlockLogEvery, not log per turn.
		unrelated := &assistantProc{
			id:     "fedcba98-2222-3333-4444-555555555555",
			agent:  "claude",
			busy:   true,
			exited: make(chan struct{}),
			stdin:  &handoffTrackingWriteCloser{},
			cmd:    &exec.Cmd{},
		}
		liveAssistants.Store(unrelated, struct{}{})
		t.Cleanup(func() { liveAssistants.Delete(unrelated) })
		synctest.Sleep(time.Minute)
		synctest.Wait()
		unrelated.mu.Lock()
		unrelated.busy = false
		unrelated.mu.Unlock()
		synctest.Sleep(time.Minute)
		synctest.Wait()

		logMu.Lock()
		beforeReminder := logBuf.String()
		logMu.Unlock()
		if !strings.Contains(beforeReminder, "conv=abcdef12 (tasks=2)") {
			t.Fatalf("missing redacted blocking summary in reload log: %q", beforeReminder)
		}
		if strings.Contains(beforeReminder, secretTask) {
			t.Fatalf("reload log leaked raw task description: %q", beforeReminder)
		}
		if got := strings.Count(beforeReminder, "resident reload:"); got != 1 {
			t.Fatalf("expected 1 rate-limited log line over 4m despite unrelated turns, got %d: %q", got, beforeReminder)
		}

		synctest.Sleep(3 * time.Minute)
		synctest.Wait()
		logMu.Lock()
		afterReminder := logBuf.String()
		logMu.Unlock()
		if got := strings.Count(afterReminder, "resident reload:"); got != 2 {
			t.Fatalf("expected reminder log after reloadBlockLogEvery, got %d lines: %q", got, afterReminder)
		}
	})
}

type syncLogWriter struct {
	mu  *sync.Mutex
	buf *bytes.Buffer
}

func (w *syncLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

// Regression test for #14449: after grace expires with an active turn (no background tasks)
// and zero active RPCs, non-headless RPCs stay open while headless admission is paused only
// for the bounded reloadDrainWindow, then resumes if the turn runs long, and reloads promptly
// once the turn finishes.
func TestResidentReloadLongTurnBoundedDrainWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &ResidentReload{}
		n := assistantNode(t)
		n.useBotmux(false, false)
		n.SetResidentReload(r)

		const id = "33333333-2222-3333-4444-555555555555"
		stdin := &handoffTrackingWriteCloser{}
		p := &assistantProc{
			id:     id,
			agent:  "claude",
			busy:   true,
			exited: make(chan struct{}),
			stdin:  stdin,
			cmd:    &exec.Cmd{},
		}
		n.procs[id] = p
		liveAssistants.Store(p, struct{}{})
		t.Cleanup(func() { liveAssistants.Delete(p) })

		ctx, cancel := context.WithCancel(context.Background())
		prepared := make(chan error, 1)
		prepareDone := make(chan struct{})
		go func() {
			defer close(prepareDone)
			prepared <- r.Prepare(ctx)
		}()
		defer func() {
			cancel()
			<-prepareDone
		}()

		synctest.Wait()
		// Inside the bounded drain window (past 60s grace, before 60s+30s drain expiry):
		// non-headless RPCs remain open, while new headless turns are temporarily paused.
		synctest.Sleep(70 * time.Second)
		synctest.Wait()
		if _, err := n.handle(ctx, "ping", json.RawMessage(`{}`)); err != nil {
			t.Fatalf("ping failed during bounded headless drain window: %v", err)
		}
		if err := r.enterHeadless(); !isNodeRestartingErr(err) {
			t.Fatalf("expected headless admission paused inside bounded drain window, got %v", err)
		}

		// Past grace + reloadDrainWindow: headless admission resumes so unrelated chats are not wedged.
		synctest.Sleep(30 * time.Second)
		synctest.Wait()
		if err := r.enterHeadless(); err != nil {
			t.Fatalf("headless admission stayed wedged past bounded drain window: %v", err)
		}
		if _, err := n.handle(ctx, "ping", json.RawMessage(`{}`)); err != nil {
			t.Fatalf("ping failed after bounded drain window elapsed: %v", err)
		}

		// External pre-transfer update_handoff pause must survive Resume().
		r.SetHeadlessPaused(true)
		r.Resume()
		if err := r.enterHeadless(); !isNodeRestartingErr(err) {
			t.Fatalf("Resume cleared external update_handoff pause: %v", err)
		}
		r.SetHeadlessPaused(false)

		p.mu.Lock()
		p.busy = false
		p.mu.Unlock()
		select {
		case err := <-prepared:
			if err != nil {
				t.Fatalf("reload failed after long turn ended: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("reload did not complete promptly after long turn ended")
		}
	})
}

// Regression test for #14449: while ResidentReload.Prepare is waiting, sweepAssistants must not
// start new idle auto-compactions on other idle conversations (which would extend the stall).
func TestResidentReloadSuppressesIdleAutoCompaction(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &ResidentReload{}
		n := assistantNode(t)
		n.SetResidentReload(r)

		const (
			blockingID = "44444444-2222-3333-4444-555555555555"
			idleID     = "55555555-2222-3333-4444-555555555555"
		)
		blocker := &assistantProc{
			id:     blockingID,
			agent:  "claude",
			tasks:  []string{"long task"},
			exited: make(chan struct{}),
			stdin:  &handoffTrackingWriteCloser{},
			cmd:    &exec.Cmd{},
		}
		idleStdin := &handoffTrackingWriteCloser{}
		idleProc := &assistantProc{
			id:         idleID,
			agent:      "claude",
			context:    assistantIdleCompact + 1000,
			lastActive: time.Now().Add(-2 * assistantIdleTimeout),
			exited:     make(chan struct{}),
			stdin:      idleStdin,
			cmd:        &exec.Cmd{},
		}
		n.procs[blockingID] = blocker
		n.procs[idleID] = idleProc
		liveAssistants.Store(blocker, struct{}{})
		liveAssistants.Store(idleProc, struct{}{})
		t.Cleanup(func() {
			liveAssistants.Delete(blocker)
			liveAssistants.Delete(idleProc)
		})

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = r.Prepare(ctx)
		}()
		defer func() {
			cancel()
			<-done
		}()

		synctest.Wait()
		if !r.Preparing() {
			t.Fatal("expected ResidentReload.Preparing() to be true while Prepare waits")
		}
		n.sweepAssistants()

		idleProc.mu.Lock()
		compacting := idleProc.compacting
		idleProc.mu.Unlock()
		if compacting {
			t.Fatal("sweepAssistants started idle auto-compaction during pending resident reload")
		}
		if idleStdin.closes.Load() == 0 {
			t.Fatal("expected idle conversation to be reaped instead of compacted during pending reload")
		}
	})
}

// Regression test for #14449: explicit SetGrace(0) preserves the indefinite natural-idle wait
// without pausing RPCs or headless admission while waiting for assistants.
func TestResidentReloadZeroGracePreservesIndefiniteNaturalIdle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &ResidentReload{}
		r.SetGrace(0)
		const id = "66666666-2222-3333-4444-555555555555"
		p := &assistantProc{
			id:     id,
			agent:  "claude",
			busy:   true,
			exited: make(chan struct{}),
			stdin:  &handoffTrackingWriteCloser{},
			cmd:    &exec.Cmd{},
		}
		liveAssistants.Store(p, struct{}{})
		t.Cleanup(func() { liveAssistants.Delete(p) })

		ctx, cancel := context.WithCancel(context.Background())
		prepared := make(chan error, 1)
		done := make(chan struct{})
		go func() {
			defer close(done)
			prepared <- r.Prepare(ctx)
		}()
		defer func() {
			cancel()
			<-done
		}()

		synctest.Wait()
		synctest.Sleep(5 * time.Minute)
		synctest.Wait()
		if leave, err := r.enter(); err != nil {
			t.Fatalf("SetGrace(0) paused RPC admission: %v", err)
		} else {
			leave()
		}
		if err := r.enterHeadless(); err != nil {
			t.Fatalf("SetGrace(0) paused headless admission: %v", err)
		}

		p.mu.Lock()
		p.busy = false
		p.mu.Unlock()
		select {
		case err := <-prepared:
			if err != nil {
				t.Fatalf("reload failed after idle under SetGrace(0): %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("reload did not complete promptly under SetGrace(0)")
		}
	})
}

// Regression test for #14449 review finding #2: if a deferred conversation briefly becomes idle
// while an active RPC is draining and then starts a brand-new turn, deferred state must have
// cleared so the new turn cannot inherit the old deferral and starve reload forever.
func TestResidentReloadClearsDeferredStateAfterIdle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &ResidentReload{}
		const id = "77777777-2222-3333-4444-555555555555"
		p := &assistantProc{
			id:     id,
			agent:  "claude",
			busy:   true,
			exited: make(chan struct{}),
			stdin:  &handoffTrackingWriteCloser{},
			cmd:    &exec.Cmd{},
		}
		liveAssistants.Store(p, struct{}{})
		t.Cleanup(func() { liveAssistants.Delete(p) })

		ctx, cancel := context.WithCancel(context.Background())
		prepared := make(chan error, 1)
		done := make(chan struct{})
		go func() {
			defer close(done)
			prepared <- r.Prepare(ctx)
		}()
		defer func() {
			cancel()
			<-done
		}()

		synctest.Wait()
		// Past 60s grace + 30s drain window -> deferredIDs={id}, headless admission resumed.
		synctest.Sleep(100 * time.Second)
		synctest.Wait()
		if err := r.enterHeadless(); err != nil {
			t.Fatalf("expected headless admission resumed after drain window: %v", err)
		}

		// Hold an active RPC so Prepare cannot finish during the idle moment, then clear busy
		// long enough for Prepare to observe len(busy)==0 and clear deferredIDs.
		finishRPC, err := r.enter()
		if err != nil {
			t.Fatalf("enter failed while deferred: %v", err)
		}
		p.mu.Lock()
		p.busy = false
		p.mu.Unlock()
		synctest.Sleep(time.Second)
		synctest.Wait()

		// A new turn starts on the same conversation and the RPC finishes: Prepare must enter
		// a fresh bounded headless drain window rather than inheriting the stale deferral.
		p.mu.Lock()
		p.busy = true
		p.mu.Unlock()
		finishRPC()
		synctest.Sleep(time.Second)
		synctest.Wait()

		if err := r.enterHeadless(); !isNodeRestartingErr(err) {
			t.Fatalf("expected fresh bounded headless drain after deferred turn cleared, got %v", err)
		}

		p.mu.Lock()
		p.busy = false
		p.mu.Unlock()
		select {
		case err := <-prepared:
			if err != nil {
				t.Fatalf("reload failed after second turn finished: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("reload did not complete after second turn finished")
		}
	})
}
