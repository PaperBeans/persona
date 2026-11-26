package extmgr

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/botcard"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// Authorizing a bot's app to read as the owner (OAuth with PKCE and offline_access, so the refresh token lasts a year),
// on the same page right after the registration. The redirect URL (the web app's /lark-oauth) must be on the app:
// the module adds it with the application/v7 config patch. An app without the patch permission falls back to the
// device flow (a QR code, no redirect needed), and when that is not offered either, to the owner pasting the redirect
// URL into the app's security settings (the page links straight there) and then the browser. Whatever the mode, the
// answer names the account authorized, and it becomes (or renews) the (account, bot) binding.

// Authorization modes (the authorize route's answer, larkSyncBinding.AuthMode).
const (
	larkSyncModeBrowser = "browser"
	larkSyncModeDevice  = "device"
	larkSyncModeManual  = "manual"
)

// larkSyncAuthorizeAnswer is the authorize route's answer.
type larkSyncAuthorizeAnswer struct {
	Mode        string            `json:"mode"`
	URL         string            `json:"url,omitempty"`
	Prompt      *connector.Prompt `json:"prompt,omitempty"`
	RedirectURI string            `json:"redirect_uri,omitempty"`
	ConsoleURL  string            `json:"console_url,omitempty"`
}

var (
	errLarkSyncRedirect = i18n.Err("hub-larksync.bad_redirect")
	errLarkSyncState    = i18n.Err("hub-larksync.auth_expired")
	errLarkSyncExchange = i18n.Err("hub-larksync.auth_failed")
)

// larkSyncValidRedirect says whether raw is an absolute http(s) URL of the web app's /lark-oauth page: at the root, or
// under the path prefix of a hub served behind a reverse proxy ("/persona/lark-oauth").
func larkSyncValidRedirect(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" &&
		(u.Path == "/lark-oauth" || strings.HasSuffix(u.Path, "/lark-oauth")) && !strings.Contains(u.Path, "..") &&
		u.User == nil && u.Fragment == "" && u.RawQuery == ""
}

// larkSyncVerifier is a new PKCE code verifier (43 characters of base64url, RFC 7636) and state (64 hex).
func larkSyncVerifier() (verifier, state string) {
	v := make([]byte, 32)
	_, _ = rand.Read(v)
	s := make([]byte, 32)
	_, _ = rand.Read(s)
	return base64.RawURLEncoding.EncodeToString(v), hex.EncodeToString(s)
}

// larkSyncPendingKey is bot's authorization under way.
func larkSyncPendingKey(botID string) string { return botID + larkSyncPendingTail }

