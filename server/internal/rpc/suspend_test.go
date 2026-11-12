package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"
)

func TestSuspendCallsDrainsOldRepliesAndRefusesUnsentWork(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	manager, node := New("h", a), New("n", b)
	started, release := make(chan struct{}), make(chan struct{})
	node.OnRequest = func(context.Context, string, json.RawMessage) (any, error) {
		close(started)
		<-release
		return map[string]bool{"ok": true}, nil
	}
	go manager.Serve(ctx, a)
	go node.Serve(ctx, b)
	result := make(chan error, 1)
	go func() { result <- manager.Call(ctx, "operation", nil, nil) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	manager.SuspendCalls()
	if err := manager.Call(ctx, "must-not-be-sent", nil, nil); !errors.Is(err, ErrSuspended) {
		t.Fatalf("suspended call: %v", err)
	}
	manager.mu.Lock()
	pending := len(manager.pending)
	manager.mu.Unlock()
	if pending != 1 {
		t.Fatalf("pending calls=%d; refused work was registered", pending)
	}
	close(release)
	if err := manager.WaitPending(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	manager.ResumeCalls()
}
