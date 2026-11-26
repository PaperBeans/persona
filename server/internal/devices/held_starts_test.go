package devices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

type heldStartTestHost struct {
	mu       sync.Mutex
	onReplay func(context.Context, devicewire.ReplayHeldStartRequest) (devicewire.ReplayHeldStartReply, error)
	revoked  map[string]bool
	events   []proto.HeldStart
	eventCh  chan proto.HeldStart
}

func newHeldStartTestHost() *heldStartTestHost {
	return &heldStartTestHost{
		revoked: map[string]bool{},
		eventCh: make(chan proto.HeldStart, 64),
	}
}

func (h *heldStartTestHost) Call(ctx context.Context, method string, params, out any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	switch method {
	case bus.MethodCredentialRevoked:
		var p bus.DeviceCredential
		_ = json.Unmarshal(raw, &p)
		h.mu.Lock()
		rev := h.revoked[p.ID]
		h.mu.Unlock()
		res, _ := json.Marshal(bus.CredentialStatus{Revoked: rev})
		if out != nil {
			return json.Unmarshal(res, out)
		}
		return nil
	case devicewire.MethodReplayHeldStart:
		var req devicewire.ReplayHeldStartRequest
		_ = json.Unmarshal(raw, &req)
		h.mu.Lock()
		fn := h.onReplay
		h.mu.Unlock()
		if fn == nil {
			return errors.New("unexpected replay")
		}
		reply, err := fn(ctx, req)
		if err != nil {
			return err
		}
		res, _ := json.Marshal(reply)
		if out != nil {
			return json.Unmarshal(res, out)
		}
		return nil
	case devicewire.MethodConnected:
		res, _ := json.Marshal(devicewire.ConnectedReply{})
		if out != nil {
			return json.Unmarshal(res, out)
		}
		return nil
	}
	return nil
}

func (h *heldStartTestHost) Notify(event string, params any) error {
	if event != assistantwire.EventBroadcast {
		return nil
	}
	raw, _ := json.Marshal(params)
	var b assistantwire.Broadcast
	if json.Unmarshal(raw, &b) != nil {
		return nil
	}
	var env struct {
		Type      string          `json:"type"`
		HeldStart proto.HeldStart `json:"held_start"`
	}
	if json.Unmarshal(b.Message, &env) == nil && env.Type == proto.HeldStartEventType {
		h.mu.Lock()
		h.events = append(h.events, env.HeldStart)
		h.mu.Unlock()
		select {
		case h.eventCh <- env.HeldStart:
		default:
		}
	}
	return nil
}

func (h *heldStartTestHost) waitEvent(t *testing.T, match func(proto.HeldStart) bool) proto.HeldStart {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-h.eventCh:
			if match(ev) {
				return ev
			}
		case <-deadline:
			h.mu.Lock()
			seen := append([]proto.HeldStart(nil), h.events...)
			h.mu.Unlock()
			t.Fatalf("timed out waiting for held_start event; seen=%+v", seen)
		}
	}
}

func newHeldStartTestDevices(t *testing.T, store *heldMem, host *heldStartTestHost, now func() time.Time) (*Devices, *Machine) {
	t.Helper()
	d := New(Options{Hub: host, Store: store})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		d.workers.Wait()
	})
	d.heldStarts.mu.Lock()
	d.heldStarts.ctx = ctx
	d.heldStarts.now = now
	d.heldStarts.mu.Unlock()
	m := newMachine(d, MachineConfig{ID: "mac", Name: "Studio Mac"})
	d.mu.Lock()
	d.entries = []*Machine{m}
	d.byID["mac"] = m
	d.mu.Unlock()
	if !d.heldStartLoad(ctx) {
		t.Fatal("heldStartLoad failed")
	}
	return d, m
}

