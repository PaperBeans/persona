package larkauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/botcard"
	"github.com/nwverse/persona/server/internal/larksyncwire"
)

var testNow = time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)

var testApp = larksyncwire.App{AppID: "cli_test", AppSecret: "app-secret", Domain: "feishu"}

const testRedirect = "https://hub.example.com/lark-oauth"

// fakePlatform is the open platform and the accounts site in one test server. Each endpoint's handler is set by the
// test; an endpoint without one fails the test. GET / is a local node's port probe and answers 404.
type fakePlatform struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	handlers map[string]http.HandlerFunc
	calls    []string
}

func newFakePlatform(t *testing.T) *fakePlatform {
	t.Helper()
	f := &fakePlatform{t: t, handlers: map[string]http.HandlerFunc{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/" { // a local node's port probe (node/ports.go)
			http.NotFound(w, r)
			return
		}
		f.mu.Lock()
		h := f.handlers[r.URL.Path]
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		if h == nil {
			t.Errorf("unexpected call %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakePlatform) handle(path string, h http.HandlerFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[path] = h
}

func (f *fakePlatform) count(call string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == call {
			n++
		}
	}
	return n
}

// auth is a UserAuth whose accounts site and open platform are the fake, with a fixed clock and no real waits.
func (f *fakePlatform) auth() *UserAuth {
	u := NewUserAuth(f.srv.URL, f.srv.URL, nil)
	u.now = func() time.Time { return testNow }
	u.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	return u
}

// userInfoOK answers user_info for the access token want with the test identity.
func (f *fakePlatform) userInfoOK(want string) {
	f.handle(userInfoPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			f.t.Errorf("user info: %s, want GET", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+want {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"code": 99991668, "msg": "invalid token"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"code": 0, "msg": "success", "data": map[string]any{
			"open_id": "ou_owner", "union_id": "on_owner", "tenant_key": "tenant_1", "name": "Test Owner"}})
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(t *testing.T, r *http.Request) map[string]string {
	t.Helper()
	var in map[string]string
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		t.Errorf("token request body: %v", err)
	}
	return in
}

func tokenOK(access, refresh string) map[string]any {
	return map[string]any{"code": 0, "access_token": access, "expires_in": 7200, "refresh_token": refresh,
		"refresh_token_expires_in": 604800, "scope": "offline_access docx:document:readonly", "token_type": "Bearer"}
}

func errKind(t *testing.T, err error) *larksyncwire.Error {
	t.Helper()
	var le *larksyncwire.Error
	if !errors.As(err, &le) {
		t.Fatalf("error %v (%T), want *larksyncwire.Error", err, err)
	}
	return le
}

// The authorization page carries the app, the redirect, every scope, the state and the S256 challenge of the
// verifier, on the accounts site.
func TestAuthorizeURLHasPKCEAndEveryScope(t *testing.T) {
	scopes := larksyncwire.ScopesFor(larksyncwire.TierFull)
	verifier := "verifier-0123456789-abcdefghijklmnopqrstuvwxyz-ABCDEF"
	raw := NewUserAuth("", "", nil).AuthorizeURL(larksyncwire.AuthRequest{App: testApp, RedirectURI: testRedirect,
		Scopes: scopes, State: "state-1", Verifier: verifier})
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != botcard.AccountsFeishu+authorizePath {
		t.Fatalf("page = %s, want %s", got, botcard.AccountsFeishu+authorizePath)
	}
	q := u.Query()
	for k, want := range map[string]string{"client_id": "cli_test", "response_type": "code", "redirect_uri": testRedirect,
		"state": "state-1", "code_challenge_method": "S256"} {
		if got := q.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if got := strings.Fields(q.Get("scope")); !slices.Equal(got, scopes) {
		t.Errorf("scope = %v, want %v", got, scopes)
	}
	if got, want := q.Get("code_challenge"), Challenge(verifier); got != want || strings.ContainsAny(got, "=+/") {
		t.Errorf("code_challenge = %q, want %q (base64url, no padding)", got, want)
	}
	if q.Has("code_verifier") || strings.Contains(raw, verifier) {
		t.Errorf("the page must not carry the verifier: %s", raw)
	}
}

// The challenge is RFC 7636's S256 (its appendix B example).
func TestChallengeIsS256(t *testing.T) {
	if got := Challenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatalf("Challenge = %q", got)
	}
}

// Feishu's and Lark's apps authorize on their own accounts sites and call their own open platforms; a private
// deployment uses its own address for both.
func TestHostsFollowTheAppsCloud(t *testing.T) {
	u := NewUserAuth("", "", nil)
	for _, c := range []struct{ domain, accounts, open string }{
		{"", botcard.AccountsFeishu, botcard.DomainFeishu},
		{"feishu", botcard.AccountsFeishu, botcard.DomainFeishu},
		{"lark", botcard.AccountsLark, botcard.DomainLark},
		{botcard.DomainLark + "/", botcard.AccountsLark, botcard.DomainLark},
		{"https://open.example.internal/", "https://open.example.internal", "https://open.example.internal"},
	} {
		app := larksyncwire.App{AppID: "cli_x", Domain: c.domain}
		if got := u.accountsBase(app); got != c.accounts {
			t.Errorf("%q: accounts %q, want %q", c.domain, got, c.accounts)
		}
		if got := u.openBase(app); got != c.open {
			t.Errorf("%q: open %q, want %q", c.domain, got, c.open)
		}
		if page := u.AuthorizeURL(larksyncwire.AuthRequest{App: app, RedirectURI: testRedirect}); !strings.HasPrefix(page, c.accounts+authorizePath+"?") {
			t.Errorf("%q: page %s", c.domain, page)
		}
	}
}

// Exchange sends the code with its verifier and redirect, then reads whose the token is.
func TestExchangeSendsTheVerifierAndIdentifies(t *testing.T) {
	f := newFakePlatform(t)
	var sent map[string]string
	f.handle(tokenPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			t.Errorf("token: %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		sent = readJSON(t, r)
		writeJSON(w, http.StatusOK, tokenOK("u-access-1", "r-refresh-1"))
	})
	f.userInfoOK("u-access-1")

	tok, err := f.auth().Exchange(t.Context(), testApp, "code-1", testRedirect, "verifier-1")
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"grant_type": "authorization_code", "client_id": "cli_test", "client_secret": "app-secret",
		"code": "code-1", "redirect_uri": testRedirect, "code_verifier": "verifier-1"} {
		if sent[k] != want {
			t.Errorf("%s = %q, want %q", k, sent[k], want)
		}
	}
	want := larksyncwire.Token{AccessToken: "u-access-1", RefreshToken: "r-refresh-1", ExpiresAt: testNow.Add(2 * time.Hour),
		RefreshExpiresAt: testNow.Add(7 * 24 * time.Hour), Scopes: []string{"offline_access", "docx:document:readonly"},
		OpenID: "ou_owner", UnionID: "on_owner", TenantKey: "tenant_1", Name: "Test Owner"}
	if !tokenEqual(tok, want) {
		t.Fatalf("token = %+v\nwant %+v", tok, want)
	}
}

func tokenEqual(a, b larksyncwire.Token) bool {
	return a.AccessToken == b.AccessToken && a.RefreshToken == b.RefreshToken && a.ExpiresAt.Equal(b.ExpiresAt) &&
		a.RefreshExpiresAt.Equal(b.RefreshExpiresAt) && slices.Equal(a.Scopes, b.Scopes) && a.OpenID == b.OpenID &&
		a.UnionID == b.UnionID && a.TenantKey == b.TenantKey && a.Name == b.Name
}

// A refresh-token lifetime the platform does not state is unknown: the zero time, not now.
func TestExchangeWithoutRefreshExpiry(t *testing.T) {
	f := newFakePlatform(t)
	f.handle(tokenPath, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"code": 0, "access_token": "u-access-1", "expires_in": 7200, "refresh_token": "r-1"})
	})
	f.userInfoOK("u-access-1")
	tok, err := f.auth().Exchange(t.Context(), testApp, "code-1", testRedirect, "verifier-1")
	if err != nil {
		t.Fatal(err)
	}
	if !tok.RefreshExpiresAt.IsZero() || !tok.ExpiresAt.Equal(testNow.Add(2*time.Hour)) {
		t.Fatalf("expiries = %v / %v", tok.ExpiresAt, tok.RefreshExpiresAt)
	}
}

