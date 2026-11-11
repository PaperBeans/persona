package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// codexRollouts gives n a Codex home with a rollout per thread (its session_meta line only), registered as the node's
// own scan would; meta is what each thread's session_meta says beyond its id. It returns the home.
func codexRollouts(t *testing.T, n *Node, threads map[string]map[string]any) string {
	t.Helper()
	n.codices = newCodexRegistry()
	n.codices.scan = func() []codexProcess { return nil }
	n.dir = filepath.Join(n.home, ".persona")
	n.configureCodex(proto.CodexConfig{})
	home := canonicalCodexHome(t.TempDir(), n.home)
	for id, meta := range threads {
		payload := map[string]any{"id": id, "originator": "codex_cli_rs", "cwd": "/w/app"}
		for k, v := range meta {
			payload[k] = v
		}
		raw, err := json.Marshal(map[string]any{"type": "session_meta", "payload": payload})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(home, id+".jsonl")
		if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		n.registerCodex(home, id, path)
	}
	return home
}

// spawnedBy is the session_meta of a Codex subagent thread: the thread that spawned it and the chat it belongs to.
func spawnedBy(parent, root string) map[string]any {
	return map[string]any{"session_id": root, "forked_from_id": parent, "parent_thread_id": parent, "thread_source": "subagent",
		"source": map[string]any{"subagent": map[string]any{"thread_spawn": map[string]any{"parent_thread_id": parent, "depth": 1}}}}
}

// A runtime's session that is a Codex thread on this machine (codex_thread: Paseo runs the Codex) is listed once, as
// the runtime's session: the thread's own history row is not listed, and the thread's subagents hang off the session
// and sit in its project (#4495). A thread no runtime names stays a history row.
func TestRuntimeSessionIsItsCodexThread(t *testing.T) {
	const thread, sub, deep, other = "0e000000-0000-4000-8000-0000000000a1", "0e000000-0000-4000-8000-0000000000a2",
		"0e000000-0000-4000-8000-0000000000a3", "0e000000-0000-4000-8000-0000000000a4"
	n, _ := runtimeNode(t)
	home := codexRollouts(t, n, map[string]map[string]any{
		thread: {"source": "vscode", "session_id": thread},
		sub:    spawnedBy(thread, thread),
		deep:   spawnedBy(sub, thread),
		other:  {"originator": "codex-tui", "cwd": "/w/else"},
	})
	host := dialRuntime(t, n, paseoHello())
	if f, _ := host.helloR["features"].([]any); !slices.Contains(f, any("codex_thread")) {
		t.Fatalf("features %v", host.helloR["features"])
	}
	status := "working"
	rows := func(named bool) []map[string]any {
		creator := map[string]any{"session_id": "paseo-o1", "agent": "claude", "title": "Plan the release", "cwd": "/w/app", "status": "idle",
			"space_id": "prj_1", "space_name": "app", "repo_root": "/w/app"}
		codex := map[string]any{"session_id": "paseo-c1", "agent": "codex", "title": "Review the plan", "cwd": "/w/app", "status": status,
			"space_id": "prj_1", "space_name": "app", "repo_root": "/w/app", "is_subagent": true, "parent_session_id": "paseo-o1"}
		if named {
			codex["codex_thread"] = thread
		}
		return []map[string]any{creator, codex}
	}

	// The Codex rows of the list by their thread (a row's id is its thread's only in Codex's default home).
	threads := func() map[string]proto.Session {
		out := map[string]proto.Session{}
		for _, s := range listed(t, n) {
			if s.SourceThreadID != "" {
				out[s.SourceThreadID] = s
			}
		}
		return out
	}

	// Not named: the thread is a history row beside the runtime's session, its subagents under that row.
	_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": rows(false)})
	waitFor(t, "listed", func() bool { _, ok := listed(t, n)["paseo-c1"]; return ok })
	got := threads()
	twin := got[thread]
	if twin.SourceKind != "codex_history" || twin.SessionID == "" || got[sub].ParentSessionID != twin.SessionID || got[deep].RootSessionID != twin.SessionID {
		t.Fatalf("without a name: thread %q %q, its subagents under %q and %q", twin.SessionID, twin.SourceKind, got[sub].ParentSessionID, got[deep].RootSessionID)
	}

	// Named: one row for the conversation, the runtime's.
	_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": rows(true)})
	waitFor(t, "the thread's own row gone", func() bool { _, twice := threads()[thread]; return !twice })
	all, got := listed(t, n), threads()
	if _, twice := all[twin.SessionID]; twice {
		t.Fatal("the thread's history row is still listed")
	}
	if s := all["paseo-c1"]; s.SourceKind != proto.SourceRuntime || s.Title != "Review the plan" || s.ParentSessionID != "paseo-o1" || s.RootSessionID != "paseo-o1" {
		t.Fatalf("the runtime's session: %+v", s)
	}
	for id, parent := range map[string]string{sub: "paseo-c1", deep: got[sub].SessionID} {
		s := got[id]
		if !s.IsSubagent || s.SourceKind != "codex_history" || s.ParentSessionID != parent || s.RootSessionID != "paseo-o1" ||
			s.SpaceID != "prj_1" || s.SpaceKind != "paseo" || s.RepoRoot != "/w/app" || s.Capabilities == nil || s.Capabilities.Send {
			t.Errorf("subagent %s: parent %q (want %q) root %q space %q/%q repo %q", id, s.ParentSessionID, parent, s.RootSessionID, s.SpaceKind, s.SpaceID, s.RepoRoot)
		}
	}
	if s := got[other]; s.SourceKind != "codex_history" || s.IsSubagent || s.ParentSessionID != "" {
		t.Fatalf("a thread no runtime names: %+v", s)
	}

	// The runtime's session not running any more (the agent closed in Paseo): still the one row of the conversation.
	status = "offline"
	_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": rows(true)})
	waitFor(t, "the session offline", func() bool { return listed(t, n)["paseo-c1"].Status == proto.StatusOffline })
	if _, twice := threads()[thread]; twice {
		t.Fatal("the thread's history row came back beside the session that is not running")
	}
	// Then another program takes the thread up (a shared app-server has it loaded): its own row is listed, drivable
	// there, and its subagents are under it again. The runtime's session stays, not running.
	shared := &codexRemote{n: n, spec: proto.CodexServer{ID: "shared", Home: home}, ready: true,
		threads: map[string]remoteThread{thread: {id: thread, status: proto.StatusIdle}}}
	n.codices.mu.Lock()
	n.codices.remotes["shared"] = shared
	n.codices.mu.Unlock()
	waitFor(t, "the thread's own row, where it is open", func() bool { return threads()[thread].SourceKind == "codex_app_server" })
	if got := threads(); got[sub].ParentSessionID != got[thread].SessionID || got[deep].RootSessionID != got[thread].SessionID {
		t.Fatalf("its subagents: under %q and %q, want the thread's own row %q", got[sub].ParentSessionID, got[deep].RootSessionID, got[thread].SessionID)
	}
	if _, still := listed(t, n)["paseo-c1"]; !still {
		t.Fatal("the runtime's session went with it")
	}
	// The runtime's session runs again: the conversation is that session, once.
	status = "working"
	_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": rows(true)})
	waitFor(t, "one row again", func() bool { _, twice := threads()[thread]; return !twice })
	n.codices.mu.Lock()
	delete(n.codices.remotes, "shared")
	n.codices.mu.Unlock()

	// The runtime gone: its sessions are, and the thread is history again.
	host.conn.Close()
	waitFor(t, "the thread back as history", func() bool {
		_, runtime := listed(t, n)["paseo-c1"]
		got := threads()
		return !runtime && got[thread].SessionID == twin.SessionID && got[sub].ParentSessionID == twin.SessionID
	})
}

