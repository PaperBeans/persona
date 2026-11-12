package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/orcart"
	"github.com/nwverse/persona/server/internal/orcart/orcarttest"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestOrcaCreateTerminalInRegisteredWorktree(t *testing.T) {
	n, _, o, _, home := orcaNode(t)
	repoDir := filepath.Join(home, "my-project")
	mkdir(t, repoDir)

	o.SetWorktrees(orcarttest.Worktree("wt-proj", repoDir, "refs/heads/main"))

	res, err := request(t, n, "session.start", map[string]any{
		"session_id": "0e000000-0000-4000-8000-000000000001",
		"cwd":        repoDir,
		"async":      true,
	})
	if err != nil {
		t.Fatalf("session.start failed: %v", err)
	}
	session, ok := res["session"].(map[string]any)
	if !ok {
		t.Fatalf("expected session map in result, got %v", res)
	}
	if sid, _ := session["session_id"].(string); sid != "0e000000-0000-4000-8000-000000000001" {
		t.Errorf("expected session ID 0e000000-0000-4000-8000-000000000001, got %v", session["session_id"])
	}

	// Verify terminal.create was called in fake Orca
	deadline := time.Now().Add(5 * time.Second)
	for len(o.Trace("terminal.create")) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for terminal.create; calls: %v", o.Trace())
		}
		time.Sleep(20 * time.Millisecond)
	}

	createCalls := o.Trace("terminal.create")
	if len(createCalls) == 0 {
		t.Fatalf("expected terminal.create call, got none")
	}
	if !strings.Contains(createCalls[0], `"worktree":"id:wt-proj"`) {
		t.Errorf("expected terminal.create for wt-proj, got %s", createCalls[0])
	}
}

func TestOrcaCreateTerminalInSubdirectory(t *testing.T) {
	n, _, o, _, home := orcaNode(t)
	repoDir := filepath.Join(home, "monorepo")
	subDir := filepath.Join(repoDir, "services", "api")
	mkdir(t, subDir)

	o.SetWorktrees(orcarttest.Worktree("wt-mono", repoDir, "refs/heads/main"))

	res, err := request(t, n, "session.start", map[string]any{
		"session_id": "0e000000-0000-4000-8000-000000000002",
		"cwd":        subDir,
		"async":      true,
	})
	if err != nil {
		t.Fatalf("session.start in subDir failed: %v", err)
	}
	session, ok := res["session"].(map[string]any)
	if !ok {
		t.Fatalf("expected session map in result, got %v", res)
	}
	if cwd, _ := session["cwd"].(string); cwd != subDir {
		t.Errorf("expected Cwd %s, got %v", subDir, session["cwd"])
	}

	deadline := time.Now().Add(5 * time.Second)
	for len(o.Trace("terminal.create")) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for terminal.create; calls: %v", o.Trace())
		}
		time.Sleep(20 * time.Millisecond)
	}

	createCalls := o.Trace("terminal.create")
	if !strings.Contains(createCalls[0], `"worktree":"id:wt-mono"`) {
		t.Errorf("expected terminal.create for wt-mono, got %s", createCalls[0])
	}

	// Verify the launched shell command executed cd -- subDir
	for len(o.Writes()) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for terminal write; writes: %v", o.Writes())
		}
		time.Sleep(20 * time.Millisecond)
	}
	w := o.Writes()[0]
	if !strings.Contains(w.Text, "cd -- "+shellQuote(subDir)) {
		t.Errorf("expected cd to %s, got %s", subDir, w.Text)
	}
}

func TestOrcaFallbackToAvailableWorktree(t *testing.T) {
	n, _, o, _, home := orcaNode(t)
	otherDir := filepath.Join(home, "unregistered")
	mkdir(t, otherDir)

	// Orca has a worktree open for another project
	wtDir := filepath.Join(home, "open-project")
	mkdir(t, wtDir)
	o.SetWorktrees(orcarttest.Worktree("wt-open", wtDir, "refs/heads/main"))

	launch, err := hostCap[host.Launcher](n)
	if err != nil {
		t.Fatalf("expected host.Launcher, got error: %v", err)
	}

	created, err := launch.CreateSpace(context.Background(), otherDir, "test")
	if err != nil {
		t.Fatalf("expected fallback to wt-open, got err: %v", err)
	}
	if created.SpaceID != "wt-open" {
		t.Errorf("expected container SpaceID wt-open, got %s", created.SpaceID)
	}
}

func TestOrcaRefusesWhenNoWorktrees(t *testing.T) {
	n, _, o, _, home := orcaNode(t)
	unregDir := filepath.Join(home, "unregistered")
	mkdir(t, unregDir)

	// No worktrees in Orca at all
	o.SetWorktrees()

	launch, err := hostCap[host.Launcher](n)
	if err != nil {
		t.Fatalf("expected host.Launcher, got error: %v", err)
	}

	_, err = launch.CreateSpace(context.Background(), unregDir, "test")
	if err == nil {
		t.Fatalf("expected refusal when no worktrees in Orca, got nil")
	}
	if !strings.Contains(err.Error(), "orca_not_registered") && !strings.Contains(err.Error(), "没有任何打开的项目") {
		t.Errorf("expected orca_not_registered refusal, got %v", err)
	}
}

