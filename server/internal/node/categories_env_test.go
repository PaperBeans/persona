package node

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/rpc"
)

// An assistant process learns its conversation's information categories (#13246): the track's default categories from
// assistant.prepare go into PERSONA_CATEGORIES next to PERSONA_SPACE and PERSONA_TRACK_KIND (empty string for general,
// comma-separated sorted names for sensitive); per-session categories from assistant.send override the directory's
// defaults, and categories_unknown (or an older manager or invalid token) leaves PERSONA_CATEGORIES unset so a command
// treats it as unknown (sensitive). An Agent started from the assistant's shell never takes it over.
func TestAssistantProcessKnowsItsCategories(t *testing.T) {
	t.Setenv("PERSONA_SKILLS_DIR", t.TempDir())
	t.Setenv("PERSONA_PLUGIN_SKILLS_DIR", t.TempDir())
	n, _ := testNode(t)
	n.dir, n.bin, n.logger = filepath.Join(n.home, ".persona"), "/opt/example/personad", log.New(io.Discard, "", 0)
	if err := os.MkdirAll(n.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	prepare := func(dir string, cats any) string {
		t.Helper()
		p := map[string]any{"dir": dir, "claude_md": "# Example"}
		if cats != nil {
			p["categories"] = cats
		}
		params, _ := json.Marshal(p)
		out, err := n.handle(t.Context(), "assistant.prepare", params)
		if err != nil {
			t.Fatal(err)
		}
		return out.(map[string]any)["cwd"].(string)
	}
	envOf := func(cwd, session string) []string {
		t.Helper()
		env, release := n.assistantLarkEnv(cwd, session)
		release()
		return env
	}
	hasCategoriesEnv := func(env []string) (string, bool) {
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, categoriesEnv+"="); ok {
				return v, true
			}
		}
		return "", false
	}

	// Main directory prepared with empty list -> known general (PERSONA_CATEGORIES=).
	own := prepare(".persona/assistant", []string{})
	if val, ok := hasCategoriesEnv(envOf(own, "")); !ok || val != "" {
		t.Fatalf("general dir: ok=%v val=%q", ok, val)
	}
	// Health directory prepared with health -> PERSONA_CATEGORIES=health.
	health := prepare(".persona/health", []string{"health"})
	if val, ok := hasCategoriesEnv(envOf(health, "")); !ok || val != "health" {
		t.Fatalf("health dir: ok=%v val=%q", ok, val)
	}
	// Multiple unsorted/duplicate categories are normalized (sorted, deduplicated).
	multi := prepare(".persona/tracks/qa-mix", []string{"finance", "emotion", "finance", "legal_id"})
	if val, ok := hasCategoriesEnv(envOf(multi, "")); !ok || val != "emotion,finance,legal_id" {
		t.Fatalf("multi dir: ok=%v val=%q", ok, val)
	}

	// Older manager (no categories field) or invalid category token -> PERSONA_CATEGORIES absent (unknown/sensitive).
	for _, bad := range []any{nil, []string{"Bad Cat"}, []string{"health", "qa\nX=1"}, []string{""}} {
		cwd := prepare(".persona/assistant", bad)
		if val, ok := hasCategoriesEnv(envOf(cwd, "")); ok {
			t.Errorf("categories %#v: got %s=%q, want absent", bad, categoriesEnv, val)
		}
	}

	// Per-session categories override the prepared directory's defaults, and categories_unknown clears them.
	own = prepare(".persona/assistant", []string{})
	tagged := []string{"finance", "emotion"}
	n.setSessionCategories("qa-conv-1", &tagged, false)
	if val, ok := hasCategoriesEnv(envOf(own, "qa-conv-1")); !ok || val != "emotion,finance" {
		t.Fatalf("per-session tagged: ok=%v val=%q, want emotion,finance", ok, val)
	}
	// Unrelated session in the same directory still uses the directory's default.
	if val, ok := hasCategoriesEnv(envOf(own, "qa-conv-other")); !ok || val != "" {
		t.Fatalf("other session in general dir: ok=%v val=%q, want empty", ok, val)
	}
	// Older manager sending neither categories nor categories_unknown leaves existing session state alone.
	n.setSessionCategories("qa-conv-1", nil, false)
	if val, ok := hasCategoriesEnv(envOf(own, "qa-conv-1")); !ok || val != "emotion,finance" {
		t.Fatalf("older manager no-op: ok=%v val=%q, want emotion,finance", ok, val)
	}
	// categories_unknown (e.g. turn waiting to be tagged) removes PERSONA_CATEGORIES even though dir is general.
	n.setSessionCategories("qa-conv-1", nil, true)
	if val, ok := hasCategoriesEnv(envOf(own, "qa-conv-1")); ok {
		t.Fatalf("categories_unknown: got %s=%q, want absent", categoriesEnv, val)
	}
	// Invalid token on session also fails closed (absent).
	invalid := []string{"finance", "BAD"}
	n.setSessionCategories("qa-conv-1", &invalid, false)
	if val, ok := hasCategoriesEnv(envOf(own, "qa-conv-1")); ok {
		t.Fatalf("invalid session token: got %s=%q, want absent", categoriesEnv, val)
	}
	// Known general again restores PERSONA_CATEGORIES=.
	general := []string{}
	n.setSessionCategories("qa-conv-1", &general, false)
	if val, ok := hasCategoriesEnv(envOf(own, "qa-conv-1")); !ok || val != "" {
		t.Fatalf("restored general: ok=%v val=%q, want empty", ok, val)
	}

	if !slices.Contains(assistantOnlyEnvNames(), categoriesEnv) {
		t.Error("an Agent would take PERSONA_CATEGORIES over from the assistant's pane")
	}

	// Inherited PERSONA_CATEGORIES in the node's own environment is stripped when starting a process or gate script
	// whose categories are unknown.
	t.Setenv(categoriesEnv, "")
	n.setSessionCategories("qa-conv-1", nil, true)
	stripped := withoutEnv(os.Environ(), []string{categoriesEnv})
	if val, ok := hasCategoriesEnv(append(stripped, envOf(own, "qa-conv-1")...)); ok {
		t.Fatalf("inherited %s leaked when unknown: %q", categoriesEnv, val)
	}

	// session.send for an assistant session also updates its recorded categories before invoking assistantSend.
	n.state.Sessions["qa-conv-rpc"] = sessionState{Cwd: own}
	sendParams, _ := json.Marshal(map[string]any{
		"session_id": "qa-conv-rpc",
		"text":       "hello",
		"launcher":   "bad launcher!",
		"categories": []string{"finance", "emotion"},
	})
	_, _ = n.handle(t.Context(), "session.send", sendParams)
	if val, ok := hasCategoriesEnv(envOf(own, "qa-conv-rpc")); !ok || val != "emotion,finance" {
		t.Fatalf("session.send categories: ok=%v val=%q, want emotion,finance", ok, val)
	}
	unknownParams, _ := json.Marshal(map[string]any{
		"session_id":         "qa-conv-rpc",
		"text":               "hello",
		"launcher":           "bad launcher!",
		"categories_unknown": true,
	})
	_, _ = n.handle(t.Context(), "session.send", unknownParams)
	if val, ok := hasCategoriesEnv(envOf(own, "qa-conv-rpc")); ok {
		t.Fatalf("session.send categories_unknown: got %s=%q, want absent", categoriesEnv, val)
	}
	// assistant.send updates recorded categories before invoking assistantSend as well.
	asstSendParams, _ := json.Marshal(map[string]any{
		"session_id": "qa-conv-rpc",
		"cwd":        own,
		"text":       "hello",
		"launcher":   "bad launcher!",
		"categories": []string{"legal_id"},
	})
	_, _ = n.handle(t.Context(), "assistant.send", asstSendParams)
	if val, ok := hasCategoriesEnv(envOf(own, "qa-conv-rpc")); !ok || val != "legal_id" {
		t.Fatalf("assistant.send categories: ok=%v val=%q, want legal_id", ok, val)
	}
}

