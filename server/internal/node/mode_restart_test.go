package node

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// restartTyped is the start command typed into w1:p1 to resume sidA in Allow All, "" when none was.
func restartTyped(f *fakeHerdr) string {
	for _, s := range typedStarts(f, "w1:p1") {
		if strings.Contains(s, "--resume "+sidA) && strings.Contains(s, "--permission-mode "+proto.ModeBypass) {
			return s
		}
	}
	return ""
}

// Allow All for an idle claude with no bypass flag (#6476): not refused, but restarted at once in the same terminal,
// resuming the same conversation on its own launcher with --permission-mode bypassPermissions. No tab is opened or
// closed, and the row is listed as starting meanwhile.
func TestBypassRestartsAnIdleAgentInIt(t *testing.T) {
	n, f, _, cwd, _ := switchNode(t, proto.StatusIdle, nil)
	quitsToShell(f, cwd)
	comesUpWhen(f, "w1:p1", func(typed string) bool { return strings.Contains(typed, "--permission-mode "+proto.ModeBypass) })
	if err := pick(t, n, map[string]any{"permission_mode": proto.ModeBypass}); err != nil {
		t.Fatalf("Allow All refused: %v", err)
	}
	waitFor(t, "the restart in Allow All", func() bool { return restartTyped(f) != "" && n.switches().restartMode(sidA) == "" })
	typed := restartTyped(f)
	if strings.Contains(typed, " launch ") || strings.HasPrefix(typed, "cd ") {
		t.Errorf("not resumed on its own launcher in its folder: %q", typed)
	}
	trace := strings.Join(f.trace("pane.send_keys", "pane.send_input"), "\n")
	if quit, start := strings.Index(trace, sendKeys("w1:p1", "ctrl+c")), strings.Index(trace, "pane.send_input"); quit < 0 || start < quit {
		t.Fatalf("not quit, then started:\n%s", trace)
	}
	if got := f.trace("tab.create", "workspace.create", "tab.close", "pane.close"); len(got) != 0 {
		t.Errorf("a tab was opened or closed: %q", got)
	}
	if got := f.trace("pane.send_keys"); strings.Contains(strings.Join(got, ""), "shift+tab") {
		t.Errorf("pressed shift+tab, which never reaches Allow All here: %q", got)
	}
	waitFor(t, "the row out of starting", func() bool { return !n.switches().inFlight(sidA) })
	if mode, _ := n.modesOf(sidA); mode != proto.ModeBypass {
		t.Errorf("mode after the restart = %q", mode)
	}
}

// While the agent works the restart waits: nothing is pressed or typed (the running tool call goes on), the row says
// the restart waits, and once the turn is over the session list starts it.
func TestBypassRestartWaitsForTheTurnToEnd(t *testing.T) {
	n, f, _, cwd, _ := switchNode(t, proto.StatusWorking, nil)
	f.reset()
	if err := pick(t, n, map[string]any{"permission_mode": proto.ModeBypass}); err != nil {
		t.Fatalf("Allow All refused: %v", err)
	}
	row := rowOf(t, n, sidA)
	if !row.BypassPending {
		t.Fatal("the row does not say a restart waits")
	}
	// the try queueModeRestart started at once finds the agent busy and puts the restart off
	waitFor(t, "the first try put off", func() bool {
		sh := n.switches()
		sh.mu.Lock()
		defer sh.mu.Unlock()
		r := sh.restarts[sidA]
		return r != nil && !r.applying && !r.retryAt.IsZero()
	})
	if got := drove(f); len(got) != 0 {
		t.Fatalf("drove a working agent: %q", got)
	}
	// The turn ends.
	quitsToShell(f, cwd)
	comesUpWhen(f, "w1:p1", func(string) bool { return true })
	f.reply("session.snapshot", snapshot(nil, nil, []any{claudeAgent(sidA, proto.StatusIdle, "w1:p1", "w1:t1", "w1", cwd, "")}, nil))
	sh := n.switches()
	sh.mu.Lock()
	if r := sh.restarts[sidA]; r != nil {
		r.retryAt = time.Time{} // no backoff wait in a test
	}
	sh.mu.Unlock()
	waitFor(t, "the restart once idle", func() bool {
		listSessions(t, n)
		return restartTyped(f) != ""
	})
}

