package transcript

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestCodexRollout(t *testing.T) {
	lines := []string{
		`{"timestamp":"2026-10-26T05:05:58.393Z","type":"session_meta","payload":{"id":"01a1","cwd":"/r/dots","cli_version":"0.159.3"}}`,
		`{"timestamp":"2026-10-26T05:05:59.475Z","type":"turn_context","payload":{"model":"gpt-6.1-sol","approval_policy":"never"}}`,
		`{"timestamp":"2026-10-26T05:05:59.515Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u1","content":[{"type":"text","text":"why is it stuck"}]}}}`,
		`{"timestamp":"2026-10-26T05:06:00.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"dup"}]}}`,
		`{"timestamp":"2026-10-26T05:06:01.000Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"m1","phase":"commentary","content":[{"type":"Text","text":"Looking at the tabs first."}]}}}`,
		`{"timestamp":"2026-10-26T05:06:02.000Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"e1","command":["/bin/zsh","-lc","herdr agent list"],"status":"completed","aggregated_output":"ok\n","exit_code":0}}}`,
		`{"timestamp":"2026-10-26T05:06:03.000Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"e2","command":["/bin/zsh","-lc","false"],"status":"failed","aggregated_output":"","exit_code":1}}}`,
		`{"timestamp":"2026-10-26T05:06:04.000Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"FileChange","id":"f1","changes":{"/r/dots/a.txt":{"type":"update","unified_diff":"@@ -1 +1 @@\n-a\n+b\n"}},"status":"completed"}}}`,
		`{"timestamp":"2026-10-26T05:06:05.000Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"McpToolCall","id":"c1","server":"cua","tool":"js","arguments":{"code":"1"},"status":"completed","result":{"content":[{"type":"text","text":"2"}],"isError":false}}}}`,
		`{"timestamp":"2026-10-26T05:06:06.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"output_tokens":211},"last_token_usage":{"input_tokens":26279}}}}`,
		`{"timestamp":"2026-10-26T05:06:07.000Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"m2","phase":"final_answer","content":[{"type":"Text","text":"Tab 1 waits on a prompt."}]}}}`,
		`{"timestamp":"2026-10-26T05:06:08.000Z","type":"event_msg","payload":{"type":"turn_aborted","reason":"interrupted"}}`,
	}
	path := filepath.Join(t.TempDir(), "rollout-2026-10-26T13-05-38-01a1.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := NewFile(path)
	items, err := f.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range items {
		got = append(got, it.Role+":"+it.Kind+":"+it.ToolName)
	}
	want := []string{
		"user:text:", "assistant:thinking:",
		"assistant:tool_use:Bash", "user:tool_result:", "assistant:tool_use:Bash", "user:tool_result:",
		"assistant:tool_use:Edit", "user:tool_result:", "assistant:tool_use:mcp__cua__js", "user:tool_result:",
		"assistant:text:", "system:notice:",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("items\n got %v\nwant %v", got, want)
	}
	if string(items[2].ToolInput) != `{"command":"herdr agent list"}` || items[3].IsError || !items[5].IsError {
		t.Fatalf("bash: %s %+v %+v", items[2].ToolInput, items[3], items[5])
	}
	if !strings.Contains(string(items[6].ToolInput), `"diff":"@@ -1 +1 @@\n-a\n+b\n"`) || items[6].ToolUseID != items[7].ToolUseID {
		t.Fatalf("edit: %s", items[6].ToolInput)
	}
	m := f.Meta()
	if !m.Codex || m.Cwd != "/r/dots" || m.Model != "gpt-6.1-sol" || m.FirstUser != "why is it stuck" ||
		m.LastText != "Tab 1 waits on a prompt." || m.ContextTokens != 26279 || m.Turns != 1 {
		t.Fatalf("meta: %+v", m)
	}
	if items[11].Text != "已中断" || items[0].Kind != proto.KindText {
		t.Fatalf("notice: %+v", items[11])
	}
}

func TestCodexUserMessageTags(t *testing.T) {
	line := `{"timestamp":"2026-10-26T05:05:59.515Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u2","content":[` +
		`{"type":"text","text":"<send_user_message_question_reply>\n[{\"questionItemId\":\"x\",\"question\":\"Accept the Xcode license?\",\"answer\":\"done, agreed\"}]\n</send_user_message_question_reply>"},` +
		`{"type":"text","text":"\n<in-app-browser-context source=\"ambient-ui-state\">tabs: a, b</in-app-browser-context>"},` +
		`{"type":"local_image","path":"/tmp/shot.png"}]}}}`
	meta := &Meta{Codex: true}
	it := only(t, ParseLine(1, []byte(line), meta))
	if it.Text != "done, agreed\n\n<attached_images>\n/tmp/shot.png\n</attached_images>" {
		t.Fatalf("got %q", it.Text)
	}
	if got := codexUserText(`<send_user_message_question_reply>[{"question":"A?","answer":"1"},{"question":"B?","answer":"2"}]</send_user_message_question_reply>`); got != "A?：1\nB?：2" {
		t.Fatalf("several answers: %q", got)
	}
}

// a compatible CLI (a Codex fork) writes the newer rollout: messages come as history_mutation display_completions, tool calls
// still as event_msg/item_completed, and an item may appear in both.
func TestHistoryMutationRollout(t *testing.T) {
	lines := []string{
		`{"timestamp":"2026-10-28T08:42:16.027Z","ordinal":0,"type":"session_meta","payload":{"session_id":"01a1","id":"01a1","cwd":"/r/fake","originator":"traex-tui","cli_version":"0.210.1"}}`,
		`{"timestamp":"2026-10-28T08:42:48.700Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"t1"}}`,
		`{"timestamp":"2026-10-28T08:42:48.752Z","ordinal":2,"type":"history_mutation","payload":{"version":1,"turn_id":"t1","operation":"append","items":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"<collaboration_mode>…</collaboration_mode>"}]}]}}`,
		`{"timestamp":"2026-10-28T08:42:48.954Z","ordinal":6,"type":"history_mutation","payload":{"display_completions":[{"turn_id":"t1","item":{"type":"UserMessage","id":"u1","content":[{"type":"text","text":"build the proxy","text_elements":[]}]}}],"version":1,"operation":"append","items":[{"type":"message","role":"user","content":[{"type":"input_text","text":"build the proxy"}]}]}}`,
		`{"timestamp":"2026-10-28T08:43:40.492Z","ordinal":8,"type":"history_mutation","payload":{"display_completions":[{"turn_id":"t1","item":{"type":"AgentMessage","id":"m1","content":[{"type":"Text","text":"Checking the repo first."}],"phase":"commentary"}}],"version":1,"operation":"append","items":[]}}`,
		`{"timestamp":"2026-10-28T08:43:41.000Z","ordinal":9,"type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"e1","command":["/bin/zsh","-lc","ls"],"status":"completed","aggregated_output":"a\n","exit_code":0}}}`,
		`{"timestamp":"2026-10-28T08:43:41.100Z","ordinal":10,"type":"history_mutation","payload":{"display_completions":[{"turn_id":"t1","item":{"type":"CommandExecution","id":"e1","command":["/bin/zsh","-lc","ls"],"status":"completed","aggregated_output":"a\n","exit_code":0}}]}}`,
		`{"timestamp":"2026-10-28T08:43:42.000Z","ordinal":11,"type":"event_msg","payload":{"type":"item_completed","item":{"type":"TerminalInteraction","id":"ti1","stdin":"","command":"go test ./..."}}}`,
		`{"timestamp":"2026-10-28T08:43:50.000Z","ordinal":12,"type":"history_mutation","payload":{"display_completions":[{"turn_id":"t1","item":{"type":"AgentMessage","id":"m2","content":[{"type":"Text","text":"Done: the proxy runs."}],"phase":"final_answer"}}]}}`,
	}
	path := filepath.Join(t.TempDir(), "rollout-2026-10-28T16-42-16-01a1.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := NewFile(path)
	items, err := f.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range items {
		got = append(got, it.Role+":"+it.Kind+":"+it.ToolName+":"+it.Text)
	}
	want := []string{
		"user:text::build the proxy", "assistant:thinking::Checking the repo first.",
		"assistant:tool_use:Bash:", "user:tool_result::a\n", "assistant:text::Done: the proxy runs.",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("items\n got %q\nwant %q", got, want)
	}
	m := f.Meta()
	if !m.Codex || m.Cwd != "/r/fake" || m.LastText != "Done: the proxy runs." || !m.TurnOpen {
		t.Fatalf("meta: %+v", m)
	}
	_ = os.WriteFile(path, []byte(strings.Join(append(lines, `{"timestamp":"2026-10-28T08:43:51.000Z","ordinal":13,"type":"event_msg","payload":{"type":"task_complete","turn_id":"t1"}}`), "\n")+"\n"), 0o644)
	if _, err := f.Refresh(); err != nil || f.Meta().TurnOpen {
		t.Fatalf("turn still open after task_complete: %v", err)
	}
}

// codexTurns is a synthesized rollout of n turns: each a user message and four agent messages, the second of them
// recorded again in history_mutation (the newer format), and the last turn reusing an id of the first.
func codexTurns(n int) []string {
	const ts = `"timestamp":"2026-11-10T08:00:00.000Z"`
	agent := func(id, text string) string {
		return `{"type":"AgentMessage","id":"` + id + `","content":[{"type":"Text","text":"` + text + `"}],"phase":"commentary"}`
	}
	lines := []string{`{` + ts + `,"type":"session_meta","payload":{"id":"01a1","cwd":"/r/fake","cli_version":"0.170.0"}}`}
	for k := range n {
		lines = append(lines,
			fmt.Sprintf(`{%s,"type":"event_msg","payload":{"type":"task_started","turn_id":"t%d"}}`, ts, k),
			fmt.Sprintf(`{%s,"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u%d","content":[{"type":"text","text":"ask %d"}]}}}`, ts, k, k))
		for j := range 4 {
			id := fmt.Sprintf("m%d-%d", k, j)
			if k == n-1 && j == 3 {
				id = "m0-3" // an id an earlier turn used
			}
			it := agent(id, fmt.Sprintf("step %d of %d", j, k))
			lines = append(lines, fmt.Sprintf(`{%s,"type":"event_msg","payload":{"type":"item_completed","item":%s}}`, ts, it))
			if j == 1 {
				lines = append(lines, fmt.Sprintf(`{%s,"type":"history_mutation","payload":{"display_completions":[{"turn_id":"t%d","item":%s}]}}`, ts, k, it))
			}
		}
		lines = append(lines, fmt.Sprintf(`{%s,"type":"event_msg","payload":{"type":"task_complete","turn_id":"t%d"}}`, ts, k))
	}
	return lines
}

// An item is recorded twice only within its turn (window.go), so the ids the parser keeps to drop the repeat are
// those of the turn under way: a long rollout does not grow them with every item it ever had.
func TestCodexSeenBoundedByTurn(t *testing.T) {
	const turns = 2000
	meta := &Meta{Codex: true}
	most, n := 0, 0
	for i, line := range codexTurns(turns) {
		n += len(ParseLine(i+1, []byte(line), meta))
		most = max(most, len(meta.codexSeen))
	}
	if most > 5 {
		t.Fatalf("the parser kept %d item ids; a turn has 5", most)
	}
	if n != turns*5 {
		t.Fatalf("%d items for %d turns of 5: the repeat in history_mutation is not dropped once", n, turns)
	}
}

// A full read and a read opened by its end (its window starting at a turn) give the same items: neither drops an
// item whose id an earlier turn used, and both drop the repeat within a turn.
func TestCodexSeenFullEqualsWindowed(t *testing.T) {
	defer func(w int64) { windowBytes = w }(windowBytes)
	data := []byte(strings.Join(codexTurns(60), "\n") + "\n")
	p := filepath.Join(t.TempDir(), "rollout-2026-11-10T08-00-00-01a1.jsonl")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	windowBytes = 1 << 40
	full := NewFile(p)
	if _, err := full.Refresh(); err != nil {
		t.Fatal(err)
	}
	all, _, _ := full.Page(0, 1<<30)
	if len(all) != 60*5 {
		t.Fatalf("a full read gives %d items for 60 turns of 5", len(all))
	}
	windowBytes = int64(len(data)) / 8
	f := NewFile(p)
	if _, err := f.Refresh(); err != nil {
		t.Fatal(err)
	}
	if f.base == 0 {
		t.Fatal("the file was not opened by its end: the test checks nothing")
	}
	var got []proto.Item
	for before := 0; ; {
		items, more, _ := f.Page(before, 7)
		got = append(append([]proto.Item(nil), items...), got...)
		if !more || len(items) == 0 {
			break
		}
		before = items[0].Seq
	}
	if !bytes.Equal(marshal(t, got), marshal(t, all)) {
		t.Fatalf("a windowed read gives %d items, a full read %d", len(got), len(all))
	}
}

// A rollout's token_count carries the model's context window next to the latest request's input (#2430).
func TestCodexContextWindow(t *testing.T) {
	f := NewFile(filepath.Join("testdata", "codex-0.163.jsonl"))
	if _, err := f.Refresh(); err != nil {
		t.Fatal(err)
	}
	if m := f.Meta(); m.ContextWindow != 475000 || m.ContextTokens <= 0 || m.ContextTokens >= m.ContextWindow {
		t.Fatalf("context %d of window %d", m.ContextTokens, m.ContextWindow)
	}
}
