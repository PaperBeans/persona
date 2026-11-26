package extmgr

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// The sync's routes, under the bots' relayed prefix (/api/v1/bots/feishu/sync…): the view, the schedule, the sync
// bots' registration, the authorization and the bindings. Reads answer any device; changes need a full-access device,
// as the bot's other changes do. Tokens, secrets and codes never leave the manager.

// larkSyncView is GET /api/v1/bots/feishu/sync.
type larkSyncView struct {
	Available    bool                  `json:"available"`
	ScheduleHour int                   `json:"schedule_hour"`
	IMPoll       bool                  `json:"im_poll"`
	Accounts     []larkSyncAccountView `json:"accounts"`
	Bots         []larkSyncBotView     `json:"bots"`
	Bindings     []larkSyncBindingView `json:"bindings"`
}

type larkSyncAccountView struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	Brand string `json:"brand,omitempty"`
	Name  string `json:"name,omitempty"`
}

type larkSyncBotView struct {
	BotID     string `json:"bot_id"`
	Label     string `json:"label,omitempty"`
	AppID     string `json:"app_id,omitempty"`
	Domain    string `json:"domain,omitempty"`
	Role      string `json:"role"`
	Tier      string `json:"tier"`
	Approval  string `json:"approval,omitempty"`
	Primary   bool   `json:"primary"`
	HasSecret bool   `json:"has_secret"`
	// Registering: a registration is under way (its QR step is asked again with the register route).
	Registering bool `json:"registering,omitempty"`
}

type larkSyncBindingView struct {
	Key           string    `json:"key"`
	BotID         string    `json:"bot_id"`
	AccountID     string    `json:"account_id"`
	Label         string    `json:"label,omitempty"`
	Tier          string    `json:"tier"`
	Enabled       bool      `json:"enabled"`
	Health        string    `json:"health"`
	Error         string    `json:"error,omitempty"`
	GrantedScopes []string  `json:"granted_scopes,omitempty"`
	AuthorizedAt  time.Time `json:"authorized_at,omitzero"`
	ReauthDueDays *int      `json:"reauth_due_days,omitempty"`
	LastRunAt     time.Time `json:"last_run_at,omitzero"`
	NextRunAt     time.Time `json:"next_run_at,omitzero"`
	LastMessages  int       `json:"last_messages"`
	LastDocs      int       `json:"last_docs"`
	Running       bool      `json:"running"`
}

