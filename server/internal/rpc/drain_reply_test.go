package rpc

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

type holdRequestWriteReturn struct {
	io.Writer
	release <-chan struct{}
}

func (w holdRequestWriteReturn) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	<-w.release
	return n, err
}

func TestReplyPublishedBeforeCloseWinsEvenBeforeCallResumes(t *testing.T) {
	for i := 0; i < 50; i++ {
		a, b := net.Pipe()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		release := make(chan struct{})
		var releaseOnce sync.Once
		releaseWrite := func() { releaseOnce.Do(func() { close(release) }) }
		t.Cleanup(func() { releaseWrite(); cancel(); _ = a.Close(); _ = b.Close() })
		manager, node := New("m", holdRequestWriteReturn{Writer: a, release: release}), New("n", b)
		handled := make(chan struct{})
		node.OnRequest = func(context.Context, string, json.RawMessage) (any, error) {
			close(handled)
			return map[string]bool{"completed": true}, nil
		}
		go func() { _ = manager.Serve(ctx, a) }()
		go func() { _ = node.Serve(ctx, b) }()
		var reply struct {
			Completed bool `json:"completed"`
		}
		done := make(chan error, 1)
		go func() { done <- manager.Call(ctx, "write", nil, &reply) }()
		// Hold Call before its receive select, then let the actual reply drain. Closure and the successful result
		// will both be ready when Call is released.
		select {
		case <-handled:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		if err := manager.WaitPending(ctx); err != nil {
			t.Fatal(err)
		}
		manager.Close(io.EOF)
		releaseWrite()
		select {
		case err := <-done:
			if err != nil || !reply.Completed {
				t.Fatalf("known reply lost after drain: error=%v reply=%+v", err, reply)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		cancel()
		_ = a.Close()
		_ = b.Close()
	}
}
