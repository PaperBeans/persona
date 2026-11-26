package node

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestUltracodeVersionGate(t *testing.T) {
	for _, tc := range []struct {
		version   string
		supported bool
	}{
		{"", true}, {"unknown", true}, {"2.1.202", false}, {"2.1.203", false}, {"2.1.283", false},
		{"2.1.284", true}, {"2.1.318", true}, {"v2.1.318", true}, {"2.2.0", true}, {"3.0.0", true},
		{"2.1.284-beta.1", true}, {"2.1.284+build", true}, {"1.9.999", false},
	} {
		t.Run(tc.version, func(t *testing.T) {
			if got := ultracodeVersionNote(tc.version) == ""; got != tc.supported {
				t.Fatalf("supported=%v, want %v", got, tc.supported)
			}
		})
	}
	if got := parseClaudeVersion("2.1.318 (Claude Code)\n"); got != "2.1.318" {
		t.Fatal(got)
	}
}

func TestUltracodeOutcome(t *testing.T) {
	for _, tc := range []struct {
		name, output string
		on           bool
		value        *bool
		note         string
		rejected     bool
	}{
		{"on", "Ultracode on (this session only): dynamic workflows on every task. Effort stays high.", true, ultraBool(true), "", false},
		{"off", "Ultracode off. Effort stays max.", false, ultraBool(false), "", false},
		{"workflows", "Ultracode needs dynamic workflows enabled (see /config).", true, nil, "工作流", false},
		{"model", "Ultracode isn't available on Sonnet. Switch to Opus to use it.", true, nil, "Sonnet", true},
		{"invalid", "Invalid argument: ultracode on", true, nil, "未接受", false},
		{"usage", "Usage: /effort [auto|low|medium|high|xhigh|max] or /effort ultracode [on|off]", true, nil, "未接受", false},
		{"env", "Not applied: CLAUDE_CODE_EFFORT_LEVEL=high overrides effort this session, and max is session-only (nothing saved)", true, nil, "CLAUDE_CODE_EFFORT_LEVEL=high", false},
		{"env override", "CLAUDE_CODE_EFFORT_LEVEL=high overrides this session — clear it and max takes over", true, nil, "CLAUDE_CODE_EFFORT_LEVEL=high", false},
		{"opposite", "Ultracode off. Effort stays high.", true, ultraBool(false), "", false},
		{"unknown", "Working…", true, nil, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, note, rejected := ultracodeOutcome("❯ "+ultracodeCommand(tc.on)+"\n  ⎿ "+tc.output, tc.on)
			if (got == nil) != (tc.value == nil) || got != nil && *got != *tc.value || !strings.Contains(note, tc.note) || tc.note == "" && note != "" || rejected != tc.rejected {
				t.Fatalf("got %v %q %v", got, note, rejected)
			}
		})
	}
	for _, shown := range []string{"Ultracode on (this session only): dynamic workflows on every task. Effort stays high.", "❯ /effort ultracode on\nUltracode on (this session only): dynamic workflows on every task. Effort stays high.\n❯ /effort ultracode on"} {
		if got, note, _ := ultracodeOutcome(shown, true); got != nil || note != "" {
			t.Fatalf("accepted stale verdict: %v %q", got, note)
		}
	}
}

func ultraScreens(t *testing.T, f *fakeHerdr, pane string) {
	t.Helper()
	plain, ansi := screenFixture(t, "claude-2.1.312-empty-162.txt"), screenFixture(t, "claude-2.1.312-empty-162.ansi")
	f.setScreens(pane, plain, ansi)
	f.after(func(c herdrCall) {
		if c.Method != "agent.prompt" {
			return
		}
		cmd := str(c.Params["text"])
		if cmd == ultracodeCommand(true) {
			f.setScreens(pane, plain+"\n❯ "+cmd+"\n  ⎿ Ultracode on (this session only): dynamic workflows on every task. Effort stays high.\n", ansi)
		}
		if cmd == ultracodeCommand(false) {
			f.setScreens(pane, plain+"\n❯ "+cmd+"\n  ⎿ Ultracode off. Effort stays high.\n", ansi)
		}
	})
}

