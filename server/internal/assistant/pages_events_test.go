package assistant

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
)

// pageEventsTold runs q from start to end on a 100 ms clock, calling write at each tick; it gives each ui's tellings.
func pageEventsTold(q *pageEventQueue, start, end time.Time, write func(now time.Time)) map[string][]time.Time {
	out := map[string][]time.Time{}
	for now := start; !now.After(end); now = now.Add(100 * time.Millisecond) {
		write(now)
		uis, _ := q.due(now)
		for _, ui := range uis {
			out[ui] = append(out[ui], now)
		}
	}
	return out
}

// mostInWindow is the most of times in any pageEventWindow.
func mostInWindow(times []time.Time) int {
	slices.SortFunc(times, func(a, b time.Time) int { return a.Compare(b) })
	most, first := 0, 0
	for i, t := range times {
		for !times[first].After(t.Add(-pageEventWindow)) {
			first++
		}
		most = max(most, i-first+1)
	}
	return most
}

// 1000 writes to one page, about one a second for 20 minutes: its invalidations and the index's stay within the
// budget, each page's once a second at most and the index's times once a minute at most, and the last write is told.
func TestPageEventsBudget(t *testing.T) {
	var q pageEventQueue
	start := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	writes := 0
	last := start
	got := pageEventsTold(&q, start, start.Add(40*time.Minute), func(now time.Time) {
		if writes < 1000 && now.Sub(start) >= time.Duration(writes)*1200*time.Millisecond {
			q.add("pg_1", pageContent, now)
			q.add(pagesIndex, indexTime, now)
			writes++
			last = now
		}
	})
	if writes != 1000 {
		t.Fatalf("%d writes", writes)
	}
	var all []time.Time
	for _, times := range got {
		all = append(all, times...)
	}
	if n := mostInWindow(all); n > pageEventBudget {
		t.Fatalf("%d events in 15 minutes", n)
	}
	page, index := got["pg_1"], got[pagesIndex]
	for i := 1; i < len(page); i++ {
		if page[i].Sub(page[i-1]) < pageEventGather {
			t.Fatalf("the page told twice in a second: %v %v", page[i-1], page[i])
		}
	}
	for i := 1; i < len(index); i++ {
		if index[i].Sub(index[i-1]) < pageEventSlow {
			t.Fatalf("the index's times told twice in a minute: %v %v", index[i-1], index[i])
		}
	}
	if len(page) == 0 || page[len(page)-1].Before(last) {
		t.Fatalf("the last write (%v) is told: %v", last, page[len(page)-1])
	}
	if len(q.pending) != 0 {
		t.Fatalf("left waiting: %v", q.pending)
	}
	t.Logf("1000 writes: the page told %d times, the index %d, at most %d in 15 minutes", len(page), len(index), mostInWindow(all))
}

// Changes of one ui within a second are one telling; past the budget a status line is dropped and contents wait for
// the window, while a change of the list is told at once.
func TestPageEventsGatherAndDrop(t *testing.T) {
	var q pageEventQueue
	now := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	q.add("pg_1", pageStatus, now)
	q.add("pg_1", pageContent, now.Add(300*time.Millisecond))
	if uis, next := q.due(now.Add(900 * time.Millisecond)); len(uis) != 0 || !next.Equal(now.Add(time.Second)) {
		t.Fatalf("gathered for a second: %v %v", uis, next)
	}
	if uis, _ := q.due(now.Add(time.Second)); !slices.Equal(uis, []string{"pg_1"}) {
		t.Fatalf("one telling: %v", uis)
	}
	// the budget spent
	for range pageEventBudget {
		q.count(now.Add(2 * time.Second))
	}
	at := now.Add(5 * time.Second)
	q.add("pg_2", pageStatus, at)
	q.add("pg_3", pageContent, at)
	q.add(pagesIndex, indexList, at)
	uis, next := q.due(at.Add(time.Second))
	if !slices.Equal(uis, []string{pagesIndex}) {
		t.Fatalf("only the list's change: %v", uis)
	}
	if _, waiting := q.pending["pg_2"]; waiting {
		t.Fatal("a status line past the budget is dropped")
	}
	// the window holds pg_1's telling, the 150 counted and the list's: the contents go once that many have left it
	for range 3 {
		if next.Before(now.Add(pageEventWindow)) {
			t.Fatalf("the contents went before the window made room: %v", next)
		}
		if uis, next = q.due(next); len(uis) > 0 {
			break
		}
	}
	if !slices.Equal(uis, []string{"pg_3"}) || len(q.pending) != 0 {
		t.Fatalf("then told: %v %v", uis, q.pending)
	}
}

// The reserved places' keys in the code are exactly the catalogs' reserved_* keys, and every language has them.
func TestReservedPagePlaces(t *testing.T) {
	for _, l := range i18n.Langs {
		raw, err := os.ReadFile(filepath.Join("..", "i18n", "locales", string(l), "assistant-pages.json"))
		if err != nil {
			t.Fatal(err)
		}
		var words map[string]string
		if err := json.Unmarshal(raw, &words); err != nil {
			t.Fatal(err)
		}
		var keys []string
		for k := range words {
			if name, ok := strings.CutPrefix(k, "reserved_"); ok {
				keys = append(keys, name)
			}
		}
		slices.Sort(keys)
		if !slices.Equal(keys, reservedPlaces) {
			t.Fatalf("%s: the catalog's reserved places %v, the code's %v", l, keys, reservedPlaces)
		}
	}
	names := reservedPageNames()
	for _, want := range []string{"Settings", "设置", "設定", "Dashboards", "仪表盘", "ダッシュボード", "Persona", "Needs you"} {
		if !slices.Contains(names, want) {
			t.Fatalf("%q is not reserved: %v", want, names)
		}
	}
	agent, owner := pageTitleRules(false), pageTitleRules(true)
	for title, refused := range map[string][2]bool{
		"Settings": {true, true}, "ｓｅｔｔｉｎｇｓ": {true, true}, "Inbox (2)": {true, false}, "收件箱 ·": {true, false},
		"Morning run": {false, false}, "晨跑打卡": {false, false},
		// a name in Latin letters as a whole word only (Persona, ToDo), a Chinese or Japanese one anywhere
		"Inbox · work": {true, false}, "My Persona notes": {true, false}, "Personal budget": {false, false}, "Todoist export": {false, false},
		"看收件箱": {true, false}, "週のToDo": {true, false},
	} {
		_, a := agent.Check(title)
		_, o := owner.Check(title)
		if (a != nil) != refused[0] || (o != nil) != refused[1] {
			t.Fatalf("%q: an Agent's %v, the owner's %v", title, a, o)
		}
	}
}
