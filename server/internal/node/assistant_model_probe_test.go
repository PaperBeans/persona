package node

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// The configured-model availability probe talks to the launcher's real gateway. A 429 or 5xx keeps the conversation on
// its fallback; once the gateway answers 2xx the next request may return to the configured model (#7824).
func TestProbeGatewayReflectsRecovery(t *testing.T) {
	var requests atomic.Int32
	var healthy atomic.Bool
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v1/messages" {
			t.Errorf("probe path %q, want /v1/messages", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "probe-key" && r.Header.Get("Authorization") != "Bearer probe-key" {
			t.Error("the probe did not carry the gateway key")
		}
		if !healthy.Load() {
			w.Header().Set("x-should-retry", "false")
			http.Error(w, `{"error":{"code":"quota_exhausted","message":"an account becomes available again at 23:59 UTC (in 1m)"}}`, http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"m","type":"message","role":"assistant","model":"primary-model","content":[],"stop_reason":"end_turn","usage":{}}`))
	}))
	defer gw.Close()

	// The shared status rule the real probe uses.
	if probeStatusOK(http.StatusTooManyRequests) || probeStatusOK(http.StatusBadGateway) {
		t.Fatal("a 429/5xx must not count as available")
	}
	if !probeStatusOK(http.StatusOK) {
		t.Fatal("a 2xx must count as available")
	}

	ctx := context.Background()
	if probeGateway(ctx, gw.URL, "probe-key", "primary-model") {
		t.Fatal("the probe said available while the gateway answers 429")
	}
	if requests.Load() == 0 {
		t.Fatal("the probe never reached the gateway")
	}

	healthy.Store(true)
	if !probeGateway(ctx, gw.URL, "probe-key", "primary-model") {
		t.Fatal("the probe did not see the recovered gateway")
	}

	// A gateway without a key, or a bad address, never guesses.
	if probeGateway(ctx, gw.URL, "", "primary-model") {
		t.Fatal("a probe without a key must fail closed")
	}
	if probeGateway(ctx, "://not a url", "probe-key", "primary-model") {
		t.Fatal("a bad gateway URL must fail closed")
	}
}
