package lark

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// An agent with no name is "an agent", not two spaces.
func TestThreadPostTextWithoutAnAgentName(t *testing.T) {
	for _, c := range []struct {
		ev   threadEvent
		want string
	}{
		{threadEvent{kind: threadAgentNeeds, dm: true}, "有个 Agent 需要你，已发到私聊"},
		{threadEvent{kind: threadAgentNeeds}, "有个 Agent 需要你"},
		{threadEvent{kind: threadAgentNeeds, text: "删缓存吗"}, "有个 Agent 需要你：删缓存吗"},
		{threadEvent{kind: threadAgentFinished}, "有个 Agent 这一轮结束了"},
	} {
		got := threadPostText("", []threadEvent{c.ev}, "")
		if got != c.want || strings.Contains(got, "  ") {
			t.Errorf("%+v: %q, want %q", c.ev, got, c.want)
		}
	}
}

// A line for a to-do bound to no topic is not queued; one for a bound to-do waits for its timer, then goes as a reply
// under the topic's latest message, in its thread, never as a message of its own, with a key of its own and the
// to-do's link; what came meanwhile is one line.
func TestThreadPostQueuesAndFlushesOneLine(t *testing.T) {
	f, posts := feishuHub("ou_a", time.Unix(100, 0))
	withWebBot(f, "https://hub.example.com")
	f.answerApp(AppFeishu, FeishuStatusGet, func(json.RawMessage) (any, error) {
		return FeishuStatus{Ready: true, Enabled: true, PublicURL: "https://hub.example.com", Chats: []string{testChat}}, nil
	})
	_ = posts
	s := newTestService(t, f)
	s.layer.ThreadPost("todo_none", assistantwire.ThreadEvent{Kind: assistantwire.ThreadDecision, Text: "x"})
	if n := len(s.th().later); n != 0 {
		t.Fatalf("an unbound to-do armed %d timers", n)
	}
	s.th().bound["todo_1"] = [2]string{testChat, testThread}
	s.chat().rememberTopicMessage(context.Background(), testChat, testThread, "om_anchor")
	s.layer.ThreadPost("todo_1", assistantwire.ThreadEvent{Kind: assistantwire.ThreadDecision, Text: "Ship it → yes"})
	s.layer.ThreadPost("todo_1", assistantwire.ThreadEvent{Kind: assistantwire.ThreadTodoAccepted})
	if n := len(s.th().later); n != 1 {
		t.Fatalf("timers %d, want one for both events", n)
	}
	if n := len(f.appParams(AppFeishu, FeishuText)); n != 0 {
		t.Fatalf("%d lines before the timer", n)
	}
	s.fireLater()
	lines := f.appParams(AppFeishu, FeishuText)
	if len(lines) != 1 {
		t.Fatalf("lines %v", lines)
	}
	p := lines[0]
	text, _ := p["text"].(string)
	uuid, _ := p["uuid"].(string)
	if p["reply_to"] != "om_anchor" || p["chat_id"] != testChat || p["in_thread"] != true || p["no_detour"] != true ||
		!strings.HasPrefix(uuid, "tp:") || len(uuid) > 50 || !strings.Contains(text, "Ship it → yes") ||
		!strings.Contains(text, "https://hub.example.com/todos/todo_1") {
		t.Fatalf("the line %v", p)
	}
	if _, has := p["full_answer"]; has {
		t.Fatalf("a topic line carries full_answer: %v", p)
	}
}

// A topic whose anchor message is gone forgets it: the line is lost, the next owner's message is the new anchor.
func TestThreadPostForgetsAGoneAnchor(t *testing.T) {
	f, _ := feishuHub("ou_a", time.Unix(100, 0))
	f.answerApp(AppFeishu, FeishuStatusGet, func(json.RawMessage) (any, error) {
		return FeishuStatus{Ready: true, Enabled: true, Chats: []string{testChat}}, nil
	})
	f.answerApp(AppFeishu, FeishuText, func(json.RawMessage) (any, error) {
		return nil, fmt.Errorf("feishu reply: 230011 The message has been withdrawn")
	})
	s := newTestService(t, f)
	s.th().bound["todo_1"] = [2]string{testChat, testThread}
	s.chat().rememberTopicMessage(context.Background(), testChat, testThread, "om_gone")
	s.layer.ThreadPost("todo_1", assistantwire.ThreadEvent{Kind: assistantwire.ThreadTodoDone})
	s.fireLater()
	if got := s.chat().topicMessage(context.Background(), testChat, testThread); got != "" {
		t.Fatalf("the gone anchor is still kept: %q", got)
	}
}

// A chat the bot may not send into gets nothing, whatever a to-do is bound to.
func TestThreadPostNothingToAChatNotInTheList(t *testing.T) {
	f, _ := feishuHub("ou_a", time.Unix(100, 0))
	s := newTestService(t, f)
	s.th().bound["todo_1"] = [2]string{"oc_other", testThread}
	s.chat().rememberTopicMessage(context.Background(), "oc_other", testThread, "om_anchor")
	s.layer.ThreadPost("todo_1", assistantwire.ThreadEvent{Kind: assistantwire.ThreadTodoDone})
	s.fireLater()
	if n := len(f.appParams(AppFeishu, FeishuText)); n != 0 {
		t.Fatalf("%d lines into a chat not in the list", n)
	}
}
