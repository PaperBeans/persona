package devices

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestCurrentConnectionListedRejectsGraceAndStaleSnapshots(t *testing.T) {
	h := newTestDevices(t)
	m := newMachine(h, MachineConfig{ID: "box", Name: "box"})
	m.reconnectGrace = time.Hour
	h.addEntry(m)

	if h.CurrentConnectionListed("nobody") {
		t.Fatal("unknown machine reported CurrentConnectionListed=true")
	}
	if h.CurrentConnectionListed("box") {
		t.Fatal("disconnected machine reported CurrentConnectionListed=true")
	}

	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	peer := rpc.New("h", c1)
	defer peer.Close(rpc.ErrClosed)

	// Retained pre-reconnect snapshot makes Shown.Listed=true while sessionsListed=false.
	m.mu.Lock()
	m.peer = peer
	m.online = true
	m.sessions = []proto.Session{{SessionID: "stale-only"}}
	m.sessionsListed = false
	m.mu.Unlock()
	if s, ok := h.Shown("box"); !ok || !s.Listed {
		t.Fatalf("expected Shown.Listed=true on retained snapshot: %+v ok=%v", s, ok)
	}
	if h.CurrentConnectionListed("box") {
		t.Fatal("retained pre-reconnect snapshot satisfied CurrentConnectionListed")
	}

	// Completing sessions.list on the current live connection satisfies CurrentConnectionListed.
	m.mu.Lock()
	m.sessionsListed = true
	m.mu.Unlock()
	if !h.CurrentConnectionListed("box") {
		t.Fatal("live listed connection reported CurrentConnectionListed=false")
	}

	// Reload grace must fail closed as uncertain.
	m.mu.Lock()
	m.reloadUntil = time.Now().Add(time.Minute)
	m.mu.Unlock()
	if h.CurrentConnectionListed("box") {
		t.Fatal("reload grace satisfied CurrentConnectionListed")
	}
	m.mu.Lock()
	m.reloadUntil = time.Time{}
	m.warmUntil = time.Now().Add(time.Minute)
	m.mu.Unlock()
	if h.CurrentConnectionListed("box") {
		t.Fatal("warm grace satisfied CurrentConnectionListed")
	}
	m.mu.Lock()
	m.warmUntil = time.Time{}
	m.mu.Unlock()

	// Closing the peer invalidates CurrentConnectionListed immediately.
	_ = c2.Close()
	peer.Close(rpc.ErrClosed)
	<-peer.Done()
	if h.CurrentConnectionListed("box") {
		t.Fatal("closed peer satisfied CurrentConnectionListed")
	}

	// Reconnect grace after detach keeps Shown.Listed=true but CurrentConnectionListed=false.
	m.detach(errors.New("node exited"), false)
	if s, _ := h.Shown("box"); !s.Listed || s.Until.IsZero() {
		t.Fatalf("expected reconnect grace Shown: %+v", s)
	}
	if h.CurrentConnectionListed("box") {
		t.Fatal("detached grace window satisfied CurrentConnectionListed")
	}
}
