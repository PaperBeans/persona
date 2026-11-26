package writers

import (
	"github.com/nwverse/persona/server/internal/recordingwire"
	"slices"
	"testing"
	"time"
)

// final_done is kept in meeting.json and moves the transcription's revision (the apps hear of it in meeting_live); it
// is dropped once the state is final.
func TestFinalDoneMovesTheRevision(t *testing.T) {
	now := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	e, changed := hubEngine(t, nil)
	e.SetClock(func() time.Time { return now })
	putRecording(t, e, recID, hubUpload(1, now.Add(-time.Hour), false, nil), now)
	parts := func() []string {
		var all []string
		for _, p := range *changed {
			all = append(all, p...)
		}
		*changed = nil
		return all
	}
	upd := func(fn func(t *MeetingTranscription)) MeetingTranscription {
		t.Helper()
		tr, err := e.TranscriptionUpdate(recID, func(t *MeetingTranscription, _ Recording) error { fn(t); return nil })
		if err != nil {
			t.Fatal(err)
		}
		return tr
	}
	upd(func(t *MeetingTranscription) { t.State, t.End = recordingwire.TranscriptionFinishing, 600 })
	parts()
	upd(func(t *MeetingTranscription) { t.FinalDone = 300 })
	if p := parts(); len(p) != 0 {
		t.Fatalf("progress before the pass started moved %v", p)
	}
	upd(func(t *MeetingTranscription) { t.FinalFrom = now })
	if p := parts(); !slices.Equal(p, []string{recordingwire.PartTranscription}) {
		t.Fatalf("the pass's start told %v", p)
	}
	upd(func(t *MeetingTranscription) { t.FinalDone = 450 })
	if p := parts(); !slices.Equal(p, []string{recordingwire.PartTranscription}) {
		t.Fatalf("a window told %v", p)
	}
	if tr := transcriptionNow(t, e, recID); tr.FinalDone != 450 {
		t.Fatalf("kept %+v", tr)
	}
	m, _, _ := e.MeetingRead(recID)
	if v := m.TranscriptionView(nil); v.FinalDone == nil || *v.FinalDone != 450 || v.End == nil || *v.End != 600 {
		t.Fatalf("view %+v", v)
	}
	tr := upd(func(t *MeetingTranscription) { t.State = recordingwire.TranscriptionFinal })
	if tr.FinalDone != 0 {
		t.Fatalf("final keeps its progress: %+v", tr)
	}
	m, _, _ = e.MeetingRead(recID)
	if v := m.TranscriptionView(nil); v.FinalDone != nil || v.End != nil {
		t.Fatalf("final view %+v", v)
	}
}
