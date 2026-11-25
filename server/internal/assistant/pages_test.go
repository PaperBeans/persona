package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistant/repositorystore"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extmgr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// pagesHub is the fake hub with what the pages' tests read: every message the apps were sent, whole, and the hub's
// web address (the link to a conversation in a page's footer).
type pagesHub struct {
	*fakeHub
	lock     sync.Mutex
	messages []map[string]any
}

func (h *pagesHub) Broadcast(ctx context.Context, b Broadcast) {
	var m map[string]any
	_ = json.Unmarshal(b.Message, &m)
	h.lock.Lock()
	h.messages = append(h.messages, m)
	h.lock.Unlock()
	h.fakeHub.Broadcast(ctx, b)
}

func (h *pagesHub) AppCall(ctx context.Context, p AppCall) (json.RawMessage, error) {
	if p.AppID == AppFeishu && p.Method == FeishuStatusGet {
		return json.Marshal(FeishuStatus{PublicURL: "https://hub.example.com/"})
	}
	return h.fakeHub.AppCall(ctx, p)
}

// sent are the messages of type typ the apps were sent.
func (h *pagesHub) sent(typ string) []map[string]any {
	h.lock.Lock()
	defer h.lock.Unlock()
	var out []map[string]any
	for _, m := range h.messages {
		if m["type"] == typ {
			out = append(out, m)
		}
	}
	return out
}

