package writers

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/nwverse/persona/server/internal/recordingwire"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// What a trimmed recording still does: read with its minutes and no transcript, found by its minutes, listed for the
// phone, its action items confirmed, its minutes given to later meetings; and what it doesn't: take an upload, make
// minutes again, get a summary.
func TestRecordingTrimmedReadAndRefused(t *testing.T) {
	e := goldenEngine(t, trimModel{t})
	days := 14
	e.RecordingDaysFunc = func() int { return days }
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, goldenZone)
	start := now.AddDate(0, 0, -20)
	putRecording(t, e, recID, meetingUpload(1, start, "orchardword transcript line"), start)
	putMeetingMinutes(t, e, recID, Minutes{Version: 1, Revision: 1, Title: "Design review", When: "2026-10-31 12:00–12:01",
		Decisions: []MinutesItem{{Text: "Keep the cap", At: 0}}, Actions: []MinutesAction{{ID: "a1", What: "Write the plan", At: 0}},
		Markdown: "# Design review\n\n- quasarminutes decision"})
	changeRecording(t, e, recID, func(r *Recording) { r.MinutesRev, r.MinutesVersion = 1, 1 })
	if n := e.PruneRecordings(now); n != 0 {
		t.Fatalf("pruned %d", n)
	}
	d, err := e.Get(KindRecording, recID, 12000)
	if err != nil {
		t.Fatal(err)
	}
	var meta struct {
		TrimmedAt *time.Time `json:"trimmed_at"`
	}
	if json.Unmarshal(d.Meta, &meta) != nil || meta.TrimmedAt == nil || len(d.Segments) != 0 || d.Recent != "" || d.Summary != "" ||
		!strings.Contains(string(d.Minutes), "Keep the cap") {
		t.Fatalf("detail %+v", d)
	}
	if raw, _ := json.Marshal(d); strings.Contains(string(raw), `"segments":[`) || strings.Contains(string(raw), "orchardword") {
		t.Fatalf("detail shows the transcript: %s", raw)
	}
	if res := e.Find("quasarminutes", 10); len(res) != 1 || res[0].Entry.ID != recID || !strings.HasSuffix(res[0].Hits[0].Path, "/minutes.md") {
		t.Fatalf("find the minutes: %+v", res)
	}
	if res := e.Find("orchardword", 10); len(res) != 0 {
		t.Fatalf("the transcript is still found: %+v", res)
	}
	kept, _ := e.RecordingStates()
	if len(kept) != 1 || kept[0].ID != recID || kept[0].TrimmedAt == nil || kept[0].Revision != 1 || kept[0].MinutesState != MinutesReady {
		t.Fatalf("states %+v", kept)
	}

	// Refused: an upload (expired, also once recording_days covers it again), minutes made again.
	for _, n := range []int{14, 60} {
		days = n
		if st, err := e.PutRecording(recID, meetingUpload(2, start, "a new transcript"), now); err != nil || st.Status != recordingwire.RecordingExpired {
			t.Fatalf("days %d: upload %+v %v, want expired", n, st, err)
		}
	}
	if r := recordingNow(t, e, recID); r.Revision != 1 || r.TrimmedAt == nil {
		t.Fatalf("an upload changed it: %+v", r)
	}
	if err := e.AskMinutes(recID); !errors.Is(err, ErrTranscriptExpired) {
		t.Fatalf("ask minutes: %v", err)
	}
	if n := e.SummarizeRecordings(context.Background(), 3); n != 0 {
		t.Fatalf("summarized %d", n)
	}
	if n := e.WriteMinutes(context.Background(), 2); n != 0 {
		t.Fatalf("wrote minutes %d", n)
	}
	if m, err := e.SetMinutesTodos(recID, 1, map[string]string{"a1": "todo_x"}, nil); err != nil || m.Actions[0].TodoID != "todo_x" {
		t.Fatalf("confirming a to-do: %+v %v", m, err)
	}

	// Earlier meetings come back as material once marked not private, a trimmed one too.
	e.EarlierMeetingsInAnswers = true
	markPrivate(t, e, recID, false)
	later := "44444444-4444-4444-8444-444444444444"
	putRecording(t, e, later, attendedUpload(1, now, recordingwire.AttendanceListener), now)
	material, err := e.MeetingMaterial(later, "What did the design review decide?")
	if err != nil || len(material.Earlier) != 1 || material.Earlier[0].ID != recID || material.Earlier[0].Minutes.Decisions[0].Text != "Keep the cap" {
		t.Fatalf("material %+v %v", material, err)
	}

	// The user's delete still removes everything and leaves a tombstone.
	if ok, err := e.DeleteRecording(recID, now); !ok || err != nil {
		t.Fatalf("delete: %v %v", ok, err)
	}
	if _, err := os.Stat(e.Store.Path(KindRecording, recID)); !os.IsNotExist(err) {
		t.Fatalf("files left: %v", err)
	}
	if _, deleted := e.RecordingStates(); !slices.Equal(deleted, []string{recID}) {
		t.Fatalf("tombstones %v", deleted)
	}
}
