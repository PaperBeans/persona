package fleetcountadapt

import (
	"time"

	"github.com/nwverse/persona/server/internal/assistant/fleetcount"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/domainkit/instant"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
	"github.com/nwverse/persona/server/internal/domainkit/slot"
)

// NeedsYou is the Needs you list for a surface, in the wire's rows: the sessions and cards fleetcount decides on, each
// at its place in the caller's lists. A session or card that does not parse is left out.
func NeedsYou(on fleetcount.Surface, sessions []assistantwire.Session, cards []assistantwire.Interaction) assistantwire.NeedsYou {
	agents := make([]fleetcount.Agent, 0, len(sessions))
	for i := range sessions {
		if a, f := outcome.Unpack(Agent(sessions[i], slot.NewSlot(i))); f == nil {
			agents = append(agents, a)
		}
	}
	found := make([]fleetcount.Card, 0, len(cards))
	for i := range cards {
		if c, f := outcome.Unpack(Card(cards[i], slot.NewSlot(i))); f == nil {
			found = append(found, c)
		}
	}
	r := renderer{sessions: sessions, cards: cards}
	fleetcount.AgentsNeedsYou(on, fleetcount.NewAgents(agents), fleetcount.NewCards(found)).Accept(&r)
	return r.list
}

// renderer fills the wire's rows from a tally, indexing its own sessions and cards by the slots NeedsYou minted. The
// visitor it reads reasons with is a field, so the nested Accept calls do not allocate.
type renderer struct {
	sessions []assistantwire.Session
	cards    []assistantwire.Interaction
	list     assistantwire.NeedsYou
	why      whyWord
}

func (r *renderer) VisitAgent(agent slot.Slot, why fleetcount.Why, since instant.Instant) {
	s := &r.sessions[agent.Wire()]
	r.list.Rows = append(r.list.Rows, assistantwire.NeedsYouRow{Machine: s.MachineID, Session: s.SessionID,
		Why: r.word(why), Since: time.Unix(0, since.Wire()).UTC()})
}

func (r *renderer) VisitCard(card slot.Slot, why fleetcount.Why) {
	r.list.Rows = append(r.list.Rows, assistantwire.NeedsYouRow{Card: r.cards[card.Wire()].ID, Why: r.word(why)})
}

func (r *renderer) VisitAgentByCard(agent, card slot.Slot, why fleetcount.Why) {
	s := &r.sessions[agent.Wire()]
	r.list.Rows = append(r.list.Rows, assistantwire.NeedsYouRow{Machine: s.MachineID, Session: s.SessionID,
		Card: r.cards[card.Wire()].ID, Why: r.word(why)})
}

func (r *renderer) VisitTotal(total fleetcount.RowCount) { r.list.Total = total.Wire() }

func (r *renderer) word(why fleetcount.Why) string {
	why.Accept(&r.why)
	return r.why.word
}

// whyWord is a reason as the wire spells it.
type whyWord struct{ word string }

func (w *whyWord) VisitNeedsYou() { w.word = "needs_you" }
func (w *whyWord) VisitHeld()     { w.word = "held" }