func requestUltra(t *testing.T, n *Node, on bool) error {
	t.Helper()
	_, err := request(t, n, "session.ultracode", map[string]any{"session_id": sidA, "ultracode": on})
	return err
}

func TestHerdrUltracodeReadsBackAndLeavesEffortAlone(t *testing.T) {
	n, f, _ := herdrNode(t)
	seedAgents(t, n, f, claudeAgent(sidA, proto.StatusIdle, "w1:p1", "w1:t1", "w1", "/tmp/project", ""))
	ultraScreens(t, f, "w1:p1")
	n.noteModes(sidA, "plan", "low")
	for _, on := range []bool{true, false} {
		if err := requestUltra(t, n, on); err != nil {
			t.Fatal(err)
		}
		s := byID(listSessions(t, n))[sidA]
		mode, effort := n.modesOf(sidA)
		if s.Ultracode == nil || *s.Ultracode != on || s.UltracodePending || effort != "low" || mode != "plan" {
			t.Fatalf("session=%+v", s)
		}
	}
	if got := f.trace("agent.prompt"); len(got) != 2 {
		t.Fatalf("commands: %v", got)
	}
}

func TestHerdrUltracodeQueuesUntilBeforeTheNextMessage(t *testing.T) {
	n, f, _ := herdrNode(t)
	a := claudeAgent(sidA, proto.StatusWorking, "w1:p1", "w1:t1", "w1", "/tmp/project", "")
	seedAgents(t, n, f, a)
	ultraScreens(t, f, "w1:p1")
	if err := requestUltra(t, n, true); err != nil {
		t.Fatal(err)
	}
	if got := f.trace("agent.prompt"); len(got) != 0 {
		t.Fatal(got)
	}
	s := byID(listSessions(t, n))[sidA]
	if !s.UltracodePending || s.Ultracode != nil {
		t.Fatalf("queued=%+v", s)
	}
	// A queued switch holds back only its command (#5510): at work, the message joins Claude's own queue.
	if err := n.send(context.Background(), sidA, "while it works", nil, sendOpts{}); err != nil {
		t.Fatalf("a message was held back by the queued switch: %v", err)
	}
	if got := f.trace("agent.prompt"); len(got) != 1 || !strings.Contains(got[0], "while it works") {
		t.Fatalf("typed while at work: %v", got)
	}
	if s := byID(listSessions(t, n))[sidA]; !s.UltracodePending {
		t.Fatalf("the message took the queued switch away: %+v", s)
	}
	// The latest request wins; none of the superseded values gets typed.
	if err := requestUltra(t, n, false); err != nil {
		t.Fatal(err)
	}
	if err := requestUltra(t, n, true); err != nil {
		t.Fatal(err)
	}
	a["agent_status"] = proto.StatusDone
	seedAgents(t, n, f, a)
	if err := n.send(context.Background(), sidA, "next task", nil, sendOpts{}); err != nil {
		t.Fatal(err)
	}
	got := f.trace("agent.prompt")
	if len(got) != 2 || !strings.Contains(got[0], "/effort ultracode on") || !strings.Contains(got[1], "next task") {
		t.Fatalf("wrong order: %v", got)
	}
}

