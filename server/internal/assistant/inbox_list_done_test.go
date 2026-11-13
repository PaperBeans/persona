package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"testing"
	"time"
)

// cappedItems are two open items, inboxListDone+20 mail items done within the window a second apart (with two pairs
// done at the same moment, one pair across the cut), a GitHub item done last, and a mail item done past the window.
func cappedItems(now time.Time) []*InboxItem {
	items := []*InboxItem{
		{ID: "mail:open-1", Source: "mail", Kind: KindMail, Title: "open", Last: "o1"},
		{ID: "mail:open-2", Source: "mail", Kind: KindMail, Title: "open", Last: "o2"},
	}
	gh := now.Add(-time.Second)
	items = append(items, &InboxItem{ID: "github:done", Source: "github", Kind: "pr", Title: "pr", Last: "g", DoneAt: &gh,
		Connector: &InboxConnector{Thread: "t"}})
	for i := range inboxListDone + 20 {
		at := now.Add(-time.Minute - time.Duration(i)*time.Second)
		switch i {
		case 5, inboxListDone: // done at the same moment as the one before it (the second pair across the cut)
			at = now.Add(-time.Minute - time.Duration(i-1)*time.Second)
		}
		items = append(items, &InboxItem{ID: fmt.Sprintf("mail:d%03d", i), Source: "mail", Kind: KindMail, Title: "filed",
			Last: "d", DoneAt: &at, DoneBy: "user"})
	}
	aged := now.Add(-inboxKeepDone - time.Minute)
	return append(items, &InboxItem{ID: "mail:aged", Source: "mail", Kind: KindMail, Title: "filed long ago", Last: "a",
		DoneAt: &aged, DoneBy: "user"})
}

// newestDone is what a list should carry of items: every open one and the newest inboxListDone of those done within
// the window, newest done first and by id (descending) among those of the same moment, as the history pages them.
func newestDone(items []*InboxItem, now time.Time, all bool) []string {
	var open, done []*InboxItem
	for _, it := range items {
		switch {
		case !all && it.Connector != nil:
		case it.DoneAt == nil:
			open = append(open, it)
		case !it.DoneAt.Before(now.Add(-inboxKeepDone)):
			done = append(done, it)
		}
	}
	sort.Slice(done, func(i, j int) bool {
		if !done[i].DoneAt.Equal(*done[j].DoneAt) {
			return done[i].DoneAt.After(*done[j].DoneAt)
		}
		return done[i].ID > done[j].ID
	})
	var out []string
	for _, it := range open {
		out = append(out, it.ID)
	}
	for _, it := range done[:min(len(done), inboxListDone)] {
		out = append(out, it.ID)
	}
	slices.Sort(out)
	return out
}

