package writers

import (
	"fmt"
	"testing"
	"time"
)

// liveBenchEngine is a meeting Persona attends, with a live log of n sentences: the state a live append works on.
func liveBenchEngine(tb testing.TB, n int) *testEngine {
	t := &testing.T{}
	e := goldenEngine(t, nil)
	now := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	putRecording(t, e, recID, attendedUpload(1, now, AttendanceParticipant), now)
	segs := make([]LiveSegment, n)
	for i := range segs {
		segs[i] = LiveSegment{Start: float64(i), End: float64(i) + 0.9, Text: fmt.Sprintf("sentence %d of the meeting", i)}
	}
	if _, _, err := e.LiveAppend(recID, 0, segs); err != nil {
		tb.Fatal(err)
	}
	return e
}

// BenchmarkLiveAppend is one append of a sentence to a live log that holds 200 already (POST …/live, the hot path
// of a meeting Persona attends): the log is read, the sentence added, the log written and synced.
func BenchmarkLiveAppend(b *testing.B) {
	e := liveBenchEngine(b, 200)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		at := 200 + i
		if _, _, err := e.LiveAppend(recID, at, []LiveSegment{{Start: float64(at), End: float64(at) + 0.9, Text: "one more sentence"}}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLiveAppendReplay is an append whose positions the log holds already (a retry): it reads the log and
// stores nothing, so it is the append without its write.
func BenchmarkLiveAppendReplay(b *testing.B) {
	e := liveBenchEngine(b, 200)
	seg := []LiveSegment{{Start: 0, End: 0.9, Text: "sentence 0 of the meeting"}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := e.LiveAppend(recID, 0, seg); err != nil {
			b.Fatal(err)
		}
	}
}
