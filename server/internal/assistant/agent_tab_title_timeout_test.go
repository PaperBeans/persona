package assistant

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

// TestAsyncAgentTitleBudgetCoversColdStart locks the #15674 fix: after #15492 the start-naming call
// is fully background, so it must not keep tabName's eight-second synchronous cap. A cold CLI
// launcher plus the first reasoning tokens has been observed to miss eight seconds at ~7.98 s.
// Restoring agentTitleAsyncTimeout to 8 s makes this test red.
func TestAsyncAgentTitleBudgetCoversColdStart(t *testing.T) {
	if agentTitleAsyncTimeout < 25*time.Second {
		t.Fatalf("agentTitleAsyncTimeout = %s, want >= 25s to cover a cold launcher and the first reasoning turn",
			agentTitleAsyncTimeout)
	}
	// The enclosing job must leave room for delayed tab identity and the guarded rename after the
	// model budget is spent.
	if agentTabLaterTimeout < agentTitleAsyncTimeout+5*time.Second {
		t.Fatalf("agentTabLaterTimeout = %s must leave at least 5s after the %s model budget",
			agentTabLaterTimeout, agentTitleAsyncTimeout)
	}
	// Only the detached background job is widened: the request/batch callers keep the short budget.
	if tabNameSyncTimeout != 8*time.Second {
		t.Fatalf("tabNameSyncTimeout = %s, must stay 8s for the synchronous agents.tab_name/autoname paths",
			tabNameSyncTimeout)
	}
}

// TestAsyncAgentTabNameSlowModelRenames is the fast compressed-scale twin of the #15674 incident:
// the fake model answers after the old synchronous cap would have expired but well inside the
// background budget, and the answer still lands through the guarded compare-and-swap rename. The
// test compresses the synchronous cap too, so wiring the background path back through tabName goes
// red. The hold is released by a timer rather than a test sleep, keeping the fixed-sleep ratchet
// count unchanged.
func TestAsyncAgentTabNameSlowModelRenames(t *testing.T) {
	oldSync, oldAsync := tabNameSyncTimeout, agentTitleAsyncTimeout
	tabNameSyncTimeout = 30 * time.Millisecond
	// Generous next to the 2 s entry watchdog: on an oversubscribed -race box the fake handler can be
	// slow to enter, and this compressed budget must not expire before the 120 ms release fires. The
	// test stays fast because the answer (and release) still happens ~120 ms in.
	agentTitleAsyncTimeout = 5 * time.Second
	t.Cleanup(func() {
		tabNameSyncTimeout, agentTitleAsyncTimeout = oldSync, oldAsync
	})

	s, f := newQuietService(t)
	entered, gate := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gate) }) }
	t.Cleanup(release)
	f.tabModelEntered, f.tabModelGate, f.tabReply = entered, gate, "Async Cold Name"

	s.nameAgentTabLater(assistantwire.AgentTabNameStart{MachineID: "box", SessionID: "s1", TabID: "w1:t1",
		ExpectedTabName: "inspect-the-failure", NameFrom: "Inspect the failure"})
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the naming model was not called")
	}
	// Past the compressed 30 ms synchronous cap, far inside the 5 s background budget.
	time.AfterFunc(120*time.Millisecond, release)
	waitAgentTabNaming(t, s)

	f.mu.Lock()
	renames := append([]map[string]any(nil), f.sends...)
	f.mu.Unlock()
	if len(renames) != 1 {
		t.Fatalf("renames = %v, want one guarded rename after the slow model", renames)
	}
	params := renames[0]["rename"].(map[string]any)
	if params["session_id"] != "s1" || params["tab_id"] != "w1:t1" ||
		params["expected_tab_name"] != "inspect-the-failure" || params["tab_name"] != "async-cold-name" {
		t.Fatalf("guarded rename = %v", params)
	}
	if got := s.agentDisplayName("box", proto.Session{SessionID: "s1"}); got != "async-cold-name" {
		t.Fatalf("display title after the guarded rename = %q", got)
	}
}

// TestAsyncAgentTabNameTimeoutSkipsRename checks the silent fallback: a model that misses the
// bounded background budget sends no rename and the tracked goroutine ends. Removing the empty-name
// return makes this test red.
func TestAsyncAgentTabNameTimeoutSkipsRename(t *testing.T) {
	oldAsync, oldJob := agentTitleAsyncTimeout, agentTabLaterTimeout
	agentTitleAsyncTimeout = 60 * time.Millisecond
	agentTabLaterTimeout = 5 * time.Second
	t.Cleanup(func() { agentTitleAsyncTimeout, agentTabLaterTimeout = oldAsync, oldJob })

	s, f := newQuietService(t)
	entered, gate := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gate) }) }
	t.Cleanup(release)
	f.tabModelEntered, f.tabModelGate, f.tabReply = entered, gate, "Never Used"

	guarded := 0
	f.answerNode(assistantwire.DeviceSessionRenameTabIfCurrent, func(_ string, _ json.RawMessage) (any, error) {
		guarded++
		return map[string]any{"ok": true}, nil
	})

	s.nameAgentTabLater(assistantwire.AgentTabNameStart{MachineID: "box", SessionID: "s1", TabID: "w1:t1",
		ExpectedTabName: "inspect-the-failure", NameFrom: "Inspect the failure"})
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the naming model was not called")
	}
	// Never release gate: the model call's own deadline has to end the attempt.
	waitAgentTabNaming(t, s)
	if guarded != 0 {
		t.Fatalf("a timed-out or failed naming call must not rename, got %d guarded rename(s)", guarded)
	}
}
