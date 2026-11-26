package feishubot

import (
	"context"
	"net/http"
	"strings"
)

// Reading as the owner (docs/extensions/bots.md "Reading as you: daily sync"): the user OAuth that lets the app read
// as the person who authorizes it needs two things on the app first, the user scopes and the page the authorization
// returns to (a redirect URL in the app's security settings). Both go on through the same config API as
// AutoConfigure, with the same one manual prerequisite (application:application:patch), and a publish submits them.

// WithConsole sets the developer console this client's app lives in, when the caller knows it better than the
// domain does: a client pointed at another address (a test server, a proxy) for an app of one of the two clouds.
// An empty console leaves the one the domain says.
func (c *Client) WithConsole(console string) *Client {
	c.console = strings.TrimRight(console, "/")
	return c
}

// ConfigureUserAccess adds userScopes as user scopes and redirectURI as a redirect URL to the app, then submits a
// publish so they take effect. It never removes anything the app has.
//
// The report follows AutoConfigure's, with one difference: a version already under review stays IssueUnderReview
// (with Published set), because the scopes asked for now only work once an admin approves it and the caller shows
// that as awaiting approval. IssuePatchScope means nothing could be changed: the caller falls back to an
// authorization that needs no redirect URL, or to the owner adding it by hand.
func (c *Client) ConfigureUserAccess(ctx context.Context, redirectURI string, userScopes []string) ConfigureReport {
	if c.appID == "" {
		return ConfigureReport{Skipped: true, Issue: IssueNoApp}
	}
	if c.consoleURL() == "" {
		return ConfigureReport{Skipped: true} // a private deployment: its console is not ours to configure
	}

	var rep ConfigureReport
	body := map[string]any{
		"scope": map[string]any{
			"add_scopes": tokenScopesPayload(userScopes, "user"),
		},
	}
	if redirectURI = strings.TrimSpace(redirectURI); redirectURI != "" {
		body["security"] = map[string]any{
			"add": map[string]any{"redirect_urls": []string{redirectURI}},
		}
	}
	if err := c.call(ctx, "configure", http.MethodPatch, "/open-apis/application/v7/applications/"+c.appID+"/config", body, nil); err != nil {
		rep.Issue, rep.Detail = configureIssue(err)
		return rep
	}
	rep.Configured = true

	pub := map[string]any{"remark": "Persona sync", "changelog": "User permissions and the authorization page for reading as the owner"}
	var pubOut struct {
		VersionID string `json:"version_id"`
	}
	if err := c.call(ctx, "publish", http.MethodPost, "/open-apis/application/v7/applications/"+c.appID+"/publish", pub, &pubOut); err != nil {
		rep.Issue, rep.Detail = configureIssue(err)
		if rep.Issue == IssueUnderReview {
			rep.Published = true // submitted before: it waits for the admin with the rest
		}
		return rep
	}
	rep.Published = true
	rep.VersionID = pubOut.VersionID
	return rep
}

// tokenScopesPayload is the config API's list of scopes to add for one kind of token ("tenant" or "user").
func tokenScopesPayload(names []string, tokenType string) []map[string]string {
	out := make([]map[string]string, 0, len(names))
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, map[string]string{"scope_name": n, "token_type": tokenType})
		}
	}
	return out
}
