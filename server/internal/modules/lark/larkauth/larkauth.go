// Package larkauth is the user OAuth of the Feishu / Lark open platform for the daily sync (docs/extensions/bots.md
// "Reading as you: daily sync"): the authorization page with PKCE, the code and refresh-token exchanges, the device
// flow for an app whose redirect URL could not be set, and adding the redirect URL and the user scopes to the app.
//
// Every call is stateless: the extension manager keeps the tokens, seals them and holds one lock per binding around a
// refresh (the platform rotates the refresh token, so two refreshes of one token lose the pair). Nothing here retries
// a token call: a code is used once and a refresh token rotates, so an answer lost on the way cannot be asked for
// again without risking the grant. Failures come back as *larksyncwire.Error, whose kind tells the manager what to do.
//
//persona:layer application
package larkauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/botcard"
	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// Paths of the user OAuth. The authorization page is on the accounts site; the token and user-info endpoints are on
// the open platform.
const (
	authorizePath = "/open-apis/authen/v1/authorize"
	tokenPath     = "/open-apis/authen/v2/oauth/token"
	userInfoPath  = "/open-apis/authen/v1/user_info"
	// devicePath is the device authorization endpoint of the accounts site. It is not in the platform's public docs:
	// the path follows RFC 8628 on the same site as the app registration (/oauth/v1/app/registration), unverified.
	devicePath = "/oauth/v1/device_authorization"
)

// UserAuth is the open platform's user OAuth (larksyncwire.UserAuth). The zero UserAuth is not usable: NewUserAuth
// makes one.
type UserAuth struct {
	http *http.Client
	// accounts and open replace the accounts site and the open platform every app would use (tests); "" follows the
	// app's domain.
	accounts, open string
	now            func() time.Time
	// sleep waits between attempts of a user-info read; tests make it return at once.
	sleep func(ctx context.Context, d time.Duration) error
}

var _ larksyncwire.UserAuth = (*UserAuth)(nil)

// NewUserAuth is the OAuth client. accounts and open, when not "", replace the accounts site and the open platform
// (tests); h, when not nil, replaces the HTTP client.
func NewUserAuth(accounts, open string, h *http.Client) *UserAuth {
	if h == nil {
		h = &http.Client{Timeout: 30 * time.Second}
	}
	return &UserAuth{
		http:     h,
		accounts: strings.TrimRight(strings.TrimSpace(accounts), "/"),
		open:     strings.TrimRight(strings.TrimSpace(open), "/"),
		now:      time.Now,
		sleep:    sleepCtx,
	}
}

// openBase is the open platform app's calls go to.
func (u *UserAuth) openBase(app larksyncwire.App) string {
	if u.open != "" {
		return u.open
	}
	return botcard.ResolveDomain(app.Domain)
}

// accountsBase is the accounts site of app's cloud: Feishu's or Lark's, and for a private deployment the deployment
// itself (its accounts pages are not known apart from it).
func (u *UserAuth) accountsBase(app larksyncwire.App) string {
	if u.accounts != "" {
		return u.accounts
	}
	switch d := botcard.ResolveDomain(app.Domain); d {
	case botcard.DomainFeishu:
		return botcard.AccountsHost("feishu")
	case botcard.DomainLark:
		return botcard.AccountsHost("lark")
	default:
		return d
	}
}

// AuthorizeURL is the authorization page for r: OAuth with PKCE (S256), asking for r.Scopes. A request without a
// verifier asks without PKCE.
func (u *UserAuth) AuthorizeURL(r larksyncwire.AuthRequest) string {
	q := url.Values{}
	q.Set("client_id", r.App.AppID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", r.RedirectURI)
	if scope := joinScopes(r.Scopes); scope != "" {
		q.Set("scope", scope)
	}
	if r.State != "" {
		q.Set("state", r.State)
	}
	if r.Verifier != "" {
		q.Set("code_challenge", Challenge(r.Verifier))
		q.Set("code_challenge_method", "S256")
	}
	return u.accountsBase(r.App) + authorizePath + "?" + q.Encode()
}

// Challenge is the PKCE S256 code challenge of verifier: its SHA-256 in base64url without padding (RFC 7636).
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// joinScopes is scopes space-separated, blanks and repeats left out.
func joinScopes(scopes []string) string {
	seen := map[string]bool{}
	out := make([]string, 0, len(scopes))
	for _, s := range scopes {
		if s = strings.TrimSpace(s); s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return strings.Join(out, " ")
}
