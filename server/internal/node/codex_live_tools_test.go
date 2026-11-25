package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestCodexLiveToolRollout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	add := func(lines ...string) {
		t.Helper()
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		for _, ln := range lines {
			if _, err := f.WriteString(ln); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	line := func(kind string, payload map[string]any) string {
		b, err := json.Marshal(map[string]any{"type": kind, "payload": payload})
		if err != nil {
			t.Fatal(err)
		}
		return string(b) + "\n"
	}
	turn := func(kind, id string) string { return line("event_msg", map[string]any{"type": kind, "turn_id": id}) }
	call := func(kind, id, ns, name string) string {
		return line("response_item", map[string]any{"type": kind, "id": "wire-" + id, "call_id": id, "namespace": ns, "name": name, "status": "completed", "input": "arbitrary private JS", "arguments": "{}"})
	}
	output := func(kind, id string) string {
		return line("response_item", map[string]any{"type": kind, "call_id": id, "output": ""})
	}
	item := func(event, kind, id, status string) string {
		return line("event_msg", map[string]any{"type": event, "item": map[string]any{"type": kind, "id": id, "status": status, "command": []string{"irrelevant command"}}})
	}
	wrapper := proto.LiveTool{ID: "outer", Name: "exec"}
	cua := proto.LiveTool{ID: "cua", Name: "mcp__cua_repl.js"}
	shell := proto.LiveTool{ID: "shell", Name: "Bash"}
	var reader codexLiveToolReader
	check := func(r *codexLiveToolReader, want ...proto.LiveTool) {
		t.Helper()
		got, err := r.Read(path)
		if err != nil || len(got.Tools) != len(want) || (len(want) > 0 && !reflect.DeepEqual(got.Tools, want)) {
			t.Fatalf("read = %+v, %v; want %+v", got, err, want)
		}
	}
	add(turn("task_started", "old"), call("custom_tool_call", "old", "", "exec"), turn("turn_aborted", "old"), turn("task_started", "current"),
		call("custom_tool_call", "outer", "", "exec"), call("function_call", "cua", "mcp__cua_repl", "js"))
	check(&reader, wrapper, cua)
	add(item("item_completed", "CommandExecution", "child", "completed"), call("custom_tool_call", "outer", "", "exec"), turn("task_complete", "old"))
	check(&reader, wrapper, cua)
	add(item("item_completed", "McpToolCall", "cua", "completed"), output("function_call_output", "cua"), output("function_call_output", ""))
	check(&reader, wrapper)
	add(item("item_started", "CommandExecution", "shell", "inProgress"), item("item_completed", "CommandExecution", "shell", "backgrounded"))
	check(&reader, wrapper, shell)
	end := output("custom_tool_call_output", "outer")
	add(end[:len(end)/2])
	check(&reader, wrapper, shell)
	add(end[len(end)/2:])
	check(&reader, shell)
	add(item("item_completed", "CommandExecution", "shell", "failed"), call("custom_tool_call", "again", "", "exec"))
	check(&reader, proto.LiveTool{ID: "again", Name: "exec"})
	add(turn("task_complete", "current"), call("custom_tool_call", "after", "", "exec"))
	check(&reader)
	check(&codexLiveToolReader{})
	add(turn("task_started", "next"), call("function_call", "nextcall", "functions", "exec_command"))
	check(&reader, proto.LiveTool{ID: "nextcall", Name: "functions.exec_command"})
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	add(turn("task_started", "replacement"), call("function_call", "other", "", "Read"))
	check(&reader, proto.LiveTool{ID: "other", Name: "Read"})
	// Same inode truncated and regrown past its previous end between polls.
	s := turn("task_started", "rewritten") + call("function_call", "new", "", "Write") + strings.Repeat(" \n", 200)
	if err := os.WriteFile(path, []byte(s), 0600); err != nil {
		t.Fatal(err)
	}
	check(&reader, proto.LiveTool{ID: "new", Name: "Write"})
}

func TestCodexLiveThinkingAndHeading(t *testing.T) {
	var r codexLiveToolReader
	line := func(src string) { r.apply([]byte(src)) }
	line(`{"type":"event_msg","payload":{"type":"task_started","turn_id":"one"}}`)
	if !r.snapshot().Active || r.snapshot().Thinking {
		t.Fatal("starting a turn is not evidence of reasoning")
	}
	line(`{"type":"response_item","payload":{"type":"reasoning","encrypted_content":"unavailable"}}`)
	line(`{"type":"event_msg","payload":{"type":"item_completed","item":{"id":"r","type":"Reasoning"}}}`)
	if r.snapshot().Thinking {
		t.Fatal("completed reasoning was shown as running")
	}
	line(`{"type":"event_msg","payload":{"type":"item_started","item":{"id":"r2","type":"Reasoning"}}}`)
	line(`{"type":"event_msg","payload":{"type":"item_completed","item":{"id":"r1","type":"Reasoning"}}}`)
	if !r.snapshot().Thinking {
		t.Fatal("lost an active reasoning item")
	}
	line(`{"type":"event_msg","payload":{"type":"item_completed","item":{"id":"r2","type":"Reasoning"}}}`)
	if r.snapshot().Thinking {
		t.Fatal("reasoning did not end")
	}
	line(`{"type":"event_msg","payload":{"type":"agent_reasoning_delta","delta":"private"}}`)
	if !r.snapshot().Thinking {
		t.Fatal("explicit reasoning delta missed")
	}
	line(`{"type":"response_item","payload":{"type":"custom_tool_call","name":"exec","call_id":"batch","input":"private program"}}`)
	if r.snapshot().Thinking {
		t.Fatal("tool execution should end reasoning")
	}
	heading := `{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"McpToolCall","id":"inner","server":"codex_app","tool":"update_running_summary","arguments":{"labels":["Checking","Checked","checkout totals"]},"status":"completed","result":{"isError":false}}}}`
	line(strings.Replace(heading, `"isError":false`, `"isError":true`, 1))
	if r.snapshot().Summary != "" {
		t.Fatal("failed summary was displayed")
	}
	line(heading)
	if v := r.snapshot(); v.Summary != "Checking checkout totals" || len(v.Tools) != 1 || v.Tools[0].ID != "batch" {
		t.Fatalf("summary bound to wrong tool: %+v", v)
	}
	line(`{"type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"batch","output":""}}`)
	if v := r.snapshot(); v.Summary != "" || len(v.Tools) != 0 || v.Thinking || !v.Active {
		t.Fatalf("stale heading: %+v", v)
	}
	line(heading)
	if r.snapshot().Summary != "" {
		t.Fatal("late receipt reintroduced a heading")
	}
	line(`{"type":"event_msg","payload":{"type":"turn_aborted","turn_id":"one"}}`)
	if r.snapshot().Active || r.snapshot().Thinking {
		t.Fatal("turn still active")
	}
	for _, s := range []struct {
		labels []string
		want   string
	}{
		{[]string{"正在核对", "已核对", "手机日志"}, "正在核对手机日志"},
		{[]string{"核对手机版式", "验证会话回放"}, "核对手机版式"},
		{[]string{"Read the code", "Check spacing", "Replay history"}, "Read the code"},
		{[]string{strings.Repeat("界", 161)}, ""},
		{[]string{"name\nvalue"}, ""},
	} {
		if got := codexLiveTitle(s.labels); got != s.want {
			t.Errorf("title = %q, want %q", got, s.want)
		}
	}
}

func TestCodexLiveCompactionLifecycle(t *testing.T) {
	var r codexLiveToolReader
	apply := func(line string) { r.apply([]byte(line)) }
	start := `{"type":"event_msg","payload":{"type":"item_started","turn_id":"one","item":{"id":"compact","type":"ContextCompaction"}}}`
	end := `{"type":"event_msg","payload":{"type":"item_completed","item":{"id":"compact","type":"ContextCompaction"}}}`
	open := `{"type":"event_msg","payload":{"type":"task_started","turn_id":"one"}}`
	apply(start)
	if r.snapshot().Compacting {
		t.Fatal("compacting outside open turn")
	}
	apply(open)
	apply(end)
	if r.snapshot().Compacting {
		t.Fatal("completed historical item started an indicator")
	}
	apply(strings.Replace(start, "one", "other", 1))
	if r.snapshot().Compacting {
		t.Fatal("started compaction from another turn")
	}
	apply(start)
	apply(strings.Replace(end, `"id":"compact"`, `"id":"older"`, 1))
	if !r.snapshot().Compacting {
		t.Fatal("unrelated completion cleared current compaction")
	}
	apply(end)
	if r.snapshot().Compacting {
		t.Fatal("compaction did not clear upon completion")
	}
	apply(start)
	apply(`{"type":"compacted","payload":{"message":"","replacement_history":[]}}`)
	if r.snapshot().Compacting || !r.snapshot().Active {
		t.Fatal("replacement history did not end compaction within the open turn")
	}
	apply(start)
	apply(`{"type":"history_mutation","payload":{"display_completions":[{"item":{"id":"compact","type":"contextCompaction"}}]}}`)
	if r.snapshot().Compacting {
		t.Fatal("mirrored completed item did not clear")
	}
	apply(start)
	apply(`{"type":"event_msg","payload":{"type":"turn_aborted","turn_id":"one"}}`)
	if r.snapshot().Compacting || r.snapshot().Active {
		t.Fatal("aborted turn left compaction running")
	}
}

func TestCodexLiveYieldedShellSurvivesWrapper(t *testing.T) {
	var r codexLiveToolReader
	apply := func(record string) { r.apply([]byte(record)) }
	start := `{"type":"response_item","payload":{"type":"custom_tool_call","call_id":"wrapper","name":"exec"}}`
	output := `{"type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"wrapper","output":[{"type":"input_text","text":"Script completed"},{"type":"input_text","text":"{\"status\":\"fulfilled\",\"value\":{\"chunk_id\":\"c0ffee\",\"session_id\":24948,\"wall_time_seconds\":0.3,\"output\":\"\"}}"}]}}`
	end := `{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"command","process_id":"24948","status":"failed"}}}`
	apply(`{"type":"event_msg","payload":{"type":"task_started","turn_id":"first"}}`)
	apply(start)
	apply(output)
	if got := r.snapshot().Tools; len(got) != 1 || got[0] != (proto.LiveTool{ID: "terminal-session:24948", Name: "Bash"}) {
		t.Fatalf("after return: %v", got)
	}
	apply(end)
	apply(start)
	apply(output)
	if got := r.snapshot().Tools; len(got) != 0 {
		t.Fatalf("late receipt revived a finished command: %v", got)
	}
	apply(`{"type":"event_msg","payload":{"type":"task_started","turn_id":"second"}}`)
	apply(start)
	apply(output)
	apply(`{"type":"event_msg","payload":{"type":"turn_aborted","turn_id":"second"}}`)
	if got := r.snapshot().Tools; len(got) != 0 {
		t.Fatalf("stale terminal after interrupt: %v", got)
	}
}
