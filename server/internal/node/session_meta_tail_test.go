package node

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The quick read parses a transcript's head (to its first prompt) and its tail. Read into one Meta, the first
// prompt's time, its open turn and its mode were the session's latest whenever the tail held no exchange, and one
// line can fill a tail (#5896): the row's updated_at jumped back to the first message (days, for a session days
// old), it showed as working after every turn had ended, in the mode it was started in.

// twoTurns are two finished turns three days apart, the first in plan mode and the second accepting edits; the
// second answer asks the user a question.
var twoTurns = []string{
	`{"type":"user","uuid":"u1","parentUuid":null,"timestamp":"2026-11-01T08:00:00Z","cwd":"/src/app","permissionMode":"plan","message":{"role":"user","content":"first question"}}`,
	`{"type":"assistant","uuid":"a1","parentUuid":"u1","timestamp":"2026-11-01T08:00:05Z","cwd":"/src/app","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"first answer"}]}}`,
	`{"type":"system","subtype":"turn_duration","uuid":"d1","parentUuid":"a1","timestamp":"2026-11-01T08:00:06Z","cwd":"/src/app","durationMs":6000}`,
	`{"type":"user","uuid":"u2","parentUuid":"d1","timestamp":"2026-11-04T01:00:00Z","cwd":"/src/app/web","permissionMode":"acceptEdits","message":{"role":"user","content":"second question"}}`,
	`{"type":"assistant","uuid":"a2","parentUuid":"u2","timestamp":"2026-11-04T01:00:05Z","cwd":"/src/app/web","message":{"id":"m2","role":"assistant","content":[{"type":"text","text":"Both are done.\n\nWhich one should ship first?"}]}}`,
	`{"type":"system","subtype":"turn_duration","uuid":"d2","parentUuid":"a2","timestamp":"2026-11-04T01:00:06Z","cwd":"/src/app/web","durationMs":6000}`,
}

var (
	firstPrompt  = time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	secondPrompt = time.Date(2026, 11, 4, 1, 0, 0, 0, time.UTC)
	secondAnswer = time.Date(2026, 11, 4, 1, 0, 5, 0, time.UTC)
)

// requestBlob is a line of Claude Code's that is no exchange and fills a tail read by itself.
func requestBlob() string {
	return `{"type":"api-request-blob","sessionId":"s","blob":"` + strings.Repeat("x", quickTail+1) + `"}`
}

// A recap written while the session is idle: its request fills the tail, and the recap and the title after it are
// no exchange. The session is as its second turn left it, read for the first time or again.
func TestQuickMetaTailWithoutAnExchange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	lines := append(twoTurns[:len(twoTurns):len(twoTurns)], requestBlob(),
		`{"type":"system","subtype":"away_summary","uuid":"r1","parentUuid":"d2","timestamp":"2026-11-04T03:00:00Z","cwd":"/src/app/web","content":"You asked two questions; both are answered."}`,
		`{"type":"ai-title","sessionId":"s","aiTitle":"Two questions"}`)
	before := readQuickMeta(path, writeLines(t, path, twoTurns)) // the node's read before the recap
	st := writeLines(t, path, lines)
	written := time.Date(2026, 11, 4, 3, 0, 1, 0, time.UTC)
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatal(err)
	}
	for name, m := range map[string]quickMeta{"first read": readQuickMeta(path, st), "read again": readQuickMetaSince(path, st, before)} {
		if got := lastUsed(m, st); !got.Equal(secondAnswer) {
			t.Errorf("%s: last used %v, want the last exchange %v (the first prompt was at %v)", name, got, secondAnswer, firstPrompt)
		}
		if m.claudeTurn || !m.turnClosed {
			t.Errorf("%s: turn open %v, closed %v: both turns ended and the recap starts none", name, m.claudeTurn, m.turnClosed)
		}
		if m.mode != "acceptEdits" || !m.modeAt.Equal(secondPrompt) {
			t.Errorf("%s: mode %q at %v, want the second prompt's", name, m.mode, m.modeAt)
		}
		if !m.lastPromptAt.Equal(secondPrompt) || !m.lastTextAt.Equal(secondAnswer) {
			t.Errorf("%s: last prompt %v, last words %v", name, m.lastPromptAt, m.lastTextAt)
		}
		// The question outlasts the recap: no message came in after it (#5096).
		if m.lastText != "Both are done.\n\nWhich one should ship first?" || m.asks != "Which one should ship first?" {
			t.Errorf("%s: last text %q, asks %q", name, m.lastText, m.asks)
		}
		// What the head is read for stays the head's.
		if m.first != "first question" || m.start != "/src/app" || m.title != "Two questions" {
			t.Errorf("%s: first %q, started in %q, title %q", name, m.first, m.start, m.title)
		}
	}
}

