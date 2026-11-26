package node

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

// The whole terminal route, through the same node requests the hub makes: start omp with its first prompt, join its
// extension to herdr's pane, send there, then resume the stopped session in a replacement omp after the process exits.
func TestOmpEndToEndStartJoinSendExitResume(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", "")
	withPiFamilyGrace(t, 3*time.Second)
	n, f, home, rh := piHerdrNode(t)
	cwd := t.TempDir()
	const id = "01d10000-0000-7000-8000-000000000001"
	file := writeOmpSession(t, ompSessionsDir(home), "-w-e2e", id, "omp end to end", cwd, time.Now())
	initial := ompExtensionIdle(t, rh, "omp-e2e-initial")

	// The first start uses a different pane from ompResumeHerdr's replacement tab below. Once omp's command is typed,
	// herdr reports its session file and the extension in that process lists the same session in the new pane.
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w1:t9"}, "root_pane": map[string]any{"pane_id": "w1:p9"}})
	f.after(func(c herdrCall) {
		if c.Method != "pane.send_input" || str(c.Params["pane_id"]) != "w1:p9" {
			return
		}
		f.reply("session.snapshot", snapshot(nil, nil, []any{ompPaneAgent("w1:p9", cwd, file)}, nil))
		_ = initial.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{{
			"session_id": id, "status": "idle", "title": "joined by the extension", "last_text": "ready",
			"pane_id": "w1:p9", "cwd": cwd,
		}}})
	})

	const firstPrompt = "inspect the parser"
	started, err := request(t, n, "session.start", map[string]any{
		"cwd": cwd, "workspace_id": "w1", "launcher": "omp", "agent_type": "omp", "prompt": firstPrompt,
	})
	if err != nil {
		t.Fatalf("session.start: %v\nherdr: %v", err, f.trace())
	}
	if s, _ := started["session"].(map[string]any); s["session_id"] != id || s["agent"] != "omp" {
		t.Fatalf("session.start answered %v", started)
	}
	// A real session.start sends the caller's first prompt unchanged when no agent-rules.md is configured.
	wantCommand := call("pane.send_input", map[string]any{"pane_id": "w1:p9", "keys": []string{"Enter"},
		"text": inAgentEnv(n.dir, "omp "+shellQuote(firstPrompt), shellQuote)})
	if got := f.trace("pane.send_input"); len(got) != 1 || got[0] != wantCommand {
		t.Fatalf("omp launch = %v, want %s", got, wantCommand)
	}
	if got := len(initial.got("session.send")); got != 0 {
		t.Fatalf("the first prompt bypassed omp's command in %d runtime sends", got)
	}

	joined := onlyRow(t, listSessions(t, n), id)
	if joined.Agent != "omp" || joined.PaneID != "w1:p9" || joined.SpaceKind != "herdr" || joined.Cwd != cwd ||
		joined.Title != "joined by the extension" || joined.LastText != "ready" || joined.ReadOnlyReason != "" {
		t.Fatalf("the extension and herdr rows did not join: %+v", joined)
	}
	if _, err := request(t, n, "session.send", map[string]any{
		"session_id": id, "text": "follow up through the node", "client_msg_id": "omp-e2e-follow-up",
	}); err != nil {
		t.Fatalf("follow-up: %v", err)
	}
	if got := initial.got("session.send"); len(got) != 1 || !strings.Contains(string(got[0]), "follow up through the node") {
		t.Fatalf("the follow-up did not reach the initial extension: %v", got)
	}

	// omp and its pane exit, leaving a valid session file. The disconnected extension must not leave a writable row.
	if err := initial.conn.Close(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the initial omp extension to exit", func() bool { return rh.reg.ownerOf(id) == nil })
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	offline := onlyRow(t, listSessions(t, n), id)
	if offline.Agent != "omp" || offline.Status != proto.StatusOffline || offline.Cwd != cwd || offline.Title != "omp end to end" {
		t.Fatalf("the exited omp is not its on-disk offline row: %+v", offline)
	}

	// A new process loads the extension in the new tab. Until it lists the session, items lookups fail just as a real
	// runtime with no open session answers; the resumed session's message must go to this replacement, not the old one.
	replacement := ompExtensionIdle(t, rh, "omp-e2e-replacement")
	replacement.mu.Lock()
	replacement.answer = func(method string, _ json.RawMessage) (any, error) {
		if method == "session.items" {
			return nil, errors.New("no such session")
		}
		return map[string]any{"ok": true}, nil
	}
	replacement.mu.Unlock()
	argv := ompResumeHerdr(t, f, cwd, id, replacement)
	f.reset()
	const resumePrompt = "continue in the replacement omp"
	if _, err := request(t, n, "session.send", map[string]any{
		"session_id": id, "text": resumePrompt, "client_msg_id": "omp-e2e-resume",
	}); err != nil {
		t.Fatalf("resume send: %v\nherdr: %v", err, f.trace())
	}
	if got := argv(); !slices.Equal(got, []string{"omp", "--resume", file}) {
		t.Fatalf("resume argv = %q, want [omp --resume %s]", got, file)
	}
	if got := f.trace("tab.create"); len(got) != 1 {
		t.Fatalf("resume opened %d tabs, want one replacement tab: %v", len(got), got)
	}
	got := replacement.got("session.send")
	if len(got) != 1 || !strings.Contains(string(got[0]), resumePrompt) {
		t.Fatalf("the resumed message did not reach the replacement extension: %v", got)
	}
	if strings.Contains(strings.Join(f.trace("pane.send_input"), "\n"), resumePrompt) {
		t.Fatal("the resumed message was typed into the terminal instead of sent to the extension")
	}
	if got := len(initial.got("session.send")); got != 1 {
		t.Fatalf("the exited extension received another message; sends = %d", got)
	}
}

