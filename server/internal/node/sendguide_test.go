package node

import (
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// guidePane is a Claude in w1:p1 that a send types into: after the n-th agent.prompt (counting from 1) the screen is
// the one guideAt gives for it ("" keeps the empty box), Esc on the guide brings the empty box back, and the
// transcript gets the message after the prompts in lands. It returns how many prompts and Esc presses there were.
func guidePane(t *testing.T, f *fakeHerdr, home, cwd string, guideAt func(prompt int) string, lands ...int) (prompts, escs func() int) {
	t.Helper()
	empty := screenFixture(t, "claude-2.1.312-empty-162.txt")
	emptyANSI := screenFixture(t, "claude-2.1.312-empty-162.ansi")
	f.setScreens("w1:p1", empty, emptyANSI)
	var mu sync.Mutex
	np, ne := 0, 0
	f.after(func(c herdrCall) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case c.Method == "agent.prompt":
			np++
			if slices.Contains(lands, np) {
				appendTranscript(t, home, cwd, sidA, promptLine(cwd, str(c.Params["text"]), 0))
			}
			if s := guideAt(np); s != "" {
				f.setScreens("w1:p1", s, s)
			}
		case isKeys("esc")(c):
			ne++
			f.setScreens("w1:p1", empty, emptyANSI)
		}
	})
	return func() int { mu.Lock(); defer mu.Unlock(); return np },
		func() int { mu.Lock(); defer mu.Unlock(); return ne }
}

func guideNode(t *testing.T, transcript bool) (*Node, *fakeHerdr, string, string) {
	t.Helper()
	withSubmitGrace(t, 50*time.Millisecond)
	old := noticeSettle
	noticeSettle = 0
	t.Cleanup(func() { noticeSettle = old })
	n, f, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	seedAgents(t, n, f, claudeAgent(sidA, proto.StatusIdle, "w1:p1", "w1:t1", "w1", cwd, ""))
	if transcript { // an earlier turn: the file is there, without the message being sent
		writeTranscript(t, home, cwd, sidA, time.Now().Add(-time.Hour),
			`{"type":"user","timestamp":"2026-01-01T00:00:00Z","cwd":"`+cwd+`","message":{"role":"user","content":"earlier"}}`)
	}
	return n, f, home, cwd
}

