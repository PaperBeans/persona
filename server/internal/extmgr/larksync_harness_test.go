package extmgr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/botcard"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// The Lark user sync's test world (larksync*_test.go): a manager whose settings and sync tables are in memory, the
// primary chat bot's app from hub.json (the oldest configuration there is), and fakes for what the lark module
// provides through botport: the app registration, the user OAuth and the readers. None of them reaches a network, so
// these tests run in a build without the lark module as well.

// larkSyncMemStore is botStore with the sync's tables: cursors, the dedup index and the outbox. commitErr, when set,
// fails every commit before anything is written (the transaction rolled back).
type larkSyncMemStore struct {
	*botStore
	now func() time.Time

	lmu       sync.Mutex
	cursors   map[string]map[string]string
	index     map[string]larksyncwire.IndexRecord
	outbox    []db.LarkSyncOutbox
	seq       int64
	commits   int
	commitErr error
}

func newLarkSyncMemStore(now func() time.Time) *larkSyncMemStore {
	return &larkSyncMemStore{botStore: newBotStore(), now: now, cursors: map[string]map[string]string{},
		index: map[string]larksyncwire.IndexRecord{}}
}

var _ larkSyncStore = (*larkSyncMemStore)(nil)

func (s *larkSyncMemStore) LarkSyncCursors(_ context.Context, binding string) (map[string]string, error) {
	s.lmu.Lock()
	defer s.lmu.Unlock()
	out := map[string]string{}
	for k, v := range s.cursors[binding] {
		out[k] = v
	}
	return out, nil
}

func (s *larkSyncMemStore) DeleteLarkSyncCursors(_ context.Context, binding string) error {
	s.lmu.Lock()
	defer s.lmu.Unlock()
	delete(s.cursors, binding)
	return nil
}

func (s *larkSyncMemStore) LarkSyncIndex(_ context.Context, keys []string) (map[string]larksyncwire.IndexRecord, error) {
	s.lmu.Lock()
	defer s.lmu.Unlock()
	out := map[string]larksyncwire.IndexRecord{}
	for _, k := range keys {
		if r, ok := s.index[k]; ok {
			out[k] = r
		}
	}
	return out, nil
}

func (s *larkSyncMemStore) CommitLarkSyncRun(_ context.Context, binding string, cursors map[string]string, recs []larksyncwire.IndexRecord, outbox []db.LarkSyncOutbox) error {
	s.lmu.Lock()
	defer s.lmu.Unlock()
	if s.commitErr != nil {
		return s.commitErr
	}
	s.commits++
	if len(cursors) > 0 && s.cursors[binding] == nil {
		s.cursors[binding] = map[string]string{}
	}
	for k, v := range cursors {
		s.cursors[binding][k] = v
	}
	for _, r := range recs {
		have, ok := s.index[r.Key]
		if ok {
			if r.Revision < have.Revision {
				r.Revision, r.BodyHash = have.Revision, have.BodyHash
			}
			r.Sources = larksyncwire.MergeSources(have.Sources, r.Sources)
		}
		s.index[r.Key] = r
	}
	for _, o := range outbox {
		s.seq++
		o.Seq, o.CreatedAt = s.seq, s.now()
		s.outbox = append(s.outbox, o)
	}
	return nil
}

func (s *larkSyncMemStore) LarkSyncOutboxAfter(_ context.Context, kind string, after int64, limit int) ([]db.LarkSyncOutbox, error) {
	s.lmu.Lock()
	defer s.lmu.Unlock()
	var out []db.LarkSyncOutbox
	for _, o := range s.outbox {
		if o.Kind == kind && o.Seq > after && len(out) < limit {
			out = append(out, o)
		}
	}
	return out, nil
}

