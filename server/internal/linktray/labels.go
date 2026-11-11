package linktray

import (
	"errors"
	"strings"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/link"
)

// Labels are the pages' fixed words, in one language, keyed as the pages use them. Each key is spelled out here
// (not built) so the catalog checks see every one in use.
func Labels(l i18n.Lang) map[string]string {
	keys := []string{
		"link-tray.mode_active",
		"link-tray.mode_passive",
		"link-tray.pair_title_new",
		"link-tray.pair_title_again",
		"link-tray.active_intro",
		"link-tray.machine_placeholder",
		"link-tray.advanced",
		"link-tray.pair_button",
		"link-tray.pairing",
		"link-tray.switch_button",
		"link-tray.addresses_title",
		"link-tray.addresses_hint",
		"link-tray.addresses_trust",
		"link-tray.addresses_empty",
		"link-tray.address_in_use",
		"link-tray.address_remove",
		"link-tray.address_add_placeholder",
		"link-tray.address_add",
		"link-tray.passive_title",
		"link-tray.passive_intro",
		"link-tray.listen_label",
		"link-tray.generate",
		"link-tray.generating",
		"link-tray.code_intro",
		"link-tray.copy",
		"link-tray.copied",
		"link-tray.firewall_hint",
		"link-tray.firewall_proxy",
		"link-tray.close",
		"link-tray.pair_window_title",
		"link-tray.rename_label",
		"link-tray.rename_hint",
		"link-tray.rename_save",
		"link-tray.rename_saving",
		"link-tray.rename_cancel",
		"link-tray.ssh_title",
		"link-tray.ssh_intro",
		"link-tray.ssh_server_label",
		"link-tray.ssh_server_placeholder",
		"link-tray.ssh_target_label",
		"link-tray.ssh_target_placeholder",
		"link-tray.ssh_always_label",
		"link-tray.ssh_always_hint",
		"link-tray.ssh_test",
		"link-tray.ssh_testing",
		"link-tray.ssh_save",
		"link-tray.ssh_saving",
		"link-tray.ssh_remove",
	}
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		out[strings.TrimPrefix(k, "link-tray.")] = i18n.T(l, k)
	}
	return out
}

// CodeAddrs says which of this computer's addresses the passive pairing link gives the hub.
func CodeAddrs(l i18n.Lang, addrs []string) string {
	return i18n.T(l, "link-tray.code_addrs", i18n.Args{"addrs": strings.Join(addrs, i18n.T(l, "link-tray.list_separator"))})
}

// ErrorText is what the tray says about an error from its Host, in language l: its own words for the failures it
// knows, with the raw error after them, else the error as it is.
func ErrorText(l i18n.Lang, err error) string {
	var pe *PairError
	var re *link.RenameError
	var se *link.SSHError
	switch {
	case errors.As(err, &se):
		return se.Text(l)
	case errors.As(err, &re):
		return re.Text(l)
	case errors.Is(err, ErrNoService):
		return i18n.T(l, "link-tray.error_no_service")
	case errors.Is(err, ErrInstallInTerminal):
		return i18n.T(l, "link-tray.error_install_in_terminal")
	case errors.As(err, &pe) && pe.Rejected:
		return i18n.T(l, "link-tray.error_pair_rejected", i18n.Args{"detail": pe.Err.Error()})
	case errors.As(err, &pe):
		hosts := strings.Join(pe.Hosts, i18n.T(l, "link-tray.list_separator"))
		return i18n.T(l, "link-tray.error_pair_unreachable", i18n.Args{"hosts": hosts, "detail": pe.Err.Error()})
	}
	return err.Error()
}

// SSHTestOKText says the SSH tunnel reached the hub at target.
func SSHTestOKText(l i18n.Lang, target string) string {
	return i18n.T(l, "link-tray.ssh_test_ok", i18n.Args{"target": target})
}

// SSHSavedText says the SSH tunnel was saved and the link is reconnecting.
func SSHSavedText(l i18n.Lang) string {
	return i18n.T(l, "link-tray.ssh_saved")
}

// SSHRemovedText says the SSH tunnel was removed.
func SSHRemovedText(l i18n.Lang) string {
	return i18n.T(l, "link-tray.ssh_removed")
}

// FirewallCommand is the PowerShell line that opens the listening port to the hub only. Not translated: it is a
// command, and <hub-address> is what the hint tells the user to replace.
func FirewallCommand(port string) string {
	return "New-NetFirewallRule -DisplayName 'Persona Link' -Direction Inbound -Protocol TCP -LocalPort " + port +
		" -RemoteAddress <hub-address> -Action Allow"
}

// Notification is a balloon the tray shows once when the link needs the user.
type Notification struct {
	Title string
	Body  string
}

// AwaitingNotification: the hub waits for the user to confirm this computer in the app.
func AwaitingNotification(l i18n.Lang) Notification {
	return Notification{Title: i18n.T(l, "link-tray.notify_awaiting_title"), Body: i18n.T(l, "link-tray.notify_awaiting_body")}
}

// OfflineNotification: the link has been down for a while.
func OfflineNotification(l i18n.Lang) Notification {
	return Notification{Title: i18n.T(l, "link-tray.notify_offline_title"), Body: i18n.T(l, "link-tray.notify_offline_body")}
}

// MenuLabels are the right-click menu's words (the panel's actions plus opening the panel).
func MenuLabels(l i18n.Lang) map[string]string {
	return map[string]string{
		ActPanel:     i18n.T(l, "link-tray.action_panel"),
		ActReconnect: i18n.T(l, "link-tray.action_reconnect"),
		ActPair:      i18n.T(l, "link-tray.action_pair_change"),
		ActRename:    i18n.T(l, "link-tray.action_rename"),
		ActAutostart: i18n.T(l, "link-tray.action_autostart"),
		ActQuit:      i18n.T(l, "link-tray.action_quit"),
	}
}
