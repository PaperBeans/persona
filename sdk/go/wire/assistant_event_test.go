package wire

import (
	"encoding/json"
	"testing"
)

// legacyAssistantEventParams is AssistantEventParams as it was before state and replace had types of their own: what
// published extensions were written against. The typed fields must write the same bytes.
type legacyAssistantEventParams struct {
	Key        string `json:"key"`
	Severity   string `json:"severity,omitempty"`
	Title      string `json:"title"`
	Body       string `json:"body,omitempty"`
	ActionHint string `json:"action_hint,omitempty"`
	State      string `json:"state,omitempty"`
	Replace    *bool  `json:"replace,omitempty"`
}

// assistant.event's state and replace read and write byte for byte what the *bool and string fields did: replace absent,
// true and false, and state absent and set.
func TestAssistantEventParamsWireIsUnchanged(t *testing.T) {
	for in, want := range map[string]AssistantEventReplace{
		`{"key":"k","title":"t"}`:                                     AssistantEventReplaceDefault,
		`{"key":"k","title":"t","state":"firing"}`:                    AssistantEventReplaceDefault,
		`{"key":"k","title":"t","replace":true}`:                      AssistantEventReplaceTrue,
		`{"key":"k","title":"t","replace":false}`:                     AssistantEventReplaceFalse,
		`{"key":"k","title":"t","state":"resolved","replace":false}`:  AssistantEventReplaceFalse,
		`{"key":"k","severity":"warning","title":"t","replace":true}`: AssistantEventReplaceTrue,
	} {
		var v AssistantEventParams
		if err := json.Unmarshal([]byte(in), &v); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if v.Replace != want || v.Replace.Or(true) != (want != AssistantEventReplaceFalse) {
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
		if legacy, _ := json.Marshal(old); string(legacy) != string(out) {
			t.Errorf("%s: the *bool fields wrote %s, the typed ones %s", in, legacy, out)
		}
		if string(v.State) != old.State {
			t.Errorf("%s: state %q, was %q", in, v.State, old.State)
		}
	}
	var v AssistantEventParams
	if err := json.Unmarshal([]byte(`{"key":"k","title":"t","replace":null}`), &v); err != nil || v.Replace != AssistantEventReplaceDefault {
		t.Errorf("null replace is absent, as a *bool read it: %v %v", v.Replace, err)
	}
	for _, bad := range []string{`{"key":"k","title":"t","replace":"no"}`, `{"key":"k","title":"t","replace":0}`, `{"key":"k","title":"t","state":1}`} {
		if err := json.Unmarshal([]byte(bad), &v); err == nil {
			t.Errorf("%s: decoded", bad)
		}
	}
	if _, err := json.Marshal(AssistantEventReplace(9)); err == nil {
		t.Error("a value outside the three marshaled")
	}
}