// larkSyncView is the view of the sync's settings.
func (h *Manager) larkSyncView(ctx context.Context) (larkSyncView, error) {
	st, err := h.larkSyncLoad(ctx)
	if err != nil {
		return larkSyncView{}, err
	}
	now := h.bots.sync.clock()
	v := larkSyncView{Available: h.larkSyncAvailable(), ScheduleHour: st.scheduleHour(), IMPoll: st.imPollEnabled(),
		Accounts: []larkSyncAccountView{}, Bots: []larkSyncBotView{}, Bindings: []larkSyncBindingView{}}
	for _, a := range st.accounts {
		v.Accounts = append(v.Accounts, larkSyncAccountView{ID: a.AccountID, Label: a.Label, Brand: a.Brand, Name: a.Name})
	}
	appID, secret, domain := h.botAppCredentials(ctx)
	if appID != "" {
		v.Bots = append(v.Bots, larkSyncBotView{BotID: larksyncwire.PrimaryBot, AppID: appID, Domain: domain,
			Role: larksyncwire.RoleChatAndSync, Tier: st.primaryTier(), Approval: st.prefs.PrimaryApproval, Primary: true,
			HasSecret: secret != "", Registering: st.prefs.PrimaryRegistration != nil})
	}
	for _, b := range st.bots {
		v.Bots = append(v.Bots, larkSyncBotView{BotID: b.BotID, Label: b.Label, AppID: b.AppID, Domain: b.Domain,
			Role: larksyncwire.RoleSyncOnly, Tier: st.tierOf(b.BotID), Approval: b.Approval, HasSecret: b.SecretEnc != "",
			Registering: b.Registration != nil})
	}
	for _, b := range st.bindings {
		if b.pending() {
			continue
		}
		label := b.Label
		if a := st.account(b.AccountID); label == "" && a != nil {
			label = a.Label
		}
		health := b.Health
		switch {
		case !b.authorized():
			health = larksyncwire.HealthUnauthorized
		case health == "":
			health = larksyncwire.HealthOK
		}
		if health == larksyncwire.HealthOK {
			if approval := st.prefs.PrimaryApproval; b.BotID == larksyncwire.PrimaryBot && approval == larksyncwire.ApprovalAwaiting {
				health = larksyncwire.HealthAwaiting
			} else if bot := st.bot(b.BotID); bot != nil && bot.Approval == larksyncwire.ApprovalAwaiting {
				health = larksyncwire.HealthAwaiting
			}
		}
		bv := larkSyncBindingView{Key: b.Key, BotID: b.BotID, AccountID: b.AccountID, Label: label, Tier: b.Tier,
			Enabled: b.Enabled, Health: health, Error: b.LastError, GrantedScopes: b.GrantedScopes, AuthorizedAt: b.AuthorizedAt,
			LastRunAt: b.LastRunAt, NextRunAt: b.NextRunAt, LastMessages: b.LastMessages, LastDocs: b.LastDocs,
			Running: h.larkSyncRuntime(b.Key).running.Load()}
		if bv.Tier == "" {
			bv.Tier = st.tierOf(b.BotID)
		}
		if b.authorized() {
			days := max(larkSyncDaysLeft(b, now), 0)
			bv.ReauthDueDays = &days
		}
		v.Bindings = append(v.Bindings, bv)
	}
	return v, nil
}

// larkSyncWriter says whether the request may change the sync (a full-access device, not an extension); it answers
// 403 when not.
func larkSyncWriter(w http.ResponseWriter, r *http.Request) bool {
	if p := principalOf(r.Context()); p.Role != roleFull || p.Extension != "" {
		writeErr(w, http.StatusForbidden, errors.New("this device can only read and answer"))
		return false
	}
	return true
}

// larkSyncErr answers err with its status.
func larkSyncErr(w http.ResponseWriter, _ *http.Request, err error) {
	code, slug := http.StatusBadGateway, "failed"
	switch {
	case errors.Is(err, errLarkSyncUnavailable):
		code, slug = http.StatusServiceUnavailable, "unavailable"
	case errors.Is(err, errLarkSyncUnread), errors.Is(err, errBotSettingsSave):
		code, slug = http.StatusServiceUnavailable, "settings_unavailable"
	case errors.Is(err, errLarkSyncNoBot), errors.Is(err, errLarkSyncNoBinding):
		code, slug = http.StatusNotFound, "not_found"
	case errors.Is(err, errLarkSyncNoApp):
		code, slug = http.StatusConflict, "no_app"
	case errors.Is(err, errLarkSyncRedirect):
		code, slug = http.StatusBadRequest, "bad_redirect"
	case errors.Is(err, errLarkSyncState), errors.Is(err, errLarkSyncExchange):
		code, slug = http.StatusBadRequest, "auth_failed"
	case errors.Is(err, errLarkSyncRunning):
		code, slug = http.StatusConflict, "running"
	}
	writeErrCode(w, code, slug, err)
}

// larkSyncDecode reads the request's JSON body into v (an empty body leaves v as it is); false when it answered.
func larkSyncDecode(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.ContentLength == 0 {
		return true
	}
	if err := decodeJSONBody(w, r, v, larkSyncBodyMost); err != nil {
		writeBodyErr(w, err, err)
		return false
	}
	return true
}

// writeLarkSyncView answers the view.
func (h *Manager) writeLarkSyncView(w http.ResponseWriter, r *http.Request) {
	v, err := h.larkSyncView(r.Context())
	if err != nil {
		larkSyncErr(w, r, err)
		return
	}
	writeJSON(w, v)
}

