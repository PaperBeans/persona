package writers

import (
	"context"
	"errors"
	"github.com/nwverse/persona/server/internal/recordingwire"
	"testing"
	"time"
)

// A meeting that completes with no text gets no minutes: minutes_state absent, nothing for the model, and asking for
// minutes is refused as for an incomplete transcript.
func TestMeetingWithoutTextGetsNoMinutes(t *testing.T) {
	m := &funcModel{f: func(int, string, string) (string, error) { return minutesReply, nil }}
	e := goldenEngine(t, m)
	now := time.Date(2026, 11, 10, 15, 0, 0, 0, goldenZone)
	start := now.Add(-time.Hour)
	putRecording(t, e, recID, attendedUpload(1, start, recordingwire.AttendanceListener), now)
	if r := recordingNow(t, e, recID); r.MinutesState != MinutesPending {
		t.Fatalf("while recording: %q, want pending", r.MinutesState)
	}
	done := attendedUpload(2, start, recordingwire.AttendanceListener)
	done.Complete = true
	putRecording(t, e, recID, done, now)
	if r := recordingNow(t, e, recID); r.MinutesState != "" {
		t.Fatalf("complete with no text: %q, want none", r.MinutesState)
	}
	if states, _ := e.RecordingStates(); len(states) != 1 || states[0].MinutesState != "" {
		t.Fatalf("states %+v", states)
	}
	if n := e.WriteMinutes(context.Background(), 3); n != 0 || len(m.calls) != 0 {
		t.Fatalf("wrote %d with %d calls", n, len(m.calls))
	}
	if err := e.AskMinutes(recID); !errors.Is(err, ErrRecordingIncomplete) {
		t.Fatalf("asked: %v", err)
	}
	if r := recordingNow(t, e, recID); r.MinutesState != "" || r.MinutesAsked {
		t.Fatalf("after asking: %q asked %v", r.MinutesState, r.MinutesAsked)
	}
	// Text in a later revision: minutes again.
	putRecording(t, e, recID, meetingUpload(3, start, "Hello everyone"), now)
	if r := recordingNow(t, e, recID); r.MinutesState != MinutesPending {
		t.Fatalf("with text: %q", r.MinutesState)
	}
}
