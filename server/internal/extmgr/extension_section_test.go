package extmgr

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/i18n"
)

func TestExtensionSectionSurvivesFormCopiesAndDetailSerialization(t *testing.T) {
	s, err := extension.ParseSchema(json.RawMessage(`{"type":"object","properties":{
		"endpoint":{"type":"string","format":"uri","section":"connection"},
		"thread_mode":{"type":"string","enum":["default","per_topic"],"section":"channel"},
		"triage_rules":{"type":"string","section":"inbox"},
		"group":{"type":"object","section":"advanced","properties":{
			"timeout":{"type":"integer","section":"connection"},
			"plain":{"type":"string"}
		}},
		"legacy":{"type":"boolean","default":false},
		"accounts":{"type":"array","maxItems":4,"section":"connection","x-secret":{"name":"account_key","title":"Account key"},"items":{"type":"object","properties":{"address":{"type":"string","format":"uri"}}}}
	}}`))
	if err != nil {
		t.Fatal(err)
	}
	manifest := &extension.Manifest{
		Node:        &extension.NodeRuntime{Machines: "machines"},
		Permissions: extension.Permissions{Network: []string{"config:accounts[].address"}},
	}
	ts := extension.Translations{i18n.En: &extension.Translation{Configuration: map[string]*extension.FieldText{
		"endpoint":    {Title: "Endpoint URL"},
		"thread_mode": {Title: "Thread mode"},
		"triage_rules": {
			Title: "Triage rules",
		},
		"group": {Properties: map[string]*extension.FieldText{"timeout": {Title: "Timeout"}}},
	}}}
	localized := ts.Schema(i18n.En, s)
	form := withoutIPhonePhones(&extension.Extension{Record: extension.Record{ID: iphoneExtensionID}}, localized)
	form = entryNetworkForm(form, manifest)
	form = nodeConfirmForm(i18n.En, form, manifest, "Example")

	for _, tc := range []struct {
		path          string
		hasEntryLists bool
	}{
		{"/api/v1/extensions/example/-/", false},
		{"/api/v1/extensions/example/-/?entry_lists=1", true},
		{"/api/v1/admin/extensions/example", false},
		{"/api/v1/plugins/example/-/", false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			raw, err := json.Marshal(detailOut(httptest.NewRequest("GET", tc.path, nil), ExtensionDetail{Form: form}))
			if err != nil {
				t.Fatal(err)
			}
			var out struct {
				Form struct {
					Fields []map[string]any `json:"fields"`
				} `json:"form"`
			}
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatal(err)
			}
			wantCount := 5
			if tc.hasEntryLists {
				wantCount = 6
			}
			if len(out.Form.Fields) != wantCount {
				t.Fatalf("fields len = %d, want %d: %s", len(out.Form.Fields), wantCount, raw)
			}
			if out.Form.Fields[0]["section"] != extension.SectionConnection || out.Form.Fields[0]["title"] != "Endpoint URL" {
				t.Fatalf("endpoint field lost section/title: %s", raw)
			}
			if out.Form.Fields[1]["section"] != extension.SectionChannel {
				t.Fatalf("thread_mode field lost section: %s", raw)
			}
			if out.Form.Fields[2]["section"] != extension.SectionInbox {
				t.Fatalf("triage_rules field lost section: %s", raw)
			}
			if out.Form.Fields[3]["section"] != extension.SectionAdvanced {
				t.Fatalf("group field lost section: %s", raw)
			}
			nested := out.Form.Fields[3]["fields"].([]any)
			nestedTimeout := nested[0].(map[string]any)
			nestedPlain := nested[1].(map[string]any)
			if nestedTimeout["section"] != extension.SectionConnection || nestedTimeout["title"] != "Timeout" {
				t.Fatalf("nested timeout field lost section/title: %s", raw)
			}
			if _, exists := nestedPlain["section"]; exists {
				t.Fatalf("unset nested section should be omitted: %s", raw)
			}
			if _, exists := out.Form.Fields[4]["section"]; exists {
				t.Fatalf("unset legacy section should be omitted: %s", raw)
			}
			if tc.hasEntryLists && out.Form.Fields[5]["section"] != extension.SectionConnection {
				t.Fatalf("entry list field lost section: %s", raw)
			}
		})
	}
	if s.Fields[0].Title != "" || s.Fields[3].Fields[0].Title != "" {
		t.Fatal("form copies mutated shared schema")
	}
}

func TestExtensionDetailExposesConfigurationSection(t *testing.T) {
	h := extensionSettingsHub(t)
	m := &extension.Manifest{
		ManifestVersion: extension.ManifestVersion,
		ID:              "example-section",
		Name:            "Section Demo",
		Version:         "1.0.0",
		Engines:         extension.Engines{ExtensionProtocol: extension.Protocol},
		Configuration: json.RawMessage(`{"type":"object","properties":{
			"endpoint":{"type":"string","format":"uri","section":"connection"},
			"channel_mode":{"type":"string","section":"channel"},
			"triage_rules":{"type":"string","section":"inbox"},
			"debug":{"type":"boolean","section":"advanced"},
			"legacy":{"type":"string"}
		}}`),
	}
	if err := h.extensions.reg.AddBuiltin(m, "/opt/example", true); err != nil {
		t.Fatal(err)
	}
	detail := h.extensions.detail(context.Background(), h.extensions.reg.Get(m.ID))
	raw, err := json.Marshal(detailOut(httptest.NewRequest("GET", "/api/v1/extensions/example-section/-/", nil), detail))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Form extension.Schema `json:"form"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Form.Fields) != 5 {
		t.Fatalf("fields = %d, want 5: %s", len(out.Form.Fields), raw)
	}
	want := []string{extension.SectionConnection, extension.SectionChannel, extension.SectionInbox, extension.SectionAdvanced, ""}
	for i, w := range want {
		if out.Form.Fields[i].Section != w {
			t.Fatalf("field[%d] (%s) section = %q, want %q", i, out.Form.Fields[i].Name, out.Form.Fields[i].Section, w)
		}
	}
	// Verify omitempty on legacy field and self-settings compatibility.
	if strings.Contains(string(raw), `"name":"legacy","type":"string","section"`) {
		t.Fatalf("legacy field unexpectedly serialized section: %s", raw)
	}
	change, err := h.setExtensionSettings(m.ID, "Section Demo", json.RawMessage(`{"endpoint":"https://example.com","debug":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := change.apply(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if got := string(h.extensions.reg.SavedConfig(h.extensions.reg.Get(m.ID))); !strings.Contains(got, `"endpoint":"https://example.com"`) {
		t.Fatalf("saved config = %s", got)
	}
}