// The token's owner is what binds the account: an exchange whose owner cannot be read fails, and the user-info read
// is tried again after a transient failure.
func TestExchangeIdentity(t *testing.T) {
	f := newFakePlatform(t)
	f.handle(tokenPath, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, tokenOK("u-access-1", "r-1"))
	})
	var mu sync.Mutex
	failures := 1
	f.handle(userInfoPath, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		fail := failures > 0
		failures--
		mu.Unlock()
		if fail {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"code": 1, "msg": "busy"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"code": 0, "data": map[string]any{"open_id": "ou_owner", "tenant_key": "tenant_1"}})
	})
	tok, err := f.auth().Exchange(t.Context(), testApp, "code-1", testRedirect, "v")
	if err != nil || tok.OpenID != "ou_owner" || tok.TenantKey != "tenant_1" {
		t.Fatalf("after one transient failure: %+v, %v", tok, err)
	}
	if n := f.count("GET " + userInfoPath); n != 2 {
		t.Fatalf("user info read %d times, want 2", n)
	}

	f.handle(userInfoPath, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"code": 99991668, "msg": "invalid access token"})
	})
	_, err = f.auth().Exchange(t.Context(), testApp, "code-2", testRedirect, "v")
	if le := errKind(t, err); le.Kind != larksyncwire.ErrKindAuth {
		t.Fatalf("refused user info: kind %q, want auth", le.Kind)
	}
}

