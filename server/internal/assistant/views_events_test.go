package assistant

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
)

type viewEventHub struct {
	*fakeHub
	views chan wire.AssistantView
}

func (f *viewEventHub) Broadcast(ctx context.Context, b Broadcast) {
	f.fakeHub.Broadcast(ctx, b)
	var event struct {
		Type string             `json:"type"`
		View wire.AssistantView `json:"view"`
	}
	if json.Unmarshal(b.Message, &event) == nil && event.Type == "assistant_view" {
		select {
		case f.views <- event.View:
		default:
		}
	}
}

func TestViewNotificationsCoalesceLatestVersion(t *testing.T) {
	s, f, _ := newViewService(t)
	hub := &viewEventHub{fakeHub: f, views: make(chan wire.AssistantView, 4)}
	setHub(s, hub)
	for _, version := range []int64{1, 3, 2} {
		s.viewChanged(wire.AssistantView{ID: "view_example", Conversation: "conversation-a", Version: version})
	}
	select {
	case v := <-hub.views:
		if v.Version != 3 || v.Conversation != "conversation-a" {
			t.Fatalf("event %+v", v)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("coalesced event timed out")
	}
	s.viewEvents.mu.Lock()
	pending := len(s.viewEvents.pending)
	s.viewEvents.mu.Unlock()
	if pending != 0 {
		t.Fatal("event left queued")
	}
}
