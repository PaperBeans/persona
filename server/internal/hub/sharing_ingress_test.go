package hub

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/shareproof"
)

func ingressRequest(ip string, now time.Time, signed bool) *http.Request {
	r := httptest.NewRequest("GET", publicSharePrefix+strings.Repeat("A", 64), nil)
	r.Host = "share.example.test:18081"
	r.RemoteAddr = "127.0.0.1:12345"
	if signed {
		r.Header.Set(shareproof.Header, shareproof.Sign(shareproof.DecodeKey(strings.Repeat("12", 32)), r, netip.MustParseAddr(ip), now))
	}
	return r
}

func TestShareIngressProofPreservesIndependentIPBudgets(t *testing.T) {
	now := time.Unix(2000000000, 0)
	for _, authenticated := range []bool{false, true} {
		h := &Hub{cfg: &Config{Sharing: &SharingConfig{IngressKey: strings.Repeat("12", 32)}}}
		limiter := &shareIngress{}
		for n := range 20 {
			r := ingressRequest("192.0.2.1", now, authenticated)
			peer, ok := h.sharingPeer(r, now)
			if ok != authenticated {
				t.Fatal("unexpected proof status")
			}
			release, ok := limiter.enter(shareSourceKey(peer), "synthetic-first-"+strings.Repeat("x", n), now)
			if !ok {
				t.Fatal("fixture budget unexpectedly exhausted")
			}
			release()
		}
		r := ingressRequest("198.51.100.1", now, authenticated)
		peer, _ := h.sharingPeer(r, now)
		release, ok := limiter.enter(shareSourceKey(peer), "synthetic-second", now)
		if ok != authenticated {
			t.Error("independent signed reader budget was not preserved")
		}
		if ok {
			release()
		}
	}
}

func TestShareIngressProofIsNotGeneralForwardedAddressTrust(t *testing.T) {
	now := time.Unix(2000000000, 0)
	h := &Hub{cfg: &Config{Sharing: &SharingConfig{IngressKey: strings.Repeat("12", 32)}}}
	r := ingressRequest("192.0.2.1", now, false)
	r.Header.Set("X-Forwarded-For", "192.0.2.1")
	if peer, ok := h.sharingPeer(r, now); ok || peer != r.RemoteAddr {
		t.Error("forwarded header trusted")
	}
	r = ingressRequest("192.0.2.1", now, true)
	r.RemoteAddr = "198.51.100.2:12345"
	if peer, ok := h.sharingPeer(r, now); ok || peer != r.RemoteAddr {
		t.Error("external proof trusted")
	}
	r = ingressRequest("192.0.2.1", now, true)
	r.URL.Path = "/api/v1/admin/state"
	if _, ok := h.sharingPeer(r, now); ok {
		t.Error("proof granted general route trust")
	}
	for _, ip := range []string{"2001:db8::1", "2001:db8::2"} {
		r = ingressRequest(ip, now, true)
		peer, ok := h.sharingPeer(r, now)
		if !ok || shareSourceKey(peer) != "2001:db8::/64" {
			t.Error("IPv6 grouping changed")
		}
	}
}

func TestShareIngressAcknowledgementDoesNotBypassCapabilities(t *testing.T) {
	h := &Hub{cfg: &Config{PublicURL: "http://app.example.test:18080", Sharing: &SharingConfig{PublicOrigin: "http://share.example.test:18081", IngressKey: strings.Repeat("12", 32)}}}
	r := ingressRequest("192.0.2.1", time.Now(), true)
	w := httptest.NewRecorder()
	h.forwardSharePublic(w, r)
	if w.Code != 404 || w.Header().Get(shareproof.Acknowledgement) != shareproof.Accepted {
		t.Errorf("anonymous capability not required: %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "192.0.2.1") || strings.Contains(w.Body.String(), h.cfg.Sharing.IngressKey) {
		t.Error("private ingress data escaped")
	}
}
