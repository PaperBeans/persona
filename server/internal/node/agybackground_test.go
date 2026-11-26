package node

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func agyBackgroundLogURI(path string) string {
	return (&url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(path), "/")}).String()
}

func agyBackgroundStep(index int, status, started string, task int, description, logURI string) string {
	content := fmt.Sprintf("Created At: %s\n%s %s/task-%d\nTask Description: %s", started, agyTaskPrefix, agyTestID, task, description)
	if logURI != "" {
		content += "\nTask logs are available at: " + logURI
	}
	return fmt.Sprintf(`{"step_index":%d,"type":"GENERIC","status":%q,"created_at":%q,"content":%q}`+"\n", index, status, started, content)
}

func TestAgyBackgroundLifecycle(t *testing.T) {
	n := &Node{home: t.TempDir()}
	t.Setenv("ANTIGRAVITY_CLI_CONFIG_DIR", "")
	dir := n.agyDataRoot()
	start := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	finish := start.Add(time.Minute)
	started, ended := start.Format(time.RFC3339), finish.Format(time.RFC3339)
	logPath := filepath.Join(dir, "brain", agyTestID, ".system_generated", "tasks", "task-1.log")
	logURI := agyBackgroundLogURI(logPath)
	planner := fmt.Sprintf(`{"step_index":0,"type":"PLANNER_RESPONSE","created_at":%q,"tool_calls":[{"name":"read_file"},{"name":"run_command","args":{"CommandLine":"\"echo ready\""}},{"name":"schedule"},{"name":"run_workflow"},{"name":"invoke_subagent"}]}`+"\n", started)
	// The command's result arrives after its sibling's result.
	file := agyTestFile(t, dir, agyTestID, planner+
		agyBackgroundStep(3, "RUNNING", started, 2, "Cron: */30 * * * * Check status", "")+
		agyBackgroundStep(2, "RUNNING", started, 1, "Wait for ready", logURI)+
		agyBackgroundStep(4, "RUNNING", started, 3, "Run checks", "file:///unrelated/task-3.log")+
		agyBackgroundStep(5, "RUNNING", started, 4, "Review changes", ""))
	row := proto.Session{Status: proto.StatusIdle}
	n.mergeAgyTranscript(&row, agyTestID)
	if len(row.Background) != 4 || len(row.BackgroundEnded()) != 0 || row.Loop != nil {
		t.Fatalf("unexpected background state: %+v / %+v", row.Background, row.BackgroundEnded())
	}
	for i, kind := range []string{"shell", "monitor", "workflow", "agent"} {
		task := row.Background[i]
		if task.ID != fmt.Sprintf("%s/task-%d", agyTestID, i+1) || task.Kind != kind || !task.StartedAt.Equal(start) {
			t.Fatalf("task %d: %+v", i, task)
		}
	}
	if row.Background[0].Command != "echo ready" || row.Background[0].OutputFile != logPath || row.Background[1].Description != "Cron: */30 * * * * Check status" || row.Background[2].OutputFile != "" {
		t.Fatalf("lost or misattributed task metadata: %+v", row.Background)
	}

	agyTestAppend(t, file, fmt.Sprintf(`{"step_index":6,"type":"SYSTEM_MESSAGE","created_at":%q,"content":%q}`+"\n", ended,
		fmt.Sprintf("<SYSTEM_MESSAGE>\n[Message] taskId=%[1]s/task-1 message=Task id \"%[1]s/task-1\" finished with result: Ready\n</SYSTEM_MESSAGE>", agyTestID)))
	agyTestAppend(t, file, fmt.Sprintf(`{"step_index":7,"type":"PLANNER_RESPONSE","created_at":%q,"tool_calls":[{"name":"manage_task","args":{"Action":"\"kill\"","TaskId":"\"%s/task-2\""}},{"name":"manage_task","args":{"Action":"status","TaskId":"%s/task-4"}}]}`+"\n", ended, agyTestID, agyTestID))
	agyTestAppend(t, file, fmt.Sprintf(`{"step_index":4,"type":"GENERIC","status":"ERROR","created_at":%q,"updated_at":%q,"content":"Workflow failed"}`+"\n", started, ended))
	n.mergeAgyTranscript(&row, agyTestID)
	if len(row.Background) != 1 || row.Background[0].Kind != "agent" {
		t.Fatalf("running tasks: %+v", row.Background)
	}
	outcomes := row.BackgroundEnded()
	if len(outcomes) != 3 {
		t.Fatalf("ended tasks: %+v", outcomes)
	}
	for i, status := range []string{"completed", "killed", "failed"} {
		if outcomes[i].Status != status || !outcomes[i].EndedAt.Equal(finish) {
			t.Fatalf("outcome %d: %+v", i, outcomes[i])
		}
	}
	if outcomes[0].OutputFile != logPath || outcomes[2].Description != "Run checks" {
		t.Fatalf("lost outcome metadata: %+v", outcomes)
	}

	for _, status := range []string{proto.StatusOffline, proto.StatusInterrupted} {
		row.Status = status
		n.mergeAgyTranscript(&row, agyTestID)
		if len(row.Background) != 0 || len(row.BackgroundEnded()) != 3 {
			t.Fatalf("stopped row %s: %+v / %+v", status, row.Background, row.BackgroundEnded())
		}
	}
	row.Status = proto.StatusIdle
	agyTestAppend(t, file, fmt.Sprintf(`{"step_index":8,"type":"SYSTEM_MESSAGE","created_at":%q,"content":%q}`+"\n", ended,
		fmt.Sprintf(`Task id "%s/task-4" was canceled.`, agyTestID)))
	n.mergeAgyTranscript(&row, agyTestID)
	if outcomes := row.BackgroundEnded(); len(row.Background) != 0 || len(outcomes) != 4 || outcomes[3].Status != "killed" {
		t.Fatalf("canceled subagent: %+v / %+v", row.Background, outcomes)
	}

	old := time.Now().UTC().Add(-2 * workflowKeep).Format(time.RFC3339)
	agyTestWrite(t, file, agyBackgroundStep(0, "RUNNING", old, 1, "Old task", "")+
		fmt.Sprintf(`{"step_index":1,"type":"SYSTEM_MESSAGE","created_at":%q,"content":%q}`+"\n", old,
			fmt.Sprintf(`Task id "%s/task-1" was canceled.`, agyTestID)))
	n.mergeAgyTranscript(&row, agyTestID)
	if len(row.Background) != 0 || len(row.BackgroundEnded()) != 0 {
		t.Fatalf("old outcome survived: %+v / %+v", row.Background, row.BackgroundEnded())
	}
}