func envCategories(env []string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		if v, ok := strings.CutPrefix(env[i], categoriesEnv+"="); ok {
			return v, true
		}
	}
	return "", false
}

func prepareCategoriesDir(t *testing.T, n *Node, dir string, cats any) string {
	t.Helper()
	p := map[string]any{"dir": dir, "claude_md": "# Example"}
	if cats != nil {
		p["categories"] = cats
	}
	params, _ := json.Marshal(p)
	out, err := n.handle(t.Context(), "assistant.prepare", params)
	if err != nil {
		t.Fatal(err)
	}
	return out.(map[string]any)["cwd"].(string)
}

func assertConversationBusy(t *testing.T, err error) {
	t.Helper()
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != assistantwire.CodeConversationBusy {
		t.Fatalf("err = %v, want rpc.Error code %q", err, assistantwire.CodeConversationBusy)
	}
}

// A live headless Claude process records the PERSONA_CATEGORIES state it started with: idle sends restart it when
// processCategories changes (e.g. categories_unknown while a turn waits to be tagged, or newly accumulated sensitive
// categories), matching sends reuse it, and active processes refuse stricter/unknown transitions with CodeConversationBusy
// without dropping active work or delivering onto stale PERSONA_CATEGORIES.
func TestAssistantProcCategoriesStaleRestartsAndRefusesBusy(t *testing.T) {
	t.Setenv("PERSONA_SKILLS_DIR", t.TempDir())
	t.Setenv("PERSONA_PLUGIN_SKILLS_DIR", t.TempDir())
	f := cliTest(t)
	ctx := context.Background()
	cwd := prepareCategoriesDir(t, f.n, ".persona/assistant", []string{})

	id, err := f.n.assistantSend(ctx, "", cwd, "claude", "turn 1", nil, "", "", cliExec())
	if err != nil {
		t.Fatal(err)
	}
	f.idle(t, id)
	p1 := f.proc(id)
	if val, ok := envCategories(p1.cmd.Env); !ok || val != "" {
		t.Fatalf("turn 1 env: ok=%v val=%q, want general empty string", ok, val)
	}

	// Same categories (general) reuses the live process without restarting.
	general := []string{}
	f.n.setSessionCategories(id, &general, false)
	if _, err := f.n.assistantSend(ctx, id, cwd, "claude", "turn 2", nil, "", "", cliExec()); err != nil {
		t.Fatal(err)
	}
	f.idle(t, id)
	if f.proc(id) != p1 {
		t.Fatal("unchanged categories needlessly restarted the live Claude process")
	}

	// categories_unknown (e.g. Turn 1 waiting to be tagged or store unreadable) restarts idle process without PERSONA_CATEGORIES.
	f.n.setSessionCategories(id, nil, true)
	if _, err := f.n.assistantSend(ctx, id, cwd, "claude", "turn 3", nil, "", "", cliExec()); err != nil {
		t.Fatal(err)
	}
	f.idle(t, id)
	p3 := f.proc(id)
	if p3 == p1 {
		t.Fatal("categories_unknown reused the live process that still had PERSONA_CATEGORIES=")
	}
	if val, ok := envCategories(p3.cmd.Env); ok {
		t.Fatalf("unknown turn 3 env: got %s=%q, want absent", categoriesEnv, val)
	}

	// Sensitive tags (emotion,finance) restart idle process with PERSONA_CATEGORIES=emotion,finance.
	sensitive := []string{"finance", "emotion"}
	f.n.setSessionCategories(id, &sensitive, false)
	if _, err := f.n.assistantSend(ctx, id, cwd, "claude", "turn 4", nil, "", "", cliExec()); err != nil {
		t.Fatal(err)
	}
	f.idle(t, id)
	p4 := f.proc(id)
	if p4 == p3 {
		t.Fatal("sensitive categories reused the live process without them")
	}
	if val, ok := envCategories(p4.cmd.Env); !ok || val != "emotion,finance" {
		t.Fatalf("sensitive turn 4 env: ok=%v val=%q, want emotion,finance", ok, val)
	}

	// Same sensitive categories reuse p4.
	if _, err := f.n.assistantSend(ctx, id, cwd, "claude", "turn 5", nil, "", "", cliExec()); err != nil {
		t.Fatal(err)
	}
	f.idle(t, id)
	if f.proc(id) != p4 {
		t.Fatal("unchanged sensitive categories needlessly restarted the live Claude process")
	}

	// Active process protection: a working process (turn, compaction, background task, queued input) refuses stricter
	// or unknown categories with CodeConversationBusy and keeps its work intact.
	for _, tc := range []struct {
		name          string
		hold, release func(*assistantProc)
	}{
		{name: "turn", hold: func(p *assistantProc) { p.busy = true }, release: func(p *assistantProc) { p.busy = false }},
		{name: "compaction", hold: func(p *assistantProc) { p.compacting = true }, release: func(p *assistantProc) { p.compacting = false }},
		{name: "background task", hold: func(p *assistantProc) { p.tasks = []string{"bg"} }, release: func(p *assistantProc) { p.tasks = nil }},
		{name: "queued input", hold: func(p *assistantProc) {
			p.written = []assistantInput{{queued: true, at: time.Now()}}
		}, release: func(p *assistantProc) { p.written = nil }},
	} {
		t.Run("active/"+tc.name, func(t *testing.T) {
			g := cliTest(t)
			gcwd := prepareCategoriesDir(t, g.n, ".persona/assistant", []string{})
			gid, err := g.n.assistantSend(ctx, "", gcwd, "claude", "start", nil, "", "", cliExec())
			if err != nil {
				t.Fatal(err)
			}
			g.idle(t, gid)
			gp := g.proc(gid)
			gp.mu.Lock()
			tc.hold(gp)
			gp.mu.Unlock()
			defer func() {
				gp.mu.Lock()
				tc.release(gp)
				gp.mu.Unlock()
			}()

			argvBefore, stdinBefore := g.file("cli-argv"), g.file("stdin.jsonl")
			// General -> unknown must refuse while active.
			g.n.setSessionCategories(gid, nil, true)
			_, err = g.n.assistantSend(ctx, gid, gcwd, "claude", "unknown while busy", nil, "", "", cliExec())
			assertConversationBusy(t, err)
			if g.proc(gid) != gp || g.file("cli-argv") != argvBefore || g.file("stdin.jsonl") != stdinBefore {
				t.Fatal("unknown categories reached or replaced an active general process")
			}

			// General -> sensitive must also refuse while active.
			cats := []string{"emotion", "finance"}
			g.n.setSessionCategories(gid, &cats, false)
			_, err = g.n.assistantSend(ctx, gid, gcwd, "claude", "sensitive while busy", nil, "", "", cliExec())
			assertConversationBusy(t, err)
			if g.proc(gid) != gp || g.file("cli-argv") != argvBefore || g.file("stdin.jsonl") != stdinBefore {
				t.Fatal("sensitive categories reached or replaced an active general process")
			}
		})
	}

	// An active process already running fail-closed (PERSONA_CATEGORIES absent) does not refuse a known-general follow-up;
	// once idle, the next send restarts it with PERSONA_CATEGORIES=.
	t.Run("active/fail-closed-allows-general", func(t *testing.T) {
		g := cliTest(t)
		gcwd := prepareCategoriesDir(t, g.n, ".persona/assistant", nil) // unknown at start
		gid, err := g.n.assistantSend(ctx, "", gcwd, "claude", "start unknown", nil, "", "", cliExec())
		if err != nil {
			t.Fatal(err)
		}
		g.idle(t, gid)
		gp := g.proc(gid)
		if _, ok := envCategories(gp.cmd.Env); ok {
			t.Fatal("initial process unexpectedly had PERSONA_CATEGORIES")
		}
		gp.mu.Lock()
		gp.busy = true
		gp.mu.Unlock()
		gen := []string{}
		g.n.setSessionCategories(gid, &gen, false)
		if _, err := g.n.assistantSend(ctx, gid, gcwd, "claude", "follow up while busy", nil, "", "", cliExec()); err != nil {
			t.Fatalf("fail-closed active process refused general follow-up: %v", err)
		}
		if g.proc(gid) != gp {
			t.Fatal("fail-closed active process was killed instead of keeping its active work")
		}
		g.idle(t, gid)
		if _, err := g.n.assistantSend(ctx, gid, gcwd, "claude", "idle turn", nil, "", "", cliExec()); err != nil {
			t.Fatal(err)
		}
		g.idle(t, gid)
		gp2 := g.proc(gid)
		if gp2 == gp {
			t.Fatal("idle fail-closed process did not restart once general categories became known")
		}
		if val, ok := envCategories(gp2.cmd.Env); !ok || val != "" {
			t.Fatalf("restarted env: ok=%v val=%q, want general empty string", ok, val)
		}
	})
}

