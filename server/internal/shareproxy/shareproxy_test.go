package shareproxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/shareproof"
)

const testOrigin = "http://share.example.test:18081"

var testToken = strings.Repeat("A", 64)
var testIngressKey = shareproof.DecodeKey(strings.Repeat("12", 32))

func acknowledged(t *testing.T, next http.Handler) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if shareproof.Scope(r) {
			if _, ok := shareproof.Verify(testIngressKey, r, time.Now()); !ok {
				t.Error("ingress request proof was not authentic")
			}
			w.Header().Set(shareproof.Acknowledgement, shareproof.Accepted)
		}
		next.ServeHTTP(w, r)
	})
}

func testProxy(t *testing.T, upstream http.Handler) http.Handler {
	t.Helper()
	s := httptest.NewServer(acknowledged(t, upstream))
	t.Cleanup(s.Close)
	h, err := Handler(Config{Listen: ":18081", Upstream: s.URL, PublicOrigin: testOrigin, IngressKey: testIngressKey})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func request(h http.Handler, method, path, host string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	r.Host = host
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func assertHeaders(t *testing.T, h http.Header) {
	t.Helper()
	for k, v := range map[string]string{"Cache-Control": "no-store", "Content-Security-Policy": csp,
		"X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer", "X-Frame-Options": "DENY"} {
		if h.Get(k) != v {
			t.Errorf("%s = %q", k, h.Get(k))
		}
	}
	for _, k := range []string{"Set-Cookie", "Location", "WWW-Authenticate", "X-Private", "Access-Control-Allow-Origin", shareproof.Header, shareproof.Acknowledgement} {
		if h.Get(k) != "" {
			t.Errorf("unexpected %s", k)
		}
	}
}

func TestConfigFailsClosed(t *testing.T) {
	valid := Config{Listen: ":18081", Upstream: "http://127.0.0.1:18080", PublicOrigin: testOrigin, IngressKey: testIngressKey}
	for _, c := range []Config{
		{}, {Listen: ":80", Upstream: valid.Upstream, PublicOrigin: "http://share.example.test:80"},
		{Listen: ":18082", Upstream: valid.Upstream, PublicOrigin: testOrigin},
		{Listen: valid.Listen, Upstream: "http://example.test:18080", PublicOrigin: testOrigin},
		{Listen: valid.Listen, Upstream: "http://user:secret@127.0.0.1:18080", PublicOrigin: testOrigin},
		{Listen: valid.Listen, Upstream: valid.Upstream + "/api", PublicOrigin: testOrigin},
		{Listen: valid.Listen, Upstream: valid.Upstream, PublicOrigin: testOrigin + "?token=bad"},
		{Listen: valid.Listen, Upstream: valid.Upstream, PublicOrigin: "https://share.example.test:18081"},
		{Listen: valid.Listen, Upstream: valid.Upstream, PublicOrigin: "http://share.example.test"},
		{Listen: valid.Listen, Upstream: "http://127.0.0.1:18081", PublicOrigin: "http://127.0.0.1:18081"},
	} {
		c.IngressKey = testIngressKey
		if _, err := Handler(c); err == nil {
			t.Error("invalid config accepted")
		}
	}
	if _, err := Handler(valid); err != nil {
		t.Fatal(err)
	}
	valid.Upstream = "http://[::1]:18080"
	if _, err := Handler(valid); err != nil {
		t.Fatal(err)
	}
}

func TestNonPublicRoutesAndForgedHostNeverReachHub(t *testing.T) {
	h := testProxy(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("nonpublic request reached hub") }))
	for _, method := range []string{"GET", "HEAD", "POST", "DELETE", "PATCH", "CONNECT"} {
		for _, path := range []string{"/", "/admin", "/api/v1/admin/state", "/api/v1/agent-shares", "/api/v1/health", "/ws",
			"/sw.js", "/manifest.webmanifest", "/share-assets/../main.js", "/share-assets/nested/main.js", "/share-assets/main.js.map",
			"/share/not-a-capability", "/api/public/agent-shares/", "/share/%41" + testToken[1:],
			"http://share.example.test:18081/share/" + testToken} {
			w := request(h, method, path, "share.example.test:18081")
			if w.Code != 404 {
				t.Errorf("route %s %s = %d", method, path, w.Code)
			}
			assertHeaders(t, w.Header())
		}
	}
	for _, host := range []string{"app.example.test:18080", "share.example.test", "attacker.test:18081"} {
		if w := request(h, "GET", "/share/"+testToken, host); w.Code != 404 {
			t.Error("forged host accepted")
		}
	}
	for _, body := range []bool{true, false} {
		r := httptest.NewRequest("GET", "/share/"+testToken, nil)
		r.Host = "share.example.test:18081"
		if body {
			r.Body = io.NopCloser(strings.NewReader("secret"))
			r.ContentLength = 6
		} else {
			r.Header.Set("Upgrade", "websocket")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 404 {
			t.Error("body or upgrade accepted")
		}
	}
}

func TestExactPathsPinHostAndStripAllCredentials(t *testing.T) {
	h := testProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "share.example.test:18081" || r.Method != "GET" {
			t.Error("upstream not pinned")
		}
		for _, k := range []string{"Authorization", "Proxy-Authorization", "Cookie", "X-Persona-Manager", "X-Jarvis-CSRF", "X-Persona-CSRF", "X-Forwarded-For", "X-Forwarded-Host", "Forwarded", "X-Private"} {
			if r.Header.Get(k) != "" {
				t.Errorf("credential or forwarded header %s leaked", k)
			}
		}
		if r.Header.Get("Accept-Language") != "ja" {
			t.Error("language lost")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Private", "secret")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_, _ = io.WriteString(w, "{\"messages\":[]}")
	}))
	for _, path := range []string{"/share/" + testToken, "/api/public/agent-shares/" + testToken + "?limit=100&cursor=opaque", "/share-assets/viewer-abc.js", "/share-assets/viewer.css"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Host = "SHARE.EXAMPLE.TEST:18081"
		for _, k := range []string{"Authorization", "Proxy-Authorization", "Cookie", "X-Persona-Manager", "X-Jarvis-CSRF", "X-Persona-CSRF", "X-Forwarded-For", "X-Forwarded-Host", "Forwarded", "X-Private"} {
			r.Header.Set(k, "synthetic-secret")
		}
		r.Header.Set(shareproof.Header, "forged-client-proof")
		r.Header.Set(shareproof.Acknowledgement, "forged-client-acknowledgement")
		r.Header.Set("Accept-Language", "ja")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.String() != "{\"messages\":[]}" {
			t.Errorf("public route failed: %d", w.Code)
		}
		assertHeaders(t, w.Header())
	}
}

