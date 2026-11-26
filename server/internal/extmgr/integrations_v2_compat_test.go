package extmgr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extwire"
)

// TestExtensionsSectionAndLegacyCoexistenceOnSameHub verifies that extensions declaring optional
// configuration section hints (connection/channel/inbox/advanced) and legacy extensions without any
// section metadata load, serialize, expose API detail forms, and persist settings side by side on
// the same Hub with zero conflicts (#14846, #14833, #14802).
func TestExtensionsSectionAndLegacyCoexistenceOnSameHub(t *testing.T) {
	h, _ := extensionTestHub(t)
	mux := h.routes()
	master := h.cfg.Token

	sectionedDir := declarativePackage(t, `{
		"manifest_version": 1,
		"id": "modern-suite",
		"name": "Modern Suite",
		"version": "1.0.0",
		"contributes": {
			"providers": [{"id": "modern-p", "name": "Modern Provider", "base_url": "https://modern.example.com"}]
		},
		"configuration": {
			"type": "object",
			"properties": {
				"endpoint": {"type": "string", "format": "uri", "title": "Endpoint", "section": "connection"},
				"thread_mode": {"type": "string", "enum": ["default", "per_topic"], "default": "default", "title": "Thread mode", "section": "channel"},
				"triage_rules": {"type": "string", "title": "Triage rules", "section": "inbox"},
				"tuning": {
					"type": "object",
					"title": "Tuning",
					"section": "advanced",
					"properties": {
						"retries": {"type": "integer", "minimum": 0, "maximum": 10, "default": 2, "section": "connection"},
						"note": {"type": "string"}
					}
				},
				"unsectioned_flag": {"type": "boolean", "default": false, "title": "Unsectioned flag"},
				"accounts": {
					"type": "array",
					"maxItems": 4,
					"section": "connection",
					"x-secret": {"name": "account_key", "title": "Account key"},
					"items": {
						"type": "object",
						"properties": {
							"address": {"type": "string", "format": "uri"}
						}
					}
				}
			}
		}
	}`)

	legacyDir := declarativePackage(t, `{
		"manifest_version": 1,
		"id": "legacy-suite",
		"name": "Legacy Suite",
		"version": "1.0.0",
		"contributes": {
			"providers": [{"id": "legacy-p", "name": "Legacy Provider", "base_url": "https://legacy.example.com"}]
		},
		"configuration": {
			"type": "object",
			"properties": {
				"webhook_url": {"type": "string", "format": "uri", "title": "Webhook URL"},
				"poll_minutes": {"type": "integer", "minimum": 1, "maximum": 60, "default": 5, "title": "Poll interval"},
				"enabled_tags": {"type": "array", "items": {"type": "string"}, "uniqueItems": true},
				"proxy": {
					"type": "object",
					"properties": {
						"host": {"type": "string"},
						"port": {"type": "integer", "default": 8080}
					}
				}
			}
		}
	}`)

	installForTest(t, h, sectionedDir)
	installForTest(t, h, legacyDir)

	// Invalid section manifest is rejected cleanly during preview without affecting already-loaded extensions.
	invalidSectionDir := declarativePackage(t, `{
		"manifest_version": 1,
		"id": "bad-section",
		"name": "Bad Section",
		"version": "1.0.0",
		"contributes": {
			"providers": [{"id": "bad-p", "name": "Bad Provider", "base_url": "https://bad.example.com"}]
		},
		"configuration": {
			"type": "object",
			"properties": {
				"endpoint": {"type": "string", "section": "unknown_section"}
			}
		}
	}`)
	if code, out := call(t, mux, http.MethodPost, "/api/v1/extensions/-/preview", master, `{"source":"dir:`+invalidSectionDir+`"}`); code != http.StatusBadRequest {
		t.Fatalf("invalid section preview: got %d (%v), want 400", code, out)
	}

	// Both extensions appear together on GET /api/v1/extensions.
	code, listOut := call(t, mux, http.MethodGet, "/api/v1/extensions", master, "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/v1/extensions: %d %v", code, listOut)
	}
	found := map[string]bool{}
	for _, raw := range listOut["extensions"].([]any) {
		item := raw.(map[string]any)
		found[item["id"].(string)] = true
	}
	if !found["modern-suite"] || !found["legacy-suite"] {
		t.Fatalf("GET /api/v1/extensions missing coexisting extensions: %v", found)
	}

	// Verify API detail passthrough across both modern and legacy detail routes.
	for _, tc := range []struct {
		path          string
		hasEntryLists bool
	}{
		{"/api/v1/extensions/modern-suite/-/", false},
		{"/api/v1/extensions/modern-suite/-/?entry_lists=1", true},
		{"/api/v1/plugins/modern-suite/-/", false},
	} {
		code, out := call(t, mux, http.MethodGet, tc.path, master, "")
		if code != http.StatusOK {
			t.Fatalf("GET %s: %d %v", tc.path, code, out)
		}
		form := out["form"].(map[string]any)
		fields := form["fields"].([]any)
		wantLen := 5
		if tc.hasEntryLists {
			wantLen = 6
		}
		if len(fields) != wantLen {
			t.Fatalf("GET %s fields len = %d, want %d", tc.path, len(fields), wantLen)
		}
		byName := map[string]map[string]any{}
		for _, item := range fields {
			m := item.(map[string]any)
			byName[m["name"].(string)] = m
		}
		if byName["endpoint"]["section"] != extension.SectionConnection ||
			byName["thread_mode"]["section"] != extension.SectionChannel ||
			byName["triage_rules"]["section"] != extension.SectionInbox ||
			byName["tuning"]["section"] != extension.SectionAdvanced {
			t.Fatalf("GET %s lost section metadata: %v", tc.path, byName)
		}
		if _, exists := byName["unsectioned_flag"]["section"]; exists {
			t.Fatalf("GET %s unexpectedly serialized section on unsectioned_flag: %v", tc.path, byName["unsectioned_flag"])
		}
		nested := byName["tuning"]["fields"].([]any)
		if nested[0].(map[string]any)["section"] != extension.SectionConnection {
			t.Fatalf("GET %s nested retries lost section: %v", tc.path, nested)
		}
		if _, exists := nested[1].(map[string]any)["section"]; exists {
			t.Fatalf("GET %s nested unsectioned note unexpectedly serialized section: %v", tc.path, nested)
		}
		if tc.hasEntryLists && byName["accounts"]["section"] != extension.SectionConnection {
			t.Fatalf("GET %s entry list accounts lost section: %v", tc.path, byName["accounts"])
		}
	}

	// Legacy extension detail serializes cleanly with zero "section" fields on any route.
	for _, path := range []string{"/api/v1/extensions/legacy-suite/-/", "/api/v1/plugins/legacy-suite/-/"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+master)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body.String())
		}
		var out struct {
			Form struct {
				Fields []map[string]any `json:"fields"`
			} `json:"form"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Form.Fields) != 4 {
			t.Fatalf("GET %s legacy fields len = %d, want 4", path, len(out.Form.Fields))
		}
		for _, field := range out.Form.Fields {
			if _, exists := field["section"]; exists {
				t.Fatalf("GET %s legacy field %q unexpectedly serialized section: %s", path, field["name"], rec.Body.String())
			}
			if nested, ok := field["fields"].([]any); ok {
				for _, sub := range nested {
					if _, exists := sub.(map[string]any)["section"]; exists {
						t.Fatalf("GET %s legacy nested field unexpectedly serialized section: %s", path, rec.Body.String())
					}
				}
			}
		}
	}

	// Saving configuration on both extensions independently works and survives registry reload without cross-talk.
	if code, out := call(t, mux, http.MethodPut, "/api/v1/extensions/modern-suite/-/config", master,
		`{"config":{"endpoint":"https://modern.example.com","thread_mode":"per_topic","triage_rules":"urgent","tuning":{"retries":4},"unsectioned_flag":true}}`); code != http.StatusOK {
		t.Fatalf("PUT modern-suite config: %d %v", code, out)
	}
	if code, out := call(t, mux, http.MethodPut, "/api/v1/extensions/legacy-suite/-/config", master,
		`{"config":{"webhook_url":"https://legacy.example.com/hook","poll_minutes":15,"enabled_tags":["ops"],"proxy":{"host":"proxy.example.com"}}}`); code != http.StatusOK {
		t.Fatalf("PUT legacy-suite config: %d %v", code, out)
	}

	if err := h.extensions.reg.Load(context.Background()); err != nil {
		t.Fatalf("reload registry: %v", err)
	}
	var modernCfg, legacyCfg map[string]any
	if err := json.Unmarshal(h.extensions.reg.SavedConfig(h.extensions.reg.Get("modern-suite")), &modernCfg); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(h.extensions.reg.SavedConfig(h.extensions.reg.Get("legacy-suite")), &legacyCfg); err != nil {
		t.Fatal(err)
	}
	if modernCfg["endpoint"] != "https://modern.example.com" || modernCfg["thread_mode"] != "per_topic" || modernCfg["unsectioned_flag"] != true {
		t.Fatalf("modern-suite saved config after reload = %v", modernCfg)
	}
	if legacyCfg["webhook_url"] != "https://legacy.example.com/hook" || legacyCfg["poll_minutes"] != float64(15) {
		t.Fatalf("legacy-suite saved config after reload = %v", legacyCfg)
	}

	// persona_settings discovery and reads work cleanly for both coexisting extensions.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	settingsList, err := h.hubCall(ctx, extwire.HubSettingsList, nil)
	if err != nil {
		t.Fatalf("HubSettingsList: %v", err)
	}
	listRaw, _ := json.Marshal(settingsList)
	for _, wantKey := range []string{`"key":"extension.modern-suite"`, `"key":"extension.legacy-suite"`} {
		if !strings.Contains(string(listRaw), wantKey) {
			t.Fatalf("HubSettingsList missing %s: %s", wantKey, listRaw)
		}
	}
	if got, err := h.toolPersonaSettings(ctx, "server", "s1", json.RawMessage(`{"action":"get","key":"extension.modern-suite"}`)); err != nil || !strings.Contains(got, "https://modern.example.com") {
		t.Fatalf("persona_settings get modern-suite: %q err=%v", got, err)
	}
	if got, err := h.toolPersonaSettings(ctx, "server", "s1", json.RawMessage(`{"action":"get","key":"extension.legacy-suite"}`)); err != nil || !strings.Contains(got, "https://legacy.example.com/hook") {
		t.Fatalf("persona_settings get legacy-suite: %q err=%v", got, err)
	}
}

// TestChannelReceiveCoexistenceLegacyAndStructuredPayloads verifies that a legacy channel extension
// sending only m.Thread and a modern channel extension sending structured chat_id/chat_kind/topic_id
// coexist cleanly on the same Hub and forward all fields intact to the assistant.
func TestChannelReceiveCoexistenceLegacyAndStructuredPayloads(t *testing.T) {
	w := newBotWorld(t)

	modernItem := channel.Item{
		ID:           "modernchat",
		Name:         "Modern Chat",
		Kind:         channel.KindIM,
		Capabilities: channel.Capabilities{Format: channel.FormatPlain, Threads: true},
	}
	legacyItem := channel.Item{
		ID:           "legacychat",
		Name:         "Legacy Chat",
		Kind:         channel.KindIM,
		Capabilities: channel.Capabilities{Format: channel.FormatPlain, Threads: true},
	}
	modernRaw, _ := json.Marshal([]channel.Item{modernItem})
	legacyRaw, _ := json.Marshal([]channel.Item{legacyItem})

	reg := extension.NewRegistry(t.TempDir(), nil, nil, 0)
	for _, m := range []*extension.Manifest{
		{
			ManifestVersion:  extension.ManifestVersion,
			ID:               "modern-chat",
			Name:             "Modern Chat",
			Version:          "1.0.0",
			Engines:          extension.Engines{ExtensionProtocol: extension.Protocol},
			ActivationEvents: []string{extension.OnStartup},
			Runtime:          &extension.Runtime{Command: extension.Command{Path: "modern-chat"}},
			Configuration:    json.RawMessage(`{"type":"object","properties":{"endpoint":{"type":"string","section":"connection"}}}`),
			Contributes:      map[string]json.RawMessage{"channel": modernRaw},
		},
		{
			ManifestVersion:  extension.ManifestVersion,
			ID:               "legacy-chat",
			Name:             "Legacy Chat",
			Version:          "1.0.0",
			Engines:          extension.Engines{ExtensionProtocol: extension.Protocol},
			ActivationEvents: []string{extension.OnStartup},
			Runtime:          &extension.Runtime{Command: extension.Command{Path: "legacy-chat"}},
			Configuration:    json.RawMessage(`{"type":"object","properties":{"endpoint":{"type":"string"}}}`),
			Contributes:      map[string]json.RawMessage{"channel": legacyRaw},
		},
	} {
		if err := reg.AddBuiltin(m, "/opt/"+m.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	w.h.extensions = newExtensions(t.TempDir(), nil, w.h.logf)
	w.h.extensions.reg = reg
	ch := &fakeChannel{}
	w.h.channels.call = ch.call
	w.h.channels.statuses = map[string]*channel.StatusParams{
		"modernchat": {State: channel.StateConnected, Account: &channel.Account{Name: "Modern"}},
		"legacychat": {State: channel.StateConnected, Account: &channel.Account{Name: "Legacy"}},
	}
	cw := &channelWorld{botWorld: w, ch: ch}

	// Legacy extension sends only m.Thread (omitting chat_id/chat_kind/topic_id).
	legacyPayload := json.RawMessage(`{"contribution":"legacychat","message":{"id":"m_leg","text":"legacy message","thread":"old_group:root_1"}}`)
	if _, err := w.h.extensionChannelCall("legacy-chat", channel.MReceive, legacyPayload); err != nil {
		t.Fatalf("legacy channel.receive: %v", err)
	}

	// Modern extension sends structured group/topic routing metadata alongside thread.
	modernPayload := json.RawMessage(`{"contribution":"modernchat","message":{"id":"m_mod","text":"topic message","chat_id":"oc_g1","chat_kind":"topic","topic_id":"omt_42","thread":"oc_g1:omt_42"}}`)
	if _, err := w.h.extensionChannelCall("modern-chat", channel.MReceive, modernPayload); err != nil {
		t.Fatalf("modern channel.receive: %v", err)
	}

	eventually(t, 5*time.Second, "both legacy and modern channel messages forwarded", func() bool {
		return len(cw.received()) == 2
	})

	byChannel := map[string]channel.Message{}
	for _, r := range cw.received() {
		var msg channel.Message
		if err := json.Unmarshal(r.Message, &msg); err != nil {
			t.Fatal(err)
		}
		byChannel[r.Channel] = msg
	}

	gotLegacy := byChannel["legacychat"]
	if gotLegacy.ID != "m_leg" || gotLegacy.Thread != "old_group:root_1" || gotLegacy.ChatID != "" || gotLegacy.ChatKind != "" || gotLegacy.TopicID != "" {
		t.Fatalf("legacy forwarded message = %+v", gotLegacy)
	}
	gotModern := byChannel["modernchat"]
	if gotModern.ID != "m_mod" || gotModern.ChatID != "oc_g1" || gotModern.ChatKind != channel.ChatKindTopic || gotModern.TopicID != "omt_42" || gotModern.Thread != "oc_g1:omt_42" {
		t.Fatalf("modern forwarded message = %+v", gotModern)
	}
}