// The transcript's last exchange is a line bigger than the tail (a screenshot's result), which the tail's start
// cuts: it is the last exchange, and its turn is still open. With a hook's summary after it, as Claude Code writes
// when the turn stops there, the same.
func TestQuickMetaLastExchangeBiggerThanTheTail(t *testing.T) {
	result := time.Date(2026, 11, 4, 1, 0, 7, 0, time.UTC)
	shot := []string{
		`{"type":"assistant","uuid":"a3","parentUuid":"u3","timestamp":"2026-11-04T01:00:05Z","message":{"id":"m3","role":"assistant","content":[{"type":"tool_use","id":"t1","name":"mcp__browser__screenshot","input":{}}]}}`,
		`{"type":"user","uuid":"r3","parentUuid":"a3","timestamp":"2026-11-04T01:00:07Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` +
			strings.Repeat("A", 900<<10) + `"}}]}]}}`,
	}
	prompt := `{"type":"user","uuid":"u3","parentUuid":"d1","timestamp":"2026-11-04T01:00:00Z","message":{"role":"user","content":"take a screenshot of the page"}}`
	for name, after := range map[string][]string{
		"nothing after it": nil,
		"a hook's summary": {`{"type":"system","subtype":"stop_hook_summary","uuid":"h1","parentUuid":"r3","timestamp":"2026-11-04T01:00:09Z","hookCount":1}`,
			`{"type":"last-prompt","lastPrompt":"take a screenshot of the page","sessionId":"s"}`},
	} {
		path := filepath.Join(t.TempDir(), "s.jsonl")
		st := writeLines(t, path, append(append(append(twoTurns[:3:3], prompt), shot...), after...))
		m := readQuickMeta(path, st)
		if !m.activity.Equal(result) {
			t.Errorf("%s: last exchange %v, want the screenshot's result %v (the first prompt was at %v)", name, m.activity, result, firstPrompt)
		}
		if !m.claudeTurn || m.turnClosed {
			t.Errorf("%s: turn open %v, closed %v: the turn the screenshot is in has not ended", name, m.claudeTurn, m.turnClosed)
		}
		if want := time.Date(2026, 11, 4, 1, 0, 0, 0, time.UTC); !m.lastPromptAt.Equal(want) || m.lastText != "first answer" {
			t.Errorf("%s: last prompt %v, last text %q", name, m.lastPromptAt, m.lastText)
		}
	}
}

// Through the list of sessions not running: a session used a minute ago whose last line is a screenshot's result
// is listed as used a minute ago, not when it was started.
func TestOfflineRowIsLastUsedAtItsLastExchange(t *testing.T) {
	n, _ := claudeHome(t)
	n.metas, n.bgScans = map[string]quickMeta{}, map[string]*bgScan{}
	const id = "dddddddd-0000-0000-0000-00000000a001"
	now := time.Now().UTC().Truncate(time.Second)
	started, last := now.Add(-72*time.Hour), now.Add(-time.Minute)
	at := func(t time.Time) string { return t.Format(time.RFC3339) }
	lines := []string{
		`{"type":"user","uuid":"u1","timestamp":"` + at(started) + `","cwd":"/src/app","message":{"role":"user","content":"first question"}}`,
		`{"type":"assistant","uuid":"a1","parentUuid":"u1","timestamp":"` + at(started.Add(5*time.Second)) + `","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"first answer"}]}}`,
		`{"type":"user","uuid":"u2","parentUuid":"a1","timestamp":"` + at(last.Add(-10*time.Second)) + `","message":{"role":"user","content":"take a screenshot"}}`,
		`{"type":"assistant","uuid":"a2","parentUuid":"u2","timestamp":"` + at(last.Add(-5*time.Second)) + `","message":{"id":"m2","role":"assistant","content":[{"type":"tool_use","id":"t1","name":"mcp__browser__screenshot","input":{}}]}}`,
		`{"type":"user","uuid":"r2","parentUuid":"a2","timestamp":"` + at(last) + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` +
			strings.Repeat("A", 500<<10) + `"}}]}]}}`,
	}
	writeLines(t, filepath.Join(n.home, ".claude", "projects", "-src-app", id+".jsonl"), lines)
	for _, s := range n.scanOffline() {
		if s.SessionID != id {
			continue
		}
		if !s.UpdatedAt.Equal(last) {
			t.Fatalf("updated_at %v, want its last exchange %v (it started at %v)", s.UpdatedAt, last, started)
		}
		return
	}
	t.Fatal("the session is not listed")
}