// larkSyncAuthorize starts authorizing botID's app as the owner with tier ("": the bot's).
func (h *Manager) larkSyncAuthorize(ctx context.Context, botID, redirect, tier string) (larkSyncAuthorizeAnswer, error) {
	if !larkSyncValidRedirect(redirect) {
		return larkSyncAuthorizeAnswer{}, errLarkSyncRedirect
	}
	ua := h.larkSyncUserAuth()
	if ua == nil {
		return larkSyncAuthorizeAnswer{}, errLarkSyncUnavailable
	}
	st, err := h.larkSyncLoad(ctx)
	if err != nil {
		return larkSyncAuthorizeAnswer{}, err
	}
	app, err := h.larkSyncApp(ctx, &st, botID)
	if err != nil {
		return larkSyncAuthorizeAnswer{}, err
	}
	if !larksyncwire.ValidTier(tier) {
		tier = st.tierOf(botID)
	}
	scopes := larksyncwire.ScopesFor(tier)
	configured := st.prefs.PrimaryRedirect
	if b := st.bot(botID); b != nil {
		configured = b.Redirect
	}
	mode := larkSyncModeBrowser
	consoleURL := ""
	// A known redirect does not imply the app has scopes added by a newer reader. Reconfigure a binding
	// reporting missing scopes when the owner explicitly starts authorization again.
	missingScopes := slices.ContainsFunc(st.bindings, func(b larkSyncBinding) bool {
		return b.BotID == botID && b.Health == larksyncwire.HealthScopeMissing
	})
	if configured != redirect || missingScopes {
		rep := ua.ConfigureRedirect(ctx, app, redirect, scopes)
		if rep.Detail != "" {
			h.logf("lark sync: %s: configure: %s", botID, rep.Detail)
		}
		switch {
		case rep.Configured:
			approval := larksyncwire.ApprovalReady
			if rep.Awaiting {
				approval = larksyncwire.ApprovalAwaiting
			}
			if _, err := h.larkSyncChange(ctx, func(st *larkSyncState) error {
				if botID == larksyncwire.PrimaryBot {
					st.prefs.PrimaryRedirect, st.prefs.PrimaryApproval = redirect, approval
					return nil
				}
				b := st.bot(botID)
				if b == nil {
					return errLarkSyncNoBot
				}
				b.Redirect, b.Approval = redirect, approval
				return nil
			}); err != nil {
				return larkSyncAuthorizeAnswer{}, err
			}
		case rep.MissingPatch:
			mode = larkSyncModeDevice
			consoleURL = rep.ConsoleURL
		default:
			mode = larkSyncModeManual
			consoleURL = rep.ConsoleURL
		}
		if consoleURL == "" {
			if base := botcard.AppConsoleURL(app.Domain, app.AppID); base != "" {
				consoleURL = base + "/safe"
			}
		}
		if mode != larkSyncModeBrowser {
			_, _ = h.larkSyncChange(ctx, func(st *larkSyncState) error {
				if botID == larksyncwire.PrimaryBot {
					st.prefs.PrimaryApproval = larksyncwire.ApprovalDegraded
				} else if b := st.bot(botID); b != nil {
					b.Approval = larksyncwire.ApprovalDegraded
				}
				return nil
			})
		}
	}
	now := h.bots.sync.clock()
	pending := larkSyncBinding{Key: larkSyncPendingKey(botID), BotID: botID, Tier: tier, AuthStartedAt: now}
	if mode == larkSyncModeDevice {
		ch, err := ua.DeviceBegin(ctx, app, scopes)
		if err != nil {
			h.logf("lark sync: %s: device flow: %v", botID, err)
			mode = larkSyncModeManual
		} else {
			sealed, err := h.larkSyncSeal(ch.DeviceCode)
			if err != nil {
				return larkSyncAuthorizeAnswer{}, err
			}
			expires := time.Duration(ch.ExpiresIn) * time.Second
			if expires <= 0 {
				expires = larkSyncAuthFor
			}
			pending.AuthMode, pending.AuthDeviceEnc = larkSyncModeDevice, sealed
			pending.AuthInterval, pending.AuthExpiresAt = max(ch.Interval, botRegisterInterval), now.Add(expires)
			if err := h.larkSyncPutPending(ctx, pending); err != nil {
				return larkSyncAuthorizeAnswer{}, err
			}
			h.auditBot(ctx, botRecord{auditLarkSyncAuthorize, map[string]any{"bot_id": botID, "mode": mode, "tier": tier}})
			p := h.larkSyncDevicePrompt(ctx, ch.URL, ch.UserCode, pending.AuthInterval, "")
			return larkSyncAuthorizeAnswer{Mode: mode, Prompt: &p}, nil
		}
	}
	verifier, state := larkSyncVerifier()
	sealed, err := h.larkSyncSeal(verifier)
	if err != nil {
		return larkSyncAuthorizeAnswer{}, err
	}
	pending.AuthMode, pending.AuthState, pending.AuthVerifierEnc, pending.AuthRedirect = mode, state, sealed, redirect
	pending.AuthExpiresAt = now.Add(larkSyncAuthFor)
	if err := h.larkSyncPutPending(ctx, pending); err != nil {
		return larkSyncAuthorizeAnswer{}, err
	}
	h.auditBot(ctx, botRecord{auditLarkSyncAuthorize, map[string]any{"bot_id": botID, "mode": mode, "tier": tier}})
	out := larkSyncAuthorizeAnswer{Mode: mode, URL: ua.AuthorizeURL(larksyncwire.AuthRequest{App: app, RedirectURI: redirect,
		Scopes: scopes, State: state, Verifier: verifier})}
	if mode == larkSyncModeManual {
		out.RedirectURI, out.ConsoleURL = redirect, consoleURL
	}
	return out, nil
}

