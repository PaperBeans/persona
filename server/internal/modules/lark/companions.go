package lark

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/botcard"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
)

// Companion bots (#1961, docs/extensions/bots.md "Companion bots"): other Lark bots an extension runs for the owner
// (an Inbox bot, a notifications bot) register here. With the workbench on, the home card links to each, and while a
// notifications companion is polling, the bot's pushes, scheduled-task results and task reports go to it (GET
// …/notices) instead of the bot's chat. Questions, approvals and anything else that answers for the owner stay in the
// bot's chat: only the bot's own cards carry the owner's action. The hub knows nothing of the companions; with none
// registered, or the workbench off, nothing here changes what the bot does.

// The kinds of companion the workbench treats on its own: the Inbox's (its button replaces the /inbox one) and the
// notifications' (it takes the notices). Any other kind is only a link.
const (
	CompanionInbox  = "inbox"
	CompanionNotify = "notify"
	CompanionOther  = "other"
)

const (
	// companionsMost is how many companions one hub keeps.
	companionsMost = 6
	// companionFresh is how recently the notifications companion must have polled to take the notices: a companion
	// that stopped (its extension switched off, uninstalled, crashed) gives them back to the bot's chat.
	companionFresh = 90 * time.Second
	// noticesKeep is how many notices are kept for the companion to fetch (they live in memory only).
	noticesKeep = 200
	// noticesWaitMost bounds a long poll.
	noticesWaitMost = 50 * time.Second
)

var companionAppID = lazyre.New(`^cli_[A-Za-z0-9]{6,40}$`)

// CompanionBot is another Lark bot the workbench links to (assistantwire.CompanionBot, kept in the chat's state).
type CompanionBot = assistantwire.CompanionBot

// companionLink opens the chat with the bot in the Lark app (applink; the web client may not follow it).
func companionLink(b CompanionBot) string {
	return "https://applink.feishu.cn/client/bot/open?appId=" + url.QueryEscape(b.AppID)
}

func companionValid(b CompanionBot) bool {
	switch b.Kind {
	case CompanionInbox, CompanionNotify, CompanionOther:
	default:
		return false
	}
	n := utf8.RuneCountInString(b.Name)
	return companionAppID.MatchString(b.AppID) && n > 0 && n <= 20 && utf8.RuneCountInString(b.Icon) <= 4
}

// notice is one card for the notifications companion: the bot's card as Lark card JSON, and its fallback.
type notice struct {
	Seq  int                      `json:"seq"`
	Kind assistantwire.NoticeKind `json:"kind"`
	Card json.RawMessage          `json:"card"`
	Text string                   `json:"text,omitempty"` // the fallback, for a card Lark refuses
	At   time.Time                `json:"at"`
}

// companionState is the notices waiting for the notifications companion and when it last polled. boot changes when the
// list starts over (a restart): a poll from before starts from the beginning.
type companionState struct {
	mu     sync.Mutex
	boot   string
	seq    int
	list   []notice
	wait   chan struct{}
	polled time.Time
}

