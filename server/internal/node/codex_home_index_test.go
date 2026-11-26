package node

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestCodexIndexListsHealthyHomeWhileAnotherScanIsBlocked(t *testing.T) {
	n := boundedTestNode(t)
	slowRoot := t.TempDir()
	slowHome := canonicalCodexHome(filepath.Join(slowRoot, ".codex"), n.home)
	const slowID = "aaaa0000-0000-4000-8000-000000000001"
	const healthyID = "bbbb0000-0000-4000-8000-000000000002"
	writeThread(t, slowRoot, slowID, n.home, "first")
	writeThread(t, n.home, healthyID, n.home, "second")
	n.configureCodex(proto.CodexConfig{Homes: []string{slowHome}})
	slow := rolloutCacheFor(slowHome)
	slow.mu.Lock() // simulate a filesystem read that never returns on its own
	var release sync.Once
	unblock := func() { release.Do(slow.mu.Unlock) }
	defer unblock()
	go n.indexCodexHome(slowHome, time.Now().Add(codexHomeListWait)) // the background scan can be holding it before a list arrives
	has := func(rows []proto.Session, id string) bool {
		return slices.ContainsFunc(rows, func(s proto.Session) bool { return s.SourceThreadID == id })
	}
	done := make(chan []proto.Session, 1)
	go func() { done <- n.codexSessions(map[string]bool{}) }()
	select {
	case rows := <-done:
		if !has(rows, healthyID) || has(rows, slowID) {
			t.Fatalf("listing while a home is blocked: %d rows, healthy=%v, blocked=%v", len(rows), has(rows, healthyID), has(rows, slowID))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a blocked Codex home stopped the session list")
	}
	unblock()
	deadline := time.After(2 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		rows := n.codexSessions(map[string]bool{})
		if has(rows, slowID) && has(rows, healthyID) {
			return
		}
		select {
		case <-deadline:
			t.Fatal("the released home did not return to the list")
		case <-tick.C:
		}
	}
}

func TestCodexIndexSharesBlockedWork(t *testing.T) {
	var index codexHomeIndex
	var calls atomic.Int32
	release := make(chan struct{})
	finished := make(chan struct{})
	read := func() (time.Duration, bool) {
		calls.Add(1)
		<-release
		close(finished)
		return time.Second, true
	}
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	for range 4 {
		if index.available("slow", time.Now().Add(codexHomeListWait), read) {
			t.Fatal("an unfinished scan was reported ready")
		}
	}
	if calls.Load() != 1 || !index.available("healthy", time.Now().Add(codexHomeListWait), func() (time.Duration, bool) { return time.Second, true }) {
		t.Fatalf("blocked scan ran %d times or prevented an unrelated scan", calls.Load())
	}
	unblock()
	<-finished
}

func TestCodexSessionsCarryGoalAndPlan(t *testing.T) {
	n := remoteTestNode(t)
	const threadID = "cccc0000-0000-4000-8000-000000000003"
	writeThread(t, n.home, threadID, n.home, "fix the parser")
	matches, err := filepath.Glob(filepath.Join(n.home, ".codex", "sessions", "*", "*", "*", "rollout-*-"+threadID+".jsonl"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("find rollout: matches=%v err=%v", matches, err)
	}
	rollout := matches[0]
	extra := `{"timestamp":"2026-11-22T10:00:02Z","type":"event_msg","payload":{"type":"thread_goal_updated","goal":{"objective":"Keep all tests green","status":"active","tokensUsed":120,"tokenBudget":1000,"timeUsedSeconds":15}}}` + "\n" +
		`{"timestamp":"2026-11-22T10:00:03Z","type":"response_item","payload":{"type":"function_call","name":"update_plan","call_id":"call_reg_1","arguments":"{\"plan\":[{\"step\":\"Reproduce\",\"status\":\"completed\"},{\"step\":\"Fix\",\"status\":\"in_progress\"}]}"}}` + "\n" +
		`{"timestamp":"2026-11-22T10:00:04Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_reg_1","output":"Plan updated"}}` + "\n"
	f, err := os.OpenFile(rollout, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(extra); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	rows := n.codexSessions(map[string]bool{})
	var found *proto.Session
	for i := range rows {
		if rows[i].SourceThreadID == threadID {
			found = &rows[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("codexSessions did not return thread %s: %+v", threadID, rows)
	}
	if found.Goal == nil || found.Goal.Objective != "Keep all tests green" || found.Goal.Status != proto.GoalActive {
		t.Fatalf("codexSessions Goal = %+v, want active goal", found.Goal)
	}
	if found.Plan == nil || found.Plan.Done != 1 || found.Plan.Total != 2 || found.Plan.Current != "Fix" {
		t.Fatalf("codexSessions Plan = %+v, want 1/2 Fix", found.Plan)
	}
}

func TestCodexIndexUsesOneWaitBudgetForAList(t *testing.T) {
	var index codexHomeIndex
	release := make(chan struct{})
	var stop sync.Once
	unblock := func() { stop.Do(func() { close(release) }) }
	defer unblock()
	scan := func() (time.Duration, bool) {
		<-release
		return time.Second, true
	}
	done := make(chan struct{})
	go func() {
		deadline := time.Now().Add(codexHomeScanWait)
		for j := range 8 {
			index.available(strconv.Itoa(j), deadline, scan)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * codexHomeScanWait):
		unblock()
		<-done
		t.Fatal("the list paid the wait budget separately for each blocked home")
	}
}

func TestCodexIndexRetainsObservedRowsWhenWorkersAreBusy(t *testing.T) {
	n := boundedTestNode(t)
	const id = "cccc0000-0000-4000-8000-000000000003"
	writeThread(t, n.home, id, n.home, "keep this history")
	rows := n.codexSessions(map[string]bool{})
	if len(rows) != 1 || rows[0].SessionID != id {
		t.Fatalf("initial list = %v", rows)
	}
	home := canonicalCodexHome(n.codexHome(), n.home)
	n.codices.index.mu.Lock()
	n.codices.index.homes[home].fresh = time.Time{}
	n.codices.index.mu.Unlock()
	acquired := 0
fill:
	for {
		select {
		case codexHomeScanSlots <- struct{}{}:
			acquired++
		default:
			break fill
		}
	}
	defer func() {
		for range acquired {
			<-codexHomeScanSlots
		}
	}()
	seen := map[string]bool{}
	got := n.codexSessions(seen)
	if len(got) != 1 || got[0].SessionID != id || got[0].Title != rows[0].Title || got[0].Status != proto.StatusUnknown ||
		got[0].Capabilities == nil || got[0].Capabilities.Send || !seen[id] {
		t.Fatalf("a busy index lost the last observation or offered stale controls: %v", got)
	}
	if dupe := n.codexSessions(map[string]bool{id: true}); len(dupe) != 0 {
		t.Fatalf("a live row was duplicated with stale history: %v", dupe)
	}
}

func TestCodexFirstMessageFindsHealthyHomePastABlockedOne(t *testing.T) {
	for _, waiting := range []bool{false, true} {
		name := "start"
		if waiting {
			name = "empty pane receives its first message"
		}
		t.Run(name, func(t *testing.T) {
			n := boundedTestNode(t)
			slowRoot, fastRoot := t.TempDir(), t.TempDir()
			const oldID = "dddd0000-0000-4000-8000-000000000004"
			const newID = "eeee0000-0000-4000-8000-000000000005"
			const prompt = "use the selected profile"
			began := time.Now()
			writeThread(t, slowRoot, oldID, n.home, "another request")
			writeThread(t, fastRoot, newID, n.home, prompt)
			slowHome := canonicalCodexHome(filepath.Join(slowRoot, ".codex"), n.home)
			fastHome := canonicalCodexHome(filepath.Join(fastRoot, ".codex"), n.home)
			n.configureCodex(proto.CodexConfig{Homes: []string{slowHome, fastHome}})
			cache := rolloutCacheFor(slowHome)
			cache.mu.Lock()
			ctx, cancel := context.WithCancel(t.Context())
			result := make(chan string, 1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				if waiting {
					w := codexWait{pane: "pane", cwd: n.home, began: began}
					for _, thread := range n.newCodexThreads(&w, codexHeld{}) {
						if thread.first == prompt {
							result <- thread.id
							return
						}
					}
					result <- ""
					return
				}
				id, _ := n.waitCodexThread(ctx, "pane", n.home, prompt, began)
				result <- id
			}()
			defer func() { cancel(); cache.mu.Unlock(); <-done }()
			want := codexSourceID(fastHome, newID, n.codexInventory().LegacyHome)
			select {
			case id := <-result:
				if id != want {
					t.Fatalf("first message matched %q, want %q", id, want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("an unrelated home held up the first message")
			}
		})
	}
}

// Even when a home's index is still marked fresh (or liveCodexHomeReady would have seen it unlocked),
// another reader acquiring the home's rollout cache mutex before decoration must not block codexSessions
// or sessionList across homes: downstream reads use TryLock and emit unavailable snapshots instead.
func TestCodexListDoesNotBlockWhenMutexTakenAfterFreshIndex(t *testing.T) {
	n := boundedTestNode(t)
	slowRoot := t.TempDir()
	slowHome := canonicalCodexHome(filepath.Join(slowRoot, ".codex"), n.home)
	const slowID = "f0000000-0000-4000-8000-000000000001"
	const healthyID = "f0000000-0000-4000-8000-000000000002"
	writeThread(t, slowRoot, slowID, n.home, "slow thread")
	writeThread(t, n.home, healthyID, n.home, "healthy thread")
	n.configureCodex(proto.CodexConfig{Homes: []string{slowHome}})

	// Warm both homes so index freshness is set and lastRows holds an observed row for slowHome.
	initial := n.codexSessions(map[string]bool{})
	if len(initial) != 2 {
		t.Fatalf("initial list = %d rows, want 2", len(initial))
	}
	slowPublicID := codexSourceID(slowHome, slowID, n.codexInventory().LegacyHome)

	// Keep the index explicitly fresh to verify freshness alone never authorizes a blocking c.mu.Lock().
	n.codices.index.mu.Lock()
	n.codices.index.homes[slowHome].fresh = time.Now().Add(time.Minute)
	n.codices.index.homes[slowHome].ok = true
	n.codices.index.mu.Unlock()

	// Also place a live non-Herdr Codex pane on slowHome so sessionList exercises codexTitleIn/transcriptPathIn directly.
	n.mu.Lock()
	n.agents = map[string]host.Agent{
		slowPublicID: {Agent: "codex", PaneID: "tmux:%1", Cwd: n.home, AgentStatus: proto.StatusIdle, Title: "slow pane"},
	}
	n.mu.Unlock()

	cache := rolloutCacheFor(slowHome)
	cache.mu.Lock()
	var once sync.Once
	release := func() { once.Do(cache.mu.Unlock) }
	defer release()

	// 1. codexSessionsIn must not block on threadTitle/listedCodexSource despite fresh index.
	historyDone := make(chan []proto.Session, 1)
	go func() { historyDone <- n.codexSessions(map[string]bool{}) }()
	select {
	case rows := <-historyDone:
		var sawHealthy, sawSlowUnavailable bool
		for _, r := range rows {
			if r.SourceThreadID == healthyID && r.Status != proto.StatusUnknown {
				sawHealthy = true
			}
			if r.SessionID == slowPublicID && r.Status == proto.StatusUnknown &&
				r.Capabilities != nil && !r.Capabilities.Send && !r.Capabilities.Terminal {
				sawSlowUnavailable = true
			}
		}
		if !sawHealthy || !sawSlowUnavailable {
			t.Fatalf("codexSessions with locked mutex after fresh index: healthy=%v slowUnavailable=%v rows=%+v", sawHealthy, sawSlowUnavailable, rows)
		}
	case <-time.After(2 * time.Second):
		release()
		<-historyDone
		t.Fatal("codexSessions blocked on rollout cache mutex after index reported fresh")
	}

	// 2. sessionListIn must not block on codexTitleIn/transcriptPathIn for the live Codex pane either.
	listDone := make(chan []proto.Session, 1)
	go func() { listDone <- n.sessionList(context.Background(), false) }()
	select {
	case rows := <-listDone:
		var sawHealthy, sawSlowUnavailable bool
		for _, r := range rows {
			if r.SourceThreadID == healthyID && r.Status != proto.StatusUnknown {
				sawHealthy = true
			}
			if r.SessionID == slowPublicID && r.Status == proto.StatusUnknown &&
				r.Capabilities != nil && !r.Capabilities.Send && !r.Capabilities.Terminal {
				sawSlowUnavailable = true
			}
		}
		if !sawHealthy || !sawSlowUnavailable {
			t.Fatalf("sessionList with locked mutex on live pane: healthy=%v slowUnavailable=%v rows=%+v", sawHealthy, sawSlowUnavailable, rows)
		}
	case <-time.After(2 * time.Second):
		release()
		<-listDone
		t.Fatal("sessionList blocked on live pane rollout cache mutex")
	}
}

// A configured home that does not exist yet uses a short retry interval so creating it after startup
// makes its history visible promptly; non-directory or broken stat failures are not cached as healthy.
func TestCodexIndexRecoversNewlyCreatedHomeAndPropagatesStatFailure(t *testing.T) {
	n := remoteTestNode(t)
	lateRoot := t.TempDir()
	lateHome := canonicalCodexHome(filepath.Join(lateRoot, ".codex"), n.home)
	n.configureCodex(proto.CodexConfig{Homes: []string{lateHome}})

	before := time.Now()
	if !n.indexCodexHome(lateHome, time.Now().Add(codexHomeListWait)) {
		t.Fatal("a not-yet-created home should not fail readiness")
	}
	n.codices.index.mu.Lock()
	freshUntil := n.codices.index.homes[lateHome].fresh
	n.codices.index.mu.Unlock()
	if ttl := freshUntil.Sub(before); ttl > 5*time.Second {
		t.Fatalf("missing home cached for %v, want short retry interval", ttl)
	}

	// Create the home with a thread after startup; it should appear once the short retry window elapses.
	const lateID = "f1000000-0000-4000-8000-000000000001"
	writeThread(t, lateRoot, lateID, n.home, "created after startup")
	waitFor(t, "newly created home's thread listed promptly", func() bool {
		return slices.ContainsFunc(n.codexSessions(map[string]bool{}), func(s proto.Session) bool {
			return s.SourceThreadID == lateID
		})
	})

	// A path that exists but is not a directory must report unavailable rather than a healthy fresh scan.
	notDir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(notDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n.indexCodexHome(notDir, time.Now().Add(codexHomeListWait)) {
		t.Fatal("non-directory home was reported as a healthy fresh scan")
	}
	if n.indexCodexHome(notDir, time.Now().Add(codexHomeListWait)) {
		t.Fatal("cached non-directory home was reported as healthy on repeat check")
	}
}

// A failed scan (transient permission, mount, or I/O error) must not be cached as fresh so the next bounded scan
// restores healthy rows as soon as the filesystem recovers.
func TestCodexIndexDoesNotCacheFailedScanAsFresh(t *testing.T) {
	t.Run("available does not cache failed scan TTL", func(t *testing.T) {
		var index codexHomeIndex
		var calls atomic.Int32
		if index.available("flaky", time.Now().Add(codexHomeListWait), func() (time.Duration, bool) {
			calls.Add(1)
			return time.Minute, false
		}) {
			t.Fatal("failed scan was reported available")
		}
		if !index.available("flaky", time.Now().Add(codexHomeListWait), func() (time.Duration, bool) {
			calls.Add(1)
			return time.Second, true
		}) {
			t.Fatal("recovered scan was blocked by a cached failure")
		}
		if calls.Load() != 2 {
			t.Fatalf("scan ran %d times, want 2", calls.Load())
		}
	})

	t.Run("recovered home lists immediately without waiting a second", func(t *testing.T) {
		n := remoteTestNode(t)
		root := t.TempDir()
		home := canonicalCodexHome(filepath.Join(root, ".codex"), n.home)
		// Start with a non-directory so indexCodexHome returns false.
		if err := os.WriteFile(home, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		n.configureCodex(proto.CodexConfig{Homes: []string{home}})
		if n.indexCodexHome(home, time.Now().Add(codexHomeListWait)) {
			t.Fatal("non-directory home succeeded")
		}
		// Immediately replace it with a healthy Codex home containing a thread.
		if err := os.Remove(home); err != nil {
			t.Fatal(err)
		}
		const id = "f2000000-0000-4000-8000-000000000001"
		writeThread(t, root, id, n.home, "recovered immediately")
		rows := n.codexSessions(map[string]bool{})
		if !slices.ContainsFunc(rows, func(s proto.Session) bool { return s.SourceThreadID == id && s.Status != proto.StatusUnknown }) {
			t.Fatalf("recovered home was hidden by cached failure: %+v", rows)
		}
	})
}

// A healthy pass where another surface (a live pane or a runtime claim) suppresses a thread's history row must still
// retain the last observed snapshot so a subsequent home stall + pane/runtime removal emits a StatusUnknown row
// instead of making the thread vanish.
func TestCodexListPreservesSnapshotAcrossLiveOwnershipAndRuntimeClaims(t *testing.T) {
	t.Run("history -> healthy live ownership -> home stall -> pane removal -> recovery", func(t *testing.T) {
		n := boundedTestNode(t)
		slowRoot := t.TempDir()
		slowHome := canonicalCodexHome(filepath.Join(slowRoot, ".codex"), n.home)
		const slowID = "f3000000-0000-4000-8000-000000000001"
		const healthyID = "f3000000-0000-4000-8000-000000000002"
		writeThread(t, slowRoot, slowID, n.home, "observed history title")
		writeThread(t, n.home, healthyID, n.home, "healthy thread")
		n.configureCodex(proto.CodexConfig{Homes: []string{slowHome}})
		slowPublicID := codexSourceID(slowHome, slowID, n.codexInventory().LegacyHome)

		// 1. Observe thread as healthy history.
		initial := n.sessionList(context.Background(), false)
		var origTitle string
		for _, r := range initial {
			if r.SessionID == slowPublicID {
				origTitle = r.Title
			}
		}
		if origTitle == "" {
			t.Fatalf("initial history missing %s: %+v", slowPublicID, initial)
		}

		// 2. Healthy live ownership suppresses the history row via seen[id].
		n.mu.Lock()
		n.agents = map[string]host.Agent{
			slowPublicID: {Agent: "codex", PaneID: "tmux:%1", Cwd: n.home, AgentStatus: proto.StatusIdle, Title: "live pane"},
		}
		n.mu.Unlock()
		livePass := n.sessionList(context.Background(), false)
		if !slices.ContainsFunc(livePass, func(s proto.Session) bool { return s.SessionID == slowPublicID && s.PaneID == "tmux:%1" }) {
			t.Fatalf("healthy live pane missing: %+v", livePass)
		}

		// 3. Home stalls and the pane disappears.
		n.mu.Lock()
		n.agents = map[string]host.Agent{}
		n.mu.Unlock()
		n.codices.index.mu.Lock()
		if s := n.codices.index.homes[slowHome]; s != nil {
			s.fresh = time.Time{}
		}
		n.codices.index.mu.Unlock()
		cache := rolloutCacheFor(slowHome)
		cache.mu.Lock()
		var once sync.Once
		release := func() { once.Do(cache.mu.Unlock) }
		defer release()

		stalled := n.sessionList(context.Background(), false)
		var sawSlowUnavailable, sawHealthy bool
		for _, r := range stalled {
			if r.SourceThreadID == healthyID && r.Status != proto.StatusUnknown {
				sawHealthy = true
			}
			if r.SessionID == slowPublicID && r.Status == proto.StatusUnknown && r.Title == origTitle &&
				r.Capabilities != nil && !r.Capabilities.Send && !r.Capabilities.Terminal {
				sawSlowUnavailable = true
			}
		}
		if !sawSlowUnavailable || !sawHealthy {
			t.Fatalf("stalled list after pane removal: slowUnavailable=%v healthy=%v rows=%+v", sawSlowUnavailable, sawHealthy, stalled)
		}

		// 4. Home recovers and emits fresh history row again.
		release()
		waitFor(t, "stalled home recovers after pane removal", func() bool {
			for _, r := range n.sessionList(context.Background(), false) {
				if r.SessionID == slowPublicID && r.Status == proto.StatusOffline {
					return true
				}
			}
			return false
		})
	})

	t.Run("history -> live runtime claim -> home stall -> runtime removal", func(t *testing.T) {
		n := boundedTestNode(t)
		slowRoot := t.TempDir()
		slowHome := canonicalCodexHome(filepath.Join(slowRoot, ".codex"), n.home)
		const slowID = "f4000000-0000-4000-8000-000000000001"
		writeThread(t, slowRoot, slowID, n.home, "claimed by runtime")
		n.configureCodex(proto.CodexConfig{Homes: []string{slowHome}})
		slowPublicID := codexSourceID(slowHome, slowID, n.codexInventory().LegacyHome)

		// 1. Observe thread as healthy history.
		initial := n.codexSessions(map[string]bool{})
		if len(initial) != 1 || initial[0].SessionID != slowPublicID {
			t.Fatalf("initial history = %+v", initial)
		}

		// 2. Live runtime claim suppresses the history row on a healthy pass.
		pass := newCodexListPass()
		pass.runtimes = map[string]codexClaim{slowID: {session: "rt-1", live: true}}
		if rows := n.codexSessionsIn(map[string]bool{}, pass); len(rows) != 0 {
			t.Fatalf("live runtime claim did not suppress history row: %+v", rows)
		}

		// 3. Home stalls and runtime claim is gone: cached unknown-status row must still appear.
		n.codices.index.mu.Lock()
		if s := n.codices.index.homes[slowHome]; s != nil {
			s.fresh = time.Time{}
		}
		n.codices.index.mu.Unlock()
		cache := rolloutCacheFor(slowHome)
		cache.mu.Lock()
		defer cache.mu.Unlock()

		stalled := n.codexSessions(map[string]bool{})
		if len(stalled) != 1 || stalled[0].SessionID != slowPublicID || stalled[0].Status != proto.StatusUnknown ||
			stalled[0].Title != initial[0].Title {
			t.Fatalf("cached snapshot lost after runtime claim: %+v", stalled)
		}
	})

	t.Run("live-first pane preserved when home stalls and pane drops", func(t *testing.T) {
		n := boundedTestNode(t)
		slowRoot := t.TempDir()
		slowHome := canonicalCodexHome(filepath.Join(slowRoot, ".codex"), n.home)
		const slowID = "f5000000-0000-4000-8000-000000000001"
		writeThread(t, slowRoot, slowID, n.home, "live first thread")
		path := rolloutPath(slowHome, slowID)
		n.configureCodex(proto.CodexConfig{Homes: []string{slowHome}})
		slowPublicID := n.registerCodex(slowHome, slowID, path)

		// First observation is exclusively as a live pane (never seen as offline history before).
		n.mu.Lock()
		n.agents = map[string]host.Agent{
			slowPublicID: {Agent: "codex", PaneID: "tmux:%2", Cwd: n.home, AgentStatus: proto.StatusIdle, Title: "live pane"},
		}
		n.mu.Unlock()
		first := n.sessionList(context.Background(), false)
		var liveTitle string
		for _, r := range first {
			if r.SessionID == slowPublicID {
				liveTitle = r.Title
			}
		}
		if liveTitle == "" {
			t.Fatalf("live-first row missing: %+v", first)
		}

		// Home stalls and pane drops from n.agents (as after a pane lookup timeout).
		n.mu.Lock()
		n.agents = map[string]host.Agent{}
		n.mu.Unlock()
		n.codices.index.mu.Lock()
		if s := n.codices.index.homes[slowHome]; s != nil {
			s.fresh = time.Time{}
		}
		n.codices.index.mu.Unlock()
		cache := rolloutCacheFor(slowHome)
		cache.mu.Lock()
		defer cache.mu.Unlock()

		stalled := n.sessionList(context.Background(), false)
		if !slices.ContainsFunc(stalled, func(s proto.Session) bool {
			return s.SessionID == slowPublicID && s.Status == proto.StatusUnknown && s.Title == liveTitle &&
				s.Capabilities != nil && !s.Capabilities.Send && !s.Capabilities.Terminal
		}) {
			t.Fatalf("live-first pane vanished when home stalled and pane dropped: %+v", stalled)
		}
	})
}

// Cold Codex home scans that invoke sqlite3 subprocesses (desktop projects and sidebar threads) can
// exceed the production 100ms per-home scan cutoff under multi-shard CI load. remoteTestNode raises
// its per-node index wait budget so healthy fixtures do not return empty lists, while boundedTestNode
// (and zero-value production codexHomeIndex) still cut off stalled homes at codexHomeScanWait.
func TestCodexIndexCustomWaitBudgetAllowsSlowHealthyScan(t *testing.T) {
	delay := codexHomeScanWait + 40*time.Millisecond

	t.Run("remoteTestNode waits for healthy scan slower than codexHomeScanWait", func(t *testing.T) {
		n := remoteTestNode(t)
		const id = "f8000000-0000-4000-8000-000000000001"
		writeThread(t, n.home, id, n.home, "slow healthy scan")
		home := canonicalCodexHome(n.codexHome(), n.home)
		cache := rolloutCacheFor(home)
		cache.mu.Lock()
		var once sync.Once
		release := func() { once.Do(cache.mu.Unlock) }
		timer := time.AfterFunc(delay, release)
		defer func() { timer.Stop(); release() }()

		rows := n.codexSessions(map[string]bool{})
		if !slices.ContainsFunc(rows, func(s proto.Session) bool { return s.SourceThreadID == id }) {
			t.Fatalf("remoteTestNode missed healthy thread after %v scan delay: %+v", delay, rows)
		}
	})

	t.Run("boundedTestNode keeps production codexHomeScanWait cutoff", func(t *testing.T) {
		n := boundedTestNode(t)
		const id = "f9000000-0000-4000-8000-000000000001"
		writeThread(t, n.home, id, n.home, "stalled scan")
		home := canonicalCodexHome(n.codexHome(), n.home)
		cache := rolloutCacheFor(home)
		cache.mu.Lock()
		released := make(chan struct{})
		var once sync.Once
		release := func() {
			once.Do(func() {
				cache.mu.Unlock()
				close(released)
			})
		}
		defer release()

		if rows := n.codexSessions(map[string]bool{}); len(rows) != 0 {
			t.Fatalf("boundedTestNode did not cut off blocked scan at production budget: %+v", rows)
		}
		release()
		<-released
		waitFor(t, "released bounded home recovers", func() bool {
			return slices.ContainsFunc(n.codexSessions(map[string]bool{}), func(s proto.Session) bool {
				return s.SourceThreadID == id
			})
		})
	})
}