// The platform rotates the refresh token: a refresh returns the new pair, and the old refresh token is refused from
// then on as an invalid grant.
func TestRefreshRotates(t *testing.T) {
	f := newFakePlatform(t)
	var mu sync.Mutex
	valid := map[string]bool{"r-1": true}
	n := 1
	f.handle(tokenPath, func(w http.ResponseWriter, r *http.Request) {
		in := readJSON(t, r)
		if in["grant_type"] != "refresh_token" || in["client_id"] != "cli_test" || in["client_secret"] != "app-secret" {
			t.Errorf("refresh body: %v", in)
		}
		mu.Lock()
		defer mu.Unlock()
		if !valid[in["refresh_token"]] {
			writeJSON(w, http.StatusBadRequest, map[string]any{"code": 20064, "error": "invalid_grant", "error_description": "refresh token revoked"})
			return
		}
		delete(valid, in["refresh_token"])
		n++
		next := "r-" + strconv.Itoa(n)
		valid[next] = true
		writeJSON(w, http.StatusOK, tokenOK("u-access-"+strconv.Itoa(n), next))
	})
	f.userInfoOK("u-access-2")
	u := f.auth()

	tok, err := u.Refresh(t.Context(), testApp, "r-1")
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "u-access-2" || tok.RefreshToken != "r-2" || tok.OpenID != "ou_owner" || !tok.ExpiresAt.Equal(testNow.Add(2*time.Hour)) {
		t.Fatalf("refreshed = %+v", tok)
	}
	_, err = u.Refresh(t.Context(), testApp, "r-1")
	if le := errKind(t, err); le.Kind != larksyncwire.ErrKindInvalidGrant || le.Code != 20064 {
		t.Fatalf("the old refresh token: %+v, want invalid_grant 20064", le)
	}
	// The user info of a refreshed token is a courtesy: a refused read keeps the new pair.
	tok, err = u.Refresh(t.Context(), testApp, "r-2")
	if err != nil || tok.RefreshToken != "r-3" || tok.OpenID != "" {
		t.Fatalf("refresh with a refused user info: %+v, %v", tok, err)
	}
}

