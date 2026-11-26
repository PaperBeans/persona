package kb

// The writes of minutes.go that delete, retention and recovery need: Core, with or without the recording module (docs/plans/plan-kb-recording-port.md,
// section 1; docs/architecture/core-and-modules.md "Recording").

import (
	"github.com/nwverse/persona/server/internal/recordingwire"
)

// minutesLineRunes: a transcript line, or a photo's text, longer than this is cut (one segment, not the whole meeting).
const minutesLineRunes = recordingwire.MinutesLineRunes

// isMeeting: the recording gets minutes. A day recording never does (it gets a recap, day_recap.go).
func isMeeting(r *Recording) bool { return recordingwire.GetsMinutes(r) }

// planMinutes sets MinutesState from the rest: pending until minutes of the current revision are written, failed after
// minutesTries unusable answers for it or minutesTimeoutTries tries that ran out of time; minutes of an older revision
// keep showing meanwhile. A meeting that completed with no text gets none ("", docs/assistant/meeting-minutes.md
// "Attendance fields"): an attended one is uploaded before anything was said, and may end so.
func planMinutes(r *Recording) { recordingwire.PlanMinutes(r) }
