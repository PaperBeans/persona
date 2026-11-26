package agentviewwire

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nwverse/persona/server/internal/jsonsubset"
)

// Every view spec/fixtures/agent-view expects reads into AgentView and writes back as the same JSON, holds to
// schema/wire/agent-view.schema.json, and names a known phase and reason kind.
func TestAgentViewSamplesRoundTrip(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "spec", "fixtures", "agent-view", "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []struct {
			Name     string `json:"name"`
			Expected struct {
				Views []struct {
					ID   string          `json:"id"`
					View json.RawMessage `json:"view"`
				} `json:"views"`
			} `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "schema", "wire", "agent-view.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	root, err := jsonsubset.Compile(data)
	if err != nil {
		t.Fatal(err)
	}
	schema, _ := root["$defs"].(map[string]any)["AgentView"].(map[string]any)
	n := 0
	for _, c := range file.Cases {
		for _, v := range c.Expected.Views {
			n++
			var view AgentView
			if err := json.Unmarshal(v.View, &view); err != nil {
				t.Fatalf("%s %s: %v", c.Name, v.ID, err)
			}
			if !view.Phase.Valid() || view.Reason == nil || !view.Reason.Kind.Valid() {
				t.Errorf("%s %s: phase %q, reason %+v", c.Name, v.ID, view.Phase, view.Reason)
			}
			back, err := json.Marshal(view)
			if err != nil {
				t.Fatal(err)
			}
			var want, got any
			if err := json.Unmarshal(v.View, &want); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(back, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Errorf("%s %s: wrote %s, want %s", c.Name, v.ID, back, v.View)
			}
			decoded, err := jsonsubset.Decode(back)
			if err != nil {
				t.Fatal(err)
			}
			if msg := jsonsubset.ValidateAt(root, schema, decoded, "$", 0); msg != "" {
				t.Errorf("%s %s: %s", c.Name, v.ID, msg)
			}
		}
	}
	if n == 0 {
		t.Fatal("no views")
	}
}

// A phase or a reason kind from a newer hub keeps its raw string, is not Valid, and is reported to the hook.
func TestUnknownPhaseIsKeptAndCounted(t *testing.T) {
	var seen []string
	AgentViewWireUnknownEnum = func(enum, raw string) { seen = append(seen, enum+"="+raw) }
	defer func() { AgentViewWireUnknownEnum = nil }()
	var v AgentView
	if err := json.Unmarshal([]byte(`{"phase":"paused","reason":{"kind":"snoozed"},"rank":50}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.Phase.Valid() || v.Phase != "paused" || v.Reason.Kind.Valid() || v.Reason.Kind != "snoozed" {
		t.Fatalf("decoded %+v %+v", v, v.Reason)
	}
	if len(seen) != 1 || seen[0] != "AgentPhase=paused" {
		t.Fatalf("hook saw %v", seen)
	}
	b, _ := json.Marshal(v)
	if !bytes.Contains(b, []byte(`"phase":"paused"`)) {
		t.Fatalf("wrote %s", b)
	}
}
