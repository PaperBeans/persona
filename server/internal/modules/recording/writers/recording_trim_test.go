package writers

import (
	"encoding/json"
	"errors"
	"github.com/nwverse/persona/server/internal/kb"
	"os"
	"slices"
	"testing"
	"time"
)

// Past recording_days a recording with minutes keeps only its metadata and minutes until minutes_days; one without
// minutes, or past minutes_days, goes as before. Later passes change nothing more.
func TestRecordingTrimAtRecordingDays(t *testing.T) {
	e := goldenEngine(t, nil)
	days, minutesDays := 14, 90
	e.RecordingDaysFunc = func() int { return days }
	e.MinutesDaysFunc = func() int { return minutesDays }
	var indexed, unindexed []string
	e.Index = func(kind, id, _ string, _ Tagging, _ string, _ time.Time) { indexed = append(indexed, id) }
	e.Unindex = func(kind string, ids []string) { unindexed = append(unindexed, ids...) }
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, goldenZone)
	start := now.AddDate(0, 0, -20)
	const kept, plain, ancient = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"

	// A meeting Persona attended, with its minutes, summary, live log, crash debris and a pending undo journal.
	u := meetingUpload(1, start, "orchardword transcript line", "the second line")
	u.Attendance = AttendanceListener
	putRecording(t, e, kept, u, start)
	if _, _, err := e.LiveAppend(kept, 0, []LiveSegment{{Start: 1, End: 2, Text: "live line"}}); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.PutSummary(KindRecording, kept, "summary from the transcript"); err != nil {
		t.Fatal(err)
	}
	putMeetingMinutes(t, e, kept, Minutes{Version: 1, Revision: 1, GeneratedAt: start, Title: "Design review", When: "2026-10-31 12:00–12:01",
		Actions: []MinutesAction{{ID: "a1", What: "Write the plan", At: 0}}, Markdown: "# Design review\n\n- quasarminutes decision"})
	if err := os.WriteFile(e.Store.Path(KindRecording, kept, "segments.json.tmp"), []byte("debris"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ws(e.Store).recordingSnapshot(kept)
	if err != nil {
		t.Fatal(err)
	}
	journal, _ := json.Marshal(recordingUndo{Version: 1, ID: kept, Files: snapshot})
	if err := os.WriteFile(e.Store.Path(KindRecording, kept, recordingJournal), journal, 0o600); err != nil {
		t.Fatal(err)
	}
	// One with no minutes, one with minutes past minutes_days.
	putRecording(t, e, plain, recUpload(1, start, true, "no minutes"), start)
	old := now.AddDate(0, 0, -minutesDays-1)
	putRecording(t, e, ancient, meetingUpload(1, old, "long ago"), old)
	putMeetingMinutes(t, e, ancient, Minutes{Version: 1, Revision: 1, Title: "Old", Markdown: "# Old"})
	indexed = nil

	if n := e.PruneRecordings(now); n != 2 {
		t.Fatalf("pruned %d, want 2", n)
	}
	for _, id := range []string{plain, ancient} {
		if _, err := os.Stat(e.Store.Path(KindRecording, id)); !os.IsNotExist(err) {
			t.Errorf("%s is still there: %v", id, err)
		}
	}
	slices.Sort(unindexed)
	if !slices.Equal(unindexed, []string{plain, ancient}) {
		t.Errorf("unindexed %v", unindexed)
	}
	if names := recordingFileNames(t, e, kept); !slices.Equal(names, []string{"meta.json", "minutes.json", "minutes.md"}) {
		t.Fatalf("a trimmed recording keeps %v", names)
	}
	r := recordingNow(t, e, kept)
	if r.TrimmedAt == nil || !r.TrimmedAt.Equal(now) || r.MinutesState != MinutesReady || r.Attendance != "" || r.Revision != 1 || r.Kind != RecordingMeeting {
		t.Fatalf("trimmed meta %+v", r)
	}
	if !slices.Equal(indexed, []string{kept}) {
		t.Errorf("indexed %v, want the trimmed one again", indexed)
	}

	// Idempotent: nothing more to do.
	indexed = nil
	if n := e.PruneRecordings(now.Add(time.Hour)); n != 0 || len(indexed) != 0 {
		t.Fatalf("again: pruned %d, indexed %v", n, indexed)
	}
	if again := recordingNow(t, e, kept); !again.TrimmedAt.Equal(now) {
		t.Fatalf("trimmed_at moved: %v", again.TrimmedAt)
	}
	if got := e.AttendingRecordings(); len(got) != 0 {
		t.Fatalf("a trimmed recording still attends: %+v", got)
	}

	// Past minutes_days it goes too.
	if n := e.PruneRecordings(start.AddDate(0, 0, minutesDays).Add(time.Second)); n != 1 {
		t.Fatalf("at minutes_days: pruned %d, want 1", n)
	}
	if _, ok := e.Store.Recording(kept); ok {
		t.Fatal("kept past minutes_days")
	}
}

// minutes_days is forever (0) by default, otherwise never below recording_days.
func TestMinutesDaysEffective(t *testing.T) {
	e := goldenEngine(t, nil)
	if got := e.MinutesDays(); got != kb.DefaultMinutesDays {
		t.Fatalf("default %d", got)
	}
	days, minutes := 30, 7
	e.RecordingDaysFunc = func() int { return days }
	e.MinutesDaysFunc = func() int { return minutes }
	if got := e.MinutesDays(); got != 30 {
		t.Fatalf("below recording_days: %d, want 30", got)
	}
	minutes = 0
	if got := e.MinutesDays(); got != 0 {
		t.Fatalf("forever raised to recording_days: %d", got)
	}
	minutes = 3650
	if got := e.MinutesDays(); got != 3650 {
		t.Fatalf("%d, want 3650", got)
	}
	if got := kb.MinutesDaysFor(0, 0); got != kb.DefaultMinutesDays {
		t.Fatalf("kb.MinutesDaysFor(0, 0) = %d", got)
	}
}

// A live log read before a trim is not written back after it, and none is written to a trimmed recording at all: the
// trim keeps no live log (putLive).
func TestRecordingTrimKeepsNoLiveLog(t *testing.T) {
	e := goldenEngine(t, nil)
	e.RecordingDaysFunc = func() int { return 14 }
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, goldenZone)
	start := now.AddDate(0, 0, -20)
	u := meetingUpload(1, start, "a transcript line")
	u.Attendance = AttendanceListener
	putRecording(t, e, recID, u, start)
	if _, _, err := e.LiveAppend(recID, 0, []LiveSegment{{Start: 1, End: 2, Text: "secret live line"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.TopicsSet(recID, []LiveTopic{{Title: "Pricing", State: TopicCurrent}}, start); err != nil { // they go with it too
		t.Fatal(err)
	}
	if r, _ := e.Store.Recording(recID); r == nil {
		t.Fatal("no recording")
	} else if _, err := e.HighlightsSet(recID, []HighlightSaid{{Text: "The cap stays.", SaidAt: LiveClock(*r, 1, goldenZone)}}, goldenZone, start); err != nil {
		t.Fatal(err) // and the highlights
	}
	putMeetingMinutes(t, e, recID, Minutes{Version: 1, Revision: 1, Title: "Design review", Markdown: "# Design review"})
	ask := func(l *Live) {
		l.Asks = append(l.Asks, LiveAsk{ID: "q1", Question: "What was decided?", State: "pending"})
	}
	kept := func(when string) {
		t.Helper()
		if names := recordingFileNames(t, e, recID); !slices.Equal(names, trimKeptNoMeeting) {
			t.Fatalf("%s: a trimmed recording keeps %v", when, names)
		}
		l, r, err := e.LiveRead(recID)
		if err != nil || !r.Trimmed() || r.Attendance != "" || len(l.Segments) != 0 || len(l.Asks) != 0 || len(l.Topics) != 0 ||
			len(l.Highlights) != 0 || l.HighlightsRev != 0 {
			t.Fatalf("%s: live %+v, recording %+v, %v", when, l, r, err)
		}
	}

	// The trim lands between the live log's read and its write.
	_, err := e.LiveUpdate(recID, func(l *Live, r Recording) error {
		if r.Attendance == "" || len(l.Segments) != 1 {
			t.Errorf("read before the trim: %+v %+v", l, r)
		}
		if n := e.PruneRecordings(now); n != 0 {
			t.Errorf("pruned %d, want it trimmed", n)
		}
		ask(l)
		return nil
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("an update across the trim: %v", err)
	}
	kept("across the trim")

	// After it: an update stores nothing, an append is refused as not attending.
	if _, err := e.LiveUpdate(recID, func(l *Live, _ Recording) error { ask(l); return nil }); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an update after the trim: %v", err)
	}
	if _, _, err := e.LiveAppend(recID, 0, []LiveSegment{{Start: 3, End: 4, Text: "another line"}}); !errors.Is(err, ErrNotAttending) {
		t.Fatalf("an append after the trim: %v", err)
	}
	if err := ws(e.Store).goneError(recID); !errors.Is(err, ErrNotAttending) {
		t.Fatalf("why a trimmed recording takes no live log: %v", err)
	}
	kept("after the trim")
}

// A trim cut short (meta.json written, the transcript's files still there: a crash, a failed removal) is finished on the
// next pass, also once recording_days made longer lets the recording back in the window; a whole recording there is
// left alone.
func TestRecordingTrimFinishedWithinWindow(t *testing.T) {
	e := goldenEngine(t, nil)
	days := 14
	e.RecordingDaysFunc = func() int { return days }
	var indexed []string
	e.Index = func(kind, id, _ string, _ Tagging, _ string, _ time.Time) { indexed = append(indexed, id) }
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, goldenZone)
	start := now.AddDate(0, 0, -20)
	const whole = "66666666-6666-4666-8666-666666666666"
	putRecording(t, e, recID, meetingUpload(1, start, "orchardword transcript line"), start)
	if err := e.Store.PutSummary(KindRecording, recID, "summary from the transcript"); err != nil {
		t.Fatal(err)
	}
	putMeetingMinutes(t, e, recID, Minutes{Version: 1, Revision: 1, Title: "Design review", Markdown: "# Design review\n\n- quasarminutes decision"})
	putRecording(t, e, whole, meetingUpload(1, start, "wholeword transcript line"), start)
	putMeetingMinutes(t, e, whole, Minutes{Version: 1, Revision: 1, Title: "Planning", Markdown: "# Planning"})
	wholeFiles := recordingFileNames(t, e, whole)

	// The crash: the trim wrote meta.json and nothing more.
	r := recordingNow(t, e, recID)
	markTrimmed(t, e, r.ID, now)
	if names := recordingFileNames(t, e, recID); !slices.Contains(names, "transcript.md") {
		t.Fatalf("set up: %v", names)
	}
	days, indexed = 60, nil
	if n := e.PruneRecordings(now); n != 0 {
		t.Fatalf("pruned %d", n)
	}
	if names := recordingFileNames(t, e, recID); !slices.Equal(names, trimKeptNoMeeting) {
		t.Fatalf("a trim cut short keeps %v", names)
	}
	d, err := e.Get(KindRecording, recID, 12000)
	if err != nil || len(d.Segments) != 0 || d.Summary != "" {
		t.Fatalf("detail %+v %v", d, err)
	}
	if res := e.Find("orchardword", 10); len(res) != 0 {
		t.Fatalf("the transcript is still found: %+v", res)
	}
	if res := e.Find("quasarminutes", 10); len(res) != 1 || res[0].Entry.ID != recID {
		t.Fatalf("find the minutes: %+v", res)
	}
	if !slices.Equal(indexed, []string{recID}) {
		t.Errorf("indexed %v, want the trimmed one again", indexed)
	}
	if names := recordingFileNames(t, e, whole); !slices.Equal(names, wholeFiles) || recordingNow(t, e, whole).Trimmed() {
		t.Fatalf("a whole recording within the window changed: %v", names)
	}

	// Finished: later passes change nothing.
	indexed = nil
	if n := e.PruneRecordings(now.Add(time.Hour)); n != 0 || len(indexed) != 0 {
		t.Fatalf("again: pruned %d, indexed %v", n, indexed)
	}
}

// With the default or explicit zero, both very old and already-trimmed minutes survive every sweep. Switching to a
// finite window still deletes them; the user's explicit delete is covered by TestRecordingTrimmedReadAndRefused.
func TestRecordingMinutesKeptForever(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "unset", true: "zero"}[explicit], func(t *testing.T) {
			e := goldenEngine(t, nil)
			if explicit {
				e.MinutesDaysFunc = func() int { return 0 }
			}
			now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
			start := now.AddDate(-20, 0, 0)
			putRecording(t, e, recID, meetingUpload(1, start, "old transcript"), start)
			putMeetingMinutes(t, e, recID, Minutes{Version: 1, Revision: 1, Title: "Old meeting", Markdown: "# Old meeting"})
			for _, sweep := range []time.Time{now, now.AddDate(100, 0, 0)} {
				if n := e.PruneRecordings(sweep); n != 0 {
					t.Fatalf("forever pruned %d", n)
				}
				if files := recordingFileNames(t, e, recID); !slices.Equal(files, trimKeptNoMeeting) {
					t.Fatalf("forever kept %v", files)
				}
			}
			// An unreadable minutes.json must not turn a forever-trimmed recording into an expiring plain one.
			if err := os.WriteFile(e.Store.Path(KindRecording, recID, "minutes.json"), []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
			if n := e.PruneRecordings(now); n != 0 {
				t.Fatalf("unreadable trimmed minutes pruned %d", n)
			}
			e.MinutesDaysFunc = func() int { return 3650 }
			if n := e.PruneRecordings(now); n != 1 {
				t.Fatalf("finite window pruned %d, want 1", n)
			}
		})
	}
}

// trimKeptNoMeeting are the files a trimmed recording without a meeting.json keeps.
var trimKeptNoMeeting = []string{"meta.json", "minutes.json", "minutes.md"}