func TestHerdrUltracodeQueuedDraftIsPreserved(t *testing.T) {
	n, f, _ := herdrNode(t)
	a := claudeAgent(sidA, proto.StatusBlocked, "w1:p1", "w1:t1", "w1", "/tmp/project", "")
	seedAgents(t, n, f, a)
	if err := requestUltra(t, n, true); err != nil {
		t.Fatal(err)
	}
	a["agent_status"] = proto.StatusDone
	seedAgents(t, n, f, a)
	f.setScreens("w1:p1", screenFixture(t, "claude-2.1.312-typed-162.txt"), screenFixture(t, "claude-2.1.312-typed-162.ansi"))
	if err := n.send(context.Background(), sidA, "next task", nil, sendOpts{}); err == nil {
		t.Fatal("draft was overwritten")
	}
	if got := f.trace("agent.prompt", "pane.send_keys", "pane.send_text"); len(got) != 0 {
		t.Fatal(got)
	}
	n.mu.Lock()
	pending := n.ultracodeLocked(sidA).pending
	n.mu.Unlock()
	if pending == nil || !*pending {
		t.Fatal("lost queued request")
	}
}

func TestHerdrUltracodeRejectionAndModelChange(t *testing.T) {
	n, f, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "project"))
	path := writeTranscript(t, home, cwd, sidA, time.Now(), `{"type":"assistant","timestamp":"2026-01-01T00:00:00Z","version":"2.1.318","message":{"model":"sonnet","content":[{"type":"text","text":"Done"}]}}`)
	n.paths[sidA] = path
	seedAgents(t, n, f, claudeAgent(sidA, proto.StatusDone, "w1:p1", "w1:t1", "w1", cwd, ""))
	plain, ansi := screenFixture(t, "claude-2.1.312-empty-162.txt"), screenFixture(t, "claude-2.1.312-empty-162.ansi")
	f.setScreens("w1:p1", plain, ansi)
	f.after(func(c herdrCall) {
		if c.Method == "agent.prompt" {
			f.setScreens("w1:p1", plain+"\n❯ /effort ultracode on\n  ⎿ Ultracode isn't available on Sonnet. Switch to Opus to use it.\n", ansi)
		}
	})
	if err := requestUltra(t, n, true); err == nil || !strings.Contains(err.Error(), "Sonnet") {
		t.Fatalf("refusal: %v", err)
	}
	before := len(f.trace("agent.prompt"))
	if err := requestUltra(t, n, true); err == nil {
		t.Fatal("retry on rejected model accepted")
	}
	if len(f.trace("agent.prompt")) != before {
		t.Fatal("repeated unsupported command")
	}
	n.noteModel(sidA, "opus", "sonnet")
	ultraScreens(t, f, "w1:p1")
	if err := requestUltra(t, n, true); err != nil {
		t.Fatalf("model changed before transcript: %v", err)
	}
}

func TestHerdrUltracodeVersionUsesSessionInsteadOfDefault(t *testing.T) {
	n, f, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "project"))
	path := writeTranscript(t, home, cwd, sidA, time.Now(), `{"type":"assistant","timestamp":"2026-01-01T00:00:00Z","version":"2.1.283","message":{"model":"opus","content":[{"type":"text","text":"Done"}]}}`)
	n.paths[sidA] = path
	n.claudeV.set("2.1.318")
	seedAgents(t, n, f, claudeAgent(sidA, proto.StatusDone, "w1:p1", "w1:t1", "w1", cwd, ""))
	if err := requestUltra(t, n, true); err == nil || !strings.Contains(err.Error(), "升级") {
		t.Fatal(err)
	}
	if got := f.trace("agent.prompt"); len(got) != 0 {
		t.Fatal(got)
	}
	if s := byID(listSessions(t, n))[sidA]; !strings.Contains(s.UltracodeNote, "升级") {
		t.Fatal(s.UltracodeNote)
	}
}

