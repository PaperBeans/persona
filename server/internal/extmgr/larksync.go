package extmgr

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/botcard"
	"github.com/nwverse/persona/server/internal/botport"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// The Lark user sync (docs/extensions/bots.md "Reading as you: daily sync"): the owner authorizes a bot's app to read
// as them, and every day each (account × bot) binding reads what changed into the knowledge base through the
// larksync connector. The module (internal/modules/lark, through botport's Sync provider) owns the open platform: the
// OAuth and the readers. This code owns the state: the accounts, the extra bots and the bindings are settings (their
// tokens sealed, in top-level fields of list elements, where the key rotation re-seals them: db.sealedSettings); the
// cursors, the dedup index and the outbox are Postgres tables (larkSyncStore).
//
// The primary bot is the chat bot of bots.feishu, never copied here: its app is read from there (the hub.json
// fallback included) whenever it is needed, and only what the sync adds to it (its tier, approval, the redirect URL
// configured on it, a scopes registration under way) lives in bots.feishu.sync. Settings from before the sync have
// none of these keys and keep working unchanged.

const (
	larkSyncPrefsKey    = "bots.feishu.sync"
	larkSyncBotsKey     = "bots.feishu.sync.bots"
	larkSyncBindingsKey = "bots.feishu.sync.bindings"
	larkSyncAccountsKey = "bots.feishu.sync.accounts"

	larkSyncDefaultHour = 3                    // local time of the daily run when the owner never chose one
	larkSyncAuthFor     = 10 * time.Minute     // how long an authorization started on the page stays valid
	larkSyncAuthLasts   = 365 * 24 * time.Hour // how long an authorization lasts, at most (the platform's limit)
	larkSyncRefreshAt   = 15 * time.Minute     // an access token closer than this to its end is refreshed first
	larkSyncBackfill    = 30 * 24 * time.Hour  // how far back a binding's first run reads
	larkSyncBudget      = 20 * time.Minute     // one full run's budget: what is left is read next time
	larkSyncIMBudget    = 5 * time.Minute      // one lightweight IM incremental run's budget
	larkSyncIMEvery     = 15 * time.Minute     // how often chat-enabled bindings run a lightweight IM incremental sync
	larkSyncPendingTail = "~pending"           // the key suffix of a bot's authorization under way
	larkSyncDocsWindow  = 7 * 24 * time.Hour   // how far back the connector's Items reach
	larkSyncOutboxKeep  = 30 * 24 * time.Hour  // how long outbox rows are kept
	larkSyncPruneEvery  = 24 * time.Hour       // how often the outbox is pruned
	larkSyncTickEvery   = 5 * time.Minute      // how often the scheduler looks for due bindings
	larkSyncParallel    = 4                    // bindings run at once, at most
	larkSyncFailSoon    = 15 * time.Minute     // the retry after one of the first failed runs
	larkSyncPartialNext = time.Hour            // the next run after a partial or rate-limited one
	larkSyncBodyMost    = int64(1 << 20)       // request bodies of the sync's routes
	larkSyncChangesMost = 100                  // chat batches one Changes call hands over by default
	larkSyncStaleAuth   = larkSyncAuthFor + time.Minute
	// larkSyncBackfillVersion triggers the one-time idempotent sender-name backfill for outbox messages:
	// v1 resolved contact/v3 names for pre-#15486 rows; v2 adds chat-member, p2p chat-info, and message-carried
	// fallbacks (plus unnamed DM chat name repair) for personal/external tenants where contact/v3 misses; v3 reads a
	// large group's member list on demand past the old 20-page cap, and names members who left the group from the
	// binding's name book and from @mentions around their messages.
	larkSyncBackfillVersion = 3
	// larkSyncLinksBackfillVersion triggers the one-time idempotent remediation that scans already-synced chat
	// outbox messages and enqueues cloud document / wiki links missing body content in lark_sync_index.
	larkSyncLinksBackfillVersion = 1
)

// larkSyncBackoff is the wait after the third, fourth and later consecutive failed runs (a binding's circuit).
var larkSyncBackoff = []time.Duration{time.Hour, 6 * time.Hour, 24 * time.Hour}

// larkSyncReminders are the days before an authorization ends when the owner is reminded, once each.
var larkSyncReminders = []int{14, 7, 1}

