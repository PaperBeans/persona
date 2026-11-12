package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

// usageReply is one reply line of a subagent's transcript (all sidechain).
func usageReply(msg, model, usage string) string {
	return `{"type":"assistant","isSidechain":true,"uuid":"u-` + msg + `","timestamp":"2026-11-12T08:00:01Z","message":{"role":"assistant","model":"` +
		model + `","id":"` + msg + `","content":[{"type":"text","text":"working"}],"usage":` + usage + `}}`
}

const usagePrompt = `{"type":"user","isSidechain":true,"uuid":"p","timestamp":"2026-11-12T08:00:00Z","message":{"role":"user","content":"do the work"}}`

func writeUsageLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendUsageLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	if _, err := fh.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		t.Fatal(err)
	}
}

func usageAgentNotice(uuid, agent, call, status string) string {
	text := `<task-notification><task-id>` + agent + `</task-id><tool-use-id>` + call + `</tool-use-id><status>` + status +
		`</status><summary>Agent "Review" finished</summary><result>Reviewed.</result></task-notification>`
	b, _ := json.Marshal(map[string]any{"type": "user", "uuid": uuid, "timestamp": "2026-11-12T08:01:00Z",
		"message": map[string]any{"role": "user", "content": text}})
	return string(b)
}

func usageAgentCall(uuid, call string) string {
	return `{"type":"assistant","uuid":"` + uuid + `","timestamp":"2026-11-12T08:00:00Z","message":{"role":"assistant","model":"claude-opus-5-5","id":"m-` + uuid +
		`","content":[{"type":"tool_use","id":"` + call + `","name":"Agent","input":{"description":"Review","prompt":"Review it."}}],"usage":{"input_tokens":1,"output_tokens":2}}}`
}

func usageAgentReceipt(uuid, call, agent string, async bool) string {
	if async {
		return `{"type":"user","uuid":"` + uuid + `","timestamp":"2026-11-12T08:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + call +
			`","content":"Async agent launched successfully.\nagentId: ` + agent + `"}]},"toolUseResult":{"isAsync":true,"status":"async_launched","agentId":"` + agent + `"}}`
	}
	return `{"type":"user","uuid":"` + uuid + `","timestamp":"2026-11-12T08:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + call +
		`","content":"Reviewed."}]},"toolUseResult":{"status":"completed","agentId":"` + agent + `"}}`
}