func (s *larkSyncMemStore) LarkSyncDocsSince(_ context.Context, since time.Time) ([]db.LarkSyncOutbox, error) {
	s.lmu.Lock()
	defer s.lmu.Unlock()
	latest := map[string]db.LarkSyncOutbox{}
	for _, o := range s.outbox {
		if o.Kind == db.LarkSyncDoc && !o.CreatedAt.Before(since) {
			latest[o.CanonicalKey] = o
		}
	}
	var out []db.LarkSyncOutbox
	for _, o := range latest {
		out = append(out, o)
	}
	slices.SortFunc(out, func(a, b db.LarkSyncOutbox) int { return int(a.Seq - b.Seq) })
	return out, nil
}

func (s *larkSyncMemStore) PruneLarkSyncOutbox(_ context.Context, before time.Time) (int64, error) {
	s.lmu.Lock()
	defer s.lmu.Unlock()
	n := len(s.outbox)
	s.outbox = slices.DeleteFunc(s.outbox, func(o db.LarkSyncOutbox) bool { return o.CreatedAt.Before(before) })
	return int64(n - len(s.outbox)), nil
}

// outboxOf are the outbox rows whose payload mentions needle.
func (s *larkSyncMemStore) outboxOf(needle string) []db.LarkSyncOutbox {
	s.lmu.Lock()
	defer s.lmu.Unlock()
	var out []db.LarkSyncOutbox
	for _, o := range s.outbox {
		if strings.Contains(string(o.Payload), needle) {
			out = append(out, o)
		}
	}
	return out
}

func (s *larkSyncMemStore) cursorsOf(binding string) map[string]string {
	out, _ := s.LarkSyncCursors(context.Background(), binding)
	return out
}

// raw is a setting as stored (what a database dump would show).
func (s *larkSyncMemStore) raw(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.vals[key])
}

// fakeRegistrar is the accounts site's app registration: it records every begin (with its options) and answers each
// poll with the next of polls (the last one repeats).
type fakeRegistrar struct {
	mu     sync.Mutex
	begins []larksyncwire.RegisterOptions
	polls  []botcard.RegisterTicket
	asked  []string
	fail   error
}

func (r *fakeRegistrar) Begin(ctx context.Context, region, name string) (botcard.RegisterChallenge, error) {
	return r.BeginWith(ctx, region, name, larksyncwire.RegisterOptions{})
}

func (r *fakeRegistrar) BeginWith(_ context.Context, region, _ string, opts larksyncwire.RegisterOptions) (botcard.RegisterChallenge, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return botcard.RegisterChallenge{}, r.fail
	}
	r.begins = append(r.begins, opts)
	r.asked = append(r.asked, "begin "+region)
	return botcard.RegisterChallenge{DeviceCode: "dc_sync_secret", URL: "https://accounts.example.com/register?user_code=UC",
		ExpiresIn: 600, Interval: 5}, nil
}

func (r *fakeRegistrar) Poll(_ context.Context, region, deviceCode string) (botcard.RegisterTicket, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.asked = append(r.asked, "poll "+region+" "+deviceCode)
	if len(r.polls) == 0 {
		return botcard.RegisterTicket{Status: botcard.RegisterPending}, nil
	}
	t := r.polls[0]
	if len(r.polls) > 1 {
		r.polls = r.polls[1:]
	}
	return t, nil
}

func (r *fakeRegistrar) lastBegin(t *testing.T) larksyncwire.RegisterOptions {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.begins) == 0 {
		t.Fatal("no registration began")
	}
	return r.begins[len(r.begins)-1]
}

// fakeUserAuth is the user OAuth. refresh answers a refresh token (nil: a rotated pair "<old>+" valid for two hours);
// refreshes counts the Refresh calls.
type fakeUserAuth struct {
	mu         sync.Mutex
	now        func() time.Time
	refreshes  atomic.Int32
	refresh    func(app larksyncwire.App, refreshToken string) (larksyncwire.Token, error)
	configure  larksyncwire.RedirectReport
	configured []string // "<app id> <redirect>"
	deviceErr  error
	devicePoll []larksyncwire.DevicePoll
	exchange   map[string]larksyncwire.Token // code → token
	exchanged  []string                      // "<code> <redirect> <verifier>"
	authorized []larksyncwire.AuthRequest
}

