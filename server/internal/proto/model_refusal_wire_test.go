package proto

import (
	"bytes"
	"encoding/json"
	"testing"
)

// The record's line (ModelRefusal.Raw) is the node's own: neither the session encoder written by hand nor the
// reflect one carries it, on a session's fallback or an item's notice (#2528). The public fields and words do.
func TestModelRefusalRawNeverEncoded(t *testing.T) {
	const marker = "HIDDEN-MARKER-2528-proto"
	r := &ModelRefusal{Subtype: "model_refusal_fallback", OriginalModel: "primary-model", FallbackModel: "fallback-model",
		Category: "synthetic-policy", Scope: "session", Explanation: "Upstream refused the request", Text: "Synthetic refusal words",
		Raw: `{"syntheticHidden":{"nested":"` + marker + `"}}`}
	s := Session{SessionID: "s1", ModelFallback: r}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(&s); err != nil { // appendSession when the layout is current
		t.Fatal(err)
	}
	type plain Session // the reflect encoder
	p, _ := json.Marshal(plain(s))
	it, _ := json.Marshal(Item{ID: "i1", Role: "system", Kind: KindNotice, Text: r.Text, ModelRefusal: r})
	for name, b := range map[string][]byte{"session": buf.Bytes(), "session (reflect)": p, "item": it} {
		if bytes.Contains(b, []byte(marker)) || bytes.Contains(b, []byte(`"raw"`)) {
			t.Fatalf("%s: %s", name, b)
		}
		for _, want := range []string{`"original_model":"primary-model"`, `"fallback_model":"fallback-model"`, `"category":"synthetic-policy"`,
			`"scope":"session"`, `"explanation":"Upstream refused the request"`, `"text":"Synthetic refusal words"`} {
			if !bytes.Contains(b, []byte(want)) {
				t.Fatalf("%s lost %s: %s", name, want, b)
			}
		}
	}
	// What an older build sent as "raw" is not read back either: a hub passing a decoded session on carries none.
	var back Session
	if err := json.Unmarshal([]byte(`{"session_id":"s1","model_fallback":{"subtype":"model_refusal_fallback","raw":"`+marker+`"}}`), &back); err != nil {
		t.Fatal(err)
	}
	if back.ModelFallback == nil || back.ModelFallback.Raw != "" {
		t.Fatalf("decoded %+v", back.ModelFallback)
	}
}
