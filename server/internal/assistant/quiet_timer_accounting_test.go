package assistant

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestQuietTimerStopDrainsRegisteredCallback(t *testing.T) {
	st := &blockingRelayStore{
		memStore: &memStore{},
		started:  make(chan struct{}),
		release:  make(chan struct{}),
	}
	s, _ := newQuietServiceOn(t, st)
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(st.release) }) })
	s.relays.mu.Lock()
	s.relays.loaded = false
	s.relays.mu.Unlock()
	st.mu.Lock()
	st.block = true
	st.mu.Unlock()

	s.quietNotify(context.Background(), Todo{ID: "quiet-drain", Title: "Quiet callback", Origin: "conv1", Quiet: true}, "box", "done", "", "Verified", nil)
	select {
	case <-st.started:
	case <-time.After(5 * time.Second):
		t.Fatal("registered quiet callback did not start")
	}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		s.stopQuietTimers()
	}()
	waitFor(t, "quiet timer shutdown to close admission", func() bool {
		s.quiet.mu.Lock()
		defer s.quiet.mu.Unlock()
		return s.quiet.stopped
	})
	select {
	case <-drained:
		t.Fatal("quiet timer shutdown returned before its registered callback finished")
	default:
	}
	release.Do(func() { close(st.release) })
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("quiet timer shutdown did not drain its released callback")
	}
	s.quiet.mu.Lock()
	n := len(s.quiet.timers)
	s.quiet.mu.Unlock()
	if n != 0 {
		t.Fatalf("in-flight callback rearmed %d quiet timers after shutdown", n)
	}
}
