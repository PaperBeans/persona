package larkauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// The token endpoint (OAuth v2 on the open platform) answers both ways: the platform's {code, msg} with code 0 for
// success, and an OAuth {error, error_description} with a 4xx status. Both are read on every answer.
// https://open.feishu.cn/document/authentication-management/access-token/get-user-access-token

// Codes of the token endpoint that mean the grant itself is gone: the refresh token expired, was revoked or was
// already used (rotated), or the code is not valid. Authorizing again is the only way on.
var invalidGrantCodes = map[int]bool{20037: true, 20064: true, 20073: true, 20074: true}

// Codes that mean the user token was refused on a call made with it (botcard.TokenGone's, and the user token's own).
var tokenRefusedCodes = map[int]bool{99991661: true, 99991663: true, 99991664: true, 99991668: true, 99991677: true}

// codeRateLimited is the gateway's "too many requests" code, which older endpoints answer with HTTP 400.
const codeRateLimited = 99991400

// answerLimit bounds what is read of any answer: token and user-info answers are small.
const answerLimit = 1 << 20

// userInfoAttempts and userInfoBackoff are how a user-info read is tried again after a transient failure. The token
// it needs was just issued and cannot be asked for again, so a short wait is worth more than a failed authorization.
const userInfoAttempts = 3

var userInfoBackoff = []time.Duration{300 * time.Millisecond, time.Second}

// userInfoMostWait is the longest wait a rate limit may ask for before a user-info read gives up.
const userInfoMostWait = 30 * time.Second

