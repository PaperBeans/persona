package hub

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nwverse/persona/server/internal/extension"
)

func TestAssistantCardActionsDenyExtensionTokens(t *testing.T) {
	h := newTestHub(t)
	mux := h.routes()
	token := extTokensOf(t, h).issue("example-extension", "Example", []string{extension.ScopeAssistantAsk, extension.ScopeAgentsRead, extension.ScopeAgentsControl})
	status, body := call(t, mux, "POST", "/api/v1/assistant/conversations/example/views/view_example/actions", token, `{"action":"yes","client_msg_id":"example"}`)
	if status != http.StatusForbidden || body["code"] != "permission_denied" {
		t.Fatalf("extension card action: %d %v", status, body)
	}
	status, body = call(t, mux, "POST", "/api/v1/assistant/conversations/example/views/view_example/html-actions", token,
		`{"kind":"open","component_key":"chart","expected_version":1,"url":"https://example.com","client_msg_id":"example","confirmed":true}`)
	if status != http.StatusForbidden || body["code"] != "permission_denied" {
		t.Fatalf("extension html action: %d %v", status, body)
	}
}

// The assistant's cards are always on (#5509): hello.features lists both card interfaces, and the assistant's hello
// still says both switches on for a manager from before, whatever a hub.json that still has the retired keys says.
func TestAssistantCardsAlwaysOn(t *testing.T) {
	h := newTestHub(t)
	hello, err := assistantLink{h}.Hello(context.Background())
	if err != nil || !hello.PersonaView || !hello.HTMLViews {
		t.Fatalf("hello %+v %v", hello, err)
	}
	features := h.features()
	if !slices.Contains(features, "assistant_views") || !slices.Contains(features, "assistant_html_bridge") {
		t.Fatalf("features %v", features)
	}
}

// A hub.json that still has the retired keys parses, and they are named for the start-up log.
func TestRetiredAssistantKeys(t *testing.T) {
	for _, tc := range []struct {
		file string
		want []string
	}{
		{`{"assistant":{"persona_view":false,"html_views":false,"external":true}}`, []string{"assistant.persona_view", "assistant.html_views"}},
		{`{"assistant":{"persona_view":true}}`, []string{"assistant.persona_view"}},
		{`{"assistant":{"external":true}}`, nil},
		{`{"persona_view":true}`, nil},
		{`{"assistant":"not an object"}`, nil},
		{`not json`, nil},
	} {
		if got := retiredKeysIn([]byte(tc.file)); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.file, got, tc.want)
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hub.json"), []byte(`{"token":"example","assistant":{"persona_view":false,"html_views":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfigFile(dir); err != nil {
		t.Fatalf("hub.json with the retired keys: %v", err)
	}
}
