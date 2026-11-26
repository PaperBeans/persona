package writers

import (
	"context"
	"github.com/nwverse/persona/server/internal/kbwire"
	"github.com/nwverse/persona/server/internal/recordingwire"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/kb"
)

// Minutes that were pending when the recording module went are written when it is back: the knowledge base opened
// without the module leaves the stored state pending (and says paused, internal/kb's
// TestRecordingInFlightIsPausedWithoutTheModule), and this module's round takes it up from there.
func TestMinutesResumeWhenTheModuleIsBack(t *testing.T) {
	m := &captureModel{reply: minutesReply}
	on := goldenEngine(t, m)
	now := time.Date(2026, 11, 10, 16, 0, 0, 0, goldenZone)
	putRecording(t, on, recID, meetingUpload(1, time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone), "第二个会议讨论排期", "下周三之前给出方案"), now)
	st, err := kb.NewStore(on.Store.Dir) // the hub started again, without the module
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Rezone(goldenZone); err != nil {
		t.Fatal(err)
	}
	off := &kb.Engine{Store: st, Model: m, Owner: on.Owner, Logf: func(string, ...any) {}}
	if n := off.RecordingProvider().WriteMinutes(context.Background(), 2); n != 0 || len(m.calls) != 0 {
		t.Fatalf("without the module: wrote %d minutes with %d model calls", n, len(m.calls))
	}
	if d, err := off.Get(KindRecording, recID, 0); err != nil || d.Paused != kb.PausedModuleOff {
		t.Fatalf("without the module: paused %q, %v", d.Paused, err)
	}
	back := newTestEngine(&kb.Engine{Store: st, Model: m, Owner: on.Owner, Logf: func(string, ...any) {}})
	if n := back.RecordingProvider().WriteMinutes(context.Background(), 2); n != 1 {
		t.Fatalf("back with the module: wrote %d minutes, want 1", n)
	}
	if d, err := back.Get(KindRecording, recID, 0); err != nil || d.Paused != "" || recordingNow(t, back, recID).MinutesState != MinutesReady {
		t.Fatalf("back with the module: paused %q, state %q, %v", d.Paused, recordingNow(t, back, recID).MinutesState, err)
	}
}

// A hub-mode meeting whose phone sent no text has the hub's: minutes can be asked for it (AskMinutes refuses a
// recording with no text). The rest of the hub transcript's bookkeeping is internal/kb's
// TestHubTranscriptCountsAsText.
func TestHubTextCountsForAskingMinutes(t *testing.T) {
	e, _ := hubEngine(t, nil)
	now := time.Date(2026, 11, 10, 16, 0, 0, 0, goldenZone)
	putRecording(t, e, recID, hubUpload(1, now.Add(-time.Hour), true, nil), now)
	if err := e.AskMinutes(recID); err != ErrRecordingIncomplete {
		t.Fatalf("no text yet: %v", err)
	}
	if _, err := e.HubTranscriptPut(recID, recordingwire.HubPassLive, []kbwire.RecordingSegment{hseg(0, 4, "Bo owns the vendor follow-up.")}); err != nil {
		t.Fatal(err)
	}
	if err := e.AskMinutes(recID); err != nil {
		t.Fatalf("ask: %v", err)
	}
}
