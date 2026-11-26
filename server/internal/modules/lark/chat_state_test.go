package lark

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// A reply to a card of days ago still goes to its agent after many more cards were bound since; a card older than
// the keep is let go, and a full table drops its oldest.
func TestFeishuOldCardStillRoutesItsReply(t *testing.T) {
	s := newTestService(t, newFakeHub())
	now := time.Unix(10_000_000, 0)
	s.ch().now = func() time.Time { return now }
	agent := botTarget{Kind: targetAgent, MachineID: "server", SessionID: "a1"}
	s.layer.RememberThread("om_first", agent, "Claude")
	for i := 0; i < 150; i++ {
		now = now.Add(time.Hour)
		s.layer.RememberThread(fmt.Sprintf("om_%d", i), botTarget{Kind: targetPersona, MachineID: "server", SessionID: "m1"}, "主对话")
	}
	if got := s.layer.Route("om_first", "om_first"); got != agent {
		t.Fatalf("a card of six days ago routes to %+v", got)
	}
	now = now.Add(botThreadsKeepFor)
	s.layer.RememberThread("om_new", agent, "Claude")
	if got := s.layer.Route("om_first", "om_first"); got.Kind != targetMain {
		t.Fatalf("a card past the keep still routes to %+v", got)
	}

	threads := map[string]*botThread{}
	for i := 0; i < botThreadsKeep+5; i++ {
		threads[fmt.Sprint(i)] = &botThread{At: now.Add(time.Duration(i) * time.Second)}
	}
	pruneBotThreads(threads, now.Add(time.Hour))
	if len(threads) != botThreadsKeep || threads["0"] != nil || threads["4"] != nil || threads["5"] == nil {
		t.Fatalf("kept %d, oldest %v", len(threads), threads["5"] != nil)
	}
}

// A stored thread without a time (from a state written before or by hand) is kept: it is not 30 days old.
func TestFeishuThreadWithoutATimeIsKept(t *testing.T) {
	threads := map[string]*botThread{"om_old": {Target: botTarget{Kind: targetMain}}}
	pruneBotThreads(threads, time.Unix(10_000_000, 0))
	if threads["om_old"] == nil {
		t.Fatal("a thread without a time was dropped")
	}
}

// The remembered messages are bounded, and old ones go.
func TestTopicAnchorsAreBounded(t *testing.T) {
	now := time.Now()
	m := map[string]*topicAnchor{"old": {MessageID: "om_old", At: now.Add(-topicAnchorsKeepFor - time.Hour)}}
	for i := 0; i < topicAnchorsKeep+10; i++ {
		m[fmt.Sprintf("k%d", i)] = &topicAnchor{MessageID: "om", At: now.Add(-time.Duration(i) * time.Minute)}
	}
	pruneTopicAnchors(m, now)
	if len(m) != topicAnchorsKeep || m["old"] != nil || m["k0"] == nil || m[fmt.Sprintf("k%d", topicAnchorsKeep+9)] != nil {
		t.Fatalf("kept %d: old %v", len(m), m["old"])
	}
}

// A topic's anchor is forgotten only while it is still the one given: a newer message that came meanwhile stays (the
// lines' TestThreadPostForgetsAGoneAnchor forgets one that is gone).
func TestTopicAnchorForgetKeepsANewerOne(t *testing.T) {
	s := newTestService(t, newFakeHub())
	c, ctx := s.chat(), context.Background()
	c.rememberTopicMessage(ctx, testChat, testThread, "om_old")
	c.rememberTopicMessage(ctx, testChat, testThread, "om_newer")
	c.forgetTopicMessage(ctx, testChat, testThread, "om_old")
	if got := s.layer.TopicAnchor(ctx, testChat, testThread); got != "om_newer" {
		t.Fatalf("anchor %q", got)
	}
}