// A long turn: its prompt is soon out of the tail, and the tail then starts and ends no turn. Read as the node reads
// a growing transcript (each read from the last), the turn stays open, in the mode and at the time of its own prompt;
// a first read after it (a node just started) finds the same further back. Then the turn ends, and lines that are no
// exchange push its end out of the tail: it stays ended.
func TestQuickMetaTurnStandsWhenTheTailSaysNothingOfIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	lines := append(twoTurns[:3:3],
		`{"type":"user","uuid":"u2","parentUuid":"d1","timestamp":"2026-11-04T01:00:00Z","permissionMode":"acceptEdits","message":{"role":"user","content":"read every file"}}`)
	last := time.Time{}
	step := func(i int) {
		at := secondPrompt.Add(time.Duration(i+1) * time.Minute)
		last = at.Add(time.Second)
		lines = append(lines,
			fmt.Sprintf(`{"type":"assistant","uuid":"a%d","timestamp":%q,"message":{"id":"t%d","role":"assistant","content":[{"type":"tool_use","id":"c%d","name":"Read","input":{"file_path":"/src/app/f%d.go"}}]}}`, 10+i, at.Format(time.RFC3339), i, i, i),
			fmt.Sprintf(`{"type":"user","uuid":"r%d","timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"c%d","content":"%s"}]}}`, 10+i, last.Format(time.RFC3339), i, strings.Repeat("x", 60<<10)))
	}
	check := func(name string, m quickMeta, open bool) {
		t.Helper()
		if m.claudeTurn != open || m.turnClosed == open {
			t.Errorf("%s: turn open %v, closed %v", name, m.claudeTurn, m.turnClosed)
		}
		if !m.activity.Equal(last) || !m.lastPromptAt.Equal(secondPrompt) || m.mode != "acceptEdits" || !m.modeAt.Equal(secondPrompt) {
			t.Errorf("%s: last exchange %v (want %v), last prompt %v, mode %q at %v", name, m.activity, last, m.lastPromptAt, m.mode, m.modeAt)
		}
		if m.first != "first question" || m.start != "/src/app" {
			t.Errorf("%s: first %q, started in %q", name, m.first, m.start)
		}
	}
	m := readQuickMeta(path, writeLines(t, path, lines))
	for i := range 10 { // 600 KB of tool results, a read after each
		step(i)
		m = readQuickMetaSince(path, writeLines(t, path, lines), m)
	}
	st, _ := os.Stat(path)
	if st.Size() < quickTail+200<<10 {
		t.Fatalf("the prompt has to be out of the tail: %d bytes", st.Size())
	}
	check("read as it grew", m, true)
	check("first read", readQuickMeta(path, st), true)

	// The turn ends; hook receipts (no exchange, no turn) then fill the tail.
	lines = append(lines,
		fmt.Sprintf(`{"type":"assistant","uuid":"a9","timestamp":%q,"message":{"id":"m9","role":"assistant","content":[{"type":"text","text":"Read them all."}]}}`, last.Add(time.Second).Format(time.RFC3339)),
		fmt.Sprintf(`{"type":"system","subtype":"turn_duration","uuid":"d9","parentUuid":"a9","timestamp":%q,"durationMs":600000}`, last.Add(2*time.Second).Format(time.RFC3339)))
	last = last.Add(time.Second)
	m = readQuickMetaSince(path, writeLines(t, path, lines), m)
	check("ended", m, false)
	for i := range 8 {
		lines = append(lines, fmt.Sprintf(`{"type":"system","subtype":"async_hook_response","uuid":"h%d","timestamp":%q,"content":"%s"}`, i,
			last.Add(time.Duration(i+1)*time.Hour).Format(time.RFC3339), strings.Repeat("y", 60<<10)))
		m = readQuickMetaSince(path, writeLines(t, path, lines), m)
	}
	check("its end out of the tail, read as it grew", m, false)
	st, _ = os.Stat(path)
	check("its end out of the tail, first read", readQuickMeta(path, st), false)
	if m.lastText != "Read them all." {
		t.Errorf("last text %q", m.lastText)
	}
}