func TestStartUltracodeBeforeFirstPrompt(t *testing.T) {
	n, f, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "project"))
	startPane(t, f, screenFixture(t, "claude-2.1.312-empty-162.txt"), screenFixture(t, "claude-2.1.312-empty-162.ansi"))
	plain, ansi := screenFixture(t, "claude-2.1.312-empty-162.txt"), screenFixture(t, "claude-2.1.312-empty-162.ansi")
	f.after(func(c herdrCall) {
		if c.Method != "agent.prompt" {
			return
		}
		text := str(c.Params["text"])
		if text == ultracodeCommand(true) {
			f.setScreens("w1:p2", plain+"\n❯ "+text+"\n  ⎿ Ultracode on (this session only): dynamic workflows on every task. Effort stays low.\n", ansi)
		} else {
			writeUserLine(t, home, cwd, startedID(f), text)
		}
	})
	_, err := request(t, n, "session.start", map[string]any{"cwd": cwd, "workspace_id": "w1", "prompt": "fix the build", "ultracode": true, "effort": "low"})
	if err != nil {
		t.Fatal(err)
	}
	n.firstWait.Wait()
	got := f.trace("agent.prompt")
	if len(got) != 2 || !strings.Contains(got[0], "/effort ultracode on") || !strings.Contains(got[1], "fix the build") {
		t.Fatalf("wrong start order: %v", got)
	}
	for _, c := range f.trace("pane.send_input") {
		if strings.Contains(c, "--effort ultracode") {
			t.Fatal(c)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("settings touched: %v", err)
	}
}

// These screens are synthetic, using the issue's verdicts and the installed
// CLI's static strings. No real Claude session or personal transcript is used.
func TestUltracodeScreenFixtures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		on    bool
		value *bool
		note  string
	}{
		{"on", true, ultraBool(true), ""}, {"off", false, ultraBool(false), ""},
		{"workflows", true, nil, "工作流"}, {"model", true, nil, "Sonnet"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, note, _ := ultracodeOutcome(screenFixture(t, "claude-2.1.318-ultracode-"+tc.name+".txt"), tc.on)
			if (value == nil) != (tc.value == nil) || value != nil && *value != *tc.value || !strings.Contains(note, tc.note) {
				t.Fatalf("value=%v note=%q", value, note)
			}
		})
	}
}

