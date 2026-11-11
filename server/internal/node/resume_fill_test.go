package node

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// A start that resumes a Claude session that is not running (the app's Resume Session, #4328) keeps what the session
// ran with for what it leaves out, as a message that resumes it does: the launcher, the model picked for that
// launcher, the directory, the modes and the title as the tab's name. What the start says wins, and the model stays
// behind when the start names another launcher. A running session keeps what was asked (start answers it as running).
func TestFillResumeKeepsWhatTheSessionRanWith(t *testing.T) {
	n, f, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	writeTranscript(t, home, cwd, sidA, time.Now().Add(-time.Hour),
		`{"type":"user","cwd":"`+cwd+`","message":{"role":"user","content":"hi"},"uuid":"u1"}`,
		`{"type":"ai-title","aiTitle":"Relay setup","sessionId":"`+sidA+`"}`)
	n.state.put(sidA, sessionState{Launcher: "cc", Cwd: cwd, Model: "opus", ModelFingerprint: "fp1"})
	n.noteModes(sidA, "plan", "high")

	got := n.fillResume(sidA, resumed{})
	if got.launcher != "cc" || got.cwd != cwd || got.mode != "plan" || got.effort != "high" || got.tab != "Relay setup" {
		t.Errorf("filled = %+v", got)
	}
	if got.execution.Model != "opus" || got.execution.Fingerprint != "fp1" || got.execution.UseEffort {
		t.Errorf("execution = %+v", got.execution)
	}

	asked := resumed{cwd: "/elsewhere", launcher: "cc", mode: "default", effort: "low", tab: "mine",
		execution: modelExecution{Model: "sonnet"}}
	if got := n.fillResume(sidA, asked); got != asked {
		t.Errorf("what the start says = %+v, want %+v", got, asked)
	}
	if got := n.fillResume(sidA, resumed{launcher: "other"}); got.launcher != "other" || got.execution.Model != "" || got.execution.Fingerprint != "" {
		t.Errorf("another launcher = %+v", got)
	}

	f.reply("session.snapshot", snapshot(nil, nil, []any{claudeAgent(sidA, proto.StatusIdle, "w1:p1", "w1:t1", "w1", cwd, "")}, nil))
	listSessions(t, n)
	if got := n.fillResume(sidA, resumed{}); got != (resumed{}) {
		t.Errorf("running = %+v", got)
	}
}
