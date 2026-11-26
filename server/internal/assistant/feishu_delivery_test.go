package assistant

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Feishu has received the post, but has yet to return its message ID when the actual node disconnects.
func TestFeishuReconnectWhileFirstCardPostAwaitsAcknowledgement(t *testing.T) {
	needsLarkLayer(t)
	tests := []struct {
		name        string
		movedOn     bool
		unconfirmed bool
		expired     bool
		updates     int
	}{
		{name: "same terminal question", updates: 2},
		{name: "agent moved on", movedOn: true, updates: 1},
		{name: "post returned no message ID", unconfirmed: true},
		{name: "the reconnect grace elapsed", expired: true, updates: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ft, n := newCardFleet(t, false)
			seen := feishuCardsOn(ft)
			received, release := make(chan struct{}), make(chan struct{})
			started := sync.OnceFunc(func() { close(received) })
			allow := sync.OnceFunc(func() { close(release) })
			t.Cleanup(allow)
			ft.f.answerAppBlocking(AppFeishu, FeishuPost, func(json.RawMessage) (any, error) {
				started()
				<-release
				if tc.unconfirmed {
					return nil, errors.New("no confirmed message id")
				}
				return map[string]any{"message_id": "om_card"}, nil
			})
			n.setScreen(pickModel)
			blockSession(ft, proto.Session{SessionID: "s1", Agent: "claude"})
			select {
			case <-received:
			case <-time.After(2 * time.Second):
				t.Fatal("Feishu did not receive the original post")
			}

			before, _ := waitCards(ft.s, 1, 2*time.Second)
			if len(before) != 1 {
				t.Fatalf("expected one live terminal dialog: %v", before)
			}
			if tc.movedOn {
				ft.d.setSessions("mac", []proto.Session{{SessionID: "s1", Agent: "claude", Status: proto.StatusUnknown}})
			}
			ft.s.NodeDetached("mac")
			if tc.movedOn {
				ft.sessions("mac", proto.Session{SessionID: "s1", Agent: "claude", Status: proto.StatusWorking})
			} else {
				after, _ := waitCards(ft.s, 1, 2*time.Second)
				for id := range before {
					if _, ok := after[id]; !ok || len(after) != 1 {
						t.Fatalf("reconnect restored different dialog: %v -> %v", before, after)
					}
				}
			}
			if tc.expired {
				later := ft.s.larkNow().Add(resumeGrace + time.Second)
				ft.s.lark.mu.Lock()
				ft.s.lark.now = func() time.Time { return later }
				ft.s.lark.mu.Unlock()
			}
			allow()
			var updates []string
			if tc.updates == 2 {
				_, updates = waitFeishu(seen, 2*time.Second, func(_ int, u []string) bool { return len(u) >= 2 })
			} else {
				// Give the same close/reopen path time to expose an unwanted update, as in the ordinary reconnect test.
				_, updates = waitFeishu(seen, 500*time.Millisecond, func(_ int, u []string) bool { return len(u) > tc.updates })
			}
			if len(updates) != tc.updates {
				t.Fatalf("wanted %d Feishu updates, got %q", tc.updates, updates)
			}
			if len(updates) > 0 && !strings.Contains(updates[0], "已结束") {
				t.Fatalf("first edit did not close the original card: %s", updates[0])
			}
			if len(updates) == 2 && (!strings.Contains(updates[1], "gpt-6") || strings.Contains(updates[1], "已结束")) {
				t.Fatalf("last edit did not ask the same live question: %s", updates[1])
			}
			if posts := len(ft.f.appParams(AppFeishu, FeishuPost)); posts != 1 {
				t.Fatalf("posted %d messages", posts)
			}
			for _, update := range ft.f.appParams(AppFeishu, FeishuUpdateCard) {
				if update["message_id"] != "om_card" {
					t.Fatalf("edited a different message: %v", update["message_id"])
				}
			}
			if keys := ft.d.callsTo("mac", "session.keys"); len(keys) != 0 {
				t.Fatalf("restoring a card sent terminal input: %v", keys)
			}
		})
	}
}
