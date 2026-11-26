package larkauth

import (
	"cmp"
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// The device flow (RFC 8628) authorizes an app whose redirect URL could not be set (no application:application:patch):
// the owner opens a page, or scans its QR code, and confirms; the hub polls the token endpoint meanwhile. It needs no
// page to return to. The platform documents it only for its own CLI, so both the device endpoint (devicePath) and the
// grant on the token endpoint are unverified: any failure here sends the caller to its next fallback, the owner
// adding the redirect URL by hand.

const deviceGrant = "urn:ietf:params:oauth:grant-type:device_code"

// DeviceBegin starts a device authorization of app for scopes.
func (u *UserAuth) DeviceBegin(ctx context.Context, app larksyncwire.App, scopes []string) (larksyncwire.DeviceChallenge, error) {
	form := url.Values{"client_id": {app.AppID}, "scope": {joinScopes(scopes)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.accountsBase(app)+devicePath, strings.NewReader(form.Encode()))
	if err != nil {
		return larksyncwire.DeviceChallenge{}, &larksyncwire.Error{Kind: larksyncwire.ErrKindOther, Op: "device", Msg: err.Error()}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(app.AppID, app.AppSecret)
	var out struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
		Code                    int    `json:"code"`
		Msg                     string `json:"msg"`
		Error                   string `json:"error"`
		ErrorDescription        string `json:"error_description"`
	}
	status, h, err := u.do(req, "device", &out)
	if err != nil {
		return larksyncwire.DeviceChallenge{}, err
	}
	page := strings.TrimSpace(out.VerificationURIComplete)
	if page == "" && out.VerificationURI != "" && out.UserCode != "" {
		page = strings.TrimRight(strings.TrimSpace(out.VerificationURI), "/") + "?user_code=" + url.QueryEscape(out.UserCode)
	}
	if status/100 != 2 || out.Code != 0 || out.Error != "" || strings.TrimSpace(out.DeviceCode) == "" || page == "" {
		e := statusError("device", status, h, out.Code)
		e.Msg = cmp.Or(strings.Join(nonEmpty(out.Error, out.ErrorDescription, out.Msg), " "), "no device code or page in the answer")
		if e.Kind == larksyncwire.ErrKindOther && out.Error == "invalid_scope" {
			e.Kind = larksyncwire.ErrKindScope
		}
		return larksyncwire.DeviceChallenge{}, e
	}
	return larksyncwire.DeviceChallenge{
		DeviceCode: strings.TrimSpace(out.DeviceCode),
		URL:        page,
		UserCode:   out.UserCode,
		ExpiresIn:  out.ExpiresIn,
		Interval:   out.Interval,
	}, nil
}

// DevicePoll asks once whether the device authorization deviceCode was confirmed. Waiting, slowing down, a code that
// ran out and a refusal are statuses, not errors; err is only a call that got no answer or a token whose owner could
// not be read.
func (u *UserAuth) DevicePoll(ctx context.Context, app larksyncwire.App, deviceCode string) (larksyncwire.DevicePoll, error) {
	ans, status, h, err := u.postToken(ctx, app, map[string]string{
		"grant_type":    deviceGrant,
		"device_code":   deviceCode,
		"client_id":     app.AppID,
		"client_secret": app.AppSecret,
	})
	if err != nil {
		return larksyncwire.DevicePoll{}, err
	}
	switch ans.Error {
	case "authorization_pending":
		return larksyncwire.DevicePoll{Status: larksyncwire.DevicePending}, nil
	case "slow_down":
		return larksyncwire.DevicePoll{Status: larksyncwire.DeviceSlowDown}, nil
	case "expired_token":
		return larksyncwire.DevicePoll{Status: larksyncwire.DeviceExpired}, nil
	case "access_denied":
		return larksyncwire.DevicePoll{Status: larksyncwire.DeviceDenied}, nil
	}
	if ans.failed(status) {
		e := tokenError("device poll", status, h, ans)
		if e.Kind == larksyncwire.ErrKindRateLimited || e.Kind == larksyncwire.ErrKindTransient {
			return larksyncwire.DevicePoll{}, e // asked again on the next poll
		}
		return larksyncwire.DevicePoll{Status: larksyncwire.DeviceFailed, ErrorText: cmp.Or(e.Msg, e.Kind)}, nil
	}
	tok := u.tokenFrom(ans, "")
	if err := u.identify(ctx, app, &tok); err != nil {
		return larksyncwire.DevicePoll{}, err
	}
	return larksyncwire.DevicePoll{Status: larksyncwire.DeviceDone, Token: tok}, nil
}