func (h *Manager) larkSyncRoutes(api *http.ServeMux) {
	const base = "/api/v1/bots/feishu/sync"
	api.HandleFunc("GET "+base, h.writeLarkSyncView)
	api.HandleFunc("PUT "+base, func(w http.ResponseWriter, r *http.Request) {
		if !larkSyncWriter(w, r) {
			return
		}
		var in struct {
			ScheduleHour *int  `json:"schedule_hour"`
			IMPoll       *bool `json:"im_poll"`
		}
		if !larkSyncDecode(w, r, &in) {
			return
		}
		if in.ScheduleHour != nil && (*in.ScheduleHour < 0 || *in.ScheduleHour > 23) {
			writeErrCode(w, http.StatusBadRequest, "bad_hour", errors.New("schedule_hour is 0 to 23"))
			return
		}
		if in.ScheduleHour != nil || in.IMPoll != nil {
			now := h.bots.sync.clock()
			if _, err := h.larkSyncChange(r.Context(), func(st *larkSyncState) error {
				if in.ScheduleHour != nil {
					v := *in.ScheduleHour
					st.prefs.ScheduleHour = &v
					for i := range st.bindings {
						if b := &st.bindings[i]; b.authorized() && b.Failures == 0 && b.NextRunAt.After(now) {
							b.NextRunAt = larkSyncNextSlot(now, v, b.Key)
						}
					}
				}
				if in.IMPoll != nil {
					v := *in.IMPoll
					st.prefs.IMPoll = &v
				}
				return nil
			}); err != nil {
				larkSyncErr(w, r, err)
				return
			}
		}
		h.writeLarkSyncView(w, r)
	})
	api.HandleFunc("POST "+base+"/bots", func(w http.ResponseWriter, r *http.Request) {
		if !larkSyncWriter(w, r) {
			return
		}
		var in struct {
			Label  string `json:"label"`
			Tier   string `json:"tier"`
			Region string `json:"region"`
		}
		if !larkSyncDecode(w, r, &in) {
			return
		}
		if !h.larkSyncAvailable() || h.registrar() == nil {
			larkSyncErr(w, r, errLarkSyncUnavailable)
			return
		}
		if !larksyncwire.ValidTier(in.Tier) {
			in.Tier = larksyncwire.TierDocs
		}
		id := "b" + randomHex(5)
		now := h.bots.sync.clock()
		if _, err := h.larkSyncChange(r.Context(), func(st *larkSyncState) error {
			st.bots = append(st.bots, larkSyncBot{BotID: id, Label: cutUTF8(strings.TrimSpace(in.Label), 80),
				Role: larksyncwire.RoleSyncOnly, Tier: in.Tier, CreatedAt: now})
			return nil
		}); err != nil {
			larkSyncErr(w, r, err)
			return
		}
		p, err := h.larkSyncBeginRegistration(r.Context(), id, in.Tier, in.Region, "")
		if err != nil {
			// A bot that never got an app is not kept.
			_, _ = h.larkSyncChange(context.WithoutCancel(r.Context()), func(st *larkSyncState) error {
				st.bots = slices.DeleteFunc(st.bots, func(b larkSyncBot) bool { return b.BotID == id })
				return nil
			})
			larkSyncErr(w, r, err)
			return
		}
		writeJSON(w, map[string]any{"bot_id": id, "prompt": p})
	})
	api.HandleFunc("POST "+base+"/bots/{bot}/register", func(w http.ResponseWriter, r *http.Request) {
		if !larkSyncWriter(w, r) {
			return
		}
		var in struct {
			Step string `json:"step"`
		}
		if !larkSyncDecode(w, r, &in) {
			return
		}
		p, err := h.larkSyncPollRegistration(r.Context(), r.PathValue("bot"))
		if err != nil {
			larkSyncErr(w, r, err)
			return
		}
		writeJSON(w, p)
	})
	api.HandleFunc("POST "+base+"/bots/{bot}/scopes", func(w http.ResponseWriter, r *http.Request) {
		if !larkSyncWriter(w, r) {
			return
		}
		var in struct {
			Tier string `json:"tier"`
		}
		if !larkSyncDecode(w, r, &in) {
			return
		}
		if !larksyncwire.ValidTier(in.Tier) {
			in.Tier = larksyncwire.TierDocs
		}
		bot := r.PathValue("bot")
		st, err := h.larkSyncLoad(r.Context())
		if err != nil {
			larkSyncErr(w, r, err)
			return
		}
		app, err := h.larkSyncApp(r.Context(), &st, bot)
		if err != nil {
			larkSyncErr(w, r, err)
			return
		}
		region := larkSyncBrand(app.Domain)
		p, err := h.larkSyncBeginRegistration(r.Context(), bot, in.Tier, region, app.AppID)
		if err != nil {
			larkSyncErr(w, r, err)
			return
		}
		writeJSON(w, p)
	})
	api.HandleFunc("DELETE "+base+"/bots/{bot}", func(w http.ResponseWriter, r *http.Request) {
		if !larkSyncWriter(w, r) {
			return
		}
		bot := r.PathValue("bot")
		if bot == larksyncwire.PrimaryBot {
			writeErrCode(w, http.StatusConflict, "primary", errors.New("the chat bot is removed in its own settings"))
			return
		}
		var dropped []string
		if _, err := h.larkSyncChange(r.Context(), func(st *larkSyncState) error {
			if st.bot(bot) == nil {
				return errLarkSyncNoBot
			}
			st.bots = slices.DeleteFunc(st.bots, func(b larkSyncBot) bool { return b.BotID == bot })
			st.bindings = slices.DeleteFunc(st.bindings, func(b larkSyncBinding) bool {
				if b.BotID == bot && !b.pending() {
					dropped = append(dropped, b.Key)
				}
				return b.BotID == bot
			})
			return nil
		}); err != nil {
			larkSyncErr(w, r, err)
			return
		}
		h.larkSyncForget(r.Context(), dropped...)
		h.auditBot(r.Context(), botRecord{auditLarkSyncRevoke, map[string]any{"bot_id": bot}})
		h.writeLarkSyncView(w, r)
	})
	api.HandleFunc("POST "+base+"/bots/{bot}/authorize", func(w http.ResponseWriter, r *http.Request) {
		if !larkSyncWriter(w, r) {
			return
		}
		var in struct {
			RedirectURI string `json:"redirect_uri"`
			Tier        string `json:"tier"`
		}
		if !larkSyncDecode(w, r, &in) {
			return
		}
		out, err := h.larkSyncAuthorize(r.Context(), r.PathValue("bot"), in.RedirectURI, in.Tier)
		if err != nil {
			larkSyncErr(w, r, err)
			return
		}
		writeJSON(w, out)
	})
	api.HandleFunc("POST "+base+"/bots/{bot}/authorize/poll", func(w http.ResponseWriter, r *http.Request) {
		if !larkSyncWriter(w, r) {
			return
		}
		p, err := h.larkSyncPollDevice(r.Context(), r.PathValue("bot"))
		if err != nil {
			larkSyncErr(w, r, err)
			return
		}
		writeJSON(w, p)
	})
	api.HandleFunc("POST "+base+"/oauth", func(w http.ResponseWriter, r *http.Request) {
		if !larkSyncWriter(w, r) {
			return
		}
		var in struct {
			State string `json:"state"`
			Code  string `json:"code"`
		}
		if !larkSyncDecode(w, r, &in) {
			return
		}
		if err := h.larkSyncCallback(r.Context(), in.State, in.Code); err != nil {
			larkSyncErr(w, r, err)
			return
		}
		h.writeLarkSyncView(w, r)
	})
	api.HandleFunc("PUT "+base+"/bindings/{key}", func(w http.ResponseWriter, r *http.Request) {
		if !larkSyncWriter(w, r) {
			return
		}
		var in struct {
			Enabled *bool   `json:"enabled"`
			Label   *string `json:"label"`
			Tier    *string `json:"tier"`
		}
		if !larkSyncDecode(w, r, &in) {
			return
		}
		if in.Tier != nil && !larksyncwire.ValidTier(*in.Tier) {
			writeErrCode(w, http.StatusBadRequest, "bad_tier", errors.New("unknown tier"))
			return
		}
		key := r.PathValue("key")
		now := h.bots.sync.clock()
		if _, err := h.larkSyncChange(r.Context(), func(st *larkSyncState) error {
			b := st.binding(key)
			if b == nil || b.pending() {
				return errLarkSyncNoBinding
			}
			if in.Enabled != nil {
				if *in.Enabled && !b.Enabled && b.NextRunAt.Before(now) {
					b.NextRunAt = larkSyncNextSlot(now, st.scheduleHour(), key)
				}
				b.Enabled = *in.Enabled
			}
			if in.Label != nil {
				b.Label = cutUTF8(strings.TrimSpace(*in.Label), 80)
			}
			if in.Tier != nil {
				b.Tier = *in.Tier
			}
			return nil
		}); err != nil {
			larkSyncErr(w, r, err)
			return
		}
		h.writeLarkSyncView(w, r)
	})
	api.HandleFunc("DELETE "+base+"/bindings/{key}", func(w http.ResponseWriter, r *http.Request) {
		if !larkSyncWriter(w, r) {
			return
		}
		key := r.PathValue("key")
		if _, err := h.larkSyncChange(r.Context(), func(st *larkSyncState) error {
			if b := st.binding(key); b == nil || b.pending() {
				return errLarkSyncNoBinding
			}
			st.bindings = slices.DeleteFunc(st.bindings, func(b larkSyncBinding) bool { return b.Key == key })
			return nil
		}); err != nil {
			larkSyncErr(w, r, err)
			return
		}
		h.larkSyncForget(r.Context(), key)
		h.auditBot(r.Context(), botRecord{auditLarkSyncRevoke, map[string]any{"binding": key}})
		h.writeLarkSyncView(w, r)
	})
	api.HandleFunc("POST "+base+"/bindings/{key}/run", func(w http.ResponseWriter, r *http.Request) {
		if !larkSyncWriter(w, r) {
			return
		}
		if !h.larkSyncAvailable() {
			larkSyncErr(w, r, errLarkSyncUnavailable)
			return
		}
		key := r.PathValue("key")
		st, err := h.larkSyncLoad(r.Context())
		if err != nil {
			larkSyncErr(w, r, err)
			return
		}
		if b := st.binding(key); b == nil || !b.authorized() {
			larkSyncErr(w, r, errLarkSyncNoBinding)
			return
		}
		base := h.ctx
		if base == nil {
			base = context.WithoutCancel(r.Context())
		}
		h.larkSyncStartRun(base, key) // a run under way already is the run asked for
		h.writeLarkSyncView(w, r)
	})
}

// larkSyncForget drops what the manager kept for removed bindings: their cursors and runtimes. What they read stays
// in the knowledge base.
func (h *Manager) larkSyncForget(ctx context.Context, keys ...string) {
	store, _ := h.store.(larkSyncStore)
	for _, k := range keys {
		if store != nil {
			if err := store.DeleteLarkSyncCursors(context.WithoutCancel(ctx), k); err != nil {
				h.logf("lark sync: %s: drop cursors: %v", k, err)
			}
		}
		h.larkSyncDropRuntime(k)
	}
}

// Ensure the connector's interfaces stay what the knowledge base asks for.
var (
	_ connector.Archive      = larkSyncConnector{}
	_ connector.ArchiveMedia = larkSyncConnector{}
	_ connector.Items        = larkSyncConnector{}
	_ connector.Accounts     = larkSyncConnector{}
)
