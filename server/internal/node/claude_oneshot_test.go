package node

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/identity"
)

// writeClaudeRun writes a Claude transcript in dir whose lines carry these entrypoints, one exchange each.
func writeClaudeRun(t *testing.T, dir, id string, age time.Duration, entrypoints ...string) string {
	t.Helper()
	var b strings.Builder
	for i, ep := range entrypoints {
		fmt.Fprintf(&b, `{"type":"user","uuid":"u%d","timestamp":"2026-11-05T07:31:12Z","cwd":"/src/app","entrypoint":%q,"message":{"role":"user","content":[{"type":"text","text":"summarize the diff"}]}}`+"\n", i, ep)
		fmt.Fprintf(&b, `{"type":"assistant","uuid":"a%d","timestamp":"2026-11-05T07:31:20Z","entrypoint":%q,"message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`+"\n", i, ep)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(p, []byte(b.String()+strings.Repeat(" ", 4096)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-age)
	_ = os.Chtimes(p, at, at)
	return p
}

func listedIDs(n *Node) map[string]bool {
	got := map[string]bool{}
	for _, s := range n.scanOffline() {
		got[s.SessionID] = true
	}
	return got
}

// A program running Claude Code's command line once, from its first entrypoint to its last (`claude -p`, sdk-cli), is no
// session of the user's: the offline list leaves it out (#3828). Listed as before: a session the user had in a terminal
// as well (either way round), the Agent SDKs' (an interactive host's, sdk-ts), Persona's own (started under its state
// directory: this or another manager's assistant, a health space, a guest; one the node keeps state for; a fork; a kept
// one), and a run whose last entrypoint is not within the tail read.
func TestOfflineListLeavesOneShotRunsOut(t *testing.T) {
	t.Setenv("PERSONA_HOME", "")
	n, home := testNode(t)
	n.metas, n.bgScans = map[string]quickMeta{}, map[string]*bgScan{}
	const (
		oneShot   = "5d500000-0000-4000-8000-000000000001"
		resumed   = "5d500000-0000-4000-8000-000000000002" // started by `claude -p`, then resumed in a terminal
		cli       = "5d500000-0000-4000-8000-000000000003"
		sdkHost   = "5d500000-0000-4000-8000-000000000004" // an interactive host on the TypeScript SDK (Paseo)
		kept      = "5d500000-0000-4000-8000-000000000005"
		fork      = "5d500000-0000-4000-8000-000000000006"
		assistant = "5d500000-0000-4000-8000-000000000007" // this manager's assistant
		manager   = "5d500000-0000-4000-8000-000000000008" // another manager's assistant
		health    = "5d500000-0000-4000-8000-000000000009" // another manager's health space
		guest     = "5d500000-0000-4000-8000-00000000000a"
		driven    = "5d500000-0000-4000-8000-00000000000b" // the node keeps state for it
		hugeTail  = "5d500000-0000-4000-8000-00000000000c" // a one-shot run whose last line outgrows the tail read
		cliThenP  = "5d500000-0000-4000-8000-00000000000d" // the user's terminal session, then `claude -p -c`
		legacy    = "5d500000-0000-4000-8000-00000000000e" // under the older state directory's name
	)
	n.kept, n.forked = map[string]bool{kept: true}, map[string]bool{fork: true}
	n.state.Sessions[driven] = sessionState{Cwd: "/src/app"}
	projects := filepath.Join(home, ".claude", "projects")
	dir := filepath.Join(projects, "-src-app")
	under := func(root, sub string) string {
		return filepath.Join(projects, nonAlnum.ReplaceAllString(filepath.Join(root, sub), "-"))
	}
	writeClaudeRun(t, dir, oneShot, time.Minute, "sdk-cli")
	writeClaudeRun(t, dir, resumed, 2*time.Minute, "sdk-cli", "cli")
	writeClaudeRun(t, dir, cli, 3*time.Minute, "cli")
	writeClaudeRun(t, dir, sdkHost, 4*time.Minute, "sdk-ts")
	writeClaudeRun(t, dir, kept, 5*time.Minute, "sdk-cli")
	writeClaudeRun(t, dir, fork, 6*time.Minute, "sdk-cli")
	writeClaudeRun(t, filepath.Join(projects, n.assistantProject()), assistant, 7*time.Minute, "sdk-cli")
	writeClaudeRun(t, under(identity.DataDir(home), "assistant-m-1a2b3c"), manager, 8*time.Minute, "sdk-cli")
	writeClaudeRun(t, under(identity.DataDir(home), "health-m-1a2b3c"), health, 9*time.Minute, "sdk-cli")
	writeClaudeRun(t, under(identity.DataDir(home), "guest"), guest, 10*time.Minute, "sdk-cli")
	writeClaudeRun(t, dir, driven, 11*time.Minute, "sdk-cli")
	big := writeClaudeRun(t, dir, hugeTail, 12*time.Minute, "sdk-cli")
	f, _ := os.OpenFile(big, os.O_APPEND|os.O_WRONLY, 0o600)
	fmt.Fprintf(f, `{"type":"user","uuid":"r1","message":{"role":"user","content":[{"type":"tool_result","content":%q}]}}`+"\n", strings.Repeat("x", 2*oneShotTail))
	f.Close()
	_ = os.Chtimes(big, time.Now().Add(-12*time.Minute), time.Now().Add(-12*time.Minute))
	writeClaudeRun(t, dir, cliThenP, 13*time.Minute, "cli", "sdk-cli")
	writeClaudeRun(t, under(filepath.Join(home, compat.StateDir), "guest"), legacy, 14*time.Minute, "sdk-cli")

	// A program's long prompt pushes its first entrypoint beyond the first tail's worth of bytes.
	const longPrompt = "5d500000-0000-4000-8000-00000000000f"
	lp := filepath.Join(dir, longPrompt+".jsonl")
	os.WriteFile(lp, []byte(fmt.Sprintf(`{"type":"user","uuid":"u0","timestamp":"2026-11-05T07:31:12Z","message":{"role":"user","content":[{"type":"text","text":%q}]},"cwd":"/src/app","entrypoint":"sdk-cli"}`+"\n", strings.Repeat("review this ", 2000))+
		`{"type":"assistant","uuid":"a0","timestamp":"2026-11-05T07:31:20Z","entrypoint":"sdk-cli","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`+"\n"), 0o600)
	_ = os.Chtimes(lp, time.Now().Add(-15*time.Minute), time.Now().Add(-15*time.Minute))

	got := listedIDs(n)
	for id, want := range map[string]bool{oneShot: false, resumed: true, cli: true, sdkHost: true, kept: true, fork: true,
		assistant: true, manager: true, health: true, guest: true, driven: true, hugeTail: true, cliThenP: true, legacy: true, longPrompt: false} {
		if got[id] != want {
			t.Errorf("%s listed=%v, want %v", id, got[id], want)
		}
	}

	// The user went on with the one-shot run in a terminal: it is listed from the next scan on.
	p := filepath.Join(dir, oneShot+".jsonl")
	f, _ = os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	fmt.Fprintf(f, `{"type":"user","uuid":"u9","timestamp":"2026-11-05T08:00:00Z","cwd":"/src/app","entrypoint":"cli","message":{"role":"user","content":[{"type":"text","text":"go on"}]}}`+"\n")
	f.Close()
	offlineCache.Lock()
	offlineCache.at = time.Time{}
	offlineCache.Unlock()
	if !listedIDs(n)[oneShot] {
		t.Fatal("a run the user went on with in a terminal is still left out")
	}
}

// While the node still loads which sessions it keeps state for, no one-shot run is left out: one it drove might be.
func TestOneShotRunsListedWhileStateLoads(t *testing.T) {
	n, home := testNode(t)
	n.metas, n.bgScans = map[string]quickMeta{}, map[string]*bgScan{}
	const id = "5d500000-0000-4000-8000-0000000000f1"
	writeClaudeRun(t, filepath.Join(home, ".claude", "projects", "-src-app"), id, time.Minute, "sdk-cli")
	n.state.beginLoad()
	if !listedIDs(n)[id] {
		t.Fatal("left out while the state loads")
	}
}

// Persona's state directory where PERSONA_HOME puts it, and reached through a link: what is started under it is listed.
func TestOneShotRunsUnderAMovedOrLinkedStateDirectoryAreListed(t *testing.T) {
	n, home := testNode(t)
	n.metas, n.bgScans = map[string]quickMeta{}, map[string]*bgScan{}
	custom := filepath.Join(home, "elsewhere", "persona-state")
	t.Setenv("PERSONA_HOME", custom)
	real := filepath.Join(home, "real-persona")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(home, ".persona")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	projects := filepath.Join(home, ".claude", "projects")
	const moved, linked = "5d500000-0000-4000-8000-0000000000f2", "5d500000-0000-4000-8000-0000000000f3"
	writeClaudeRun(t, filepath.Join(projects, nonAlnum.ReplaceAllString(filepath.Join(custom, "guest"), "-")), moved, time.Minute, "sdk-cli")
	// Claude Code names a project after the resolved directory: ~/.persona/assistant-m-x is real-persona/assistant-m-x.
	writeClaudeRun(t, filepath.Join(projects, nonAlnum.ReplaceAllString(filepath.Join(realPath(real), "assistant-m-x"), "-")), linked, 2*time.Minute, "sdk-cli")
	got := listedIDs(n)
	if !got[moved] || !got[linked] {
		t.Fatalf("PERSONA_HOME listed=%v, linked listed=%v", got[moved], got[linked])
	}
}

// One-shot runs never count toward the 80 the offline list shows, and are told from their first and last bytes without
// being parsed: a script running `claude -p` all day does not push the user's older sessions out.
func TestOneShotRunsDoNotTakeTheCap(t *testing.T) {
	n, home := testNode(t)
	n.metas, n.bgScans = map[string]quickMeta{}, map[string]*bgScan{}
	dir := filepath.Join(home, ".claude", "projects", "-src-app")
	for i := 0; i < 85; i++ {
		writeClaudeRun(t, dir, fmt.Sprintf("5d5ff000-0000-4000-8000-%012d", i), time.Duration(i+1)*time.Minute, "sdk-cli")
	}
	const older = "aaaaaaaa-0000-4000-8000-0000000000aa"
	writeClaudeRun(t, dir, older, 3*time.Hour, "cli")
	rows := n.scanOffline()
	if len(rows) != 1 || rows[0].SessionID != older {
		t.Fatalf("rows = %d, want only the user's older session", len(rows))
	}
	if len(n.metas) > 1 {
		t.Fatalf("parsed %d transcripts, want only the listed one", len(n.metas))
	}
}
