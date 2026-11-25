package lark

import (
	"testing"

	"github.com/nwverse/persona/server/internal/botcard"
)

// Feishu may send a topic's first message with no root_id, or with its own id as the root: both open one. A message
// that replies into the topic (a root that is another message) and one outside any topic do not. (That such a message
// opens one to-do is the assistant's TestIsTopicRoot.)
func TestIsTopicRootShapes(t *testing.T) {
	for _, c := range []struct {
		name                   string
		thread, root, messages string
		want                   bool
	}{
		{"no root_id", "omt_a", "", "om_1", true},
		{"its own id as the root", "omt_a", "om_1", "om_1", true},
		{"a reply to the root", "omt_a", "om_0", "om_1", false},
		{"outside a topic", "", "", "om_1", false},
		{"outside a topic, own root", "", "om_1", "om_1", false},
	} {
		var ev botcard.MessageEvent
		ev.Message.ThreadID, ev.Message.RootID, ev.Message.MessageID = c.thread, c.root, c.messages
		if got := isTopicRoot(ev); got != c.want {
			t.Errorf("%s: isTopicRoot = %v, want %v", c.name, got, c.want)
		}
	}
}
