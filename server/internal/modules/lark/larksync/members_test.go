package larksync

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// pagedMembers answers a chat's member list with pages of 100, member i named "Member i", and member at (page,
// index) replaced by the given ids.
func pagedMembers(t *testing.T, f *fakeOpen, chatID string, pages int, place map[string][2]int) {
	t.Helper()
	f.handle("/open-apis/im/v1/chats/"+chatID+"/members", func(w http.ResponseWriter, r *http.Request) {
		p := 0
		if tok := r.URL.Query().Get("page_token"); tok != "" {
			p, _ = strconv.Atoi(strings.TrimPrefix(tok, "p"))
		}
		var items []any
		for i := range 100 {
			id, name := fmt.Sprintf("ou_m%d_%d", p, i), fmt.Sprintf("Member %d-%d", p, i)
			for want, at := range place {
				if at == [2]int{p, i} {
					id, name = want, "Named "+want
				}
			}
			items = append(items, map[string]any{"member_id": id, "name": name})
		}
		writeData(w, map[string]any{"items": items, "has_more": p+1 < pages, "page_token": fmt.Sprintf("p%d", p+1)})
	})
}

// A sender of a large external group is found past the old 20-page cap; the list is read only until each sender is
// found, resuming where it stopped, never twice.
func TestChatMembersReadOnDemandPastTheOldCap(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)
	b.Mode = larksyncwire.ModeIM
	for _, id := range []string{"ou_far", "ou_farther"} {
		f.handle("/open-apis/contact/v3/users/"+id, func(w http.ResponseWriter, _ *http.Request) {
			writeCode(w, http.StatusBadRequest, 41001, "external user")
		})
	}
	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{map[string]any{"chat_id": "oc_big", "name": "Big Group", "chat_mode": "group"}}})
	pagedMembers(t, f, "oc_big", 60, map[string][2]int{"ou_far": {24, 7}, "ou_farther": {31, 2}})
	f.handle("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		writeData(w, map[string]any{"items": []any{
			textMsg("om_1", "oc_big", "ou_far", "hello", testNow.Add(-5*time.Minute)),
			textMsg("om_2", "oc_big", "ou_farther", "hello again", testNow.Add(-4*time.Minute)),
		}})
	})
	f.data("/open-apis/search/v2/message", map[string]any{"items": []string{}})

	res, err := s.Run(t.Context(), b, staticToken("tok"), nil, newMemIndex())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, m := range res.Messages {
		got[m.ID] = m.SenderName
	}
	if got["om_1"] != "Named ou_far" || got["om_2"] != "Named ou_farther" {
		t.Fatalf("senders = %v", got)
	}
	if n := f.count("/open-apis/im/v1/chats/oc_big/members"); n != 32 {
		t.Errorf("member pages read = %d, want 32 (up to page 31, none twice, not the remaining 28)", n)
	}
	book := nameBookNames(res.Cursors[larksyncwire.SenderNamesCursor])
	if book["ou_far"] != "Named ou_far" || book["ou_farther"] != "Named ou_farther" || len(book) != 2 {
		t.Errorf("name book = %v, want the two senders only (not the whole member list)", book)
	}
}

// A member who left the group is in no member list: a later run names their messages from the book an earlier
// run filled from an @mention.
func TestNameBookNamesAMemberWhoLeft(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)
	b.Mode = larksyncwire.ModeIM
	f.handle("/open-apis/contact/v3/users/ou_left", func(w http.ResponseWriter, _ *http.Request) {
		writeCode(w, http.StatusBadRequest, 41001, "external user")
	})
	f.data("/open-apis/im/v1/chats", map[string]any{"items": []any{map[string]any{"chat_id": "oc_grp", "name": "Group", "chat_mode": "group"}}})
	f.data("/open-apis/search/v2/message", map[string]any{"items": []string{}})
	mention := textMsg("om_m", "oc_grp", "ou_other", "thanks @_user_1", testNow.Add(-10*time.Minute))
	mention["mentions"] = []any{map[string]any{"key": "@_user_1", "id": "ou_left", "id_type": "open_id", "name": "Former Member"}}
	f.data("/open-apis/im/v1/messages", map[string]any{"items": []any{mention}})

	first, err := s.Run(t.Context(), b, staticToken("tok"), nil, newMemIndex())
	if err != nil {
		t.Fatal(err)
	}
	raw := first.Cursors[larksyncwire.SenderNamesCursor]
	if nameBookNames(raw)["ou_left"] != "Former Member" {
		t.Fatalf("the mention did not reach the book: %s", raw)
	}

	// The next run reads an old message of theirs (a backfill, or a late page) with no mention in it.
	f.data("/open-apis/im/v1/messages", map[string]any{"items": []any{
		textMsg("om_old", "oc_grp", "ou_left", "an old message", testNow.Add(-5*time.Minute)),
	}})
	second, err := s.Run(t.Context(), b, staticToken("tok"), first.Cursors, newMemIndex())
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range second.Messages {
		if m.ID == "om_old" && m.SenderName != "Former Member" {
			t.Fatalf("om_old sender = %q, want the book's name", m.SenderName)
		}
	}
}

