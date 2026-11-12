package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/db"
)

func TestViewReplyValidatedByServer(t *testing.T) {
	raw := []byte(`{"fallback":"Form","sections":[{"items":[{"type":"field","name":"priority","label":"优先级","kind":"choice","choices":[{"value":"normal"},{"value":"high"}]},{"type":"field","name":"notify","label":"通知","kind":"toggle"},{"type":"button","title":"Submit","action":{"id":"submit","reply":"Server reply","submit":true,"confirm":"Confirm?"}}]}]}`)
	v, err := wire.ParseAssistantView(raw)
	if err != nil {
		t.Fatal(err)
	}
	base := viewActionInput{Action: "submit", Values: map[string]string{"priority": "high", "notify": "true"}, ClientMsgID: "client", Confirmed: true, ExpectedVersion: 1}
	text, values, err := viewReplyMessage(v, `item#0/1<"`, base)
	if err != nil || !strings.HasPrefix(text, "Server reply") || !strings.Contains(text, "优先级：high") || !strings.Contains(text, `&lt;&#34;`) || !json.Valid(values) {
		t.Fatalf("%s %s %v", text, values, err)
	}
	for _, change := range []func(*viewActionInput){
		func(in *viewActionInput) { in.Confirmed = false },
		func(in *viewActionInput) { in.ExpectedVersion = 0 }, // confirm binds the version the user saw
		func(in *viewActionInput) { in.Action = "unknown" },
		func(in *viewActionInput) { in.Values = map[string]string{"priority": "urgent", "notify": "true"} },
		func(in *viewActionInput) { in.Values = map[string]string{"priority": "normal", "notify": "yes"} },
		func(in *viewActionInput) {
			in.Values = map[string]string{"priority": "normal", "notify": "true", "password": "secret"}
		},
		func(in *viewActionInput) { in.Values = map[string]string{"priority": "normal"} },
	} {
		in := base
		change(&in)
		if _, _, err := viewReplyMessage(v, "key", in); err == nil {
			t.Fatal("invalid form accepted")
		}
	}
}

func TestViewReplyIdempotentResponse(t *testing.T) {
	a := db.AssistantViewActionRow{Key: "key", Action: "yes", ClientMsgID: "client", Values: json.RawMessage(`{"a":"b"}`), Status: "sent", At: time.Now()}
	for _, tc := range []struct {
		in     viewActionInput
		status int
	}{
		{viewActionInput{Action: "yes", ClientMsgID: "client", Values: map[string]string{"a": "b"}}, 200},
		{viewActionInput{Action: "yes", ClientMsgID: "other", Values: map[string]string{"a": "b"}}, 409},
		{viewActionInput{Action: "no", ClientMsgID: "client", Values: map[string]string{"a": "b"}}, 409},
		{viewActionInput{Action: "yes", ClientMsgID: "client", Values: map[string]string{"a": "changed"}}, 409},
		{viewActionInput{Action: "yes", ClientMsgID: "client", Values: map[string]string{"a": "b"}, ExpectedVersion: 3}, 200}, // stored before versions
	} {
		w := httptest.NewRecorder()
		writeViewAction(w, a, tc.in)
		if w.Code != tc.status {
			t.Fatalf("status %d, wanted %d: %s", w.Code, tc.status, w.Body.String())
		}
	}
}

func TestViewActionRouteUsesStoredReplyAndPreservesOutbox(t *testing.T) {
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
	f := newFakeHub().withMachine()
	conversation := "actions-" + randomHex(8)
	s := newServiceOn(t, f, database)
	at := time.Now().UTC()
	id := "view_" + randomHex(8)
	v := db.AssistantViewRow{ID: id, Conversation: conversation, MachineID: "server", Version: 1, CreatedAt: at, UpdatedAt: at, View: json.RawMessage(`{"fallback":"Choose","sections":[{"items":[{"type":"button","title":"Yes","action":{"id":"yes","reply":"Trusted reply"}}]}]}`)}
	feed := db.FeedRow{ID: "feed_" + id, Conversation: conversation, Kind: "view", Ref: id, At: at}
	if err := database.CreateAssistantView(ctx, v, feed); err != nil {
		t.Fatal(err)
	}
	request := func(body, role string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/v1/assistant/conversations/"+conversation+"/views/"+id+"/actions", strings.NewReader(body))
		if role != "" {
			r.Header.Set(RoleHeader, role)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	if w := request(`{"action":"yes","client_msg_id":"client","reply":"forged"}`, ""); w.Code != 400 {
		t.Fatalf("client reply override: %d %s", w.Code, w.Body.String())
	}
	if w := request(`{"action":"yes","client_msg_id":"client"}`, "answer"); w.Code != http.StatusForbidden {
		t.Fatalf("read-only: %d", w.Code)
	}
	body := `{"action":"yes","client_msg_id":"client"}`
	if w := request(body, ""); w.Code != 200 {
		t.Fatalf("submit %d %s", w.Code, w.Body.String())
	}
	a, err := database.AssistantViewAction(ctx, conversation, id)
	if err != nil || a == nil || !strings.HasPrefix(a.Text, "Trusted reply") || a.MessageID == "client" {
		t.Fatalf("%+v %v", a, err)
	}
	if w := request(body, ""); w.Code != 200 {
		t.Fatalf("retry %d %s", w.Code, w.Body.String())
	}
	if w := request(`{"action":"yes","client_msg_id":"other"}`, ""); w.Code != 409 {
		t.Fatalf("second device %d %s", w.Code, w.Body.String())
	}
	list, err := database.AssistantViewActions(ctx, conversation)
	if err != nil || len(list) != 1 {
		t.Fatalf("%+v %v", list, err)
	}
	// Wait for the bounded delivery attempt before closing its test database.
	deadline := time.Now().Add(3 * time.Second)
	for {
		current, err := database.AssistantViewAction(ctx, conversation, id)
		if err != nil {
			t.Fatal(err)
		}
		if current.Error != "" {
			if current.Status != "pending" {
				t.Fatalf("missing source delivery: %+v", current)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("delivery attempt did not settle")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The fake has no such live session: delivery must stay pending, never start a new conversation.
	if f.sent() != 0 {
		t.Fatal("missing source conversation received a message")
	}
}
