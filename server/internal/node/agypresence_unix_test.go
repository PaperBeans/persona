//go:build unix

package node

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
)

// agyPresenceLockFile is the presence lock an agy in a terminal holds for id under dir, held until the test ends or
// release is called.
func agyPresenceLockFile(t *testing.T, dir, id string) (release func()) {
	t.Helper()
	p := filepath.Join(dir, "presence", id+".lock")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	done := false
	release = func() {
		if !done {
			done = true
			_ = f.Close()
		}
	}
	t.Cleanup(release)
	return release
}

// Reproduces #14561: a starting agy CLI tries LOCK_EX|LOCK_NB once and never retries. Probing presence/<id>.lock in a
// tight loop must never take flock (not even LOCK_SH|LOCK_NB), so a concurrent LOCK_EX|LOCK_NB starter never sees EAGAIN.
func TestAgyPresenceProbeNeverBlocksLockExStarter(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "presence", agyTestID+".lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(lockPath, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var stop atomic.Bool
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		close(started)
		for !stop.Load() {
			_ = agyPresenceHeld(dir, agyTestID)
		}
	}()
	<-started

	const attempts = 5000
	var collisions int
	for range attempts {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err != nil {
			if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
				collisions++
				continue
			}
			stop.Store(true)
			<-done
			t.Fatalf("unexpected flock error: %v", err)
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
			stop.Store(true)
			<-done
			t.Fatalf("unlock failed: %v", err)
		}
	}
	stop.Store(true)
	<-done

	if collisions > 0 {
		t.Fatalf("agyPresenceHeld caused %d/%d LOCK_EX|LOCK_NB EAGAIN collisions for a starting agy CLI (#14561)", collisions, attempts)
	}
}