// #5504: Claude Code opened its auto mode setup guide right as the message was submitted and took it. The guide is
// declined with Esc (never Enter), the message typed again into the emptied box, and the send succeeds once the
// transcript has it.
func TestSendRetypesAMessageAGuideTook(t *testing.T) {
	n, f, home, cwd := guideNode(t, true)
	teach := screenFixture(t, "claude-2.1.318-teach-auto-mode-184.txt")
	prompts, escs := guidePane(t, f, home, cwd, func(p int) string {
		if p == 1 {
			return teach
		}
		return ""
	}, 2)
	if _, err := request(t, n, "session.send", map[string]any{"session_id": sidA, "text": "run the voice check"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if prompts() != 2 || escs() != 1 {
		t.Fatalf("prompts %d, esc %d; want 2 and 1", prompts(), escs())
	}
	for _, c := range f.calls() {
		if isKeys("enter")(c) {
			t.Fatalf("pressed Enter on the guide: %v", c)
		}
	}
}

// A send with no guide is as before: one prompt, no Esc, success.
func TestSendWithoutAGuideIsUnchanged(t *testing.T) {
	n, f, home, cwd := guideNode(t, true)
	prompts, escs := guidePane(t, f, home, cwd, func(int) string { return "" }, 1)
	if _, err := request(t, n, "session.send", map[string]any{"session_id": sidA, "text": "run the voice check"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if prompts() != 1 || escs() != 0 {
		t.Fatalf("prompts %d, esc %d; want 1 and 0", prompts(), escs())
	}
}

// The guide came up after the message went in (the transcript has it): it is declined, and nothing is typed again.
func TestSendDeclinesAGuideAfterTheMessageWentIn(t *testing.T) {
	n, f, home, cwd := guideNode(t, true)
	teach := screenFixture(t, "claude-2.1.318-teach-auto-mode-184.txt")
	prompts, escs := guidePane(t, f, home, cwd, func(int) string { return teach }, 1)
	if _, err := request(t, n, "session.send", map[string]any{"session_id": sidA, "text": "run the voice check"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if prompts() != 1 || escs() != 1 {
		t.Fatalf("prompts %d, esc %d; want 1 and 1", prompts(), escs())
	}
}

// Not confirmed: the guide takes the message typed again too, or there is no transcript to tell whether it went in
// (typing it again could run it twice). The send fails with delivered "unknown" instead of answering success.
func TestSendAGuideTookIsNotReportedDelivered(t *testing.T) {
	teach := func(t *testing.T) string { return screenFixture(t, "claude-2.1.318-teach-auto-mode-184.txt") }
	for _, c := range []struct {
		name              string
		transcript        bool
		prompts, escPress int
	}{
		{"taken twice", true, 2, 2},
		{"no transcript", false, 1, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			n, f, home, cwd := guideNode(t, c.transcript)
			s := teach(t)
			prompts, escs := guidePane(t, f, home, cwd, func(int) string { return s })
			_, err := request(t, n, "session.send", map[string]any{"session_id": sidA, "text": "run the voice check"})
			if err == nil {
				t.Fatal("send answered success for a message the guide took")
			}
			if _, data := refusalOf(t, err); data["delivered"] != deliveredUnknown {
				t.Fatalf("delivered %v: %v", data["delivered"], err)
			}
			if prompts() != c.prompts || escs() != c.escPress {
				t.Fatalf("prompts %d, esc %d; want %d and %d", prompts(), escs(), c.prompts, c.escPress)
			}
		})
	}
}

// If a guide retry fails, Attempt 1 was already typed: a late-recorded transcript line confirms delivery ONLY when the
// retry itself is proven not to have typed (e.g. agent_blocked); a retry that may have pasted stays delivered="unknown".
func TestSendGuideRetryBlockedPreservesDeliveredOrUnknown(t *testing.T) {
	teach := screenFixture(t, "claude-2.1.318-teach-auto-mode-184.txt")
	empty := screenFixture(t, "claude-2.1.312-empty-162.txt")
	emptyANSI := screenFixture(t, "claude-2.1.312-empty-162.ansi")
	for _, tc := range []struct {
		name        string
		landedLate  bool
		retryFail   herdrFail
		wantSuccess bool
		wantCode    string
		wantSubstr  string
	}{
		{
			name:        "refused retry with late transcript confirms delivery",
			landedLate:  true,
			retryFail:   herdrFail{Code: "agent_blocked", Message: "interactive input required"},
			wantSuccess: true,
		},
		{
			name:       "refused retry with unseen transcript stays unknown",
			landedLate: false,
			retryFail:  herdrFail{Code: "agent_blocked", Message: "interactive input required"},
			wantCode:   "delivery_unknown",
			wantSubstr: "还不能确认首次发送是否已经送达",
		},
		{
			name:       "uncertain retry failure with late transcript stays unknown",
			landedLate: true,
			retryFail:  herdrFail{Code: "unavailable", Message: "connection lost after paste"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, f, home, cwd := guideNode(t, true)
			f.setScreens("w1:p1", empty, emptyANSI)
			var mu sync.Mutex
			attempt := 0
			f.handle("agent.prompt", func(p map[string]any) any {
				mu.Lock()
				attempt++
				cur := attempt
				mu.Unlock()
				if cur == 1 {
					f.setScreens("w1:p1", teach, teach)
					return map[string]any{}
				}
				if tc.landedLate {
					appendTranscript(t, home, cwd, sidA, promptLine(cwd, str(p["text"]), 0))
				}
				return tc.retryFail
			})
			f.after(func(c herdrCall) {
				if isKeys("esc")(c) {
					f.setScreens("w1:p1", empty, emptyANSI)
				}
			})
			_, err := request(t, n, "session.send", map[string]any{"session_id": sidA, "text": "run the voice check"})
			if tc.wantSuccess {
				if err != nil {
					t.Fatalf("first attempt landed and retry typed nothing, want nil error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected unknown delivery error")
			}
			code, data := refusalOf(t, err)
			if data["delivered"] != deliveredUnknown {
				t.Fatalf("delivered = %v (err=%v), want %q", data["delivered"], err, deliveredUnknown)
			}
			if tc.wantCode != "" && code != tc.wantCode {
				t.Fatalf("code = %q (err=%v), want %q", code, err, tc.wantCode)
			}
			if tc.wantCode == "delivery_unknown" {
				if data["reason"] != "guide_retry_refused" || data["retry_code"] != "dialog_open" || data["dialog"] == "" {
					t.Fatalf("guide_retry_refused structured fields missing: %v", data)
				}
			}
			if tc.wantSubstr != "" && !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("err = %q, want substring %q", err.Error(), tc.wantSubstr)
			}
		})
	}
}
