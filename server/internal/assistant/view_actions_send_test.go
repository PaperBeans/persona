package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

type viewSendHub struct {
	*fakeHub
	lock      sync.Mutex
	sendError error
	sent      []map[string]any
}

func (h *viewSendHub) DeviceCall(ctx context.Context, p DeviceCall) (json.RawMessage, error) {
	if p.Method != DeviceSessionSendOnce {
		return h.fakeHub.DeviceCall(ctx, p)
	}
	h.lock.Lock()
	defer h.lock.Unlock()
	var params map[string]any
	_ = json.Unmarshal(p.Params, &params)
	h.sent = append(h.sent, params)
	return json.RawMessage(`{}`), h.sendError
}

func TestViewOutboxDeliveryAndSafeRetry(t *testing.T) {
	url := os.Getenv("PERSONA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PERSONA_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	database, err := db.Open(ctx, url, db.ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	for _, tc := range []struct {
		name       string
		firstError error
		retry      bool
		want       string
		calls      int
	}{
		{"success", nil, false, "sent", 1},
		{"offline", apierr.ErrOffline, true, "sent", 2},
		{"uncertain", errors.New("delivery interrupted"), true, "failed", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conversation := "outbox-" + randomHex(8)
			id := "view_" + randomHex(8)
			at := time.Now().UTC()
			f := newFakeHub().withMachine()
			f.setSessions("server", proto.Session{SessionID: conversation, Assistant: true})
			hub := &viewSendHub{fakeHub: f, sendError: tc.firstError}
			s := withSettings(t, f, New(Options{Hub: hub, Store: func() Store { return database }, Logf: testLogf(t)}))
			v := db.AssistantViewRow{ID: id, Conversation: conversation, MachineID: "server", Version: 1, View: json.RawMessage(`{"fallback":"Example","sections":[]}`), CreatedAt: at, UpdatedAt: at}
			feed := db.FeedRow{ID: "feed_" + id, Conversation: conversation, Kind: "view", Ref: id, At: at}
			if err := database.CreateAssistantView(ctx, v, feed); err != nil {
				t.Fatal(err)
			}
			a := db.AssistantViewActionRow{Conversation: conversation, Key: id, Action: "yes", ClientMsgID: "client", MessageID: "message-" + randomHex(8), MachineID: "server", Text: "Trusted reply", At: at}
			if _, _, err := database.BeginAssistantViewAction(ctx, a, 1); err != nil {
				t.Fatal(err)
			}
			s.deliverViewAction(ctx, conversation, id)
			if tc.retry {
				hub.lock.Lock()
				hub.sendError = nil
				hub.lock.Unlock()
				s.deliverViewAction(ctx, conversation, id)
			}
			s.deliverViewAction(ctx, conversation, id)
			got, err := database.AssistantViewAction(ctx, conversation, id)
			if err != nil || got.Status != tc.want {
				t.Fatalf("%+v %v", got, err)
			}
			hub.lock.Lock()
			defer hub.lock.Unlock()
			if len(hub.sent) != tc.calls {
				t.Fatalf("delivery attempts: %d", len(hub.sent))
			}
			for _, params := range hub.sent {
				if params["client_msg_id"] != a.MessageID || params["session_id"] != conversation || params["text"] != a.Text || params["resume_only"] != true {
					t.Fatalf("send params %+v", params)
				}
			}
		})
	}
}