// Interactive Claude Code (assistant-tui) keeps its process across turns and must likewise restart when idle or refuse
// when active if PERSONA_CATEGORIES changes.
func TestAssistantTUICategoriesStaleRestartsAndRefusesBusy(t *testing.T) {
	t.Setenv("PERSONA_SKILLS_DIR", t.TempDir())
	t.Setenv("PERSONA_PLUGIN_SKILLS_DIR", t.TempDir())
	resetTUICooldown(t)
	f := cliTest(t)
	n := f.n
	dir := t.TempDir()
	bin := filepath.Join(dir, "personad")
	script := "#!/bin/sh\nIFS= read -r spec\n" +
		`echo '{"type":"system","subtype":"persona_tui_ready"}'` + "\n" +
		`while IFS= read -r line; do
  case "$line" in
    *'"type":"control_request"'*)
      id=$(printf '%s' "$line" | sed -n 's/.*"request_id":"\([^"]*\)".*/\1/p')
      echo "{\"type\":\"control_response\",\"response\":{\"subtype\":\"success\",\"request_id\":\"$id\"}}" ;;
    *'"type":"user"'*)
      echo '{"type":"system","subtype":"init"}'
      echo '{"type":"result","subtype":"success","is_error":false,"result":"ok"}' ;;
  esac
done
`
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	n.bin = bin
	cwd := prepareCategoriesDir(t, n, ".persona/assistant", []string{})
	ctx := context.Background()
	waitIdle := func(id string) {
		t.Helper()
		waitFor(t, "tui idle", func() bool {
			n.mu.Lock()
			p := n.procs[id]
			n.mu.Unlock()
			if p == nil {
				return false
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			return !p.busy
		})
	}

	id, err := n.assistantSend(ctx, "", cwd, "claude", "turn 1", nil, "", "", modelExecution{Interactive: true})
	if err != nil {
		t.Fatal(err)
	}
	waitIdle(id)
	n.mu.Lock()
	p1 := n.procs[id]
	n.mu.Unlock()
	if !p1.tui {
		t.Fatal("expected interactive assistant-tui process")
	}
	if val, ok := envCategories(p1.cmd.Env); !ok || val != "" {
		t.Fatalf("tui turn 1 env: ok=%v val=%q, want general empty string", ok, val)
	}

	// Sensitive tags restart the idle assistant-tui process with the new PERSONA_CATEGORIES.
	cats := []string{"finance", "emotion"}
	n.setSessionCategories(id, &cats, false)
	if _, err := n.assistantSend(ctx, id, cwd, "claude", "turn 2", nil, "", "", modelExecution{Interactive: true}); err != nil {
		t.Fatal(err)
	}
	waitIdle(id)
	n.mu.Lock()
	p2 := n.procs[id]
	n.mu.Unlock()
	if p2 == p1 || !p2.tui {
		t.Fatalf("expected fresh assistant-tui process; same=%v tui=%v", p2 == p1, p2.tui)
	}
	if val, ok := envCategories(p2.cmd.Env); !ok || val != "emotion,finance" {
		t.Fatalf("tui turn 2 env: ok=%v val=%q, want emotion,finance", ok, val)
	}

	// While active, categories_unknown refuses with CodeConversationBusy and keeps p2 intact.
	p2.mu.Lock()
	p2.busy = true
	p2.mu.Unlock()
	n.setSessionCategories(id, nil, true)
	_, err = n.assistantSend(ctx, id, cwd, "claude", "turn 3 busy", nil, "", "", modelExecution{Interactive: true})
	assertConversationBusy(t, err)
	n.mu.Lock()
	stillP2 := n.procs[id]
	n.mu.Unlock()
	if stillP2 != p2 {
		t.Fatal("busy assistant-tui process was replaced on categories_unknown")
	}
	p2.mu.Lock()
	p2.busy = false
	p2.mu.Unlock()

	// Once idle, the retry restarts assistant-tui without PERSONA_CATEGORIES.
	if _, err := n.assistantSend(ctx, id, cwd, "claude", "turn 3 idle", nil, "", "", modelExecution{Interactive: true}); err != nil {
		t.Fatal(err)
	}
	waitIdle(id)
	n.mu.Lock()
	p3 := n.procs[id]
	n.mu.Unlock()
	if p3 == p2 || !p3.tui {
		t.Fatalf("expected fresh assistant-tui process on unknown; same=%v tui=%v", p3 == p2, p3.tui)
	}
	if val, ok := envCategories(p3.cmd.Env); ok {
		t.Fatalf("tui turn 3 env: got %s=%q, want absent", categoriesEnv, val)
	}
}

// codex app-server stays alive across turns and must restart when idle (or refuse when active) if PERSONA_CATEGORIES
// changes.
func TestCodexAppServerCategoriesStaleRestartsAndRefusesBusy(t *testing.T) {
	t.Setenv("PERSONA_SKILLS_DIR", t.TempDir())
	t.Setenv("PERSONA_PLUGIN_SKILLS_DIR", t.TempDir())
	fakeCodexHome(t, testThread)
	rolloutDir := filepath.Join(os.Getenv("HOME"), ".codex", "sessions", "2026", "11", "24")
	if err := os.MkdirAll(rolloutDir, 0o700); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(rolloutDir, "rollout-2026-11-24T00-00-00-"+testThread+".jsonl")
	if err := os.WriteFile(rollout, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PERSONA_FAKE_CODEX_PATH", rollout)
	t.Setenv("PERSONA_FAKE_CODEX_COMPLETE", "1")
	n := codexTestNode(t)
	withFakeHub(t, n)
	cwd := prepareCategoriesDir(t, n, ".persona/assistant", []string{})
	ctx := context.Background()
	waitIdle := func(id string) {
		t.Helper()
		waitFor(t, "codex idle", func() bool {
			n.mu.Lock()
			p := n.procs[id]
			n.mu.Unlock()
			if p == nil {
				return false
			}
			p.mu.Lock()
			defer p.mu.Unlock()
			return !p.busy
		})
	}

	id, err := n.assistantSend(ctx, "", cwd, "codex", "turn 1", nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { n.stopAssistant(id) })
	waitIdle(id)
	n.mu.Lock()
	p1 := n.procs[id]
	n.mu.Unlock()
	if val, ok := envCategories(p1.cmd.Env); !ok || val != "" {
		t.Fatalf("codex turn 1 env: ok=%v val=%q, want general empty string", ok, val)
	}

	// Same general categories reuse the live codex app-server.
	general := []string{}
	n.setSessionCategories(id, &general, false)
	if _, err := n.assistantSend(ctx, id, cwd, "codex", "turn 2", nil, "", ""); err != nil {
		t.Fatal(err)
	}
	waitIdle(id)
	n.mu.Lock()
	p2 := n.procs[id]
	n.mu.Unlock()
	if p2 != p1 {
		t.Fatal("unchanged categories needlessly restarted codex app-server")
	}

	// Sensitive tags restart idle codex app-server with PERSONA_CATEGORIES=emotion,finance.
	cats := []string{"finance", "emotion"}
	n.setSessionCategories(id, &cats, false)
	if _, err := n.assistantSend(ctx, id, cwd, "codex", "turn 3", nil, "", ""); err != nil {
		t.Fatal(err)
	}
	waitIdle(id)
	n.mu.Lock()
	p3 := n.procs[id]
	n.mu.Unlock()
	if p3 == p1 {
		t.Fatal("sensitive categories reused the live codex app-server that still had PERSONA_CATEGORIES=")
	}
	if val, ok := envCategories(p3.cmd.Env); !ok || val != "emotion,finance" {
		t.Fatalf("codex turn 3 env: ok=%v val=%q, want emotion,finance", ok, val)
	}

	// Active codex app-server refuses categories_unknown with CodeConversationBusy.
	p3.mu.Lock()
	p3.busy = true
	p3.mu.Unlock()
	n.setSessionCategories(id, nil, true)
	_, err = n.assistantSend(ctx, id, cwd, "codex", "turn 4 busy", nil, "", "")
	assertConversationBusy(t, err)
	n.mu.Lock()
	stillP3 := n.procs[id]
	n.mu.Unlock()
	if stillP3 != p3 {
		t.Fatal("active codex app-server was replaced on categories_unknown")
	}
	p3.mu.Lock()
	p3.busy = false
	p3.mu.Unlock()

	// Once idle, codex app-server restarts without PERSONA_CATEGORIES.
	if _, err := n.assistantSend(ctx, id, cwd, "codex", "turn 4 idle", nil, "", ""); err != nil {
		t.Fatal(err)
	}
	waitIdle(id)
	n.mu.Lock()
	p4 := n.procs[id]
	n.mu.Unlock()
	if p4 == p3 {
		t.Fatal("idle codex app-server was not restarted on categories_unknown")
	}
	if val, ok := envCategories(p4.cmd.Env); ok {
		t.Fatalf("codex turn 4 env: got %s=%q, want absent", categoriesEnv, val)
	}
}

// A warm spare spawned before assistant.prepare (or before the directory's default categories changed) is never adopted
// with stale PERSONA_CATEGORIES and is replaced on the next assistantWarm.
func TestSpareRejectsAndReplacesStaleCategories(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			t.Setenv("PERSONA_SKILLS_DIR", t.TempDir())
			t.Setenv("PERSONA_PLUGIN_SKILLS_DIR", t.TempDir())
			if agent == "codex" {
				fakeCodexHome(t, testThread)
			} else {
				fakeClaudeHome(t, "if [ \"$1\" = \"--version\" ]; then echo '2.1.320'; exit 0; fi\necho '{\"type\":\"system\",\"subtype\":\"init\"}'\nwhile read line; do :; done\n")
			}
			n := codexTestNode(t)
			withFakeHub(t, n)
			cwd := filepath.Join(n.home, ".persona", "assistant")
			if err := os.MkdirAll(cwd, 0o700); err != nil {
				t.Fatal(err)
			}

			// Warm BEFORE assistant.prepare -> spare has PERSONA_CATEGORIES absent.
			if err := n.assistantWarm(cwd, agent); err != nil {
				t.Fatal(err)
			}
			n.mu.Lock()
			beforePrep := n.spare
			n.mu.Unlock()
			if beforePrep == nil {
				t.Fatal("no initial spare")
			}
			t.Cleanup(beforePrep.stop)
			if _, ok := envCategories(beforePrep.cmd.Env); ok {
				t.Fatal("pre-prepare spare unexpectedly had PERSONA_CATEGORIES")
			}

			// assistant.prepare sets general categories -> pre-prepare spare must be rejected by adoptSpare and replaced by assistantWarm.
			execOpt := modelExecution{ConfigDir: beforePrep.model.ConfigDir}
			cwd = prepareCategoriesDir(t, n, ".persona/assistant", []string{})
			if got := n.adoptSpare(cwd, agent, execOpt); got != nil {
				t.Fatal("pre-prepare spare with absent PERSONA_CATEGORIES was adopted after assistant.prepare")
			}
			if err := n.assistantWarm(cwd, agent, execOpt); err != nil {
				t.Fatal(err)
			}
			n.mu.Lock()
			generalSpare := n.spare
			n.mu.Unlock()
			if generalSpare == nil || generalSpare == beforePrep {
				t.Fatal("assistantWarm kept the pre-prepare spare")
			}
			t.Cleanup(generalSpare.stop)
			if val, ok := envCategories(generalSpare.cmd.Env); !ok || val != "" {
				t.Fatalf("general spare env: ok=%v val=%q, want empty string", ok, val)
			}
			select {
			case <-beforePrep.exited:
			case <-time.After(5 * time.Second):
				t.Fatal("stale pre-prepare spare did not exit")
			}

			// Changing the directory's default categories invalidates generalSpare too.
			prepareCategoriesDir(t, n, ".persona/assistant", []string{"health"})
			if got := n.adoptSpare(cwd, agent, execOpt); got != nil {
				t.Fatal("spare with PERSONA_CATEGORIES= was adopted after directory categories changed to health")
			}
			if err := n.assistantWarm(cwd, agent, execOpt); err != nil {
				t.Fatal(err)
			}
			n.mu.Lock()
			healthSpare := n.spare
			n.mu.Unlock()
			if healthSpare == nil || healthSpare == generalSpare {
				t.Fatal("assistantWarm kept the stale general spare")
			}
			t.Cleanup(healthSpare.stop)
			if val, ok := envCategories(healthSpare.cmd.Env); !ok || val != "health" {
				t.Fatalf("health spare env: ok=%v val=%q, want health", ok, val)
			}
			select {
			case <-generalSpare.exited:
			case <-time.After(5 * time.Second):
				t.Fatal("stale general spare did not exit")
			}

			// Matching spare is adopted cleanly.
			if got := n.adoptSpare(cwd, agent, execOpt); got != healthSpare {
				t.Fatal("up-to-date spare was not adopted")
			}
		})
	}
}

