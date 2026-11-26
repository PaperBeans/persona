package extmgr

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/extension"
)

func TestInboxConfigViewCarriesVisibilityFromManagerConfig(t *testing.T) {
	visibility := &assistantwire.InboxVisibilityConfig{Sources: []string{"lark"}, Chats: []string{"synthetic-private"}}
	h := &Manager{cfg: &Config{Inbox: InboxConfig{Visibility: visibility}}}
	got := h.inboxConfigView(context.Background()).Visibility
	if got != visibility {
		t.Fatalf("inbox visibility = %#v, want the manager's configured boundary %#v", got, visibility)
	}
	if len(got.Chats) != 1 || got.Chats[0] != "synthetic-private" || got.DirectChats {
		t.Fatalf("inbox visibility changed in transit: %#v", got)
	}
}

func TestInboxVisibilityInitializeConfigMergesWithoutPersisting(t *testing.T) {
	base := json.RawMessage(`{"inbox_exclude_chats":["synthetic-local"],"ordinary":true}`)
	visibility := json.RawMessage(`{"sources":["lark"],"chats":["synthetic-manager"]}`)
	merged, err := inboxVisibilityInitializeConfig(base, visibility)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Local      []string        `json:"inbox_exclude_chats"`
		Ordinary   bool            `json:"ordinary"`
		Visibility json.RawMessage `json:"inbox_visibility"`
	}
	if err := json.Unmarshal(merged, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Local) != 1 || got.Local[0] != "synthetic-local" || !got.Ordinary || string(got.Visibility) != string(visibility) {
		t.Fatalf("initialize config lost local settings or central visibility: %s", merged)
	}
	if string(base) != `{"inbox_exclude_chats":["synthetic-local"],"ordinary":true}` {
		t.Fatalf("the registry's config bytes were modified: %s", base)
	}
	unchanged, err := inboxVisibilityInitializeConfig(base, nil)
	if err != nil || string(unchanged) != string(base) {
		t.Fatalf("empty visibility changed legacy config: %s, %v", unchanged, err)
	}
	if _, err := inboxVisibilityInitializeConfig(json.RawMessage(`not-json`), visibility); err == nil {
		t.Fatal("malformed extension config was accepted with a privacy boundary")
	}
}

func TestInboxVisibilityInitializeRejectsOlderSelectedProvider(t *testing.T) {
	visibility := &assistantwire.InboxVisibilityConfig{Sources: []string{"lark"}, Chats: []string{"synthetic-private"}}
	older := []extension.ConnectorItem{{ID: "lark", Capabilities: []string{"inbox", "tools"}}}
	if err := validateInboxVisibilityCapabilities(visibility, older); err == nil {
		t.Fatal("an older provider that ignores the boundary was allowed to start source collection")
	}
	capable := []extension.ConnectorItem{{ID: "lark", Capabilities: []string{"inbox", "tools", "inbox_visibility"}}}
	if err := validateInboxVisibilityCapabilities(visibility, capable); err != nil {
		t.Fatalf("a provider with source-side preflight was rejected: %v", err)
	}
	if err := validateInboxVisibilityCapabilities(visibility, []extension.ConnectorItem{{ID: "other"}}); err != nil {
		t.Fatalf("an unselected provider was rejected: %v", err)
	}
	if err := validateInboxVisibilityCapabilities(&assistantwire.InboxVisibilityConfig{Sources: []string{"lark"}}, older); err != nil {
		t.Fatalf("empty visibility controls changed legacy provider initialization: %v", err)
	}
	if err := validateInboxVisibilityCapabilities(nil, older); err != nil {
		t.Fatalf("nil visibility changed legacy provider initialization: %v", err)
	}
}

func TestInboxVisibilityInitializeTargetsOnlySelectedConnector(t *testing.T) {
	visibility := &assistantwire.InboxVisibilityConfig{Sources: []string{"lark"}, Chats: []string{"synthetic-private"}}
	raw := inboxVisibilityForConnectors(visibility, []extension.ConnectorItem{{ID: "other"}, {ID: "lark"}})
	var got assistantwire.InboxVisibilityConfig
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("selected connector did not receive visibility: %v", err)
	}
	if len(got.Chats) != 1 || got.Chats[0] != "synthetic-private" {
		t.Fatalf("initialize visibility = %#v", got)
	}
	if raw := inboxVisibilityForConnectors(visibility, []extension.ConnectorItem{{ID: "other"}}); raw != nil {
		t.Fatalf("unselected extension received private chat ids: %s", raw)
	}
	if raw := inboxVisibilityForConnectors(nil, []extension.ConnectorItem{{ID: "lark"}}); raw != nil {
		t.Fatalf("nil visibility changed legacy initialize params: %s", raw)
	}
}
