package larksync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// testNow is the fake clock's start.
var testNow = time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)

// testOwner is the owner's open id; testAccount carries it as its last part.
const (
	testOwner   = "ou_owner"
	testAccount = "feishu:tenant_x:" + testOwner
)

// fakeClock is the syncer's clock: it moves only when a test, the fake sleeper or the fake platform moves it.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// fakeOpen is the open platform: one handler per path, set by the test. A path without one fails the test. GET / and
// any request with User-Agent persona-ports are a local node's port probe (node/ports.go): 404, not counted.
type fakeOpen struct {
	t   *testing.T
	srv *httptest.Server
	clk *fakeClock
	// step moves the clock on every call (the budget tests).
	step time.Duration

	mu       sync.Mutex
	handlers map[string]http.HandlerFunc
	calls    []string
	auths    []string
}

func newFakeOpen(t *testing.T, clk *fakeClock) *fakeOpen {
	t.Helper()
	f := &fakeOpen{t: t, clk: clk, handlers: map[string]http.HandlerFunc{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.Header.Get("User-Agent") == "persona-ports" {
			http.NotFound(w, r)
			return
		}
		f.mu.Lock()
		custom := f.handlers[r.URL.Path] != nil
		defaultContact := !custom && strings.HasPrefix(r.URL.Path, "/open-apis/contact/v3/users/")
		defaultMembers := !custom && strings.HasPrefix(r.URL.Path, "/open-apis/im/v1/chats/") && strings.HasSuffix(r.URL.Path, "/members")
		if defaultContact || defaultMembers {
			f.calls = append(f.calls, r.Method+" "+r.URL.Path)
			f.auths = append(f.auths, r.Header.Get("Authorization"))
		}
		f.mu.Unlock()
		if name, ok := strings.CutPrefix(r.URL.Path, "/open-apis/contact/v3/users/"); ok && !custom {
			// Senders' names (run.senderName): every user is "Name <open id>" unless a test answers otherwise.
			writeData(w, map[string]any{"user": map[string]any{"name": "Name " + name}})
			return
		}
		if defaultMembers {
			writeData(w, map[string]any{"items": []any{}})
			return
		}
		f.mu.Lock()
		h := f.handlers[r.URL.Path]
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		f.auths = append(f.auths, r.Header.Get("Authorization"))
		step := f.step
		f.mu.Unlock()
		if step > 0 {
			clk.advance(step)
		}
		if h == nil {
			t.Errorf("unexpected call %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeOpen) handle(path string, h http.HandlerFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[path] = h
}

// data answers path with the envelope {code: 0, data}.
func (f *fakeOpen) data(path string, data any) {
	f.handle(path, func(w http.ResponseWriter, _ *http.Request) { writeData(w, data) })
}

func (f *fakeOpen) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasSuffix(c, " "+path) {
			n++
		}
	}
	return n
}

func (f *fakeOpen) authHeaders() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.auths...)
}

func writeData(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "success", "data": data})
}

func writeCode(w http.ResponseWriter, status, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "msg": msg})
}

// testSyncer is a Syncer on the fake clock whose waits move the clock instead of sleeping (recorded in sleeps), with
// no jitter and rate limits high enough that the limiter never waits.
type testSyncer struct {
	*Syncer
	clk *fakeClock

	mu     sync.Mutex
	sleeps []time.Duration
}

func newTestSyncer(t *testing.T, clk *fakeClock) *testSyncer {
	t.Helper()
	ts := &testSyncer{clk: clk}
	ts.Syncer = NewSyncer(nil, clk.now)
	ts.sleep = func(ctx context.Context, d time.Duration) error {
		ts.mu.Lock()
		ts.sleeps = append(ts.sleeps, d)
		ts.mu.Unlock()
		clk.advance(d)
		return ctx.Err()
	}
	ts.jitter = func(time.Duration) time.Duration { return 0 }
	rates := map[string]float64{}
	for fam := range familyRates {
		rates[fam] = 1e6
	}
	ts.lim.rates = rates
	return ts
}

func (ts *testSyncer) slept() []time.Duration {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return append([]time.Duration(nil), ts.sleeps...)
}

func binding(f *fakeOpen, key, tier string) larksyncwire.Binding {
	return larksyncwire.Binding{
		Key:       key,
		BotID:     "primary",
		AccountID: testAccount,
		Label:     "Work",
		Tier:      tier,
		App:       larksyncwire.App{AppID: "cli_test", AppSecret: "app-secret", Domain: f.srv.URL},
		Backfill:  48 * time.Hour,
	}
}

// staticToken is a token func that always gives tok.
func staticToken(tok string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return tok, nil }
}

// memIndex is the dedup index in memory. It writes with Put (it cannot stage).
type memIndex struct {
	mu      sync.Mutex
	recs    map[string]larksyncwire.IndexRecord
	lookups int
	puts    int
}

func newMemIndex() *memIndex { return &memIndex{recs: map[string]larksyncwire.IndexRecord{}} }

func (m *memIndex) Lookup(_ context.Context, keys []string) (map[string]larksyncwire.IndexRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lookups++
	out := map[string]larksyncwire.IndexRecord{}
	for _, k := range keys {
		if r, ok := m.recs[k]; ok {
			out[k] = r
		}
	}
	return out, nil
}

func (m *memIndex) Put(_ context.Context, recs []larksyncwire.IndexRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.puts++
	for _, r := range recs {
		m.recs[r.Key] = r
	}
	return nil
}

func (m *memIndex) get(key string) (larksyncwire.IndexRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.recs[key]
	return r, ok
}

// stagingIndex is the manager's kind of index: it stages what a run hands over, and Put must not be called.
type stagingIndex struct {
	*memIndex
	t      *testing.T
	staged []larksyncwire.IndexRecord
}

func (s *stagingIndex) Put(context.Context, []larksyncwire.IndexRecord) error {
	s.t.Error("Put called on an index that stages")
	return nil
}

func (s *stagingIndex) Stage(recs []larksyncwire.IndexRecord) { s.staged = append(s.staged, recs...) }

// entries is the sorted Entry of each source.
func entries(refs []larksyncwire.SourceRef) []string {
	var out []string
	for _, r := range refs {
		out = append(out, r.Binding+"/"+r.Entry)
	}
	sort.Strings(out)
	return out
}

// msgTime is a message create_time (ms) for t.
func msgTime(t time.Time) string { return itoa(t.UnixMilli()) }

func secs(t time.Time) string { return itoa(t.Unix()) }

func itoa(v int64) string { return unixCursor(v) }

// textMsg is an im/v1 message item.
func textMsg(id, chat, sender, text string, at time.Time) map[string]any {
	content, _ := json.Marshal(map[string]string{"text": text})
	return map[string]any{
		"message_id":  id,
		"chat_id":     chat,
		"msg_type":    "text",
		"create_time": msgTime(at),
		"sender":      map[string]string{"id": sender, "sender_type": "user"},
		"body":        map[string]string{"content": string(content)},
	}
}

// emptyDocs answers the documents tier's listings with nothing (drive and wiki).
func emptyDocs(f *fakeOpen) {
	f.data("/open-apis/drive/v1/files", map[string]any{"files": []any{}})
	f.data("/open-apis/wiki/v2/spaces", map[string]any{"items": []any{}})
}

// emptySearch answers message search with nothing.
func emptySearch(f *fakeOpen) {
	f.data("/open-apis/search/v2/message", map[string]any{"items": []any{}})
}
