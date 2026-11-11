package assistant

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"testing"
	"time"
)

func pageItems(t *testing.T, out map[string]any) []FeedItem {
	t.Helper()
	raw, err := json.Marshal(out["items"])
	if err != nil {
		t.Fatal(err)
	}
	var items []FeedItem
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatal(err)
	}
	return items
}

func TestFeedPaginationCursorRoundtrip(t *testing.T) {
	s, _ := newTaskService(t)
	old := time.Now().UTC().Add(-3 * time.Hour)
	since, changed := old.Add(time.Hour), old.Add(2*time.Hour)
	body, _ := json.Marshal(map[string]any{"draft": "Old progress", "dismissed_at": changed, "dismissal_id": "synthetic-decision", "dismissal_updated_at": changed})
	s.tasks.mu.Lock()
	// Reverse insertion order also verifies store-less (at,id) ordering and
	// tie-breaking, rather than relying on the map's append order.
	for i := 499; i >= 0; i-- {
		s.tasks.feed["page-chat"] = append(s.tasks.feed["page-chat"], FeedItem{ID: fmt.Sprintf("page-%03d", i), Conversation: "page-chat", At: old, Kind: feedMessage, Body: body})
	}
	s.tasks.feed["page-chat"] = append(s.tasks.feed["page-chat"], FeedItem{ID: "new-progress", Conversation: "page-chat", At: since.Add(time.Minute), Kind: feedMessage, Body: json.RawMessage(`{"draft":"New progress"}`)})
	s.tasks.mu.Unlock()
	path := "/api/v1/assistant/conversations/page-chat/feed"
	code, first := inboxCall(t, s, "GET", path+"?paginate=1&since="+url.QueryEscape(since.Format(time.RFC3339Nano)), "")
	if code != 200 {
		t.Fatalf("first page: %d %v", code, first)
	}
	items := pageItems(t, first)
	cursor, ok := first["next_cursor"].(string)
	if len(items) != 500 || !ok || cursor == "" || items[0].ID != "page-000" || items[499].ID != "page-499" {
		t.Fatalf("first page: %d items, cursor %q", len(items), cursor)
	}
	window, err := parseFeedCursor(cursor, "page-chat")
	if err != nil || !window.Since.Equal(since) {
		t.Fatalf("cursor window: %+v %v", window, err)
	}
	s.tasks.mu.Lock()
	s.tasks.feed["page-chat"] = append(s.tasks.feed["page-chat"], FeedItem{ID: "after-window", Conversation: "page-chat", At: window.Until.Add(time.Second), Kind: feedMessage})
	s.tasks.mu.Unlock()
	code, second := inboxCall(t, s, "GET", path+"?cursor="+url.QueryEscape(cursor), "")
	more := pageItems(t, second)
	if code != 200 || len(more) != 1 || more[0].ID != "new-progress" || second["next_cursor"] != "" {
		t.Fatalf("second page: %d %v", code, second)
	}
	if len(items)+len(more) != 501 {
		t.Fatal("updated old cards starved new progress")
	}
	_, legacy := inboxCall(t, s, "GET", path+"?since="+url.QueryEscape(since.Format(time.RFC3339Nano)), "")
	if _, present := legacy["next_cursor"]; present {
		t.Fatal("legacy response acquired pagination fields")
	}
	for _, query := range []string{"cursor=", "cursor=invalid", "cursor=" + url.QueryEscape(cursor) + "&since=" + url.QueryEscape(since.Format(time.RFC3339Nano))} {
		if code, _ := inboxCall(t, s, "GET", path+"?"+query, ""); code != 400 {
			t.Fatalf("invalid cursor query %q: %d", query, code)
		}
	}
	if code, _ := inboxCall(t, s, "GET", "/api/v1/assistant/conversations/other-chat/feed?cursor="+url.QueryEscape(cursor), ""); code != 400 {
		t.Fatalf("cross-conversation cursor: %d", code)
	}
}

func TestFeedPaginationRejectsInvalidCursorState(t *testing.T) {
	now := time.Now().UTC()
	base := feedCursor{Version: 1, Conversation: "chat", Until: now, AfterAt: now.Add(-time.Minute), AfterID: "synthetic-card"}
	for _, change := range []func(*feedCursor){
		func(c *feedCursor) { c.Version = 2 },
		func(c *feedCursor) { c.Until = time.Time{} },
		func(c *feedCursor) { c.AfterAt = c.Until.Add(time.Second) },
		func(c *feedCursor) { c.AfterID = "" },
		func(c *feedCursor) { c.Since = c.Until.Add(time.Second) },
	} {
		c := base
		change(&c)
		raw, _ := json.Marshal(c)
		if _, err := parseFeedCursor(base64.RawURLEncoding.EncodeToString(raw), "chat"); err == nil {
			t.Fatalf("accepted invalid cursor: %+v", c)
		}
	}
}