func TestCategoriesStricterMatrix(t *testing.T) {
	unknown := sessionCategoriesState{}
	general := sessionCategoriesState{value: "", known: true}
	emotion := sessionCategoriesState{value: "emotion", known: true}
	both := sessionCategoriesState{value: "emotion,finance", known: true}
	health := sessionCategoriesState{value: "health", known: true}

	for _, tc := range []struct {
		name       string
		have, want sessionCategoriesState
		wantStrict bool
	}{
		{"unknown to unknown", unknown, unknown, false},
		{"unknown to general", unknown, general, false},
		{"unknown to sensitive", unknown, both, false},
		{"general to general", general, general, false},
		{"general to unknown", general, unknown, true},
		{"general to sensitive", general, both, true},
		{"emotion to unknown", emotion, unknown, true},
		{"emotion to emotion,finance", emotion, both, true},
		{"emotion to health", emotion, health, true},
		{"emotion,finance to emotion,finance", both, both, false},
		{"emotion,finance to emotion", both, emotion, false},
		{"emotion,finance to general", both, general, false},
	} {
		if got := categoriesStricter(tc.have, tc.want); got != tc.wantStrict {
			t.Errorf("%s: categoriesStricter(%+v, %+v) = %v, want %v", tc.name, tc.have, tc.want, got, tc.wantStrict)
		}
	}
}
