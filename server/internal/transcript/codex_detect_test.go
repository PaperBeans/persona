package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Claude line can hold a tool input shaped like a rollout line (an MCP tool called with {"type":"event_msg",
// "payload":{}}). Only a line's own top-level type makes it a rollout line, so that Claude line keeps its tool call and
// does not switch the rest of the session to the Codex parser, where every later line used to yield nothing.
func TestClaudeLineWithCodexShapedInputStaysClaude(t *testing.T) {
	lines := []string{
		`{"type":"user","uuid":"u1","timestamp":"2026-11-10T10:00:00Z","message":{"role":"user","content":"call the tool"}}`,
		`{"type":"assistant","uuid":"a1","parentUuid":"u1","timestamp":"2026-11-10T10:00:01Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"mcp__x__y","input":{"type":"event_msg","payload":{}}}]}}`,
		`{"type":"assistant","uuid":"a2","parentUuid":"a1","timestamp":"2026-11-10T10:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`,
	}
	path := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := NewFile(path)
	items, err := f.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range items {
		got = append(got, it.ID+":"+it.Kind+":"+it.ToolName+":"+it.Text)
	}
	if f.meta.Codex || len(items) != 3 || items[1].ToolName != "mcp__x__y" || items[2].Text != "done" {
		t.Fatalf("codex %v, items %q", f.meta.Codex, got)
	}
}

// isCodexLine reads only the line's own top-level type, wherever it stands among the keys; a rollout type in a nested
// object or in a string does not count.
func TestIsCodexLineReadsTheTopLevelType(t *testing.T) {
	for line, want := range map[string]bool{
		`{"timestamp":"2026-11-10T10:00:00Z","type":"session_meta","payload":{"id":"x"}}`:                            true,
		`{"timestamp":"2026-11-10T10:00:00Z","type":"event_msg","payload":{"type":"task_started"}}`:                  true,
		`{"timestamp":"2026-11-10T10:00:00Z","type":"response_item","payload":{"type":"message"}}`:                   true,
		`{"timestamp":"2026-11-10T10:00:00Z","type":"turn_context","payload":{"model":"m"}}`:                         true,
		`{"timestamp":"2026-11-10T10:00:00Z","type":"history_mutation","payload":{}}`:                                true,
		`{"payload":{"type":"user","text":"a \"quoted\" } word"},"timestamp":"t","type":"event_msg"}`:                true,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","input":{"type":"event_msg","payload":{}}}]}}`: false,
		`{"message":{"content":[{"input":{"type":"session_meta","payload":{}}}]},"type":"user"}`:                     false,
		`{"type":"user","message":{"content":"\"type\":\"event_msg\",\"payload\""}}`:                                 false,
		`{"type":"user","payload":{"type":"event_msg"}}`:                                                             false,
		`{"type":"event_msg"}`:                  false, // no payload
		`not json "type":"event_msg","payload"`: false,
		``:                                      false,
	} {
		if got := isCodexLine([]byte(line)); got != want {
			t.Errorf("isCodexLine(%s) = %v, want %v", line, got, want)
		}
	}
}