func TestAgyBackgroundScheduleMessages(t *testing.T) {
	const started = "2026-06-01T10:00:00Z"
	const fired = "2026-06-01T10:01:01Z"
	const id = agyTestID + "/task-1"
	for _, tc := range []struct {
		name, args, description, sender, message string
		completed                                bool
	}{
		{"timer duration", `{"DurationSeconds":60}`, "Check status", id, "Check again", true},
		{"timer description", `{}`, "Timer: 60s", id, "Check again", true},
		{"cron expression", `{"CronExpression":"0 * * * *"}`, "Timer: 60s", id, "Check again", false},
		{"cron description", `{"DurationSeconds":"60"}`, "Cron: 0 * * * *", id, "Check again", false},
		{"cron iteration", `{"DurationSeconds":"60"}`, "Timer: 60s", id, "Check again (iteration 1)", false},
		{"heartbeat", `{"DurationSeconds":"60"}`, "Timer: 60s", id, "Task is still running", false},
		{"different sender", `{}`, "Timer: 60s", id + "0", "Check again", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			planner := fmt.Sprintf(`{"step_index":0,"type":"PLANNER_RESPONSE","tool_calls":[{"name":"schedule","args":%s}]}`+"\n", tc.args)
			message := fmt.Sprintf("<SYSTEM_MESSAGE>\n[Message] timestamp=%s sender=%s priority=MESSAGE_PRIORITY_HIGH content=%s\n</SYSTEM_MESSAGE>", started, tc.sender, tc.message)
			notice := fmt.Sprintf(`{"step_index":2,"type":"SYSTEM_MESSAGE","created_at":%q,"content":%q}`+"\n", fired, message)
			steps, _ := parseAgySteps([]byte(planner+agyBackgroundStep(1, "RUNNING", started, 1, tc.description, "")+notice), agyTestID)
			tasks := agyBackground(steps, agyTestID, "", nil)
			if tc.completed {
				if len(tasks.running) != 0 || len(tasks.ended) != 1 {
					t.Fatalf("expected one ended timer: %+v", tasks)
				}
				ended := tasks.ended[0]
				if ended.ID != id || ended.Status != proto.WorkflowCompleted || ended.EndedAt.Format(time.RFC3339) != fired {
					t.Fatalf("unexpected timer outcome: %+v", ended)
				}
			} else if len(tasks.running) != 1 || tasks.running[0].ID != id || len(tasks.ended) != 0 {
				t.Fatalf("expected the task to keep running: %+v", tasks)
			}
		})
	}
}