func (c *companionState) bootLocked() string {
	if c.boot == "" {
		c.boot = strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return c.boot
}

// companionBots are the registered companions.
func (s core) companionBots(ctx context.Context) []CompanionBot {
	var bots []CompanionBot
	s.book(ctx, func(b chatBook) { bots = b.Companions() })
	return bots
}

// noticesTaken: with the workbench on, a notifications companion is registered and polling, so it takes the notices.
func (s core) noticesTaken(ctx context.Context) bool {
	if !s.t.wb.on.Load() {
		return false
	}
	c := &s.t.comp
	c.mu.Lock()
	fresh := !c.polled.IsZero() && time.Since(c.polled) < companionFresh
	c.mu.Unlock()
	if !fresh {
		return false
	}
	for _, b := range s.companionBots(ctx) {
		if b.Kind == CompanionNotify {
			return true
		}
	}
	return false
}

// divertNotices hands cards to the notifications companion when it takes them; false: they go to the bot's chat.
func (s core) divertNotices(ctx context.Context, kind assistantwire.NoticeKind, cards []*botcard.Card, fallbacks []string) bool {
	kind.RequireKnown()
	if len(cards) == 0 || !s.noticesTaken(ctx) {
		return false
	}
	c := &s.t.comp
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bootLocked()
	now := time.Now().UTC()
	for i, card := range cards {
		c.seq++
		n := notice{Seq: c.seq, Kind: kind, Card: json.RawMessage(card.JSON()), At: now}
		if i < len(fallbacks) {
			n.Text = fallbacks[i]
		}
		c.list = append(c.list, n)
	}
	if n := len(c.list); n > noticesKeep {
		c.list = c.list[n-noticesKeep:]
	}
	if c.wait != nil {
		close(c.wait)
		c.wait = nil
	}
	return true
}

// notices are the notices after seq after (all of them when boot is not the current list's), waiting up to wait for
// the next; the poll counts as the companion being there.
func (s core) notices(ctx context.Context, boot string, after int, wait time.Duration) (string, []notice) {
	c := &s.t.comp
	deadline := time.Now().Add(min(max(wait, 0), noticesWaitMost))
	for {
		c.mu.Lock()
		c.polled = time.Now()
		cur := c.bootLocked()
		if boot != cur {
			after = 0
		}
		var out []notice
		for _, n := range c.list {
			if n.Seq > after {
				out = append(out, n)
			}
		}
		if len(out) > 0 || !time.Now().Before(deadline) {
			c.mu.Unlock()
			return cur, out
		}
		if c.wait == nil {
			c.wait = make(chan struct{})
		}
		ch := c.wait
		c.mu.Unlock()
		select {
		case <-ch:
		case <-time.After(time.Until(deadline)):
		case <-ctx.Done():
			return cur, nil
		}
	}
}

// The four routes the companions use, served through the module's hub-route registration (Module.Register,
// module.json hub_routes): with the module off or built out the hub has no such routes and answers 404.
const (
	routeCompanions    = "PUT /api/v1/assistant/feishu/companions"
	routeNotices       = "GET /api/v1/assistant/feishu/notices"
	routeNoticesAction = "POST /api/v1/assistant/feishu/notices/action"
	routeWorkbenchPost = "POST /api/v1/assistant/feishu/workbench"
	roleFull           = "full"
)

// fullRole: the request came from a device that may do everything (the hub's role header; none: a test's own request).
func fullRole(r *http.Request) bool {
	role := r.Header.Get(assistantwire.RoleHeader)
	return role == "" || role == roleFull
}

// withRequestLang puts the request's language on its context.
func withRequestLang(r *http.Request) *http.Request {
	if l := i18n.FromRequest(r); l != "" {
		return r.WithContext(i18n.With(r.Context(), l))
	}
	return r
}

// putCompanions: PUT /api/v1/assistant/feishu/companions {bots: [CompanionBot]}: the companions, all of them (an empty
// list removes them). 400 for one that is not valid.
func (s core) putCompanions(w http.ResponseWriter, r *http.Request) {
	if !fullRole(r) {
		apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot change the companions"))
		return
	}
	var in struct {
		Bots []CompanionBot `json:"bots"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		apierr.Write(w, http.StatusBadRequest, err)
		return
	}
	if len(in.Bots) > companionsMost {
		apierr.Write(w, http.StatusBadRequest, errors.New("too many companions"))
		return
	}
	for _, b := range in.Bots {
		if !companionValid(b) {
			apierr.Write(w, http.StatusBadRequest, errors.New("a companion needs a known kind, a name of at most 20 characters and a cli_ app id"))
			return
		}
	}
	saved := false
	s.book(r.Context(), func(b chatBook) {
		if saved = b.Loaded(); saved {
			b.SetCompanions(in.Bots)
		}
	})
	if !saved {
		apierr.Write(w, http.StatusServiceUnavailable, i18n.Err("http-status.unavailable"))
		return
	}
	s.boardChanged() // the home card links to them
	apierr.WriteJSON(w, map[string]any{"bots": in.Bots})
}

// getNotices: GET /api/v1/assistant/feishu/notices?boot=&after=&wait=: the notifications companion's long poll, notices
// after seq after in list boot, waiting up to wait seconds (at most 50) for one.
func (s core) getNotices(w http.ResponseWriter, r *http.Request) {
	if !fullRole(r) {
		apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot take notices"))
		return
	}
	q := r.URL.Query()
	after, _ := strconv.Atoi(q.Get("after"))
	wait, _ := strconv.Atoi(q.Get("wait"))
	boot, list := s.notices(r.Context(), q.Get("boot"), after, time.Duration(wait)*time.Second)
	if list == nil {
		list = []notice{}
	}
	apierr.WriteJSON(w, map[string]any{"boot": boot, "notices": list})
}

// postNoticesAction: POST /api/v1/assistant/feishu/notices/action: the owner pressed a notice's button in the
// companion's chat (the companion checked it is the owner). Only what needs no owner's action is taken (a task
// verified, a workbench button); a question or an approval is answered in the bot's chat.
func (s core) postNoticesAction(w http.ResponseWriter, r *http.Request) {
	if !fullRole(r) {
		apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot press cards"))
		return
	}
	var a botcard.CardAction
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		apierr.Write(w, http.StatusBadRequest, err)
		return
	}
	ctx := withRequestLang(r).Context()
	switch a.Str("a") {
	case "accept", "nav":
	default:
		apierr.WriteJSON(w, toast("info", wbT(s.botLang(ctx), "answer_in_workbench")))
		return
	}
	apierr.WriteJSON(w, s.botCardAction(ctx, &a, ""))
}

// postWorkbench: POST /api/v1/assistant/feishu/workbench: bring up the workbench (as /home does). 409 with the switch off.
func (s core) postWorkbench(w http.ResponseWriter, r *http.Request) {
	if !fullRole(r) {
		apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot post the workbench"))
		return
	}
	ctx := withRequestLang(r).Context()
	if f, ready := s.feishuFeatures(ctx); !s.workbenchOn(f, ready) {
		apierr.Write(w, http.StatusConflict, errors.New("the Lark bot's workbench is off"))
		return
	}
	go func() {
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s.summonWorkbench(cctx)
	}()
	apierr.WriteJSON(w, map[string]any{"ok": true})
}

// workbenchCompanions are the companions the home card links to.
func (s core) workbenchCompanions(ctx context.Context) []assistantwire.WorkbenchCompanion {
	var out []assistantwire.WorkbenchCompanion
	for _, b := range s.companionBots(ctx) {
		out = append(out, assistantwire.WorkbenchCompanion{Name: b.Name, Link: companionLink(b), Inbox: b.Kind == CompanionInbox})
	}
	return out
}
