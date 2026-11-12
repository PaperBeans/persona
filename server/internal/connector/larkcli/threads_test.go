package larkcli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	adapter "github.com/nwverse/persona/server/internal/larkcli"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestConversationPageUsesSourceIdentityAndCursor(t *testing.T) {
	c := testConnector(t, "success")
	log := filepath.Join(t.TempDir(), "calls")
	t.Setenv("LARKCLI_FAKE_LOG", log)
	p, err := c.Thread(context.Background(), "larkcli:chat:oc_example_group", "older-cursor", 900)
	if err != nil {
		t.Fatal(err)
	}
	if p.Chat.ID != "larkcli:chat:oc_example_group" || p.Chat.Reply != "" || len(p.Messages) != 2 || p.Before == "" {
		t.Fatalf("page = %+v", p)
	}
	if p.Messages[0].ID != "om_example_older" || p.Messages[0].Text != "Earlier example message" || p.Messages[0].Mine || p.Messages[1].ID != "om_example_newer" || !p.Messages[1].Mine {
		t.Fatalf("messages = %+v", p.Messages)
	}
	if p.Messages[0].At != time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC) {
		t.Fatal(p.Messages[0].At)
	}
	if p.Messages[1].URL != "https://applink.feishu.cn/client/thread/open?open_chat_id=oc_example_group&open_thread_id=omt_example_thread&thread_position=4" {
		t.Fatalf("source AppLink lost from the read page: %q", p.Messages[1].URL)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "+chat-messages-list --chat-id oc_example_group --order desc --page-size 50 --no-reactions --page-token older-cursor") {
		t.Fatalf("calls = %s", data)
	}
	if _, ok := any(c).(connector.Threads); !ok {
		t.Fatal("threads capability missing")
	}
}

func TestConversationTopicAndReadFailures(t *testing.T) {
	c := testConnector(t, "success")
	log := filepath.Join(t.TempDir(), "calls")
	t.Setenv("LARKCLI_FAKE_LOG", log)
	if _, err := c.Thread(context.Background(), "larkcli:chat:oc_example_group:thread:omt_example_thread", "", 30); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(log)
	if !strings.Contains(string(data), "+threads-messages-list --thread omt_example_thread") {
		t.Fatalf("topic call = %s", data)
	}
	for _, tc := range []struct{ scenario, code string }{{"rate_limited", "rate_limited"}, {"missing_scope", "no_permission"}, {"not_logged_in", "not_signed_in"}} {
		c = testConnector(t, tc.scenario)
		_, err := c.Thread(context.Background(), "larkcli:chat:oc_example_group", "", 30)
		typed, ok := err.(*rpc.Error)
		if !ok || typed.Code != tc.code {
			t.Fatalf("%s error = %#v", tc.scenario, err)
		}
	}
}

func TestConversationRejectsMalformedKeysWithoutReading(t *testing.T) {
	c := &Connector{}
	for _, key := range []string{"", "oc_example", "larkcli:chat:", "larkcli:chat:oc_example:thread:", "larkcli:chat:oc_example:thread:x:y", "larkcli:chat:other\nchat"} {
		if _, err := c.Thread(context.Background(), key, "", 30); err == nil {
			t.Fatalf("accepted %q", key)
		}
	}
}

func TestConversationContentPreservesCardAndUnknownJSON(t *testing.T) {
	card := "<card title=\"Merged\">\n**Title**\nExample\n</card>"
	encoded, _ := json.Marshal(map[string]string{"text": card})
	nested, _ := json.Marshal(string(encoded))
	for _, raw := range []json.RawMessage{encoded, nested} {
		if got := conversationText(raw); got != card {
			t.Fatalf("card = %q", got)
		}
	}
	quoted, _ := json.Marshal(`"Quoted user text"`)
	if got := conversationText(quoted); got != `"Quoted user text"` {
		t.Fatalf("quoted text changed: %q", got)
	}
	unknown := json.RawMessage(`{"elements":[{"tag":"unknown","value":"Keep this"}]}`)
	if got := conversationText(unknown); got != string(unknown) {
		t.Fatalf("unknown changed: %q", got)
	}
	for _, value := range []string{"invalid", ""} {
		if !conversationTime(value).IsZero() {
			t.Fatal("invented timestamp")
		}
	}
	if conversationTime("1893456000000").Year() != 2030 || conversationTime("1893456000").Year() != 2030 {
		t.Fatal("incorrect epoch units")
	}
	if conversationError(&adapter.Error{Kind: adapter.KindTimeout}) == nil {
		t.Fatal("lost read error")
	}
}

func TestConversationTimeAcceptsCLIWallTimeInItsProcessZone(t *testing.T) {
	previous := time.Local
	time.Local = time.FixedZone("Example offset", 8*60*60)
	t.Cleanup(func() { time.Local = previous })
	for _, value := range []string{"2030-01-01 12:34", "2030-01-01 12:34:56"} {
		got := conversationTime(value)
		_, offset := got.Zone()
		if got.Year() != 2030 || got.Hour() != 12 || got.Minute() != 34 || offset != 8*60*60 {
			t.Fatalf("wall time = %s", got)
		}
	}
	if got := conversationTime("2030-01-01T12:34:56-05:00"); got.Hour() != 12 {
		t.Fatalf("explicit offset changed: %s", got)
	}
}
