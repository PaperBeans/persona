package db

import (
	"context"
	"encoding/json"
	"testing"
)

// state.put/state.load must not drop an assistant's explicit profile at the
// manager's JSON boundary, otherwise a node restart silently chooses another.
func TestNodeSessionConfigDirJSON(t *testing.T) {
	var state NodeSession
	if err := json.Unmarshal([]byte(`{"config_dir":"/tmp/assistant-profile"}`), &state); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out["config_dir"] != "/tmp/assistant-profile" {
		t.Fatalf("explicit config_dir lost at the manager boundary: %s", raw)
	}
}

func TestNodeConfigDirIsPersisted(t *testing.T) {
	d, ctx := open(t), context.Background()
	for _, dir := range []string{"/tmp/assistant-profile", "~/.config/assistant-profile", "default", ""} {
		if err := d.PutNodeSession(ctx, "machine", "session", NodeSession{
			Launcher: "claude", ConfigDir: dir, CreationID: "synthetic-creation", Incarnation: "synthetic-incarnation",
		}); err != nil {
			t.Fatal(err)
		}
		sessions, err := d.NodeSessions(ctx, "machine")
		if err != nil {
			t.Fatal(err)
		}
		if got := sessions["session"].ConfigDir; got != dir {
			t.Fatalf("persisted config_dir=%q, want %q", got, dir)
		}
		if got := sessions["session"]; got.CreationID != "synthetic-creation" || got.Incarnation != "synthetic-incarnation" {
			t.Fatalf("config_dir write changed the creation receipt: %+v", got)
		}
	}
}
