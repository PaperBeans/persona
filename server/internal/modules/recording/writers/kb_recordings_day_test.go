package writers

import (
	"errors"
	"github.com/nwverse/persona/server/internal/recordingwire"
	"testing"
	"time"
)

// A trimmed day recording takes no append nor PUT (expired) and makes no recap again.
func TestDayAppendTrimmed(t *testing.T) {
	now := dayClock.Add(time.Hour)
	e := dayEngine(t, nil, &now)
	mustPut(t, e, dayID, dayUpload(10, "一"), now)
	writeTestRecap(t, e, dayID)
	now = dayClock.AddDate(0, 0, 20)
	e.PruneRecordings(now)
	r := recordingNow(t, e, dayID)
	if !r.Trimmed() || r.RecapState != MinutesReady {
		t.Fatalf("trimmed %+v", r)
	}
	now = dayClock.Add(time.Hour)
	e.RecordingDaysFunc = func() int { return 60 } // back in the window: still expired
	if got, err := e.AppendRecording(dayID, dayAppend(10, 11, 1, seg(70, "二"))); err != nil || got.Status != recordingwire.RecordingExpired {
		t.Errorf("append %+v %v", got, err)
	}
	if err := e.AskMinutes(dayID); !errors.Is(err, ErrTranscriptExpired) {
		t.Errorf("ask: %v", err)
	}
}

// A day's hours are hours of absolute time in its zone: the hour a fall-back repeats is two chunks, each ending when
// its hour does, and coverage passing from one into the other finishes the first (and wakes the recap lane).
func TestDayChunksDST(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skip(err)
	}
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, la) // 01:00–02:00 comes twice that night
	lines := []minutesLine{{N: 1, At: 5400, Text: "一点半，夏令时"}, {N: 2, At: 9000, Text: "一点半，标准时"}}
	chunks := dayChunks(lines, start, la, 9600, nil) // to 01:40 PST
	if len(chunks) != 2 {
		t.Fatalf("chunks %+v", chunks)
	}
	if c := chunks[0]; c.end != 7200 || !c.finished || len(c.lines) != 1 {
		t.Errorf("01:00 PDT: %+v", c)
	}
	if c := chunks[1]; c.end != 9600 || c.finished || len(c.lines) != 1 {
		t.Errorf("01:00 PST: %+v", c)
	}
	if h := hourStart(start.Add(9000*time.Second), la); !h.Equal(start.Add(7200 * time.Second)) {
		t.Errorf("hourStart %v", h)
	}
	now := start.Add(3 * time.Hour)
	e := dayEngine(t, nil, &now)
	u := dayUpload(1)
	u.StartedAt, u.Day, u.TZ = start, "2026-11-01", "America/Los_Angeles"
	u.Segments = []RecordingSegment{seg(5400, "一点半")}
	u.Coverage = &recordingwire.RecordingCoverage{To: 6600} // 01:50 PDT
	mustPut(t, e, dayID, u, now)
	a := dayAppend(1, 2, 1)
	a.Coverage = &recordingwire.RecordingCoverage{To: 7300} // 01:01 PST
	if got := mustAppend(t, e, dayID, a); !got.Wake {
		t.Error("the repeated hour began without a wake")
	}
}
