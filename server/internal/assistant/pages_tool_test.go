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
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/extmgr"
	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// pagesToolService is pagesTestService with the pages' write window of the hour and the sidebar's arrangement cleared
// (other tests' writes count in them), the owner speaking English, and the conversations the page tool's tests call
// from: main-1 (the main one), conv-1 and conv-2.
func pagesToolService(t *testing.T) (*Service, *pagesHub, *db.DB) {
	t.Helper()
	s, hub, database := pagesTestService(t)
	pageSQL(t, `DELETE FROM settings WHERE key = ANY($1)`, []string{db.AssistantPagesRateKey, navigationKey})
	hub.fakeHub.setLocale("en")
	hub.fakeHub.setSessions("server", proto.Session{SessionID: "main-1", Assistant: true}, proto.Session{SessionID: "conv-1", Assistant: true},
		proto.Session{SessionID: "conv-2", Assistant: true})
	return s, hub, database
}

// pageSQL runs a statement on the test database (what only the owner's later controls change: a page's pause).
func pageSQL(t *testing.T, sql string, args ...any) {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), os.Getenv("PERSONA_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

// callPage calls the page tool as conversation session on machine does.
func callPage(s *Service, machine, session string, args map[string]any) (map[string]any, error) {
	raw, _ := json.Marshal(args)
	res := s.Tool(context.Background(), ToolCall{Name: pageToolName, Arguments: raw, MachineID: machine, SessionID: session})
	var text strings.Builder
	for _, c := range res.Content {
		text.WriteString(c.Text)
	}
	if res.IsError {
		return nil, errors.New(text.String())
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(text.String()), &out); err != nil {
		return nil, fmt.Errorf("%v: %s", err, text.String())
	}
	return out, nil
}

func mustCallPage(t *testing.T, s *Service, session string, args map[string]any) map[string]any {
	t.Helper()
	out, err := callPage(s, "server", session, args)
	if err != nil {
		t.Fatalf("page %v from %s: %v", args["action"], session, err)
	}
	return out
}

// pageListed: the page tool is in the list conversation session on machine gets.
func pageListed(s *Service, machine, session string) bool {
	return slices.ContainsFunc(s.ToolsFor(context.Background(), machine, session), func(t proto.Tool) bool { return t.Name == pageToolName })
}

func runView() json.RawMessage { return json.RawMessage(runPageView) }

// createPage drafts the Morning run page from session and gives its id and its preview card.
func createPage(t *testing.T, s *Service, database *db.DB, session, title string) (string, string) {
	t.Helper()
	out := mustCallPage(t, s, session, map[string]any{"action": "create", "title": title, "icon": "heart",
		"why": "So you can see your streak at a glance.", "view": runView()})
	if out["status"] != "draft" || out["version"] != float64(1) {
		t.Fatalf("create: %v", out)
	}
	id, _ := out["id"].(string)
	p, err := database.AssistantPage(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return id, p.OfferView
}

// postCardAction is the owner's tap on action of card in conversation, from the phone.
func postCardAction(t *testing.T, s *Service, conversation, card, action string) (int, map[string]any) {
	t.Helper()
	body := fmt.Sprintf(`{"action":%q,"client_msg_id":"tap-%s"}`, action, randomHex(4))
	r := httptest.NewRequest(http.MethodPost, "/api/v1/assistant/conversations/"+conversation+"/views/"+card+"/actions", strings.NewReader(body))
	r.Header.Set(extwire.HeaderDevice, "phone")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	out := map[string]any{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// Who gets and may call the page tool: the main conversation and ordinary conversations, while Dashboards are on, on
// the built-in manager. Every other caller neither lists it nor gets a call through, with a sentence that says why.
func TestPageToolCallers(t *testing.T) {
	s, hub, _ := pagesToolService(t)
	ctx := context.Background()
	f := hub.fakeHub
	f.setSessions("server", proto.Session{SessionID: "main-1", Assistant: true}, proto.Session{SessionID: "conv-1", Assistant: true},
		proto.Session{SessionID: "lead-1", Assistant: true}, proto.Session{SessionID: "inbox-1", Assistant: true},
		proto.Session{SessionID: "lark-1", Assistant: true}, proto.Session{SessionID: "ext-1", Assistant: true},
		proto.Session{SessionID: "opened-1", Assistant: true}, proto.Session{SessionID: "guest-1", Assistant: true},
		proto.Session{SessionID: "bg-1", Assistant: true}, proto.Session{SessionID: "routine-1", Assistant: true},
		proto.Session{SessionID: "trip-1", Assistant: true, Space: "trip"}, proto.Session{SessionID: "health-1", Assistant: true, Space: SpaceHealth})
	f.setSessions("box", proto.Session{SessionID: "work-1", Assistant: true})
	f.setHello(func(h *Hello) { h.Assistant.WorkMachines = []string{"box"} })
	setSettings(t, s, func(v *Settings) {
		v.Leaders = []string{"lead-1"}
		v.BackgroundSupervisor, v.BackgroundSupervisorMachineID = "bg-1", "server"
	})
	s.originSet(ctx, "server", "inbox-1", OriginInbox, "item-1", "")
	s.originSet(ctx, "server", "lark-1", OriginLarkAgent, "lark", "lark")
	s.originSet(ctx, "server", "ext-1", OriginExtension, "", "sample-ext")
	s.originSet(ctx, "server", "opened-1", OriginUser, "", "sample-ext") // opened by an extension's token
	s.guests.mu.Lock()
	if err := s.guestsLocked(ctx); err != nil {
		t.Fatal(err)
	}
	s.guests.m["guest-1"] = &guestConv{MachineID: "server", Tools: []string{"lark_agent_reply"}, At: time.Now()}
	s.guests.mu.Unlock()
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sc.tasks = append(sc.tasks, Task{ID: "digest", RunMachineID: "server", RunSessionID: "routine-1"})
	sc.mu.Unlock()

	for _, writer := range []string{"main-1", "conv-1", "lead-1"} {
		if !pageListed(s, "server", writer) {
			t.Fatalf("%s may write dashboards but is not given page", writer)
		}
		if _, err := callPage(s, "server", writer, map[string]any{"action": "list"}); err != nil {
			t.Fatalf("%s: %v", writer, err)
		}
	}
	refused := map[string]struct{ machine, session, says string }{
		"an Inbox handling chat":            {"server", "inbox-1", "Inbox conversations cannot call page"},
		"Lark /agent":                       {"server", "lark-1", "writes to other people"},
		"an extension's conversation":       {"server", "ext-1", "writes to other people"},
		"one an extension's token opened":   {"server", "opened-1", "writes to other people"},
		"a meeting guest":                   {"server", "guest-1", "meeting guest"},
		"the background supervisor's round": {"server", "bg-1", "background supervisor"},
		"a scheduled routine's run":         {"server", "routine-1", "routine's run"},
		"a space's conversation":            {"server", "trip-1", "space's conversations"},
		"the Health space":                  {"server", "health-1", "Health space"},
		"a work computer's conversation":    {"box", "work-1", "work computer"},
	}
	for name, c := range refused {
		if pageListed(s, c.machine, c.session) {
			t.Errorf("%s is given page", name)
		}
		_, err := callPage(s, c.machine, c.session, map[string]any{"action": "create", "title": "Morning run", "view": runView()})
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// a supervision round's list never has it
	if slices.Contains(roundToolNames, pageToolName) || slices.ContainsFunc(roundTools(s.Tools(ctx)), func(t proto.Tool) bool { return t.Name == pageToolName }) {
		t.Fatal("a supervision round lists page")
	}
	// the tool's registry list has it (the hub's goldens measure it), a conversation's only when it may write
	if !slices.ContainsFunc(s.ToolsFor(ctx, "", ""), func(t proto.Tool) bool { return t.Name == pageToolName }) {
		t.Fatal("the registry's list lacks page")
	}
	// another manager's conversations: no pages there
	s.id = "m-0d0d0d"
	if pageListed(s, "server", "main-1") {
		t.Fatal("a manager other than the built-in one lists page")
	}
	if _, err := callPage(s, "server", "main-1", map[string]any{"action": "list"}); err == nil || !strings.Contains(err.Error(), "built-in assistant") {
		t.Fatalf("another manager: %v", err)
	}
	s.id = ""
	// Dashboards off: nobody; an open conversation's list is made again (its discovery cursor moves)
	on := s.guardDiscoveryCursor(ctx, "server", "main-1", true, "base")
	setSettings(t, s, func(v *Settings) { v.Dashboards = nil })
	if pageListed(s, "server", "main-1") {
		t.Fatal("listed while Dashboards are off")
	}
	if off := s.guardDiscoveryCursor(ctx, "server", "main-1", true, "base"); off == on || on != "base:page" {
		t.Fatalf("the discovery cursor: on %q, off %q", on, off)
	}
	if _, err := callPage(s, "server", "main-1", map[string]any{"action": "list"}); err == nil || !strings.Contains(err.Error(), "Settings › Assistant") {
		t.Fatalf("off: %v", err)
	}
}

// create drafts a page and puts its preview in the calling conversation: the page read-only (no button, form or
// anything to tap from the page, its first three sections at most), why it exists and the three choices. A kept title
// is refused with the page that has it; bad titles, icons and views are refused with what to fix.
func TestPageCreatePreview(t *testing.T) {
	s, hub, database := pagesToolService(t)
	ctx := context.Background()
	view := `{"fallback":"Morning run: 12 days","sections":[
		{"key":"a","items":[{"type":"metric","key":"streak","label":"Streak","value":"12 days"},
			{"type":"button","key":"done","title":"Done today","action":{"id":"done","reply":"Done today"}}]},
		{"key":"b","items":[{"type":"field","key":"minutes","name":"minutes","label":"Minutes","kind":"number"}]},
		{"key":"c","items":[{"type":"row","key":"mon","title":"Monday","action":{"id":"mon","reply":"Monday"}}]},
		{"key":"d","items":[{"type":"text","key":"t","text":"Keep going"}]},
		{"key":"e","items":[{"type":"text","key":"u","text":"Too far"}]}]}`
	out := mustCallPage(t, s, "conv-1", map[string]any{"action": "create", "title": "Morning run", "icon": "heart",
		"why": "So you can see your streak.", "view": json.RawMessage(view)})
	id := out["id"].(string)
	p, err := database.AssistantPage(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != db.AssistantPageDraft || p.OwnerKind != db.AssistantPageOwnerConversation || p.OwnerMachine != "server" ||
		p.OwnerRef != "conv-1" || p.CreatedSession != "conv-1" || p.Why != "So you can see your streak." || p.Icon != "heart" {
		t.Fatalf("the draft: %+v", p)
	}
	card, err := database.AssistantView(ctx, "conv-1", p.OfferView)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := wire.ParseAssistantView(card.View)
	if err != nil {
		t.Fatalf("the preview is a card: %v", err)
	}
	var keys []string
	for _, sec := range preview.Sections {
		keys = append(keys, sec.Key)
	}
	if !slices.Equal(keys, []string{"a", "c", "d", pageOfferSection}) || preview.Title != "Morning run" {
		t.Fatalf("the first three sections with anything left, then the choices: %v", keys)
	}
	var ids []string
	for _, a := range wire.Actions(preview) {
		ids = append(ids, a.ID)
	}
	if !slices.Equal(ids, []string{pageOfferKeep, pageOfferKeepPin, pageOfferNotNow}) {
		t.Fatalf("only the choices can be tapped: %v", ids)
	}
	if pageItem(preview, "persona.why").Text != "So you can see your streak." || pageItem(preview, pageOfferKeep).Title != "Keep in Dashboards" ||
		pageItem(preview, pageOfferKeepPin).Title != "Keep and pin to the sidebar" || pageItem(preview, pageOfferNotNow).Title != "Not now" {
		t.Fatalf("the choices: %+v", preview.Sections[len(preview.Sections)-1])
	}
	if len(hub.sent("assistant_feed")) == 0 {
		t.Fatal("the card is put in the feed")
	}
	// the preview is the hub's: a view_update of it is refused
	if _, err := runPageTool(s, "conv-1", "view_update", map[string]any{"id": p.OfferView, "view": json.RawMessage(`{"fallback":"x","sections":[]}`)}); err == nil ||
		!strings.Contains(err.Error(), "page update") {
		t.Fatalf("view_update of a preview: %v", err)
	}

	for name, args := range map[string]map[string]any{
		"a reserved title":      {"title": "Inbox (2)"},
		"a title on two lines":  {"title": "Morning\nrun"},
		"a settings icon":       {"title": "Running", "icon": "settings"},
		"html":                  {"title": "Running", "view": json.RawMessage(`{"fallback":"x","sections":[{"items":[{"type":"html","key":"h","html":"<p>x</p>"}]}]}`)},
		"no fallback":           {"title": "Running", "view": json.RawMessage(`{"sections":[]}`)},
		"a why of three lines":  {"title": "Running", "why": "a\nb\nc"},
		"an action of the hubs": {"title": "Running", "view": json.RawMessage(`{"fallback":"x","sections":[{"items":[{"type":"button","key":"b","title":"B","action":{"id":"persona.pin","reply":"Pin"}}]}]}`)},
	} {
		args["action"] = "create"
		if args["view"] == nil {
			args["view"] = runView()
		}
		if _, err := callPage(s, "server", "conv-1", args); err == nil {
			t.Errorf("%s: created", name)
		}
	}
	// titles in Latin letters are refused as whole words only
	for title, refused := range map[string]bool{"Personal budget": false, "Todoist export": false, "Inbox · work": true} {
		_, err := callPage(s, "server", "conv-2", map[string]any{"action": "create", "title": title, "view": runView()})
		if (err != nil) != refused {
			t.Errorf("%q: %v", title, err)
		}
	}

	// a kept page's title: the page that has it
	if code, _ := postCardAction(t, s, "conv-1", p.OfferView, pageOfferKeep); code != http.StatusOK {
		t.Fatalf("keep: %d", code)
	}
	exists := mustCallPage(t, s, "main-1", map[string]any{"action": "create", "title": "morning RUN", "view": runView()})
	if exists["error"] != "exists" || exists["id"] != id {
		t.Fatalf("exists: %v", exists)
	}
}

// runPageTool calls another assistant tool from conversation self on server.
func runPageTool(s *Service, self, name string, args map[string]any) (string, error) {
	raw, _ := json.Marshal(args)
	res := s.Tool(context.Background(), ToolCall{Name: name, Arguments: raw, MachineID: "server", SessionID: self})
	var b strings.Builder
	for _, c := range res.Content {
		b.WriteString(c.Text)
	}
	if res.IsError {
		return "", errors.New(b.String())
	}
	return b.String(), nil
}

// update, get, list and remove: who may, the version check, the refusals, the taps get gives, and what the main
// conversation reads of the others' pages. A new main conversation writes the old one's pages.
func TestPageUpdateGetListRemove(t *testing.T) {
	s, hub, database := pagesToolService(t)
	ctx := context.Background()
	mainPage, mainCard := createPage(t, s, database, "main-1", "Morning run")
	convPage, convCard := createPage(t, s, database, "conv-1", "Reading list")
	// a draft's update redraws its preview
	before, _ := database.AssistantView(ctx, "conv-1", convCard)
	newer := `{"fallback":"Reading list: 3 books","sections":[{"key":"books","items":[{"type":"text","key":"n","text":"3 books"}]}]}`
	if out := mustCallPage(t, s, "conv-1", map[string]any{"action": "update", "id": convPage, "view": json.RawMessage(newer), "note": "three now"}); out["version"] != float64(2) {
		t.Fatalf("update: %v", out)
	}
	after, _ := database.AssistantView(ctx, "conv-1", convCard)
	if after.Version != before.Version+1 || !strings.Contains(string(after.View), "3 books") {
		t.Fatalf("the preview follows the draft: %s", after.View)
	}
	for _, c := range []struct{ conversation, card string }{{"main-1", mainCard}, {"conv-1", convCard}} {
		if code, out := postCardAction(t, s, c.conversation, c.card, pageOfferKeep); code != http.StatusOK {
			t.Fatalf("keep: %d %v", code, out)
		}
	}
	// a conversation writes its own page, not another's
	if _, err := callPage(s, "server", "conv-2", map[string]any{"action": "update", "id": convPage, "view": runView()}); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("another conversation's page: %v", err)
	}
	if _, err := callPage(s, "server", "conv-1", map[string]any{"action": "update", "id": mainPage, "view": runView()}); err == nil {
		t.Fatal("conv-1 wrote the main conversation's page")
	}
	// the version check, and the title of a kept page
	if _, err := callPage(s, "server", "main-1", map[string]any{"action": "update", "id": mainPage, "view": runView(), "version": 7}); err == nil ||
		!strings.HasPrefix(err.Error(), "version_conflict") || !strings.Contains(err.Error(), "page get") {
		t.Fatalf("version conflict: %v", err)
	}
	if _, err := callPage(s, "server", "main-1", map[string]any{"action": "update", "id": mainPage, "view": runView(), "title": "Runs"}); err == nil {
		t.Fatal("a kept page's title changed")
	}
	if _, err := callPage(s, "server", "main-1", map[string]any{"action": "update", "id": mainPage,
		"view": json.RawMessage(`{"fallback":"x","sections":[{"items":[{"type":"metric","key":"m"}]}]}`)}); err == nil || !strings.Contains(err.Error(), "invalid view") {
		t.Fatalf("a bad view: %v", err)
	}
	invalidations := len(pageInvalidations(hub, mainPage))
	if out := mustCallPage(t, s, "main-1", map[string]any{"action": "update", "id": mainPage, "view": runView(), "version": 1}); out["version"] != float64(2) {
		t.Fatalf("update: %v", out)
	}
	waitFor(t, "the page told", func() bool { return len(pageInvalidations(hub, mainPage)) > invalidations })
	// paused by the owner: refused, saying to ask
	pageSQL(t, `UPDATE assistant_pages SET paused = true, paused_by = 'owner' WHERE id = $1`, mainPage)
	if _, err := callPage(s, "server", "main-1", map[string]any{"action": "update", "id": mainPage, "view": runView()}); err == nil || !strings.HasPrefix(err.Error(), "paused_by_owner") {
		t.Fatalf("paused: %v", err)
	}
	pageSQL(t, `UPDATE assistant_pages SET paused = false, paused_by = '' WHERE id = $1`, mainPage)

	// get: the page and its latest taps
	mustPage(t, s, mainPage, wire.UIRequest{Action: "done", Values: map[string]string{"minutes": "30"}, State: pageState2("", 2)})
	got := mustCallPage(t, s, "main-1", map[string]any{"action": "get", "id": mainPage})
	taps, _ := got["recent_actions"].([]any)
	if got["title"] != "Morning run" || got["status"] != "active" || got["version"] != float64(2) || got["view"] == nil || len(taps) != 1 ||
		taps[0].(map[string]any)["action"] != "done" || taps[0].(map[string]any)["values"].(map[string]any)["minutes"] != "30" || got["read_only"] != nil {
		t.Fatalf("get: %v", got)
	}
	// the main conversation reads conv-1's page, read only; conv-2 does not
	if ro := mustCallPage(t, s, "main-1", map[string]any{"action": "get", "id": convPage}); ro["read_only"] != true {
		t.Fatalf("main reads another's: %v", ro)
	}
	if _, err := callPage(s, "server", "conv-2", map[string]any{"action": "get", "id": convPage}); err == nil {
		t.Fatal("conv-2 read conv-1's page")
	}
	// list: what the caller writes; the main conversation also the others'
	listed := func(session string) (mine, others []string) {
		out := mustCallPage(t, s, session, map[string]any{"action": "list"})
		for _, e := range out["pages"].([]any) {
			mine = append(mine, e.(map[string]any)["id"].(string))
		}
		if ro, ok := out["read_only"].([]any); ok {
			for _, e := range ro {
				others = append(others, e.(map[string]any)["id"].(string))
			}
		}
		return mine, others
	}
	if mine, others := listed("main-1"); !slices.Equal(mine, []string{mainPage}) || !slices.Equal(others, []string{convPage}) {
		t.Fatalf("main's list: %v %v", mine, others)
	}
	if mine, others := listed("conv-1"); !slices.Equal(mine, []string{convPage}) || others != nil {
		t.Fatalf("conv-1's list: %v %v", mine, others)
	}

	// a new main conversation writes the old one's page; the old one, now an ordinary conversation, does not
	hub.fakeHub.setSessions("server", proto.Session{SessionID: "main-1", Assistant: true}, proto.Session{SessionID: "main-2", Assistant: true},
		proto.Session{SessionID: "conv-1", Assistant: true}, proto.Session{SessionID: "conv-2", Assistant: true})
	setSettings(t, s, func(v *Settings) { v.Main = "main-2" })
	if out := mustCallPage(t, s, "main-2", map[string]any{"action": "update", "id": mainPage, "view": runView()}); out["version"] != float64(3) {
		t.Fatalf("the new main conversation: %v", out)
	}
	if _, err := callPage(s, "server", "main-1", map[string]any{"action": "update", "id": mainPage, "view": runView()}); err == nil {
		t.Fatal("the old main conversation still writes the main conversation's page")
	}

	// remove: a reason, then Recently removed, with it
	if _, err := callPage(s, "server", "conv-1", map[string]any{"action": "remove", "id": convPage}); err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatalf("no reason: %v", err)
	}
	lists := len(hub.sent("assistant_pages"))
	if out := mustCallPage(t, s, "conv-1", map[string]any{"action": "remove", "id": convPage, "reason": "You finished the list."}); out["status"] != "removed" {
		t.Fatalf("remove: %v", out)
	}
	if len(hub.sent("assistant_pages")) != lists+1 {
		t.Fatal("the places are told")
	}
	index := mustPage(t, s, pagesIndex, wire.UIRequest{}).View
	if row := pageItem(index, "removed:"+convPage); !strings.HasPrefix(row.Subtitle, "Removed by Persona ") || !strings.HasSuffix(row.Subtitle, ": You finished the list.") {
		t.Fatalf("Recently removed says why: %+v", row)
	}
	if _, err := callPage(s, "server", "conv-1", map[string]any{"action": "update", "id": convPage, "view": runView()}); err == nil || !strings.HasPrefix(err.Error(), "removed") {
		t.Fatalf("a removed page: %v", err)
	}
	// a draft is withdrawn: its card says so
	draft, draftCard := createPage(t, s, database, "conv-2", "Weekly watch")
	mustCallPage(t, s, "conv-2", map[string]any{"action": "remove", "id": draft, "reason": "Not needed after all."})
	card, _ := database.AssistantView(ctx, "conv-2", draftCard)
	v, _ := wire.ParseAssistantView(card.View)
	if pageItem(v, "persona.closed").Text != "Withdrawn: Persona took this draft back." || len(wire.Actions(v)) != 0 {
		t.Fatalf("the withdrawn draft's card: %s", card.View)
	}
}

// pageInvalidations are the extension_ui invalidations the apps were sent for ui of the pages.
func pageInvalidations(hub *pagesHub, ui string) []map[string]any {
	var out []map[string]any
	for _, m := range hub.sent("extension_ui") {
		if m["extension"] == extmgr.PagesExtension && m["id"] == ui && m["invalidate"] == true {
			out = append(out, m)
		}
	}
	return out
}

// The owner's choice on the preview card is made in its own request: Keep and pin makes the page kept and pinned
// (the places told, its keys shown in the sidebar) at once; Not now declines; a draft that expired says so on its card
// and cannot be kept; a full sidebar keeps without pinning; Dashboards off keeps nothing.
func TestPageOfferChoices(t *testing.T) {
	s, hub, database := pagesToolService(t)
	ctx := context.Background()
	pinned, pinCard := createPage(t, s, database, "main-1", "Morning run")
	code, out := postCardAction(t, s, "main-1", pinCard, pageOfferKeepPin)
	if code != http.StatusOK {
		t.Fatalf("keep and pin: %d %v", code, out)
	}
	p, _ := database.AssistantPage(ctx, pinned)
	if p.Status != db.AssistantPageActive || !p.Pinned || p.KeptDevice != "phone" {
		t.Fatalf("kept and pinned: %+v", p)
	}
	places := s.pagePlaces(ctx, i18n.En)
	if len(places) != 2 || places[0].ID != pagesIndex || places[1].ID != pinned {
		t.Fatalf("the places: %+v", places)
	}
	if len(hub.sent("assistant_pages")) == 0 || len(hub.sent("navigation")) == 0 {
		t.Fatal("the places and the sidebar's arrangement are told")
	}
	var n navigation
	if _, err := database.Setting(ctx, navigationKey, &n); err != nil || n.Pages == nil ||
		!slices.Contains(n.Pages.Shown, "p:-pages/index") || !slices.Contains(n.Pages.Shown, "p:-pages/"+pinned) {
		t.Fatalf("shown: %+v %v", n.Pages, err)
	}
	// the answer goes to the conversation as the card's reply, as any card's
	a := waitCardAction(t, database, "main-1", pinCard)
	if a.Action != pageOfferKeepPin || !strings.HasPrefix(a.Reply+a.Text, "Keep and pin to the sidebar") {
		t.Fatalf("the card's answer: %+v", a)
	}

	no, noCard := createPage(t, s, database, "conv-1", "Reading list")
	if code, _ := postCardAction(t, s, "conv-1", noCard, pageOfferNotNow); code != http.StatusOK {
		t.Fatalf("not now: %d", code)
	}
	if p, _ := database.AssistantPage(ctx, no); p.Status != db.AssistantPageRemoved || p.RemovedBy != db.AssistantPageRemovedByDeclined || p.Listed() {
		t.Fatalf("declined: %+v", p)
	}

	// expired: the card says so, and Keep is refused with the card as it is now
	old, oldCard := createPage(t, s, database, "conv-2", "Weekly watch")
	st, _ := s.pageStore()
	s.cleanPages(ctx, st, time.Now().Add(db.AssistantPageDraftLife+time.Hour))
	card, _ := database.AssistantView(ctx, "conv-2", oldCard)
	v, _ := wire.ParseAssistantView(card.View)
	if pageItem(v, "persona.closed").Text != "Expired: this draft wasn't kept within 7 days." || len(wire.Actions(v)) != 0 {
		t.Fatalf("the expired draft's card: %s", card.View)
	}
	if code, out := postCardAction(t, s, "conv-2", oldCard, pageOfferKeep); code != http.StatusConflict || out["code"] != "view_changed" {
		t.Fatalf("keep an expired draft: %d %v", code, out)
	}
	if p, _ := database.AssistantPage(ctx, old); p.Status != db.AssistantPageRemoved {
		t.Fatalf("still expired: %+v", p)
	}

	// Dashboards off: nothing kept, the card not answered
	off, offCard := createPage(t, s, database, "conv-2", "Plants")
	setSettings(t, s, func(v *Settings) { v.Dashboards = nil })
	if code, out := postCardAction(t, s, "conv-2", offCard, pageOfferKeep); code != http.StatusConflict || out["code"] != "dashboards_off" {
		t.Fatalf("off: %d %v", code, out)
	}
	if p, _ := database.AssistantPage(ctx, off); p.Status != db.AssistantPageDraft {
		t.Fatalf("not kept while off: %+v", p)
	}
	if a, _ := database.AssistantViewAction(ctx, "conv-2", offCard); a != nil {
		t.Fatalf("the card is not answered: %+v", a)
	}
}

// waitCardAction is the owner's answer on card of conversation once it is stored.
func waitCardAction(t *testing.T, database *db.DB, conversation, card string) *db.AssistantViewActionRow {
	t.Helper()
	var a *db.AssistantViewActionRow
	waitFor(t, "the card's answer", func() bool {
		a, _ = database.AssistantViewAction(context.Background(), conversation, card)
		return a != nil
	})
	return a
}

// A deleted conversation's pages go to Recently removed with it, and come back as the main conversation's.
func TestPagesOfADeletedConversation(t *testing.T) {
	s, hub, database := pagesToolService(t)
	ctx := context.Background()
	id, card := createPage(t, s, database, "conv-1", "Reading list")
	if code, _ := postCardAction(t, s, "conv-1", card, pageOfferKeep); code != http.StatusOK {
		t.Fatalf("keep: %d", code)
	}
	draft, _ := createPage(t, s, database, "conv-1", "Weekly watch")
	lists := len(hub.sent("assistant_pages"))
	if err := s.deleteConversation(ctx, "server", "conv-1"); err != nil {
		t.Fatal(err)
	}
	p, _ := database.AssistantPage(ctx, id)
	if p.Status != db.AssistantPageRemoved || p.RemovedBy != db.AssistantPageRemovedByConversation || !p.Listed() {
		t.Fatalf("in Recently removed: %+v", p)
	}
	if d, _ := database.AssistantPage(ctx, draft); d.Status != db.AssistantPageRemoved || d.Listed() {
		t.Fatalf("its draft goes, unlisted: %+v", d)
	}
	if len(hub.sent("assistant_pages")) <= lists {
		t.Fatal("the places are told")
	}
	index := mustPage(t, s, pagesIndex, wire.UIRequest{}).View
	if row := pageItem(index, "removed:"+id); row.Title != "Reading list" {
		t.Fatalf("listed in Recently removed: %+v", index.Sections)
	}
	if res := mustPage(t, s, pagesIndex, wire.UIRequest{Action: "persona.restore:" + id}); res.Toast != "Restored" {
		t.Fatalf("restore: %+v", res)
	}
	p, _ = database.AssistantPage(ctx, id)
	if p.Status != db.AssistantPageActive || p.OwnerKind != db.AssistantPageOwnerMain || p.OwnerRef != "" {
		t.Fatalf("the main conversation's now: %+v", p)
	}
	if out := mustCallPage(t, s, "main-1", map[string]any{"action": "update", "id": id, "view": runView()}); out["version"] != float64(2) {
		t.Fatalf("main writes it: %v", out)
	}
}

// A thousand writes of one page: past 60 an hour each is refused as rate_limited, and the -pages events stay within
// their budget. Neither drafts nor updates push or count in the card ledger.
func TestPageWritesLimitedQuietAndUncounted(t *testing.T) {
	s, hub, database := pagesToolService(t)
	ctx := context.Background()
	ledger := func() string {
		var m json.RawMessage
		if _, err := database.Setting(ctx, cardLedgerKey, &m); err != nil {
			t.Fatal(err)
		}
		return string(m)
	}
	ledgerBefore := ledger()
	id, card := createPage(t, s, database, "main-1", "Morning run")
	if code, _ := postCardAction(t, s, "main-1", card, pageOfferKeep); code != http.StatusOK {
		t.Fatalf("keep: %d", code)
	}
	written, limited := 0, 0
	for i := 0; i < 1000; i++ {
		_, err := callPage(s, "server", "main-1", map[string]any{"action": "update", "id": id, "view": runView()})
		switch {
		case err == nil:
			written++
		case strings.HasPrefix(err.Error(), "rate_limited"):
			limited++
		default:
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if written != db.AssistantPageWritesPerHour-1 || limited != 1000-written { // the draft was the hour's first write
		t.Fatalf("written %d, limited %d", written, limited)
	}
	waitFor(t, "the last write told", func() bool {
		s.pages.events.mu.Lock()
		defer s.pages.events.mu.Unlock()
		_, waiting := s.pages.events.queue.pending[id]
		return !waiting
	})
	var events int
	for _, m := range append(hub.sent("extension_ui"), hub.sent("assistant_pages")...) {
		if m["type"] == "assistant_pages" || m["extension"] == extmgr.PagesExtension {
			events++
		}
	}
	if events == 0 || events > pageEventBudget {
		t.Fatalf("%d -pages events for a thousand writes (budget %d in %v)", events, pageEventBudget, pageEventWindow)
	}
	if p := hub.fakeHub.pushed(); len(p) != 0 {
		t.Fatalf("a draft or an update pushed: %+v", p)
	}
	if after := ledger(); after != ledgerBefore {
		t.Fatalf("the card ledger counted: %s -> %s", ledgerBefore, after)
	}
}

// persona_settings reads the Dashboards switch and never changes it: the owner does, in Settings › Assistant.
func TestDashboardsSettingReadOnly(t *testing.T) {
	s, _, _ := pagesToolService(t)
	get, err := runPageTool(s, "main-1", "persona_settings", map[string]any{"action": "get", "key": dashboardsSettingKey})
	if err != nil || get != "true" {
		t.Fatalf("get: %q %v", get, err)
	}
	if _, err := runPageTool(s, "main-1", "persona_settings", map[string]any{"action": "set", "key": dashboardsSettingKey, "value": false}); err == nil ||
		!strings.Contains(err.Error(), "does not take") {
		t.Fatalf("set: %v", err)
	}
	if !s.dashboardsOn(context.Background()) {
		t.Fatal("changed")
	}
	for _, l := range i18n.Langs {
		if title := i18n.T(l, "assistant-settings_tool.title_dashboards"); title == "" || strings.HasPrefix(title, "assistant-") {
			t.Fatalf("%s: no title", l)
		}
	}
	// the built-in manager names the capability, another manager does not
	if caps := withPages(nil, "", s); len(caps) != 0 {
		t.Fatalf("not the built-in manager's id: %v", caps)
	}
	if caps := withPages(nil, "assistant", s); !slices.Equal(caps, []string{"assistant_pages"}) {
		t.Fatalf("the built-in manager: %v", caps)
	}
	if caps := withPages(nil, "m-0d0d0d", s); len(caps) != 0 {
		t.Fatalf("another manager: %v", caps)
	}
}

// End to end over the real routes (phase 1's acceptance): the main conversation drafts a page, its preview is in the
// feed, the owner keeps it while the computer is offline and the places list it at once, a tap on the page is received
// within its request and delivered after the turn under way as the owner's message with the tag, the footer says
// Delivered and then Persona is working, and the page's update tells the apps and clears the footer at the turn's end.
func TestDashboardsEndToEnd(t *testing.T) {
	s, hub, database := pagesToolService(t)
	f := hub.fakeHub
	ctx, cancel := context.WithCancel(context.Background())
	ext := extmgr.New(extmgr.Options{Dir: t.TempDir(), Config: &extmgr.Config{}, Store: database, Pages: pagesHook{svc: func() *Service { return s }}})
	done := make(chan struct{})
	go func() { _ = ext.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("the extensions did not stop")
		}
	})
	select {
	case <-ext.Started():
	case <-time.After(10 * time.Second):
		t.Fatal("the extensions did not start")
	}
	extRequest := func(method, path, body string) (int, map[string]any) {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set(extwire.HeaderDevice, "phone")
		r.Header.Set(extwire.HeaderRole, "full")
		r.Header.Set(extwire.HeaderLang, "en")
		w := httptest.NewRecorder()
		ext.ServeHTTP(w, r)
		out := map[string]any{}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	pagesListed := func() []string {
		t.Helper()
		code, out := extRequest(http.MethodGet, "/api/v1/extensions/-/ui", "")
		if code != http.StatusOK {
			t.Fatalf("places: %d %v", code, out)
		}
		var ids []string
		for _, p := range out["ui"].([]any) {
			if place := p.(map[string]any); place["extension"] == extmgr.PagesExtension {
				ids = append(ids, place["id"].(string))
			}
		}
		return ids
	}
	if got := pagesListed(); got != nil {
		t.Fatalf("nothing kept, nothing listed: %v", got)
	}

	// 1. the main conversation drafts the page; its preview is in the feed
	created := mustCallPage(t, s, "main-1", map[string]any{"action": "create", "title": "Morning run", "icon": "heart",
		"why": "So you see your streak.", "view": runView()})
	id := created["id"].(string)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/assistant/conversations/main-1/feed", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var feed struct {
		Items []FeedItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &feed); err != nil || w.Code != http.StatusOK {
		t.Fatalf("feed: %d %v", w.Code, err)
	}
	p, _ := database.AssistantPage(context.Background(), id)
	if !slices.ContainsFunc(feed.Items, func(it FeedItem) bool { return it.Kind == feedView && it.Ref == p.OfferView }) {
		t.Fatalf("the preview is in the feed: %+v", feed.Items)
	}

	// 2. the computer goes offline; the owner's Keep makes the page at once
	f.mu.Lock()
	if f.nodes == nil {
		f.nodes = map[string]nodeAnswer{}
	}
	offline := func(string, json.RawMessage) (any, error) { return nil, fmt.Errorf("server: %w", apierr.ErrOffline) }
	f.nodes[DeviceSessionGet], f.nodes[DeviceSessionSendOnce] = offline, offline
	f.mu.Unlock()
	lists, navs := len(hub.sent("assistant_pages")), len(hub.sent("navigation"))
	code, out := postCardAction(t, s, "main-1", p.OfferView, pageOfferKeep)
	if code != http.StatusOK {
		t.Fatalf("keep: %d %v", code, out)
	}
	if got := pagesListed(); !slices.Equal(got, []string{pagesIndex}) {
		t.Fatalf("the Dashboards place at once: %v", got)
	}
	if len(hub.sent("assistant_pages")) != lists+1 || len(hub.sent("navigation")) != navs+1 {
		t.Fatal("assistant_pages and navigation are told before the answer")
	}
	answer := waitCardAction(t, database, "main-1", p.OfferView)
	waitFor(t, "the answer waiting for the computer", func() bool {
		answer, _ = database.AssistantViewAction(context.Background(), "main-1", p.OfferView)
		return answer.Status == "pending" && answer.Error != ""
	})
	f.mu.Lock()
	delete(f.nodes, DeviceSessionGet)
	delete(f.nodes, DeviceSessionSendOnce)
	sentBefore := len(f.sentOnce)
	f.mu.Unlock()

	// 3. a tap on the page: Received within its request
	state := pageState{V: 1}.encode()
	code, out = extRequest(http.MethodPost, "/api/v1/extensions/-pages/-/ui/"+id,
		`{"action":"done","values":{"minutes":"32"},"state":`+mustQuote(state)+`,"context":{"ui_version":10}}`)
	if code != http.StatusOK || out["toast"] != "Received" {
		t.Fatalf("the tap: %d %v", code, out)
	}
	tap := lastTap(t, database, id)
	sent := waitTap(t, database, tap.ID, func(a *db.AssistantPageActionRow) bool { return a.Status == db.AssistantPageTapSent })
	if sent.TargetConversation != "main-1" || sent.Redirected {
		t.Fatalf("delivered to the main conversation: %+v", sent)
	}
	// 4. the owner's message, with the tag the apps hide, after the turn under way
	f.mu.Lock()
	var message map[string]any
	for _, m := range f.sentOnce[sentBefore:] {
		if m["client_msg_id"] == tap.MessageID {
			message = m
		}
	}
	f.mu.Unlock()
	wantTag := `<persona-action view="-pages/` + id + `" action="done">{"values":{"minutes":"32"}}</persona-action>`
	if message == nil || message["session_id"] != "main-1" || !strings.HasSuffix(message["text"].(string), wantTag) || message["interject_background"] != false {
		t.Fatalf("the owner's message: %+v", message)
	}
	footer := func() wire.Component {
		t.Helper()
		code, out := extRequest(http.MethodPost, "/api/v1/extensions/-pages/-/ui/"+id, `{"context":{"ui_version":10}}`)
		if code != http.StatusOK {
			t.Fatalf("the page: %d %v", code, out)
		}
		raw, _ := json.Marshal(out["view"])
		var v wire.View
		_ = json.Unmarshal(raw, &v)
		return pageItem(&v, "persona.status")
	}
	if got := footer(); got.Text != "Delivered to Persona" {
		t.Fatalf("delivered: %+v", got)
	}
	// 5. its turn starts: Persona is working
	s.pageTurnStarted("server", "main-1", time.Now())
	waitTap(t, database, tap.ID, func(a *db.AssistantPageActionRow) bool { return a.TurnStartedAt != nil })
	if got := footer(); got.Text != "Persona is working" {
		t.Fatalf("working: %+v", got)
	}
	// 6. the turn updates the page: the apps are told to draw it again; at the turn's end the line goes
	told := len(pageInvalidations(hub, id))
	mustCallPage(t, s, "main-1", map[string]any{"action": "update", "id": id, "view": runView(), "note": "32 minutes today"})
	waitFor(t, "the page's invalidation", func() bool { return len(pageInvalidations(hub, id)) > told })
	s.pageTurnEnded("server", "main-1", time.Now())
	waitTap(t, database, tap.ID, func(a *db.AssistantPageActionRow) bool { return a.TurnEndedAt != nil })
	if got := footer(); got.Type != "" {
		t.Fatalf("the page answered, the line goes: %+v", got)
	}
	if s.pages.preempted.Load() != 0 {
		t.Fatal("a tap preempted a turn")
	}
}

func mustQuote(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}