func (f *fakeUserAuth) AuthorizeURL(r larksyncwire.AuthRequest) string {
	f.mu.Lock()
	f.authorized = append(f.authorized, r)
	f.mu.Unlock()
	return "https://accounts.example.com/authorize?client_id=" + r.App.AppID + "&state=" + r.State
}

func (f *fakeUserAuth) Exchange(_ context.Context, _ larksyncwire.App, code, redirectURI, verifier string) (larksyncwire.Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exchanged = append(f.exchanged, code+" "+redirectURI+" "+verifier)
	tok, ok := f.exchange[code]
	if !ok {
		return larksyncwire.Token{}, &larksyncwire.Error{Kind: larksyncwire.ErrKindInvalidGrant, Op: "exchange", Msg: "bad code"}
	}
	return tok, nil
}

func (f *fakeUserAuth) Refresh(_ context.Context, app larksyncwire.App, refreshToken string) (larksyncwire.Token, error) {
	f.refreshes.Add(1)
	f.mu.Lock()
	fn := f.refresh
	f.mu.Unlock()
	if fn != nil {
		return fn(app, refreshToken)
	}
	return larksyncwire.Token{AccessToken: "acc-" + refreshToken + "+", RefreshToken: refreshToken + "+",
		ExpiresAt: f.now().Add(2 * time.Hour)}, nil
}

func (f *fakeUserAuth) DeviceBegin(context.Context, larksyncwire.App, []string) (larksyncwire.DeviceChallenge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deviceErr != nil {
		return larksyncwire.DeviceChallenge{}, f.deviceErr
	}
	return larksyncwire.DeviceChallenge{DeviceCode: "device_secret", URL: "https://accounts.example.com/device?code=UC",
		UserCode: "UC-1", ExpiresIn: 300, Interval: 5}, nil
}

func (f *fakeUserAuth) DevicePoll(_ context.Context, _ larksyncwire.App, deviceCode string) (larksyncwire.DevicePoll, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if deviceCode != "device_secret" {
		return larksyncwire.DevicePoll{Status: larksyncwire.DeviceFailed, ErrorText: "unknown device code"}, nil
	}
	if len(f.devicePoll) == 0 {
		return larksyncwire.DevicePoll{Status: larksyncwire.DevicePending}, nil
	}
	p := f.devicePoll[0]
	f.devicePoll = f.devicePoll[1:]
	return p, nil
}

func (f *fakeUserAuth) ConfigureRedirect(_ context.Context, app larksyncwire.App, redirectURI string, _ []string) larksyncwire.RedirectReport {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.configured = append(f.configured, app.AppID+" "+redirectURI)
	return f.configure
}

// fakeSyncer is one binding's reader: it does what the world's script says for the binding it reads.
type fakeSyncer struct{ w *larkWorld }

func (s *fakeSyncer) Run(ctx context.Context, b larksyncwire.Binding, token func(context.Context) (string, error), cursors map[string]string, idx larksyncwire.Index) (larksyncwire.Result, error) {
	s.w.mu.Lock()
	fn := s.w.runs[b.BotID+"/"+b.AccountID]
	s.w.ran = append(s.w.ran, b.Key)
	s.w.mu.Unlock()
	if fn == nil {
		if _, err := token(ctx); err != nil {
			return larksyncwire.Result{}, err
		}
		return larksyncwire.Result{}, nil
	}
	return fn(ctx, b, token, cursors, idx)
}

func (s *fakeSyncer) Media(ctx context.Context, b larksyncwire.Binding, token func(context.Context) (string, error), ref string, _ int64) ([]byte, string, error) {
	tok, err := token(ctx)
	if err != nil {
		return nil, "", err
	}
	return []byte(b.Key + " " + tok + " " + ref), "image/png", nil
}

