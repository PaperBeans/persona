package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// profileProbeLauncher writes a ~/.zshrc whose launcher work runs claude in profile, counting every time the shell
// runs it (the probe does, in a subshell with claude stubbed; a start does too) in the file it returns.
func profileProbeLauncher(t *testing.T, profile string) string {
	t.Helper()
	home := os.Getenv("HOME")
	runs := filepath.Join(home, "work-runs")
	writeFile(t, filepath.Join(home, ".zshrc"), `function work { print -r -- run >> `+shellQuote(runs)+`; CLAUDE_CONFIG_DIR=`+
		shellQuote(profile)+` command claude "$@"; }`+"\n")
	return runs
}

// settleFile dates path back past profileProbeSettle, as a ~/.zshrc nobody is editing.
func settleFile(t *testing.T, path string) {
	t.Helper()
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

func launcherRuns(t *testing.T, runs string) int {
	t.Helper()
	b, _ := os.ReadFile(runs)
	return strings.Count(string(b), "run\n")
}

// A message to a running assistant conversation does not run its shell launcher again: the profile check reuses the
// probe of the message before it (#8947). Through the RPC boundary, as the hub sends.
func TestAssistantMessageDoesNotProbeTheLauncherEachTime(t *testing.T) {
	f := sdkTest(t)
	n := f.n
	n.home = os.Getenv("HOME")
	profile := t.TempDir()
	runs := profileProbeLauncher(t, profile)
	settleFile(t, filepath.Join(n.home, ".zshrc"))
	cwd := filepath.Join(n.dir, "assistant")
	answer, err := nodeCall(t, n, "assistant.send", map[string]any{"launcher": "work", "cwd": cwd, "text": "hello", "model_fingerprint": "probe-test"})
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(answer, &res); err != nil || res.SessionID == "" {
		t.Fatalf("send: %s (%v)", answer, err)
	}
	f.idle(t, res.SessionID)
	if st, _ := n.state.get(res.SessionID); st.ConfigDir != profile {
		t.Fatalf("the conversation did not start in its launcher's profile: %q, want %q", st.ConfigDir, profile)
	}
	before := launcherRuns(t, runs)
	for _, text := range []string{"again", "and again"} {
		if _, err := nodeCall(t, n, "session.send", map[string]any{"session_id": res.SessionID, "text": text}); err != nil {
			t.Fatal(err)
		}
		f.idle(t, res.SessionID)
	}
	if got := launcherRuns(t, runs) - before; got != 0 {
		t.Fatalf("two messages to a running conversation ran its launcher %d more times; want none (one login shell each is a second per message)", got)
	}
}

// The check still holds: a launcher edited to set another config directory is refused on the very next message, not
// after the cached probe ages out, and a launcher that sets the conversation's own directory again is let through.
func TestAssistantMessageSeesAnEditedLauncherAtOnce(t *testing.T) {
	f := sdkTest(t)
	n := f.n
	n.home = os.Getenv("HOME")
	profile := t.TempDir()
	profileProbeLauncher(t, profile)
	settleFile(t, filepath.Join(n.home, ".zshrc"))
	cwd := filepath.Join(n.dir, "assistant")
	answer, err := nodeCall(t, n, "assistant.send", map[string]any{"launcher": "work", "cwd": cwd, "text": "hello", "model_fingerprint": "probe-test"})
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(answer, &res); err != nil || res.SessionID == "" {
		t.Fatalf("send: %s (%v)", answer, err)
	}
	f.idle(t, res.SessionID)
	profileProbeLauncher(t, t.TempDir()) // the user points the launcher at another profile, the same length of text
	_, err = nodeCall(t, n, "session.send", map[string]any{"session_id": res.SessionID, "text": "after the edit"})
	if err == nil || !strings.Contains(err.Error(), "conflicts with the launcher's own profile") {
		t.Fatalf("a launcher moved to another profile was not refused on the next message: %v", err)
	}
	profileProbeLauncher(t, profile) // and back
	if _, err := nodeCall(t, n, "session.send", map[string]any{"session_id": res.SessionID, "text": "back"}); err != nil {
		t.Fatalf("the launcher's own profile again was refused: %v", err)
	}
}

// What the stamp cannot see (a file the startup files source) is seen once the probe is profileProbeFresh old, and a
// failed probe is tried again after profileProbeRetry.
func TestAssistantLauncherProfileProbeAgesOut(t *testing.T) {
	f := sdkTest(t)
	n := f.n
	n.home = os.Getenv("HOME")
	sourced := filepath.Join(n.home, "work.zsh")
	first, second := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(n.home, ".zshrc"), "source "+shellQuote(sourced)+"\n")
	settleFile(t, filepath.Join(n.home, ".zshrc"))
	define := func(profile string) {
		writeFile(t, sourced, `function work { CLAUDE_CONFIG_DIR=`+shellQuote(profile)+` command claude "$@"; }`+"\n")
	}
	age := func(by time.Duration) {
		profileProbes.Lock()
		defer profileProbes.Unlock()
		c := profileProbes.got[n.home+"\x00work"]
		c.at = c.at.Add(-by)
		profileProbes.got[n.home+"\x00work"] = c
	}
	ctx := context.Background()
	if got := n.assistantLauncherProfile(ctx, "work", ""); got != "" {
		t.Fatalf("a launcher that is not defined yet has profile %q", got)
	}
	define(first)
	if got := n.assistantLauncherProfile(ctx, "work", ""); got != "" {
		t.Fatalf("a failed probe was tried again before profileProbeRetry: %q", got)
	}
	age(profileProbeRetry)
	if got := n.assistantLauncherProfile(ctx, "work", ""); got != first {
		t.Fatalf("a failed probe was not tried again after profileProbeRetry: %q, want %q", got, first)
	}
	define(second)
	if got := n.assistantLauncherProfile(ctx, "work", ""); got != first {
		t.Fatalf("a probe younger than profileProbeFresh was taken again for an unwatched file: %q", got)
	}
	age(profileProbeFresh)
	if got := n.assistantLauncherProfile(ctx, "work", ""); got != second {
		t.Fatalf("a probe older than profileProbeFresh was reused: %q, want %q", got, second)
	}
}