// A finished subagent's result and completion notice carry what its own transcript used, its nested subagents'
// included, one entry per model and speed under its id; a launch still running and a notice that it waits carry none.
// Pages and pushes alike.
func TestAgentUsageOnFinishedSubagents(t *testing.T) {
	n, home := testNode(t)
	const id = "aaaaaaaa-0000-0000-0000-000000005426"
	proj := filepath.Join(home, ".claude", "projects", "-work")
	dir := filepath.Join(proj, id, "subagents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// s1, run in the foreground: two requests, the first over three lines whose output grew; Claude Code's own error
	// line and a request of zeros count nothing.
	writeUsageLines(t, filepath.Join(dir, "agent-s1.jsonl"), usagePrompt,
		usageReply("r1", "claude-opus-5-5", `{"input_tokens":2,"cache_read_input_tokens":100,"cache_creation_input_tokens":40,"output_tokens":5,"cache_creation":{"ephemeral_1h_input_tokens":40}}`),
		usageReply("r1", "claude-opus-5-5", `{"input_tokens":2,"cache_read_input_tokens":100,"cache_creation_input_tokens":40,"output_tokens":30,"cache_creation":{"ephemeral_1h_input_tokens":40}}`),
		usageReply("r1", "claude-opus-5-5", `{"input_tokens":2,"cache_read_input_tokens":100,"cache_creation_input_tokens":40,"output_tokens":30,"cache_creation":{"ephemeral_1h_input_tokens":40}}`),
		usageReply("r2", "claude-opus-5-5", `{"input_tokens":3,"cache_read_input_tokens":140,"output_tokens":20}`),
		usageReply("r3", "<synthetic>", `{"input_tokens":0,"output_tokens":0}`),
		usageReply("r4", "glm-5.3", `{"input_tokens":0,"output_tokens":0}`),
	)
	writeUsageLines(t, filepath.Join(dir, "agent-s1.meta.json"), `{"agentType":"general-purpose","toolUseId":"toolu_s1","finished":true}`)
	// b1, in the background, launched n1 (fast), which launched n2.
	writeUsageLines(t, filepath.Join(dir, "agent-b1.jsonl"), usagePrompt, usageReply("b1r1", "claude-opus-5-5", `{"input_tokens":10,"output_tokens":100}`))
	writeUsageLines(t, filepath.Join(dir, "agent-b1.meta.json"), `{"agentType":"general-purpose","toolUseId":"toolu_b1","requestShape":"background"}`)
	writeUsageLines(t, filepath.Join(dir, "agent-n1.jsonl"), usagePrompt, usageReply("n1r1", "claude-opus-5-5", `{"input_tokens":4,"output_tokens":40,"speed":"fast"}`))
	writeUsageLines(t, filepath.Join(dir, "agent-n1.meta.json"), `{"agentType":"Explore","parentAgentId":"b1","finished":true}`)
	writeUsageLines(t, filepath.Join(dir, "agent-n2.jsonl"), usagePrompt, usageReply("n2r1", "claude-sonnet-5-5", `{"input_tokens":6,"output_tokens":60}`))
	writeUsageLines(t, filepath.Join(dir, "agent-n2.meta.json"), `{"agentType":"Explore","parentAgentId":"n1","finished":true}`)
	path := filepath.Join(proj, id+".jsonl")
	writeUsageLines(t, path,
		`{"type":"user","uuid":"p1","timestamp":"2026-11-12T08:00:00Z","message":{"role":"user","content":"review it twice"}}`,
		usageAgentCall("l1", "toolu_s1"), usageAgentReceipt("r1", "toolu_s1", "s1", false),
		usageAgentCall("l2", "toolu_b1"), usageAgentReceipt("r2", "toolu_b1", "b1", true),
		usageAgentNotice("w1", "b1", "toolu_b1", "waiting"),
		usageAgentNotice("d1", "b1", "toolu_b1", "completed"),
		// n1 ran in the background too: Claude Code tells the session itself it finished, the same entries again
		usageAgentNotice("d9", "n1", "toolu_n1", "completed"),
	)
	s1 := []proto.TokenUsage{
		{ID: "agent:s1", Model: "claude-opus-5-5", Input: 5, Output: 50, CacheRead: 240, CacheWrite: 40, CacheWrite1h: 40},
	}
	// b1's report: its own and each nested agent's, under their own ids.
	n1 := []proto.TokenUsage{
		{ID: "agent:n1", Model: "claude-opus-5-5", Input: 4, Output: 40, Fast: true},
		{ID: "agent:n2", Model: "claude-sonnet-5-5", Input: 6, Output: 60},
	}
	b1 := append([]proto.TokenUsage{{ID: "agent:b1", Model: "claude-opus-5-5", Input: 10, Output: 100}}, n1...)
	got := map[string][]proto.TokenUsage{}
	for _, it := range pageItems(t, n, id) {
		if it.Kind == proto.KindToolResult || it.Task != nil {
			got[it.ID] = it.Usage
		}
	}
	want := map[string][]proto.TokenUsage{"r1#0": s1, "r2#0": nil, "w1#0": nil, "d1#0": b1, "d9#0": n1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("page usage %+v,\nwant %+v", got, want)
	}

	// Pushed: b2 finishes while the session is watched; it grew a request since it was last read.
	b2 := filepath.Join(dir, "agent-b2.jsonl")
	writeUsageLines(t, b2, usagePrompt, usageReply("b2r1", "claude-opus-5-5", `{"input_tokens":1,"output_tokens":1}`))
	if u := agentFileUsage(b2); len(u) != 1 || u[0].Output != 1 {
		t.Fatalf("b2 so far: %+v", u)
	}
	appendUsageLines(t, b2, usageReply("b2r2", "claude-opus-5-5", `{"input_tokens":2,"output_tokens":7}`))
	hub := withHub(t, n)
	if err := n.watch(id, 7); err != nil {
		t.Fatal(err)
	}
	defer n.unwatch(id)
	appendUsageLines(t, path, usageAgentCall("l3", "toolu_b2"), usageAgentReceipt("r3", "toolu_b2", "b2", true), usageAgentNotice("d2", "b2", "toolu_b2", "completed"))
	waitFor(t, "the notice pushed", func() bool {
		for _, it := range hub.got() {
			if it.ID == "d2#0" {
				return true
			}
		}
		return false
	})
	for _, it := range hub.got() {
		switch it.ID {
		case "d2#0":
			if want := []proto.TokenUsage{{ID: "agent:b2", Model: "claude-opus-5-5", Input: 3, Output: 8}}; !reflect.DeepEqual(it.Usage, want) {
				t.Fatalf("pushed notice usage %+v, want %+v", it.Usage, want)
			}
		case "r3#0":
			if it.Usage != nil {
				t.Fatalf("a running launch carries usage: %+v", it.Usage)
			}
		}
	}
}

// A subagent's file is read once, then only for what it gained: a change to lines already read is not read again (a
// full read would count it), and a rewrite to a shorter file starts over.
func TestAgentFileUsageReadsOnlyAppended(t *testing.T) {
	p := filepath.Join(t.TempDir(), "agent-x1.jsonl")
	pad := `{"type":"attachment","isSidechain":true,"attachment":{"type":"note","text":"` + strings.Repeat("x", 300) + `"}}`
	first := usageReply("q1", "claude-opus-5-5", `{"input_tokens":2,"output_tokens":10}`)
	writeUsageLines(t, p, usagePrompt, pad, first)
	if u := agentFileUsage(p); len(u) != 1 || u[0].Output != 10 {
		t.Fatalf("first read: %+v", u)
	}
	// The same file, the line already read changed in place (past its head) and a request added.
	changed := strings.Replace(first, `"output_tokens":10`, `"output_tokens":90`, 1)
	writeUsageLines(t, p, usagePrompt, pad, changed,
		usageReply("q1", "claude-opus-5-5", `{"input_tokens":2,"output_tokens":12}`), // q1's later block: +2
		usageReply("q2", "claude-opus-5-5", `{"input_tokens":3,"output_tokens":5}`))
	if u := agentFileUsage(p); len(u) != 1 || u[0].Input != 5 || u[0].Output != 17 {
		t.Fatalf("after the append: %+v (the lines already read were read again?)", u)
	}
	writeUsageLines(t, p, usagePrompt, usageReply("q9", "claude-haiku-4-5", `{"input_tokens":1,"output_tokens":1}`))
	if u := agentFileUsage(p); len(u) != 1 || u[0].Model != "claude-haiku-4-5" || u[0].Output != 1 {
		t.Fatalf("after a rewrite: %+v", u)
	}
}

// A fork's transcript starts with its launcher's reply that launched it, copied: that request is the launcher's, and
// only what follows the fork's prompt is its own.
func TestAgentFileUsageSkipsAForksCopiedContext(t *testing.T) {
	p := filepath.Join(t.TempDir(), "agent-f1.jsonl")
	writeUsageLines(t, p,
		`{"type":"fork-context-ref","agentId":"f1","parentSessionId":"s","parentLastUuid":"x","contextLength":394}`,
		usageReply("parent", "claude-opus-5-5", `{"input_tokens":2,"cache_read_input_tokens":150000,"output_tokens":300}`),
		usagePrompt,
		usageReply("own", "claude-opus-5-5", `{"input_tokens":4,"output_tokens":8}`))
	if u := agentFileUsage(p); len(u) != 1 || u[0].CacheRead != 0 || u[0].Output != 8 {
		t.Fatalf("fork usage %+v", u)
	}
}

// Nested subagents count once each, even when the metas name each other in a cycle.
func TestSubagentUsageNestedCycle(t *testing.T) {
	dir := t.TempDir()
	for _, a := range []struct{ id, parent string }{{"c1", "c3"}, {"c2", "c1"}, {"c3", "c2"}} {
		writeUsageLines(t, filepath.Join(dir, "agent-"+a.id+".jsonl"), usagePrompt, usageReply(a.id+"r", "claude-opus-5-5", `{"input_tokens":1,"output_tokens":1}`))
		writeUsageLines(t, filepath.Join(dir, "agent-"+a.id+".meta.json"), `{"agentType":"x","parentAgentId":"`+a.parent+`"}`)
	}
	want := []proto.TokenUsage{
		{ID: "agent:c1", Model: "claude-opus-5-5", Input: 1, Output: 1},
		{ID: "agent:c2", Model: "claude-opus-5-5", Input: 1, Output: 1},
		{ID: "agent:c3", Model: "claude-opus-5-5", Input: 1, Output: 1},
	}
	if u := subagentUsage(dir, "c1"); !reflect.DeepEqual(u, want) {
		t.Fatalf("usage %+v", u)
	}
}

func TestSubagentsDirOf(t *testing.T) {
	for path, want := range map[string]string{
		"/h/.claude/projects/-w/s1.jsonl":                    "/h/.claude/projects/-w/s1/subagents",
		"/h/.claude/projects/-w/s1/subagents/agent-a1.jsonl": "/h/.claude/projects/-w/s1/subagents",
		"/h/.codex/sessions/2026/11/12/rollout-2026-x.jsonl": "",
		"/h/somewhere/history.json":                          "",
	} {
		if got := subagentsDirOf(path); got != want {
			t.Errorf("%s: %q, want %q", path, got, want)
		}
	}
	for id, ok := range map[string]bool{"a1b2": true, "areview-77550c8cb6361a57": true, "": false, "../x": false, "a/b": false} {
		if agentFileID(id) != ok {
			t.Errorf("agentFileID(%q) = %v", id, !ok)
		}
	}
}

// A team's in-process teammate is left out: its spawn result (which the transcript reads as completed) carries none,
// and a subagent's usage does not walk into a teammate it launched (or what that teammate launched).
func TestAgentUsageLeavesTeammatesOut(t *testing.T) {
	n, home := testNode(t)
	const id = "aaaaaaaa-0000-0000-0000-000000005427"
	proj := filepath.Join(home, ".claude", "projects", "-work")
	dir := filepath.Join(proj, id, "subagents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	agent := func(name, meta, usage string) {
		writeUsageLines(t, filepath.Join(dir, "agent-"+name+".jsonl"), usagePrompt, usageReply(name+"r", "claude-opus-5-5", usage))
		writeUsageLines(t, filepath.Join(dir, "agent-"+name+".meta.json"), meta)
	}
	const team = `"taskKind":"in_process_teammate","teamName":"session-x"`
	agent("tm1", `{"agentType":"server","name":"server",`+team+`}`, `{"input_tokens":50,"output_tokens":500}`)
	agent("b1", `{"agentType":"general-purpose","finished":true}`, `{"input_tokens":1,"output_tokens":10}`)
	agent("tm2", `{"agentType":"web","parentAgentId":"b1",`+team+`}`, `{"input_tokens":70,"output_tokens":700}`)
	agent("k1", `{"agentType":"Explore","parentAgentId":"tm2","finished":true}`, `{"input_tokens":90,"output_tokens":900}`)
	agent("k2", `{"agentType":"Explore","parentAgentId":"b1","finished":true}`, `{"input_tokens":2,"output_tokens":20}`)
	spawned := `{"type":"user","uuid":"r1","timestamp":"2026-11-12T08:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_tm1","content":"Spawned successfully.\nagent_id: tm1\nname: server\nThe agent is now running and will receive instructions via mailbox."}]},"toolUseResult":{"status":"teammate_spawned","agentId":"tm1","name":"server"}}`
	writeUsageLines(t, filepath.Join(proj, id+".jsonl"),
		`{"type":"user","uuid":"p1","timestamp":"2026-11-12T08:00:00Z","message":{"role":"user","content":"start the team"}}`,
		usageAgentCall("l1", "toolu_tm1"), spawned,
		usageAgentCall("l2", "toolu_b1"), usageAgentReceipt("r2", "toolu_b1", "b1", false),
	)
	got := map[string][]proto.TokenUsage{}
	for _, it := range pageItems(t, n, id) {
		if it.Kind == proto.KindToolResult {
			if it.ID == "r1#0" && (it.Task == nil || it.Task.TaskID != "tm1" || !transcript.AgentTerminal(it.Task.Status)) {
				t.Fatalf("the spawn result is no finished agent's, the test checks nothing: %+v", it.Task)
			}
			got[it.ID] = it.Usage
		}
	}
	want := map[string][]proto.TokenUsage{
		"r1#0": nil,
		"r2#0": {{ID: "agent:b1", Model: "claude-opus-5-5", Input: 1, Output: 10}, {ID: "agent:k2", Model: "claude-opus-5-5", Input: 2, Output: 20}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("page usage %+v,\nwant %+v", got, want)
	}
}

// A workflow's final report carries what each agent of its run used, under the agent's id: the run is the one whose
// state file names the report's task; another run's agents and a run that has not ended are not it.
func TestWorkflowUsageOnItsReport(t *testing.T) {
	n, home := testNode(t)
	const id = "aaaaaaaa-0000-0000-0000-000000005428"
	proj := filepath.Join(home, ".claude", "projects", "-work")
	sess := filepath.Join(proj, id)
	for _, d := range []string{"subagents/workflows/wf_aaa-111", "subagents/workflows/wf_bbb-222", "subagents/workflows/wf_ccc-333", "workflows"} {
		if err := os.MkdirAll(filepath.Join(sess, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	agent := func(run, name, usage string) {
		writeUsageLines(t, filepath.Join(sess, "subagents", "workflows", run, "agent-"+name+".jsonl"), usagePrompt, usageReply(name+"r", "claude-opus-5-5", usage))
		writeUsageLines(t, filepath.Join(sess, "subagents", "workflows", run, "agent-"+name+".meta.json"), `{"agentType":"workflow-subagent","workflowPhase":"Review"}`)
	}
	agent("wf_aaa-111", "w1", `{"input_tokens":1,"output_tokens":10}`)
	agent("wf_aaa-111", "w2", `{"input_tokens":2,"output_tokens":20,"speed":"fast"}`)
	agent("wf_bbb-222", "w3", `{"input_tokens":3,"output_tokens":30}`)
	agent("wf_ccc-333", "w4", `{"input_tokens":4,"output_tokens":40}`)
	// The state files: the run's script and result before its task, as long as they come; the third run has none yet.
	writeUsageLines(t, filepath.Join(sess, "workflows", "wf_aaa-111.json"), `{"runId":"wf_aaa-111","script":"`+strings.Repeat("s", 70000)+`","taskId":"wtask1","status":"completed"}`)
	writeUsageLines(t, filepath.Join(sess, "workflows", "wf_bbb-222.json"), `{"runId":"wf_bbb-222","taskId":"wtask2","status":"completed"}`)
	note := func(uuid, task, call, status string) string {
		text := `<task-notification><task-id>` + task + `</task-id><tool-use-id>` + call + `</tool-use-id><status>` + status +
			`</status><summary>Dynamic workflow "Review the change" ` + status + `</summary></task-notification>`
		b, _ := json.Marshal(map[string]any{"type": "user", "uuid": uuid, "timestamp": "2026-11-12T08:01:00Z",
			"message": map[string]any{"role": "user", "content": text}})
		return string(b)
	}
	writeUsageLines(t, filepath.Join(proj, id+".jsonl"),
		`{"type":"user","uuid":"p1","timestamp":"2026-11-12T08:00:00Z","message":{"role":"user","content":"review it"}}`,
		note("d1", "wtask1", "toolu_wf1", "completed"),
		note("d3", "wtask3", "toolu_wf3", "completed"),
	)
	got := map[string][]proto.TokenUsage{}
	for _, it := range pageItems(t, n, id) {
		if it.Task != nil {
			if it.Task.Kind != "workflow" {
				t.Fatalf("not a workflow's report: %+v", it.Task)
			}
			got[it.ID] = it.Usage
		}
	}
	want := map[string][]proto.TokenUsage{
		"d1#0": {{ID: "agent:w1", Model: "claude-opus-5-5", Input: 1, Output: 10}, {ID: "agent:w2", Model: "claude-opus-5-5", Input: 2, Output: 20, Fast: true}},
		"d3#0": nil,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("workflow reports %+v,\nwant %+v", got, want)
	}
	if task := workflowStateTask(filepath.Join(sess, "workflows", "wf_aaa-111.json")); task != "wtask1" {
		t.Fatalf("state file task %q", task)
	}
}

// An agent reported more than once, a nested background one to the session and to its launcher's report, or one that
// failed and was resumed in a later exchange, says the same id each time, with what its transcript used by then (from
// the #5426 review's reproduction).
func TestAgentUsageReportedAgainUnderItsID(t *testing.T) {
	n, home := testNode(t)
	const id = "aaaaaaaa-0000-0000-0000-000000005429"
	proj := filepath.Join(home, ".claude", "projects", "-work")
	dir := filepath.Join(proj, id, "subagents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeUsageLines(t, filepath.Join(dir, "agent-P.jsonl"), usagePrompt, usageReply("p1", "claude-opus-5-5", `{"input_tokens":10,"output_tokens":100}`))
	writeUsageLines(t, filepath.Join(dir, "agent-P.meta.json"), `{"agentType":"general-purpose","toolUseId":"toolu_P","requestShape":"background"}`)
	writeUsageLines(t, filepath.Join(dir, "agent-C.jsonl"), usagePrompt, usageReply("c1", "claude-opus-5-5", `{"input_tokens":1000,"output_tokens":5000}`))
	writeUsageLines(t, filepath.Join(dir, "agent-C.meta.json"), `{"agentType":"general-purpose","parentAgentId":"P","requestShape":"background"}`)
	path := filepath.Join(proj, id+".jsonl")
	writeUsageLines(t, path,
		`{"type":"user","uuid":"p1","timestamp":"2026-11-12T08:00:00Z","message":{"role":"user","content":"review"}}`,
		usageAgentCall("l1", "toolu_P"), usageAgentReceipt("r1", "toolu_P", "P", true),
		usageAgentNotice("nc", "C", "toolu_inside_P", "failed"),
		usageAgentNotice("np", "P", "toolu_P", "failed"))
	c := proto.TokenUsage{ID: "agent:C", Model: "claude-opus-5-5", Input: 1000, Output: 5000}
	page := func() map[string][]proto.TokenUsage {
		got := map[string][]proto.TokenUsage{}
		for _, it := range pageItems(t, n, id) {
			if it.Task != nil {
				got[it.ID] = it.Usage
			}
		}
		return got
	}
	want := map[string][]proto.TokenUsage{"r1#0": nil, "nc#0": {c},
		"np#0": {{ID: "agent:P", Model: "claude-opus-5-5", Input: 10, Output: 100}, c}}
	if got := page(); !reflect.DeepEqual(got, want) {
		t.Fatalf("first exchange %+v,\nwant %+v", got, want)
	}
	// Resumed: P works on and reports again.
	appendUsageLines(t, filepath.Join(dir, "agent-P.jsonl"), usagePrompt, usageReply("p2", "claude-opus-5-5", `{"input_tokens":5,"output_tokens":50}`))
	appendUsageLines(t, path,
		`{"type":"user","uuid":"p2","timestamp":"2026-11-12T09:00:00Z","message":{"role":"user","content":"resume it"}}`,
		usageAgentNotice("np2", "P", "toolu_P", "completed"))
	want["np#0"] = []proto.TokenUsage{{ID: "agent:P", Model: "claude-opus-5-5", Input: 15, Output: 150}, c}
	want["np2#0"] = want["np#0"]
	if got := page(); !reflect.DeepEqual(got, want) {
		t.Fatalf("after the resume %+v,\nwant %+v", got, want)
	}
}
