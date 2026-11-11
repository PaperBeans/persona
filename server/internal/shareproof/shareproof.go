// Package shareproof authenticates only the peer IP of a local public-sharing ingress.
// The proof is not an account credential or permission to read a publication.
package shareproof

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

const (
	Header          = "X-Persona-Share-Ingress"
	Acknowledgement = "X-Persona-Share-Ingress-Acknowledged"
	Accepted        = "v1"
	APIPrefix       = "/api/public/agent-shares/"
)

func DecodeKey(raw string) []byte {
	if len(raw) != 64 {
		return nil
	}
	key, err := hex.DecodeString(raw)
	if err != nil || len(key) != 32 {
		return nil
	}
	return key
}

func Scope(r *http.Request) bool {
	token, ok := strings.CutPrefix(r.URL.Path, APIPrefix)
	if !ok || r.Method != http.MethodGet || r.Host == "" || r.URL.RawPath != "" || len(token) != 64 {
		return false
	}
	for _, c := range token {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func Peer(remote string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return netip.Addr{}, false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.IsUnspecified() {
		return netip.Addr{}, false
	}
	return ip.WithZone("").Unmap(), true
}

func mac(key []byte, r *http.Request, ip, stamp string) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte("persona-public-share-ingress/v1\n" + r.Method + "\n" + r.Host + "\n" + r.URL.RequestURI() + "\n" + ip + "\n" + stamp))
	return h.Sum(nil)
}

// Sign derives no address from request headers; the ingress supplies its actual TCP peer.
func Sign(key []byte, r *http.Request, ip netip.Addr, now time.Time) string {
	if len(key) != 32 || !Scope(r) || !ip.IsValid() || ip.Zone() != "" || ip.IsUnspecified() {
		return ""
	}
	address, stamp := ip.Unmap().String(), strconv.FormatInt(now.Unix(), 10)
	return stamp + "|" + address + "|" + hex.EncodeToString(mac(key, r, address, stamp))
}

// Verify accepts an exact request only from literal loopback, within a 30-second window.
func Verify(key []byte, r *http.Request, now time.Time) (netip.Addr, bool) {
	peer, ok := Peer(r.RemoteAddr)
	if len(key) != 32 || !ok || !peer.IsLoopback() || !Scope(r) || len(r.Header.Values(Header)) != 1 {
		return netip.Addr{}, false
	}
	value := r.Header.Get(Header)
	if len(value) > 160 {
		return netip.Addr{}, false
	}
	parts := strings.Split(value, "|")
	if len(parts) != 3 {
		return netip.Addr{}, false
	}
	stamp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || stamp < now.Unix()-30 || stamp > now.Unix()+2 {
		return netip.Addr{}, false
	}
	ip, err := netip.ParseAddr(parts[1])
	if err != nil || ip.Zone() != "" || ip.IsUnspecified() || ip.Unmap().String() != parts[1] {
		return netip.Addr{}, false
	}
	got, err := hex.DecodeString(parts[2])
	if err != nil || !hmac.Equal(got, mac(key, r, parts[1], parts[0])) {
		return netip.Addr{}, false
	}
	return ip.Unmap(), true
}
