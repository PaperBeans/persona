package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// An idle conversation's batch switch is accepted at once, and its handoff waits for the conversation's next message
// (#14488): that message reaches the target with the summary of what went before, once; the one after it goes as
// written.
func TestConversationSwitchIdleItemKeepsTheHandoffForTheNextMessage(t *testing.T) {
	r := newConversationSwitchTestRig(t)
	ctx := context.Background()
	var mu sync.Mutex
	var sent []map[string]any
	r.f.answerNode(DeviceSessionSend, func(_ string, raw json.RawMessage) (any, error) {
		var p map[string]any
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		mu.Lock()
		sent = append(sent, p)
		mu.Unlock()
		return map[string]any{"ok": true, "session_id": p["session_id"]}, nil
	})
	to := func(id string) []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		var out []map[string]any
		for _, p := range sent {
			if p["session_id"] == id {
				out = append(out, p)
			}
		}
		return out
	}
	send := func(text, id string) {
		t.Helper()
		if err := r.s.call(ctx, "server", DeviceSessionSend, map[string]any{"session_id": "fresh-chat", "text": text, "client_msg_id": id}, nil); err != nil {
			t.Fatal(err)
		}
	}

	code, raw := conversationSwitchTestCall(t, r.s, http.MethodPost, "/api/v1/assistant/conversation-switches", "", `{"conversation_ids":["fresh-chat"],"launcher":"fallback","model":"new-model"}`)
	started := conversationSwitchTestDecode(t, code, raw)
	conversationSwitchTestDrive(t, r.s, started.BatchID, map[string]string{"fresh-chat": conversationSwitchSwitched})
	if got := to("prepared-chat-1"); len(got) != 0 {
		t.Fatalf("the switch sent something by itself: %+v", got)
	}

	send("the owner's next message", "owner-1")
	waitFor(t, "the next message on the target", func() bool {
		r.s.logicalSweep(ctx)
		return len(to("prepared-chat-1")) == 1
	})
	first := to("prepared-chat-1")[0]
	if text := fmt.Sprint(first["text"]); first["client_msg_id"] != "owner-1" || !strings.Contains(text, "<persona-context") || !strings.Contains(text, "the owner's next message") {
		t.Fatalf("the next message did not carry the handoff: %+v", first)
	}

	send("and one more", "owner-2")
	waitFor(t, "the message after it", func() bool {
		r.s.logicalSweep(ctx)
		return len(to("prepared-chat-1")) == 2
	})
	if second := to("prepared-chat-1")[1]; second["client_msg_id"] != "owner-2" || strings.Contains(fmt.Sprint(second["text"]), "<persona-context") {
		t.Fatalf("the handoff went twice: %+v", second)
	}
	if got := to("fresh-chat"); len(got) != 0 {
		t.Fatalf("a message reached the retired source: %+v", got)
	}
}
