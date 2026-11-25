package kb

// The writes of minutes.go that delete, retention and recovery need: Core, with or without the recording module (docs/plans/plan-kb-recording-port.md,
// section 1; docs/architecture/core-and-modules.md "Recording").

import (
	"time"
)

const (
	minutesTries         = 3      // unusable answers for one revision before the minutes are failed
	minutesMinCalendar   = 5 * 60 // seconds: shorter recordings are not meetings just because a calendar item overlaps
	minutesCalendarAhead = 15 * time.Minute
	// minutesMatchWait: a recording no calendar item matched is looked at again until this long after it ended, the
	// calendar items being collected every half hour (a meeting's video call is listed once it has been held).
	minutesMatchWait = time.Hour
	// minutesImportWait: a matched recording with no imported meeting yet is looked at again until this long after it
	// ended, for one a connected app imports later (#8872).
	minutesImportWait  = 24 * time.Hour
	minutesOneCall     = 16000 // runes of transcript read in one call; longer ones go in chunks
	minutesChunk       = 12000
	minutesMaxChunks   = 40
	minutesCallTimeout = 6 * time.Minute  // a call's at the first try, twice a summary's (minutes_timeout.go)
	minutesJobTimeout  = 45 * time.Minute // all the calls of one recording: the knowledge-base round waits for them
	minutesMaxItems    = 100
	minutesTextRunes   = 400  // an item's text at most, the "…" of a cut included
	minutesLineRunes   = 4000 // a transcript line longer than this is cut (one segment, not the whole meeting)
	// What the merging call reads of all the parts' notes: per section, at most this many items over all parts (an
	// equal share each, at least 2), each at most minutesNoteRunes.
	minutesMergeItems = 60
	minutesNoteRunes  = 160
)

// isMeeting: the recording gets minutes. A day recording never does (it gets a recap, day_recap.go).
func isMeeting(r *Recording) bool {
	if r.Kind == RecordingDay {
		return false
	}
	return r.Kind == RecordingMeeting || r.MinutesAsked || r.Meeting != nil && r.Duration >= minutesMinCalendar
}

// planMinutes sets MinutesState from the rest: pending until minutes of the current revision are written, failed after
// minutesTries unusable answers for it or minutesTimeoutTries tries that ran out of time; minutes of an older revision
// keep showing meanwhile. A meeting that completed with no text gets none ("", docs/assistant/meeting-minutes.md
// "Attendance fields"): an attended one is uploaded before anything was said, and may end so.
func planMinutes(r *Recording) {
	switch {
	case !isMeeting(r):
		r.MinutesState = "" // minutes made while it was one stay readable; nothing more is made
	case r.Trimmed():
		r.MinutesState = MinutesReady // the transcript is gone: the minutes kept are all there will be
	case r.Complete && r.Chars == 0:
		r.MinutesState = ""
	case r.MinutesRev > 0 && r.MinutesRev >= r.Revision:
		r.MinutesState = MinutesReady
	case r.MinutesFailRev == r.Revision && (r.MinutesFails >= minutesTries || r.MinutesTimeouts >= minutesTimeoutTries):
		r.MinutesState = MinutesFailed
	default:
		r.MinutesState = MinutesPending
	}
}
