package assistant

import (
	"encoding/json"
	"slices"
	"testing"

	w "github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
)

func TestInboxExternalReferenceOnIngest(t *testing.T) {
	for _, tc := range []struct {
		name, source, via string
		thing             connector.InboxThing
		mail              *InboxMail
		ref               w.ExternalRef
		events            []string
	}{
		{"Feishu topic", "feishu", "", connector.InboxThing{Thread: "oc_group", Last: "8",
			OpenURL: "https://applink.feishu.cn/client/thread/open?open_chat_id=oc_group&open_thread_id=omt_topic",
			Extra:   &connector.ThingExtra{Lark: json.RawMessage(`{"feed_id":"oc_group","account_id":2,"reply_to":"om_message"}`)}}, nil,
			w.ExternalRef{Provider: "lark", Channel: "2", Container: externalNode("chat", "oc_group"), Item: externalNode("thread", "omt_topic")}, []string{"om_message"}},
		{"Lark CLI topic", "larkcli", "", connector.InboxThing{Thread: "larkcli:chat:oc_group:thread:omt_topic", Last: "om_message"}, nil,
			w.ExternalRef{Provider: "lark", Container: externalNode("chat", "oc_group"), Item: externalNode("thread", "omt_topic")}, []string{"om_message"}},
		{"Lark chat", "lark", "", connector.InboxThing{Thread: "oc_group", Last: "17",
			Extra: &connector.ThingExtra{Lark: json.RawMessage(`{"feed_id":"oc_group","account_id":1}`)}}, nil,
			w.ExternalRef{Provider: "lark", Container: externalNode("chat", "oc_group")}, []string{"17"}},
		{"GitHub through another source", "sub_test", "github", connector.InboxThing{Thread: "Example/Web#17",
			URL:  "https://www.github.com/Example/Web/pull/17#issuecomment-42",
			Last: `gh-message:{"at":"2026-11-25T00:00:00Z","ids":["comment:42","review:51"]}#7`}, nil,
			w.ExternalRef{Provider: "github", Container: externalNode("repository", "example/web"), Item: externalNode("pull_request", "17")}, []string{"comment:42", "review:51"}},
		{"GitHub Enterprise issue", "github", "", connector.InboxThing{Thread: "Example/Web#17",
			URL: "https://code.example.com/Example/Web/issues/17", Last: "cursor1"}, nil,
			w.ExternalRef{Provider: "github", Channel: "https://code.example.com", Container: externalNode("repository", "example/web"), Item: externalNode("issue", "17")}, []string{"cursor1"}},
		{"Mail mailbox and thread", "mail", "", connector.InboxThing{}, &InboxMail{AccountID: "work", Mailbox: "INBOX", Thread: "abc123", MessageID: "<example@example.com>"},
			w.ExternalRef{Provider: "mail", Channel: "work", Container: externalNode("mailbox", "INBOX"), Item: externalNode("thread", "abc123")}, []string{"message:example@example.com"}},
		{"Mail account thread", "mail", "", connector.InboxThing{Last: "41"}, &InboxMail{AccountID: "work", Thread: "abc123", Generation: 2},
			w.ExternalRef{Provider: "mail", Channel: "work", Item: externalNode("thread", "abc123")}, []string{"uid:2:41"}},
		{"Subscription", "sub_test", "", connector.InboxThing{Thread: "sub:sub_test:article/17", Last: "entry-34"}, nil,
			w.ExternalRef{Provider: "subscription", Container: externalNode("source", "sub_test"), Item: externalNode("item", "article/17")}, []string{"entry-34"}},
		{"Push", "webhook", "", connector.InboxThing{Thread: "alerts", Last: "event/22"}, nil,
			w.ExternalRef{Provider: "webhook", Container: externalNode("source", "webhook"), Item: externalNode("item", "alerts")}, []string{"event/22"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			it := &InboxItem{Source: tc.source, Mail: tc.mail, Last: tc.thing.Last}
			if tc.thing.Extra != nil {
				setBlocks(it, tc.thing.Extra)
			}
			setInboxExternalPath(it, tc.via, tc.thing)
			path := []w.ExternalRef{tc.ref}
			parent := tc.ref
			if parent.Item != (w.ExternalRefNode{}) && parent.Container != (w.ExternalRefNode{}) {
				parent.Item = w.ExternalRefNode{}
				path = append(path, parent)
			}
			if tc.ref.Item != (w.ExternalRefNode{}) || tc.ref.Container != (w.ExternalRefNode{}) {
				path = append(path, w.ExternalRef{Provider: tc.ref.Provider, Channel: tc.ref.Channel})
			}
			if !slices.Equal(it.ExternalPath, path) || !slices.Equal(inboxExternalEventIDs(it), tc.events) {
				t.Fatalf("path = %+v, events = %v", it.ExternalPath, inboxExternalEventIDs(it))
			}
		})
	}
}

func TestInboxExternalReferenceDoesNotInferGitHubItemFromProse(t *testing.T) {
	it := &InboxItem{Source: "github", Last: "new"}
	setInboxExternalPath(it, "", connector.InboxThing{Thread: "example/web#17",
		Context: "See https://github.com/example/other/pull/17", Tags: []connector.Tag{{Key: "repo", Value: "example/web"}}})
	if len(it.ExternalPath) != 2 || it.ExternalPath[0].Item != (w.ExternalRefNode{}) || it.ExternalPath[0].Container.ID != "example/web" {
		t.Fatalf("bare thread number or body text selected an object: %+v", it.ExternalPath)
	}
}