// larkSyncPutPending keeps p as its bot's authorization under way, replacing an older one.
func (h *Manager) larkSyncPutPending(ctx context.Context, p larkSyncBinding) error {
	_, err := h.larkSyncChange(ctx, func(st *larkSyncState) error {
		st.bindings = slices.DeleteFunc(st.bindings, func(b larkSyncBinding) bool { return b.Key == p.Key })
		st.bindings = append(st.bindings, p)
		return nil
	})
	return err
}

// larkSyncDevicePrompt is the device flow's QR step.
func (h *Manager) larkSyncDevicePrompt(ctx context.Context, link, userCode string, interval int, errText string) connector.Prompt {
	l := h.botLang(ctx)
	p := connector.Prompt{Step: stepLarkSyncAuthorize, Kind: connector.PromptQRCode, URL: link, UserCode: userCode, Interval: interval,
		Hint: i18n.T(l, "hub-larksync.device_hint"), Link: link, LinkText: i18n.T(l, "hub-larksync.device_open_link"), Error: errText}
	if err := withQR(&p); err != nil {
		h.logf("lark sync: device QR: %v", err)
	}
	return p
}

// larkSyncCallback finishes a browser authorization: the page the platform redirected to hands back state and code.
func (h *Manager) larkSyncCallback(ctx context.Context, state, code string) error {
	if state == "" || code == "" {
		return errLarkSyncState
	}
	ua := h.larkSyncUserAuth()
	if ua == nil {
		return errLarkSyncUnavailable
	}
	st, err := h.larkSyncLoad(ctx)
	if err != nil {
		return err
	}
	var p *larkSyncBinding
	for i := range st.bindings {
		b := &st.bindings[i]
		if b.pending() && b.AuthState != "" && subtle.ConstantTimeCompare([]byte(b.AuthState), []byte(state)) == 1 {
			p = b
			break
		}
	}
	if p == nil || !h.bots.sync.clock().Before(p.AuthExpiresAt) {
		return errLarkSyncState
	}
	verifier, err := h.larkSyncOpen(p.AuthVerifierEnc)
	if err != nil {
		return errLarkSyncState
	}
	app, err := h.larkSyncApp(ctx, &st, p.BotID)
	if err != nil {
		return err
	}
	tok, err := ua.Exchange(ctx, app, code, p.AuthRedirect, verifier)
	if err != nil {
		h.logf("lark sync: %s: exchange: %v", p.BotID, err)
		return errLarkSyncExchange
	}
	_, err = h.larkSyncAdopt(ctx, p.BotID, p.Key, p.Tier, app, tok)
	return err
}

// larkSyncPollDevice asks about bot's device authorization: still waiting, failed (the authorization under way is
// dropped), or done: the account is bound and the prompt is done.
func (h *Manager) larkSyncPollDevice(ctx context.Context, botID string) (connector.Prompt, error) {
	ua := h.larkSyncUserAuth()
	if ua == nil {
		return connector.Prompt{}, errLarkSyncUnavailable
	}
	st, err := h.larkSyncLoad(ctx)
	if err != nil {
		return connector.Prompt{}, err
	}
	p := st.binding(larkSyncPendingKey(botID))
	if p == nil || p.AuthMode != larkSyncModeDevice {
		return connector.Prompt{}, errLarkSyncState
	}
	l := h.botLang(ctx)
	drop := func(key string) (connector.Prompt, error) {
		_, _ = h.larkSyncChange(ctx, func(st *larkSyncState) error {
			st.bindings = slices.DeleteFunc(st.bindings, func(b larkSyncBinding) bool { return b.Key == p.Key })
			return nil
		})
		return connector.Prompt{Step: stepLarkSyncAuthorize, Error: i18n.T(l, key)}, nil
	}
	if !h.bots.sync.clock().Before(p.AuthExpiresAt) {
		return drop("hub-larksync.auth_expired")
	}
	code, err := h.larkSyncOpen(p.AuthDeviceEnc)
	if err != nil || code == "" {
		return drop("hub-larksync.auth_failed")
	}
	app, err := h.larkSyncApp(ctx, &st, botID)
	if err != nil {
		return connector.Prompt{}, err
	}
	res, err := ua.DevicePoll(ctx, app, code)
	if ctx.Err() != nil {
		return connector.Prompt{}, ctx.Err()
	}
	again := connector.Prompt{Step: stepLarkSyncAuthorize, Interval: p.AuthInterval}
	if err != nil { // the network: asked again on the next poll
		h.logf("lark sync: %s: device poll: %v", botID, err)
		return again, nil
	}
	switch res.Status {
	case larksyncwire.DevicePending:
		return again, nil
	case larksyncwire.DeviceSlowDown:
		interval := p.AuthInterval + 5
		_, _ = h.larkSyncChange(ctx, func(st *larkSyncState) error {
			if b := st.binding(p.Key); b != nil {
				b.AuthInterval = interval
			}
			return nil
		})
		again.Interval = interval
		return again, nil
	case larksyncwire.DeviceDone:
		name, err := h.larkSyncAdopt(ctx, botID, p.Key, p.Tier, app, res.Token)
		if err != nil {
			return connector.Prompt{}, err
		}
		return connector.Prompt{Done: true, Account: name}, nil
	case larksyncwire.DeviceExpired:
		return drop("hub-larksync.auth_expired")
	case larksyncwire.DeviceDenied:
		return drop("hub-larksync.auth_denied")
	default:
		h.logf("lark sync: %s: device flow: %s", botID, res.ErrorText)
		return drop("hub-larksync.auth_failed")
	}
}