// A terminal-only omp remains readable through herdr, but the node never guesses a send into its prompt.
func TestOmpEndToEndRefusesSendWithoutExtension(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", "")
	n, f, _, _ := piHerdrNode(t)
	cwd := t.TempDir()
	seedAgents(t, n, f, ompPaneAgent("w1:p10", cwd, ""))
	id := otherSession(host.Agent{Agent: "omp", PaneID: "w1:p10"})
	s := onlyRow(t, listSessions(t, n), id)
	if s.ReadOnlyReason != piFamilyNoExtension(ompAgent) || s.PaneID != "w1:p10" {
		t.Fatalf("terminal-only omp is not read-only: %+v", s)
	}
	f.reset()
	_, err := request(t, n, "session.send", map[string]any{"session_id": id, "text": "must not be typed", "client_msg_id": "omp-e2e-no-extension"})
	if !isRefusal(err, "omp_no_extension") {
		t.Fatalf("send without the extension: %v", err)
	}
	if got := f.trace("tab.create", "pane.send_input", "agent.prompt"); len(got) != 0 {
		t.Fatalf("a refused send asked herdr to run or type something: %v", got)
	}
}

// A scanned offline row is not enough to resume once its file has disappeared; in particular, no replacement tab opens.
func TestOmpEndToEndRefusesResumeWhoseFileDisappeared(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", "")
	n, f, home, _ := piHerdrNode(t)
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	cwd := t.TempDir()
	const id = "01d10000-0000-7000-8000-000000000002"
	file := writeOmpSession(t, ompSessionsDir(home), filepath.Base(cwd), id, "deleted before resume", cwd, time.Now())
	onlyRow(t, listSessions(t, n), id) // scan and cache the row before the file goes away
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	f.reset()
	_, err := request(t, n, "session.send", map[string]any{"session_id": id, "text": "cannot resume", "client_msg_id": "omp-e2e-missing-file"})
	if !isRefusal(err, "omp_resume_unknown") {
		t.Fatalf("send with a missing session file: %v", err)
	}
	if got := f.trace("tab.create"); len(got) != 0 {
		t.Fatalf("a missing session file opened a tab: %v", got)
	}
}

// An existing, valid session file is still untrusted when the cached stopped row names it outside omp's sessions root.
// The real send route must refuse it before opening a replacement tab, not merely reject it in the path helper.
func TestOmpEndToEndRefusesResumeOutsideAgentSessions(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", "")
	n, f, home, _ := piHerdrNode(t)
	cwd := t.TempDir()
	const id = "01d10000-0000-7000-8000-000000000003"
	outside := writeOmpSession(t, t.TempDir(), "-w-outside", id, "outside the trusted root", cwd, time.Now())
	d := n.omps()
	d.mu.Lock()
	d.dir = filepath.Join(ompAgentDir(home), "sessions")
	d.at = time.Now()
	d.order = []ompDiskSession{{ID: id, Path: outside, Cwd: cwd, Title: "outside the trusted root", Modified: time.Now()}}
	d.mu.Unlock()

	// If the path guard were bypassed, the fixture could open this tab; the bounded context keeps that mutant finite.
	f.reply("workspace.list", map[string]any{"workspaces": []any{map[string]any{"workspace_id": "w1", "label": filepath.Base(cwd)}}})
	f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w1:t11"}, "root_pane": map[string]any{"pane_id": "w1:p11"}})
	f.reset()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := requestCtx(t, ctx, n, "session.send", map[string]any{"session_id": id, "text": "must not run outside the trusted root", "client_msg_id": "omp-e2e-outside-file"})
	if !isRefusal(err, "omp_resume_unknown") {
		t.Fatalf("send with an existing untrusted session file: %v", err)
	}
	if got := f.trace("tab.create"); len(got) != 0 {
		t.Fatalf("an existing untrusted session file opened a tab: %v", got)
	}
}