// larkSyncPrefs is bots.feishu.sync: the schedule and what the sync adds to the primary bot. Its sealed field is
// top-level (db.sealedSettings).
type larkSyncPrefs struct {
	ScheduleHour *int `json:"schedule_hour,omitempty"`
	// IMPoll enables the 15-minute lightweight IM incremental sync for chat-enabled (full_with_im) bindings;
	// nil reads as on by default so existing and new bindings get timely chat updates and can turn it off in Settings.
	IMPoll *bool `json:"im_poll,omitempty"`
	// PrimaryTier is the scope tier the primary app asks for as the owner (a first registration included); "" is
	// TierDocs.
	PrimaryTier     string `json:"primary_tier,omitempty"`
	PrimaryApproval string `json:"primary_approval,omitempty"`
	// PrimaryRedirect is the redirect URL configured on the primary app ("" until it is).
	PrimaryRedirect string `json:"primary_redirect,omitempty"`
	// PrimaryRegistration is adding the user scopes to the primary app, under way; its device code is sealed in
	// RegistrationDeviceEnc.
	PrimaryRegistration   *larkSyncRegistration `json:"primary_registration,omitempty"`
	RegistrationDeviceEnc string                `json:"registration_device_enc,omitempty"`
}

// larkSyncRegistration is a registration of a sync bot's app (a new app, or the user scopes added to an existing one)
// under way. The device code is sealed in the owner's registration_device_enc.
type larkSyncRegistration struct {
	Region   string    `json:"region"`
	URL      string    `json:"url"`
	Expires  time.Time `json:"expires"`
	Interval int       `json:"interval"`
	Tier     string    `json:"tier"`
	// AddTo is the app the scopes are added to ("" for a new app).
	AddTo   string `json:"add_to,omitempty"`
	Failure string `json:"failure,omitempty"` // localization key of a terminal outcome
}

// larkSyncBot is one extra bot of bots.feishu.sync.bots: an app only for reading as the owner (no events, no long
// connection), possibly in another tenant or on the other cloud. Its sealed fields are top-level (db.sealedSettings).
type larkSyncBot struct {
	BotID                 string                `json:"bot_id"`
	Label                 string                `json:"label,omitempty"`
	AppID                 string                `json:"app_id,omitempty"`
	SecretEnc             string                `json:"secret_enc,omitempty"`
	Domain                string                `json:"domain,omitempty"`
	Role                  string                `json:"role"`
	Tier                  string                `json:"tier"`
	Approval              string                `json:"approval,omitempty"`
	CreatedAt             time.Time             `json:"created_at,omitzero"`
	Redirect              string                `json:"redirect,omitempty"` // the redirect URL configured on the app
	Registration          *larkSyncRegistration `json:"registration,omitempty"`
	RegistrationDeviceEnc string                `json:"registration_device_enc,omitempty"`
}

// larkSyncBinding is one (account, bot) pair of bots.feishu.sync.bindings, or a bot's authorization under way (Key
// "<bot>~pending", no account yet). Its sealed fields are top-level (db.sealedSettings).
type larkSyncBinding struct {
	Key              string    `json:"key"`
	BotID            string    `json:"bot_id"`
	AccountID        string    `json:"account_id,omitempty"`
	Label            string    `json:"label,omitempty"`
	Tier             string    `json:"tier"`
	Enabled          bool      `json:"enabled"`
	AccessTokenEnc   string    `json:"access_token_enc,omitempty"`
	RefreshTokenEnc  string    `json:"refresh_token_enc,omitempty"`
	TokenExpiresAt   time.Time `json:"token_expires_at,omitzero"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at,omitzero"`
	AuthorizedAt     time.Time `json:"authorized_at,omitzero"`
	GrantedScopes    []string  `json:"granted_scopes,omitempty"`
	Health           string    `json:"health,omitempty"`
	LastError        string    `json:"last_error,omitempty"`
	LastRunAt        time.Time `json:"last_run_at,omitzero"`
	NextRunAt        time.Time `json:"next_run_at,omitzero"`
	LastIMRunAt      time.Time `json:"last_im_run_at,omitzero"`
	NextIMRunAt      time.Time `json:"next_im_run_at,omitzero"`
	LastMessages     int       `json:"last_messages,omitempty"`
	LastDocs         int       `json:"last_docs,omitempty"`
	RemindedDays     []int     `json:"reminded_days,omitempty"`
	// Failures counts the consecutive failed runs (the circuit, larkSyncBackoff).
	Failures int `json:"failures,omitempty"`
	// BackfillVersion records the highest completed one-time data repair version for this binding (larkSyncBackfillVersion).
	BackfillVersion int `json:"backfill_version,omitempty"`
	// LinksBackfillVersion records the highest completed one-time dropped-links remediation version (larkSyncLinksBackfillVersion).
	LinksBackfillVersion int `json:"links_backfill_version,omitempty"`
	// An authorization under way: browser (State, the PKCE verifier and the redirect), or device (the device code).
	AuthState       string    `json:"auth_state,omitempty"`
	AuthVerifierEnc string    `json:"auth_verifier_enc,omitempty"`
	AuthRedirect    string    `json:"auth_redirect,omitempty"`
	AuthStartedAt   time.Time `json:"auth_started_at,omitzero"`
	AuthDeviceEnc   string    `json:"auth_device_enc,omitempty"`
	AuthMode        string    `json:"auth_mode,omitempty"`
	AuthInterval    int       `json:"auth_interval,omitempty"`
	AuthExpiresAt   time.Time `json:"auth_expires_at,omitzero"`
}