// listIDs is the ids of a list's answer, sorted.
func listIDs(t *testing.T, body []byte) []string {
	t.Helper()
	var out struct{ Items []json.RawMessage }
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for id := range byID(t, out.Items) {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// With more done items within the window than inboxListDone, the list carries every open item and only the newest
// inboxListDone done ones, the same from the kept bytes as encoded afresh, with and without the connectors' items
// (which count only in the list that shows them).
func TestInboxListCarriesTheNewestDone(t *testing.T) {
	now := time.Now()
	items := cappedItems(now)
	h := memoInbox(t, items)
	for _, tc := range []struct {
		query string
		all   bool
	}{{"?sources=all", true}, {"", false}} {
		want := newestDone(items, now, tc.all)
		if len(want) != 2+inboxListDone {
			t.Fatalf("%q: the fixture expects %d items, got %d", tc.query, 2+inboxListDone, len(want))
		}
		for round := range 2 { // encoded, then kept
			got, ref := getInbox(t, h, tc.query)
			if string(got) != string(ref) {
				t.Fatalf("%q round %d: the kept bytes differ from apierr.WriteJSON's", tc.query, round)
			}
			if ids := listIDs(t, got); !slices.Equal(ids, want) {
				t.Fatalf("%q round %d: listed %d items, want %d:\n got %v\nwant %v", tc.query, round, len(ids), len(want), ids, want)
			}
		}
	}
	// The cut falls between the two done at the same moment, by id: the larger is listed, as the history has it first.
	got, _ := getInbox(t, h, "")
	ids := listIDs(t, got)
	last, next := fmt.Sprintf("mail:d%03d", inboxListDone), fmt.Sprintf("mail:d%03d", inboxListDone-1)
	if _, ok := slices.BinarySearch(ids, last); !ok {
		t.Fatalf("%s (the larger id of the pair at the cut) is not listed", last)
	}
	if _, ok := slices.BinarySearch(ids, next); ok {
		t.Fatalf("%s (the smaller id of the pair at the cut) is listed", next)
	}
	for _, id := range ids {
		if id == "mail:aged" || id == "github:done" {
			t.Fatalf("%s is listed without connectors or past the window", id)
		}
	}
}

// An item marked done now is the newest done item: the next list carries it however old it is and however many were
// done before it, so the app that marked it finds it (its Undo, its Done row); the oldest one listed before leaves.
func TestInboxListCarriesWhatWasJustDone(t *testing.T) {
	now := time.Now()
	items := cappedItems(now)
	h := memoInbox(t, items)
	h.inbox.mu.Lock()
	h.inbox.items["mail:open-2"].UpdatedAt = now.Add(-6 * 24 * time.Hour)
	h.inbox.mu.Unlock()
	before, _ := getInbox(t, h, "")
	first := listIDs(t, before)
	if _, err := h.setInboxDone(context.Background(), "mail:open-2", true, false); err != nil {
		t.Fatal(err)
	}
	after, _ := getInbox(t, h, "")
	ids := listIDs(t, after)
	if len(ids) != len(first)-1 { // one open item fewer, as many done ones
		t.Fatalf("listed %d items after one was done, %d before", len(ids), len(first))
	}
	var out struct {
		Items []struct {
			ID     string     `json:"id"`
			DoneAt *time.Time `json:"done_at"`
		}
	}
	if err := json.Unmarshal(after, &out); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range out.Items {
		found = found || it.ID == "mail:open-2" && it.DoneAt != nil
	}
	if !found {
		t.Fatal("the item just marked done is not listed as done")
	}
	gone := fmt.Sprintf("mail:d%03d", inboxListDone) // the oldest listed without connectors
	if _, ok := slices.BinarySearch(ids, gone); ok {
		t.Fatalf("%s is still listed: the cut did not move", gone)
	}
}

// A done item that falls past the cut is "removed" in a delta, and the delta applied to the list it followed is the
// whole list now.
func TestInboxSinceRemovesWhatFallsPastTheCut(t *testing.T) {
	now := time.Now()
	items := cappedItems(now)
	h := memoInbox(t, items)
	first := getSince(t, h, "", false)
	held := byID(t, first.Items)
	if len(held) != 2+inboxListDone {
		t.Fatalf("the whole list holds %d items, want %d", len(held), 2+inboxListDone)
	}
	justNow := now.Add(time.Second)
	change(h, "mail:open-1", func(it *InboxItem) { it.DoneAt, it.DoneBy = &justNow, "user" })
	d := getSince(t, h, first.Cursor, false)
	if !d.Delta {
		t.Fatal("not a delta")
	}
	for id, raw := range byID(t, d.Items) {
		held[id] = raw
	}
	var removed []string
	for _, r := range d.Removed {
		removed = append(removed, r.ID)
		delete(held, r.ID)
	}
	if want := []string{fmt.Sprintf("mail:d%03d", inboxListDone)}; !slices.Equal(removed, want) {
		t.Fatalf("removed %v, want %v (the oldest listed done item)", removed, want)
	}
	whole := getSince(t, h, "", false)
	if want := byID(t, whole.Items); !mapsEqual(held, want) {
		t.Fatalf("old list + delta != whole list now:\n%v\n%v", keys(held), keys(want))
	}
}

// What the list leaves out is all on Done's history, newest first, and still opens by id.
func TestInboxHistoryHoldsWhatTheListLeavesOut(t *testing.T) {
	s := newTestService(t, newFakeHub())
	if err := s.inboxReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	items := cappedItems(now)
	s.inbox.mu.Lock()
	for i, it := range items {
		it.UpdatedAt = now.Add(-time.Duration(i) * time.Minute)
		s.inbox.items[it.ID] = it
	}
	s.inbox.mu.Unlock()
	listed := map[string]bool{}
	for _, it := range s.inboxList(false) {
		listed[it.ID] = true
	}
	got, _ := historyIDs(t, s, "limit=200")
	var want, left []string
	for _, it := range items {
		if it.DoneAt != nil && it.Connector == nil {
			want = append(want, it.ID)
			if !listed[it.ID] {
				left = append(left, it.ID)
			}
		}
	}
	if len(left) < 20 {
		t.Fatalf("the list left out %d done items, want at least 20", len(left))
	}
	slices.Sort(want)
	sorted := slices.Clone(got)
	slices.Sort(sorted)
	if !slices.Equal(sorted, want) {
		t.Fatalf("the history holds %d done items, want all %d:\n%v", len(got), len(want), got)
	}
	if got[0] != "mail:d000" || got[len(got)-1] != "mail:aged" {
		t.Fatalf("the history is not newest first: %s … %s", got[0], got[len(got)-1])
	}
	for _, id := range left {
		if code, out := inboxCall(t, s, "GET", "/api/v1/inbox/"+id, ""); code != http.StatusOK || out["id"] != id {
			t.Fatalf("GET %s: %d %v", id, code, out)
		}
		if id != "mail:aged" {
			if _, ok := s.workbenchItem(id); !ok {
				t.Fatalf("a Feishu card's button can't find %s, done within the window", id)
			}
		}
	}
	if _, ok := s.workbenchItem("mail:aged"); ok {
		t.Fatal("a Feishu card's button finds an item done past the window")
	}
}

// Folders count the list's window, not the list: a folder also counts its done items past the cut (GET
// /api/v1/inbox/folders and the inbox_folders tool say how many items a folder has, not how many rows are listed).
func TestInboxFoldersCountTheWindow(t *testing.T) {
	now := time.Now()
	items := cappedItems(now)
	for _, it := range items {
		if it.Connector == nil {
			it.Folder = "work"
		}
	}
	h := memoInbox(t, items)
	listed := 0
	for _, it := range h.inboxList(false) {
		if it.Folder == "work" {
			listed++
		}
	}
	h.inbox.mu.Lock()
	folders := h.inbox.folders(false)
	h.inbox.mu.Unlock()
	// two open items and inboxListDone+20 done within the window (mail:aged is past it); the list has the newest
	// inboxListDone of the done ones
	if len(folders) != 1 || folders[0].Count != 2+inboxListDone+20 || folders[0].Open != 2 || listed != 2+inboxListDone {
		t.Fatalf("folders %+v, %d listed in the folder", folders, listed)
	}
}
