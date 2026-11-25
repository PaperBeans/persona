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
	n := remoteTestNode(t)
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
	n := remoteTestNode(t)
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
			n := remoteTestNode(t)
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
	n := remoteTestNode(t)
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
