package assistant

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestInlineViewsUseOnlyTrustedAssistantItems(t *testing.T) {
	connection := os.Getenv("PERSONA_TEST_DATABASE_URL")
	if connection == "" {
		t.Skip("PERSONA_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	database, err := db.Open(ctx, connection, db.ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	f := newFakeHub().withMachine()
	conversation := "inline-" + randomHex(8)
	f.setSessions("server", proto.Session{SessionID: conversation, Assistant: true}, proto.Session{SessionID: "ordinary", Assistant: false})
	s := newServiceOn(t, f, database)
	card := `{"fallback":"Choose","sections":[{"items":[{"type":"button","title":"Yes","action":{"id":"yes","reply":"Trusted inline reply"}}]}]}`
	text := "```persona-view\ninvalid\n```\n~~~persona-view\n" + card + "\n~~~\n"
	items := []proto.Item{
		{ID: "user#0", Role: "user", Kind: proto.KindText, Text: text},
		{ID: "tool#0", Role: "assistant", Kind: proto.KindToolResult, Text: text},
		{ID: "example#0", Role: "assistant", Kind: proto.KindText, Text: "````markdown\n```persona-view\n" + card + "\n```\n````"},
		{ID: "partial#0", Role: "assistant", Kind: proto.KindText, Text: "```persona-view\n" + card},
		{ID: "truncated#0", Role: "assistant", Kind: proto.KindText, Text: text, FullSize: 100000},
		{ID: "trusted#0", Role: "assistant", Kind: proto.KindText, Text: text},
	}
	s.SessionItems(ctx, SessionItems{MachineID: "server", SessionID: conversation, Items: items})
	keys, err := database.AssistantViewBlockKeys(ctx, conversation)
	if err != nil || len(keys) != 1 || keys[0] != "trusted#0/1" {
		t.Fatalf("keys %v %v", keys, err)
	}
	if strings.Count(f.eventTypes(), "assistant_view_block") != 1 {
		t.Fatalf("registration events %q", f.eventTypes())
	}
	s.SessionItems(ctx, SessionItems{MachineID: "server", SessionID: conversation, Items: items})
	if strings.Count(f.eventTypes(), "assistant_view_block") != 1 {
		t.Fatalf("replay broadcast another registration: %q", f.eventTypes())
	}
	s.SessionItems(ctx, SessionItems{MachineID: "server", SessionID: "ordinary", Items: items})
	keys, err = database.AssistantViewBlockKeys(ctx, "ordinary")
	if err != nil || len(keys) != 0 {
		t.Fatalf("ordinary keys %v %v", keys, err)
	}
	path := "/api/v1/assistant/conversations/" + conversation + "/views/" + url.PathEscape("trusted#0/1") + "/actions"
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(`{"action":"yes","client_msg_id":"client"}`)))
	if w.Code != 200 {
		t.Fatalf("escaped key submit %d %s", w.Code, w.Body.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		a, err := database.AssistantViewAction(ctx, conversation, "trusted#0/1")
		if err != nil {
			t.Fatal(err)
		}
		if a != nil && a.Status != "pending" {
			if !strings.HasPrefix(a.Text, "Trusted inline reply") {
				t.Fatalf("message %q", a.Text)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("inline action delivery did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Source errors are diagnostics, not another executable view; valid content retains its block index.
	feedback, err := database.PeekAssistantViewErrors(ctx, conversation, "server", 5)
	if err != nil || len(feedback) < 1 {
		t.Fatalf("feedback %v %v", feedback, err)
	}
	var action map[string]any
	if json.Unmarshal(w.Body.Bytes(), &action) != nil || action["action"] == nil {
		t.Fatal("no action result")
	}
}

type inlineFullHub struct {
	*fakeHub
	full proto.Item
}

func (h inlineFullHub) DeviceCall(ctx context.Context, p assistantwire.DeviceCall) (json.RawMessage, error) {
	if p.Method == "session.item" {
		return json.Marshal(h.full)
	}
	return h.fakeHub.DeviceCall(ctx, p)
}

func TestInlineTurnRegistersWithoutAnAppAndReadsFullText(t *testing.T) {
	connection := os.Getenv("PERSONA_TEST_DATABASE_URL")
	if connection == "" {
		t.Skip("PERSONA_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	database, err := db.Open(ctx, connection, db.ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	f := newFakeHub().withMachine()
	conversation := "unwatched-" + randomHex(8)
	f.setSessions("server", proto.Session{SessionID: conversation, Assistant: true}, proto.Session{SessionID: "ordinary"})
	f.setItems(proto.Item{ID: "long#0", Role: "assistant", Kind: proto.KindText, Text: "A long conclusion before the card", FullSize: 100000})
	h := inlineFullHub{fakeHub: f, full: proto.Item{ID: "long#0", Role: "assistant", Kind: proto.KindText, Text: "Conclusion\n```persona-view\n{\"fallback\":\"Finished\",\"sections\":[]}\n```\n"}}
	s := withSettings(t, f, New(Options{Hub: h, Store: func() Store { return database }, Logf: testLogf(t)}))
	ref := SessionRef{MachineID: "server", SessionID: conversation}
	s.registerInlineTurn(ctx, SessionRef{MachineID: "server", SessionID: "ordinary"})
	keys, err := database.AssistantViewBlockKeys(ctx, "ordinary")
	if err != nil || len(keys) != 0 {
		t.Fatalf("ordinary turn keys %v %v", keys, err)
	}
	s.registerInlineTurn(ctx, ref)
	keys, err = database.AssistantViewBlockKeys(ctx, conversation)
	if err != nil || len(keys) != 1 || keys[0] != "long#0/0" {
		t.Fatalf("full reply keys %v %v", keys, err)
	}
}