// pending says whether b is an authorization under way rather than a binding.
func (b larkSyncBinding) pending() bool { return b.AccountID == "" }

// authorized says whether b has a token pair to read with.
func (b larkSyncBinding) authorized() bool {
	return !b.pending() && !b.AuthorizedAt.IsZero() && b.RefreshTokenEnc != ""
}

// authEnds is when b's authorization ends: a year after it was given, or earlier when the platform said so.
func (b larkSyncBinding) authEnds() time.Time {
	end := b.AuthorizedAt.Add(larkSyncAuthLasts)
	if !b.RefreshExpiresAt.IsZero() && b.RefreshExpiresAt.Before(end) {
		end = b.RefreshExpiresAt
	}
	return end
}

// larkSyncAccount is one Lark account the owner authorized as (bots.feishu.sync.accounts): who it is, nothing secret.
type larkSyncAccount struct {
	AccountID string `json:"account_id"` // "<brand>:<tenant_key>:<open_id>"
	Label     string `json:"label,omitempty"`
	Brand     string `json:"brand,omitempty"`
	TenantKey string `json:"tenant_key,omitempty"`
	OpenID    string `json:"open_id,omitempty"`
	UnionID   string `json:"union_id,omitempty"`
	Name      string `json:"name,omitempty"`
}

// larkSyncState is the four settings as one value.
type larkSyncState struct {
	prefs    larkSyncPrefs
	bots     []larkSyncBot
	bindings []larkSyncBinding
	accounts []larkSyncAccount
}

func (st *larkSyncState) bot(id string) *larkSyncBot {
	for i := range st.bots {
		if st.bots[i].BotID == id {
			return &st.bots[i]
		}
	}
	return nil
}

func (st *larkSyncState) binding(key string) *larkSyncBinding {
	for i := range st.bindings {
		if st.bindings[i].Key == key {
			return &st.bindings[i]
		}
	}
	return nil
}

func (st *larkSyncState) account(id string) *larkSyncAccount {
	for i := range st.accounts {
		if st.accounts[i].AccountID == id {
			return &st.accounts[i]
		}
	}
	return nil
}

// primaryTier is the tier of the primary app.
func (st *larkSyncState) primaryTier() string {
	if larksyncwire.ValidTier(st.prefs.PrimaryTier) {
		return st.prefs.PrimaryTier
	}
	return larksyncwire.TierDocs
}

// scheduleHour is the local hour of the daily run.
func (st *larkSyncState) scheduleHour() int {
	if h := st.prefs.ScheduleHour; h != nil && *h >= 0 && *h < 24 {
		return *h
	}
	return larkSyncDefaultHour
}

// imPollEnabled says whether high-frequency (15-minute) IM incremental polling is on (default true when unset).
func (st *larkSyncState) imPollEnabled() bool {
	return st.prefs.IMPoll == nil || *st.prefs.IMPoll
}

