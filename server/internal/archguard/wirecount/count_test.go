package wirecount

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"testing"
)

func parse(t *testing.T, src string) Counts {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	c := newCounts()
	CountFields(c, f)
	CountHandlers(c, f)
	return c
}

func TestFieldMetrics(t *testing.T) {
	c := parse(t, `package p
import ("encoding/json"; "time")
type Session struct {
	State string
	PhaseStatus string
	NodeKind string
	ContentType string
	Role string
	MachineID string
	SessionIDs []string
	Extra map[string]any
	Loose []map[string]interface{}
	Raw json.RawMessage
	Many map[string]json.RawMessage
	At *time.Time
	Done *bool
	LastError *string
	Name string
	Typed StateName
	Opaque Unchecked
	Flag Tristate
	Inner struct{ Mode string; OwnerID string }
}
type StateName string
// A named opaque type and a closed three-state type are the fix for raw-json and ptr-bool: neither counts.
type Unchecked json.RawMessage
type Tristate uint8
`)
	want := map[string][]string{
		"bare-enum-state":  {"Session.State", "Session.PhaseStatus"},
		"bare-enum-kind":   {"Session.NodeKind"},
		"bare-enum-type":   {"Session.ContentType"},
		"bare-enum-role":   {"Session.Role"},
		"bare-id":          {"Session.MachineID", "Session.SessionIDs", "Session.Inner.OwnerID"},
		"map-any":          {"Session.Extra", "Session.Loose"},
		"raw-json":         {"Session.Raw", "Session.Many"},
		"ptr-time":         {"Session.At"},
		"ptr-bool":         {"Session.Done"},
		"ptr-error-string": {"Session.LastError"},
	}
	for metric, names := range want {
		if len(c[metric]) != len(names) {
			t.Errorf("%s = %v, want %v", metric, c[metric], names)
		}
		for _, n := range names {
			if c[metric][n] != 1 {
				t.Errorf("%s missing %s: %v", metric, n, c[metric])
			}
		}
	}
}

func TestHandlerMetrics(t *testing.T) {
	c := parse(t, `package p
func h() {
	send(map[string]any{"type": "session_update", "id": 1})
	send(map[string]any{"type": kind})
	writeJSON(w, 200, map[string]any{"ok": true, "error": ""})
	s.hub.Call(ctx, "node.ping", nil)
	s.hub.Call(ctx, method, nil)
}
`)
	if c["inline-event"]["session_update"] != 1 || c["inline-event"]["<dynamic>"] != 1 {
		t.Errorf("inline-event = %v", c["inline-event"])
	}
	if c["write-json-map"]["{error,ok}"] != 1 {
		t.Errorf("write-json-map = %v", c["write-json-map"])
	}
	if c["literal-call"]["node.ping"] != 1 || len(c["literal-call"]) != 1 {
		t.Errorf("literal-call = %v", c["literal-call"])
	}
}

func TestFieldMetricsArePrefix(t *testing.T) {
	if FieldMetrics[len(FieldMetrics)-1] != "ptr-error-string" || Metrics[len(FieldMetrics)] != "inline-event" {
		t.Fatalf("FieldMetrics = %v, want every field metric and no handler metric", FieldMetrics)
	}
}

// TestEmit is the ratchet's entry point: with WIRE_COUNTS_OUT set it counts the server tree and writes the JSON there.
func TestEmit(t *testing.T) {
	out := os.Getenv("WIRE_COUNTS_OUT")
	if out == "" {
		t.Skip("WIRE_COUNTS_OUT is not set")
	}
	root := os.Getenv("WIRE_COUNTS_ROOT")
	if root == "" {
		root = "../../.."
	}
	c, err := Count(root)
	if err != nil {
		t.Fatal(err)
	}
	wide, err := Wide(root)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.MarshalIndent(map[string]any{"metrics": c, "wide": wide}, "", " ")
	if err := os.WriteFile(out, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
