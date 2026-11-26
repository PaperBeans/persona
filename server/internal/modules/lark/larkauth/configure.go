package larkauth

import (
	"context"

	"github.com/nwverse/persona/server/internal/botcard"
	"github.com/nwverse/persona/server/internal/larksyncwire"
	"github.com/nwverse/persona/server/internal/modules/lark/feishubot"
)

// consoleSecurityPage is the app console's page with the redirect URLs, under the app's own page.
const consoleSecurityPage = "/safe"

// ConfigureRedirect adds redirectURI and scopes (as user scopes) to app and publishes a version, through the app's
// own config API (feishubot.Client.ConfigureUserAccess).
//
// MissingPatch is set when nothing could be changed and the owner's help is the way on: the app lacks
// application:application:patch, or it is on a private deployment whose console is not ours to configure. Either way
// the caller tries the device flow, then shows ConsoleURL (the two clouds only) for the owner to add the redirect URL.
// Other failures are in Detail with nothing set.
func (u *UserAuth) ConfigureRedirect(ctx context.Context, app larksyncwire.App, redirectURI string, scopes []string) larksyncwire.RedirectReport {
	var out larksyncwire.RedirectReport
	if page := botcard.AppConsoleURL(app.Domain, app.AppID); page != "" {
		out.ConsoleURL = page + consoleSecurityPage
	}
	// The console follows the app's cloud even when the calls go elsewhere (u.open, tests).
	c := feishubot.New(app.AppID, app.AppSecret, u.openBase(app)).WithHTTP(u.http).WithConsole(botcard.ConsoleURL(app.Domain))
	rep := c.ConfigureUserAccess(ctx, redirectURI, scopes)
	switch {
	case rep.Skipped && rep.Issue == botcard.IssueNoApp:
		out.Detail = "no app to configure"
	case rep.Skipped:
		out.MissingPatch = true
		out.Detail = "a private deployment: its console is not configured from here"
	case rep.Issue == botcard.IssuePatchScope:
		out.MissingPatch = true
		out.Detail = rep.Detail
	default:
		out.Configured = rep.Configured
		out.Published = rep.Published
		out.Awaiting = rep.Issue == botcard.IssueUnderReview
		if rep.Issue != botcard.IssueNone && !out.Awaiting {
			out.Detail = string(rep.Issue)
			if rep.Detail != "" {
				out.Detail += ": " + rep.Detail
			}
		}
	}
	return out
}
