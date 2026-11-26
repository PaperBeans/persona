package lark

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/botcard"
	"github.com/nwverse/persona/server/internal/compat"
)

// contextBlocks are the context blocks a message to a conversation carries (the core's stripContext takes them off).
var (
	contextBlock = regexp.MustCompile(`(?s)<` + compat.ContextPattern() + `\b[^>]*>.*?</` + compat.ContextPattern() + `>`)
	contextOpen  = regexp.MustCompile(`<` + compat.ContextPattern() + `\b`)
)

// stripContext is what the owner said, the context blocks taken off (the core's channels_plugin.go).
func stripContext(s string) string {
	s = contextBlock.ReplaceAllString(s, "")
	if loc := contextOpen.FindStringIndex(s); loc != nil {
		s = s[:loc[0]]
	}
	return strings.TrimSpace(s)
}

// A group reply cannot import another person's quote or a private to-do into this turn: in a topic the message goes as
// said, with the owner and the place named in the owner's language, nothing is read of the message it replies to, the
// reminder's to-do is not looked up, and the run answers into the topic. (The assistant's
// TestChannelOwnerAndAudienceAcrossLocales checks the private deliveries.)
func TestTopicDeliveryImportsNothingAcrossLocales(t *testing.T) {
	for _, tc := range []struct{ locale, owner, topic string }{
		{"en", "The user", "Lark group topic"},
		{"ja", "ユーザー", "Lark のグループトピック"},
		{"zh-Hans", "用户", "飞书群里的话题"},
	} {
		t.Run(tc.locale, func(t *testing.T) {
			f := newFakeHub()
			s := newTestService(t, f)
			s.ch().hello.Locale = tc.locale
			var ev botcard.MessageEvent
			ev.Message.MessageID, ev.Message.ChatID, ev.Message.ChatType, ev.Message.ThreadID = "topic_42", testChat, "group", testThread
			ev.Message.RootID, ev.Message.ParentID = "om_root", "other_42"
			ev.Message.MessageType, ev.Message.Content = "text", `{"text":"Buenos días."}`
			target := botTarget{Kind: targetPersona, MachineID: "server", SessionID: "m1", TodoID: "private_42"}
			if err := s.chat().botSendIn(context.Background(), &ev, botcard.Content{Text: "Buenos días.", Forward: true}, target,
				&feishuTopic{Chat: testChat, Thread: testThread}); err != nil {
				t.Fatal(err)
			}
			runs := s.ch().started
			if len(runs) != 1 {
				t.Fatalf("runs %+v", runs)
			}
			text := runs[0].text
			if stripContext(text) != "Buenos días." || !strings.Contains(text, tc.owner) || !strings.Contains(text, tc.topic) ||
				len(f.appParams(AppFeishu, FeishuMessages)) != 0 || strings.Contains(text, "private_42") ||
				runs[0].ext["chat"] != testChat || runs[0].ext["thread"] != testThread {
				t.Fatalf("topic delivery: %q, %+v", text, runs[0])
			}
		})
	}
}
