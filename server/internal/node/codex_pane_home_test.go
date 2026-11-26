package node

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestHerdrWaitsForPaneHomeForDuplicateThreadOrTitle(t *testing.T) {
	n := remoteTestNode(t)
	otherRoot := t.TempDir()
	other := canonicalCodexHome(filepath.Join(otherRoot, ".codex"), n.home)
	n.configureCodex(proto.CodexConfig{Homes: []string{other}})
	for _, root := range []string{n.home, otherRoot} {
		writeThread(t, root, remoteRaw, n.home, "same UUID in two homes")
		home := canonicalCodexHome(filepath.Join(root, ".codex"), n.home)
		n.registerCodex(home, remoteRaw, rolloutPath(home, remoteRaw))
		if err := os.WriteFile(filepath.Join(home, "session_index.jsonl"), []byte(`{"id":"`+remoteRaw+`","thread_name":"Same title"}`+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a := host.Agent{Agent: "codex", PaneID: "w1:p1", Cwd: n.home, AgentStatus: proto.StatusIdle, Title: "Same title | Work"}
	for _, reported := range []*host.AgentSession{nil, {Kind: "id", Value: remoteRaw}} {
		a.AgentSession = reported
		n.setAgentsAt([]host.Agent{a}, 0, nil)
		if len(n.agents) != 0 {
			t.Fatal("ambiguous title or raw UUID acquired a live owner")
		}
	}
	n.codices.byPane = map[string]string{a.PaneID: other}
	n.setAgentsAt([]host.Agent{a}, 0, nil)
	if _, ok := n.agent(codexSourceID(other, remoteRaw, n.codexInventory().LegacyHome)); !ok {
		t.Fatal("process evidence did not recover the alternate pane")
	}
}

func TestHerdrRecognizesArchivedThreadInObservedHome(t *testing.T) {
	n := remoteTestNode(t)
	root := t.TempDir()
	home := canonicalCodexHome(filepath.Join(root, ".codex"), n.home)
	writeThread(t, root, remoteRaw, n.home, "archived")
	path := rolloutPath(home, remoteRaw)
	archive := filepath.Join(home, "archived_sessions", filepath.Base(path))
	if err := os.MkdirAll(filepath.Dir(archive), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, archive); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-40 * 24 * time.Hour)
	if err := os.Chtimes(archive, old, old); err != nil {
		t.Fatal(err)
	}
	a := host.Agent{Agent: "codex", PaneID: "w1:p1", Cwd: n.home, AgentStatus: proto.StatusIdle, AgentSession: &host.AgentSession{Kind: "id", Value: remoteRaw}}
	n.configureCodex(proto.CodexConfig{Homes: []string{home}})
	n.codices.byPane = map[string]string{a.PaneID: home}
	n.setAgentsAt([]host.Agent{a}, 0, nil)
	id := codexSourceID(home, remoteRaw, n.codexInventory().LegacyHome)
	if s, ok := n.codexSource(id); !ok || s.path != archive {
		t.Fatalf("archived lookup did not retain its path: %+v", s)
	}
	if _, ok := n.agent(id); !ok {
		t.Fatal("archived rollout not bound to its observed pane")
	}
}

func TestColdHerdrUUIDWaitsForItsRolloutHome(t *testing.T) {
	n := remoteTestNode(t)
	root := t.TempDir()
	other := canonicalCodexHome(filepath.Join(root, ".codex"), n.home)
	n.configureCodex(proto.CodexConfig{Homes: []string{other}})
	a := host.Agent{Agent: "codex", PaneID: "w1:p1", Cwd: n.home, AgentStatus: proto.StatusIdle,
		AgentSession: &host.AgentSession{Kind: "id", Value: remoteRaw}}
	n.setAgentsAt([]host.Agent{a}, 0, nil)
	if _, ok := n.agent(remoteRaw); ok {
		t.Fatal("pane with no home evidence became a controllable default-home UUID")
	}
	if _, ok := n.codexSource(remoteRaw); ok {
		t.Fatal("unknown pane manufactured a default-home source")
	}
	writeThread(t, root, remoteRaw, n.home, "from the other home")
	path := rolloutPath(other, remoteRaw)
	n.configureCodex(proto.CodexConfig{Homes: []string{other}})
	id := n.registerCodex(other, remoteRaw, path)
	if id == remoteRaw {
		t.Fatal("test home must be distinct from the legacy home")
	}
	n.setAgentsAt([]host.Agent{a}, 0, nil)
	if got, ok := n.agent(id); !ok || got.PaneID != a.PaneID {
		t.Fatal("resolved alternative home pane missing")
	}
}

func TestHerdrRefreshIsolatesBlockedRolloutFromOtherAgents(t *testing.T) {
	n := boundedTestNode(t)
	const healthyID = "eeee0000-0000-4000-8000-000000000002"
	slowRoot, fastRoot := t.TempDir(), t.TempDir()
	writeThread(t, slowRoot, remoteRaw, n.home, "slow")
	writeThread(t, fastRoot, healthyID, n.home, "fast")
	slow, fast := canonicalCodexHome(filepath.Join(slowRoot, ".codex"), n.home), canonicalCodexHome(filepath.Join(fastRoot, ".codex"), n.home)
	slowPath, fastPath := rolloutPath(slow, remoteRaw), rolloutPath(fast, healthyID)
	n.configureCodex(proto.CodexConfig{Homes: []string{slow, fast}})
	raw := func(pane, id string) host.Agent {
		return host.Agent{Agent: "codex", PaneID: pane, Cwd: n.home, AgentStatus: proto.StatusIdle, AgentSession: &host.AgentSession{Kind: "id", Value: id}}
	}
	a, b := raw("w1:p1", remoteRaw), raw("w1:p2", healthyID)
	n.codices.byPane = map[string]string{a.PaneID: slow, b.PaneID: fast}
	n.runtimes = newRuntimeRegistry()
	n.runtimes.conns[1] = &runtimeConn{serial: 1, ready: true, sessions: []proto.Session{{SessionID: "runtime-healthy", Agent: "runtime", SourceKind: "runtime", Status: proto.StatusIdle}}}
	n.runtimes.owner["runtime-healthy"] = n.runtimes.conns[1]
	slowID, fastID := n.registerCodex(slow, remoteRaw, slowPath), n.registerCodex(fast, healthyID, fastPath)
	n.setAgentsAt([]host.Agent{a, b}, 0, nil)
	if _, ok := n.agent(slowID); !ok {
		t.Fatal("expected the first host pass to see the slow pane while healthy")
	}
	lock := rolloutCacheFor(slow)
	lock.mu.Lock()
	var once sync.Once
	release := func() { once.Do(lock.mu.Unlock) }
	defer release()
	go n.indexCodexHome(slow, time.Now().Add(codexHomeListWait))
	done := make(chan []proto.Session, 1)
	go func() { done <- n.sessionList(context.Background(), false) }()
	select {
	case rows := <-done:
		if !slices.ContainsFunc(rows, func(s proto.Session) bool {
			return s.SessionID == slowID && s.Status == proto.StatusUnknown && s.Capabilities != nil && !s.Capabilities.Send && !s.Capabilities.Terminal
		}) {
			t.Fatal("list between host refreshes still advertised the blocked live pane")
		}
	case <-time.After(2 * time.Second):
		release()
		<-done
		t.Fatal("list between host refreshes waited for the blocked rollout")
	}
	go func() { n.setAgentsAt([]host.Agent{a, b}, 0, nil); done <- n.sessionList(context.Background(), false) }()
	select {
	case ss := <-done:
		for _, id := range []string{fastID, "runtime-healthy"} {
			if !slices.ContainsFunc(ss, func(s proto.Session) bool { return s.SessionID == id }) {
				t.Fatalf("other row %s missing", id)
			}
		}
		for _, s := range ss {
			if s.SessionID == slowID && s.Capabilities != nil && (s.Capabilities.Send || s.Capabilities.Terminal) {
				t.Fatalf("unverified pane advertised control: %s", s.SessionID)
			}
		}
	case <-time.After(2 * time.Second):
		release()
		<-done
		t.Fatal("one inaccessible Codex home held the host refresh and the unrelated runtime")
	}
	release()
	waitFor(t, "slow pane recovered", func() bool { n.setAgentsAt([]host.Agent{a, b}, 0, nil); _, ok := n.agent(slowID); return ok })
}

func TestVerifiedHerdrPaneDoesNotNeedAHistoryScanSlot(t *testing.T) {
	n := remoteTestNode(t)
	root := t.TempDir()
	writeThread(t, root, remoteRaw, n.home, "active pane")
	home := canonicalCodexHome(filepath.Join(root, ".codex"), n.home)
	n.configureCodex(proto.CodexConfig{Homes: []string{home}})
	a := host.Agent{Agent: "codex", PaneID: "w1:p1", Cwd: n.home, AgentStatus: proto.StatusIdle,
		AgentSession: &host.AgentSession{Kind: "id", Value: remoteRaw}}
	n.codices.byPane = map[string]string{a.PaneID: home}
	// The catalog's filesystem slots can all be waiting on unrelated homes. Live
	// pane lookup has its own small bound and just verified the active rollout.
	count := 0
	defer func() {
		for range count {
			<-codexHomeScanSlots
		}
	}()
fill:
	for {
		select {
		case codexHomeScanSlots <- struct{}{}:
			count++
		default:
			break fill
		}
	}
	n.setAgentsAt([]host.Agent{a}, 0, nil)
	id := codexSourceID(home, remoteRaw, n.codexInventory().LegacyHome)
	for _, row := range n.sessionList(context.Background(), false) {
		if row.SessionID == id && row.PaneID == a.PaneID && row.Status != proto.StatusUnknown {
			return
		}
	}
	t.Fatal("the healthy pane lost its live row when unrelated history scans exhausted the slots")
}

// A timed-out pane lookup is superseded after codexPaneReadRetry so a recovered filesystem or updated owner can
// restore the live pane without waiting forever on an earlier wedged syscall, while per-pane inflight capping keeps
// one permanently wedged mount from starving healthy panes.
func TestCodexPaneIdentitySupersedesTimedOutReadAfterCooldown(t *testing.T) {
	oldRetry := codexPaneReadRetry
	codexPaneReadRetry = 40 * time.Millisecond
	t.Cleanup(func() { codexPaneReadRetry = oldRetry })

	t.Run("supersedes wedged lookup after cooldown once home switches", func(t *testing.T) {
		n := remoteTestNode(t)
		slowRoot, fastRoot := t.TempDir(), t.TempDir()
		const fastID = "f6000000-0000-4000-8000-000000000002"
		writeThread(t, slowRoot, remoteRaw, n.home, "slow")
		writeThread(t, fastRoot, fastID, n.home, "fast")
		slow := canonicalCodexHome(filepath.Join(slowRoot, ".codex"), n.home)
		fast := canonicalCodexHome(filepath.Join(fastRoot, ".codex"), n.home)
		n.configureCodex(proto.CodexConfig{Homes: []string{slow, fast}})
		a := host.Agent{Agent: "codex", PaneID: "w1:p1", Cwd: n.home, AgentStatus: proto.StatusIdle,
			AgentSession: &host.AgentSession{Kind: "id", Value: remoteRaw}}
		n.codices.byPane = map[string]string{a.PaneID: slow}

		slowLock := rolloutCacheFor(slow)
		slowLock.mu.Lock()
		var once sync.Once
		release := func() { once.Do(slowLock.mu.Unlock) }
		defer release()

		// 1. First lookup blocks on slow home and times out.
		if _, ok := n.codexPaneIdentity(a, "", false, nil, time.Now().Add(20*time.Millisecond)); ok {
			t.Fatal("blocked lookup reported ok")
		}
		// 2. Immediate retry before cooldown does not spawn a second worker.
		if _, ok := n.codexPaneIdentity(a, "", false, nil, time.Now().Add(5*time.Millisecond)); ok {
			t.Fatal("retry before cooldown reported ok")
		}
		n.codices.paneReads.Lock()
		beforeCooldown := n.codices.paneReads.inflight[a.PaneID]
		n.codices.paneReads.Unlock()
		if beforeCooldown != 1 {
			t.Fatalf("inflight before cooldown = %d, want 1", beforeCooldown)
		}

		// 3. After cooldown, the pane's owner points to the healthy home while the old syscall is still wedged.
		waitFor(t, "pane lookup retry cooldown", func() bool {
			n.codices.paneReads.Lock()
			defer n.codices.paneReads.Unlock()
			for _, read := range n.codices.paneReads.pending {
				return time.Since(read.started) >= codexPaneReadRetry
			}
			return false
		})
		a.AgentSession = &host.AgentSession{Kind: "id", Value: fastID}
		n.codices.mu.Lock()
		n.codices.byPane[a.PaneID] = fast
		n.codices.mu.Unlock()

		want := codexSourceID(fast, fastID, n.codexInventory().LegacyHome)
		match, ok := n.codexPaneIdentity(a, "", false, nil, time.Now().Add(time.Second))
		if !ok || match.id != want {
			t.Fatalf("after cooldown supersede: match=%+v ok=%v, want id=%s", match, ok, want)
		}

		// Release the wedged worker and verify inflight count drains cleanly.
		release()
		waitFor(t, "wedged pane worker drains", func() bool {
			n.codices.paneReads.Lock()
			defer n.codices.paneReads.Unlock()
			return n.codices.paneReads.inflight[a.PaneID] == 0
		})
	})

	t.Run("per-pane cap prevents a permanently wedged mount from starving healthy panes", func(t *testing.T) {
		n := remoteTestNode(t)
		slowRoot, fastRoot := t.TempDir(), t.TempDir()
		const fastID = "f7000000-0000-4000-8000-000000000002"
		writeThread(t, slowRoot, remoteRaw, n.home, "slow")
		writeThread(t, fastRoot, fastID, n.home, "fast")
		slow := canonicalCodexHome(filepath.Join(slowRoot, ".codex"), n.home)
		fast := canonicalCodexHome(filepath.Join(fastRoot, ".codex"), n.home)
		n.configureCodex(proto.CodexConfig{Homes: []string{slow, fast}})
		a := host.Agent{Agent: "codex", PaneID: "w1:p1", Cwd: n.home, AgentStatus: proto.StatusIdle,
			AgentSession: &host.AgentSession{Kind: "id", Value: remoteRaw}}
		b := host.Agent{Agent: "codex", PaneID: "w1:p2", Cwd: n.home, AgentStatus: proto.StatusIdle,
			AgentSession: &host.AgentSession{Kind: "id", Value: fastID}}
		n.codices.byPane = map[string]string{a.PaneID: slow, b.PaneID: fast}

		slowLock := rolloutCacheFor(slow)
		slowLock.mu.Lock()
		var once sync.Once
		release := func() { once.Do(slowLock.mu.Unlock) }
		defer release()

		for range 5 {
			n.codexPaneIdentity(a, "", false, nil, time.Now().Add(5*time.Millisecond))
			waitFor(t, "next pane retry", func() bool {
				n.codices.paneReads.Lock()
				defer n.codices.paneReads.Unlock()
				for _, read := range n.codices.paneReads.pending {
					return time.Since(read.started) >= codexPaneReadRetry
				}
				return false
			})
		}
		n.codices.paneReads.Lock()
		inflight := n.codices.paneReads.inflight[a.PaneID]
		n.codices.paneReads.Unlock()
		if inflight > codexPaneMaxInflight {
			t.Fatalf("wedged pane consumed %d workers, want at most %d", inflight, codexPaneMaxInflight)
		}
		want := codexSourceID(fast, fastID, n.codexInventory().LegacyHome)
		if match, ok := n.codexPaneIdentity(b, "", false, nil, time.Now().Add(time.Second)); !ok || match.id != want {
			t.Fatalf("healthy pane starved by wedged pane: match=%+v ok=%v", match, ok)
		}
		release()
		waitFor(t, "wedged workers drain", func() bool {
			n.codices.paneReads.Lock()
			defer n.codices.paneReads.Unlock()
			return n.codices.paneReads.inflight[a.PaneID] == 0
		})
	})
}
