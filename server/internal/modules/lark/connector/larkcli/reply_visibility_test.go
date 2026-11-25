package larkcli

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/connectorwire"
)

func setReplyVisibility(t *testing.T, c *Connector, raw string) {
	t.Helper()
	if err := c.SetOptions(json.RawMessage(raw)); err != nil {
		t.Fatal(err)
	}
}

func fakeReplyCalls(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestReplyVisibilityOptionsRoundTrip(t *testing.T) {
	c := testConnector(t, "success")
	setReplyVisibility(t, c, `{"inbox_exclude_chats":["oc_example_dm"],"inbox_exclude_direct_chats":true}`)
	var options InboxOptions
	if err := json.Unmarshal(c.Options(), &options); err != nil {
		t.Fatal(err)
	}
	if len(options.InboxExcludeChats) != 1 || options.InboxExcludeChats[0] != "oc_example_dm" || !options.InboxExcludeDirectChats {
		t.Fatalf("visibility options did not round-trip: %+v", options)
	}
	if err := c.SetOptions(json.RawMessage(`{"inbox_exclude_chats":["duplicate","duplicate"]}`)); err == nil {
		t.Fatal("duplicate excluded chat ids were accepted")
	}
}

func TestReplyVisibilityMessagesVetoBeforeBodyRead(t *testing.T) {
	log := t.TempDir() + "/calls.log"
	t.Setenv("LARKCLI_FAKE_LOG", log)
	c := testConnector(t, "success")
	setReplyVisibility(t, c, `{"inbox_exclude_chats":["oc_example_dm"]}`)
	if _, err := c.Call(context.Background(), "larkcli_messages", json.RawMessage(`{"chat_id":"oc_example_dm"}`)); err == nil {
		t.Fatal("an explicitly excluded chat was read")
	}
	if calls := fakeReplyCalls(t, log); calls != "" {
		t.Fatalf("explicit veto made downstream calls: %s", calls)
	}
	if _, err := c.Call(context.Background(), "larkcli_messages", json.RawMessage(`{"thread":"synthetic-thread"}`)); err == nil {
		t.Fatal("an unverifiable thread was read with visibility controls enabled")
	}
	if calls := fakeReplyCalls(t, log); calls != "" {
		t.Fatalf("thread veto made downstream calls: %s", calls)
	}
	if _, err := c.Call(context.Background(), "larkcli_search", json.RawMessage(`{"query":"synthetic"}`)); err == nil {
		t.Fatal("global search remained available with visibility controls enabled")
	}
	if calls := fakeReplyCalls(t, log); calls != "" {
		t.Fatalf("search veto made downstream calls: %s", calls)
	}

	setReplyVisibility(t, c, `{"inbox_exclude_direct_chats":true}`)
	if _, err := c.Call(context.Background(), "larkcli_messages", json.RawMessage(`{"chat_id":"oc_example_dm"}`)); err == nil {
		t.Fatal("a direct chat was read with the direct-chat switch on")
	}
	calls := fakeReplyCalls(t, log)
	if !strings.Contains(calls, "chats get") || strings.Contains(calls, "+chat-messages-list") {
		t.Fatalf("direct-chat veto did not stop after metadata: %s", calls)
	}
	if _, err := c.Call(context.Background(), "larkcli_messages", json.RawMessage(`{"chat_id":"oc_example_group"}`)); err != nil {
		t.Fatalf("ordinary group remained readable: %v", err)
	}
	out, err := c.Call(context.Background(), "larkcli_chats", json.RawMessage(`{"filter":"recent"}`))
	if err != nil || strings.Contains(out, "Example User") || !strings.Contains(out, "Example Group") {
		t.Fatalf("filtered chat list = %s, %v", out, err)
	}

	setReplyVisibility(t, c, `{}`)
	out, err = c.Call(context.Background(), "larkcli_messages", json.RawMessage(`{"chat_id":"oc_example_dm"}`))
	if err != nil || !strings.Contains(out, "Example question") {
		t.Fatalf("default configuration changed direct-chat reads: %s, %v", out, err)
	}
}

func TestReplyVisibilityDirectSweepAndSearch(t *testing.T) {
	t.Run("explicit list skips direct history body", func(t *testing.T) {
		log := t.TempDir() + "/calls.log"
		t.Setenv("LARKCLI_FAKE_LOG", log)
		c := testConnector(t, "inbox_lag")
		setReplyVisibility(t, c, `{"inbox_exclude_chats":["oc_lag_dm"]}`)
		messages, err := c.sweepDirectChats(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(messages)
		if strings.Contains(string(raw), "oc_lag_dm") {
			t.Fatalf("excluded direct history was returned: %s", raw)
		}
		calls := fakeReplyCalls(t, log)
		for _, line := range strings.Split(calls, "\n") {
			if strings.Contains(line, "+chat-messages-list") && strings.Contains(line, "oc_lag_dm") {
				t.Fatalf("excluded direct history had a body read: %s", calls)
			}
		}
	})

	t.Run("all direct switch skips the sweep entirely", func(t *testing.T) {
		log := t.TempDir() + "/calls.log"
		t.Setenv("LARKCLI_FAKE_LOG", log)
		c := testConnector(t, "inbox_lag")
		setReplyVisibility(t, c, `{"inbox_exclude_direct_chats":true}`)
		messages, err := c.sweepDirectChats(context.Background())
		if err != nil || len(messages) != 0 {
			t.Fatalf("all-direct sweep = %+v, %v", messages, err)
		}
		if calls := fakeReplyCalls(t, log); calls != "" {
			t.Fatalf("all-direct sweep made downstream calls: %s", calls)
		}
	})

	for _, tc := range []struct {
		name string
		cfg  string
		want bool
	}{
		{name: "default keeps direct chats", cfg: `{}`, want: true},
		{name: "explicit list filters search", cfg: `{"inbox_exclude_chats":["oc_example_dm"]}`},
		{name: "all direct filters search", cfg: `{"inbox_exclude_direct_chats":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testConnector(t, "inbox_p2p")
			setReplyVisibility(t, c, tc.cfg)
			if _, err := c.pollChat(context.Background()); err != nil {
				t.Fatal(err)
			}
			things, err := c.Collect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(things)
			visible := strings.Contains(string(raw), "Direct question")
			if visible != tc.want {
				t.Fatalf("direct-chat visibility = %v, want %v: %s", visible, tc.want, raw)
			}
		})
	}
}

func TestReplyVisibilityPollSkipsBodySearch(t *testing.T) {
	for _, tc := range []struct {
		name       string
		cfg        string
		wantSearch bool
	}{
		{name: "legacy configuration keeps search", cfg: `{}`, wantSearch: true},
		{name: "explicit list disables body-returning search", cfg: `{"inbox_exclude_chats":["oc_example_dm"]}`},
		{name: "all-direct switch disables body-returning search", cfg: `{"inbox_exclude_direct_chats":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := t.TempDir() + "/calls.log"
			t.Setenv("LARKCLI_FAKE_LOG", log)
			c := testConnector(t, "inbox_p2p")
			setReplyVisibility(t, c, tc.cfg)
			if _, err := c.pollChat(context.Background()); err != nil {
				t.Fatal(err)
			}
			calls := fakeReplyCalls(t, log)
			if got := strings.Contains(calls, "+messages-search"); got != tc.wantSearch {
				t.Fatalf("body-returning search called=%v, want %v: %s", got, tc.wantSearch, calls)
			}
		})
	}
}

func TestReplyVisibilityPollPreservesLegacySearchState(t *testing.T) {
	c := testConnector(t, "inbox_p2p")
	c.inboxState.PageToken = "saved-search-page"
	c.inboxState.TopicsSeeded = false
	setReplyVisibility(t, c, `{"inbox_exclude_chats":["oc_example_dm"]}`)
	if _, err := c.pollChat(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.inboxState.PageToken != "saved-search-page" || c.inboxState.TopicsSeeded {
		t.Fatalf("visibility-only round changed legacy search progress: %+v", c.inboxState)
	}
	setReplyVisibility(t, c, `{}`)
	if _, err := c.pollChat(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !c.inboxState.TopicsSeeded {
		t.Fatal("legacy polling never performed its historical topic seed after visibility was disabled")
	}
}

func TestReplyVisibilityAtMeVetoBeforeBodySearch(t *testing.T) {
	log := t.TempDir() + "/calls.log"
	t.Setenv("LARKCLI_FAKE_LOG", log)
	c := testConnector(t, "success")
	setReplyVisibility(t, c, `{"inbox_exclude_chats":["oc_example_dm"]}`)
	if _, err := c.Call(context.Background(), "larkcli_chats", json.RawMessage(`{"filter":"at_me"}`)); err == nil {
		t.Fatal("at_me used a body-returning search with visibility controls enabled")
	}
	if calls := fakeReplyCalls(t, log); calls != "" {
		t.Fatalf("at_me veto made downstream calls: %s", calls)
	}
}

func TestManagedReplyVisibilitySurvivesConnectorOptions(t *testing.T) {
	c := testConnector(t, "success")
	if err := c.SetManagedInboxVisibility([]string{"oc_manager_private"}, true); err != nil {
		t.Fatal(err)
	}
	// A connector options update cannot clear the manager's central authorization boundary.
	setReplyVisibility(t, c, `{}`)
	if !c.replyChatExcluded("oc_manager_private", "group", "") || !c.replyChatExcluded("oc_work_dm", "p2p", "") {
		t.Fatal("connector options cleared the manager's visibility boundary")
	}
	if err := c.SetManagedInboxVisibility([]string{"duplicate", "duplicate"}, false); err == nil {
		t.Fatal("invalid managed visibility was accepted")
	}
	if !c.replyChatExcluded("oc_ordinary_group", "group", "") {
		t.Fatal("invalid managed visibility did not fail the source closed")
	}
}

func TestReplyVisibilityFiltersRetainedThings(t *testing.T) {
	c := testConnector(t, "success")
	c.inboxCollecting = true
	c.inboxThings = map[string]connectorwire.InboxThing{
		"private": {Thread: "private", Kind: "dm", Context: "SECRET-DM", Tags: []connectorwire.Tag{{Key: connectorwire.TagChat, Value: "oc_example_dm"}}},
		"group":   {Thread: "group", Kind: "group", Context: "ORDINARY-OK", Tags: []connectorwire.Tag{{Key: connectorwire.TagChat, Value: "oc_example_group"}}},
	}
	setReplyVisibility(t, c, `{"inbox_exclude_chats":["oc_example_dm"]}`)
	things, err := c.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(things)
	if strings.Contains(string(raw), "SECRET-DM") || !strings.Contains(string(raw), "ORDINARY-OK") {
		t.Fatalf("retained things were not filtered: %s", raw)
	}
}
