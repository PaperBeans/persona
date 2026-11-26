package agentview

import "github.com/nwverse/persona/server/internal/domainkit/defect"

// CardCount is how many pending cards stand in a row. The zero CardCount is no cards, a real count.
type CardCount struct{ cardCount int }

// ParseCardCount is n cards; a negative n is a defect, since only the caller's list counts them.
func ParseCardCount(n int) CardCount {
	if n < 0 {
		defect.Raise("agentview.ParseCardCount", "a negative count")
	}
	return CardCount{cardCount: n}
}

// Wire is the count as the wire carries it. Adapters only.
func (c CardCount) Wire() int { return c.cardCount }

// Rank is where a session sorts among the others, lower first (docs/protocol/agent-states.md "Rank"). The zero Rank is
// the first place, a real rank: a counted Needs you row's.
type Rank struct{ rank int }

// Wire is the rank as the wire carries it. Adapters only.
func (r Rank) Wire() int { return r.rank }

// Before says whether r sorts ahead of o.
func (r Rank) Before(o Rank) bool { return r.rank < o.rank }
