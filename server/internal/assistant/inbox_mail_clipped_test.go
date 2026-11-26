package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// mailClippedDetail is GET /api/v1/inbox/{id} as a client reads it: the body's keys.
func mailClippedDetail(t *testing.T, h http.Handler, id string) map[string]json.RawMessage {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/inbox/"+id, nil))
	out := map[string]json.RawMessage{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/inbox/%s: %d %s %v", id, w.Code, w.Body, err)
	}
	return out
}

// The item's own page says whether its stored mail text was clipped: true, false, or nothing when the hub does not
// know. The list never carries it, and nothing else the list leaves out comes back with it.
func TestInboxMailClippedReachesTheItemPage(t *testing.T) {
	yes, no := true, false
	mail := func() *InboxMail {
		return &InboxMail{AccountID: "a1", Mailbox: "INBOX", UIDs: []uint32{7}, MessageID: "m@example.com",
			From: MailAddress{Email: "sender@example.com"}}
	}
	for _, c := range []struct {
		name string
		edit func(*InboxItem)
		want string // the JSON value, "" when the key is absent
	}{
		{"clipped", func(it *InboxItem) { it.MailBodyTruncated = &yes }, "true"},
		{"whole", func(it *InboxItem) { it.MailBodyTruncated = &no }, "false"},
		{"not known: a row from before the flag", func(*InboxItem) {}, ""},
		{"not known: a newer mail's text is not read yet", func(it *InboxItem) { it.MailBodyTruncated, it.Last = &no, "8" }, ""},
		{"not known: a clipped flag for an older mail", func(it *InboxItem) { it.MailBodyTruncated, it.Last = &yes, "8" }, ""},
		{"not known: whole, but the text also holds merged members", func(it *InboxItem) {
			it.MailBodyTruncated, it.MergedFrom = &no, []InboxMergeRef{{ID: "example:member", Source: "example"}}
		}, ""},
		{"clipped and merged", func(it *InboxItem) {
			it.MailBodyTruncated, it.MergedFrom = &yes, []InboxMergeRef{{ID: "example:member", Source: "example"}}
		}, "true"},
		{"not a mail", func(it *InboxItem) { it.MailBodyTruncated, it.Mail = &yes, nil }, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			old := time.Now().Add(-20 * 24 * time.Hour)
			item := func(id string) *InboxItem {
				it := &InboxItem{ID: id, Source: "mail", Kind: KindMail, Title: "Example", UpdatedAt: time.Now(),
					Context: "Example text", Last: "7", BodyOf: "7", Mail: mail(),
					// The neighbours listed() clears on the same line: they must stay out of every client view.
					ReplyPolicy:         &InboxReplyPolicySnapshot{},
					ReplyContentSources: []string{"mail"}, ReplyContextTruncated: true}
				c.edit(it)
				return it
			}
			h := newInboxHub(t)
			ctx := context.Background()
			// One item only the database holds (done long ago), one in memory: both answers of the item route.
			stored := item("mail:stored")
			stored.UpdatedAt, stored.DoneAt, stored.DoneBy = old, &old, "user"
			raw, _ := json.Marshal(stored)
			if err := h.st.PutInboxItem(ctx, stored.ID, raw, stored.UpdatedAt, stored.DoneAt); err != nil {
				t.Fatal(err)
			}
			if err := h.inboxReady(ctx); err != nil {
				t.Fatal(err)
			}
			waitNeedsYouIdle(t, h.Service)
			live := item("mail:live")
			h.inbox.mu.Lock()
			h.inbox.items[live.ID] = live
			h.inbox.mu.Unlock()
			mux := h.routes()
			for _, id := range []string{"mail:live", "mail:stored"} {
				out := mailClippedDetail(t, mux, id)
				if got := string(out["mail_body_truncated"]); got != c.want {
					t.Errorf("%s: mail_body_truncated = %q, want %q", id, got, c.want)
				}
				if string(out["context"]) != `"Example text"` {
					t.Errorf("%s: context = %s", id, out["context"])
				}
				for _, key := range []string{"reply_policy", "reply_content_sources", "reply_context_truncated", "body_of", "last"} {
					if _, ok := out[key]; ok {
						t.Errorf("%s: the item page carries %s", id, key)
					}
				}
			}
			// The list (whole and since a cursor) has no context, so it does not say whether that text was clipped.
			for _, path := range []string{"/api/v1/inbox?sources=all", "/api/v1/inbox?sources=all&since=0"} {
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
				if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"mail:live"`) {
					t.Fatalf("GET %s: %d %s", path, w.Code, w.Body)
				}
				for _, key := range []string{"mail_body_truncated", "reply_policy", "reply_content_sources", "reply_context_truncated", `"context"`} {
					if strings.Contains(w.Body.String(), key) {
						t.Errorf("GET %s carries %s: %s", path, key, w.Body)
					}
				}
			}
			if live.MailBodyTruncated != stored.MailBodyTruncated && (live.MailBodyTruncated == nil || stored.MailBodyTruncated == nil) {
				t.Error("the views changed the stored flag")
			}
		})
	}
}

// What readMailBody stores is what the page then says: a long text is clipped, a short one is whole, and a mail with
// no plain text leaves the mailbox's snippet in place, whose completeness is not known.
func TestInboxMailClippedFollowsTheBodyRead(t *testing.T) {
	for _, c := range []struct {
		name, text, want string
	}{
		{"clipped", strings.Repeat("x", 3001), "true"},
		{"whole", "A short body", "false"},
		{"no plain text", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newInboxHub(t)
			if err := h.inboxReady(t.Context()); err != nil {
				t.Fatal(err)
			}
			waitNeedsYouIdle(t, h.Service)
			now := time.Now()
			account := assistantwire.InboxMailAccount{ID: "a1", Email: "owner@example.com"}
			from, to := MailAddress{Name: "Sender", Email: "sender@example.com"}, []MailAddress{{Email: account.Email}}
			summary := assistantwire.InboxMailSummary{Mailbox: "INBOX", UID: 9, Subject: "Example", From: from, To: to,
				Date: now.Add(-time.Hour).Format(time.RFC3339), Snippet: "A snippet"}
			h.in.mail = &fakeMail{bodies: map[uint32]assistantwire.InboxMailMessage{9: {From: from, To: to, Subject: "Example", Date: summary.Date, Text: c.text}}}
			items := h.inbox.applyMail(account, []assistantwire.InboxMailSummary{summary}, now)
			if len(items) != 1 {
				t.Fatalf("%d items, want one", len(items))
			}
			it := items[0]
			// An earlier read's answer must not outlive a read that stored no text.
			stale := true
			it.MailBodyTruncated = &stale
			if !h.readMailBody(t.Context(), account, it) {
				t.Fatal("the body was not read")
			}
			out := mailClippedDetail(t, h.routes(), it.ID)
			if got := string(out["mail_body_truncated"]); got != c.want {
				t.Errorf("mail_body_truncated = %q, want %q", got, c.want)
			}
		})
	}
}
