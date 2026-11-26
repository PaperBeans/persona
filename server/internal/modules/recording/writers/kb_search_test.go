package writers

import (
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/recordingwire"
	"reflect"
	"strings"
	"testing"
	"time"
)

// SearchKind reads one kind's files only: a meeting recording is found by the calendar title the phone sent and by
// the name of the calendar item it was matched to, even when the user renamed it, and a chat about it never uses up
// the limit first. The entries whose name matches come first, then as List ranks them (newest recording first).
func TestSearchKind(t *testing.T) {
	e := goldenEngine(t, nil)
	day := func(d, h int) time.Time { return time.Date(2026, 11, d, h, 0, 0, 0, goldenZone) }
	now := day(10, 18)
	c := &kb.Chat{ID: kb.ChatID("lark", "g1"), Source: "lark", SourceID: "g1", Kind: "group", Name: "Team", Created: now}
	if err := e.Store.PutChat(c); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.PutSummary(kb.KindChat, c.ID, "Ada booked the design review for Monday."); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.AppendMessages(c, []kb.Line{{At: day(9, 9), Sender: "Bo", Text: "design review moved to 10:00"}}); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.PutItem(&kb.Item{Kind: KindMeeting, ID: "m1", Title: "Design review", At: day(3, 10)}); err != nil {
		t.Fatal(err)
	}
	const (
		oldest  = "1a8fad5b-d9cb-469f-a165-70867728950e" // named after the phone's calendar title
		matched = "2a8fad5b-d9cb-469f-a165-70867728950e" // renamed; the calendar item it overlaps says Design review
		phone   = "3a8fad5b-d9cb-469f-a165-70867728950e" // renamed; the phone's calendar said Design review
		other   = "4a8fad5b-d9cb-469f-a165-70867728950e"
	)
	up := func(id string, start time.Time, title, calendar string, texts ...string) {
		t.Helper()
		u := recUpload(1, start, true, texts...)
		u.Title = title
		if calendar != "" {
			u.Calendar = &recordingwire.RecordingCalendar{Title: calendar}
		}
		putRecording(t, e, id, u, now)
	}
	up(oldest, day(1, 10), "", "Design review", "the export drops large entries")
	up(matched, day(3, 10), "Ada's notes", "", "we keep the cap")
	up(phone, day(8, 10), "Bo's notes", "Design review", "Bo adds the test")
	up(other, day(9, 10), "Lunch", "", "nothing about it")
	e.eng().matchRecordings(now)
	// The summarizer tags recordings: an older one tagged higher still comes after a newer one (kb_list's order).
	if r := recordingNow(t, e, matched); true {
		r.Tagging = Tagging{Importance: "P0", Relevance: 95}
		if err := ws(e.Store).putEntity(KindRecording, r.ID, r, &r.Tagging, false); err != nil {
			t.Fatal(err)
		}
	}
	if r := recordingNow(t, e, matched); r.Meeting == nil || r.Meeting.Name != "Design review" || r.Name != "Ada's notes" {
		t.Fatalf("matched: %+v %q", r.Meeting, r.Name)
	}

	// Search walks the chats first: one hit is the chat's.
	if hits := e.Search("design review", true, 1); len(hits) != 1 || !strings.HasPrefix(hits[0].Path, "chats/") {
		t.Fatalf("Search: %+v", hits)
	}
	var order []string
	for _, h := range e.SearchKind("Design REVIEW", KindRecording, true, 40) {
		kind, id, _ := entryOf(h.Path)
		if kind != KindRecording {
			t.Fatalf("a hit of another kind: %+v", h)
		}
		if len(order) == 0 || order[len(order)-1] != id {
			order = append(order, id)
		}
	}
	if want := []string{oldest, phone, matched}; !reflect.DeepEqual(order, want) {
		t.Fatalf("recordings found %v, want %v", order, want)
	}
	if hits := e.SearchKind("design review", KindRecording, true, 2); len(hits) != 2 {
		t.Fatalf("limit: %+v", hits)
	}
	if hits := e.SearchKind("design review", KindMeeting, true, 40); len(hits) != 1 || hits[0].Path != "meetings/m1.json" {
		t.Fatalf("meetings: %+v", hits)
	}
	// Chats: their message days only with messages, after the profiles.
	if hits := e.SearchKind("design review", kb.KindChat, false, 40); len(hits) != 1 || hits[0].Path != "chats/lark-g1/summary.md" {
		t.Fatalf("chats without the messages: %+v", hits)
	}
	if hits := e.SearchKind("design review", kb.KindChat, true, 40); len(hits) != 2 || !strings.Contains(hits[1].Path, "/messages/") {
		t.Fatalf("chats: %+v", hits)
	}
	for _, kind := range []string{"", "moments", "recording"} {
		if hits := e.SearchKind("design review", kind, true, 40); hits != nil {
			t.Fatalf("kind %q: %+v", kind, hits)
		}
	}
	if hits := e.SearchKind("  ", KindRecording, true, 40); hits != nil {
		t.Fatalf("no query: %+v", hits)
	}
}