// A Claude session that is not running resumes in Orca as on herdr (#5548): a terminal opened in the Orca worktree
// holding its directory, cd there, its launcher with --resume. One still open elsewhere (a live claude writes it) is
// refused, nothing started: two writers would corrupt the transcript.
func TestOrcaResumesAnOfflineSession(t *testing.T) {
	n, _, o, _, home := orcaNode(t)
	n.claudeStart, n.claudeReady = 200*time.Millisecond, 200*time.Millisecond // the fake terminal never runs claude
	projDir := filepath.Join(home, "my-project")
	mkdir(t, projDir)
	o.SetWorktrees(orcarttest.Worktree("repo1::"+projDir, projDir, "refs/heads/main"))
	const id = "0e000000-0000-4000-8000-000000000003"
	writeTranscript(t, home, projDir, id, time.Now().Add(-time.Hour), `{"type":"custom-title","customTitle":"x"}`)
	starts := orcaTypedStarts(o)

	// The start then waits for claude, which the fake never runs: a timeout, not a refusal.
	_, err := request(t, n, "session.start", map[string]any{"session_id": id, "resume_session_id": id, "cwd": projDir})
	if isRefusal(err, "orca_resume") || isRefusal(err, openElsewhere) {
		t.Fatalf("resume refused: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(strings.Join(starts(), "\n"), "--resume "+id) {
		if time.Now().After(deadline) {
			t.Fatalf("no resume typed; calls: %v", o.Trace())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if c := o.Trace("terminal.create"); len(c) != 1 || !strings.Contains(c[0], `"worktree":"id:repo1::`+projDir+`"`) {
		t.Errorf("terminal.create %v", c)
	}
	if s := strings.Join(o.Trace("terminal.send"), "\n"); !strings.Contains(s, "cd -- ") || !strings.Contains(s, projDir) {
		t.Errorf("not started in the session's directory: %s", s)
	}

	const open = "0e000000-0000-4000-8000-000000000004"
	writeClaudeSession(t, home, os.Getpid(), open, "cli") // this test's own pid: a live claude has it open
	o.Reset()
	_, err = request(t, n, "session.start", map[string]any{"session_id": open, "resume_session_id": open, "cwd": projDir})
	if !isRefusal(err, openElsewhere) {
		t.Fatalf("resume of a session open elsewhere: %v", err)
	}
	if c := o.Trace("terminal.create", "terminal.send"); len(c) != 0 {
		t.Errorf("Orca was asked %v", c)
	}

	// Only Claude sessions: a TRAEX resume is refused, and so is one an Orca native chat owns (its runtime's).
	_, err = request(t, n, "session.start", map[string]any{"session_id": id, "resume_session_id": id, "cwd": projDir, "launcher": traexAgent})
	if !isRefusal(err, "orca_resume") {
		t.Errorf("TRAEX resume: %v", err)
	}
	if n.runtimes == nil {
		n.runtimes = newRuntimeRegistry()
	}
	n.runtimes.mu.Lock()
	n.runtimes.known[id] = knownSession{runtime: "orca", label: "Orca", at: time.Now()}
	n.runtimes.mu.Unlock()
	_, err = request(t, n, "session.start", map[string]any{"session_id": id, "resume_session_id": id, "cwd": projDir})
	if !isRefusal(err, "runtime_session") {
		t.Errorf("resume of a native chat's session: %v", err)
	}
	if c := o.Trace("terminal.create", "terminal.send"); len(c) != 0 {
		t.Errorf("Orca was asked %v", c)
	}
}

// A directory whose name holds a control character is refused before Orca opens a terminal: Run types `cd -- <cwd>`
// into the shell's line editor, which acts on a tab (completion) or a line break even inside the quotes (#2867).
func TestOrcaRefusesADirectoryItCannotType(t *testing.T) {
	fake := orcarttest.New(t)
	root := t.TempDir()
	fake.SetWorktrees(orcarttest.Worktree("wt-1", root, "refs/heads/main"))
	h := newOrcaHost(&orcart.Client{MetaPath: fake.MetaPath})
	for _, name := range []string{"a\tb", "a\nb", "a\x1bb", "a\x7fb"} {
		dir := mkdir(t, filepath.Join(root, name))
		if created, err := h.CreateSpace(context.Background(), dir, "tab1"); created != nil || !errors.Is(err, host.ErrNotDelivered) || refusalCode(err) != proto.CodeDirectoryUntypable {
			t.Errorf("CreateSpace(%q) = %v, %v", name, created, err)
		}
		if created, err := h.CreateSlot(context.Background(), "wt-1", dir, "tab1"); created != nil || !errors.Is(err, host.ErrNotDelivered) || refusalCode(err) != proto.CodeDirectoryUntypable {
			t.Errorf("CreateSlot(%q) = %v, %v", name, created, err)
		}
	}
	if c := fake.Trace("terminal.create", "terminal.send"); len(c) != 0 {
		t.Errorf("Orca was asked %v", c)
	}
	// A name that only looks unusual still opens a terminal and is typed quoted.
	dir := mkdir(t, filepath.Join(root, `it's a \dir`))
	created, err := h.CreateSpace(context.Background(), dir, "tab1")
	if err != nil {
		t.Fatalf("CreateSpace: %v", err)
	}
	if err := h.Run(context.Background(), created.Pane, "echo hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if w := fake.Writes(); len(w) != 1 || !strings.HasPrefix(w[0].Text, "cd -- "+shellQuote(dir)+" && echo hello") {
		t.Errorf("typed %v", w)
	}
}

func TestOrcaRunSingleUseGrant(t *testing.T) {
	fake := orcarttest.New(t)
	fake.SetWorktrees(orcarttest.Worktree("wt-1", "/tmp/repo", "refs/heads/main"))
	c := &orcart.Client{MetaPath: fake.MetaPath}
	h := newOrcaHost(c)

	created, err := h.CreateSpace(context.Background(), "/tmp/repo", "tab1")
	if err != nil {
		t.Fatalf("CreateSpace failed: %v", err)
	}

	// First Run should succeed
	if err := h.Run(context.Background(), created.Pane, "echo hello"); err != nil {
		t.Fatalf("first Run failed: %v", err)
	}

	// Second Run should be rejected (token already consumed)
	if err := h.Run(context.Background(), created.Pane, "echo twice"); err == nil {
		t.Fatalf("second Run expected error, got nil")
	}
}

// A new Orca tab has no screen for its first moments (terminal.read answers screen-unavailable): the start waits for
// the shell's prompt instead of typing into a tty whose shell is not there yet, and then types the short line that
// sources its start file, so a shell slow to take the terminal cannot cut the command at macOS's 1024-byte canonical
// line limit and leave it unrun at the prompt (#5450).
func TestOrcaStartWaitsForTheScreenAndTypesAShortLine(t *testing.T) {
	withPaneShell(t, "/bin/bash")
	n, _, o, _, home := orcaNode(t)
	watchShell(n)
	n.shellSettle, n.shellQuiet = 10*time.Second, 100*time.Millisecond
	repoDir := mkdir(t, filepath.Join(home, "proj"))
	o.SetWorktrees(orcarttest.Worktree("wt-proj", repoDir, "refs/heads/main"))
	var drawn atomic.Bool
	var reads atomic.Int32
	read := make(chan struct{}, 1)
	o.Handle("terminal.read", func(p map[string]any) any {
		reads.Add(1)
		select {
		case read <- struct{}{}:
		default:
		}
		if !drawn.Load() {
			return map[string]any{"terminal": map[string]any{"handle": p["terminal"], "tail": []string{}, "source": "screen-unavailable"}}
		}
		return map[string]any{"terminal": map[string]any{"handle": p["terminal"], "tail": []string{"bash-5.2$ "}, "source": "screen"}}
	})
	if _, err := request(t, n, "session.start", map[string]any{"session_id": startID, "cwd": repoDir, "async": true}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the new tab", func() bool { return len(o.Trace("terminal.create")) > 0 })
	for reads.Load() < 3 { // no screen across more than one settle poll
		if w := o.Writes(); len(w) != 0 {
			t.Fatalf("typed before the tab had a screen: %q", w[0].Text)
		}
		select {
		case <-read:
		case <-time.After(5 * time.Second):
			t.Fatalf("the tab was not read again: %v", o.Trace())
		}
	}
	drawn.Store(true)
	waitFor(t, "the start line", func() bool { return len(o.Writes()) > 0 })
	typed := o.Writes()[0].Text
	want := "cd -- " + shellQuote(repoDir) + " && . "
	if !strings.HasPrefix(typed, want) || !strings.HasSuffix(typed, " && true "+shellQuote(startID)+"\r") || len(typed) >= 1024 {
		t.Errorf("typed %d bytes, not the start file's line: %q", len(typed), typed)
	}
}

// orcaTypedStarts has o's terminal.send read the start file an Orca start's typed line (`cd -- <dir> && . <file>…`)
// sources, while it is still there (typedstart.go), and returns the commands read so far.
func orcaTypedStarts(o *orcarttest.Fake) func() []string {
	var mu sync.Mutex
	var cmds []string
	o.Handle("terminal.send", func(p map[string]any) any {
		text, _ := p["text"].(string)
		_, line, _ := strings.Cut(strings.TrimSuffix(text, "\r"), " && ")
		if cmd, ok := startFileCommand(line); ok {
			mu.Lock()
			cmds = append(cmds, cmd)
			mu.Unlock()
		}
		return map[string]any{"send": map[string]any{"handle": p["terminal"], "accepted": true, "bytesWritten": len(text)}}
	})
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(cmds)
	}
}
