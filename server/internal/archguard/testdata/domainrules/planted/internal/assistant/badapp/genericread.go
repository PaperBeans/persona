package badapp

import (
	"github.com/nwverse/persona/server/internal/assistant/fleetcount"
	"github.com/nwverse/persona/server/internal/domainkit/instant"
	"github.com/nwverse/persona/server/internal/domainkit/slot"
)

// busy decides on a tally's total itself, through genericvisitor.go's peek.
func busy(t fleetcount.Tally, quiet fleetcount.RowCount) bool {
	var total fleetcount.RowCount
	t.Accept(peek[slot.Slot, fleetcount.Why, instant.Instant, fleetcount.RowCount]{total: &total})
	return total != quiet
}
