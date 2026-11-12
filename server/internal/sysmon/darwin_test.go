//go:build darwin

package sysmon

import (
	"context"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/sys/unix"
)

// fakeModelCommands stands in for ioreg and system_profiler: ioreg prints out (nothing: an Intel Mac), and
// system_profiler answers with the fixture once release closes, or fails when its context ends (#6028).
type fakeModelCommands struct {
	ioreg   []byte
	release chan struct{}

	mu  sync.Mutex
	ran []string
}

func (f *fakeModelCommands) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.ran = append(f.ran, name)
	f.mu.Unlock()
	switch name {
	case "ioreg":
		return f.ioreg, nil
	case "system_profiler":
		select {
		case <-f.release:
			return []byte("Hardware:\n\n    Hardware Overview:\n\n      Model Name: MacBook Pro\n"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, nil
}

func (f *fakeModelCommands) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.ran)
}

// A system_profiler that takes its time (over 10 s on a Mac whose Activation Lock check does not answer) holds the
// first read for modelWait at most and later ones not at all; once it answers, the name is there for every read.
func TestModelLookupDoesNotWaitForSystemProfiler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeModelCommands{release: make(chan struct{})}
		l := newModelLookup(f.run)
		ctx := context.Background()

		start := time.Now()
		if m := l.get(ctx); m != "" {
			t.Fatalf("first read = %q while system_profiler runs", m)
		}
		if waited := time.Since(start); waited != modelWait {
			t.Fatalf("the first read waited %v, want %v", waited, modelWait)
		}
		start = time.Now()
		if m := l.get(ctx); m != "" || time.Since(start) != 0 {
			t.Fatalf("a later read = %q after %v, want none at once", m, time.Since(start))
		}

		close(f.release)
		synctest.Wait()
		start = time.Now()
		if m := l.get(ctx); m != "MacBook Pro" || time.Since(start) != 0 {
			t.Fatalf("read after system_profiler answered = %q after %v", m, time.Since(start))
		}
		if got := f.commands(); !slices.Equal(got, []string{"ioreg", "system_profiler"}) {
			t.Fatalf("ran %v, want ioreg then system_profiler once", got)
		}
	})
}

// On Apple silicon the IORegistry has the name: system_profiler never runs and nothing waits.
func TestModelLookupTakesTheIORegistryName(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeModelCommands{ioreg: fixture(t, "ioreg_product"), release: make(chan struct{})}
		l := newModelLookup(f.run)
		start := time.Now()
		if m := l.get(context.Background()); m != "Mac Studio" || time.Since(start) != 0 {
			t.Fatalf("read = %q after %v", m, time.Since(start))
		}
		if got := f.commands(); !slices.Equal(got, []string{"ioreg"}) {
			t.Fatalf("ran %v, want ioreg only", got)
		}
	})
}

// A system_profiler that never answers is given up after profilerTimeout, and the identifier stays.
func TestModelLookupGivesUpOnAHungSystemProfiler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeModelCommands{release: make(chan struct{})}
		l := newModelLookup(f.run)
		start := time.Now()
		_ = l.get(context.Background())
		<-l.done
		if took := time.Since(start); took != profilerTimeout {
			t.Fatalf("the lookup ended after %v, want %v", took, profilerTimeout)
		}
		if m := l.get(context.Background()); m != "" {
			t.Fatalf("read = %q after system_profiler was cut off", m)
		}
	})
}

// ReadSystem answers with the identifier while system_profiler is still out, and with the name once it is in.
func TestReadSystemFallsBackToTheIdentifierWhileTheNameIsOut(t *testing.T) {
	hw, err := unix.Sysctl("hw.model")
	if err != nil || hw == "" {
		t.Skipf("hw.model: %q, %v", hw, err)
	}
	synctest.Test(t, func(t *testing.T) {
		f := &fakeModelCommands{release: make(chan struct{})}
		l := newModelLookup(f.run)
		if s := readSystem(context.Background(), l); s.Model != hw || s.Name == "" || s.Cores == 0 {
			t.Fatalf("system while system_profiler runs = %+v, want model %q", s, hw)
		}
		close(f.release)
		synctest.Wait()
		if s := readSystem(context.Background(), l); s.Model != "MacBook Pro" {
			t.Fatalf("model after system_profiler answered = %q", s.Model)
		}
	})
}
