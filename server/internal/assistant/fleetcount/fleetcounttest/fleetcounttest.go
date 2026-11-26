// Package fleetcounttest is fleetcount's values for its tests: places in a caller's list, and a visitor that writes a
// tally's rows down as text.
//
//persona:layer testkit
package fleetcounttest

import (
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/fleetcount"
	"github.com/nwverse/persona/server/internal/domainkit/instant"
	"github.com/nwverse/persona/server/internal/domainkit/slot"
)

// Slot is place i of a test's list.
func Slot(i int) slot.Slot { return slot.NewSlot(i) }

// Rows writes a tally's rows down, one line each in the order they are told: "agent 2 needs_you since 09:01",
// "card 0 held", "agent 1 by card 3 muted", "under 4" (a further card of the row before), "total 2". A time is its
// hour and minute in UTC, "unknown" when it is.
type Rows struct {
	lines []string
	why   whyWord
}

func (r *Rows) VisitAgent(agent slot.Slot, why fleetcount.Why, since instant.Instant) {
	at := "unknown"
	if since.Known() {
		at = time.Unix(0, since.Wire()).UTC().Format("15:04")
	}
	r.add("agent", place(agent), r.word(why), "since", at)
}

func (r *Rows) VisitCard(card slot.Slot, why fleetcount.Why) { r.add("card", place(card), r.word(why)) }

func (r *Rows) VisitAgentByCard(agent, card slot.Slot, why fleetcount.Why) {
	r.add("agent", place(agent), "by card", place(card), r.word(why))
}

func (r *Rows) VisitCardUnder(card slot.Slot) { r.add("under", place(card)) }

func (r *Rows) VisitTotal(total fleetcount.RowCount) { r.add("total", strconv.Itoa(total.Wire())) }

// String is the rows, one per line.
func (r *Rows) String() string { return strings.Join(r.lines, "\n") }

func (r *Rows) add(words ...string) { r.lines = append(r.lines, strings.Join(words, " ")) }

func (r *Rows) word(why fleetcount.Why) string {
	why.Accept(&r.why)
	return r.why.word
}

func place(s slot.Slot) string { return strconv.Itoa(int(s.Wire())) }

// whyWord is a reason as the tests write it.
type whyWord struct{ word string }

func (w *whyWord) VisitNeedsYou() { w.word = "needs_you" }
func (w *whyWord) VisitHeld()     { w.word = "held" }
func (w *whyWord) VisitMuted()    { w.word = "muted" }
func (w *whyWord) VisitQuiet()    { w.word = "quiet" }

// Tally is the rows AgentsNeedsYou makes of agents and cards on a surface, written down by Rows.
func Tally(on fleetcount.Surface, agents []fleetcount.Agent, cards []fleetcount.Card) string {
	var r Rows
	fleetcount.AgentsNeedsYou(on, fleetcount.NewAgents(agents), fleetcount.NewCards(cards)).Accept(&r)
	return r.String()
}
