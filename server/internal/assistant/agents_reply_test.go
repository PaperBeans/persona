package assistant

import (
	"context"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/closeout"
	"github.com/nwverse/persona/server/internal/proto"
)

// 待你回复 (#5096): which stopped agents wait on the user's answer, and the manager's one reminder of those the user
// looked at and left.

func TestAwaitsReply(t *testing.T) {
	asking := proto.Session{SessionID: "s1", Status: proto.StatusDone, State: proto.StateIdle, Asks: "要推到 beta 吗？"}
	for _, c := range []struct {
		name string
		edit func(*proto.Session)
		want bool
	}{
		{"seen question", func(*proto.Session) {}, true},
		{"unread question", func(s *proto.Session) { s.State = proto.StateUnread }, true},
		{"no question", func(s *proto.Session) { s.Asks = "" }, false},
		{"working", func(s *proto.Session) { s.State = proto.StateWorking }, false},
		{"needs you (a card)", func(s *proto.Session) { s.State, s.PendingInteractions = proto.StateNeedsYou, 1 }, false},
		{"model error", func(s *proto.Session) { s.TurnError = "API Error: 529" }, false},
		{"loop", func(s *proto.Session) { s.Loop = &proto.LoopInfo{} }, false},
		{"background", func(s *proto.Session) { s.Background = []proto.BackgroundTask{{ID: "b1"}} }, false},
		{"assistant", func(s *proto.Session) { s.Assistant = true }, false},
		{"triage done", func(s *proto.Session) { s.Work = &proto.WorkState{Phase: proto.WorkDone} }, false},
		{"triage waits on the user, no words", func(s *proto.Session) {
			s.Asks, s.Work = "", &proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitUser, Line: "等你选方案"}
		}, true},
		{"put off on its card", func(s *proto.Session) {
			s.Work = &proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitUser, Line: "你说先不管", Deferred: true}
		}, false},
		{"triage waits on CI", func(s *proto.Session) {
			s.Work = &proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitExternal}
		}, false},
	} {
		s := asking
		c.edit(&s)
		if got := awaitsReply(s); got != c.want {
			t.Errorf("%s: awaitsReply = %v, want %v", c.name, got, c.want)
		}
	}
}

// An older node sends no asks: the list's last words stand in when they are the whole reply, not when they were cut.
func TestDecorateAwaitsReadsShortLastText(t *testing.T) {
	s := proto.Session{Status: proto.StatusDone, State: proto.StateIdle, LastText: "改好了。要合并吗？"}
	decorateAwaits(&s, askReader{})
	if !s.AwaitsReply || s.Asks != "要合并吗？" {
		t.Fatalf("short reply: awaits %v asks %q", s.AwaitsReply, s.Asks)
	}
	s = proto.Session{Status: proto.StatusDone, State: proto.StateIdle, LastText: "要合并吗？还有很多…"}
	if decorateAwaits(&s, askReader{}); s.AwaitsReply {
		t.Fatal("a cut reply's beginning is not its close")
	}
	// A node that read the whole reply and found nothing (the user answered after it) is taken at its word.
	s = proto.Session{Status: proto.StatusDone, State: proto.StateIdle, LastText: "改好了。要合并吗？", AsksRead: true}
	if decorateAwaits(&s, askReader{}); s.AwaitsReply || s.Asks != "" {
		t.Fatalf("read by the node: awaits %v asks %q", s.AwaitsReply, s.Asks)
	}
}

