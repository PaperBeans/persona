package node

import (
	"context"
	"testing"
)

// The assistant's claude has tool search on, for the plain claude launcher as for a shell launcher
// (TestAssistantShellLauncherCompaction): exactly true, never false (#5838, #7317).
func TestAssistantToolSearchOn(t *testing.T) {
	f := cliTest(t)
	id, err := f.n.assistantSend(context.Background(), "", t.TempDir(), "claude", "hello", nil, "", "", cliExec())
	if err != nil {
		t.Fatal(err)
	}
	if got := envValue(f.proc(id).cmd.Env, "ENABLE_TOOL_SEARCH"); got != "true" {
		t.Fatalf("claude has ENABLE_TOOL_SEARCH=%q, want \"true\"", got)
	}
}
