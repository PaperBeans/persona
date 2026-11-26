package kb

// The read path of echo.go: Core, with or without the recording module (docs/plans/plan-kb-recording-port.md,
// section 1; docs/architecture/core-and-modules.md "Recording").

import "github.com/nwverse/persona/server/internal/recordingwire"

// MeetingLines is segs without Persona's echo: what the summary, the minutes and the follow-up read.
func MeetingLines(segs []RecordingSegment) []RecordingSegment {
	return recordingwire.MeetingLines(segs)
}
