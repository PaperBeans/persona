package node

import (
	"strings"
	"testing"
)

func TestCodexStartUsesThePurposeEffort(t *testing.T) {
	withCodexLaunchers(t, map[string][]string{"cx": {"codex", "-c", "model_reasoning_effort=low"}})
	n, _, _, _ := codexStartHerdr(t, 0)
	_, err := request(t, n, "session.start", map[string]any{"cwd": "~/proj", "launcher": "cx", "prompt": "hello", "effort": "high"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"codex", "-c", "model_reasoning_effort=low", "-c", "model_reasoning_effort=high"}
	if strings.Join(ran, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("runs %q, want %q", ran, want)
	}
}

func TestCodexStartRejectsInvalidPurposeEffort(t *testing.T) {
	withCodexLaunchers(t, map[string][]string{"codex": {"codex"}})
	n, _, _, cmds := codexStartHerdr(t, 0)
	_, err := request(t, n, "session.start", map[string]any{"cwd": "~/proj", "launcher": "codex", "prompt": "hello", "effort": "invalid"})
	if err == nil || !strings.Contains(err.Error(), "unknown effort") {
		t.Fatalf("invalid effort: %v", err)
	}
	if len(*cmds) != 0 {
		t.Fatalf("started with invalid effort: %q", *cmds)
	}
}
