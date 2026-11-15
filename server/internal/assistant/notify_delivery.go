package assistant

import (
	"github.com/nwverse/persona/server/internal/i18n"
)

// Where a notify_user notice went, from the hub's answer (assistantwire.NotifyResult): the tool tells the model so it
// never assumes the user was alerted when nobody was (#8138), and the north star counts the notice as an interruption
// only when a device took it (north_star.go).

// Why no device took a notify_user notice when it was made (the work event's "missed", the readout's notify_missed).
const (
	missedNoDevice = "no_device" // no phone, computer or browser could take it
	missedFiltered = "filtered"  // every device that could turned it off
	missedKept     = "kept"      // only offline devices keep it for their next connection
	missedHeld     = "held"      // low-distraction mode kept it for the briefing, and no connected device took it
	missedHubAway  = "hub_away"  // the hub was away: the manager's outbox keeps it until the hub is back
)

// notifyMissed is why no device took the notice res answers for, "" when one did or the hub did not say (a hub before
// it reported devices: counted as before, an interruption).
func notifyMissed(res NotifyResult) string {
	switch {
	case res.Queued:
		return missedHubAway
	case res.Devices == nil || *res.Devices > 0:
		return ""
	case res.Held:
		return missedHeld
	case res.Kept > 0:
		return missedKept
	case res.Reason == missedFiltered:
		return missedFiltered
	}
	return missedNoDevice
}

// notifyAnswerText is notify_user's result for the model, in the user's language l: sent to how many devices, held for
// the briefing, kept until a device or the hub is back, or received by nothing. A hub that does not report devices
// keeps the old answer.
func notifyAnswerText(l i18n.Lang, res NotifyResult) string {
	switch {
	case res.Queued:
		return i18n.T(l, "assistant-notify.hub_away_notice")
	case res.Held:
		// ln mode kept it off the phone (a non-main conversation's ordinary notice): say so, so the model does not
		// believe the user was alerted.
		return i18n.T(l, "assistant-notify.ln_held_notice")
	case res.Devices == nil:
		return "Notification sent."
	case *res.Devices > 0:
		return i18n.T(l, "assistant-notify.sent_notice", i18n.Args{"count": *res.Devices})
	case res.Kept > 0:
		return i18n.T(l, "assistant-notify.kept_notice", i18n.Args{"count": res.Kept})
	case res.Reason == missedFiltered:
		return i18n.T(l, "assistant-notify.filtered_notice")
	}
	return i18n.T(l, "assistant-notify.no_device_notice")
}
