package memtrim

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A trim starts after a large body, not after a smaller one, and at most once per Every: of many bodies at once only
// one starts it, and the next after Every starts the next.
func TestAfterLargeAtMostOncePerEvery(t *testing.T) {
	var runs atomic.Int32
	done := make(chan struct{}, 64)
	orig, wait := free, settle
	free, settle = func() { runs.Add(1); done <- struct{}{} }, time.Millisecond
	t.Cleanup(func() { free, settle = orig, wait; last.Store(0) })

	AfterLarge(LargeBody - 1)
	if last.Load() != 0 {
		t.Fatal("a body under LargeBody started a trim")
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() { AfterLarge(LargeBody) })
	}
	wg.Wait()
	<-done
	time.Sleep(20 * time.Millisecond) // sleep-ok: room for a second trim that must not come
	if n := runs.Load(); n != 1 {
		t.Fatalf("32 large bodies at once started %d trims, want 1", n)
	}

	start := time.Unix(0, last.Load())
	for _, c := range []struct {
		at   time.Duration
		want bool
	}{{Every - time.Nanosecond, false}, {Every, true}, {Every + time.Second, false}, {2 * Every, true}} {
		if got := claim(start.Add(c.at)); got != c.want {
			t.Errorf("a claim %v after the first: %v, want %v", c.at, got, c.want)
		}
	}
}