func TestAgyBackgroundSessionListMerge(t *testing.T) {
	n, herdr, home := herdrNode(t)
	t.Setenv("ANTIGRAVITY_CLI_CONFIG_DIR", "")
	dir := n.agyDataRoot()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	logPath := filepath.Join(dir, "brain", agyTestID, ".system_generated", "tasks", "task-1.log")
	logURI := agyBackgroundLogURI(logPath)
	agyTestFile(t, dir, agyTestID, `{"step_index":0,"type":"PLANNER_RESPONSE","tool_calls":[{"name":"schedule"}]}`+"\n"+
		agyBackgroundStep(1, "RUNNING", stamp, 1, "Cron: */30 * * * * Check status", logURI))

	const sid = "agy-" + agyTestID
	n.runtimes = newRuntimeRegistry()
	h := n.runtimes.add(nil)
	h.ready, h.hello.Runtime = true, agyRuntimeID
	runtimeHost := &RuntimeHost{reg: n.runtimes}
	setRuntimeRow := func(status, backgroundField string) {
		t.Helper()
		raw := fmt.Sprintf(`{"sessions":[{"session_id":%q,"cwd":%q,"model":"gemini-test","status":%q,%q:[{"id":%q,"kind":"monitor","output_file":"/unrelated.log"}],"background_ended":[{"id":%q,"kind":"workflow","status":"completed","ended_at":%q,"output_file":"/unrelated.log"}]}]}`,
			sid, home, status, backgroundField, agyTestID+"/task-1", agyTestID+"/task-2", stamp)
		runtimeHost.event(h, "runtime.sessions", json.RawMessage(raw))
	}
	setRuntimeRow(proto.StatusIdle, "background")
	rows := listed(t, n)
	acp := rows[sid]
	if len(acp.Background) != 1 || acp.Background[0].Kind != "monitor" || acp.Background[0].OutputFile != logPath ||
		len(acp.BackgroundEnded()) != 1 || acp.BackgroundEnded()[0].Kind != "workflow" || acp.BackgroundEnded()[0].OutputFile != "" {
		t.Fatalf("ACP fields after normalization: %+v / %+v", acp.Background, acp.BackgroundEnded())
	}

	setRuntimeRow(proto.StatusIdle, "agy_pane_background") // The companion sees a transcript without an ACP process.
	if acp := listed(t, n)[sid]; acp.Model != "gemini-test" || len(acp.Background) != 0 {
		t.Fatalf("history row shows pane-only work: %+v", acp)
	}
	pane := agyTestAgent(agyTestPane, home, agyTestID)
	herdr.reply("session.snapshot", snapshot(nil, nil, []any{pane}, nil))
	rows = listed(t, n)
	row := rows[agyTestRow]
	if _, duplicate := rows[sid]; duplicate || row.Model != "gemini-test" || len(row.Background) != 1 || row.Background[0].Kind != "monitor" || row.Loop != nil {
		t.Fatalf("live pane merge: pane=%+v, duplicate=%v", row, duplicate)
	}
	if row.Background[0].OutputFile != logPath || len(row.BackgroundEnded()) != 1 || row.BackgroundEnded()[0].OutputFile != "" {
		t.Fatalf("pane did not keep projected tasks with verified logs: %+v / %+v", row.Background, row.BackgroundEnded())
	}

	setRuntimeRow(proto.StatusOffline, "background")
	pane["agent_status"] = proto.StatusOffline
	herdr.reply("session.snapshot", snapshot(nil, nil, []any{pane}, nil))
	rows = listed(t, n)
	if len(rows[sid].Background) != 0 || len(rows[agyTestRow].Background) != 0 {
		t.Fatalf("stopped sessions show running work: %+v / %+v", rows[sid].Background, rows[agyTestRow].Background)
	}
}