// pagesTestService is a service on a real database with no pages yet, its main conversation main-1 and conversation
// conv-1 on machine "server", and Dashboards on.
func pagesTestService(t *testing.T, also ...func(string, ...any)) (*Service, *pagesHub, *db.DB) {
	t.Helper()
	url := os.Getenv("PERSONA_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PERSONA_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	database, err := db.Open(ctx, url, db.ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `TRUNCATE assistant_pages CASCADE`); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(ctx)
	f := newFakeHub().withMachine()
	f.setSessions("server", proto.Session{SessionID: "main-1", Assistant: true}, proto.Session{SessionID: "conv-1", Assistant: true})
	hub := &pagesHub{fakeHub: f}
	st := repositorystore.New(database)
	logf := testLogf(t)
	if len(also) > 0 {
		// one guarded logger for the whole test: a background job that logs after it ended must not reach t
		base := logf
		logf = func(format string, args ...any) {
			base(format, args...)
			also[0](format, args...)
		}
	}
	s := withSettings(t, f, New(Options{Hub: hub, Store: func() Store { return st }, Logf: logf}))
	setSettings(t, s, func(v *Settings) {
		v.MachineID, v.Main, v.Timezone = "server", "main-1", "UTC"
		on := true
		v.Dashboards = &on
	})
	return s, hub, database
}

// A page with a metric, a table (UI version 3), a form sent by Done today, a confirmed Skip today, a detail page that
// pops, and a link.
const runPageView = `{"fallback":"Morning run","sections":[{"key":"streak","items":[
	{"type":"metric","key":"streak","label":"Streak","value":"12 days"},
	{"type":"table","key":"week","columns":[{"title":"Day"}],"rows":[["Mon"]]},
	{"type":"field","key":"minutes","name":"minutes","label":"Minutes","kind":"number"},
	{"type":"button","key":"done","title":"Done today","action":{"id":"done","reply":"Done today","submit":true}},
	{"type":"button","key":"skip","title":"Skip today","action":{"id":"skip","reply":"Skip today","confirm":"Skip today?"}},
	{"type":"button","key":"history","title":"History","action":{"id":"history","push":{"page":"history"}}},
	{"type":"button","key":"guide","title":"Guide","action":{"id":"guide","open":"https://example.com/guide"}}]}],
	"pages":{"history":{"fallback":"History","sections":[{"key":"h","items":[{"type":"text","key":"h1","text":"Last week"},
	{"type":"button","key":"back","title":"Back","action":{"id":"back","pop":true}}]}]}}}`

// keptPage is a page of conversation conv-1 on server (owner "main": the main conversation's) titled title, drafted at
// at, kept now (pinned when pin).
func keptPage(t *testing.T, database *db.DB, title, owner string, at time.Time, pin bool) *db.AssistantPageRow {
	t.Helper()
	ctx := context.Background()
	id := db.NewAssistantPageID()
	p := db.NewAssistantPage{ID: id, Title: title, Icon: "heart", OwnerKind: db.AssistantPageOwnerConversation, OwnerMachine: "server", OwnerRef: owner,
		CreatedMachine: "server", CreatedSession: "conv-1", OfferView: "view-" + id, View: json.RawMessage(runPageView), At: at}
	if owner == "main" {
		p.OwnerKind, p.OwnerMachine, p.OwnerRef = db.AssistantPageOwnerMain, "", ""
	}
	if _, err := database.CreateAssistantPage(ctx, p); err != nil {
		t.Fatal(err)
	}
	row, _, err := database.KeepAssistantPage(ctx, db.AssistantPageKeep{OfferView: "view-" + id, Device: "phone", Pin: pin, At: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	return row
}

// ui asks the pages for ui as the phone does, at UI version 10 unless the request says.
func pagesUI(t *testing.T, s *Service, ui string, in wire.UIRequest) (extension.ActionResult, error) {
	t.Helper()
	if in.Context == nil {
		in.Context = &wire.UIContext{UIVersion: 10}
	}
	return s.pageUI(context.Background(), i18n.En, ui, in, "phone")
}

func mustPage(t *testing.T, s *Service, ui string, in wire.UIRequest) extension.ActionResult {
	t.Helper()
	res, err := pagesUI(t, s, ui, in)
	if err != nil {
		t.Fatalf("%s %+v: %v", ui, in, err)
	}
	return res
}

func pageState2(p string, v int64) string { return pageState{P: p, V: v}.encode() }

// item is the component keyed key in v ({} when it has none).
func pageItem(v *wire.View, key string) wire.Component {
	for _, s := range v.Sections {
		for _, e := range s.Items {
			if e.Key == key {
				return e
			}
		}
	}
	return wire.Component{}
}

func pageSection(v *wire.View, key string) (wire.Section, bool) {
	for _, s := range v.Sections {
		if s.Key == key {
			return s, true
		}
	}
	return wire.Section{}, false
}

func pagesRefused(err error, status int) bool {
	var pe *extmgr.PagesError
	return errors.As(err, &pe) && pe.Status == status
}

// The places, the index and a page as the apps get them: fitted to the app's UI version, the page's title as the
// View's, its actions an extension page's own, the hub's footer and state; nothing while Dashboards are off.
func TestPagesPlacesIndexAndPage(t *testing.T) {
	s, _, database := pagesTestService(t)
	ctx := context.Background()
	if got := s.pagePlaces(ctx, i18n.En); got != nil {
		t.Fatalf("nothing kept, nothing listed: %v", got)
	}
	day := 25 * time.Hour
	run := keptPage(t, database, "Morning run", "conv-1", time.Now().Add(-3*day), true)
	reading := keptPage(t, database, "Reading list", "main", time.Now().Add(-2*day), false)
	trip := keptPage(t, database, "Weekend trip", "main", time.Now().Add(-day), false)
	if _, _, err := database.RemoveAssistantPage(ctx, trip.ID, db.AssistantPageRemovedByOwner, time.Now()); err != nil {
		t.Fatal(err)
	}
	places := s.pagePlaces(ctx, i18n.Ja)
	if len(places) != 2 || places[0].ID != pagesIndex || places[0].Title != "ダッシュボード" || places[0].Icon != "chart" || places[0].Placement != "nav_page" ||
		places[1].ID != run.ID || places[1].Title != "Morning run" || places[1].Extension != extmgr.PagesExtension || places[1].Badge != nil {
		t.Fatalf("the index, then the pinned page: %+v", places)
	}

	res := mustPage(t, s, pagesIndex, wire.UIRequest{})
	v := res.View
	pinned, _ := pageSection(v, "pinned")
	all, _ := pageSection(v, "all")
	removed, _ := pageSection(v, "removed")
	if v.Title != "Dashboards" || len(pinned.Items) != 1 || len(all.Items) != 1 || len(removed.Items) != 1 {
		t.Fatalf("Pinned, All, Recently removed: %+v", v)
	}
	row := pageItem(v, "page:"+run.ID)
	if !strings.HasPrefix(row.Subtitle, "Updated ") || row.Action == nil || row.Action.Push == nil || row.Action.Push.UI != run.ID {
		t.Fatalf("a row opens its page: %+v", row)
	}
	gone := pageItem(v, "removed:"+trip.ID)
	if len(gone.Actions) != 2 || gone.Actions[0].Action.ID != "persona.restore:"+trip.ID || gone.Actions[1].Action.ID != "persona.delete:"+trip.ID ||
		gone.Actions[1].Action.Confirm == "" || !strings.HasPrefix(gone.Subtitle, "Removed ") {
		t.Fatalf("restore, and delete with a confirmation: %+v", gone)
	}
	_ = reading

	res = mustPage(t, s, run.ID, wire.UIRequest{})
	v = res.View
	if v.Title != "Morning run" || v.State != pageState2("", run.Version) || v.Fallback != "" || len(v.Pages) != 0 {
		t.Fatalf("the page's title and state: %q %q", v.Title, v.State)
	}
	for _, key := range []string{"done", "skip", "guide"} {
		a := pageItem(v, key).Action
		if a == nil || a.Reply != "" || a.Open != "" || a.Pop || a.ID != key {
			t.Fatalf("%s: an extension page's action: %+v", key, a)
		}
	}
	if a := pageItem(v, "history").Action; a == nil || a.Push == nil || a.Push.UI != run.ID || a.Push.Page != "" || a.Push.State != pageState2("history", run.Version) {
		t.Fatalf("a detail page is this page with its state: %+v", a)
	}
	if pageItem(v, "week").Type != "table" {
		t.Fatal("UI version 10 draws a table")
	}
	footer, ok := pageSection(v, "persona.footer")
	if !ok || pageItem(v, "persona.updated").Text == "" || pageItem(v, "persona.pin").Action.ID != "persona.unpin" ||
		pageItem(v, "persona.rename").Action.Push == nil || pageItem(v, "persona.remove").Action.Confirm == "" || pageItem(v, "persona.status").Type != "" {
		t.Fatalf("the hub's footer: %+v", footer)
	}
	// an older app gets what it draws
	old := mustPage(t, s, run.ID, wire.UIRequest{Context: &wire.UIContext{UIVersion: 2}}).View
	if pageItem(old, "week").Type == "table" || old.Title != "Morning run" {
		t.Fatalf("fitted to UI version 2: %+v", old.Sections)
	}
	// the detail page
	detail := mustPage(t, s, run.ID, wire.UIRequest{State: pageState2("history", run.Version)}).View
	if pageItem(detail, "h1").Text != "Last week" || detail.Title != "Morning run" || detail.State != pageState2("history", run.Version) {
		t.Fatalf("the detail page: %+v", detail)
	}
	if res := mustPage(t, s, run.ID, wire.UIRequest{Action: "back", State: pageState2("history", run.Version)}); !res.Pop {
		t.Fatalf("its pop is answered: %+v", res)
	}
	if res := mustPage(t, s, run.ID, wire.UIRequest{Action: "guide", State: pageState2("", run.Version)}); res.OpenURL != "https://example.com/guide" {
		t.Fatalf("its link is answered: %+v", res)
	}
	// a removed page opens to say so; a draft or another id is no page
	if v := mustPage(t, s, trip.ID, wire.UIRequest{}).View; pageItem(v, "persona.gone").Button == nil {
		t.Fatalf("a removed page: %+v", v)
	}
	if _, err := pagesUI(t, s, "pg_0000000000000000", wire.UIRequest{}); !pagesRefused(err, http.StatusNotFound) {
		t.Fatalf("no such page: %v", err)
	}
	// off: nothing listed, nothing drawn
	setSettings(t, s, func(v *Settings) { v.Dashboards = nil })
	if got := s.pagePlaces(ctx, i18n.En); got != nil {
		t.Fatalf("off lists nothing: %v", got)
	}
	if _, err := pagesUI(t, s, pagesIndex, wire.UIRequest{}); !pagesRefused(err, http.StatusNotFound) {
		t.Fatalf("off draws nothing: %v", err)
	}
}

// The owner's actions: at most 3 pinned (a pin puts the sidebar keys in the navigation record), rename (a reserved
// name refused), remove with a line to undo it, restore, and delete.
func TestPagesOwnerActions(t *testing.T) {
	s, hub, database := pagesTestService(t)
	ctx := context.Background()
	day := 25 * time.Hour
	var pages []*db.AssistantPageRow
	for i, title := range []string{"Morning run", "Reading list", "Weekend trip", "Grocery list"} {
		pages = append(pages, keptPage(t, database, title, "main", time.Now().Add(-time.Duration(i+1)*day), false))
	}
	for _, p := range pages[:3] {
		if res := mustPage(t, s, pagesIndex, wire.UIRequest{Action: "persona.pin", Values: map[string]string{"page": p.ID}}); res.Toast != "Pinned to the sidebar" {
			t.Fatalf("pin %s: %+v", p.Title, res)
		}
	}
	if res := mustPage(t, s, pages[3].ID, wire.UIRequest{Action: "persona.pin"}); res.Toast != "You can pin up to 3 pages" || pageItem(res.View, "persona.pin").Action.ID != "persona.pin" {
		t.Fatalf("a fourth pin: %+v", res)
	}
	if got := s.pagePlaces(ctx, i18n.En); len(got) != 4 {
		t.Fatalf("the index and three pinned pages: %+v", got)
	}
	var nav navigation
	if _, err := s.store().Setting(ctx, navigationKey, &nav); err != nil || nav.Pages == nil {
		t.Fatalf("%+v %v", nav, err)
	}
	for _, key := range []string{"p:-pages/index", "p:-pages/" + pages[0].ID, "p:-pages/" + pages[2].ID} {
		if !slices.Contains(nav.Pages.Shown, key) {
			t.Fatalf("a pin shows %s in the sidebar: %+v", key, nav.Pages)
		}
	}
	if slices.Contains(nav.Pages.Shown, "p:-pages/"+pages[3].ID) || len(hub.sent("navigation")) != 3 {
		t.Fatalf("navigation: %+v, %d events", nav.Pages, len(hub.sent("navigation")))
	}
	if res := mustPage(t, s, pages[0].ID, wire.UIRequest{Action: "persona.unpin"}); res.Toast != "Unpinned" || pageItem(res.View, "persona.pin").Action.ID != "persona.pin" {
		t.Fatalf("unpin: %+v", res)
	}
	lists := len(hub.sent("assistant_pages"))
	if lists != 4 {
		t.Fatalf("each change of the places is told: %d", lists)
	}

	// rename: the form, a reserved name, one taken, then a new one
	form := mustPage(t, s, pages[1].ID, wire.UIRequest{State: pageState{V: pages[1].Version, Form: "rename"}.encode()}).View
	if f := pageItem(form, "persona.title"); f.Name != "title" || f.Value != "Reading list" || pageItem(form, "persona.save").Action.ID != "persona.rename" {
		t.Fatalf("the rename form: %+v", form)
	}
	rename := func(title string) extension.ActionResult {
		return mustPage(t, s, pages[1].ID, wire.UIRequest{Action: "persona.rename", Values: map[string]string{"title": title}, State: pageState{Form: "rename"}.encode()})
	}
	for title, want := range map[string]string{"Inbox": "That's the name of one of the app's own places. Choose another title.",
		"受信トレイ": "That's the name of one of the app's own places. Choose another title.", "Morning run": "Another page already has this title",
		"": "A title is 1–40 characters on one line", strings.Repeat("x", 41): "A title is 1–40 characters on one line"} {
		if res := rename(title); res.Toast != want || res.View != nil || res.Pop {
			t.Fatalf("rename %q: %+v", title, res)
		}
	}
	if res := rename("Inbox zero books"); !res.Pop || res.Toast != "Renamed" {
		t.Fatalf("the owner may use a place's name inside a title: %+v", res)
	}
	if got, _ := database.AssistantPage(ctx, pages[1].ID); got.Title != "Inbox zero books" || got.Version != pages[1].Version+1 {
		t.Fatalf("renamed: %+v", got)
	}

	// remove from the page: the index, with a line to undo it
	res := mustPage(t, s, pages[2].ID, wire.UIRequest{Action: "persona.remove"})
	if res.View == nil || res.View.Title != "Dashboards" || res.Toast != "Removed" {
		t.Fatalf("removed: %+v", res)
	}
	if undo := pageItem(res.View, "persona.undo"); undo.Action == nil || undo.Action.ID != "persona.restore:"+pages[2].ID ||
		pageItem(res.View, "persona.removed").Text != "Removed “Weekend trip”" {
		t.Fatalf("the line to undo it: %+v", res.View.Sections)
	}
	if got := s.pagePlaces(ctx, i18n.En); len(got) != 2 {
		t.Fatalf("a removed page is no longer pinned: %+v", got)
	}
	if res := mustPage(t, s, pagesIndex, wire.UIRequest{Action: "persona.restore:" + pages[2].ID}); res.Toast != "Restored" || pageItem(res.View, "page:"+pages[2].ID).Title != "Weekend trip" {
		t.Fatalf("restored: %+v", res)
	}
	// delete for good: only a removed page
	if res := mustPage(t, s, pagesIndex, wire.UIRequest{Action: "persona.delete:" + pages[3].ID}); res.Toast != "This button can't be used on this page" {
		t.Fatalf("a kept page is not deleted: %+v", res)
	}
	mustPage(t, s, pagesIndex, wire.UIRequest{Action: "persona.remove", Values: map[string]string{"page": pages[3].ID}})
	if res := mustPage(t, s, pagesIndex, wire.UIRequest{Action: "persona.delete:" + pages[3].ID}); res.Toast != "Deleted" {
		t.Fatalf("deleted: %+v", res)
	}
	if _, err := database.AssistantPage(ctx, pages[3].ID); !errors.Is(err, db.ErrAssistantPageNotFound) {
		t.Fatalf("gone for good: %v", err)
	}
	if n := len(hub.sent("assistant_pages")); n != lists+5 {
		t.Fatalf("rename, remove, restore, remove, delete: %d more", n-lists)
	}
	// a page's own action ids never reach the hub's
	if _, err := pagesUI(t, s, pages[0].ID, wire.UIRequest{Action: "PERSONA.anything"}); !pagesRefused(err, http.StatusBadRequest) {
		t.Fatalf("an unknown hub action: %v", err)
	}
}

// waitTap waits until tap id is in a state ok accepts.
func waitTap(t *testing.T, database *db.DB, id string, ok func(*db.AssistantPageActionRow) bool) *db.AssistantPageActionRow {
	t.Helper()
	var a *db.AssistantPageActionRow
	waitFor(t, "tap "+id, func() bool {
		var err error
		if a, err = database.AssistantPageAction(context.Background(), id); err != nil {
			t.Fatal(err)
		}
		return a != nil && ok(a)
	})
	return a
}

func lastTap(t *testing.T, database *db.DB, page string) db.AssistantPageActionRow {
	t.Helper()
	taps, err := database.AssistantPageActions(context.Background(), page, 1)
	if err != nil || len(taps) == 0 {
		t.Fatalf("%v %v", taps, err)
	}
	return taps[0]
}

// A tap: checked against the stored page, Received at once, delivered to its conversation as the owner's message
// with the tag, never interrupting the turn under way (whatever the owner's interject setting says), then Persona is
// working and Persona replied in the chat as its turn goes; a confirmed tap on a version the owner did not see is not
// sent.
func TestPageTapDelivery(t *testing.T) {
	s, hub, database := pagesTestService(t)
	ctx := context.Background()
	// The owner moves work aside for their own messages, and conv-1 is on a round another message started: a message
	// of theirs would interrupt it (ownerInterjects). A tap never does.
	setSettings(t, s, func(v *Settings) { yes := true; v.InterjectBackground = &yes })
	s.preempt.mu.Lock()
	if s.preempt.rounds == nil {
		s.preempt.rounds = map[string]backgroundRound{}
	}
	s.preempt.rounds["conv-1"] = backgroundRound{kind: "a2a", running: true, at: time.Now()}
	s.preempt.mu.Unlock()
	run := keptPage(t, database, "Morning run", "conv-1", time.Now().Add(-time.Hour), true)
	state := pageState2("", run.Version)

	// what the page does not take
	for name, in := range map[string]wire.UIRequest{
		"a field it does not have":  {Action: "done", Values: map[string]string{"minutes": "30", "note": "x"}, State: state},
		"a value that is no number": {Action: "done", Values: map[string]string{"minutes": "lots"}, State: state},
		"too long":                  {Action: "done", Values: map[string]string{"minutes": strings.Repeat("1", 2001)}, State: state},
		"a form not filled":         {Action: "done", State: state},
	} {
		if _, err := pagesUI(t, s, run.ID, in); !pagesRefused(err, http.StatusBadRequest) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// a confirmed action on another version: the page again, nothing sent
	res := mustPage(t, s, run.ID, wire.UIRequest{Action: "skip", State: pageState2("", run.Version+1)})
	if res.Toast != "This page just changed, confirm again" || res.View == nil {
		t.Fatalf("confirm again: %+v", res)
	}
	if taps, _ := database.AssistantPageActions(ctx, run.ID, 5); len(taps) != 0 {
		t.Fatalf("not recorded: %+v", taps)
	}

	res = mustPage(t, s, run.ID, wire.UIRequest{Action: "done", Values: map[string]string{"minutes": "32"}, State: state})
	if res.Toast != "Received" || pageItem(res.View, "persona.status").Text != "Received" {
		t.Fatalf("received at once: %+v", res)
	}
	tap := lastTap(t, database, run.ID)
	wantTag := `<persona-action view="-pages/` + run.ID + `" action="done">{"values":{"minutes":"32"}}</persona-action>`
	if !strings.HasPrefix(tap.Text, "Done today\nMinutes：32\n") || !strings.HasSuffix(tap.Text, "\n"+wantTag) || tap.Device != "phone" {
		t.Fatalf("the owner's message: %q", tap.Text)
	}
	sent := waitTap(t, database, tap.ID, func(a *db.AssistantPageActionRow) bool { return a.Status == db.AssistantPageTapSent })
	if sent.TargetMachine != "server" || sent.TargetConversation != "conv-1" || sent.Redirected || sent.DeliveredAt == nil {
		t.Fatalf("delivered to its conversation: %+v", sent)
	}
	hub.fakeHub.mu.Lock()
	sends := slices.Clone(hub.fakeHub.sentOnce)
	hub.fakeHub.mu.Unlock()
	if len(sends) != 1 || sends[0]["session_id"] != "conv-1" || sends[0]["text"] != tap.Text || sends[0]["resume_only"] != true ||
		sends[0]["client_msg_id"] != tap.MessageID || sends[0]["interject_background"] != false {
		t.Fatalf("sent as the owner's message, after the turn under way: %+v", sends)
	}
	if s.pages.preempted.Load() != 0 {
		t.Fatal("a tap preempted a turn")
	}
	footer := func() wire.Component {
		return pageItem(mustPage(t, s, run.ID, wire.UIRequest{}).View, "persona.status")
	}
	if got := footer(); got.Text != "Delivered to Persona" {
		t.Fatalf("delivered: %+v", got)
	}
	// the same tap again within 30 seconds is the first
	mustPage(t, s, run.ID, wire.UIRequest{Action: "done", Values: map[string]string{"minutes": "32"}, State: state})
	if taps, _ := database.AssistantPageActions(ctx, run.ID, 5); len(taps) != 1 {
		t.Fatalf("one tap: %+v", taps)
	}

	// its conversation's turn starts and ends
	waitPending := func(ui string) {
		t.Helper()
		waitFor(t, "an invalidation of "+ui, func() bool {
			s.pages.events.mu.Lock()
			defer s.pages.events.mu.Unlock()
			_, waiting := s.pages.events.queue.pending[ui]
			return waiting || !s.pages.events.queue.last[ui].IsZero()
		})
	}
	waitPending(run.ID)
	s.pageTurnStarted("server", "conv-1", time.Now())
	waitTap(t, database, tap.ID, func(a *db.AssistantPageActionRow) bool { return a.TurnStartedAt != nil })
	if got := footer(); got.Text != "Persona is working" {
		t.Fatalf("working: %+v", got)
	}
	s.pageTurnEnded("server", "conv-1", time.Now())
	waitTap(t, database, tap.ID, func(a *db.AssistantPageActionRow) bool { return a.TurnEndedAt != nil })
	if got := footer(); got.Type != "link" || got.Title != "Persona replied in the chat" || got.URL != "https://hub.example.com/persona/conv-1" {
		t.Fatalf("answered in the chat: %+v", got)
	}
	// a turn that wrote the page clears the line
	if _, err := database.UpdateAssistantPage(ctx, db.AssistantPageUpdate{ID: run.ID, Expected: run.Version, View: json.RawMessage(runPageView),
		By: db.AssistantPageWriter{Kind: db.AssistantPageByAgent, Machine: "server", Session: "conv-1"}, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if got := footer(); got.Type != "" {
		t.Fatalf("the page answered: %+v", got)
	}
	// the turns of a conversation no tap went to are not followed
	if _, ok := s.pageTurnMachine("server", "main-1"); ok {
		t.Fatal("main-1 is followed")
	}
}

// A page whose conversation is gone goes to the main conversation, and says so; a failed delivery keeps the owner's
// values for Retry; a page of the main conversation goes to whichever conversation is the main one now.
func TestPageTapRedirectAndRetry(t *testing.T) {
	s, hub, database := pagesTestService(t)
	day := 25 * time.Hour
	tapOn := func(p *db.AssistantPageRow) db.AssistantPageActionRow {
		t.Helper()
		mustPage(t, s, p.ID, wire.UIRequest{Action: "done", Values: map[string]string{"minutes": "20"}, State: pageState2("", p.Version)})
		return lastTap(t, database, p.ID)
	}
	// conv-gone is not on its machine
	gone := keptPage(t, database, "Old page", "conv-gone", time.Now().Add(-3*day), false)
	a := waitTap(t, database, tapOn(gone).ID, func(a *db.AssistantPageActionRow) bool { return a.Status == db.AssistantPageTapSent })
	if a.TargetConversation != "main-1" || !a.Redirected {
		t.Fatalf("redirected to the main conversation: %+v", a)
	}
	if got := pageItem(mustPage(t, s, gone.ID, wire.UIRequest{}).View, "persona.status"); got.Text != "Sent to your main conversation instead" {
		t.Fatalf("the page says so: %+v", got)
	}
	// conv-1 is listed, but its machine says the conversation is gone when sent
	hub.fakeHub.mu.Lock()
	hub.fakeHub.failSendOnce = map[string]error{"server/conv-1": errors.New("conversation conv-1 is gone")}
	hub.fakeHub.mu.Unlock()
	listed := keptPage(t, database, "Listed page", "conv-1", time.Now().Add(-2*day), false)
	if a := waitTap(t, database, tapOn(listed).ID, func(a *db.AssistantPageActionRow) bool { return a.Status == db.AssistantPageTapSent }); a.TargetConversation != "main-1" || !a.Redirected {
		t.Fatalf("redirected after the send: %+v", a)
	}
	// the main conversation refuses: not delivered, with Retry
	hub.fakeHub.mu.Lock()
	hub.fakeHub.failSendOnce = map[string]error{"server/main-2": errors.New("model unavailable")}
	hub.fakeHub.mu.Unlock()
	setSettings(t, s, func(v *Settings) { v.Main = "main-2" })
	hub.fakeHub.setSessions("server", proto.Session{SessionID: "main-1", Assistant: true}, proto.Session{SessionID: "main-2", Assistant: true},
		proto.Session{SessionID: "conv-1", Assistant: true})
	mainPage := keptPage(t, database, "Main page", "main", time.Now().Add(-day), false)
	failed := waitTap(t, database, tapOn(mainPage).ID, func(a *db.AssistantPageActionRow) bool { return a.Status == db.AssistantPageTapFailed })
	view := mustPage(t, s, mainPage.ID, wire.UIRequest{}).View
	if pageItem(view, "persona.status").Text != "Not delivered" || pageItem(view, "persona.retry").Action.ID != "persona.retry:"+failed.ID || failed.Values["minutes"] != "20" {
		t.Fatalf("not delivered: %+v", view.Sections)
	}
	hub.fakeHub.mu.Lock()
	hub.fakeHub.failSendOnce = nil
	hub.fakeHub.mu.Unlock()
	if res := mustPage(t, s, mainPage.ID, wire.UIRequest{Action: "persona.retry:" + failed.ID}); pageItem(res.View, "persona.status").Text != "Received" {
		t.Fatalf("retried: %+v", res)
	}
	retried := waitTap(t, database, failed.ID, func(a *db.AssistantPageActionRow) bool { return a.Status == db.AssistantPageTapSent })
	if retried.TargetConversation != "main-2" || retried.Redirected {
		t.Fatalf("the main conversation now: %+v", retried)
	}
	hub.fakeHub.mu.Lock()
	defer hub.fakeHub.mu.Unlock()
	for _, p := range hub.fakeHub.sentOnce {
		if p["interject_background"] != false {
			t.Fatalf("a tap interrupted a turn: %+v", p)
		}
	}
}

// The switch: off until the owner turns it on, from a device with full access; each change tells the apps.
func TestPagesSwitch(t *testing.T) {
	s, hub, _ := pagesTestService(t)
	setSettings(t, s, func(v *Settings) { v.Dashboards = nil })
	request := func(method, body, role string) (int, map[string]any) {
		r := httptest.NewRequest(method, "/api/v1/assistant", strings.NewReader(body))
		if role != "" {
			r.Header.Set(RoleHeader, role)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		out := map[string]any{}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	if code, out := request("GET", "", ""); code != http.StatusOK || out["dashboards"] != false {
		t.Fatalf("off by default: %d %v", code, out["dashboards"])
	}
	if code, _ := request("PUT", `{"dashboards":true}`, "answer"); code != http.StatusForbidden || s.dashboardsOn(context.Background()) {
		t.Fatalf("a device that can only answer: %d", code)
	}
	if code, out := request("PUT", `{"dashboards":true}`, ""); code != http.StatusOK || out["dashboards"] != true || !s.dashboardsOn(context.Background()) {
		t.Fatalf("on: %d %v", code, out["dashboards"])
	}
	if n := len(hub.sent("assistant_pages")); n != 1 {
		t.Fatalf("told once: %d", n)
	}
	request("PUT", `{"dashboards":true}`, "")
	request("PUT", `{"kb_topics":false}`, "")
	if n := len(hub.sent("assistant_pages")); n != 1 {
		t.Fatalf("no change, nothing told: %d", n)
	}
	if code, out := request("PUT", `{"dashboards":false}`, ""); code != http.StatusOK || out["dashboards"] != false || len(hub.sent("assistant_pages")) != 2 {
		t.Fatalf("off: %d %v", code, out["dashboards"])
	}
}

// The upkeep: drafts past their week expire, pages removed 30 days are deleted (and the places told), and each day's
// taps are logged once: how many, the queue time, the turn wait and the turns preempted (none).
func TestPagesCleanup(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	s, hub, database := pagesTestService(t, func(format string, args ...any) {
		mu.Lock()
		lines = append(lines, fmt.Sprintf(format, args...))
		mu.Unlock()
	})
	ctx := context.Background()
	st, _ := s.pageStore()
	now := time.Now()
	removed := keptPage(t, database, "Old trip", "main", now.Add(-50*time.Hour), false)
	if _, _, err := database.RemoveAssistantPage(ctx, removed.ID, db.AssistantPageRemovedByOwner, now); err != nil {
		t.Fatal(err)
	}
	id := db.NewAssistantPageID()
	if _, err := database.CreateAssistantPage(ctx, db.NewAssistantPage{ID: id, Title: "A draft", OwnerKind: db.AssistantPageOwnerMain,
		CreatedMachine: "server", CreatedSession: "conv-1", OfferView: "view-" + id, View: json.RawMessage(runPageView), At: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	kept := keptPage(t, database, "Morning run", "main", now.Add(-26*time.Hour), false)
	mustPage(t, s, kept.ID, wire.UIRequest{Action: "done", Values: map[string]string{"minutes": "30"}, State: pageState2("", kept.Version)})
	tap := waitTap(t, database, lastTap(t, database, kept.ID).ID, func(a *db.AssistantPageActionRow) bool { return a.Status == db.AssistantPageTapSent })

	lists := len(hub.sent("assistant_pages"))
	s.cleanPages(ctx, st, now.Add(31*24*time.Hour))
	if _, err := database.AssistantPage(ctx, removed.ID); !errors.Is(err, db.ErrAssistantPageNotFound) {
		t.Fatalf("removed 30 days ago: %v", err)
	}
	if p, err := database.AssistantPage(ctx, id); err == nil && p.Status != db.AssistantPageRemoved || err != nil && !errors.Is(err, db.ErrAssistantPageNotFound) {
		t.Fatalf("an old draft expires: %+v %v", p, err)
	}
	if len(hub.sent("assistant_pages")) != lists+1 {
		t.Fatal("Recently removed changed: the places are told")
	}
	// the day of the tap, logged once
	s.logPageTaps(ctx, st, tap.At.AddDate(0, 0, 1))
	s.logPageTaps(ctx, st, tap.At.AddDate(0, 0, 1))
	mu.Lock()
	defer mu.Unlock()
	var logged []string
	for _, l := range lines {
		if strings.HasPrefix(l, "assistant pages: taps on "+tap.At.UTC().Format(time.DateOnly)) {
			logged = append(logged, l)
		}
	}
	if len(logged) != 1 || !strings.Contains(logged[0], ": 1 (delivered 1, redirected 0, failed 0)") || !strings.Contains(logged[0], "preempted turns 0") {
		t.Fatalf("the day's line: %q", logged)
	}
}
