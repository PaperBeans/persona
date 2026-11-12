package extmgr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/extension"
)

// slowOfficialMarket is a new hub whose official market answers after delay, as GitHub does on a first read.
func slowOfficialMarket(t *testing.T, delay time.Duration) (h *Manager, phone string) {
	t.Helper()
	h, phone, _ = marketHub(t)
	dir := uiPackage(t)
	pkg := tarDir(t, dir)
	gh := &fakeGitHub{release: "v1", files: map[string][]byte{}, assets: map[string][]byte{"example-ui-1.0.0.tar.gz": pkg}}
	key := newMarketKey(t)
	ix, _ := json.Marshal(map[string]any{"market_version": 1, "name": "Example",
		"extensions": []any{marketEntry(t, dir, "release:v1/example-ui-1.0.0.tar.gz", pkg)}})
	gh.files[extension.IndexFile] = key.sign(ix)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		gh.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	h.extensions.markets.client, h.extensions.markets.githubAPI = srv.Client(), srv.URL
	useOfficialMarket(t, "github:example/plugins", key.text)
	t.Cleanup(func() { waitMarketsRead(t, h) })
	return h, phone
}

// A new hub's first look at the catalog is the catalog (#5626): the request that adds the official market waits for
// its first read instead of answering with the "not verified yet" placeholder, which the apps show as unreachable.
func TestFirstCatalogWaitsForTheOfficialMarketsFirstRead(t *testing.T) {
	h, phone := slowOfficialMarket(t, 300*time.Millisecond)
	code, out := call(t, h.routes(), "GET", "/api/v1/extensions/-/catalog", phone, "")
	if code != 200 {
		t.Fatalf("catalog: %d %v", code, out)
	}
	items, _ := out["extensions"].([]any)
	markets, _ := out["markets"].([]any)
	if len(items) != 1 || len(markets) != 1 {
		t.Fatalf("the first catalog is not the catalog: %v", out)
	}
	if m := markets[0].(map[string]any); m["error"] != nil || m["fetched_at"] == nil {
		t.Fatalf("the official market still shows its placeholder: %v", m)
	}
}

// The markets list waits the same way, and a market read before is answered as it stands, without waiting again.
func TestFirstMarketsListWaitsOnlyForAFirstRead(t *testing.T) {
	h, phone := slowOfficialMarket(t, 300*time.Millisecond)
	if list := marketList(t, h, phone); len(list) != 1 || list[0]["error"] != nil || list[0]["fetched_at"] == nil {
		t.Fatalf("first markets list: %v", list)
	}
	// Stale now: the catalog reads it again in the background and answers with the index it has.
	ms := h.marketsState()
	ms.mu.Lock()
	for _, s := range ms.byID {
		old := time.Now().Add(-2 * marketStale)
		s.rec.FetchedAt = &old
	}
	ms.mu.Unlock()
	start := time.Now()
	if code, out := call(t, h.routes(), "GET", "/api/v1/extensions/-/catalog", phone, ""); code != 200 || len(out["extensions"].([]any)) != 1 {
		t.Fatalf("catalog: %d %v", code, out)
	}
	if took := time.Since(start); took > 250*time.Millisecond {
		t.Fatalf("a market read before waited for its new read: %v", took)
	}
}

// A first read slower than the bound does not hold the request: it answers with what there is.
func TestFirstReadWaitIsBounded(t *testing.T) {
	was := firstReadWait
	firstReadWait = 100 * time.Millisecond
	t.Cleanup(func() { firstReadWait = was })
	h, phone := slowOfficialMarket(t, 2*time.Second)
	start := time.Now()
	if code, out := call(t, h.routes(), "GET", "/api/v1/extensions/-/catalog", phone, ""); code != 200 {
		t.Fatalf("catalog: %d %v", code, out)
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("the request waited %v for a slow first read", took)
	}
}