// Picking another mode before the restart ran drops it: the agent keeps running, switched with shift+tab as always.
func TestAnotherModeDropsAWaitingRestart(t *testing.T) {
	n, f, _, _, _ := switchNode(t, proto.StatusWorking, nil)
	if err := pick(t, n, map[string]any{"permission_mode": proto.ModeBypass}); err != nil {
		t.Fatal(err)
	}
	if n.switches().restartMode(sidA) != proto.ModeBypass {
		t.Fatal("no restart waits")
	}
	cycleModes(t, f, "w1:p1", "default")
	if err := pick(t, n, map[string]any{"permission_mode": "auto"}); err != nil {
		t.Fatal(err)
	}
	if n.switches().restartMode(sidA) != "" || rowOf(t, n, sidA).BypassPending {
		t.Error("the restart still waits")
	}
	if restartTyped(f) != "" {
		t.Error("restarted anyway")
	}
}

// A claude started with a bypass flag reaches Allow All with shift+tab: no restart.
func TestBypassWithTheFlagNeedsNoRestart(t *testing.T) {
	n, _, _, _, _ := switchNode(t, proto.StatusIdle, nil)
	if !n.needsModeRestart(sidA, proto.ModeBypass) {
		t.Error("a claude without the flag needs no restart")
	}
	n.hooks = &hookTurns{}
	n.hooks.mu.Lock()
	n.hooks.entry(sidA, time.Now()).bypass = true
	n.hooks.mu.Unlock()
	for _, mode := range []string{proto.ModeBypass, "auto", "plan", "acceptEdits", "default"} {
		if n.needsModeRestart(sidA, mode) {
			t.Errorf("%s needs a restart", mode)
		}
	}
}

// A restart that cannot go now is put off, longer each time; done or failed drops it; one picked too long ago goes.
func TestModeRestartBackoff(t *testing.T) {
	var s switchShare
	now := time.Now()
	s.setRestart("x", proto.ModeBypass, now)
	if s.takeRestart("x", now) != proto.ModeBypass {
		t.Fatal("not due")
	}
	if s.takeRestart("x", now) != "" {
		t.Fatal("taken twice")
	}
	s.settleRestart("x", proto.ModeBypass, errRestartLater, now)
	if s.takeRestart("x", now.Add(modeRestartRetry-time.Millisecond)) != "" {
		t.Fatal("tried again before its retry")
	}
	if s.takeRestart("x", now.Add(modeRestartRetry)) == "" {
		t.Fatal("not tried again after its retry")
	}
	s.settleRestart("x", proto.ModeBypass, errRestartLater, now)
	if s.takeRestart("x", now.Add(modeRestartRetry)) != "" {
		t.Fatal("the second wait is not longer")
	}
	for range 10 {
		s.restarts["x"].retryAt = time.Time{}
		s.takeRestart("x", now)
		s.settleRestart("x", proto.ModeBypass, errRestartLater, now)
	}
	if d := s.restarts["x"].delay; d != modeRestartMaxRetry {
		t.Errorf("delay grew to %v", d)
	}
	s.restarts["x"].retryAt = time.Time{}
	s.takeRestart("x", now)
	s.settleRestart("x", proto.ModeBypass, errors.New("did not come up"), now)
	if s.restartMode("x") != "" {
		t.Error("a failed restart still waits")
	}
	s.setRestart("y", proto.ModeBypass, now.Add(-switchMaxAge-time.Minute))
	if s.takeRestart("y", now) != "" || s.restartMode("y") != "" {
		t.Error("a restart picked over a day ago still runs")
	}
}
