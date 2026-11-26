package writers

import (
	"testing"
	"time"
)

// BenchmarkAudioAppend is one socket append of 10 ms of audio (160 samples), the hot path of a hub-mode meeting.
func BenchmarkAudioAppend(b *testing.B) {
	t := &testing.T{}
	e, _ := hubEngine(t, nil)
	now := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	hubMeeting(t, e, recID, now)
	w, err := e.AudioOpen(recID)
	if err != nil {
		b.Fatal(err)
	}
	defer w.Close()
	pcm := pcmAt(0, 160)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := w.Write(sampleAt(int64(i)*160), pcm); err != nil {
			b.Fatal(err)
		}
	}
}
