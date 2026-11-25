package kb

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// The recordings the module gates start their hubs on (scripts/ci/core-only-smoke.py, scripts/ci/module-e2e.py):
// written by the knowledge base's own writers, as a hub with the recording module leaves them, so the gates can check
// that a hub without the module still lists, opens and finds them and leaves their files as they are.
const (
	// fixtureRecording is a meeting with its transcript and minutes; fixturePendingRecording one whose minutes were
	// still to be written. scripts/ci/core-only-smoke.py names both.
	fixtureRecording        = "6f1c2a3b-0000-4000-8000-00000000c0de"
	fixturePendingRecording = "6f1c2a3b-0000-4000-8000-00000000beef"
)

type fixtureMinutesModel struct{}

func (fixtureMinutesModel) JSONMax(_ context.Context, _, _ string, _ int, out any) error {
	return json.Unmarshal([]byte(`{"title": "Kept recording review", "points": [{"text": "The wombat checklist stays readable without the module", "line": 2}],
		"decisions": [{"text": "Stored recordings are never hidden by a switch", "line": 3}]}`), out)
}

// TestWriteRecordingFixture writes the gates' recordings into the knowledge base at PERSONA_RECORDING_FIXTURE_DIR (a
// data directory no hub has open); skipped without it.
func TestWriteRecordingFixture(t *testing.T) {
	dir := os.Getenv("PERSONA_RECORDING_FIXTURE_DIR")
	if dir == "" {
		t.Skip("PERSONA_RECORDING_FIXTURE_DIR is not set: only the module gates' scripts write this fixture")
	}
	st, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{Store: st, Model: fixtureMinutesModel{}, Owner: "Owner", Logf: func(string, ...any) {}}
	e.SetRecordingProvider(RecordingWriters(e))
	now := time.Now().Truncate(time.Second)
	put := func(id, title string, start time.Time, texts ...string) {
		u := RecordingUpload{Revision: 1, StartedAt: start, Duration: float64(10 * len(texts)), Language: "en-US", Device: "phone",
			Title: title, Complete: true, Kind: RecordingMeeting}
		for i, text := range texts {
			u.Segments = append(u.Segments, RecordingSegment{Start: float64(10 * i), End: float64(10*i + 9), Text: text})
		}
		if p, err := e.PutRecording(id, u, now); err != nil || p.Status != RecordingStored {
			t.Fatalf("put %s: %+v %v", id, p, err)
		}
	}
	put(fixtureRecording, "Kept recording", now.Add(-2*time.Hour), "This made-up meeting is the module gate's fixture",
		"The quokka transcript stays readable without the module", "Stored recordings are never hidden by a switch")
	if n := e.RecordingProvider().WriteMinutes(context.Background(), 2); n != 1 {
		t.Fatalf("wrote %d minutes, want 1", n)
	}
	put(fixturePendingRecording, "Kept recording, minutes pending", now.Add(-time.Hour), "A second made-up meeting", "Its minutes were never written")
	if r, ok := st.Recording(fixturePendingRecording); !ok || r.MinutesState != MinutesPending {
		t.Fatalf("the pending fixture's minutes state: %+v", r)
	}
	if _, err := st.ReadMinutes(fixtureRecording); err != nil {
		t.Fatal(err)
	}
}
