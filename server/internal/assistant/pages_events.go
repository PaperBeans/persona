package assistant

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/extmgr"
)

// What the apps are told of the assistant's pages (Dashboards, #15017; docs/protocol/protocol.md "WebSocket"):
// assistant_pages ({}) when the places change (a page kept, pinned, unpinned, renamed, removed, restored or deleted,
// the switch turned on or off), so they read the sidebar's list again; and extension_ui {extension:"-pages", id,
// invalidate:true} when what a page draws changed, so an app showing it draws it again and one that is not only marks
// it. Only these few bytes go out, never a page: the hub's event log replays at most a few hundred messages to an app
// that comes back (hub/eventlog.go), and the pages must not crowd the others out of it.
//
// So the invalidations are gathered: each page's once a second at most, the index's at once for a change of its list
// and once a minute at most for a time it shows ("Updated today 8:02"). All of them together keep to a budget of
// pageEventBudget in pageEventWindow: past it a page's status line (a tap delivered, its turn started or ended) is not
// told at all (the page shows it when drawn next), and a page's contents and the index's times go once a minute at
// most, and only as the window makes room. A change of the list is always told. A write (S3's page tool) calls
// pageWritten; an owner's change of the places calls pagesListChanged.

// pageChange is what changed of a -pages ui since it was last told.
type pageChange uint8

const (
	// pageStatus: a page's footer status line (a tap's progress) and nothing else.
	pageStatus pageChange = 1 << iota
	// pageContent: what a page draws (its contents, title, pin...).
	pageContent
	// indexTime: a time the index shows (a page's "Updated ...").
	indexTime
	// indexList: the index's list (a page kept, pinned, renamed, removed, restored or deleted).
	indexList
)

const (
	// pageEventGather is how long the changes of a ui are gathered into one invalidation.
	pageEventGather = time.Second
	// pageEventSlow is how often the index's times are told, and a page's contents past the budget.
	pageEventSlow = time.Minute
	// pageEventWindow and pageEventBudget: at most pageEventBudget -pages events in any pageEventWindow, the list's
	// changes aside (they are always told, and counted).
	pageEventWindow = 15 * time.Minute
	pageEventBudget = 150
)

// pageEventQueue is the invalidations waiting to be told and the events told in the window, in a clock the caller
// gives (pure: pageEvents drives it on timers, tests on a clock of their own).
type pageEventQueue struct {
	pending map[string]pendingPageEvent
	last    map[string]time.Time // when each ui was last told
	sent    []time.Time          // the -pages events told in the window, oldest first
}

type pendingPageEvent struct {
	change pageChange
	since  time.Time
}

// add records that ui changed at now.
func (q *pageEventQueue) add(ui string, c pageChange, now time.Time) {
	if q.pending == nil {
		q.pending, q.last = map[string]pendingPageEvent{}, map[string]time.Time{}
	}
	p, waiting := q.pending[ui]
	if !waiting {
		p.since = now
	}
	p.change |= c
	q.pending[ui] = p
}

// count records an event told outside the queue at now (assistant_pages).
func (q *pageEventQueue) count(now time.Time) { q.sent = append(q.sent, now) }

// due takes out the uis to tell at now, in their order, and says when the next one is due (zero: none waits).
func (q *pageEventQueue) due(now time.Time) (out []string, next time.Time) {
	cut := 0
	for cut < len(q.sent) && !q.sent[cut].After(now.Add(-pageEventWindow)) {
		cut++
	}
	q.sent = q.sent[cut:]
	for _, ui := range slices.Sorted(maps.Keys(q.pending)) {
		p := q.pending[ui]
		full := len(q.sent) >= pageEventBudget
		list := p.change&indexList != 0
		interval := pageEventGather
		switch {
		case list:
		case p.change&pageContent == 0 && ui == pagesIndex: // the index's times only
			interval = pageEventSlow
		case p.change&pageContent == 0 && full: // a status line past the budget: drawn when the page is next drawn
			delete(q.pending, ui)
			continue
		case full:
			interval = pageEventSlow
		}
		at := p.since.Add(pageEventGather)
		if last, told := q.last[ui]; told && last.Add(interval).After(at) {
			at = last.Add(interval)
		}
		if full && !list && q.sent[0].Add(pageEventWindow).After(at) {
			at = q.sent[0].Add(pageEventWindow) // the window makes room when its oldest event leaves it
		}
		if at.After(now) {
			if next.IsZero() || at.Before(next) {
				next = at
			}
			continue
		}
		out = append(out, ui)
		q.last[ui] = now
		q.sent = append(q.sent, now)
		delete(q.pending, ui)
	}
	return out, next
}