// larkSyncAdopt binds the account tok belongs to with botID (pendingKey: the authorization under way, dropped): the
// account is kept (who it is, nothing secret), the binding made or renewed with the sealed pair, healthy, and due to
// run soon. It gives the account's label.
func (h *Manager) larkSyncAdopt(ctx context.Context, botID, pendingKey, tier string, app larksyncwire.App, tok larksyncwire.Token) (string, error) {
	if tok.OpenID == "" || tok.AccessToken == "" || tok.RefreshToken == "" {
		return "", errLarkSyncExchange
	}
	brand := larkSyncBrand(app.Domain)
	accountID := larkSyncAccountID(brand, tok.TenantKey, tok.OpenID)
	key := larkSyncBindingKey(botID, accountID)
	access, err := h.larkSyncSeal(tok.AccessToken)
	if err != nil {
		return "", err
	}
	refresh, err := h.larkSyncSeal(tok.RefreshToken)
	if err != nil {
		return "", err
	}
	// The binding's refresh lock: a run's refresh must not write the old pair over the new one.
	rt := h.larkSyncRuntime(key)
	rt.refreshMu.Lock()
	defer rt.refreshMu.Unlock()
	rt.unsaved = nil
	now := h.bots.sync.clock()
	label := ""
	_, err = h.larkSyncChange(ctx, func(st *larkSyncState) error {
		a := st.account(accountID)
		if a == nil {
			n := len(st.accounts) + 1
			st.accounts = append(st.accounts, larkSyncAccount{AccountID: accountID, Brand: brand, TenantKey: tok.TenantKey, OpenID: tok.OpenID})
			a = &st.accounts[len(st.accounts)-1]
			a.Label = tok.Name
			if a.Label == "" {
				a.Label = i18n.T(h.userLang(), "hub-larksync.account_n", i18n.Args{"n": n})
			}
		}
		a.UnionID, a.Name = tok.UnionID, tok.Name
		label = a.Label
		st.bindings = slices.DeleteFunc(st.bindings, func(b larkSyncBinding) bool { return b.Key == pendingKey })
		b := st.binding(key)
		if b == nil {
			st.bindings = append(st.bindings, larkSyncBinding{Key: key, BotID: botID, AccountID: accountID, Enabled: true})
			b = &st.bindings[len(st.bindings)-1]
		}
		if larksyncwire.ValidTier(tier) {
			b.Tier = tier
		}
		b.AccessTokenEnc, b.RefreshTokenEnc = access, refresh
		b.TokenExpiresAt, b.RefreshExpiresAt = tok.ExpiresAt, tok.RefreshExpiresAt
		b.AuthorizedAt, b.GrantedScopes = now, slices.Clone(tok.Scopes)
		b.Health, b.LastError, b.Failures, b.RemindedDays = larksyncwire.HealthOK, "", 0, nil
		b.NextRunAt = now
		b.NextIMRunAt = now
		return nil
	})
	if err != nil {
		return "", err
	}
	h.auditBot(ctx, botRecord{auditLarkSyncAuthorized, map[string]any{"bot_id": botID, "binding": key, "account_id": accountID}})
	return label, nil
}
