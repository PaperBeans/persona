package hub

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

func TestModelRefusalNoticeLocalization(t *testing.T) {
	for _, subtype := range []string{"model_refusal_fallback", "model_refusal_no_fallback", "model_refusal_future"} {
		t.Run(subtype, func(t *testing.T) {
			raw := `{"type":"system","subtype":"` + subtype + `","uuid":"refusal-example","trigger":"refusal","direction":"retry","scope":"session","originalModel":"primary-model","fallbackModel":"fallback-model","requestId":"synthetic-request","apiRefusalCategory":"synthetic-policy","apiRefusalExplanation":"Upstream refused the request","content":"Synthetic refusal words","refusedUserMessageUuid":"synthetic-user","retractedMessageUuids":[],"syntheticHidden":{"nested":"` + hiddenMarker + `"}}`
			var meta transcript.Meta
			items := transcript.ParseLine(1, []byte(raw), &meta)
			if len(items) != 1 || items[0].Kind != proto.KindNotice || items[0].ModelRefusal == nil {
				t.Fatalf("refusal notice missing: %+v", items)
			}
			original := items[0]
			for _, lang := range []i18n.Lang{i18n.En, i18n.ZhHans, i18n.Ja} {
				got := localizedItems(lang, items)[0]
				if !strings.HasSuffix(got.Text, "\nUpstream refused the request\nSynthetic refusal words") {
					t.Fatalf("%s: missing explanation or the refusal's words: %q", lang, got.Text)
				}
				b, _ := json.Marshal(got)
				if strings.Contains(string(b), hiddenMarker) || strings.Contains(string(b), "syntheticHidden") || strings.Contains(string(b), `"raw"`) {
					t.Fatalf("%s: the record's other fields left the hub: %s", lang, b)
				}
				if got.ModelRefusal != original.ModelRefusal || got.ID != original.ID || got.Seq != original.Seq {
					t.Fatalf("%s: changed original data: %+v", lang, got)
				}
				if subtype == "model_refusal_fallback" && !strings.Contains(got.Text, "/model") {
					t.Fatalf("%s: no recovery guidance: %q", lang, got.Text)
				}
				if subtype != "model_refusal_fallback" && strings.Contains(got.Text, "/model") {
					t.Fatalf("%s: invented a fallback for %s", lang, subtype)
				}
			}
			if items[0].Text != original.Text || items[0].ModelRefusal.Raw != raw {
				t.Fatal("localization mutated the shared transcript")
			}
			if got := localizedItems(i18n.Lang("unknown"), items)[0]; got.Text != localizedItems(i18n.ZhHans, items)[0].Text {
				t.Fatal("an unknown language must use the default")
			}
		})
	}
}

// hiddenMarker stands for a field of a refusal record no one reviewed (synthetic): it must never reach a client.
const hiddenMarker = "HIDDEN-MARKER-2528-7f3c"

// An older node sends the whole line as the notice's text and as model_refusal.raw: what the hub answers (a page,
// one item, a push) carries neither, and keeps the record's fields and words.
func TestModelRefusalFromOlderNodeLeavesNothingHidden(t *testing.T) {
	line := `{"type":"system","subtype":"model_refusal_fallback","scope":"session","originalModel":"primary-model","fallbackModel":"fallback-model","apiRefusalCategory":"synthetic-policy","apiRefusalExplanation":"Upstream refused the request","syntheticHidden":{"nested":{"deeper":"` + hiddenMarker + `"}}}`
	old, _ := json.Marshal(map[string]any{"id": "line1#0", "seq": 1, "role": "system", "kind": "notice", "text": line,
		"model_refusal": map[string]any{"subtype": "model_refusal_fallback", "original_model": "primary-model", "fallback_model": "fallback-model",
			"category": "synthetic-policy", "scope": "session", "explanation": "Upstream refused the request", "raw": line}})
	var it proto.Item // as the hub decodes a node's items (http.go)
	if err := json.Unmarshal(old, &it); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []i18n.Lang{i18n.En, i18n.ZhHans, i18n.Ja} {
		got := localizedItems(lang, []proto.Item{it})[0]
		b, _ := json.Marshal(got)
		if strings.Contains(string(b), hiddenMarker) || strings.Contains(string(b), `"raw"`) {
			t.Fatalf("%s: %s", lang, b)
		}
		r := got.ModelRefusal
		if r == nil || r.OriginalModel != "primary-model" || r.FallbackModel != "fallback-model" || r.Category != "synthetic-policy" ||
			r.Scope != "session" || r.Explanation != "Upstream refused the request" || !strings.Contains(got.Text, "Upstream refused the request") {
			t.Fatalf("%s: lost the refusal signal: %+v %q", lang, r, got.Text)
		}
	}
}
