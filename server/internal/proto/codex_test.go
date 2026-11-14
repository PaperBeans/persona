package proto

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSessionCapabilitiesTakeoverAndRewindRoundTrip(t *testing.T) {
	for _, raw := range []string{
		`{"session_id":"s","capabilities":{"takeover":{"running":true,"can_resume":true},"rewind":true,"rewind_code":false}}`,
		`{"session_id":"s","capabilities":{"takeover":{"running":false,"can_resume":false,"reason":"encrypted_context"}}}`,
		`{"session_id":"s","capabilities":{"rewind":false,"rewind_code":true}}`,
		`{"session_id":"s","capabilities":{}}`,
	} {
		var session Session
		if err := json.Unmarshal([]byte(raw), &session); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(session)
		if err != nil {
			t.Fatal(err)
		}
		var before, after struct {
			Capabilities map[string]any `json:"capabilities"`
		}
		if err := json.Unmarshal([]byte(raw), &before); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encoded, &after); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"takeover", "rewind", "rewind_code"} {
			if !reflect.DeepEqual(before.Capabilities[key], after.Capabilities[key]) {
				t.Errorf("%s changed %s: %v -> %v", raw, key, before.Capabilities[key], after.Capabilities[key])
			}
		}
	}
}
