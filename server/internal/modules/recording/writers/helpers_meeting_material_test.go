package writers

// Test helpers the knowledge base's own tests have under the same names (internal/kb/*_test.go).

import (
	"testing"
)

// putMeetingMinutes stores minutes for recording id as the minutes round would.
func putMeetingMinutes(t *testing.T, e *testEngine, id string, m Minutes) {
	t.Helper()
	if ok, err := ws(e.Store).putMinutes(id, &m); !ok || err != nil {
		t.Fatalf("minutes of %s: %v %v", id, ok, err)
	}
}

// changeRecording rewrites recording id's meta.json with fn applied.
func changeRecording(t *testing.T, e *testEngine, id string, fn func(r *Recording)) {
	t.Helper()
	changeRecordingMeta(t, e, id, fn)
}

// markPrivate sets recording id's private mark.
func markPrivate(t *testing.T, e *testEngine, id string, private bool) {
	t.Helper()
	if _, err := e.SetPrivate(id, private); err != nil {
		t.Fatalf("mark %s: %v", id, err)
	}
}

func changeRecordingMeta(t *testing.T, e *testEngine, id string, fn func(r *Recording)) {
	t.Helper()
	r := recordingNow(t, e, id)
	fn(r)
	if err := ws(e.Store).putEntity(KindRecording, id, r, &r.Tagging, false); err != nil {
		t.Fatal(err)
	}
}
