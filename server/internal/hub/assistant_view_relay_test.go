package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/db"
)

func TestAssistantInlineCardActionSurvivesHTTPBusRelay(t *testing.T) {
	connection := os.Getenv("PERSONA_TEST_DATABASE_URL")
	if connection == "" {
		t.Skip("PERSONA_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	database, err := db.Open(ctx, connection, db.AllSets)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	h := newTestHub(t)
	h.store = database
	connectAssistant(t, h)
	mux := h.routes()
	for _, item := range []string{"item", "trusted#0", "escaped%value?"} {
		t.Run(item, func(t *testing.T) {
			conversation := "relay-" + randomHex(8)
			key := wire.AssistantBlockKey(item, 1)
			at := time.Now().UTC()
			view := json.RawMessage(`{"fallback":"Choose","sections":[{"items":[{"type":"button","title":"Yes","action":{"id":"yes","reply":"Selected"}}]}]}`)
			block := db.AssistantViewBlockRow{Conversation: conversation, Key: key, MachineID: "example-machine", ItemID: item, Index: 1, View: view, CreatedAt: at}
			if _, err := database.RegisterAssistantViewBlock(ctx, block); err != nil {
				t.Fatal(err)
			}
			action := db.AssistantViewActionRow{Conversation: conversation, Key: key, MachineID: block.MachineID, Action: "yes", ClientMsgID: "client", MessageID: "relay-message-" + conversation, Text: "Selected", At: at}
			if _, first, err := database.BeginAssistantViewAction(ctx, action, 1); err != nil || !first {
				t.Fatalf("reserve: %v %v", first, err)
			}
			path := "/api/v1/assistant/conversations/" + conversation + "/views/" + url.PathEscape(key) + "/actions"
			status, body := call(t, mux, "POST", path, h.cfg.Token, `{"action":"yes","client_msg_id":"client"}`)
			if status != http.StatusOK {
				t.Fatalf("relay: %d %v", status, body)
			}
			got, ok := body["action"].(map[string]any)
			if !ok || got["key"] != key || got["client_msg_id"] != "client" {
				t.Fatalf("key changed through relay: %v", body)
			}
		})
	}
}
