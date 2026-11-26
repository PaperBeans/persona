package node

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestEvictFilesKeepsLiveAssistantProcs verifies that running headless assistant
// processes registered in n.procs are never evicted from n.files when opening
// more than maxFiles transcripts, while exited procs and offline sessions are
// evicted in LRU order.
func TestEvictFilesKeepsLiveAssistantProcs(t *testing.T) {
	n, home := testNode(t)
	dir := filepath.Join(home, ".claude", "projects", "-work")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	id := func(i int) string { return fmt.Sprintf("22222222-3333-4444-5555-%012d", i) }

	const (
		liveCount = 3
		extra     = 10
	)
	total := maxFiles + extra
	for i := 0; i < total; i++ {
		writeCodexToolLines(t, filepath.Join(dir, id(i)+".jsonl"), 0, 1)
	}

	exited := make(chan struct{})
	close(exited)

	n.mu.Lock()
	for i := 0; i < liveCount; i++ {
		n.procs[id(i)] = &assistantProc{id: id(i), agent: "claude", exited: make(chan struct{})}
	}
	// An exited process still present in n.procs is not live and must remain evictable.
	n.procs[id(liveCount)] = &assistantProc{id: id(liveCount), agent: "claude", exited: exited}
	n.mu.Unlock()

	for i := 0; i < total; i++ {
		if _, err := n.file(id(i)); err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.files) != maxFiles {
		t.Fatalf("kept %d parsed transcripts, want %d", len(n.files), maxFiles)
	}
	for i := 0; i < liveCount; i++ {
		if n.files[id(i)] == nil {
			t.Fatalf("live headless assistant proc %s was evicted", id(i))
		}
	}
	if n.files[id(liveCount)] != nil {
		t.Fatalf("exited headless assistant proc %s was not evicted", id(liveCount))
	}
	// Oldest evictable entries id(liveCount)..id(liveCount+extra-1) are dropped;
	// newest offline sessions stay.
	for i := liveCount; i < liveCount+extra; i++ {
		if n.files[id(i)] != nil {
			t.Fatalf("oldest offline session %s (index %d) was not evicted", id(i), i)
		}
	}
	for i := liveCount + extra; i < total; i++ {
		if n.files[id(i)] == nil {
			t.Fatalf("recent offline session %s (index %d) was evicted", id(i), i)
		}
	}

	t.Run("all_live_terminates", func(t *testing.T) {
		n2, home2 := testNode(t)
		dir2 := filepath.Join(home2, ".claude", "projects", "-work")
		if err := os.MkdirAll(dir2, 0o700); err != nil {
			t.Fatal(err)
		}
		liveTotal := maxFiles + 2
		for i := 0; i < liveTotal; i++ {
			sid := id(1000 + i)
			writeCodexToolLines(t, filepath.Join(dir2, sid+".jsonl"), 0, 1)
			n2.mu.Lock()
			n2.procs[sid] = &assistantProc{id: sid, agent: "claude", exited: make(chan struct{})}
			n2.mu.Unlock()
			if _, err := n2.file(sid); err != nil {
				t.Fatalf("open live %d: %v", i, err)
			}
		}
		n2.mu.Lock()
		defer n2.mu.Unlock()
		if len(n2.files) != liveTotal {
			t.Fatalf("kept %d live assistant transcripts, want %d", len(n2.files), liveTotal)
		}
	})
}