func TestRejectsDangerousAndOversizedUpstreamResponses(t *testing.T) {
	for _, which := range []string{"redirect", "cookie", "auth", "encoding", "oversized", "chunked-oversized", "content-type"} {
		t.Run(which, func(t *testing.T) {
			h := testProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch which {
				case "redirect":
					w.Header().Set("Location", "http://app.example.test/login")
					w.WriteHeader(302)
				case "cookie":
					w.Header().Set("Set-Cookie", "admin=secret")
				case "auth":
					w.Header().Set("WWW-Authenticate", "Bearer")
					w.WriteHeader(401)
				case "encoding":
					w.Header().Set("Content-Encoding", "gzip")
				case "oversized":
					w.Header().Set("Content-Length", strconv.Itoa(maxResponse+1))
				case "chunked-oversized":
					w.(http.Flusher).Flush()
					_, _ = io.WriteString(w, strings.Repeat("x", maxResponse+1))
					return
				case "content-type":
					w.Header().Set("Content-Type", "application/octet-stream")
				}
				_, _ = io.WriteString(w, "synthetic-private-body")
			}))
			w := request(h, "GET", "/api/public/agent-shares/"+testToken, "share.example.test:18081")
			if w.Code != 502 || strings.Contains(w.Body.String(), "private") {
				t.Errorf("unexpected response: %d", w.Code)
			}
			assertHeaders(t, w.Header())
		})
	}
}

func TestPublicErrorsStayProtected(t *testing.T) {
	for _, status := range []int{400, 404, 409, 429} {
		h := testProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "15")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, "{\"code\":\"not_found\"}")
		}))
		w := request(h, "GET", "/api/public/agent-shares/"+testToken, "share.example.test:18081")
		if w.Code != status || w.Header().Get("Retry-After") != "15" {
			t.Errorf("status %d not retained", status)
		}
		assertHeaders(t, w.Header())
	}
}

func TestInFlightRelaysAreBounded(t *testing.T) {
	entered, release := make(chan struct{}, maxRelays), make(chan struct{})
	h := testProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}")
	}))
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var workers sync.WaitGroup
	for range maxRelays {
		workers.Go(func() { request(h, "GET", "/api/public/agent-shares/"+testToken, "share.example.test:18081") })
	}
	for range maxRelays {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("relay did not enter")
		}
	}
	w := request(h, "GET", "/api/public/agent-shares/"+testToken, "share.example.test:18081")
	if w.Code != 429 {
		t.Errorf("extra relay=%d", w.Code)
	}
	assertHeaders(t, w.Header())
	unblock()
	workers.Wait()
}

func TestRealListenerCancellationLeavesUpstreamAlive(t *testing.T) {
	up := httptest.NewServer(acknowledged(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}")
	})))
	defer up.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	_, port, _ := net.SplitHostPort(address)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, Config{Listen: address, Upstream: up.URL, PublicOrigin: "http://share.example.test:" + port, IngressKey: testIngressKey})
	}()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(5 * time.Second)
	for {
		r, _ := http.NewRequest("GET", "http://"+address+"/share/"+testToken, nil)
		r.Host = "share.example.test:" + port
		response, e := client.Do(r)
		if e == nil {
			_ = response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatal(response.StatusCode)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("listener not ready")
		}
		select {
		case e := <-done:
			t.Fatal(e)
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown timed out")
	}
	response, err := client.Get(up.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("upstream was affected")
	}
}

func TestRunHelpDoesNotListen(t *testing.T) {
	var output strings.Builder
	err := Run(t.Context(), []string{"-h"}, &output)
	if err == nil || !strings.Contains(output.String(), "public-origin") {
		t.Fatal(fmt.Sprint(err))
	}
}
