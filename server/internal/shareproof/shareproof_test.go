package shareproof

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func signed(t *testing.T) ([]byte, *http.Request, time.Time) {
	t.Helper()
	key := DecodeKey(strings.Repeat("12", 32))
	now := time.Unix(2000000000, 0)
	r := httptest.NewRequest("GET", APIPrefix+strings.Repeat("A", 64)+"?limit=100&cursor=opaque", nil)
	r.Host = "share.example.test:18081"
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set(Header, Sign(key, r, netip.MustParseAddr("192.0.2.3"), now))
	return key, r, now
}

func TestExactLoopbackProofAndTampering(t *testing.T) {
	key, r, now := signed(t)
	ip, ok := Verify(key, r, now)
	if !ok || ip.String() != "192.0.2.3" {
		t.Fatal("valid proof rejected")
	}
	for _, change := range []func(*http.Request){
		func(r *http.Request) { r.Method = "POST" },
		func(r *http.Request) { r.Host = "app.example.test:18080" },
		func(r *http.Request) { r.URL.Path = APIPrefix + strings.Repeat("B", 64) },
		func(r *http.Request) { r.URL.RawQuery = "limit=1" },
		func(r *http.Request) { r.URL.RawPath = "/encoded" },
		func(r *http.Request) { r.RemoteAddr = "198.51.100.8:12345" },
		func(r *http.Request) {
			r.Header.Set(Header, strings.Replace(r.Header.Get(Header), "192.0.2.3", "192.0.2.4", 1))
		},
		func(r *http.Request) { r.Header.Add(Header, r.Header.Get(Header)) },
		func(r *http.Request) { r.Header.Set(Header, strings.Repeat("x", 161)) },
		func(r *http.Request) { r.Header.Set(Header, "invalid") },
	} {
		copy := r.Clone(r.Context())
		copy.Header = r.Header.Clone()
		change(copy)
		if _, ok := Verify(key, copy, now); ok {
			t.Error("tampered request accepted")
		}
	}
	for _, key := range [][]byte{nil, []byte("short"), DecodeKey(strings.Repeat("34", 32))} {
		if _, ok := Verify(key, r, now); ok {
			t.Error("wrong key accepted")
		}
	}
	for _, time := range []time.Time{now.Add(31 * time.Second), now.Add(-3 * time.Second)} {
		if _, ok := Verify(key, r, time); ok {
			t.Error("expired or future proof accepted")
		}
	}
	for _, remote := range []string{"[::1]:12345", "[::ffff:127.0.0.1]:12345"} {
		copy := r.Clone(r.Context())
		copy.RemoteAddr = remote
		if _, ok := Verify(key, copy, now); !ok {
			t.Error("literal loopback rejected")
		}
	}
}

func TestProofDoesNotAuthorizeOtherRoutesOrForwardedAddresses(t *testing.T) {
	key, r, now := signed(t)
	for _, path := range []string{"/api/v1/admin/state", "/api/v1/agent-shares", "/share/" + strings.Repeat("A", 64), APIPrefix + "short"} {
		copy := r.Clone(r.Context())
		copy.URL.Path = path
		if Sign(key, copy, netip.MustParseAddr("192.0.2.3"), now) != "" {
			t.Error("out-of-scope proof generated")
		}
		if _, ok := Verify(key, copy, now); ok {
			t.Error("out-of-scope proof verified")
		}
	}
	r.Header.Del(Header)
	r.Header.Set("X-Forwarded-For", "192.0.2.3")
	r.Header.Set("Forwarded", "for=192.0.2.3")
	if _, ok := Verify(key, r, now); ok {
		t.Error("untrusted forwarding headers accepted")
	}
}

func TestCanonicalPeerAddressesAndPrivateKeyDecode(t *testing.T) {
	for _, remote := range []string{"", "not-an-address", "0.0.0.0:10", "[::]:10"} {
		if _, ok := Peer(remote); ok {
			t.Error("invalid peer accepted")
		}
	}
	ip, ok := Peer("[fe80::1%example-interface]:10")
	if !ok || ip.String() != "fe80::1" || ip.Zone() != "" {
		t.Error("interface zone was not removed from a TCP peer")
	}
	for _, raw := range []string{"", strings.Repeat("1", 63), strings.Repeat("z", 64)} {
		if DecodeKey(raw) != nil {
			t.Error("invalid key accepted")
		}
	}
}
