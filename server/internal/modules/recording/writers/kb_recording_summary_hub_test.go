package writers

import (
	"context"
	"github.com/nwverse/persona/server/internal/recordingwire"
	"strings"
	"testing"
	"time"
)

// hubCompleteMeeting stores a complete hub-mode meeting of the phone's lines (uploaded at *clock), its live hub
// sentences and some audio.
func hubCompleteMeeting(t *testing.T, e *testEngine, id string, at time.Time, phone []string, hub []RecordingSegment) {
	t.Helper()
	putRecording(t, e, id, hubUpload(1, at.Add(-time.Hour), true, nil, phone...), at)
	if len(hub) > 0 {
		if _, err := e.HubTranscriptPut(id, HubPassLive, hub); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.AudioPut(id, 0, pcmAt(0, secs(1))); err != nil {
		t.Fatal(err)
	}
}

// finishTranscription runs what the manager does at the end of the final pass: the final sentences, the state, and
// FinalTranscriptStored.
func finishTranscription(t *testing.T, e *testEngine, id, state string, final []RecordingSegment) {
	t.Helper()
	if final != nil {
		if _, err := e.HubTranscriptPut(id, HubPassFinal, final); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.TranscriptionUpdate(id, func(t *MeetingTranscription, _ Recording) error { t.State = state; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := e.FinalTranscriptStored(id); err != nil {
		t.Fatal(err)
	}
}

// The short summary is made again from the final transcript, also when its call from the live text was still running
// as the final pass ended, and when a crash kept FinalTranscriptStored from being told.
func TestHubSummaryMadeAgainAfterTheFinalPass(t *testing.T) {
	var e *testEngine
	race := true
	m := &funcModel{f: func(n int, _, user string) (string, error) {
		if race && n == 1 { // the final pass ends while the live text's summary is being made
			finishTranscription(t, e, recID, recordingwire.TranscriptionFinal, []RecordingSegment{hseg(0, 8, "final words")})
		}
		return `{"title": "Design review", "summary": "A summary.", "one_line": "One line."}`, nil
	}}
	e, _ = hubEngine(t, m)
	now := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	hubCompleteMeeting(t, e, recID, now, []string{"手机一"}, []RecordingSegment{hseg(0, 8, "live words")})
	ctx := context.Background()
	if n := e.SummarizeRecordings(ctx, 3); n != 1 || !strings.Contains(m.calls[0][1], "live words") {
		t.Fatalf("the live text's summary: %d", n)
	}
	if r := recordingNow(t, e, recID); !summaryDue(r) {
		t.Fatalf("the summary of the live text settled it: rev %d final %v", r.SummarizedRev, r.SummarizedFinal)
	}
	if n := e.SummarizeRecordings(ctx, 3); n != 1 || !strings.Contains(m.calls[1][1], "final words") {
		t.Fatalf("again from the final transcript: %d", n)
	}
	if n := e.SummarizeRecordings(ctx, 3); n != 0 || len(m.calls) != 2 {
		t.Fatalf("a third summary: %d, %d calls", n, len(m.calls))
	}

	// A crash between the final state and FinalTranscriptStored: the summary of the live text is due all the same.
	race = false
	const crashed = "5b6c3f4a-1d2e-4f80-9a1b-2c3d4e5f6a7b"
	hubCompleteMeeting(t, e, crashed, now, []string{"手机一"}, []RecordingSegment{hseg(0, 8, "live words")})
	if n := e.SummarizeRecordings(ctx, 3); n != 1 {
		t.Fatalf("the live text's summary: %d", n)
	}
	if _, err := e.HubTranscriptPut(crashed, HubPassFinal, []RecordingSegment{hseg(0, 8, "crashed final words")}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.TranscriptionUpdate(crashed, func(t *MeetingTranscription, _ Recording) error {
		t.State = recordingwire.TranscriptionFinal
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n := e.SummarizeRecordings(ctx, 3); n != 1 || !strings.Contains(m.calls[len(m.calls)-1][1], "crashed final words") {
		t.Fatalf("after the crash: %d", n)
	}
}