func TestHerdrUltracodeRunsQueuedRequestAfterTheTurn(t *testing.T) {
	n, f, _ := herdrNode(t)
	a := claudeAgent(sidA, proto.StatusWorking, "w1:p1", "w1:t1", "w1", "/tmp/project", "")
	seedAgents(t, n, f, a)
	ultraScreens(t, f, "w1:p1")
	if err := requestUltra(t, n, true); err != nil {
		t.Fatal(err)
	}
	a["agent_status"] = proto.StatusDone
	seedAgents(t, n, f, a)
	n.mu.Lock()
	n.ultracodeLocked(sidA).retryAt = time.Time{}
	n.mu.Unlock()
	_ = listSessions(t, n)
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		n.mu.Lock()
		u := n.ultracodeLocked(sidA)
		done := u.Value != nil && *u.Value && u.pending == nil && !u.applying
		n.mu.Unlock()
		if done {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("queued switch was never applied after the turn")
}

func TestUltracodeRefusesAssistantAndOtherAgents(t *testing.T) {
	n, f, home := herdrNode(t)
	n.state.put(sidA, sessionState{Cwd: filepath.Join(home, ".persona", "assistant")})
	if err := requestUltra(t, n, true); err == nil {
		t.Fatal("assistant accepted ultracode")
	}
	seedAgents(t, n, f, agentOf("codex", sidA, proto.StatusIdle, "w1:p1", "w1:t1", "w1", "/tmp/project", ""))
	if err := requestUltra(t, n, true); err == nil {
		t.Fatal("Codex accepted ultracode")
	}
	if got := f.trace("agent.prompt"); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestUltracodeOwnStartupDoesNotSwallowExternalResume(t *testing.T) {
	n, _, _ := herdrNode(t)
	n.beginUltracodeProcess(sidA, false, true, time.Now().Add(-time.Second))
	n.mu.Lock()
	u := n.ultracodeLocked(sidA)
	u.Value, u.At = ultraBool(true), time.Now()
	n.mu.Unlock()
	n.ultracodeHook(sockRequest{}, hookInput{SessionID: sidA, HookEventName: "SessionStart", Source: "resume"})
	n.mu.Lock()
	defer n.mu.Unlock()
	u = n.ultracodeLocked(sidA)
	if u.Value == nil || *u.Value || u.LostAt == nil {
		t.Fatalf("external resume skipped: %+v", u)
	}
}

func TestUltracodeIgnoresVerdictFromPreviousProcess(t *testing.T) {
	n, f, _ := herdrNode(t)
	seedAgents(t, n, f, claudeAgent(sidA, proto.StatusIdle, "w1:p1", "w1:t1", "w1", "/tmp/project", ""))
	plain, ansi := screenFixture(t, "claude-2.1.312-empty-162.txt"), screenFixture(t, "claude-2.1.312-empty-162.ansi")
	f.setScreens("w1:p1", plain, ansi)
	f.after(func(c herdrCall) {
		if c.Method == "agent.prompt" {
			n.beginUltracodeProcess(sidA, true, false, time.Now())
			f.setScreens("w1:p1", plain+"\n❯ /effort ultracode on\n  ⎿ Ultracode on (this session only): dynamic workflows on every task. Effort stays high.\n", ansi)
		}
	})
	if err := requestUltra(t, n, true); err == nil {
		t.Fatal("previous process verdict accepted")
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	u := n.ultracodeLocked(sidA)
	if u.Value == nil || *u.Value || u.note != "" {
		t.Fatalf("new process polluted: %+v", u)
	}
}

func TestStartAsyncUltracodeDoesNotDeadlockAnEarlyMessage(t *testing.T) {
	n, f, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "project"))
	plain, ansi := screenFixture(t, "claude-2.1.312-empty-162.txt"), screenFixture(t, "claude-2.1.312-empty-162.ansi")
	startPane(t, f, plain, ansi)
	f.after(func(c herdrCall) {
		if c.Method != "agent.prompt" {
			return
		}
		text := str(c.Params["text"])
		if text == ultracodeCommand(true) {
			f.setScreens("w1:p2", plain+"\n❯ "+text+"\n  ⎿ Ultracode on (this session only): dynamic workflows on every task. Effort stays high.\n", ansi)
		} else {
			id := startedID(f)
			writeUserLine(t, home, cwd, id, text)
			f.reply("session.snapshot", snapshot(nil, nil, []any{claudeAgent(id, proto.StatusWorking, "w1:p2", "w1:t2", "w1", cwd, "")}, nil))
		}
	})
	s, err := n.startAsync("", cwd, "claude", "first task", "w1", "", "", "", modelExecution{Ultracode: ultraBool(true)})
	if err != nil {
		t.Fatal(err)
	}
	if !s.Starting {
		t.Fatal("async start did not return starting")
	}
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		done <- n.send(ctx, s.SessionID, "second task", nil, sendOpts{})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("start and early send deadlocked")
	}
	n.firstWait.Wait()
	got := f.trace("agent.prompt")
	if len(got) != 3 || !strings.Contains(got[0], "/effort ultracode on") || !strings.Contains(got[1], "first task") || !strings.Contains(got[2], "second task") {
		t.Fatalf("order: %v", got)
	}
}

