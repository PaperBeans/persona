//go:build windows

package linktray

import (
	"errors"
	"strings"

	"golang.org/x/sys/windows/registry"

	"github.com/nwverse/persona/server/internal/link"
)

// Where Windows keeps what starts at sign-in and which program opens pairing links: the user's own registry, no
// administrator. `persona-link install` sets both, the panel's switch turns the first on and off, `uninstall` removes
// them.

const (
	runKey      = `Software\Microsoft\Windows\CurrentVersion\Run`
	approvedKey = `Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run`
	runValue    = "Persona Link"
)

// TrayCommand is the command line that starts the tray from exe.
func TrayCommand(exe string) string { return `"` + exe + `" tray` }

// Autostart says whether the tray starts at sign-in: our Run entry is there and the user has not turned it off in
// Settings › Apps › Startup (which keeps the entry and marks it in StartupApproved).
func Autostart() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	if _, _, err := k.GetStringValue(runValue); err != nil {
		return false
	}
	a, err := registry.OpenKey(registry.CURRENT_USER, approvedKey, registry.QUERY_VALUE)
	if err != nil {
		return true
	}
	defer a.Close()
	b, _, err := a.GetBinaryValue(runValue)
	return err != nil || len(b) == 0 || b[0]&1 == 0 // an odd first byte is "disabled"
}

// SetAutostart starts the tray at sign-in with exe, or stops doing so. Turning it on also clears a "disabled" the
// user set in Settings, since this switch is the user saying so too.
func SetAutostart(exe string, on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		if err := k.DeleteValue(runValue); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := k.SetStringValue(runValue, TrayCommand(exe)); err != nil {
		return err
	}
	if a, err := registry.OpenKey(registry.CURRENT_USER, approvedKey, registry.SET_VALUE); err == nil {
		defer a.Close()
		_ = a.DeleteValue(runValue)
	}
	return nil
}

// linkSchemes are the link schemes the pairing window takes (the apps' and the hub's pairing links).
var linkSchemes = link.PairSchemes()

// RegisterLinks makes pairing links open the pairing window, unless another program already
// handles a scheme: the phone link of a desktop app must keep working.
func RegisterLinks(exe string) error {
	var errs []error
	for _, scheme := range linkSchemes {
		if owner := linkHandler(scheme); owner != "" && !ours(owner) {
			continue
		}
		base := `Software\Classes\` + scheme
		k, _, err := registry.CreateKey(registry.CURRENT_USER, base, registry.SET_VALUE)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		_ = k.SetStringValue("", "URL:Persona Link")
		_ = k.SetStringValue("URL Protocol", "")
		k.Close()
		c, _, err := registry.CreateKey(registry.CURRENT_USER, base+`\shell\open\command`, registry.SET_VALUE)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		errs = append(errs, c.SetStringValue("", TrayCommand(exe)+` --open "%1"`))
		c.Close()
	}
	return errors.Join(errs...)
}

// UnregisterLinks takes back the schemes RegisterLinks took, and only those.
func UnregisterLinks() {
	for _, scheme := range linkSchemes {
		if !ours(linkHandler(scheme)) {
			continue
		}
		base := `Software\Classes\` + scheme
		for _, sub := range []string{`\shell\open\command`, `\shell\open`, `\shell`, ``} {
			_ = registry.DeleteKey(registry.CURRENT_USER, base+sub)
		}
	}
}

// linkHandler is the command that opens scheme:// links for this user, "" when none does.
func linkHandler(scheme string) string {
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		k, err := registry.OpenKey(root, `Software\Classes\`+scheme+`\shell\open\command`, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		v, _, err := k.GetStringValue("")
		k.Close()
		if err == nil && v != "" {
			return v
		}
	}
	return ""
}

// ours: the command is one of the connector's programs, under its current or its baseline name.
func ours(command string) bool {
	return strings.Contains(strings.ToLower(command), "-link") && strings.Contains(strings.ToLower(command), " tray --open ")
}
