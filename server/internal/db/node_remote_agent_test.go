package db

import (
	"context"
	"encoding/json"
	"testing"
)

func TestNodeRemoteAgentIsPersistedAndLatched(t *testing.T) {
	var state NodeSession
	if err := json.Unmarshal([]byte(`{"launcher":"pac","cwd":"/tmp/assistant","remote_agent":true}`), &state); err != nil {
		t.Fatal(err)
	}
	if !state.RemoteAgent {
		t.Fatalf("remote_agent lost at the manager boundary: %+v", state)
	}
	d, ctx := open(t), context.Background()
	if err := d.PutNodeSession(ctx, "machine", "session", state); err != nil {
		t.Fatal(err)
	}
	sessions, err := d.NodeSessions(ctx, "machine")
	if err != nil {
		t.Fatal(err)
	}
	got := sessions["session"]
	if !got.RemoteAgent || got.Cwd != "/tmp/assistant" {
		t.Fatalf("persisted %+v, want remote_agent true", got)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if v, _ := back["remote_agent"].(bool); !v {
		t.Fatalf("state.load answers %s, want remote_agent true", raw)
	}

	// A later PutNodeSession with RemoteAgent false must preserve the latch.
	state.RemoteAgent = false
	if err := d.PutNodeSession(ctx, "machine", "session", state); err != nil {
		t.Fatal(err)
	}
	sessions, err = d.NodeSessions(ctx, "machine")
	if err != nil {
		t.Fatal(err)
	}
	if !sessions["session"].RemoteAgent {
		t.Fatalf("remote_agent cleared by subsequent PutNodeSession: %+v", sessions["session"])
	}
}
