package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/proto"
)

// endHub is the Feishu fake with a run that already shows its progress card, and the fake hub to set its
// conversation on.
func endHub(t *testing.T) (*Service, *fakeHub, *channel.Run, *[]map[string]any) {
	t.Helper()
	f, posts := feishuHub("ou_a", time.Unix(100, 0))
	f.answerApp(AppFeishu, FeishuReact, func(json.RawMessage) (any, error) { return map[string]any{}, nil })
	f.answerApp(AppFeishu, FeishuUnreact, func(json.RawMessage) (any, error) { return map[string]any{}, nil })
	s := newTestService(t, f)
	r, _ := cotRun(s)
	feishuSink{s}.progress(context.Background(), r, time.Second, []string{"运行 `ls`"}, "")
	return s, f, r, posts
}

// lastCard is the card the run's progress card was last rewritten to.
func lastCard(t *testing.T, r *channel.Run, posts []map[string]any) string {
	t.Helper()
	last := posts[len(posts)-1]
	if last["update"] != true || last["message_id"] != r.Ext["card"] {
		t.Fatalf("the progress card was not rewritten: %v", last)
	}
	card, _ := last["card"].(string)
	return card
}

// A normal end: the answer replaces the card, its last line says done, the time and the tools called.
func TestFeishuEndDoneFooter(t *testing.T) {
	s, _, r, posts := endHub(t)
	s.finishChannelRun(r, &channel.Result{Answer: "结论在这里"})
	card := lastCard(t, r, *posts)
	if len(*posts) != 2 || !strings.Contains(card, "完成 · Persona · 用时") || !strings.Contains(card, "调用了 1 次工具") ||
		!strings.Contains(card, "已调用 1 次工具") || strings.Contains(card, "处理中") || strings.Contains(card, `"template"`) {
		t.Fatalf("card %s (posts %d)", card, len(*posts))
	}
}

// Ends that are not normal leave the card in a final state with a header saying so, never "Working".
func TestFeishuEndStates(t *testing.T) {
	for _, tc := range []struct {
		res  *channel.Result
		want string
	}{
		{&channel.Result{End: channel.EndStopped, Answer: "做了一半"}, "已中断"},
		{&channel.Result{End: channel.EndError, Stopped: "API 错误：overloaded"}, "出错了"},
		{&channel.Result{End: channel.EndOffline, Answer: "写了一半"}, "电脑离线"},
		{&channel.Result{End: channel.EndGone, Note: "这段对话不见了（被关掉或删掉了）。"}, "对话已关闭"},
	} {
		s, _, r, posts := endHub(t)
		s.finishChannelRun(r, tc.res)
		card := lastCard(t, r, *posts)
		if !strings.Contains(card, `"template"`) || !strings.Contains(card, tc.want) || strings.Contains(card, "处理中") || strings.Contains(card, "完成 ·") {
			t.Fatalf("%s: card %s", tc.want, card)
		}
	}
}

// checkEnd sets the run's conversation to sess and items, looks at it once, and returns the card it ended on.
func checkEnd(t *testing.T, s *Service, f *fakeHub, r *channel.Run, posts *[]map[string]any, sess proto.Session, items ...proto.Item) string {
	t.Helper()
	sess.MachineID, sess.SessionID = "server", "c1"
	f.setSessions("server", sess)
	f.setItems(items...)
	s.checkChannelRun(r.ID)
	if run, _ := s.channelRunByID(r.ID); run != nil {
		t.Fatalf("the run did not end")
	}
	return lastCard(t, r, *posts)
}

// The conversation's turn error makes the end "Error"; an answer that only talks about an error is still "Done".
func TestFeishuCheckEndFailed(t *testing.T) {
	answer := proto.Item{Seq: 5, Role: "assistant", Kind: proto.KindText, Text: "修好了 API Error: 503 时的崩溃"}
	s, f, r, posts := endHub(t)
	s.setRunSeen(r)
	card := checkEnd(t, s, f, r, posts, proto.Session{Status: proto.StatusDone, State: proto.StateIdle, TurnError: "unexpected status 503"}, answer)
	if !strings.Contains(card, `"template"`) || !strings.Contains(card, "出错了") {
		t.Fatalf("failed: card %s", card)
	}
	s, f, r, posts = endHub(t)
	s.setRunSeen(r)
	card = checkEnd(t, s, f, r, posts, proto.Session{Status: proto.StatusDone, State: proto.StateIdle, LastText: answer.Text}, answer)
	if strings.Contains(card, `"template"`) || strings.Contains(card, "出错了") || !strings.Contains(card, "完成 · ") {
		t.Fatalf("mentions an error: card %s", card)
	}
}

