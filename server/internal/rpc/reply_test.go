package rpc

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

type heldReply struct {
	entered chan struct{}
	allow   chan struct{}
	once    sync.Once
}

func (w *heldReply) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.allow
	return len(p), nil
}

func TestAfterReplyWaitsForWriteEvenWhenCallerCancels(t *testing.T) {
	writer := &heldReply{entered: make(chan struct{}), allow: make(chan struct{})}
	peer := New("n", writer)
	peer.TrackReplies = true
	released := make(chan struct{})
	peer.OnRequest = func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
		if !AfterReply(ctx, func() { close(released) }) {
			t.Error("reply hook was not registered")
		}
		return map[string]bool{"ok": true}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan struct{})
	go func() { defer close(finished); peer.handle(ctx, message{ID: "h1", Method: "write"}) }()
	select {
	case <-writer.entered:
	case <-time.After(time.Second):
		t.Fatal("reply did not start")
	}
	cancel()
	select {
	case <-released:
		t.Fatal("an admitted operation was released before its reply write")
	default:
	}
	close(writer.allow)
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("reply completion did not release the operation")
	}
	<-finished
}

func TestAfterReplyRequiresTrackedHandlerContext(t *testing.T) {
	if AfterReply(context.Background(), func() { t.Error("unexpected hook") }) {
		t.Fatal("a direct handler call pretended to have a reply boundary")
	}
}
