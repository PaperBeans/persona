package assistant

import (
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/modapi"
	"github.com/nwverse/persona/server/internal/proto"
)

// The push of a routine with facts says the counts Persona worked out, not the first words of the model's answer
// (briefing.go): the same numbers as the card. A conversation that is on no such run is pushed as ever.
func TestBriefRoutinePushSaysTheCounts(t *testing.T) {
	needsBriefingModule(t)
	ft := newFleetTestNoStore(t)
	fastDone(ft)
	ft.s.briefSetSessionRun("s1", &briefRun{Facts: &briefFacts{At: time.Now(), Lang: string(i18n.ZhHans),
		Waiting: make([]modapi.BriefBoardRow, 3), Failed: []modapi.BriefFailure{{ID: "r1"}}, FailedTotal: 1, Accept: make([]modapi.BriefBoardRow, 2)}})
	work := proto.Session{SessionID: "s1", Title: "早报", Cwd: "/code/app", Agent: "claude", Status: proto.StatusWorking, PaneID: "p1",
		UpdatedAt: time.Now()}
	turn := func(session, text string) {
		w := work
		w.SessionID, w.UpdatedAt = session, time.Now()
		ft.sessions("mac", w)
		w.Status, w.LastText, w.UpdatedAt = proto.StatusDone, text, time.Now()
		ft.sessions("mac", w)
	}

	ft.s.noteReportTurn("s1")
	turn("s1", "今天先看登录崩溃的验收，其余都不急。")
	got := ft.pushesOf(assistantwire.NotifyDone, 1, 2*time.Second)
	if len(got) != 1 || got[0].Body != "等你 3 · 没成 1 · 待验收 2" {
		t.Fatalf("the push of the routine: %+v", got)
	}

	ft.s.noteReportTurn("s2")
	turn("s2", "另一个对话的总述")
	got = ft.pushesOf(assistantwire.NotifyDone, 2, 2*time.Second)
	if len(got) != 2 || got[1].Body != "另一个对话的总述" {
		t.Fatalf("a conversation on no run is pushed as before: %+v", got)
	}
}

// Only the turn a scheduled run started says the counts: what the owner types into the routine's conversation later is
// pushed as ever, and a turn that asks a question pushes the question.
func TestBriefRoutinePushLeavesOtherTurnsAlone(t *testing.T) {
	needsBriefingModule(t)
	ft := newFleetTestNoStore(t)
	fastDone(ft)
	ft.s.briefSetSessionRun("s1", &briefRun{Facts: &briefFacts{At: time.Now(), Lang: string(i18n.ZhHans), Waiting: make([]modapi.BriefBoardRow, 3)}})
	turn := func(text string, asks bool) {
		w := proto.Session{SessionID: "s1", Title: "早报", Cwd: "/code/app", Agent: "claude", Status: proto.StatusWorking, PaneID: "p1", UpdatedAt: time.Now()}
		ft.sessions("mac", w)
		w.Status, w.LastText, w.AwaitsReply, w.Asks, w.UpdatedAt = proto.StatusDone, text, asks, text, time.Now()
		ft.sessions("mac", w)
	}

	turn("第二条是什么意思？这是我自己问的。", false) // no run was sent into it: not a report turn
	got := ft.pushesOf(assistantwire.NotifyDone, 1, 2*time.Second)
	if len(got) != 1 || got[0].Body != "第二条是什么意思？这是我自己问的。" {
		t.Fatalf("the owner's own turn is pushed as ever: %+v", got)
	}

	ft.s.noteReportTurn("s1")
	turn("要我先发预发还是直接上线？", true)
	got = ft.pushesOf(assistantwire.NotifyDone, 2, 2*time.Second)
	if len(got) != 2 || got[1].Body != "要我先发预发还是直接上线？" {
		t.Fatalf("a question was replaced by the counts: %+v", got)
	}
}