func TestNameBookKeepsTheNewestAndReadsAnything(t *testing.T) {
	if b := parseNameBook("not json"); len(b.names) != 0 || b.encode() != "" {
		t.Fatal("an unreadable book is an empty one, written only once something is learned")
	}
	now := time.Unix(1_800_000_000, 0)
	b := parseNameBook("")
	for i := range senderNamesMost + 3 {
		b.learn(fmt.Sprintf("ou_%05d", i), "Name", now.Add(time.Duration(i)*time.Second))
	}
	b.learn("all", "Everyone", now)
	b.learn("ou_blank", " ", now)
	raw := b.encode()
	var saved map[string]bookName
	if err := json.Unmarshal([]byte(raw), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved) != senderNamesMost {
		t.Fatalf("book holds %d, want %d", len(saved), senderNamesMost)
	}
	for _, gone := range []string{"ou_00000", "ou_00001", "ou_00002", "all", "ou_blank"} {
		if _, ok := saved[gone]; ok {
			t.Errorf("%s kept", gone)
		}
	}
	again := parseNameBook(raw)
	again.learn("ou_00010", "Name", now.Add(10*time.Second+time.Hour))
	if again.encode() != "" {
		t.Error("the same name learned within a day must not rewrite the book")
	}
	again.learn("ou_00010", "Renamed", now)
	if !strings.Contains(again.encode(), "Renamed") {
		t.Error("a new name is kept")
	}
}

// The sender-name backfill (v3) names a large group's member past 20 pages, a member who left from the book or from
// an @mention near their message, and a bot's single chat from the book; what nothing names stays unnamed, without
// an error.
func TestResolveNamesWithFallbackV3LargeGroupsAndMembersWhoLeft(t *testing.T) {
	clk := &fakeClock{t: testNow}
	f := newFakeOpen(t, clk)
	s := newTestSyncer(t, clk)
	b := binding(f, "primary~a1", larksyncwire.TierFull)
	for _, id := range []string{"ou_far", "ou_left_book", "ou_left_mention", "ou_unknown"} {
		f.handle("/open-apis/contact/v3/users/"+id, func(w http.ResponseWriter, _ *http.Request) {
			writeCode(w, http.StatusBadRequest, 41001, "external user")
		})
	}
	pagedMembers(t, f, "oc_big", 40, map[string][2]int{"ou_far": {33, 50}})
	sampleAt := testNow.Add(-48 * time.Hour)
	for _, id := range []string{"om_left_book", "om_left_mention", "om_unknown"} {
		f.data("/open-apis/im/v1/messages/"+id, map[string]any{"items": []any{}})
	}
	reply := textMsg("om_reply", "oc_big", "ou_far", "@_user_1 noted", sampleAt.Add(time.Minute))
	reply["mentions"] = []any{map[string]any{"key": "@_user_1", "id": "ou_left_mention", "id_type": "open_id", "name": "Mentioned Leaver"}}
	f.handle("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("container_id") != "oc_big" || q.Get("container_id_type") != "chat" {
			t.Errorf("mention scan read %v", q)
		}
		if q.Get("sort_type") == "ByCreateTimeAsc" && q.Get("start_time") == secs(sampleAt) {
			writeData(w, map[string]any{"items": []any{reply}})
			return
		}
		writeData(w, map[string]any{"items": []any{}})
	})
	f.data("/open-apis/im/v1/chats/oc_bot_dm", map[string]any{"chat_mode": "p2p", "name": ""})
	f.data("/open-apis/im/v1/chats/oc_bot_dm/members", map[string]any{"items": []any{map[string]any{"member_id": testOwner, "name": "Owner"}}})
	book := parseNameBook("")
	book.learn("ou_left_book", "Booked Leaver", testNow.Add(-30*24*time.Hour))
	book.learn("cli_other_bot", "Other Bot", testNow.Add(-24*time.Hour))

	got, err := s.ResolveNamesWithFallback(t.Context(), b, staticToken("tok"), larksyncwire.NameResolveRequest{
		OpenIDs: []larksyncwire.OpenID{"ou_far", "ou_left_book", "ou_left_mention", "ou_unknown"},
		ChatSenders: map[string][]string{
			"oc_big":    {"ou_far", "ou_left_book", "ou_left_mention", "ou_unknown"},
			"oc_bot_dm": {"cli_other_bot"},
		},
		P2PChatIDs:     []larksyncwire.ChatID{"oc_bot_dm"},
		SampleMessages: map[string]string{"ou_left_book": "om_left_book", "ou_left_mention": "om_left_mention", "ou_unknown": "om_unknown"},
		SampleTimes:    map[string]time.Time{"ou_left_book": sampleAt, "ou_left_mention": sampleAt, "ou_unknown": sampleAt},
		SampleChats:    map[string]string{"ou_left_book": "oc_big", "ou_left_mention": "oc_big", "ou_unknown": "oc_big"},
		NameBook:       book.encode(),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"ou_far":          "Named ou_far",
		"ou_left_book":    "Booked Leaver",
		"ou_left_mention": "Mentioned Leaver",
		"cli_other_bot":   "Other Bot",
	}
	for id, name := range want {
		if got.UserNames[id] != name {
			t.Errorf("UserNames[%s] = %q, want %q", id, got.UserNames[id], name)
		}
	}
	if got.P2PChatNames["oc_bot_dm"] != "Other Bot" {
		t.Errorf("bot single chat name = %q, want the book's", got.P2PChatNames["oc_bot_dm"])
	}
	if name, ok := got.UserNames["ou_unknown"]; ok {
		t.Errorf("nothing names ou_unknown, got %q", name)
	}
	if n := f.count("/open-apis/im/v1/chats/oc_big/members"); n != 40 {
		t.Errorf("member pages = %d, want 40 (the whole list once: two senders are in none of it)", n)
	}
	// One scan for the chat and hour: both ways for the still-unnamed senders, stopping early when all are named.
	if n := f.count("/open-apis/im/v1/messages"); n == 0 || n > 2*mentionScanPagesMost {
		t.Errorf("mention scan pages = %d", n)
	}
}