// A conversational agent ends every turn on a question (#4110). When its closeout says the turn is done and needs
// nobody, the turn does not await the user's reply, whether the node read the reply (asks) or the manager reads a
// whole last_text itself; the same turn without that closeout still does; a card the agent put up with an ask tool is
// Needs You either way.
func TestAwaitsReplyHonoursCloseout(t *testing.T) {
	const question = "你最在意哪一点？"
	plain := "三个方向都列在上面了。" + question
	settled := "三个方向都列在上面了。<closeout status=\"done\" merged=\"n/a\" deployed=\"n/a\"></closeout> " + question
	for _, c := range []struct {
		name  string
		reply string
		want  bool
	}{
		{"no closeout", plain, true},
		{"closeout: done, needs nobody", settled, false},
		{"closeout: blocked on the user", "三个方向都列在上面了。<closeout status=\"blocked\"> waiting: 你选一个 </closeout> " + question, true},
	} {
		// Read by the node off the whole reply.
		s := proto.Session{Status: proto.StatusDone, State: proto.StateIdle, LastText: c.reply, Asks: closeout.AskOf(c.reply), AsksRead: true}
		if decorateAwaits(&s, askReader{}); s.AwaitsReply != c.want {
			t.Errorf("%s, read by the node: awaits %v asks %q, want %v", c.name, s.AwaitsReply, s.Asks, c.want)
		}
		// An older node: the manager reads the whole last_text.
		s = proto.Session{Status: proto.StatusDone, State: proto.StateUnread, LastText: c.reply}
		if decorateAwaits(&s, askReader{}); s.AwaitsReply != c.want {
			t.Errorf("%s, read by the manager: awaits %v asks %q, want %v", c.name, s.AwaitsReply, s.Asks, c.want)
		}
	}
	// A real ask tool: its card is pending and the session Needs You, with or without the closeout.
	for _, reply := range []string{plain, settled} {
		s := proto.Session{Status: proto.StatusBlocked, State: proto.StateNeedsYou, PendingInteractions: 1, LastText: reply,
			Asks: closeout.AskOf(reply), AsksRead: true}
		decorateAwaits(&s, askReader{})
		if s.State != proto.StateNeedsYou || s.PendingInteractions != 1 || s.AwaitsReply {
			t.Errorf("card pending, reply %q: state %s pending %d awaits %v; want Needs You from the card alone", reply, s.State, s.PendingInteractions, s.AwaitsReply)
		}
	}
}

func TestReplyReminder(t *testing.T) {
	ft := newFleetTest(t)
	ended := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
	asks := func(id string) proto.Session {
		return proto.Session{SessionID: id, Title: "修登录", Cwd: "/code/app", Status: proto.StatusDone, UpdatedAt: ended,
			LastText: "改好了，要推到 beta 吗？", Asks: "要推到 beta 吗？"}
	}
	done := asks("s3")
	done.Asks, done.LastText = "", "改好了，测试都过了。很长很长……" // asks nothing
	ft.sessions("mac", asks("s1"), asks("s2"), done)
	ctx := context.Background()
	seenAgo := func(id string, ago time.Duration) {
		ft.s.markSeen(ctx, "mac", id)
		ft.s.fleet.mu.Lock()
		ft.s.fleet.seen[[2]string{"mac", id}] = time.Now().Add(-ago)
		ft.s.fleet.mu.Unlock()
	}
	// s1 was looked at long ago: the first look after a start takes it as reminded (no burst after a restart).
	seenAgo("s1", 40*time.Minute)
	seenAgo("s3", 40*time.Minute)
	ft.s.replyTick(time.Now(), replyAfter)
	if got := ft.pushesOf(assistantwire.NotifyReply, 1, 50*time.Millisecond); len(got) != 0 {
		t.Fatalf("first look pushed %d", len(got))
	}
	// s2 is looked at now: nothing before its 30 minutes, one reminder after them.
	seenAgo("s2", time.Minute)
	ft.s.replyTick(time.Now(), replyAfter)
	if got := ft.pushesOf(assistantwire.NotifyReply, 1, 50*time.Millisecond); len(got) != 0 {
		t.Fatalf("reminded before the time: %d", len(got))
	}
	seenAgo("s2", 31*time.Minute)
	ft.s.replyTick(time.Now(), replyAfter)
	got := ft.pushesOf(assistantwire.NotifyReply, 1, time.Second)
	if len(got) != 1 || got[0].SessionID != "s2" || got[0].Body != "要推到 beta 吗？" || got[0].DeviceID != "mac" {
		t.Fatalf("reminders = %+v, want one for s2 with its question", got)
	}
	if got[0].Title != "修登录 还在等你回复" {
		t.Fatalf("title = %q", got[0].Title)
	}

	// Once per question: also when its transcript was touched after the turn (UpdatedAt moved, the same words).
	ft.s.replyTick(time.Now(), replyAfter)
	touched := asks("s2")
	touched.UpdatedAt = ended.Add(30 * time.Second)
	ft.sessions("mac", asks("s1"), touched, done)
	seenAgo("s2", 31*time.Minute)
	ft.s.replyTick(time.Now(), replyAfter)
	if got := ft.pushesOf(assistantwire.NotifyReply, 2, 100*time.Millisecond); len(got) != 1 {
		t.Fatalf("reminded again: %d", len(got))
	}
	// Muted: no reminder, even of a new turn.
	s1 := asks("s1")
	s1.UpdatedAt = ended.Add(time.Minute)
	ft.sessions("mac", s1, asks("s2"), done)
	seenAgo("s1", 31*time.Minute)
	ft.s.fleet.mutesMu.Lock()
	ft.s.fleet.mutes = &Pins{Sessions: []string{"mac/s1"}}
	ft.s.fleet.mutesMu.Unlock()
	ft.s.replyTick(time.Now(), replyAfter)
	if got := ft.pushesOf(assistantwire.NotifyReply, 2, 100*time.Millisecond); len(got) != 1 {
		t.Fatalf("a muted agent was reminded of: %d", len(got))
	}
}

