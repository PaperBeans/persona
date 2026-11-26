//go:build unix

package node

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/host"
)

func TestCodexSlowCardTailKeepsTerminalStatusAvailable(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	n := &Node{hosts: host.NewRegistry(&screenHost{bareHost: bareHost{kind: host.Herdr}, screen: "› \n"})}
	read := make(chan struct{})
	go func() {
		n.cx.asks("slow", fifo)
		close(read)
	}()
	// There is no writer until cleanup, so the rollout open cannot finish yet.
	t.Cleanup(func() {
		f, err := os.OpenFile(fifo, os.O_RDWR, 0)
		if err != nil {
			t.Error(err)
			return
		}
		defer f.Close()
		<-read
	})
	waitFor(t, "slow card tail to begin", func() bool {
		if !n.cx.mu.TryLock() {
			return true
		}
		defer n.cx.mu.Unlock()
		return n.cx.tails["slow"] != nil
	})

	healthy := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(healthy, []byte(cxSyncCall+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	working := quickMeta{turnOpen: true, turnActivityAt: now}
	result := make(chan bool, 1)
	go func() {
		// A blocked terminal with an unanswered question must remain blocked.
		result <- n.codexRolloutWorkingOverBlocked(context.Background(), "healthy", "w1:p1", healthy, working, now.Add(-time.Minute), now)
		result <- n.codexRolloutWorkingOverBlocked(context.Background(), "slow", "w1:p2", fifo, working, now.Add(-time.Minute), now)
	}()
	for _, session := range []string{"healthy", "slow"} {
		select {
		case changed := <-result:
			if changed {
				t.Fatalf("%s terminal lost its blocked state", session)
			}
		case <-time.After(time.Second):
			t.Fatalf("slow card read stalled status lookup for %s terminal", session)
		}
	}
}

func TestCodexStatusKeepsBlockedOnFirstSlowCardRead(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	var cards codexCards
	result := make(chan bool, 1)
	go func() { result <- cards.hasBlockingAsk("slow", fifo) }()
	t.Cleanup(func() {
		f, err := os.OpenFile(fifo, os.O_RDWR, 0)
		if err != nil {
			t.Error(err)
			return
		}
		defer f.Close()
		tail := cards.tail("slow", fifo)
		tail.mu.Lock()
		tail.mu.Unlock() // the pending OS read finished before the directory goes away
	})
	select {
	case blocked := <-result:
		if !blocked {
			t.Fatal("an unread rollout cleared the blocked state")
		}
	case <-time.After(time.Second):
		t.Fatal("the status check waited for the filesystem")
	}
}
