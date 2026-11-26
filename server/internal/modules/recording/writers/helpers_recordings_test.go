package writers

// Test helpers the knowledge base's own tests have under the same names (internal/kb/*_test.go).

import (
	"time"
)

const (
	recID  = "0f8fad5b-d9cb-469f-a165-70867728950e"
	recID2 = "1a2b3c4d-0000-4000-8000-000000000002"
)

func recUpload(rev int64, start time.Time, complete bool, texts ...string) RecordingUpload {
	u := RecordingUpload{Revision: rev, StartedAt: start, Duration: float64(10 * len(texts)), Language: "zh-CN", Device: "phone", Complete: complete}
	for i, t := range texts {
		u.Segments = append(u.Segments, RecordingSegment{Start: float64(10 * i), End: float64(10*i + 9), Text: t})
	}
	return u
}