func attachFakeNode(t *testing.T, m *Machine, caps []string, onStartStatus func(opID string) (proto.StartStatus, error)) (disconnect func()) {
	t.Helper()
	c1, c2 := net.Pipe()
	nodePeer := rpc.New("node", c2)
	nodePeer.OnRequest = func(ctx context.Context, method string, raw json.RawMessage) (any, error) {
		if method == proto.MethodStartStatus {
			var p proto.StartStatusParams
			_ = json.Unmarshal(raw, &p)
			if onStartStatus == nil {
				return proto.StartStatus{State: proto.StartStatusUnknown}, nil
			}
			return onStartStatus(p.OpID)
		}
		return map[string]any{}, nil
	}
	mgrPeer := rpc.New("mgr", c1)
	go func() { _ = nodePeer.Serve(context.Background(), c2) }()
	go func() { _ = mgrPeer.Serve(context.Background(), c1) }()

	connAt := time.Now().UTC()
	m.mu.Lock()
	m.peer = mgrPeer
	m.connectedAt = connAt
	m.online = true
	m.sessionsListed = true
	m.info = proto.NodeInfo{
		Version:      "test",
		Protocol:     nodeproto.Version,
		Capabilities: caps,
	}
	m.mu.Unlock()

	closeOnce := sync.OnceFunc(func() {
		m.mu.Lock()
		if m.peer == mgrPeer {
			m.peer = nil
			m.online = false
			m.sessionsListed = false
		}
		m.mu.Unlock()
		_ = c1.Close()
		_ = c2.Close()
	})
	t.Cleanup(closeOnce)
	return closeOnce
}

func makeHoldReq(opID, prompt string, onlyOffline bool) devicewire.HoldStartRequest {
	params, _ := json.Marshal(map[string]any{
		"machine_id": "mac",
		"cwd":        "/work/app",
		"launcher":   "claude",
		"prompt":     prompt,
	})
	return devicewire.HoldStartRequest{
		OnlyIfOffline: onlyOffline,
		Call: devicewire.HeldStartCall{
			MachineID: "mac",
			OpID:      opID,
			Params:    params,
			Meta: devicewire.HeldStartMeta{
				Cwd:      "/work/app",
				Launcher: "claude",
			},
			Principal: devicewire.HeldStartPrincipal{
				ID:   "dev-phone",
				Name: "Phone",
				Role: "full",
			},
			Caller: &devicewire.Caller{Kind: "device", CredentialID: "dev-phone"},
			Lang:   "en",
		},
	}
}

func personHTTPRequest(method, path string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set(assistantwire.RoleHeader, "full")
	r.Header.Set(assistantwire.PersonHeader, "1")
	return r
}

func TestHeldStartHoldListCancelAndOnlineGuard(t *testing.T) {
	store := newHeldMem()
	host := newHeldStartTestHost()
	d, m := newHeldStartTestDevices(t, store, host, nil)
	h := d.Handler(http.NotFoundHandler())

	const opID = "11111111-1111-4111-8111-111111111111"
	reply, err := d.holdStart(context.Background(), makeHoldReq(opID, "fix tests", true))
	if err != nil {
		t.Fatal(err)
	}
	if reply.Delivered != "later" || reply.HeldStart.OpID != opID || reply.HeldStart.State != proto.HeldStartWaiting ||
		reply.HeldStart.Prompt != "fix tests" || reply.HeldStart.Computer != "Studio Mac" || !reply.HeldStart.CanCancel {
		t.Fatalf("unexpected hold reply: %+v", reply)
	}
	ev := host.waitEvent(t, func(e proto.HeldStart) bool { return e.OpID == opID && e.State == proto.HeldStartWaiting })
	if ev.Prompt != "fix tests" {
		t.Fatalf("waiting event: %+v", ev)
	}

	// GET list requires person full credentials.
	unauth := httptest.NewRecorder()
	h.ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, "/api/v1/machines/mac/held-starts", nil))
	if unauth.Code != http.StatusForbidden {
		t.Fatalf("unauth list: %d %s", unauth.Code, unauth.Body)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, personHTTPRequest(http.MethodGet, "/api/v1/machines/mac/held-starts"))
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	var list proto.HeldStartList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Starts) != 1 || list.Starts[0].OpID != opID {
		t.Fatalf("listed starts: %s %v", rec.Body, err)
	}

	// DELETE cancels while waiting and broadcasts deleted.
	del := httptest.NewRecorder()
	h.ServeHTTP(del, personHTTPRequest(http.MethodDelete, "/api/v1/machines/mac/held-starts/"+opID))
	if del.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", del.Code, del.Body)
	}
	host.waitEvent(t, func(e proto.HeldStart) bool { return e.OpID == opID && e.State == proto.HeldStartDeleted })

	// When computer is online, OnlyIfOffline returns CodeMachineOnline instead of holding.
	disconnect := attachFakeNode(t, m, nodeproto.Announced, nil)
	defer disconnect()
	_, err = d.holdStart(context.Background(), makeHoldReq("22222222-2222-4222-8222-222222222222", "online", true))
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != devicewire.CodeMachineOnline {
		t.Fatalf("expected machine_online when connected, got %v", err)
	}
}