// A held run whose background work never reported back ends as timed out in the card it still has: the card is
// rewritten with the note, not left saying it is working.
func TestFeishuHeldRunGivesUpOnTheCard(t *testing.T) {
	s, f, r, posts := endHub(t)
	s.chans.mu.Lock()
	r.Held, r.Started = true, time.Now().Add(-channel.HeldMost-time.Minute)
	s.chans.mu.Unlock()
	card := checkEnd(t, s, f, r, posts, proto.Session{Status: proto.StatusDone, State: proto.StateIdle})
	if !strings.Contains(card, `"template"`) || !strings.Contains(card, "已超时，去 App 里看") || !strings.Contains(card, "已调用 1 次工具") ||
		strings.Contains(card, "处理中") {
		t.Fatalf("card %s", card)
	}
}

// setRunSeen marks r as seen working, as the watch would.
func (s *Service) setRunSeen(r *channel.Run) {
	s.chans.mu.Lock()
	r.Seen = true
	s.chans.mu.Unlock()
}

// A run held after answering has no progress card any more (its card went back with the message). When the background
// work it waits for never reports back, the wait runs out — and the chat has to hear about it: the ending posts a card
// of its own, rather than saying nothing and leaving the owner to wonder.
func TestFeishuHeldRunGivesUpWithoutItsCard(t *testing.T) {
	s, f, r, posts := endHub(t)
	s.finishChannelRun(r, &channel.Result{Answer: "先答一部分", Held: true})
	s.chans.mu.Lock()
	left := r.Ext["card"]
	r.Started = time.Now().Add(-channel.HeldMost - time.Minute)
	s.chans.mu.Unlock()
	if left != "" {
		t.Fatalf("the held run kept its card: %q", left)
	}
	*posts = nil
	sess := proto.Session{Status: proto.StatusDone, State: proto.StateIdle, MachineID: "server", SessionID: "c1"}
	f.setSessions("server", sess)
	f.setItems()
	s.checkChannelRun(r.ID)
	if run, _ := s.channelRunByID(r.ID); run != nil {
		t.Fatalf("the run did not end")
	}
	if len(*posts) != 1 {
		t.Fatalf("the run ended without telling the owner: %v", *posts)
	}
	card, _ := (*posts)[0]["card"].(string)
	if (*posts)[0]["update"] == true || !strings.Contains(card, "已超时，去 App 里看") || !strings.Contains(card, "先不等了") {
		t.Fatalf("card %v", (*posts)[0])
	}
}

// A held run's ending can also be quiet: its background work reported back in a turn that said nothing. The run has no
// card left to rewrite, but the chat was told it would answer again — so its ending line goes out as a card of its own,
// rather than nowhere.
func TestFeishuHeldRunQuietEndWithoutItsCard(t *testing.T) {
	s, f, r, posts := endHub(t)
	s.finishChannelRun(r, &channel.Result{Answer: "先答一部分", Held: true})
	s.chans.mu.Lock()
	left := r.Ext["card"]
	r.Started = time.Now().Add(-channel.HeldIdleMost - time.Minute)
	s.chans.mu.Unlock()
	if left != "" {
		t.Fatalf("the held run kept its card: %q", left)
	}
	*posts = nil
	sess := proto.Session{Status: proto.StatusDone, State: proto.StateIdle, MachineID: "server", SessionID: "c1"}
	f.setSessions("server", sess)
	f.setItems()
	s.checkChannelRun(r.ID)
	if run, _ := s.channelRunByID(r.ID); run != nil {
		t.Fatalf("the run did not end")
	}
	if len(*posts) != 1 {
		t.Fatalf("the run ended without telling the owner: %v", *posts)
	}
	post := (*posts)[0]
	card, _ := post["card"].(string)
	if post["update"] == true || post["reply_to"] != "om_m1" || !strings.Contains(card, "完成 · Persona · 用时") {
		t.Fatalf("post %v", post)
	}
}

// A quiet ending is not always one to report: a message the owner recalled says nothing, with a card or without one.
func TestFeishuRecalledEndWithoutItsCardSaysNothing(t *testing.T) {
	s, _, r, posts := endHub(t)
	s.chans.mu.Lock()
	delete(r.Ext, "card") // recalled before its first progress card
	r.Recalled = true
	s.chans.mu.Unlock()
	*posts = nil
	s.finishChannelRun(r, recalledResult())
	if len(*posts) != 0 {
		t.Fatalf("a recalled message said something: %v", *posts)
	}
}
