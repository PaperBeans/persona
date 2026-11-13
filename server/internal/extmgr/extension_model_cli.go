package extmgr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/i18n"
)

// The extension model's commands (docs/extensions/extensions.md "Turning contributions on and off"), next to extensionUsage's.
const extensionModelUsage = `  contributions <id>                its contributions and their switches
  contribution <id> <point>/<cid> on|off
                                    switch one contribution (a connector's is the connector's switch)`

// extensionModelCommand runs the extension model's commands; done is false for a command that is not one of them.
func extensionModelCommand(ctx context.Context, c *extensionClient, cmd string, positional []string, stdout io.Writer) (done bool, err error) {
	switch cmd {
	case "contributions":
		if len(positional) < 1 {
			return true, errors.New(extensionModelUsage)
		}
		var d ExtensionDetail
		if err := c.do(ctx, "GET", "/api/v1/extensions/"+url.PathEscape(positional[0])+"/-/", nil, &d); err != nil {
			return true, err
		}
		printContributions(c.l(), stdout, d.Contributions)
		return true, nil
	case "contribution":
		if len(positional) < 3 || (positional[2] != "on" && positional[2] != "off") {
			return true, errors.New(extensionModelUsage)
		}
		point, cid, ok := strings.Cut(positional[1], "/")
		if !ok {
			return true, fmt.Errorf("%q is not <point>/<id>", positional[1])
		}
		var d ExtensionDetail
		path := "/api/v1/extensions/" + url.PathEscape(positional[0]) + "/-/contributions/" + url.PathEscape(point) + "/" + url.PathEscape(cid)
		if err := c.do(ctx, "PUT", path, map[string]bool{"enabled": positional[2] == "on"}, &d); err != nil {
			return true, err
		}
		printContributions(c.l(), stdout, d.Contributions)
		return true, nil
	}
	return false, nil
}

// printBundle says what installs with a preview: its dependencies and their state, the previews that come along
// (each with what it may do), and what no market has.
func printBundle(l i18n.Lang, w io.Writer, pv *extension.Preview) {
	for _, d := range pv.Depends {
		state := ""
		switch d.State {
		case extension.DepOK:
			state = i18n.T(l, "extmgr-cli.dep_ok")
		case extension.DepMissing:
			state = i18n.T(l, "extmgr-cli.dep_missing")
		case extension.DepDisabled:
			state = i18n.T(l, "extmgr-cli.dep_disabled")
		case extension.DepOld:
			state = i18n.T(l, "extmgr-cli.dep_old", i18n.Args{"version": d.Installed})
		}
		fmt.Fprintln(w, i18n.T(l, "extmgr-cli.dep", i18n.Args{"id": d.ID, "version": d.Version, "state": state}))
	}
	if len(pv.Bundle) > 0 {
		fmt.Fprintln(w, i18n.T(l, "extmgr-cli.bundle"))
	}
	for _, b := range pv.Bundle {
		key := "extmgr-cli.bundle_install"
		if b.Upgrade {
			key = "extmgr-cli.bundle_upgrade"
		}
		fmt.Fprintln(w, "  "+i18n.T(l, key, i18n.Args{"id": b.Manifest.ID, "version": b.Manifest.Version, "name": b.Manifest.Name}))
		for _, p := range b.Permissions {
			fmt.Fprintln(w, "    · "+i18n.T(l, "extmgr-cli.preview_permission", i18n.Args{"title": p.Title, "detail": p.Detail}))
		}
	}
	for _, u := range pv.Unresolved {
		fmt.Fprintln(w, i18n.T(l, "extmgr-cli.unresolved", i18n.Args{"id": u}))
	}
}

// bundleRefs are a preview's bundle as the install request sends it back.
func bundleRefs(pv *extension.Preview) []bundleRef {
	out := []bundleRef{}
	for _, b := range pv.Bundle {
		out = append(out, bundleRef{PreviewID: b.ID, Digest: b.Digest})
	}
	return out
}

func printContributions(l i18n.Lang, w io.Writer, cs []ContributionView) {
	if len(cs) == 0 {
		fmt.Fprintln(w, i18n.T(l, "extmgr-cli.contributions_empty"))
	}
	for _, c := range cs {
		state := i18n.T(l, "extmgr-cli.switch_off")
		if c.Enabled {
			state = i18n.T(l, "extmgr-cli.switch_on")
		}
		note := ""
		switch {
		case c.Via == "connector":
			note = i18n.T(l, "extmgr-cli.contribution_via_connector")
		case !c.CanDisable:
			note = i18n.T(l, "extmgr-cli.contribution_fixed")
		}
		fmt.Fprintf(w, "%-28s %s  %s%s\n", c.Point+"/"+c.ID, state, c.Title, note)
	}
}