// Two sessions naming one Codex thread (an agent closed and another that took its conversation over): the thread is
// listed as the one that runs, the same one on every list.
func TestCodexThreadNamedTwice(t *testing.T) {
	const thread = "0e000000-0000-4000-8000-0000000000b1"
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	reg := newRuntimeRegistry()
	h := reg.add(nil)
	h.hello, h.ready = runtimeHello{Runtime: "paseo", Name: "Paseo"}, true
	list := []proto.Session{
		{SessionID: "paseo-a", Status: proto.StatusOffline, UpdatedAt: at.Add(time.Hour)},
		{SessionID: "paseo-b", Status: proto.StatusIdle, UpdatedAt: at},
		{SessionID: "paseo-c", Status: proto.StatusIdle, UpdatedAt: at},
		{SessionID: "paseo-d", Status: proto.StatusWorking, UpdatedAt: at},
	}
	reg.setSessions(h, list)
	for _, c := range []struct {
		name  string
		named []string
		want  string
	}{
		{"none", nil, ""},
		{"one", []string{"paseo-a"}, "paseo-a"},
		{"the one not offline", []string{"paseo-a", "paseo-b"}, "paseo-b"},
		{"of two alike, the lower id", []string{"paseo-c", "paseo-b"}, "paseo-b"},
		{"a session the runtime no longer lists", []string{"paseo-gone", "paseo-a"}, "paseo-a"},
	} {
		m := map[string]string{}
		for _, id := range c.named {
			m[id] = thread
		}
		h.setCodexThreads(m)
		for range 20 { // map order must not show
			if got := reg.codexThreads()[thread]; got.session != c.want || got.live != (c.want != "" && c.want != "paseo-a") {
				t.Fatalf("%s: listed as %+v, want %q", c.name, got, c.want)
			}
		}
	}
	if (*runtimeRegistry)(nil).codexThreads() != nil || (*codexListPass)(nil).runtimeOf(thread) != (codexClaim{}) {
		t.Fatal("no registry, no pass: nothing named")
	}
}