// An answer without a new refresh token keeps the old one rather than losing it.
func TestRefreshKeepsTheOldRefreshTokenWhenNoneComes(t *testing.T) {
	f := newFakePlatform(t)
	f.handle(tokenPath, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"code": 0, "access_token": "u-access-2", "expires_in": 7200})
	})
	f.userInfoOK("u-access-2")
	tok, err := f.auth().Refresh(t.Context(), testApp, "r-1")
	if err != nil || tok.RefreshToken != "r-1" {
		t.Fatalf("refreshed = %+v, %v", tok, err)
	}
}

// How each failure of the token endpoint maps to a kind the manager acts on.
func TestTokenErrorKinds(t *testing.T) {
	cases := []struct {
		name   string
		status int
		header map[string]string
		body   string
		kind   string
		after  time.Duration
	}{
		{"oauth invalid_grant", 400, nil, `{"error":"invalid_grant","error_description":"bad code"}`, larksyncwire.ErrKindInvalidGrant, 0},
		{"refresh expired", 400, nil, `{"code":20037,"msg":"refresh token expired"}`, larksyncwire.ErrKindInvalidGrant, 0},
		{"refresh revoked", 200, nil, `{"code":20064,"msg":"revoked"}`, larksyncwire.ErrKindInvalidGrant, 0},
		{"refresh used", 400, nil, `{"code":20073,"msg":"already used"}`, larksyncwire.ErrKindInvalidGrant, 0},
		{"refresh invalid", 400, nil, `{"code":20074,"msg":"invalid"}`, larksyncwire.ErrKindInvalidGrant, 0},
		{"invalid scope", 400, nil, `{"error":"invalid_scope"}`, larksyncwire.ErrKindScope, 0},
		{"http 429", 429, map[string]string{"Retry-After": "7"}, `{"code":99991400,"msg":"too many"}`, larksyncwire.ErrKindRateLimited, 7 * time.Second},
		{"gateway limit", 400, map[string]string{"X-Ogw-Ratelimit-Reset": "3"}, `{"code":99991400,"msg":"limited"}`, larksyncwire.ErrKindRateLimited, 3 * time.Second},
		{"429 no json", 429, nil, `slow down`, larksyncwire.ErrKindRateLimited, 0},
		{"server error", 503, nil, `{"code":20050,"msg":"internal"}`, larksyncwire.ErrKindTransient, 0},
		{"server error no json", 502, nil, `<html>bad gateway</html>`, larksyncwire.ErrKindTransient, 0},
		{"bad client", 401, nil, `{"error":"invalid_client"}`, larksyncwire.ErrKindOther, 0},
		{"no token", 200, nil, `{"code":0}`, larksyncwire.ErrKindOther, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakePlatform(t)
			f.handle(tokenPath, func(w http.ResponseWriter, r *http.Request) {
				for k, v := range c.header {
					w.Header().Set(k, v)
				}
				w.WriteHeader(c.status)
				_, _ = io.WriteString(w, c.body)
			})
			_, err := f.auth().Refresh(t.Context(), testApp, "r-1")
			le := errKind(t, err)
			if le.Kind != c.kind || le.RetryAfter != c.after {
				t.Fatalf("error = %+v, want kind %q after %v", le, c.kind, c.after)
			}
			if f.count("POST "+tokenPath) != 1 {
				t.Fatalf("token endpoint called %d times: a token call is never retried", f.count("POST "+tokenPath))
			}
		})
	}
}

