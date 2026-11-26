package node

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestStartOpIDRecordedDeduplicatedAndPersisted(t *testing.T) {
	n, hub := runtimeNode(t)
	assistantHub(hub)

	var persisted atomic.Pointer[sessionState]
	n.state.persist = func(id string, v sessionState) {
		if id == "sess-rt-1" {
			cp := v
			persisted.Store(&cp)
		}
	}

	hello := dshHello()
	rt := dialRuntime(t, n, hello)
	t.Cleanup(func() { _ = rt.conn.Close() })

	startedCh := make(chan struct{})
	releaseCh := make(chan struct{})
	var startCalls atomic.Int32
	rt.answer = func(method string, _ json.RawMessage) (any, error) {
		if method == "session.start" {
			if startCalls.Add(1) == 1 {
				close(startedCh)
				<-releaseCh
			}
			return map[string]any{
				"session": map[string]any{
					"session_id": "sess-rt-1",
					"cwd":        n.home,
					"launcher":   "dsh",
					"status":     "idle",
				},
			}, nil
		}
		return map[string]any{"ok": true}, nil
	}

	const opID = "11111111-1111-4111-8111-111111111111"

	// Before any start, start.status answers unknown.
	raw, err := nodeCall(t, n, proto.MethodStartStatus, proto.StartStatusParams{OpID: opID})
	if err != nil {
		t.Fatal(err)
	}
	var st proto.StartStatus
	if err := json.Unmarshal(raw, &st); err != nil || st.State != proto.StartStatusUnknown || st.SessionID != "" {
		t.Fatalf("before start: %s %v", raw, err)
	}

	// Start in flight: start.status answers pending and a concurrent duplicate start waits rather than spawning twice.
	type startRes struct {
		raw json.RawMessage
		err error
	}
	firstDone := make(chan startRes, 1)
	go func() {
		r, e := nodeCall(t, n, "session.start", map[string]any{
			"cwd":      n.home,
			"launcher": "dsh",
			"prompt":   "hello",
			"op_id":    opID,
		})
		firstDone <- startRes{r, e}
	}()
	select {
	case <-startedCh:
	case res := <-firstDone:
		t.Fatalf("first start ended before reaching runtime: %s %v", res.raw, res.err)
	}

	raw, err = nodeCall(t, n, proto.MethodStartStatus, proto.StartStatusParams{OpID: opID})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &st); err != nil || st.State != proto.StartStatusPending {
		t.Fatalf("while pending: %s %v", raw, err)
	}

	dupDone := make(chan startRes, 1)
	go func() {
		r, e := nodeCall(t, n, "session.start", map[string]any{
			"cwd":      n.home,
			"launcher": "dsh",
			"prompt":   "hello",
			"op_id":    opID,
		})
		dupDone <- startRes{r, e}
	}()

	close(releaseCh)
	res1 := <-firstDone
	res2 := <-dupDone
	if res1.err != nil || res2.err != nil {
		t.Fatalf("starts failed: first=%v dup=%v", res1.err, res2.err)
	}
	if got := startCalls.Load(); got != 1 {
		t.Fatalf("runtime session.start called %d times, want 1", got)
	}

	var out1, out2 struct {
		Session proto.Session `json:"session"`
	}
	if err := json.Unmarshal(res1.raw, &out1); err != nil || out1.Session.SessionID != "sess-rt-1" {
		t.Fatalf("first reply: %s %v", res1.raw, err)
	}
	if err := json.Unmarshal(res2.raw, &out2); err != nil || out2.Session.SessionID != "sess-rt-1" {
		t.Fatalf("dup reply: %s %v", res2.raw, err)
	}

	// After completion, start.status answers started with the session id, and state.put recorded StartOpID.
	raw, err = nodeCall(t, n, proto.MethodStartStatus, proto.StartStatusParams{OpID: opID})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &st); err != nil || st.State != proto.StartStatusStarted || st.SessionID != "sess-rt-1" {
		t.Fatalf("after start: %s %v", raw, err)
	}
	saved := persisted.Load()
	if saved == nil || saved.StartOpID != opID {
		t.Fatalf("persisted state missing StartOpID: %+v", saved)
	}

	// A later state.put without StartOpID preserves the recorded StartOpID.
	n.state.put("sess-rt-1", sessionState{Launcher: "dsh", Cwd: n.home, Pane: "w1:p9"})
	if cur, ok := n.state.get("sess-rt-1"); !ok || cur.StartOpID != opID {
		t.Fatalf("state.put lost StartOpID: %+v", cur)
	}

	// Simulate node restart: fresh Node loads persisted sessionState via state.load (empty in-memory startOps).
	n2, hub2 := runtimeNode(t)
	assistantHub(hub2)
	n2.state.beginLoad()
	loaded := make(chan struct{})
	statusDone := make(chan startRes, 1)
	go func() {
		r, e := nodeCall(t, n2, proto.MethodStartStatus, proto.StartStatusParams{OpID: opID})
		statusDone <- startRes{r, e}
	}()
	// Complete state.load with the persisted state from before restart.
	n2.state.merge(map[string]sessionState{"sess-rt-1": *saved})
	n2.state.endLoad()
	close(loaded)
	<-loaded
	afterRestart := <-statusDone
	if afterRestart.err != nil {
		t.Fatal(afterRestart.err)
	}
	st = proto.StartStatus{}
	if err := json.Unmarshal(afterRestart.raw, &st); err != nil || st.State != proto.StartStatusStarted || st.SessionID != "sess-rt-1" {
		t.Fatalf("after restart start.status: %s %v", afterRestart.raw, err)
	}

	// Repeating session.start with the same op_id on the restarted node returns the existing session without launching.
	var restartCalls atomic.Int32
	rt2 := dialRuntime(t, n2, hello)
	t.Cleanup(func() { _ = rt2.conn.Close() })
	rt2.answer = func(method string, _ json.RawMessage) (any, error) {
		if method == "session.start" {
			restartCalls.Add(1)
		}
		return map[string]any{"ok": true}, nil
	}
	againRaw, err := nodeCall(t, n2, "session.start", map[string]any{
		"cwd":      n2.home,
		"launcher": "dsh",
		"prompt":   "hello",
		"op_id":    opID,
	})
	if err != nil {
		t.Fatal(err)
	}
	var againOut struct {
		Session proto.Session `json:"session"`
	}
	if err := json.Unmarshal(againRaw, &againOut); err != nil || againOut.Session.SessionID != "sess-rt-1" {
		t.Fatalf("restarted duplicate start: %s %v", againRaw, err)
	}
	if restartCalls.Load() != 0 {
		t.Fatalf("restarted node spawned a duplicate session (%d calls)", restartCalls.Load())
	}
}

