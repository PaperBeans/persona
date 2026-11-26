package writers

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRecordingIdempotentAndOrdered(t *testing.T) {
	e := goldenEngine(t, nil)
	var indexed []string
	e.Index = func(kind, id, name string, _ Tagging, _ string, _ time.Time) {
		indexed = append(indexed, kind+"/"+id+" "+name)
	}
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, goldenZone)
	start := now.Add(-time.Hour)
	// Upper case is the same recording.
	for range 2 { // a replay changes nothing
		if st, err := e.PutRecording(strings.ToUpper(recID), recUpload(2, start, false, "第一段", "第二段"), now); err != nil || st.Status != RecordingStored {
			t.Fatalf("put: %v %v", st, err)
		}
	}
	if st, _ := e.PutRecording(recID, recUpload(1, start, false, "旧的"), now); st.Status != RecordingStale {
		t.Fatalf("an older revision: %v, want stale", st)
	}
	if ids := e.Store.IDs(KindRecording); len(ids) != 1 || ids[0] != recID {
		t.Fatalf("ids %v", ids)
	}
	if segs := e.Store.RecordingSegments(recID); len(segs) != 2 || segs[0].Text != "第一段" {
		t.Fatalf("segments %+v", segs)
	}
	if len(indexed) == 0 || !strings.HasPrefix(indexed[0], "recordings/"+recID+" 录音 2026-11-05 11:00") {
		t.Fatalf("indexed %v", indexed)
	}
	for _, bad := range []struct {
		id string
		u  RecordingUpload
	}{
		{"not-a-uuid", recUpload(3, start, false)},
		{recID, recUpload(0, start, false)},
		{recID, recUpload(3, time.Time{}, false)},
		{recID, RecordingUpload{Revision: 3, StartedAt: start, Device: "tv"}},
	} {
		if _, err := e.PutRecording(bad.id, bad.u, now); !errors.Is(err, ErrBadRecording) {
			t.Errorf("%s %+v: %v, want ErrBadRecording", bad.id, bad.u, err)
		}
	}
}
