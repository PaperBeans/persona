package recording

import (
	"github.com/nwverse/persona/server/internal/recordingwire"
	"strings"
	"testing"
	"time"
)

// The batches an attending session gets (prompts/meeting/feed.md), in each language: new lines with an answer, the
// meeting so far with lines left out, and the end with nothing new and with the last line.
func TestGoldenMeetingFeed(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	start := time.Date(2026, 11, 10, 6, 0, 0, 0, time.UTC) // 14:00 there
	r := recordingwire.Recording{ID: "a0000000-0000-4000-8000-000000000001", StartedAt: start, Title: "Design review", Attendance: recordingwire.AttendanceParticipant}
	lines := recordingwire.Live{Segments: []recordingwire.LiveSegment{
		{Start: 61, End: 64, Text: "Ada: let's keep the cap for this release."},
		{Start: 70, End: 70, Text: ""},
		{Start: 125, End: 131, Text: "Bo: I'll write the regression test by Friday."},
	}}
	asked := []recordingwire.LiveAsk{{ID: "q1", Question: "What did we decide last time?", State: feedAskAnswered,
		Answer: "At the design review on November 3 you kept the 4 MB cap.", Sources: []recordingwire.AskSource{}}}
	for _, lang := range []string{"en", "zh-Hans", "ja"} {
		l := lines
		l.Asks = asked
		golden(t, "meeting_feed.lines."+lang, feedText(r, feedBatchOf(l, false, false), lang, loc))

		far := recordingwire.Live{Segments: []recordingwire.LiveSegment{{Start: 0, End: 5, Text: strings.Repeat("x", feedBatchRunes)}}}
		far.Segments = append(far.Segments, lines.Segments...)
		listener := r
		listener.Attendance, listener.Title = recordingwire.AttendanceListener, ""
		listener.Calendar = &recordingwire.RecordingCalendar{Title: "Weekly sync"}
		golden(t, "meeting_feed.so_far."+lang, feedText(listener, feedBatchOf(far, true, false), lang, loc))

		done := lines
		done.Fed = len(done.Segments)
		golden(t, "meeting_feed.end."+lang, feedText(r, feedBatchOf(done, false, true), lang, loc))
		last := lines
		last.Fed = 2
		golden(t, "meeting_feed.end_lines."+lang, feedText(r, feedBatchOf(last, false, true), lang, loc))
	}
}
