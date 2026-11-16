package devices

import (
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/server/internal/devicewire"
)

// Only the hub's human-input submission from a device marks a message as the owner's own; a caller's own owner_input
// never survives, and everything else in the params is kept as it was.
func TestOwnerInputParams(t *testing.T) {
	human := &devicewire.Submission{ID: "s1", Kind: "human_input", OriginalText: "merge it"}
	device := &devicewire.Caller{Kind: "device", CredentialID: "d1"}
	for _, c := range []struct {
		name   string
		call   devicewire.NodeCall
		params string
		want   bool
	}{
		{"the owner's app", devicewire.NodeCall{Caller: device, Submission: human}, `{"session_id":"x","text":"merge it"}`, true},
		{"the owner's app, a claim of its own", devicewire.NodeCall{Caller: device, Submission: human}, `{"text":"merge it","owner_input":false}`, true},
		{"the assistant", devicewire.NodeCall{Caller: &devicewire.Caller{Kind: "system"}}, `{"text":"merge it","owner_input":true}`, false},
		{"no caller", devicewire.NodeCall{}, `{"text":"merge it","owner_input":true}`, false},
		{"a device, no submission", devicewire.NodeCall{Caller: device}, `{"text":"merge it"}`, false},
		{"another kind of submission", devicewire.NodeCall{Caller: device, Submission: &devicewire.Submission{ID: "s2", Kind: "relay"}}, `{"text":"merge it"}`, false},
		{"a submission without an id", devicewire.NodeCall{Caller: device, Submission: &devicewire.Submission{Kind: "human_input"}}, `{"text":"merge it"}`, false},
	} {
		out, err := ownerInputParams(c.call, json.RawMessage(c.params))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var fields map[string]any
		if err := json.Unmarshal(out, &fields); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := fields["owner_input"] == true; got != c.want {
			t.Errorf("%s: owner_input = %v in %s", c.name, fields["owner_input"], out)
		}
		if _, there := fields["owner_input"]; there && !c.want {
			t.Errorf("%s: a caller's owner_input survived: %s", c.name, out)
		}
		if fields["text"] != "merge it" {
			t.Errorf("%s: text changed: %s", c.name, out)
		}
	}
	if _, err := ownerInputParams(devicewire.NodeCall{}, json.RawMessage(`[1]`)); err == nil {
		t.Error("params that are not an object were passed on")
	}
}
