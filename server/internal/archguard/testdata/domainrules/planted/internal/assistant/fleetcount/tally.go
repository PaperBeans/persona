package fleetcount

import (
	"github.com/nwverse/persona/server/internal/agentref"
	"github.com/nwverse/persona/server/internal/domainkit/defect"
	"github.com/nwverse/persona/server/internal/domainkit/instant"
	"github.com/nwverse/persona/server/internal/domainkit/slot"
)

// rowKind is which of TallyVisitor's calls a row is: a closed set over its own field, as a primitive under the
// package's own name would not be.
type rowKind struct{ rowKind uint8 }

func agentRow() rowKind       { return rowKind{rowKind: 1} }
func cardRow() rowKind        { return rowKind{rowKind: 2} }
func agentByCardRow() rowKind { return rowKind{rowKind: 3} }

// row is one entry of a tally.
type row struct {
	kind  rowKind
	agent slot.Slot
	card  slot.Slot
	why   Why
	since instant.Instant
}

// Tally is the rows that need the owner, in the order the owner should see them. The zero Tally is empty.
type Tally struct{ tally []row }

// TallyVisitor is told every row of a tally, then the total.
type TallyVisitor interface {
	VisitAgent(agent slot.Slot, why Why, since instant.Instant) // an agent that waits with no card
	VisitCard(card slot.Slot, why Why)                          // a card on no session
	VisitAgentByCard(agent, card slot.Slot, why Why)            // an agent its oldest card stands for
	VisitTotal(total RowCount)
}

// Accept tells v every row, then the total.
func (t Tally) Accept(v TallyVisitor) {
	for i := range t.tally {
		r := &t.tally[i]
		switch r.kind {
		case agentRow():
			v.VisitAgent(r.agent, r.why, r.since)
		case cardRow():
			v.VisitCard(r.card, r.why)
		case agentByCardRow():
			v.VisitAgentByCard(r.agent, r.card, r.why)
		}
	}
	v.VisitTotal(RowCount{rowCount: len(t.tally)})
}

// AgentsNeedsYou is the agents and cards that need the owner on a surface: a card stands for the agent that asked
// it, and an agent that waits with no card counts on its own.
func AgentsNeedsYou(on Surface, agents Agents, cards Cards) Tally {
	if on == (Surface{}) {
		defect.Raise("fleetcount.AgentsNeedsYou", "the zero Surface")
	}
	rows := make([]row, 0, len(agents.agents)+len(cards.cards))
	counted := make(map[agentref.Ref]slot.Slot, len(agents.agents))
	for i := range agents.agents {
		if a := &agents.agents[i]; a.Counts(on) {
			counted[a.ref] = a.at
		}
	}
	asked := make(map[agentref.Ref]bool, len(cards.cards))
	for i := range cards.cards {
		c := &cards.cards[i]
		c.asker.Match(func(ref agentref.Ref) {
			if at, ok := counted[ref]; ok && !asked[ref] {
				asked[ref] = true
				rows = append(rows, row{kind: agentByCardRow(), agent: at, card: c.at, why: WhyNeedsYou()})
			}
		}, func() {
			rows = append(rows, row{kind: cardRow(), card: c.at, why: WhyNeedsYou()})
		})
	}
	for i := range agents.agents {
		a := &agents.agents[i]
		if a.Counts(on) && a.waitsAlone() && !asked[a.ref] {
			since := a.updated
			a.since.Match(func(t instant.Instant) { since = t }, func() {})
			rows = append(rows, row{kind: agentRow(), agent: a.at, why: WhyNeedsYou(), since: since})
		}
	}
	return Tally{tally: rows}
}