func TestStartOpIDFailedStartReleasesOpForRetry(t *testing.T) {
	n, hub := runtimeNode(t)
	assistantHub(hub)
	hello := dshHello()
	rt := dialRuntime(t, n, hello)
	t.Cleanup(func() { _ = rt.conn.Close() })

	var calls atomic.Int32
	rt.answer = func(method string, _ json.RawMessage) (any, error) {
		if method == "session.start" {
			if calls.Add(1) == 1 {
				return nil, &rpc.Error{Code: "runtime_refused", Message: "temporary failure"}
			}
			return map[string]any{
				"session": map[string]any{"session_id": "sess-retry-1", "cwd": n.home, "launcher": "dsh", "status": "idle"},
			}, nil
		}
		return map[string]any{"ok": true}, nil
	}

	const opID = "22222222-2222-4222-8222-222222222222"
	if _, err := nodeCall(t, n, "session.start", map[string]any{
		"cwd": n.home, "launcher": "dsh", "prompt": "try", "op_id": opID,
	}); err == nil {
		t.Fatal("first start unexpectedly succeeded")
	}

	// Failed start must leave start.status as unknown (never poisoning op_id).
	raw, err := nodeCall(t, n, proto.MethodStartStatus, proto.StartStatusParams{OpID: opID})
	if err != nil {
		t.Fatal(err)
	}
	var st proto.StartStatus
	if err := json.Unmarshal(raw, &st); err != nil || st.State != proto.StartStatusUnknown {
		t.Fatalf("after failure: %s %v", raw, err)
	}

	// Retry with the same op_id runs and succeeds.
	raw, err = nodeCall(t, n, "session.start", map[string]any{
		"cwd": n.home, "launcher": "dsh", "prompt": "try", "op_id": opID,
	})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Session proto.Session `json:"session"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Session.SessionID != "sess-retry-1" {
		t.Fatalf("retry reply: %s %v", raw, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls=%d, want 2", calls.Load())
	}
}

func TestStartStatusRequiresOpID(t *testing.T) {
	n := &Node{state: newState()}
	_, err := n.startStatus(context.Background(), json.RawMessage(`{}`))
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != "invalid_params" {
		t.Fatalf("expected invalid_params, got %v", err)
	}
}
