package assistantwire

import (
	"encoding/json"
	"testing"
)

// legacyAssistantEventParams is AssistantEventParams as it was before state and replace had types of their own.
type legacyAssistantEventParams struct {
	Key        string `json:"key"`
	Severity   string `json:"severity,omitempty"`
	Title      string `json:"title"`
	Body       string `json:"body,omitempty"`
	ActionHint string `json:"action_hint,omitempty"`
	State      string `json:"state,omitempty"`
	Replace    *bool  `json:"replace,omitempty"`
}

// The typed state and replace read and write byte for byte what the string and *bool fields did.
func TestAssistantEventParamsWireIsUnchanged(t *testing.T) {
	for in, want := range map[string]EventReplace{
		`{"key":"k","title":"t"}`:                                    EventReplaceDefault,
		`{"key":"k","title":"t","state":"firing"}`:                   EventReplaceDefault,
		`{"key":"k","title":"t","replace":true}`:                     EventReplaceYes,
		`{"key":"k","title":"t","replace":false}`:                    EventReplaceKeep,
		`{"key":"k","title":"t","state":"resolved","replace":false}`: EventReplaceKeep,
	} {
		var v AssistantEventParams
		if err := json.Unmarshal([]byte(in), &v); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if v.Replace != want || v.Replace.Replaces() != (want != EventReplaceKeep) {
			t.Errorf("%s: replace %v", in, v.Replace)
		}
		out, err := json.Marshal(v)
		if err != nil || string(out) != in {
			t.Errorf("%s: wrote %s (%v)", in, out, err)
		}
		var old legacyAssistantEventParams
		if err := json.Unmarshal([]byte(in), &old); err != nil {
			t.Fatal(err)
		}
		if legacy, _ := json.Marshal(old); string(legacy) != string(out) || string(v.State) != old.State {
			t.Errorf("%s: the old fields wrote %s, the typed ones %s", in, legacy, out)
		}
	}
	var v AssistantEventParams
	if err := json.Unmarshal([]byte(`{"key":"k","title":"t","replace":null}`), &v); err != nil || v.Replace != EventReplaceDefault {
		t.Errorf("null replace is absent, as a *bool read it: %v %v", v.Replace, err)
	}
	for _, bad := range []string{`{"key":"k","title":"t","replace":"no"}`, `{"key":"k","title":"t","replace":1}`} {
		if err := json.Unmarshal([]byte(bad), &v); err == nil {
			t.Errorf("%s: decoded", bad)
		}
	}
	if _, err := json.Marshal(EventReplace(9)); err == nil {
		t.Error("a value outside the three marshaled")
	}
}

// ExtensionEvent forwards the params as the program sent them, byte for byte, and writes absent params as null as the
// json.RawMessage did.
func TestExtensionEventParamsPassThrough(t *testing.T) {
	for _, in := range []string{
		`{"extension":"x","name":"X","params":{"key":"k","title":"t","replace":false,"extra":[1,2]}}`,
		`{"extension":"x","name":"X","params":null}`,
		`{"extension":"x","name":"X","params":"not an object"}`,
	} {
		var v ExtensionEvent
		if err := json.Unmarshal([]byte(in), &v); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		out, err := json.Marshal(v)
		if err != nil || string(out) != in {
			t.Errorf("%s: wrote %s (%v)", in, out, err)
		}
	}
	if out, _ := json.Marshal(ExtensionEvent{Extension: "x"}); string(out) != `{"extension":"x","name":"","params":null}` {
		t.Errorf("empty params: %s", out)
	}
}