func TestReplyReminderSwitch(t *testing.T) {
	off := false
	for _, c := range []struct {
		sw    TriageSwitches
		on    bool
		after time.Duration
	}{
		{TriageSwitches{}, true, 30 * time.Minute},
		{TriageSwitches{ReplyReminder: &off}, false, 30 * time.Minute},
		{TriageSwitches{ReplyReminderMinutes: 90}, true, 90 * time.Minute},
	} {
		if on, after := c.sw.replyOn(); on != c.on || after != c.after {
			t.Errorf("%+v: replyOn = %v %v", c.sw, on, after)
		}
	}
	// An app that does not know the switch leaves it as it was.
	s, _ := workService(t)
	if w := serve(s, "PUT", "/api/v1/agents/triage", `{"enabled":true,"reply_reminder":false,"reply_reminder_minutes":60}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := serve(s, "PUT", "/api/v1/agents/triage", `{"enabled":false}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	sw := s.triageSwitches(context.Background())
	if sw.ReplyReminder == nil || *sw.ReplyReminder || sw.ReplyReminderMinutes != 60 || sw.Enabled {
		t.Fatalf("switches = %+v", sw)
	}
}

// Turned off and on again, what waited meanwhile is not pushed at once; a session that worked and asks the same words
// again is a new question.
func TestReplyReminderAfterTheSwitchAndANewTurn(t *testing.T) {
	ft := newFleetTest(t)
	ended := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
	q := proto.Session{SessionID: "s1", Title: "修登录", Cwd: "/code/app", Status: proto.StatusDone, UpdatedAt: ended,
		Asks: "要推到 beta 吗？", AsksRead: true}
	ft.sessions("mac", q)
	ctx := context.Background()
	seenAgo := func(ago time.Duration) {
		ft.s.markSeen(ctx, "mac", "s1")
		ft.s.fleet.mu.Lock()
		ft.s.fleet.seen[[2]string{"mac", "s1"}] = time.Now().Add(-ago)
		ft.s.fleet.mu.Unlock()
	}
	seenAgo(time.Minute)
	ft.s.replyTick(time.Now(), replyAfter) // primes
	ft.s.replyOff()
	seenAgo(40 * time.Minute)
	ft.s.replyTick(time.Now(), replyAfter) // on again: primes again
	if got := ft.pushesOf(assistantwire.NotifyReply, 1, 50*time.Millisecond); len(got) != 0 {
		t.Fatalf("turned on again, pushed %d at once", len(got))
	}
	// It works (the user answered), then asks the same words again in a new turn.
	w := q
	w.Status = proto.StatusWorking
	ft.sessions("mac", w)
	ft.s.replyTick(time.Now(), replyAfter)
	again := q
	again.UpdatedAt = time.Now().Add(-time.Hour).UTC()
	ft.sessions("mac", again)
	seenAgo(31 * time.Minute)
	ft.s.replyTick(time.Now(), replyAfter)
	if got := ft.pushesOf(assistantwire.NotifyReply, 1, time.Second); len(got) != 1 {
		t.Fatalf("the new turn's question was not reminded of: %d", len(got))
	}
}