// tokenAnswer is the token endpoint's answer, in either of its forms.
type tokenAnswer struct {
	Code             int    `json:"code"`
	Msg              string `json:"msg"`
	AccessToken      string `json:"access_token"`
	ExpiresIn        int64  `json:"expires_in"`
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresIn int64  `json:"refresh_token_expires_in"`
	Scope            string `json:"scope"`
	TokenType        string `json:"token_type"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// failed reports whether the answer is not a token.
func (a tokenAnswer) failed(status int) bool {
	return status/100 != 2 || a.Code != 0 || a.Error != "" || a.AccessToken == ""
}

// Exchange trades the code the authorization page redirected with for a token, then reads whose it is. The code works
// once; a failure is not retried here.
func (u *UserAuth) Exchange(ctx context.Context, app larksyncwire.App, code, redirectURI, verifier string) (larksyncwire.Token, error) {
	body := map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     app.AppID,
		"client_secret": app.AppSecret,
		"code":          code,
		"redirect_uri":  redirectURI,
	}
	if verifier != "" {
		body["code_verifier"] = verifier
	}
	ans, status, h, err := u.postToken(ctx, app, body)
	if err != nil {
		return larksyncwire.Token{}, err
	}
	if ans.failed(status) {
		return larksyncwire.Token{}, tokenError("exchange", status, h, ans)
	}
	tok := u.tokenFrom(ans, "")
	// Whose token it is decides which account it binds: without it the authorization is no use.
	if err := u.identify(ctx, app, &tok); err != nil {
		return larksyncwire.Token{}, err
	}
	return tok, nil
}

// Refresh trades refreshToken for a new pair. The platform rotates the refresh token, so the old one stops working
// as soon as this answers: the caller persists the pair before anyone refreshes again. An answer without a new
// refresh token keeps the old one. Who the token belongs to is read again when the platform answers; a failed
// read never fails the refresh (the new pair must not be lost over it).
func (u *UserAuth) Refresh(ctx context.Context, app larksyncwire.App, refreshToken string) (larksyncwire.Token, error) {
	ans, status, h, err := u.postToken(ctx, app, map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     app.AppID,
		"client_secret": app.AppSecret,
		"refresh_token": refreshToken,
	})
	if err != nil {
		return larksyncwire.Token{}, err
	}
	if ans.failed(status) {
		return larksyncwire.Token{}, tokenError("refresh", status, h, ans)
	}
	tok := u.tokenFrom(ans, refreshToken)
	_ = u.identify(ctx, app, &tok)
	return tok, nil
}

// tokenFrom is the answer as a Token: expiries from now, a refresh expiry of 0 unknown (the zero time), and
// oldRefresh kept when the answer carries no refresh token.
func (u *UserAuth) tokenFrom(a tokenAnswer, oldRefresh string) larksyncwire.Token {
	now := u.now()
	tok := larksyncwire.Token{
		AccessToken:  a.AccessToken,
		RefreshToken: a.RefreshToken,
		// An answer without expires_in expires at once: the next use refreshes rather than trusting a guess.
		ExpiresAt: now.Add(time.Duration(max(a.ExpiresIn, 0)) * time.Second),
		Scopes:    strings.Fields(a.Scope),
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = oldRefresh
	}
	if a.RefreshExpiresIn > 0 {
		tok.RefreshExpiresAt = now.Add(time.Duration(a.RefreshExpiresIn) * time.Second)
	}
	return tok
}

// postToken posts body to the token endpoint and reads the answer whatever its status. err is set only when there is
// no answer to read (the network, or a body that is not JSON), already an *larksyncwire.Error.
func (u *UserAuth) postToken(ctx context.Context, app larksyncwire.App, body map[string]string) (tokenAnswer, int, http.Header, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return tokenAnswer{}, 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.openBase(app)+tokenPath, bytes.NewReader(payload))
	if err != nil {
		return tokenAnswer{}, 0, nil, &larksyncwire.Error{Kind: larksyncwire.ErrKindOther, Op: "token", Msg: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	var ans tokenAnswer
	status, h, err := u.do(req, "token", &ans)
	return ans, status, h, err
}

// do sends req and decodes its JSON answer into out, whatever the status. A transport failure, an answer that is not
// JSON with a 429 or 5xx status, or a context that ended are *larksyncwire.Error of the matching kind.
func (u *UserAuth) do(req *http.Request, op string, out any) (int, http.Header, error) {
	resp, err := u.http.Do(req)
	if err != nil {
		if ctxErr := req.Context().Err(); ctxErr != nil {
			return 0, nil, ctxErr
		}
		return 0, nil, &larksyncwire.Error{Kind: larksyncwire.ErrKindTransient, Op: op, Msg: err.Error()}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, answerLimit))
	if err != nil {
		return resp.StatusCode, resp.Header, &larksyncwire.Error{Kind: larksyncwire.ErrKindTransient, Op: op, Msg: err.Error()}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		e := statusError(op, resp.StatusCode, resp.Header, 0)
		e.Msg = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw[:min(len(raw), 200)])))
		return resp.StatusCode, resp.Header, e
	}
	return resp.StatusCode, resp.Header, nil
}

// tokenError is a failed token answer as an *larksyncwire.Error.
func tokenError(op string, status int, h http.Header, a tokenAnswer) *larksyncwire.Error {
	e := statusError(op, status, h, a.Code)
	e.Code = a.Code
	e.Msg = strings.TrimSpace(strings.Join(nonEmpty(a.Error, a.ErrorDescription, a.Msg), " "))
	if e.Kind == larksyncwire.ErrKindRateLimited || e.Kind == larksyncwire.ErrKindTransient {
		return e
	}
	switch {
	case a.Error == "invalid_grant" || invalidGrantCodes[a.Code]:
		e.Kind = larksyncwire.ErrKindInvalidGrant
	case a.Error == "invalid_scope":
		e.Kind = larksyncwire.ErrKindScope
	case a.Code == 0 && a.Error == "" && a.AccessToken == "" && status/100 == 2:
		e.Msg = "no access token in the answer"
	}
	return e
}

// statusError classifies an answer by its status and the platform's code alone: a rate limit (with the wait it asked
// for), a server failure, or another failure the caller may narrow down.
func statusError(op string, status int, h http.Header, code int) *larksyncwire.Error {
	e := &larksyncwire.Error{Kind: larksyncwire.ErrKindOther, Op: op, Code: code}
	switch {
	case status == http.StatusTooManyRequests || code == codeRateLimited:
		e.Kind = larksyncwire.ErrKindRateLimited
		e.RetryAfter = retryAfter(h)
	case status >= 500:
		e.Kind = larksyncwire.ErrKindTransient
	}
	return e
}

// retryAfter is the wait a throttled answer asks for: the gateway's x-ogw-ratelimit-reset (seconds until its window
// resets) or a standard Retry-After in seconds, whichever is longer; 0 when neither says.
func retryAfter(h http.Header) time.Duration {
	var after time.Duration
	for _, k := range []string{"Retry-After", "X-Ogw-Ratelimit-Reset"} {
		if secs, err := strconv.Atoi(strings.TrimSpace(h.Get(k))); err == nil && secs > 0 {
			after = max(after, time.Duration(secs)*time.Second)
		}
	}
	return after
}

func nonEmpty(v ...string) []string {
	out := v[:0:0]
	for _, s := range v {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// identify reads whose token tok is (user_info) into it. A transient failure or a rate limit is tried again after a
// short wait; a refused token is an ErrKindAuth.
func (u *UserAuth) identify(ctx context.Context, app larksyncwire.App, tok *larksyncwire.Token) error {
	var last error
	for attempt := range userInfoAttempts {
		if attempt > 0 {
			wait := userInfoBackoff[min(attempt-1, len(userInfoBackoff)-1)]
			var le *larksyncwire.Error
			if errors.As(last, &le) && le.RetryAfter > 0 {
				if le.RetryAfter > userInfoMostWait {
					return last
				}
				wait = max(wait, le.RetryAfter)
			}
			if err := u.sleep(ctx, wait); err != nil {
				return err
			}
		}
		err := u.userInfo(ctx, app, tok)
		if err == nil {
			return nil
		}
		last = err
		var le *larksyncwire.Error
		if !errors.As(err, &le) || (le.Kind != larksyncwire.ErrKindTransient && le.Kind != larksyncwire.ErrKindRateLimited) {
			return err
		}
	}
	return last
}

// userInfo is one read of GET /open-apis/authen/v1/user_info with tok's access token.
func (u *UserAuth) userInfo(ctx context.Context, app larksyncwire.App, tok *larksyncwire.Token) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.openBase(app)+userInfoPath, nil)
	if err != nil {
		return &larksyncwire.Error{Kind: larksyncwire.ErrKindOther, Op: "user info", Msg: err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			OpenID    string `json:"open_id"`
			UnionID   string `json:"union_id"`
			TenantKey string `json:"tenant_key"`
			Name      string `json:"name"`
		} `json:"data"`
	}
	status, h, err := u.do(req, "user info", &out)
	if err != nil {
		return err
	}
	if status/100 != 2 || out.Code != 0 || out.Data.OpenID == "" {
		e := statusError("user info", status, h, out.Code)
		e.Msg = strings.TrimSpace(out.Msg)
		switch {
		case e.Kind != larksyncwire.ErrKindOther:
		case status == http.StatusUnauthorized || tokenRefusedCodes[out.Code]:
			e.Kind = larksyncwire.ErrKindAuth
		case status/100 == 2 && out.Code == 0:
			e.Msg = "no open id in the answer"
		}
		return e
	}
	tok.OpenID = out.Data.OpenID
	tok.UnionID = out.Data.UnionID
	tok.TenantKey = out.Data.TenantKey
	tok.Name = out.Data.Name
	return nil
}

// sleepCtx waits d or until ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