// When session.ultracode commits dismiss_lost before the toggle is refused by agent_blocked, it returns a partial
// unknown-delivery error (not delivered=none) so client_ops does not record the committed dismissal as failed.
func TestUltracodeDismissLostWithBlockedToggleReturnsPartial(t *testing.T) {
	n, f, home := herdrNode(t)
	seedAgents(t, n, f, claudeAgent(sidA, proto.StatusIdle, "w1:p1", "w1:t1", "w1", home, ""))
	plain, ansi := screenFixture(t, "claude-2.1.312-empty-162.txt"), screenFixture(t, "claude-2.1.312-empty-162.ansi")
	f.setScreens("w1:p1", plain, ansi)
	f.reply("agent.prompt", herdrFail{Code: "agent_blocked", Message: "interactive input required"})
	lostAt := time.Now().UTC()
	n.mu.Lock()
	u := n.ultracodeLocked(sidA)
	u.LostAt = &lostAt
	u.LostDismissed = false
	n.mu.Unlock()
	_, err := request(t, n, "session.ultracode", map[string]any{"session_id": sidA, "ultracode": true, "dismiss_lost": true})
	code, data := refusalOf(t, err)
	if code != "dialog_open" || data["delivered"] != deliveredUnknown || data["partial"] != true || data["retryable"] != false {
		t.Fatalf("dismiss+blocked = code=%q data=%v, want dialog_open delivered=unknown partial=true retryable=false", code, data)
	}
	n.mu.Lock()
	u = n.ultracodeLocked(sidA)
	dismissed := u.LostDismissed && u.LostAt == nil
	n.mu.Unlock()
	if !dismissed {
		t.Fatalf("lost notice was not dismissed: %+v", u)
	}

	// When a replacement toggle without dismiss_lost is definitively refused (delivered=none), preserve the previously
	// queued ultracode toggle instead of dropping it.
	t.Run("refused replacement preserves prior queued toggle", func(t *testing.T) {
		n2, f2, home2 := herdrNode(t)
		seedAgents(t, n2, f2, claudeAgent(sidA, proto.StatusIdle, "w1:p1", "w1:t1", "w1", home2, ""))
		f2.setScreens("w1:p1", plain, ansi)
		f2.reply("agent.prompt", herdrFail{Code: "agent_blocked", Message: "interactive input required"})
		n2.mu.Lock()
		u2 := n2.ultracodeLocked(sidA)
		u2.pending = ultraBool(false)
		u2.note = "waiting for turn"
		n2.mu.Unlock()
		_, err := request(t, n2, "session.ultracode", map[string]any{"session_id": sidA, "ultracode": true})
		code, data := refusalOf(t, err)
		if code != "dialog_open" || data["delivered"] != "none" {
			t.Fatalf("refused replacement = code=%q data=%v, want dialog_open delivered=none", code, data)
		}
		n2.mu.Lock()
		u2 = n2.ultracodeLocked(sidA)
		restored := u2.pending != nil && !*u2.pending && u2.note == "waiting for turn"
		n2.mu.Unlock()
		if !restored {
			t.Fatalf("prior queued toggle was lost: %+v", u2)
		}
	})

	// If a new Claude process begins while a replacement toggle is refused, do not restore the old process's queue.
	t.Run("refused replacement does not restore prior queue across process change", func(t *testing.T) {
		n3, f3, home3 := herdrNode(t)
		seedAgents(t, n3, f3, claudeAgent(sidA, proto.StatusIdle, "w1:p1", "w1:t1", "w1", home3, ""))
		f3.setScreens("w1:p1", plain, ansi)
		n3.beginUltracodeProcess(sidA, false, true, time.Now().Add(-time.Second))
		n3.mu.Lock()
		u3 := n3.ultracodeLocked(sidA)
		u3.pending = ultraBool(false)
		n3.mu.Unlock()
		f3.handle("agent.prompt", func(map[string]any) any {
			n3.beginUltracodeProcess(sidA, true, false, time.Now())
			return herdrFail{Code: "agent_blocked", Message: "interactive input required"}
		})
		_, err := request(t, n3, "session.ultracode", map[string]any{"session_id": sidA, "ultracode": true})
		if code, data := refusalOf(t, err); code != "dialog_open" || data["delivered"] != "none" {
			t.Fatalf("code=%q data=%v, want dialog_open delivered=none", code, data)
		}
		n3.mu.Lock()
		pending := n3.ultracodeLocked(sidA).pending
		n3.mu.Unlock()
		if pending != nil {
			t.Fatalf("old process's queued toggle leaked onto new process: %v", *pending)
		}
	})
}