// larkSync is the manager's sync: the bindings' runtimes, the settings' lock and the seams tests replace.
type larkSync struct {
	// mu orders every read-modify-write of the four settings; never held across a network call.
	mu sync.Mutex
	// last is the state as last read or written, for what answers from memory (the connector's Status).
	last     larkSyncState
	haveLast bool

	rtMu     sync.Mutex
	runtimes map[string]*larkBindingRuntime

	// regMu orders the sync bots' registration steps (a begin or a poll at a time).
	regMu sync.Mutex

	// Seams (tests): the OAuth client and the readers (nil: the module's, through botport), the clock and the
	// scheduler's tick.
	userAuth  larksyncwire.UserAuth
	newSyncer func() larksyncwire.Syncer
	now       func() time.Time
	tick      <-chan time.Time

	authOnce sync.Once
	auth     larksyncwire.UserAuth

	lastPrune time.Time
}

func (s *larkSync) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// larkSyncUserAuth is the OAuth client; nil in a build without the lark module.
func (h *Manager) larkSyncUserAuth() larksyncwire.UserAuth {
	s := &h.bots.sync
	if s.userAuth != nil {
		return s.userAuth
	}
	s.authOnce.Do(func() {
		if botport.Available(h.modules()) {
			s.auth = botport.NewUserAuth("", "", nil)
		}
	})
	return s.auth
}

// larkSyncNewSyncer is a new binding's reader; nil in a build without the lark module.
func (h *Manager) larkSyncNewSyncer() larksyncwire.Syncer {
	s := &h.bots.sync
	if s.newSyncer != nil {
		return s.newSyncer()
	}
	if !botport.Available(h.modules()) {
		return nil
	}
	return botport.NewSyncer(nil, s.now)
}

// larkSyncAvailable says whether the sync can run here: the module's OAuth and readers are in the build, and the
// database keeps the sync's tables.
func (h *Manager) larkSyncAvailable() bool {
	_, ok := h.store.(larkSyncStore)
	return ok && h.larkSyncUserAuth() != nil
}

var (
	errLarkSyncUnavailable = i18n.Err("hub-larksync.unavailable")
	errLarkSyncUnread      = i18n.Err("hub-larksync.settings_unavailable")
	errLarkSyncNoBot       = i18n.Err("hub-larksync.no_bot")
	errLarkSyncNoBinding   = i18n.Err("hub-larksync.no_binding")
	errLarkSyncNoApp       = i18n.Err("hub-larksync.no_app")
	errLarkSyncRunning     = errors.New("lark sync: the binding is running")
)

// larkSyncSeal seals plain with the manager's secret.key: every value the sync seals goes through here, kept in a
// field db.sealedSettings lists.
func (h *Manager) larkSyncSeal(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	return h.sealKey(plain)
}

// larkSyncOpen opens a sealed value ("" stays "").
func (h *Manager) larkSyncOpen(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	return h.openKey(sealed)
}

// larkSyncLoad reads the four settings (s.mu held).
func (h *Manager) larkSyncLoadLocked(ctx context.Context) (larkSyncState, error) {
	var st larkSyncState
	if h.store == nil {
		return st, nil
	}
	rctx, cancel := context.WithTimeout(ctx, botSettingsReadTimeout)
	defer cancel()
	for _, r := range []struct {
		key string
		out any
	}{{larkSyncPrefsKey, &st.prefs}, {larkSyncBotsKey, &st.bots}, {larkSyncBindingsKey, &st.bindings}, {larkSyncAccountsKey, &st.accounts}} {
		if _, err := h.store.Setting(rctx, r.key, r.out); err != nil {
			h.logf("lark sync: read %s: %v", r.key, err)
			return larkSyncState{}, errLarkSyncUnread
		}
	}
	s := &h.bots.sync
	s.last, s.haveLast = st.clone(), true
	return st, nil
}

// larkSyncLoad is the sync's settings now.
func (h *Manager) larkSyncLoad(ctx context.Context) (larkSyncState, error) {
	s := &h.bots.sync
	s.mu.Lock()
	defer s.mu.Unlock()
	return h.larkSyncLoadLocked(ctx)
}

