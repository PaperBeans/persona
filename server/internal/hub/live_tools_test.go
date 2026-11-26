package hub

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestLiveToolSnapshotsFollowTheWatchedSession(t *testing.T) {
	h := newTestHub(t)
	m := newMachine(h, MachineConfig{ID: "box"})
	m.online = true
	registerMachine(h, m)
	key := [2]string{"box", "s1"}
	clientFor := func(watch, feature bool) *client {
		c := &client{role: roleFull, out: make(chan []byte, 32), subs: map[[2]string]bool{}, urlFeatures: map[string]bool{proto.FeatureSessionLiveTools: feature}}
		addTestClient(h, c)
		if watch {
			c.subs[key] = true
			m.watcherJoined("s1")
		}
		return c
	}
	want := func(c *client, state proto.LiveToolState) {
		t.Helper()
		for len(c.out) > 0 {
			var frame liveToolsPush
			raw := <-c.out
			if err := json.Unmarshal(raw, &frame); err != nil {
				t.Fatal(err)
			}
			if frame.Type != "live_tools" {
				continue
			}
			if frame.MachineID != "box" || frame.SessionID != "s1" || !reflect.DeepEqual(frame.LiveToolState, state) {
				t.Fatalf("unexpected tools frame: %s", raw)
			}
			return
		}
		t.Fatal("missing tools frame")
	}
	first := clientFor(true, true)
	legacy := clientFor(true, false)
	other := clientFor(false, true)
	empty := proto.LiveToolState{Tools: []proto.LiveTool{}}
	active := proto.LiveToolState{Tools: []proto.LiveTool{{ID: "call-a", Name: "Read"}}, Summary: "Checking the source", Active: true}
	m.onEvent("session.live_tools", json.RawMessage(`{"session_id":"s1","tools":[{"id":"call-a","name":"Read","arguments":"not on the wire"}],"summary":"Checking the source","active":true}`))
	want(first, active)
	if len(legacy.out) != 0 || len(other.out) != 0 {
		t.Fatal("tools sent to the wrong subscription")
	}
	h.onClientMessage(context.Background(), other, []byte(`{"type":"subscribe","machine_id":"box","session_id":"s1"}`))
	want(other, active)
	h.onClientHello(legacy, []byte(`{"features":["session_live_tools"]}`))
	want(legacy, active)
	m.onEvent("session.live_tools", json.RawMessage(`{"session_id":"s1","tools":[],"thinking":true,"active":true}`))
	think := proto.LiveToolState{Tools: []proto.LiveTool{}, Thinking: true, Active: true}
	want(first, think)
	want(other, think)
	want(legacy, think)
	// Missing tools is not an empty list and must not erase a valid snapshot.
	m.onEvent("session.live_tools", json.RawMessage(`{"session_id":"s1"}`))
	m.replayLiveTools(first, "s1")
	want(first, think)
	h.connectionEnded(m, "test disconnect")
	want(first, empty)
	want(other, empty)
	want(legacy, empty)
	m.replayLiveTools(first, "s1")
	if len(first.out) > 0 {
		t.Fatal("disconnected snapshot replayed")
	}
	m.onEvent("session.live_tools", json.RawMessage(`{"session_id":"s1","tools":[{"id":"call-a","name":"Read"}],"active":true}`))
	for _, c := range []*client{first, other, legacy} {
		h.onClientMessage(context.Background(), c, []byte(`{"type":"unsubscribe","machine_id":"box","session_id":"s1"}`))
	}
	m.onEvent("session.live_tools", json.RawMessage(`{"session_id":"s1","tools":[{"id":"late","name":"late"}]}`))
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.liveTools) > 0 {
		t.Fatal("live tools survived the last unwatch or a late frame")
	}
}
