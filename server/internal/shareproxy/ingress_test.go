package shareproxy

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nwverse/persona/server/internal/osproc"
	"github.com/nwverse/persona/server/internal/shareproof"
)

func TestPrivateIngressKeyConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hub.json")
	body, _ := json.Marshal(map[string]any{"token": "synthetic-unrelated-secret", "sharing": map[string]string{"ingress_key": strings.Repeat("12", 32)}})
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	key, err := readIngressKey(path)
	if !osproc.OwnedByMe(info) {
		if err == nil {
			t.Error("unsupported ownership checking did not fail closed")
		}
		return
	}
	if err != nil || len(key) != 32 {
		t.Fatal("private key not loaded")
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readIngressKey(path); err == nil {
		t.Error("nonprivate key accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(path), "symlink.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readIngressKey(link); err == nil {
		t.Error("symlink file accepted")
	}
	if err := os.WriteFile(path, []byte(`{"token":"synthetic-unrelated-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readIngressKey(path); err == nil || strings.Contains(err.Error(), "synthetic-unrelated-secret") {
		t.Error("missing key did not fail privately")
	}
}

func TestMissingKeyAndLegacyUpstreamDoNotOpenListener(t *testing.T) {
	c := Config{Listen: ":18081", Upstream: "http://127.0.0.1:18080", PublicOrigin: testOrigin}
	if _, err := Handler(c); err == nil {
		t.Error("missing key accepted")
	}
	legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}")
	}))
	defer legacy.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	_, port, _ := net.SplitHostPort(address)
	c = Config{Listen: address, Upstream: legacy.URL, PublicOrigin: "http://share.example.test:" + port, IngressKey: testIngressKey}
	if err := Serve(t.Context(), c); err == nil {
		t.Error("legacy upstream accepted")
	}
	listener, err = net.Listen("tcp", address)
	if err != nil {
		t.Fatal("listener opened before authentication")
	}
	_ = listener.Close()
}

func TestLostAcknowledgementBlocksFurtherAnonymousRelays(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}")
	}))
	defer up.Close()
	h, err := Handler(Config{Listen: ":18081", Upstream: up.URL, PublicOrigin: testOrigin, IngressKey: testIngressKey})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		w := request(h, "GET", shareproof.APIPrefix+testToken, "share.example.test:18081")
		if w.Code != 502 {
			t.Errorf("unauthenticated response=%d", w.Code)
		}
		assertHeaders(t, w.Header())
	}
	if calls.Load() != 1 {
		t.Error("continued relay into an unauthenticated shared IP budget")
	}
}

func TestInvalidPeerDoesNotDisableOtherReaders(t *testing.T) {
	h := testProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}")
	}))
	r := httptest.NewRequest("GET", shareproof.APIPrefix+testToken, nil)
	r.Host = "share.example.test:18081"
	r.RemoteAddr = "invalid-peer"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Error("invalid TCP peer was forwarded")
	}
	w = request(h, "GET", shareproof.APIPrefix+testToken, "share.example.test:18081")
	if w.Code != 200 {
		t.Error("another reader was blocked by an invalid peer")
	}
}