func TestHeldStartReplaysOnceOnReconnectAndRefusal(t *testing.T) {
	store := newHeldMem()
	host := newHeldStartTestHost()
	d, m := newHeldStartTestDevices(t, store, host, nil)
	h := d.Handler(http.NotFoundHandler())

	const opA = "11111111-1111-4111-8111-111111111111"
	const opB = "22222222-2222-4222-8222-222222222222"
	if _, err := d.holdStart(context.Background(), makeHoldReq(opA, "bad launcher", true)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.holdStart(context.Background(), makeHoldReq(opB, "good start", true)); err != nil {
		t.Fatal(err)
	}

	var callsA, callsB atomic.Int32
	host.onReplay = func(_ context.Context, req devicewire.ReplayHeldStartRequest) (devicewire.ReplayHeldStartReply, error) {
		switch req.Call.OpID {
		case opA:
			if callsA.Add(1) == 1 {
				return devicewire.ReplayHeldStartReply{Outcome: devicewire.ReplayOutcomeRefused, Reason: "launcher claude missing"}, nil
			}
			return devicewire.ReplayHeldStartReply{Outcome: devicewire.ReplayOutcomeStarted, SessionID: "sess-a"}, nil
		case opB:
			callsB.Add(1)
			return devicewire.ReplayHeldStartReply{Outcome: devicewire.ReplayOutcomeStarted, SessionID: "sess-b"}, nil
		}
		return devicewire.ReplayHeldStartReply{}, errors.New("unexpected op")
	}

	// Reconnect: opA refuses (must NOT block independent opB!), opB starts exactly once.
	disconnect := attachFakeNode(t, m, nodeproto.Announced, nil)
	defer disconnect()
	d.heldStartWake("mac")

	refused := host.waitEvent(t, func(e proto.HeldStart) bool { return e.OpID == opA && e.State == proto.HeldStartFailed })
	if refused.Failure != db.HeldRefused || !refused.CanRetry {
		t.Fatalf("expected refused opA: %+v", refused)
	}
	startedB := host.waitEvent(t, func(e proto.HeldStart) bool { return e.OpID == opB && e.State == proto.HeldStartStarted })
	if startedB.SessionID != "sess-b" {
		t.Fatalf("expected started opB: %+v", startedB)
	}
	if callsB.Load() != 1 {
		t.Fatalf("opB replayed %d times, want 1", callsB.Load())
	}

	// Repeated wakes do not re-run failed opA automatically.
	d.heldStartWake("mac")
	if callsA.Load() != 1 {
		t.Fatalf("refused opA auto-retried (%d calls)", callsA.Load())
	}

	// Owner Retry on opA runs it again and succeeds.
	retry := httptest.NewRecorder()
	h.ServeHTTP(retry, personHTTPRequest(http.MethodPost, "/api/v1/machines/mac/held-starts/"+opA+"/retry"))
	if retry.Code != http.StatusOK {
		t.Fatalf("retry opA: %d %s", retry.Code, retry.Body)
	}
	startedA := host.waitEvent(t, func(e proto.HeldStart) bool { return e.OpID == opA && e.State == proto.HeldStartStarted })
	if startedA.SessionID != "sess-a" || callsA.Load() != 2 {
		t.Fatalf("retried opA: %+v calls=%d", startedA, callsA.Load())
	}
}

func TestHeldStartExpiresAfter24Hours(t *testing.T) {
	store := newHeldMem()
	host := newHeldStartTestHost()
	var clockMu sync.Mutex
	cur := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	now := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return cur
	}
	d, _ := newHeldStartTestDevices(t, store, host, now)

	const opID = "33333333-3333-4333-8333-333333333333"
	if _, err := d.holdStart(context.Background(), makeHoldReq(opID, "expire me", true)); err != nil {
		t.Fatal(err)
	}

	clockMu.Lock()
	cur = cur.Add(heldExpiry)
	clockMu.Unlock()
	d.heldStartExpire(context.Background())

	expired := host.waitEvent(t, func(e proto.HeldStart) bool { return e.OpID == opID && e.State == proto.HeldStartFailed })
	if expired.Failure != db.HeldExpired || !expired.CanRetry {
		t.Fatalf("expected expired: %+v", expired)
	}
}