func TestAgyPaneKeepsCompanionBackgroundAfterRewind(t *testing.T) {
	n, herdr, home := herdrNode(t)
	t.Setenv("ANTIGRAVITY_CLI_CONFIG_DIR", "")
	dir := n.agyDataRoot()
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	kept, ended, abandoned := agyTestID+"/task-1", agyTestID+"/task-2", agyTestID+"/task-3"
	logPath := filepath.Join(dir, "brain", agyTestID, ".system_generated", "tasks", "task-1.log")
	// The compact export still contains a running task and a completion past the native rewind cut.
	agyTestFile(t, dir, agyTestID,
		agyBackgroundStep(0, "RUNNING", stamp, 1, "Kept task", agyBackgroundLogURI(logPath))+
			agyBackgroundStep(1, "DONE", stamp, 2, "Finished before the cut", "")+
			agyBackgroundStep(2, "RUNNING", stamp, 3, "Abandoned task", "")+
			fmt.Sprintf(`{"step_index":3,"type":"SYSTEM_MESSAGE","created_at":%q,"content":%q}`+"\n", stamp,
				fmt.Sprintf(`Task id "%s" was killed.`, kept)))
	seedAgents(t, n, herdr, agyTestAgent(agyTestPane, home, agyTestID))
	n.runtimes = newRuntimeRegistry()
	h := n.runtimes.add(nil)
	h.ready, h.hello.Runtime = true, agyRuntimeID

	companion := proto.Session{SessionID: agyAgent + "-" + agyTestID, Status: proto.StatusIdle, Cwd: home}
	companion.Background = []proto.BackgroundTask{{ID: kept, Kind: "monitor", StartedAt: now}}
	companion.SetBackgroundEnded([]proto.BackgroundTask{{ID: ended, Kind: "workflow", Status: proto.WorkflowCompleted, EndedAt: &now}})
	n.runtimes.setSessions(h, normalizeRuntimeSessions(h, []proto.Session{companion}))

	row := listed(t, n)[agyTestRow]
	if len(row.Background) != 1 || row.Background[0].ID != kept || row.Background[0].Kind != "monitor" || row.Background[0].OutputFile != logPath {
		t.Errorf("pane should keep the projected running task and attach its local log: %+v", row.Background)
	}
	if outcomes := row.BackgroundEnded(); len(outcomes) != 1 || outcomes[0].ID != ended || outcomes[0].Status != proto.WorkflowCompleted || row.Loop != nil {
		t.Errorf("pane should keep the projected outcomes without adding a loop: %+v / %+v", outcomes, row.Loop)
	}

	companion.Background = nil
	companion.SetBackgroundEnded(nil)
	n.runtimes.setSessions(h, normalizeRuntimeSessions(h, []proto.Session{companion}))
	row = listed(t, n)[agyTestRow]
	if len(row.Background) != 0 || len(row.BackgroundEnded()) != 0 {
		t.Errorf("a full rewind should keep the pane inventory empty: %+v / %+v", row.Background, row.BackgroundEnded())
	}

	n.runtimes.setSessions(h, nil)
	row = listed(t, n)[agyTestRow]
	if len(row.Background) != 1 || row.Background[0].ID != abandoned || len(row.BackgroundEnded()) != 2 {
		t.Errorf("pane without a companion row should still read local tasks: %+v / %+v", row.Background, row.BackgroundEnded())
	}
}

func TestAgyBackgroundTaskLogRejectsEscapingSymlink(t *testing.T) {
	dir := t.TempDir()
	path := agyTestFile(t, dir, agyTestID, "")
	logPath := filepath.Join(filepath.Dir(filepath.Dir(path)), "tasks", "task-1.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.log")
	agyTestWrite(t, outside, "unrelated file")
	if err := os.Symlink(outside, logPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	brain, err := agyBrain(dir, agyTestID)
	if err != nil {
		t.Fatal(err)
	}
	defer brain.Close()
	uri := agyBackgroundLogURI(logPath)
	if path := agyTaskLog(uri, dir, agyTestID, "task-1", brain); path != "" {
		t.Fatalf("accepted a symlink outside the conversation: %s", path)
	}
}
