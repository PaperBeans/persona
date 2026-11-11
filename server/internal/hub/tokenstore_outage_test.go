package hub

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
)

// downTokenStore is the token store whose database stops answering while down is set.
type downTokenStore struct {
	*tokenStore
	down atomic.Bool
}

var errTokenStoreDown = errors.New("connection refused")

func (s *downTokenStore) DeviceTokenByHash(ctx context.Context, hash string) (*db.DeviceToken, error) {
	if s.down.Load() {
		return nil, errTokenStoreDown
	}
	return s.tokenStore.DeviceTokenByHash(ctx, hash)
}

func (s *downTokenStore) PairTokenByHash(context.Context, string) (*db.PairToken, error) {
	if s.down.Load() {
		return nil, errTokenStoreDown
	}
	return nil, nil
}

// A database outage is not a revoked credential (#3217): the app gets a retryable 503, nothing is counted against its
// address, and the same token works once the database answers again. A token the database does not know is still 401.
func TestTokenStoreOutageIsNotRevocation(t *testing.T) {
	h := newTestHub(t)
	s := &downTokenStore{tokenStore: &tokenStore{rows: map[string]db.DeviceToken{}, rev: map[string]bool{}}}
	h.store = s
	h.cfg.Security = SecurityConfig{AuthFailures: 1, AuthBlockSeconds: 120} // one counted failure blocks the source
	if err := s.CreateDeviceToken(context.Background(), db.DeviceToken{ID: "dev_phone", Role: roleFull}, hashToken("jd_phone")); err != nil {
		t.Fatal(err)
	}
	mux := h.routes()
	request := func(tok string, status int, code string) {
		t.Helper()
		w := securityRequest(mux, "GET", "/api/v1/machines", tok, "", "198.51.100.7:1", true)
		requireCode(t, w, status, code)
		if status == 503 && (w.Header().Get("Retry-After") == "" || strings.Contains(w.Body.String(), "invalid token")) {
			t.Fatalf("503 must carry Retry-After and not say the token is invalid: %v %s", w.Header(), w.Body.String())
		}
	}

	// Nothing cached and the database down: device and pairing tokens alike.
	s.down.Store(true)
	for range 3 {
		request("jd_phone", 503, codeTokenStoreUnavailable)
	}
	request("jp_pairing", 503, codeTokenStoreUnavailable)
	if len(h.devtok.failures.buckets) != 0 {
		t.Fatal("an outage was counted as authentication failures")
	}
	if len(h.devtok.cache) != 0 {
		t.Fatal("an outage was cached as a lookup")
	}

	// Back up: the same credential works.
	s.down.Store(false)
	request("jd_phone", 200, "")

	// Its cache entry expired and the database down again: still no verdict.
	h.devtok.mu.Lock()
	for key, c := range h.devtok.cache {
		c.at = time.Now().Add(-tokenCacheFor - time.Second)
		h.devtok.cache[key] = c
	}
	h.devtok.mu.Unlock()
	s.down.Store(true)
	request("jd_phone", 503, codeTokenStoreUnavailable)

	// A token the database answers for as unknown is refused and counted as before.
	s.down.Store(false)
	request("jd_unknown", 401, "")
	request("jd_phone", 429, "rate_limited")
}