// No answer at all is transient; a context that ended is the context's error.
func TestTokenNetworkFailures(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.URL
	srv.Close()
	_, err := NewUserAuth(addr, addr, nil).Refresh(t.Context(), testApp, "r-1")
	if le := errKind(t, err); le.Kind != larksyncwire.ErrKindTransient {
		t.Fatalf("closed server: %+v, want transient", le)
	}

	f := newFakePlatform(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.auth().Exchange(ctx, testApp, "code", testRedirect, "v"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v, want context.Canceled", err)
	}
}

// The device flow: begin on the accounts site with the app's credentials, then pending, slow down and done.
func TestDeviceFlow(t *testing.T) {
	f := newFakePlatform(t)
	f.handle(devicePath, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		user, pass, ok := r.BasicAuth()
		if r.Method != http.MethodPost || !ok || user != "cli_test" || pass != "app-secret" ||
			r.PostForm.Get("client_id") != "cli_test" || r.PostForm.Get("scope") != "offline_access wiki:wiki:readonly" {
			t.Errorf("device begin: %s %v basic %q:%q", r.Method, r.PostForm, user, pass)
		}
		writeJSON(w, http.StatusOK, map[string]any{"device_code": "dev-1", "user_code": "ABCD-EFGH",
			"verification_uri": "https://accounts.example.com/device", "expires_in": 600, "interval": 5})
	})
	var mu sync.Mutex
	answers := []string{"authorization_pending", "slow_down", ""}
	f.handle(tokenPath, func(w http.ResponseWriter, r *http.Request) {
		in := readJSON(t, r)
		if in["grant_type"] != deviceGrant || in["device_code"] != "dev-1" || in["client_id"] != "cli_test" || in["client_secret"] != "app-secret" {
			t.Errorf("device poll body: %v", in)
		}
		mu.Lock()
		next := answers[0]
		answers = answers[1:]
		mu.Unlock()
		if next != "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": next})
			return
		}
		writeJSON(w, http.StatusOK, tokenOK("u-access-1", "r-1"))
	})
	f.userInfoOK("u-access-1")
	u := f.auth()

	ch, err := u.DeviceBegin(t.Context(), testApp, []string{"offline_access", "wiki:wiki:readonly", "offline_access"})
	if err != nil {
		t.Fatal(err)
	}
	if ch.DeviceCode != "dev-1" || ch.UserCode != "ABCD-EFGH" || ch.ExpiresIn != 600 || ch.Interval != 5 ||
		ch.URL != "https://accounts.example.com/device?user_code=ABCD-EFGH" {
		t.Fatalf("challenge = %+v", ch)
	}
	for _, want := range []string{larksyncwire.DevicePending, larksyncwire.DeviceSlowDown, larksyncwire.DeviceDone} {
		p, err := u.DevicePoll(t.Context(), testApp, "dev-1")
		if err != nil {
			t.Fatal(err)
		}
		if p.Status != want {
			t.Fatalf("poll = %+v, want %s", p, want)
		}
		if want == larksyncwire.DeviceDone && (p.Token.AccessToken != "u-access-1" || p.Token.RefreshToken != "r-1" || p.Token.OpenID != "ou_owner") {
			t.Fatalf("done token = %+v", p.Token)
		}
	}
}

func TestDevicePollEnds(t *testing.T) {
	for _, c := range []struct {
		status int
		body   string
		want   string
	}{
		{400, `{"error":"expired_token"}`, larksyncwire.DeviceExpired},
		{400, `{"error":"access_denied"}`, larksyncwire.DeviceDenied},
		{400, `{"error":"unsupported_grant_type","error_description":"no device flow"}`, larksyncwire.DeviceFailed},
	} {
		f := newFakePlatform(t)
		f.handle(tokenPath, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
			_, _ = io.WriteString(w, c.body)
		})
		p, err := f.auth().DevicePoll(t.Context(), testApp, "dev-1")
		if err != nil || p.Status != c.want {
			t.Fatalf("%s: %+v, %v, want %s", c.body, p, err, c.want)
		}
		if c.want == larksyncwire.DeviceFailed && !strings.Contains(p.ErrorText, "unsupported_grant_type") {
			t.Fatalf("failed poll text = %q", p.ErrorText)
		}
	}
	// A rate limit is an error to wait out, not the end of the authorization.
	f := newFakePlatform(t)
	f.handle(tokenPath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "2")
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"code": 99991400})
	})
	_, err := f.auth().DevicePoll(t.Context(), testApp, "dev-1")
	if le := errKind(t, err); le.Kind != larksyncwire.ErrKindRateLimited || le.RetryAfter != 2*time.Second {
		t.Fatalf("limited poll: %+v", le)
	}
}

// A device endpoint the platform does not have fails the begin, so the caller falls back to the manual way.
func TestDeviceBeginUnsupported(t *testing.T) {
	f := newFakePlatform(t)
	f.handle(devicePath, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]any{"code": 404, "msg": "not found"})
	})
	_, err := f.auth().DeviceBegin(t.Context(), testApp, []string{"offline_access"})
	if le := errKind(t, err); le.Kind != larksyncwire.ErrKindOther || le.Op != "device" {
		t.Fatalf("unsupported device flow: %+v", le)
	}
}

