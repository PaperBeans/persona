package node

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCodexCompletedTurnDoesNotReviveAfterStartResponse(t *testing.T) {
	n := codexTestNode(t)
	withFakeHub(t, n)
	p, c, f := newFakeCodex(t, n, t.TempDir())
	p.id, c.threadID = testThread, testThread
	n.procs[testThread] = p
	n.state.put(testThread, sessionState{Launcher: "codex", Cwd: p.cwd, OriginKnown: true})
	// A real turn finished before Persona observed either its start response or started notification.
	p.busy = false
	c.notification("turn/completed", json.RawMessage(`{"threadId":"`+testThread+`","turn":{"id":"turn-1","status":"completed"}}`))

	// Its duplicate started notification cannot make that same completed turn active again.
	c.notification("turn/started", json.RawMessage(`{"threadId":"`+testThread+`","turn":{"id":"turn-1","status":"inProgress"}}`))
	p.mu.Lock()
	busy := p.busy
	p.mu.Unlock()
	if busy || c.turn() != "" {
		t.Fatalf("late started revived a completed turn: busy=%v turn=%q", busy, c.turn())
	}
	f.mu.Lock()
	f.results["turn/start"] = map[string]any{"turn": map[string]any{"id": "turn-1"}}
	f.mu.Unlock()
	if err := c.deliver(context.Background(), "", "next", nil); err != nil {
		t.Fatal(err)
	}
	if got := len(f.sent("turn/steer")); got != 0 {
		t.Fatalf("a completed turn received %d steers", got)
	}
	if got := len(f.sent("turn/start")); got != 1 {
		t.Fatalf("next message starts = %d, want 1", got)
	}
	if c.turn() != "" {
		t.Fatalf("start response restored completed turn %q", c.turn())
	}
	p.mu.Lock()
	busy = p.busy
	p.mu.Unlock()
	if busy {
		t.Fatal("start response left the completed turn busy")
	}
}
