package writers

import (
	"time"

	"github.com/nwverse/persona/server/internal/recordingwire"
)

// The minutes' limits. Those the knowledge base's planners and readers apply too are recordingwire's.
const (
	minutesTries         = recordingwire.MinutesTries // unusable answers for one revision before the minutes are failed
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
	minutesTextRunes   = 400                            // an item's text at most, the "…" of a cut included
	minutesLineRunes   = recordingwire.MinutesLineRunes // a transcript line longer than this is cut (one segment, not the whole meeting)
	// What the merging call reads of all the parts' notes: per section, at most this many items over all parts (an
	// equal share each, at least 2), each at most minutesNoteRunes.
	minutesMergeItems = 60
	minutesNoteRunes  = 160
)

// A slow knowledge-base model (#3170). A minutes answer is several times longer than a summary, and a model that needs
// more than a call's time for it is slow, not wrong. So a try that runs out of its time (one call, or the whole job) is
// not an unusable answer: it is counted apart, the recording waits before its next try (the other recordings go first
// meanwhile), and each try gets one minutesCallTimeout more than the one before. minutesTimeoutTries of them for one
// revision make the minutes failed, so a recording no model ever finishes still can't hold the queue for ever.

const (
	minutesTimeoutTries = recordingwire.MinutesTimeoutTries // tries of one revision that ran out of time before the minutes are failed
	minutesRetry        = 2 * time.Minute                   // the wait after the first of them, doubled after each further one
)