func (s *fakeSyncer) ResolveUserNames(ctx context.Context, b larksyncwire.Binding, token func(context.Context) (string, error), openIDs []string) (map[string]string, error) {
	s.w.mu.Lock()
	fn := s.w.resolveNames
	s.w.mu.Unlock()
	if fn != nil {
		return fn(ctx, b, token, openIDs)
	}
	return nil, nil
}

func (s *fakeSyncer) ResolveNamesWithFallback(ctx context.Context, b larksyncwire.Binding, token func(context.Context) (string, error), req larksyncwire.NameResolveRequest) (larksyncwire.NameResolveResult, error) {
	s.w.mu.Lock()
	fn := s.w.resolveNamesWithFallback
	legacy := s.w.resolveNames
	s.w.mu.Unlock()
	if fn != nil {
		return fn(ctx, b, token, req)
	}
	if legacy != nil {
		rawIDs := make([]string, len(req.OpenIDs))
		for i, id := range req.OpenIDs {
			rawIDs[i] = string(id)
		}
		names, err := legacy(ctx, b, token, rawIDs)
		return larksyncwire.NameResolveResult{UserNames: names}, err
	}
	return larksyncwire.NameResolveResult{}, nil
}

func (s *fakeSyncer) ExtractDocLinks(text string) []larksyncwire.PendingLinkCandidate {
	s.w.mu.Lock()
	fn := s.w.extractDocLinks
	s.w.mu.Unlock()
	if fn != nil {
		return fn(text)
	}
	return nil
}

func (s *fakeSyncer) MergePendingLinks(existingCursor string, add []larksyncwire.PendingLinkCandidate) (string, int) {
	s.w.mu.Lock()
	fn := s.w.mergePendingLinks
	s.w.mu.Unlock()
	if fn != nil {
		return fn(existingCursor, add)
	}
	return existingCursor, 0
}

type larkRun func(ctx context.Context, b larksyncwire.Binding, token func(context.Context) (string, error), cursors map[string]string, idx larksyncwire.Index) (larksyncwire.Result, error)

// larkWorld is a manager with the sync: the primary chat bot's app from hub.json, the fakes, a clock it moves.
type larkWorld struct {
	h     *Manager
	store *larkSyncMemStore
	ua    *fakeUserAuth
	reg   *fakeRegistrar
	mux   http.Handler

	mu                       sync.Mutex
	clock                    time.Time
	runs                     map[string]larkRun // "<bot>/<account id>" → what its reader does
	resolveNames             func(ctx context.Context, b larksyncwire.Binding, token func(context.Context) (string, error), openIDs []string) (map[string]string, error)
	resolveNamesWithFallback func(ctx context.Context, b larksyncwire.Binding, token func(context.Context) (string, error), req larksyncwire.NameResolveRequest) (larksyncwire.NameResolveResult, error)
	extractDocLinks          func(text string) []larksyncwire.PendingLinkCandidate
	mergePendingLinks        func(existingCursor string, add []larksyncwire.PendingLinkCandidate) (string, int)
	ran                      []string
	readers                  int
}

func newLarkWorld(t *testing.T) *larkWorld {
	t.Helper()
	w := &larkWorld{clock: time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC), runs: map[string]larkRun{}}
	h := newTestManager(t)
	h.cfg.Bots.Feishu = FeishuBotConfig{AppID: "cli_primary", AppSecret: "primary-secret", Domain: "feishu"}
	w.store = newLarkSyncMemStore(w.now)
	h.store = w.store
	w.ua = &fakeUserAuth{now: w.now, exchange: map[string]larksyncwire.Token{}}
	w.reg = &fakeRegistrar{}
	h.bots.registrar = w.reg
	h.bots.regDomain = func(region string) string { return region }
	h.bots.sync.userAuth = w.ua
	h.bots.sync.now = w.now
	h.bots.sync.newSyncer = func() larksyncwire.Syncer {
		w.mu.Lock()
		w.readers++
		w.mu.Unlock()
		return &fakeSyncer{w}
	}
	mux := http.NewServeMux()
	h.botRoutes(mux)
	w.h, w.mux = h, mux
	return w
}

