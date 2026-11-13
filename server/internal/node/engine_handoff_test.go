package node

import (
	"strings"
	"testing"
	"time"
)

// engineHandoff over real files: a Claude Code transcript and a Codex rollout in a home of their own, read through
// the node's transcript helpers, each span its window of them.

const (
	handoffClaude = "0d000000-0000-4000-8000-0000000000c1"
	handoffCodex  = "0d000000-0000-4000-8000-0000000000c2"
)

func handoffAt(t *testing.T, s string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

// handoffFiles writes the conversation: on Claude Code until 10:05 (a fix, its test run, then a question after the
// switch back at 10:30), on Codex from 10:10 to 10:20.
func handoffFiles(t *testing.T) (*Node, string) {
	n, home := testNode(t)
	cwd := "/repo"
	writeTranscript(t, home, cwd, handoffClaude, time.Now(),
		`{"type":"user","uuid":"u1","timestamp":"2026-01-01T10:00:00Z","cwd":"/repo","sessionId":"`+handoffClaude+`","message":{"role":"user","content":"The login test fails, fix it."}}`,
		`{"type":"assistant","uuid":"a1","parentUuid":"u1","timestamp":"2026-01-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"look at the handler"},{"type":"tool_use","id":"toolu_1","name":"Edit","input":{"file_path":"/repo/login.go","old_string":"a","new_string":"b"}}]}}`,
		`{"type":"user","uuid":"u2","parentUuid":"a1","timestamp":"2026-01-01T10:01:30Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"The file /repo/login.go has been updated."}]}}`,
		`{"type":"assistant","uuid":"a2","parentUuid":"u2","timestamp":"2026-01-01T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_2","name":"Bash","input":{"command":"go test ./login/...","description":"Run the login tests"}}]}}`,
		`{"type":"user","uuid":"u3","parentUuid":"a2","timestamp":"2026-01-01T10:03:00Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_2","content":"Exit code 1\n--- FAIL: TestLogin (0.01s)","is_error":true}]}}`,
		`{"type":"assistant","uuid":"a3","parentUuid":"u3","timestamp":"2026-01-01T10:04:00Z","message":{"role":"assistant","content":[{"type":"text","text":"The handler is fixed; TestLogin still fails on the session cookie."}]}}`,
		`{"type":"user","uuid":"u4","parentUuid":"a3","timestamp":"2026-01-01T10:30:00Z","message":{"role":"user","content":"<persona-context kind=\"engine-switch\" title=\"Handoff\" from=\"codex\" to=\"claude\" resumed=\"1\">\nan earlier note\n</persona-context>\n\nIs the cookie fixed now?"}}`,
	)
	writeRollout(t, home, handoffCodex, cwd, handoffAt(t, "2026-01-01T10:10:00Z"), time.Now(),
		`{"timestamp":"2026-01-01T10:10:00Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"cu1","content":[{"type":"text","text":"<persona-context kind=\"engine-switch\" title=\"Handoff\" from=\"claude\" to=\"codex\">\nthe first note\n</persona-context>\n\nFix the cookie too."}]}}}`,
		`{"timestamp":"2026-01-01T10:11:00Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"Reasoning","id":"cr1","summary_text":["check the cookie path"]}}}`,
		`{"timestamp":"2026-01-01T10:12:00Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"FileChange","id":"cf1","status":"completed","changes":{"/repo/cookie.go":{"type":"update","unified_diff":"@@ -1 +1 @@\n-x\n+y"}}}}}`,
		`{"timestamp":"2026-01-01T10:13:00Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","id":"cc1","command":["/bin/bash","-lc","go test ./login/..."],"aggregated_output":"ok  \texample.com/login\t0.3s","exit_code":0,"status":"completed"}}}`,
		`{"timestamp":"2026-01-01T10:14:00Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"cm1","content":[{"type":"text","text":"Fixed the cookie path; the login tests pass."}]}}}`,
	)
	return n, home
}

func TestEngineHandoffReadsTheSpans(t *testing.T) {
	n, home := handoffFiles(t)
	switchedAt, back := handoffAt(t, "2026-01-01T10:05:00Z"), handoffAt(t, "2026-01-01T10:20:00Z")
	// Back to a fresh Claude Code session after Codex: the whole conversation, the Claude stretch up to the switch.
	full, err := n.engineHandoff([]handoffSpan{{SessionID: handoffClaude, Agent: "claude", Until: switchedAt},
		{SessionID: handoffCodex, Agent: "codex", After: switchedAt, Until: back}}, "codex", "claude", false)
	if err != nil {
		t.Fatal(err)
	}
	full = strings.ReplaceAll(full, home, "<home>")
	for _, want := range []string{
		"- /repo/login.go\n- /repo/cookie.go\n",
		"- go test ./login/... → exit 1\n- go test ./login/... → exit 0\n",
		"[User]\nThe login test fails, fix it.\n[Tool] Edit (id=toolu_1)",
		"---\n[User]\nFix the cookie too.\n[Tool] Edit (id=cf1-0)",
		"- Claude Code session " + handoffClaude + ": <home>/.claude/projects/-repo/" + handoffClaude + ".jsonl\n",
		"- Codex session " + handoffCodex + ": <home>/.codex/sessions/2026/01/01/rollout-",
	} {
		if !strings.Contains(full, want) {
			t.Errorf("full note lacks %q:\n%s", want, full)
		}
	}
	for _, gone := range []string{"Is the cookie fixed now?", "the first note", "look at the handler", "check the cookie path"} {
		if strings.Contains(full, gone) {
			t.Errorf("full note carries %q:\n%s", gone, full)
		}
	}

	// Back to Claude Code's own session: only the Codex stretch.
	delta, err := n.engineHandoff([]handoffSpan{{SessionID: handoffCodex, Agent: "codex", After: switchedAt}}, "codex", "claude", true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(delta, "The login test fails") || strings.Contains(delta, "/repo/login.go\n") ||
		!strings.Contains(delta, "== Conversation while you were away ==\n[User]\nFix the cookie too.") || strings.Contains(delta, ".claude/projects") {
		t.Errorf("delta note:\n%s", delta)
	}

	// A window after the switch back: only the question asked then, without the block it came behind.
	later, err := n.engineHandoff([]handoffSpan{{SessionID: handoffClaude, Agent: "claude", After: back}}, "claude", "codex", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(later, "[User]\nIs the cookie fixed now?") || strings.Contains(later, "an earlier note") || strings.Contains(later, "login.go") {
		t.Errorf("later window:\n%s", later)
	}

	// An unknown session is left out; with nothing readable at all the switch gets the error.
	if _, err := n.engineHandoff([]handoffSpan{{SessionID: "0d000000-0000-4000-8000-0000000000ff", Agent: "claude"}}, "claude", "codex", false); err == nil {
		t.Error("no error for a conversation with no transcript")
	}
	part, err := n.engineHandoff([]handoffSpan{{SessionID: "0d000000-0000-4000-8000-0000000000ff", Agent: "claude"},
		{SessionID: handoffCodex, Agent: "codex"}}, "codex", "claude", false)
	if err != nil || !strings.Contains(part, "Fix the cookie too.") {
		t.Errorf("a missing transcript stopped the note (%v):\n%s", err, part)
	}
}

// The note as the engines get it: a full one, a delta, and a delta with nothing in it (docs/assistant/prompts.md).
func TestGoldenEngineHandoff(t *testing.T) {
	n, home := handoffFiles(t)
	switchedAt, back := handoffAt(t, "2026-01-01T10:05:00Z"), handoffAt(t, "2026-01-01T10:20:00Z")
	note := func(spans []handoffSpan, from, to string, delta bool) string {
		t.Helper()
		text, err := n.engineHandoff(spans, from, to, delta)
		if err != nil {
			t.Fatal(err)
		}
		return strings.ReplaceAll(text, home, "<home>")
	}
	goldenNode(t, "engine_handoff.full", note([]handoffSpan{{SessionID: handoffClaude, Agent: "claude", Until: switchedAt},
		{SessionID: handoffCodex, Agent: "codex", After: switchedAt, Until: back}}, "codex", "claude", false))
	goldenNode(t, "engine_handoff.delta", note([]handoffSpan{{SessionID: handoffCodex, Agent: "codex", After: switchedAt}}, "codex", "claude", true))
	goldenNode(t, "engine_handoff.delta_empty", note([]handoffSpan{{SessionID: handoffClaude, Agent: "claude", After: back, Until: back}}, "claude", "codex", true))
	// The message the switch types: the note in its block, then the user's words.
	goldenNode(t, "engine_handoff.message", handoffMessage("The note.\n", "codex", "claude", "gpt-5.5", true, "Is the cookie fixed now?"))
}
