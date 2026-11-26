package badapp

import (
	"github.com/nwverse/persona/server/internal/assistant/fleetcount"
	"github.com/nwverse/persona/server/internal/domainkit/instant"
	"github.com/nwverse/persona/server/internal/domainkit/slot"
)

// view reads a tally itself.
type view struct{ rows int }

func (x *view) VisitAgent(a slot.Slot, w fleetcount.Why, s instant.Instant) { x.rows++ } // out-visitor-impl
