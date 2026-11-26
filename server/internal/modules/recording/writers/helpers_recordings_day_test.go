package writers

// Test helpers the knowledge base's own tests have under the same names (internal/kb/*_test.go).

import (
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/recordingwire"
	"testing"
	"time"
)

const dayID = "1a2b3c4d-5e6f-4a1b-8c2d-3e4f5a6b7c8d"

// dayClock is the day recordings' tests' day: 2026-11-11, recorded from 08:00 in the golden zone (Asia/Shanghai).
var dayClock = time.Date(2026, 11, 11, 8, 0, 0, 0, goldenZone)

// dayUpload is a day recording's PUT of texts, a minute apart from the day's start (8:00), covered up to the last.
func dayUpload(rev int64, texts ...string) RecordingUpload {
	u := RecordingUpload{Revision: rev, StartedAt: dayClock, Duration: float64(60 * len(texts)), Language: "zh-CN", Device: "watch",
		Kind: RecordingDay, Day: "2026-11-11", TZ: "Asia/Shanghai"}
	for i, t := range texts {
		u.Segments = append(u.Segments, RecordingSegment{Start: float64(60 * i), End: float64(60*i + 5), Text: t})
	}
	u.Coverage = &recordingwire.RecordingCoverage{To: float64(60 * len(texts))}
	return u
}

// dayEngine is a golden engine whose clock reads now.
func dayEngine(t *testing.T, m kb.Model, now *time.Time) *testEngine {
	t.Helper()
	e := goldenEngine(t, m)
	e.SetClock(func() time.Time { return *now })
	return e
}

func ptr[T any](v T) *T { return &v }

// dayAppend is an append of base at revision rev from position from.
func dayAppend(base, rev int64, from int, segs ...RecordingSegment) RecordingAppend {
	return RecordingAppend{Base: base, Revision: rev, From: ptr(from), Segments: segs}
}

func seg(start float64, text string) RecordingSegment {
	return RecordingSegment{Start: start, End: start + 5, Text: text}
}

func mustPut(t *testing.T, e *testEngine, id string, u RecordingUpload, now time.Time) recordingwire.RecordingPut {
	t.Helper()
	put, err := e.PutRecording(id, u, now)
	if err != nil || put.Status != recordingwire.RecordingStored {
		t.Fatalf("put %s: %+v %v", id, put, err)
	}
	return put
}

func mustAppend(t *testing.T, e *testEngine, id string, a RecordingAppend) RecordingAppended {
	t.Helper()
	got, err := e.AppendRecording(id, a)
	if err != nil || got.Status != recordingwire.RecordingStored {
		t.Fatalf("append %s: %+v %v", id, got, err)
	}
	return got
}
