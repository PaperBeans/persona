package larkcli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/connectorwire"
	adapter "github.com/nwverse/persona/server/internal/modules/lark/larkcli"
)

func TestReplyVisibilityMixedChatAndThreadVetoBeforeBodyRead(t *testing.T) {
	log := t.TempDir() + "/calls.log"
	t.Setenv("LARKCLI_FAKE_LOG", log)
	c := testConnector(t, "success")
	setReplyVisibility(t, c, `{"inbox_exclude_chats":["oc_example_dm"]}`)
	args := json.RawMessage(`{"chat_id":"oc_example_group","thread":"synthetic-private-thread"}`)
	if _, err := c.Call(context.Background(), "larkcli_messages", args); err == nil {
		t.Fatal("an allowed group chat_id authorized a different, unverifiable thread")
	}
	if calls := fakeReplyCalls(t, log); calls != "" {
		t.Fatalf("mixed chat/thread veto made downstream calls: %s", calls)
	}

	// Empty visibility configuration retains the legacy thread-precedence behavior.
	setReplyVisibility(t, c, `{}`)
	out, err := c.Call(context.Background(), "larkcli_messages", args)
	if err != nil || !strings.Contains(out, "Newer example message") {
		t.Fatalf("legacy mixed chat/thread read = %s, %v", out, err)
	}
	if calls := fakeReplyCalls(t, log); !strings.Contains(calls, "+threads-messages-list --thread synthetic-private-thread") {
		t.Fatalf("legacy read did not use its thread: %s", calls)
	}
}

func TestReplyVisibilityChatTotalLegacyAndFiltered(t *testing.T) {
	c := testConnector(t, "success")
	page := adapter.ChatList{Total: 120, Chats: []adapter.Chat{
		{ChatID: "oc_example_group", ChatType: "group"},
		{ChatID: "oc_example_dm", ChatType: "p2p"},
	}}
	got := c.filterReplyChats(page)
	if got.Total != 120 || len(got.Chats) != 2 {
		t.Fatalf("empty visibility changed the legacy upstream total: %+v", got)
	}

	setReplyVisibility(t, c, `{"inbox_exclude_direct_chats":true}`)
	got = c.filterReplyChats(page)
	if got.Total != 1 || len(got.Chats) != 1 || got.Chats[0].ChatID != "oc_example_group" {
		t.Fatalf("enabled visibility did not report the visible rows: %+v", got)
	}
	if !c.Manifest().Has(connectorwire.CapInboxVisibility) {
		t.Fatal("the connector does not declare its implemented visibility preflight")
	}
}