// pageEvents drives the queue on a timer and holds the pages' manual-refresh runtime state.
type pageEvents struct {
	mu      sync.Mutex
	queue   pageEventQueue
	timer   *time.Timer
	timerAt time.Time
	refresh pageRefreshState
}

// pageWritten: what page id draws changed (an Agent wrote it), and so the time the index shows for it.
func (s *Service) pageWritten(id string) {
	s.pageChanged(id, pageContent)
	s.pageChanged(pagesIndex, indexTime)
}

// pageReadingChanged: a watch reading landed on page p; invalidate the page, the index glance and its Overview card.
func (s *Service) pageReadingChanged(p *db.AssistantPageRow) {
	if p == nil {
		return
	}
	s.pageChanged(p.ID, pageContent)
	s.pageChanged(pagesIndex, pageContent)
	if len(p.Watch) > 0 && p.OnOverview {
		s.pageChanged(overviewCardID(p.ID), pageContent)
	}
}

// pageChanged: ui of the pages changed as c says; the apps are told when its turn comes.
func (s *Service) pageChanged(ui string, c pageChange) {
	e := &s.pages.events
	e.mu.Lock()
	e.queue.add(ui, c, time.Now())
	e.mu.Unlock()
	s.tellPageEvents()
}

// pagesListChanged: the places changed. The apps read the list again at once, and the index is drawn again.
func (s *Service) pagesListChanged(ctx context.Context) {
	s.event(ctx, map[string]any{"type": "assistant_pages"})
	e := &s.pages.events
	e.mu.Lock()
	now := time.Now()
	e.queue.count(now)
	e.queue.add(pagesIndex, indexList, now)
	e.mu.Unlock()
	s.tellPageEvents()
}

// tellPageEvents tells the apps the invalidations due now and sets the timer for the next.
func (s *Service) tellPageEvents() {
	e := &s.pages.events
	e.mu.Lock()
	now := time.Now()
	uis, next := e.queue.due(now)
	if !next.IsZero() && (e.timer == nil || next.Before(e.timerAt)) {
		if e.timer != nil {
			e.timer.Stop()
		}
		e.timerAt = next
		e.timer = time.AfterFunc(next.Sub(now), func() {
			e.mu.Lock()
			e.timer, e.timerAt = nil, time.Time{}
			e.mu.Unlock()
			s.tellPageEvents()
		})
	}
	e.mu.Unlock()
	if len(uis) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, ui := range uis {
		s.event(ctx, map[string]any{"type": "extension_ui", "extension": extmgr.PagesExtension, "id": ui, "invalidate": true})
	}
}

// pagesNavKey is a -pages ui's key in the sidebar's arrangement (navigation.go), as the web's sidebar names it.
func pagesNavKey(ui string) string { return "p:" + extmgr.PagesExtension + "/" + ui }

// showPagesInSidebar puts the index's sidebar key, and page id's (when it is not ""), in the navigation record's
// shown (and takes them out of hidden), and tells the apps: the owner's keep or pin asked for them in the sidebar, and
// the web keeps an extension's page past its first few in the list only when shown says so (web/src/lib/sidebar-pages.ts
// EXTENSION_CAP). Keys there already change nothing; the owner's later arrangement is theirs.
func (s *Service) showPagesInSidebar(ctx context.Context, id string) error {
	keys := []string{pagesNavKey(pagesIndex)}
	if id != "" {
		keys = append(keys, pagesNavKey(id))
	}
	st := s.store()
	if st == nil {
		return errors.New("the sidebar's arrangement needs a database")
	}
	s.navMu.Lock()
	defer s.navMu.Unlock()
	var n navigation
	if _, err := st.Setting(ctx, navigationKey, &n); err != nil {
		return err
	}
	pages := sidebarPages{Order: []string{}, Shown: []string{}, Hidden: []string{}}
	if n.Pages != nil {
		pages = *n.Pages
	}
	shown := slices.Clone(pages.Shown)
	for _, k := range keys {
		if !slices.Contains(shown, k) {
			shown = append(shown, k)
		}
	}
	hidden := slices.DeleteFunc(slices.Clone(pages.Hidden), func(k string) bool { return slices.Contains(keys, k) })
	if len(shown) == len(pages.Shown) && len(hidden) == len(pages.Hidden) {
		return nil
	}
	pages.Shown, pages.Hidden = shown, hidden
	if pages.Order == nil {
		pages.Order = []string{}
	}
	if pages.Hidden == nil {
		pages.Hidden = []string{}
	}
	if err := pages.check(); err != nil {
		return err
	}
	n.Pages = &pages
	if err := st.PutSetting(ctx, navigationKey, n); err != nil {
		return err
	}
	s.event(ctx, map[string]any{"type": "navigation", "pages": n.Pages})
	return nil
}
