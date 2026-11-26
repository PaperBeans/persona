package node

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestAgySwarmRosterInSessionInfo(t *testing.T) {
	n, herdr, home := herdrNode(t)
	t.Setenv("ANTIGRAVITY_CLI_CONFIG_DIR", "")
	agyTestFile(t, n.agyDataRoot(), agyTestID, `{"step_index":0,"type":"USER_INPUT","source":"USER_EXPLICIT","content":"Start"}`+"\n")
	seedAgents(t, n, herdr, agyTestAgent(agyTestPane, home, agyTestID))
	path := filepath.Join(n.agyDataRoot(), "brain", agyTestID, agySwarmMembersPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	listWorkers := func() []agySwarmWorker {
		t.Helper()
		info := agyTestCall(t, n, "session.info", agyTestRow, nil).(map[string]any)
		workers, _ := info["swarm_workers"].([]agySwarmWorker)
		return workers
	}
	if got := listWorkers(); len(got) != 0 {
		t.Fatalf("a regular agy pane has a swarm roster: %+v", got)
	}

	agyTestWrite(t, path, `{"members":{
		"parent":{"role":"coordinator","phase":"RUNNING","wake_mode":"working"},
		"review":{"role":"worker","phase":"RUNNING","wake_mode":"mentions"},
		"build":{"role":"worker","phase":"RUNNING","wake_mode":"working"},
		"draft":{"role":"worker","phase":"RUNNING","killed":true},
		"check":{"role":"worker","phase":"FUTURE"}
	}}`)
	want := []agySwarmWorker{
		{Name: "build", State: "working"},
		{Name: "check", State: "unknown"},
		{Name: "draft", State: "stopped"},
		{Name: "review", State: "waiting"},
	}
	if got := listWorkers(); !slices.Equal(got, want) {
		t.Fatalf("swarm workers = %+v, want %+v", got, want)
	}

	// The worker can start while the parent's transcript stays unchanged.
	agyTestWrite(t, path, `{"members":{"review":{"role":"worker","phase":"RUNNING","wake_mode":"working"}}}`)
	if got, want := listWorkers(), []agySwarmWorker{{Name: "review", State: "working"}}; !slices.Equal(got, want) {
		t.Fatalf("swarm workers = %+v, want %+v", got, want)
	}

	agyTestWrite(t, path, `{"members":`)
	if got := listWorkers(); len(got) != 0 {
		t.Fatalf("incomplete roster = %+v", got)
	}
}
