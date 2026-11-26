package devices

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func reloadBackoffFixture(t *testing.T, onRequest func(context.Context, string, json.RawMessage) (any, error)) (*Devices, *Machine) {
	t.Helper()
	d := newTestDevices(t)
	m := newMachine(d, MachineConfig{ID: "example-node", Name: "Example node", Kind: kindLink})
	m.online = true
	m.info = proto.NodeInfo{
		OS:           "linux",
		Arch:         "amd64",
		Version:      "test",
		Protocol:     nodeproto.Version,
		Capabilities: append([]string(nil), nodeproto.Required...),
	}
	d.addEntry(m)

	toNode, nodeIn := io.Pipe()
	toHub, hubIn := io.Pipe()
	m.peer = rpc.New("hub", nodeIn)
	nodePeer := rpc.New("node", hubIn)
	m.peer.OnEvent = m.onEvent
	nodePeer.OnRequest = onRequest

	ctx, cancel := context.WithCancel(context.Background())
	d.mu.Lock()
	d.ctx = ctx
	d.mu.Unlock()

	hubDone := make(chan struct{})
	nodeDone := make(chan struct{})
	go func() {
		defer close(hubDone)
		_ = m.peer.Serve(ctx, toHub)
	}()
	go func() {
		defer close(nodeDone)
		_ = nodePeer.Serve(ctx, toNode)
	}()
	t.Cleanup(func() {
		cancel()
		_ = toNode.Close()
		_ = nodeIn.Close()
		_ = toHub.Close()
		_ = hubIn.Close()
		<-hubDone
		<-nodeDone
	})
	return d, m
}

// Regression test for #14449: under sustained node_restarting refusals across the default
// 20s reloadRetryBudget, Machine.callWithSnapshot must back off so total RPC attempts stay
// sharply bounded instead of hammering the node every 100ms (~198 attempts per caller).
func TestReloadBackoffBoundedUnderSustainedRestarting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var attempts atomic.Int32
		d, m := reloadBackoffFixture(t, func(_ context.Context, method string, _ json.RawMessage) (any, error) {
			if method == "session.items" {
				attempts.Add(1)
				return nil, &rpc.Error{Code: nodeproto.CodeRestarting, Message: nodeproto.CodeRestarting}
			}
			return map[string]any{"ok": true}, nil
		})

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		started := time.Now()
		_, err := d.CallNode(ctx, m.cfg.ID, "session.items", json.RawMessage(`{"session_id":"example-session"}`))
		elapsed := time.Since(started)

		if !errors.Is(err, rpc.ErrNotSent) || !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			t.Fatalf("sustained node_restarting returned %v (caller err=%v), want rpc.ErrNotSent + context.DeadlineExceeded", err, ctx.Err())
		}
		if elapsed < reloadRetryBudget {
			t.Fatalf("call gave up early after %s, want at least full budget %s", elapsed, reloadRetryBudget)
		}
		got := attempts.Load()
		if got < 2 || got > 25 {
			t.Fatalf("sustained node_restarting made %d RPC attempts over %s (want bounded backoff between 2 and 25)", got, elapsed)
		}
	})
}

// Companion check for #14449: exponential backoff must still start promptly so brief
// resident reloads recover well under 1s with exact attempt counts.
func TestReloadFastRecoveryOnBriefRestarting(t *testing.T) {
	cases := []struct {
		name     string
		refusals int32
	}{
		{name: "single-refusal", refusals: 1},
		{name: "two-refusals", refusals: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var attempts atomic.Int32
				d, m := reloadBackoffFixture(t, func(_ context.Context, method string, _ json.RawMessage) (any, error) {
					if method == "session.items" {
						if attempts.Add(1) <= tc.refusals {
							return nil, &rpc.Error{Code: nodeproto.CodeRestarting, Message: nodeproto.CodeRestarting}
						}
						return map[string]any{"items": []any{}, "has_more": false}, nil
					}
					return map[string]any{"ok": true}, nil
				})

				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				started := time.Now()
				_, err := d.CallNode(ctx, m.cfg.ID, "session.items", json.RawMessage(`{"session_id":"example-session"}`))
				elapsed := time.Since(started)

				if err != nil {
					t.Fatalf("brief node_restarting did not recover: %v", err)
				}
				wantAttempts := tc.refusals + 1
				if got := attempts.Load(); got != wantAttempts {
					t.Fatalf("attempts=%d, want %d", got, wantAttempts)
				}
				if elapsed > 500*time.Millisecond {
					t.Fatalf("brief reload recovery took %s, want <= 500ms", elapsed)
				}
			})
		})
	}
}

// Regression test for #14449: concurrent callers hitting sustained node_restarting must all back off
// instead of multiplying a 100ms fixed-interval retry storm across callers.
func TestReloadBackoffConcurrentCallersBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var attempts atomic.Int32
		d, m := reloadBackoffFixture(t, func(_ context.Context, method string, _ json.RawMessage) (any, error) {
			if method == "session.items" {
				attempts.Add(1)
				return nil, &rpc.Error{Code: nodeproto.CodeRestarting, Message: nodeproto.CodeRestarting}
			}
			return map[string]any{"ok": true}, nil
		})

		const callers = 5
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		var wg sync.WaitGroup
		errs := make([]error, callers)
		for i := range callers {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				_, errs[idx] = d.CallNode(ctx, m.cfg.ID, "session.items", json.RawMessage(`{"session_id":"example-session"}`))
			}(i)
		}
		wg.Wait()

		for i, err := range errs {
			if !errors.Is(err, rpc.ErrNotSent) || !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("caller %d returned %v, want rpc.ErrNotSent + context.DeadlineExceeded", i, err)
			}
		}
		got := attempts.Load()
		if got < callers*2 || got > callers*25 {
			t.Fatalf("concurrent sustained node_restarting made %d RPC attempts across %d callers (want between %d and %d)", got, callers, callers*2, callers*25)
		}
	})
}