func (w *larkWorld) now() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.clock
}

func (w *larkWorld) advance(d time.Duration) {
	w.mu.Lock()
	w.clock = w.clock.Add(d)
	w.mu.Unlock()
}

func (w *larkWorld) script(botID, accountID string, fn larkRun) {
	w.mu.Lock()
	w.runs[botID+"/"+accountID] = fn
	w.mu.Unlock()
}

// do is a request as a full-access device (as the tests' handlers are called directly).
func (w *larkWorld) do(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	return w.doWith(t, principal{Role: roleFull}, method, path, body)
}

func (w *larkWorld) doWith(t *testing.T, p principal, method, path, body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(withPrincipal(req.Context(), p))
	w.mux.ServeHTTP(rec, req)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// addSyncBot keeps a registered sync-only bot (its secret sealed).
func (w *larkWorld) addSyncBot(t *testing.T, botID, appID, tier string) {
	t.Helper()
	sealed, err := w.h.larkSyncSeal(appID + "-secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.h.larkSyncChange(context.Background(), func(st *larkSyncState) error {
		st.bots = append(st.bots, larkSyncBot{BotID: botID, Label: "Sync " + botID, AppID: appID, SecretEnc: sealed, Domain: "feishu",
			Role: larksyncwire.RoleSyncOnly, Tier: tier, Approval: larksyncwire.ApprovalReady, CreatedAt: w.now()})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// adopt binds openID's account (tenant "t1") with botID, as a finished authorization does, with a token pair that
// ends in expires; it gives the binding's key and the account's id.
func (w *larkWorld) adopt(t *testing.T, botID, openID, access, refresh string, expires time.Duration) (key, accountID string) {
	t.Helper()
	ctx := context.Background()
	st, err := w.h.larkSyncLoad(ctx)
	if err != nil {
		t.Fatal(err)
	}
	app, err := w.h.larkSyncApp(ctx, &st, botID)
	if err != nil {
		t.Fatal(err)
	}
	tok := larksyncwire.Token{AccessToken: access, RefreshToken: refresh, ExpiresAt: w.now().Add(expires), OpenID: openID,
		TenantKey: "t1", Name: "Account " + openID, Scopes: larksyncwire.ScopesFor(larksyncwire.TierDocs)}
	if _, err := w.h.larkSyncAdopt(ctx, botID, larkSyncPendingKey(botID), "", app, tok); err != nil {
		t.Fatal(err)
	}
	accountID = larkSyncAccountID(larkSyncBrand(app.Domain), "t1", openID)
	return larkSyncBindingKey(botID, accountID), accountID
}

// binding is key's binding as stored.
func (w *larkWorld) binding(t *testing.T, key string) larkSyncBinding {
	t.Helper()
	st, err := w.h.larkSyncLoad(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b := st.binding(key)
	if b == nil {
		t.Fatalf("no binding %s", key)
	}
	return *b
}

// pushes are the pushes the manager asked the hub for.
func (w *larkWorld) pushes() []map[string]any {
	th := hubOf(w.h)
	th.mu.Lock()
	defer th.mu.Unlock()
	return slices.Clone(th.pushes)
}

// noSecretIn fails when any of secrets is in s.
func noSecretIn(t *testing.T, what, s string, secrets ...string) {
	t.Helper()
	for _, x := range secrets {
		if x != "" && strings.Contains(s, x) {
			t.Fatalf("%s holds the secret %q: %s", what, x, s)
		}
	}
}