func TestHeldStartMidFlightDisconnectReconcilesWithoutDuplicate(t *testing.T) {
	t.Run("node already started session with op_id", func(t *testing.T) {
		store := newHeldMem()
		host := newHeldStartTestHost()
		d, m := newHeldStartTestDevices(t, store, host, nil)

		const opID = "44444444-4444-4444-8444-444444444444"
		if _, err := d.holdStart(context.Background(), makeHoldReq(opID, "mid-flight", true)); err != nil {
			t.Fatal(err)
		}

		var liveAttempts atomic.Int32
		reconciledCh := make(chan string, 1)
		disconnect1 := attachFakeNode(t, m, nodeproto.Announced, nil)
		host.onReplay = func(_ context.Context, req devicewire.ReplayHeldStartRequest) (devicewire.ReplayHeldStartReply, error) {
			if req.ReconciledSessionID != "" {
				reconciledCh <- req.ReconciledSessionID
				return devicewire.ReplayHeldStartReply{Outcome: devicewire.ReplayOutcomeStarted, SessionID: req.ReconciledSessionID}, nil
			}
			liveAttempts.Add(1)
			disconnect1() // Close the first peer while its launch is still in flight.
			return devicewire.ReplayHeldStartReply{Outcome: devicewire.ReplayOutcomeUnclear}, nil
		}

		// Connection 1 drops mid-flight -> unclear.
		d.heldStartWake("mac")
		unclear := host.waitEvent(t, func(e proto.HeldStart) bool { return e.OpID == opID && e.State == proto.HeldStartFailed })
		if unclear.Failure != db.HeldUnclear {
			t.Fatalf("expected unclear, got %+v", unclear)
		}
		d.workers.Wait() // Reconnect only after the first worker can no longer publish events.

		// Connection 2 reconnects: start.status says pending first, then started -> reconciles without launching again.
		var statusChecks atomic.Int32
		disconnect2 := attachFakeNode(t, m, nodeproto.Announced, func(gotOp string) (proto.StartStatus, error) {
			if gotOp != opID {
				t.Errorf("start.status op_id=%q want %q", gotOp, opID)
			}
			if statusChecks.Add(1) == 1 {
				return proto.StartStatus{State: proto.StartStatusPending}, nil
			}
			return proto.StartStatus{State: proto.StartStatusStarted, SessionID: "sess-reconciled-1"}, nil
		})
		defer disconnect2()

		// First check sees pending -> stays unclear, does not duplicate.
		d.heldStartWake("mac")
		host.waitEvent(t, func(e proto.HeldStart) bool { return e.OpID == opID && e.State == proto.HeldStartStarting })
		host.waitEvent(t, func(e proto.HeldStart) bool {
			return e.OpID == opID && e.State == proto.HeldStartFailed && e.Failure == db.HeldUnclear
		})
		// The failed event is published before the worker releases its wake guard.
		d.workers.Wait()
		if got := statusChecks.Load(); got != 1 {
			t.Fatalf("pending check made %d status calls, want 1", got)
		}
		if liveAttempts.Load() != 1 {
			t.Fatalf("pending check launched a duplicate (%d attempts)", liveAttempts.Load())
		}

		// Next tick/wake checks again -> sees started -> reconciles cleanly!
		d.heldStartWake("mac")
		started := host.waitEvent(t, func(e proto.HeldStart) bool { return e.OpID == opID && e.State == proto.HeldStartStarted })
		if started.SessionID != "sess-reconciled-1" {
			t.Fatalf("reconciled event: %+v", started)
		}
		if got := <-reconciledCh; got != "sess-reconciled-1" {
			t.Fatalf("hub reconciled session=%q", got)
		}
		d.workers.Wait()
		if got := statusChecks.Load(); got != 2 {
			t.Fatalf("reconciliation made %d status calls, want 2", got)
		}
		if liveAttempts.Load() != 1 {
			t.Fatalf("reconcile triggered duplicate live replay (%d attempts)", liveAttempts.Load())
		}
	})

	t.Run("unknown outcome or older node waits for owner retry", func(t *testing.T) {
		store := newHeldMem()
		host := newHeldStartTestHost()
		d, m := newHeldStartTestDevices(t, store, host, nil)
		h := d.Handler(http.NotFoundHandler())

		const opID = "55555555-5555-4555-8555-555555555555"
		if _, err := d.holdStart(context.Background(), makeHoldReq(opID, "older node", true)); err != nil {
			t.Fatal(err)
		}

		var liveAttempts atomic.Int32
		host.onReplay = func(_ context.Context, req devicewire.ReplayHeldStartRequest) (devicewire.ReplayHeldStartReply, error) {
			if liveAttempts.Add(1) == 1 {
				return devicewire.ReplayHeldStartReply{Outcome: devicewire.ReplayOutcomeUnclear}, nil
			}
			return devicewire.ReplayHeldStartReply{Outcome: devicewire.ReplayOutcomeStarted, SessionID: "sess-after-confirm"}, nil
		}

		// Older node without StartOpID capability.
		var disconnect1 func()
		disconnect1 = attachFakeNode(t, m, []string{nodeproto.Sessions}, nil)
		host.onReplay = func(_ context.Context, req devicewire.ReplayHeldStartRequest) (devicewire.ReplayHeldStartReply, error) {
			if liveAttempts.Add(1) == 1 {
				disconnect1() // node disconnects mid-flight
				return devicewire.ReplayHeldStartReply{Outcome: devicewire.ReplayOutcomeUnclear}, nil
			}
			return devicewire.ReplayHeldStartReply{Outcome: devicewire.ReplayOutcomeStarted, SessionID: "sess-after-confirm"}, nil
		}
		d.heldStartWake("mac")
		host.waitEvent(t, func(e proto.HeldStart) bool {
			return e.OpID == opID && e.State == proto.HeldStartFailed && e.Failure == db.HeldUnclear
		})

		// Reconnect: check runs on the older node, leaves it unclear, NEVER blindly auto-retries.
		disconnect2 := attachFakeNode(t, m, []string{nodeproto.Sessions}, nil)
		defer disconnect2()
		d.heldStartWake("mac")
		host.waitEvent(t, func(e proto.HeldStart) bool { return e.OpID == opID && e.State == proto.HeldStartStarting })
		host.waitEvent(t, func(e proto.HeldStart) bool {
			return e.OpID == opID && e.State == proto.HeldStartFailed && e.Failure == db.HeldUnclear && e.CanRetry
		})
		if liveAttempts.Load() != 1 {
			t.Fatalf("older node blindly auto-retried (%d attempts)", liveAttempts.Load())
		}

		// Owner Retry #1: cannot confirm -> moves to unconfirmed (no duplicate launch yet).
		r1 := httptest.NewRecorder()
		h.ServeHTTP(r1, personHTTPRequest(http.MethodPost, "/api/v1/machines/mac/held-starts/"+opID+"/retry"))
		if r1.Code != http.StatusOK {
			t.Fatalf("retry 1: %d %s", r1.Code, r1.Body)
		}
		host.waitEvent(t, func(e proto.HeldStart) bool {
			return e.OpID == opID && e.State == proto.HeldStartFailed && e.Failure == db.HeldUnconfirmed
		})
		if liveAttempts.Load() != 1 {
			t.Fatalf("first retry on unconfirmed node launched prematurely (%d attempts)", liveAttempts.Load())
		}

		// Owner Retry #2 on unconfirmed: starts once.
		r2 := httptest.NewRecorder()
		h.ServeHTTP(r2, personHTTPRequest(http.MethodPost, "/api/v1/machines/mac/held-starts/"+opID+"/retry"))
		if r2.Code != http.StatusOK {
			t.Fatalf("retry 2: %d %s", r2.Code, r2.Body)
		}
		started := host.waitEvent(t, func(e proto.HeldStart) bool { return e.OpID == opID && e.State == proto.HeldStartStarted })
		if started.SessionID != "sess-after-confirm" || liveAttempts.Load() != 2 {
			t.Fatalf("second retry: %+v attempts=%d", started, liveAttempts.Load())
		}
	})
}

