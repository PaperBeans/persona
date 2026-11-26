// Package fleetview is the text the fleet's surfaces write for an agent and a card: its title and place, what a card
// asks, and its time, in the lengths the Live Activity and Persona Link have room for. The needs-you rows
// (fleetcount/fleetcountadapt) and package assistant's working and finished rows write them the same way.
//
//persona:layer library
package fleetview

import (
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/textfmt"
)

// MaxItems is how many agents the Live Activity lists; DetailMax, TitleMax and PlaceMax are how long an item's line,
// title and place get there, in runes, before they are clipped (with an ellipsis after), as the lock screen has room
// for them.
const (
	MaxItems  = 3
	DetailMax = 60
	TitleMax  = 24
	PlaceMax  = 32
)

// LinkTextMax and LinkDetailMax are how long a Persona Link item's title and place, and its detail, get in runes, the
// ellipsis that marks a cut included.
const (
	LinkTextMax   = 80
	LinkDetailMax = 60
)

// AgentTitle is an agent's title (its session's, or its project's folder when it has none) in one line of at most n
// runes.
func AgentTitle(title string, n int) string { return textfmt.Clip(textfmt.OneLine(title), n) }

// AgentPlace is "machine · space › tab", the subtitle pushes use too, in at most n runes.
func AgentPlace(machine string, sess *proto.Session, n int) string {
	place := machine
	if sess.SpaceName != "" {
		place += " · " + sess.SpaceName
		if sess.SlotName != "" && sess.SlotName != fmt.Sprint(sess.SlotIndex) {
			place += " › " + sess.SlotName
		}
	}
	return textfmt.Clip(textfmt.OneLine(place), n)
}

// InteractionDetail is what a card asks, for its item's line: a permission's summary (else its tool), a plan's title,
// a question's first question; a terminal dialog's card says it waits there.
func InteractionDetail(l i18n.Lang, ix proto.Interaction) string {
	switch ix.Kind {
	case proto.IxPermission:
		if ix.Summary != "" {
			return ix.Summary
		}
		return textfmt.ToolName(ix)
	case proto.IxPlan:
		return textfmt.PlanTitle(ix.Plan)
	case proto.IxQuestion:
		if len(ix.Questions) > 0 {
			return ix.Questions[0].Question
		}
		return ix.Summary
	}
	return i18n.T(l, "hub-liveactivity.screen")
}

// UnixSeconds is t in seconds since 1970, 0 for the zero time: the Live Activity's times.
func UnixSeconds(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return float64(t.Unix())
}

// Fit is s in at most n runes, the ellipsis that marks a cut included (textfmt.Clip adds its ellipsis after n runes).
func Fit(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return textfmt.Clip(s, n-1)
}
