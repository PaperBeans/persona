package extension

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
)

// offloadManifest is the smallest manifest with these disk_offload items, for the hub's Parse and the SDK's schema alike.
func offloadManifest(t *testing.T, items ...map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{"manifest_version": 1, "id": "example-cloud", "name": "Example Cloud", "version": "1.0.0",
		"engines": map[string]any{"extension_protocol": 1}, "runtime": map[string]any{"command": "bin/example-cloud"},
		"contributes": map[string]any{"disk_offload": items}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDiskOffloadPoint(t *testing.T) {
	p := Points["disk_offload"]
	if p.Name != "disk_offload" || p.Namespace != "offload" || p.Version != 1 || !p.NeedsRPC || p.NeedsHTTP {
		t.Fatalf("point %+v", p)
	}
	m, err := Parse(offloadManifest(t, map[string]any{"id": "cloud", "name": "Example Cloud", "kinds": []string{"app", "file"}, "restore": true},
		map[string]any{"id": "cold", "name": "Cold storage"}))
	if err != nil {
		t.Fatal(err)
	}
	got := m.DiskOffloads()
	if len(got) != 2 || got[0].ID != "cloud" || !got[0].Restore || got[1].Restore {
		t.Fatalf("%+v", got)
	}
	if k := OffloadKinds(got[0]); !slices.Equal(k, []string{"app", "file"}) {
		t.Error(k)
	}
	if k := OffloadKinds(got[1]); !slices.Equal(k, []string{"file", "dir"}) {
		t.Error(k)
	}
	if items := m.Items("disk_offload"); !slices.Equal(items, []string{"cloud", "cold"}) {
		t.Fatal(items)
	}
	lines := Explain(m)
	if !slices.ContainsFunc(lines, func(l PermissionLine) bool { return l.Key == "disk_offload" }) {
		t.Errorf("the install page does not say it: %+v", lines)
	}
	if (&Manifest{}).DiskOffloads() != nil {
		t.Error("no contribution")
	}
}

// What an item may be: the hub's Parse and the SDK's schema agree.
func TestDiskOffloadRefusals(t *testing.T) {
	item := func(change func(it map[string]any)) []map[string]any {
		it := map[string]any{"id": "cloud", "name": "Example Cloud"}
		change(it)
		return []map[string]any{it}
	}
	for _, c := range []struct {
		name  string
		items []map[string]any
		ok    bool
	}{
		{"plain", item(func(map[string]any) {}), true},
		{"every kind", item(func(it map[string]any) { it["kinds"] = []string{"file", "dir", "app"} }), true},
		{"a name of 40 characters", item(func(it map[string]any) { it["name"] = strings.Repeat("代", 40) }), true},
		{"no name", item(func(it map[string]any) { delete(it, "name") }), false},
		{"a blank name", item(func(it map[string]any) { it["name"] = " " }), false},
		{"a name of 41 characters", item(func(it map[string]any) { it["name"] = strings.Repeat("a", 41) }), false},
		{"a control character", item(func(it map[string]any) { it["name"] = "a\nb" }), false},
		{"a bad id", item(func(it map[string]any) { it["id"] = "Cloud!" }), false},
		{"no kinds named", item(func(it map[string]any) { it["kinds"] = []string{} }), false},
		{"an unknown kind", item(func(it map[string]any) { it["kinds"] = []string{"photos"} }), false},
		{"a kind twice", item(func(it map[string]any) { it["kinds"] = []string{"file", "file"} }), false},
		{"an unknown field", item(func(it map[string]any) { it["quota"] = 5 }), false},
		{"no items", nil, false},
	} {
		raw := offloadManifest(t, c.items...)
		_, hubErr := Parse(raw)
		schemaErr := wire.ValidateJSON("Manifest", raw)
		if (hubErr == nil) != c.ok {
			t.Errorf("%s: hub: want valid=%v, got %v", c.name, c.ok, hubErr)
		}
		if (schemaErr == nil) != c.ok {
			t.Errorf("%s: schema: want valid=%v, got %v", c.name, c.ok, schemaErr)
		}
	}
	if _, err := Parse(with(t, func(m map[string]any) {
		delete(m, "runtime")
		delete(m, "activation_events")
		m["contributes"] = map[string]any{"disk_offload": []any{map[string]any{"id": "cloud", "name": "Example Cloud"}}}
	})); err == nil || !strings.Contains(err.Error(), "needs a runtime that speaks the protocol") {
		t.Errorf("a provider without a program: %v", err)
	}
}