// larkSyncChange reads the settings, lets fn change them and writes back the keys it changed. fn returning an error
// writes nothing. It returns the state as written.
func (h *Manager) larkSyncChange(ctx context.Context, fn func(st *larkSyncState) error) (larkSyncState, error) {
	s := &h.bots.sync
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := h.larkSyncLoadLocked(ctx)
	if err != nil {
		return st, err
	}
	before := st.clone()
	if err := fn(&st); err != nil {
		return before, err
	}
	if h.store == nil {
		s.last, s.haveLast = st.clone(), true
		return st, nil
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	for _, w := range []struct {
		key       string
		old, next any
	}{{larkSyncPrefsKey, before.prefs, st.prefs}, {larkSyncBotsKey, before.bots, st.bots},
		{larkSyncBindingsKey, before.bindings, st.bindings}, {larkSyncAccountsKey, before.accounts, st.accounts}} {
		a, _ := json.Marshal(w.old)
		b, _ := json.Marshal(w.next)
		if bytes.Equal(a, b) {
			continue
		}
		if err := h.store.PutSetting(wctx, w.key, w.next); err != nil {
			h.logf("lark sync: save %s: %v", w.key, err)
			return before, errBotSettingsSave
		}
	}
	s.last, s.haveLast = st.clone(), true
	return st, nil
}

// clone is a deep copy of st (the slices the callers change in place).
func (st larkSyncState) clone() larkSyncState {
	out := st
	out.bots = slices.Clone(st.bots)
	for i := range out.bots {
		if r := out.bots[i].Registration; r != nil {
			c := *r
			out.bots[i].Registration = &c
		}
	}
	out.bindings = slices.Clone(st.bindings)
	for i := range out.bindings {
		out.bindings[i].GrantedScopes = slices.Clone(st.bindings[i].GrantedScopes)
		out.bindings[i].RemindedDays = slices.Clone(st.bindings[i].RemindedDays)
	}
	out.accounts = slices.Clone(st.accounts)
	if r := st.prefs.PrimaryRegistration; r != nil {
		c := *r
		out.prefs.PrimaryRegistration = &c
	}
	if h := st.prefs.ScheduleHour; h != nil {
		v := *h
		out.prefs.ScheduleHour = &v
	}
	if p := st.prefs.IMPoll; p != nil {
		v := *p
		out.prefs.IMPoll = &v
	}
	return out
}

// larkSyncApp is bot's app as the module needs it: the primary's from bots.feishu (its hub.json fallback included).
func (h *Manager) larkSyncApp(ctx context.Context, st *larkSyncState, botID string) (larksyncwire.App, error) {
	if botID == larksyncwire.PrimaryBot {
		id, secret, domain := h.botAppCredentials(ctx)
		if id == "" || secret == "" {
			return larksyncwire.App{}, errLarkSyncNoApp
		}
		return larksyncwire.App{AppID: id, AppSecret: secret, Domain: domain}, nil
	}
	b := st.bot(botID)
	if b == nil {
		return larksyncwire.App{}, errLarkSyncNoBot
	}
	if b.AppID == "" || b.SecretEnc == "" {
		return larksyncwire.App{}, errLarkSyncNoApp
	}
	secret, err := h.larkSyncOpen(b.SecretEnc)
	if err != nil {
		return larksyncwire.App{}, err
	}
	return larksyncwire.App{AppID: b.AppID, AppSecret: secret, Domain: b.Domain}, nil
}

// larkSyncBrand is the cloud of a domain: "lark" for Lark's, "feishu" for Feishu's and private deployments.
func larkSyncBrand(domain string) string {
	if botcard.ResolveDomain(domain) == botcard.DomainLark {
		return "lark"
	}
	return "feishu"
}

// larkSyncAccountID is an account's id: its cloud, tenant and open id.
func larkSyncAccountID(brand, tenant, openID string) string {
	return brand + ":" + tenant + ":" + openID
}

// larkSyncBindingKey is the stable key of the (bot, account) binding: the account's id is hashed so the key fits a
// path segment and an attachment ref ("|"-separated) whatever the account's id holds.
func larkSyncBindingKey(botID, accountID string) string {
	sum := sha256.Sum256([]byte(accountID))
	return botID + "~" + hex.EncodeToString(sum[:6])
}

// larkSyncTierOf is the tier a bot reads with.
func (st *larkSyncState) tierOf(botID string) string {
	if botID == larksyncwire.PrimaryBot {
		return st.primaryTier()
	}
	if b := st.bot(botID); b != nil && larksyncwire.ValidTier(b.Tier) {
		return b.Tier
	}
	return larksyncwire.TierDocs
}

// larkSyncPrimaryTier is the tier the primary app's first registration asks the user scopes of (bindFeishu's
// register step); TierDocs when the sync settings cannot be read or were never set.
func (h *Manager) larkSyncPrimaryTier(ctx context.Context) string {
	st, err := h.larkSyncLoad(ctx)
	if err != nil {
		return larksyncwire.TierDocs
	}
	return st.primaryTier()
}