func TestHeldStartManagerRestartRecovery(t *testing.T) {
	store := newHeldMem()
	host1 := newHeldStartTestHost()
	d1, _ := newHeldStartTestDevices(t, store, host1, nil)

	const opSending = "66666666-6666-4666-8666-666666666666"
	const opWaiting = "77777777-7777-4777-8777-777777777777"
	if _, err := d1.holdStart(context.Background(), makeHoldReq(opSending, "was in flight", true)); err != nil {
		t.Fatal(err)
	}
	if _, err := d1.holdStart(context.Background(), makeHoldReq(opWaiting, "still waiting", true)); err != nil {
		t.Fatal(err)
	}
	// Mark opSending as sending in the store (simulating manager crash mid-flight).
	op, found, err := store.HeldOperationOf(context.Background(), db.HeldKindStart, "mac", "", opSending)
	if err != nil || !found {
		t.Fatalf("lookup opSending: %v %v", found, err)
	}
	if _, ok, err := store.ClaimHeldOperation(context.Background(), op.Seq, time.Now().UTC()); err != nil || !ok {
		t.Fatalf("claim opSending: %v %v", ok, err)
	}

	// Fresh manager starts against the same store (simulating restart).
	host2 := newHeldStartTestHost()
	var liveStarted []string
	var mu sync.Mutex
	host2.onReplay = func(_ context.Context, req devicewire.ReplayHeldStartRequest) (devicewire.ReplayHeldStartReply, error) {
		if req.ReconciledSessionID != "" {
			return devicewire.ReplayHeldStartReply{Outcome: devicewire.ReplayOutcomeStarted, SessionID: req.ReconciledSessionID}, nil
		}
		mu.Lock()
		liveStarted = append(liveStarted, req.Call.OpID)
		mu.Unlock()
		return devicewire.ReplayHeldStartReply{Outcome: devicewire.ReplayOutcomeStarted, SessionID: "sess-waiting-1"}, nil
	}
	d2, m2 := newHeldStartTestDevices(t, store, host2, nil)

	// Reconnect node that already created opSending before the manager crashed.
	disconnect := attachFakeNode(t, m2, nodeproto.Announced, func(gotOp string) (proto.StartStatus, error) {
		if gotOp == opSending {
			return proto.StartStatus{State: proto.StartStatusStarted, SessionID: "sess-recovered-1"}, nil
		}
		return proto.StartStatus{State: proto.StartStatusUnknown}, nil
	})
	defer disconnect()
	d2.heldStartWake("mac")

	s1 := host2.waitEvent(t, func(e proto.HeldStart) bool { return e.OpID == opSending && e.State == proto.HeldStartStarted })
	s2 := host2.waitEvent(t, func(e proto.HeldStart) bool { return e.OpID == opWaiting && e.State == proto.HeldStartStarted })
	if s1.SessionID != "sess-recovered-1" || s2.SessionID != "sess-waiting-1" {
		t.Fatalf("recovered=%+v waiting=%+v", s1, s2)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(liveStarted) != 1 || liveStarted[0] != opWaiting {
		t.Fatalf("expected only opWaiting to replay live, got %v", liveStarted)
	}
}

func TestHeldStartRevocationCapAndDeleteWhileStarting(t *testing.T) {
	store := newHeldMem()
	host := newHeldStartTestHost()
	d, m := newHeldStartTestDevices(t, store, host, nil)
	h := d.Handler(http.NotFoundHandler())

	// 1. Delete while actively starting returns 409 Conflict.
	const opActive = "88888888-8888-4888-8888-888888888888"
	if _, err := d.holdStart(context.Background(), makeHoldReq(opActive, "slow start", true)); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	host.onReplay = func(_ context.Context, req devicewire.ReplayHeldStartRequest) (devicewire.ReplayHeldStartReply, error) {
		close(entered)
		<-release
		return devicewire.ReplayHeldStartReply{Outcome: devicewire.ReplayOutcomeStarted, SessionID: "sess-active"}, nil
	}
	disconnect := attachFakeNode(t, m, nodeproto.Announced, nil)
	d.heldStartWake("mac")
	<-entered
	delBusy := httptest.NewRecorder()
	h.ServeHTTP(delBusy, personHTTPRequest(http.MethodDelete, "/api/v1/machines/mac/held-starts/"+opActive))
	if delBusy.Code != http.StatusConflict {
		t.Fatalf("delete while starting: %d %s", delBusy.Code, delBusy.Body)
	}
	close(release)
	host.waitEvent(t, func(e proto.HeldStart) bool { return e.OpID == opActive && e.State == proto.HeldStartStarted })
	disconnect()

	// 2. Credential revocation marks held starts failed/signed_out (DeleteOnly), blocking Retry.
	const opRev = "99999999-9999-4999-8999-999999999999"
	if _, err := d.holdStart(context.Background(), makeHoldReq(opRev, "from revoked phone", true)); err != nil {
		t.Fatal(err)
	}
	host.mu.Lock()
	host.revoked["dev-phone"] = true
	host.mu.Unlock()
	d.heldCredentialRevoked("dev-phone")
	revEv := host.waitEvent(t, func(e proto.HeldStart) bool { return e.OpID == opRev && e.State == proto.HeldStartFailed })
	if revEv.Failure != db.HeldSignedOut || !revEv.DeleteOnly || revEv.CanRetry {
		t.Fatalf("revoked event: %+v", revEv)
	}
	retryRev := httptest.NewRecorder()
	h.ServeHTTP(retryRev, personHTTPRequest(http.MethodPost, "/api/v1/machines/mac/held-starts/"+opRev+"/retry"))
	if retryRev.Code != http.StatusConflict {
		t.Fatalf("retry revoked: %d %s", retryRev.Code, retryRev.Body)
	}
	delRev := httptest.NewRecorder()
	h.ServeHTTP(delRev, personHTTPRequest(http.MethodDelete, "/api/v1/machines/mac/held-starts/"+opRev))
	if delRev.Code != http.StatusOK {
		t.Fatalf("delete revoked: %d %s", delRev.Code, delRev.Body)
	}

	// 3. Per-machine cap (heldStartsPerMachine) refuses the 51st held start with CodeHeldStartFull.
	d.heldStarts.mu.Lock()
	delete(d.heldStarts.revoked, "dev-phone")
	d.heldStarts.mu.Unlock()
	for i := range heldStartsPerMachine {
		id := fmt.Sprintf("aaaaaaaa-aaaa-4aaa-8aaa-%012d", i)
		if _, err := d.holdStart(context.Background(), makeHoldReq(id, "batch", true)); err != nil {
			t.Fatalf("hold #%d: %v", i, err)
		}
	}
	_, err := d.holdStart(context.Background(), makeHoldReq("bbbbbbbb-bbbb-4bbb-8bbb-000000000051", "one too many", true))
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != devicewire.CodeHeldStartFull {
		t.Fatalf("expected held_start_full, got %v", err)
	}
}