// configPlatform serves the app's tenant token, config and publish with the given answers' codes.
func configPlatform(t *testing.T, config, publish int) (*fakePlatform, *map[string]any) {
	f := newFakePlatform(t)
	var sent map[string]any
	f.handle("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"code": 0, "tenant_access_token": "t-1", "expire": 7200})
	})
	f.handle("/open-apis/application/v7/applications/cli_test/config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.Header.Get("Authorization") != "Bearer t-1" {
			t.Errorf("config: %s %q", r.Method, r.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&sent)
		writeJSON(w, http.StatusOK, map[string]any{"code": config, "msg": "config answer"})
	})
	f.handle("/open-apis/application/v7/applications/cli_test/publish", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"code": publish, "msg": "publish answer", "data": map[string]any{"version_id": "oav_1"}})
	})
	return f, &sent
}

func TestConfigureRedirect(t *testing.T) {
	scopes := larksyncwire.ScopesFor(larksyncwire.TierDocs)
	consoleFeishu := botcard.DomainFeishu + "/app/cli_test/safe"

	f, sent := configPlatform(t, 0, 0)
	rep := f.auth().ConfigureRedirect(t.Context(), testApp, testRedirect, scopes)
	if !rep.Configured || !rep.Published || rep.Awaiting || rep.MissingPatch || rep.ConsoleURL != consoleFeishu {
		t.Fatalf("success: %+v", rep)
	}
	sec := (*sent)["security"].(map[string]any)["add"].(map[string]any)["redirect_urls"].([]any)
	if len(sec) != 1 || sec[0] != testRedirect || len((*sent)["scope"].(map[string]any)["add_scopes"].([]any)) != len(scopes) {
		t.Fatalf("config body: %v", *sent)
	}

	f, _ = configPlatform(t, 0, 210302)
	if rep := f.auth().ConfigureRedirect(t.Context(), testApp, testRedirect, scopes); !rep.Configured || !rep.Published || !rep.Awaiting {
		t.Fatalf("under review: %+v, want awaiting", rep)
	}

	for _, code := range []int{230002, 230003, 99991672, 99991676} {
		f, _ = configPlatform(t, code, 0)
		rep := f.auth().ConfigureRedirect(t.Context(), testApp, testRedirect, scopes)
		if rep.Configured || rep.Published || !rep.MissingPatch || rep.ConsoleURL != consoleFeishu {
			t.Fatalf("%d: %+v, want missing patch with the console's security page", code, rep)
		}
		if f.count("POST /open-apis/application/v7/applications/cli_test/publish") != 0 {
			t.Fatalf("%d: published after a refused config", code)
		}
	}

	lark := larksyncwire.App{AppID: "cli_test", AppSecret: "app-secret", Domain: "lark"}
	f, _ = configPlatform(t, 230002, 0)
	if rep := f.auth().ConfigureRedirect(t.Context(), lark, testRedirect, scopes); rep.ConsoleURL != botcard.DomainLark+"/app/cli_test/safe" {
		t.Fatalf("lark console = %q", rep.ConsoleURL)
	}

	f, _ = configPlatform(t, 210041, 0)
	if rep := f.auth().ConfigureRedirect(t.Context(), testApp, testRedirect, scopes); rep.Configured || rep.MissingPatch || !strings.HasPrefix(rep.Detail, "bot_capability") {
		t.Fatalf("other failure: %+v", rep)
	}

	// A private deployment's console is not configured from here: nothing is called, and the caller falls back.
	f = newFakePlatform(t)
	private := larksyncwire.App{AppID: "cli_test", AppSecret: "app-secret", Domain: "https://open.example.internal"}
	if rep := f.auth().ConfigureRedirect(t.Context(), private, testRedirect, scopes); !rep.MissingPatch || rep.ConsoleURL != "" || rep.Configured {
		t.Fatalf("private: %+v", rep)
	}
}