// A session whose one prompt is its last exchange: the first prompt's time is the right one then, found before the
// tail like any other.
func TestQuickMetaFirstPromptIsTheLastExchangeWhenItIsTheOnlyOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	m := readQuickMeta(path, writeLines(t, path, []string{twoTurns[0], requestBlob()}))
	if !m.activity.Equal(firstPrompt) || !m.lastPromptAt.Equal(firstPrompt) || !m.claudeTurn || m.mode != "plan" || m.first != "first question" {
		t.Fatalf("last exchange %v, last prompt %v, turn open %v, mode %q, first %q", m.activity, m.lastPromptAt, m.claudeTurn, m.mode, m.first)
	}
}

// A Codex rollout says its directory, version and client on its first line only: they stay when a turn has written
// more than the tail, and so does the turn (codex_more_test.go). Its last exchange is the turn's latest item, not the
// first prompt, when only tool output (no item) fills the tail.
func TestQuickMetaRolloutLongerThanTheTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-2026-11-01T08-00-00-"+testThread+".jsonl")
	lines := []string{
		`{"timestamp":"2026-11-01T08:00:00Z","type":"session_meta","payload":{"id":"` + testThread + `","cwd":"/work","cli_version":"0.170.0","originator":"codex_cli_rs"}}`,
		`{"timestamp":"2026-11-01T08:00:00Z","type":"turn_context","payload":{"cwd":"/work","model":"gpt-5.5","approval_policy":"on-request","sandbox_policy":{"type":"workspace-write"}}}`,
		`{"timestamp":"2026-11-01T08:00:01Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t1"}}`,
		`{"timestamp":"2026-11-01T08:00:01Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u1","content":[{"type":"text","text":"build it"}]}}}`,
		`{"timestamp":"2026-11-01T08:00:09Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"t1"}}`,
		`{"timestamp":"2026-11-04T01:00:00Z","type":"turn_context","payload":{"cwd":"/work","model":"gpt-5.5","approval_policy":"never","sandbox_policy":{"type":"danger-full-access"}}}`,
		`{"timestamp":"2026-11-04T01:00:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t2"}}`,
		`{"timestamp":"2026-11-04T01:00:00Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u2","content":[{"type":"text","text":"now test it"}]}}}`,
		`{"timestamp":"2026-11-04T01:00:05Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"m2","content":[{"type":"Text","text":"Running the tests."}]}}}`,
	}
	for range 8 { // 512 KB of a command's output: no item
		lines = append(lines, `{"timestamp":"2026-11-04T01:00:06Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"`+strings.Repeat("x", 64<<10)+`"}}`)
	}
	before := readQuickMeta(path, writeLines(t, path, lines[:9]))
	st := writeLines(t, path, lines)
	for name, m := range map[string]quickMeta{"first read": readQuickMeta(path, st), "read again": readQuickMetaSince(path, st, before)} {
		if !m.codex || m.cwd != "/work" || m.version != "0.170.0" || m.surface == "" || m.first != "build it" || m.model != "gpt-5.5" {
			t.Errorf("%s: codex %v, cwd %q, version %q, surface %q, first %q, model %q", name, m.codex, m.cwd, m.version, m.surface, m.first, m.model)
		}
		if !m.turnOpen || m.turnClosed {
			t.Errorf("%s: turn open %v, closed %v: the second turn runs", name, m.turnOpen, m.turnClosed)
		}
		if !m.activity.Equal(secondAnswer) || !m.lastPromptAt.Equal(secondPrompt) || m.lastText != "Running the tests." {
			t.Errorf("%s: last exchange %v, last prompt %v, last text %q", name, m.activity, m.lastPromptAt, m.lastText)
		}
		if m.mode != "never" || m.sandbox != "danger-full-access" {
			t.Errorf("%s: mode %q, sandbox %q, want the second turn's", name, m.mode, m.sandbox)
		}
	}
}
