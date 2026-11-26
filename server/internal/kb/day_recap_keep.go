package kb

// The writes of day_recap.go that delete, retention and recovery need: Core, with or without the recording module (docs/plans/plan-kb-recording-port.md,
// section 1; docs/architecture/core-and-modules.md "Recording").

import (
	"github.com/nwverse/persona/server/internal/recordingwire"
)

// RecapTarget is declared in recordingwire (docs/plans/plan-kb-recording-port.md).
type RecapTarget = recordingwire.RecapTarget

// planRecap sets a day recording's RecapState from the rest: pending while a recap is due, failed once the model could
// not write it, ready once one was written and none is due, absent before any. A trimmed one is due no more: its
// recap kept is all there will be.
func planRecap(r *Recording) { recordingwire.PlanRecap(r) }
