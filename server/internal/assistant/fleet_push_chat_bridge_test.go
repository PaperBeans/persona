package assistant

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/sessionorigin"
)

// bridgeSession is supAgentSession hosted in a Lark chat bridge: its pane and transport are those of the first
// "feishu" row of the origin rule's shared fixture, so the tests follow what sessionorigin.Of calls a chat bridge.
func bridgeSession(t *testing.T) proto.Session {
	t.Helper()
	raw, err := os.ReadFile("../sessionorigin/testdata/pane_sources.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Want    string        `json:"want"`
		Session proto.Session `json:"session"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Want != "feishu" {
			continue
		}
		sess := supAgentSession()
		sess.PaneID, sess.SourceKind = r.Session.PaneID, r.Session.SourceKind
		if got := sessionorigin.Of(sess); got != "feishu" {
			t.Fatalf("fixture session's origin = %q, want feishu", got)
		}
		return sess
	}
	t.Fatal("no feishu row in the origin fixture")
	return proto.Session{}
}

// finishOnce drives one finished turn of sess (supAgent on mac) and decides it at once.
func (e *supervisedEnv) finishOnce(sess proto.Session) {
	e.t.Helper()
	e.holdDone()
	driveAgentDone(e.fleetTest, sess)
	e.finishNow()
}

// A session a Lark chat bridge hosts answers in the topic it was asked in (#8592): its normal finish sends neither the
// phone's "finished" push nor Persona's own Lark card, every turn, on Claude Code and Codex alike. The Agents list
// still has it finished and unseen. Before the fix each of these pushed and posted a card.
func TestChatBridgeNormalFinishIsNotPushed(t *testing.T) {
	for _, agent := range []string{"claude", "", "codex"} {
		t.Run("agent="+agent, func(t *testing.T) {
			e := newSupervisedEnv(t, true)
			for _, last := range []string{"改好了", "又改了一处"} {
				sess := bridgeSession(t)
				sess.Agent, sess.LastText = agent, last
				e.finishOnce(sess)
			}
			if got := e.agentDoneCards(); len(got) != 0 {
				t.Fatalf("posted %d finished cards, want none: %+v", len(got), got)
			}
			if got := e.donePushes(); len(got) != 0 {
				t.Fatalf("pushed %d finished pushes, want none: %+v", len(got), got)
			}
			if got := e.logged("kind=agent_done"); len(got) != 0 {
				t.Fatalf("logged a finished card sent: %q", got)
			}
			sess, ok := e.s.decoratedSession("mac", supAgent)
			if !ok {
				t.Fatal("the session is not listed")
			}
			if got := agentState(sess, e.s.seenAt("mac", supAgent)); got != proto.StateUnread {
				t.Fatalf("Agents state = %q, want %q (finished, unseen)", got, proto.StateUnread)
			}
		})
	}
}

// What did not end normally is pushed and carded as ever from a chat bridge too: a model error, a cut-off answer, a
// usage limit nothing takes up, a stop the host reports, a stuck turn, and an agent that does not tell its failures.
// A terminal session's normal finish is pushed as before.
func TestChatBridgeFailedFinishAndTerminalStillPushed(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*proto.Session)
	}{
		{"model error", func(s *proto.Session) { s.TurnError = "exit status 1" }},
		{"cut off", func(s *proto.Session) { s.LastText = "API Error: 529 overloaded" }},
		{"usage limit", func(s *proto.Session) {
			s.TurnError, s.TurnErrorKind = "You've hit your limit", proto.TurnErrorUsageLimit
		}},
		{"host failure", func(s *proto.Session) { s.FailureReason = "the host stopped on an error" }},
		{"stuck", func(s *proto.Session) {
			s.Work = &proto.WorkState{Phase: proto.WorkBlocked, Line: "等数据库权限"}
		}},
		{"another agent", func(s *proto.Session) { s.Agent = "dsh" }},
		{"terminal session", func(s *proto.Session) { *s = supAgentSession() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newSupervisedEnv(t, true)
			fastDone(e.fleetTest)
			sess := bridgeSession(t)
			tc.edit(&sess)
			driveAgentDone(e.fleetTest, sess)
			waitFor(t, "the finished card", func() bool { return len(e.agentDoneCards()) == 1 })
			if got := e.donePushes(); len(got) != 1 {
				t.Fatalf("pushed %d finished pushes, want 1: %+v", len(got), got)
			}
			waitFor(t, "the line saying it was sent", func() bool {
				return len(e.logged("bots: card sent: kind=agent_done machine=mac session="+supAgent)) == 1
			})
		})
	}
}
