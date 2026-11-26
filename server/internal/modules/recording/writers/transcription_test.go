package writers

import (
	"encoding/json"
	"errors"
	"github.com/nwverse/persona/server/internal/recordingwire"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// hubEngine is an engine whose manager can transcribe on the hub (TranscriptionReady), collecting MeetingChanged.
func hubEngine(t *testing.T, m Model) (*testEngine, *[][]string) {
	t.Helper()
	e := goldenEngine(t, m)
	// newTestEngine hooked the module's provider: hub transcription is the recording module's: retention reaches its audio and its sweep
	e.TranscriptionReady = func() bool { return true }
	var changed [][]string
	e.MeetingChanged = func(_ string, parts []string) { changed = append(changed, parts) }
	return e, &changed
}

// hubUpload is a meeting upload in hub mode with the phone's lines (10 seconds each) and timeline tl.
func hubUpload(rev int64, start time.Time, complete bool, tl *recordingwire.Timeline, texts ...string) RecordingUpload {
	u := recUpload(rev, start, complete, texts...)
	u.Kind = RecordingMeeting
	u.SetTranscription(TranscriptionHub, tl)
	return u
}

// transcriptionNow is recording id's meeting.json transcription.
func transcriptionNow(t *testing.T, e *testEngine, id string) *MeetingTranscription {
	t.Helper()
	m, _, err := e.MeetingRead(id)
	if err != nil {
		t.Fatal(err)
	}
	return m.Transcription
}

// The upload's transcription: device or hub (any case) names a mode, anything else (another string, another type,
// null, absent) none and never fails the upload; hub while the hub is not ready is device with unavailable; a note or
// a day naming hub is device with not_meeting and stores nothing. The 200's fields come with every upload.
func TestTranscriptionUploadModes(t *testing.T) {
	now := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	start := now.Add(-time.Hour).UTC().Format(time.RFC3339)
	for _, c := range []struct {
		name, extra   string
		ready         bool
		mode, problem string
		stored        string // the mode meeting.json keeps ("" none)
		asks          bool   // the hub's readiness is asked
	}{
		{"hub", `"kind":"meeting","transcription":"hub"`, true, TranscriptionHub, "", TranscriptionHub, true},
		{"hub in capitals", `"kind":"meeting","transcription":" HUB "`, true, TranscriptionHub, "", TranscriptionHub, true},
		{"device", `"kind":"meeting","transcription":"device"`, true, recordingwire.TranscriptionDevice, "", recordingwire.TranscriptionDevice, false},
		{"hub not ready", `"kind":"meeting","transcription":"hub"`, false, recordingwire.TranscriptionDevice, recordingwire.TranscriptionUnavailable, recordingwire.TranscriptionDevice, true},
		{"another string", `"kind":"meeting","transcription":"cloud"`, true, recordingwire.TranscriptionDevice, "", "", false},
		{"a number", `"kind":"meeting","transcription":1`, true, recordingwire.TranscriptionDevice, "", "", false},
		{"null", `"kind":"meeting","transcription":null`, true, recordingwire.TranscriptionDevice, "", "", false},
		{"absent", `"kind":"meeting"`, true, recordingwire.TranscriptionDevice, "", "", false},
		{"a note", `"kind":"note","transcription":"hub","timeline":{"roles":[{"at":0,"attendance":"listener"}]}`, true, recordingwire.TranscriptionDevice, recordingwire.TranscriptionNotMeeting, "", false},
		{"a note asking device", `"kind":"note","transcription":"device"`, true, recordingwire.TranscriptionDevice, "", "", false},
		{"a day", `"kind":"day","day":"2026-11-10","tz":"Asia/Shanghai","transcription":"hub","timeline":{"speaking":[{"start":1,"end":2}]}`, true, recordingwire.TranscriptionDevice, recordingwire.TranscriptionNotMeeting, "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := goldenEngine(t, nil)
			asked := 0
			e.TranscriptionReady = func() bool { asked++; return c.ready }
			u := roomUploadJSON(t, `{"revision":1,"started_at":"`+start+`","device":"phone","segments":[],`+c.extra+`}`)
			put, err := e.PutRecording(recID, u, now)
			if err != nil || put.Status != recordingwire.RecordingStored || put.Transcription != c.mode || put.TranscriptionProblem != c.problem {
				t.Fatalf("put %+v %v", put, err)
			}
			got := ""
			if tr := transcriptionNow(t, e, recID); tr != nil {
				got = tr.Mode
				if tr.Timeline != nil {
					t.Errorf("timeline stored: %+v", tr.Timeline)
				}
			}
			if got != c.stored {
				t.Fatalf("stored mode %q, want %q", got, c.stored)
			}
			if (asked == 1) != c.asks || asked > 1 {
				t.Errorf("readiness asked %d times", asked)
			}
		})
	}
}

// The mode of the first upload that names one stays for the recording's life: later uploads naming another, or the hub
// becoming ready, change nothing, and the 200 says the mode in effect with why it is not the one asked for.
func TestTranscriptionModeLocked(t *testing.T) {
	now := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	start := now.Add(-time.Hour)
	ready := false
	e := goldenEngine(t, nil)
	e.TranscriptionReady = func() bool { return ready }
	u := hubUpload(1, start, false, nil)
	if put, _ := e.PutRecording(recID, u, now); put.Transcription != recordingwire.TranscriptionDevice || put.TranscriptionProblem != recordingwire.TranscriptionUnavailable {
		t.Fatalf("not ready %+v", put)
	}
	ready = true
	if put, _ := e.PutRecording(recID, hubUpload(2, start, false, nil), now); put.Transcription != recordingwire.TranscriptionDevice || put.TranscriptionProblem != recordingwire.TranscriptionUnavailable {
		t.Fatalf("ready later %+v", put)
	}
	// A stale upload asking hub hears the mode in effect too.
	if put, _ := e.PutRecording(recID, hubUpload(1, start, false, nil), now); put.Status != recordingwire.RecordingStale || put.Transcription != recordingwire.TranscriptionDevice {
		t.Fatalf("stale %+v", put)
	}
	if tr := transcriptionNow(t, e, recID); tr.Mode != recordingwire.TranscriptionDevice || tr.State != "" {
		t.Fatalf("stored %+v", tr)
	}

	// Hub decided: an upload naming device or none stays hub, with no problem.
	const other = "5b6c3f4a-1d2e-4f80-9a1b-2c3d4e5f6a7b"
	putRecording(t, e, other, hubUpload(1, start, false, nil), now)
	dev := recUpload(2, start, false)
	dev.Kind = RecordingMeeting
	dev.SetTranscription(recordingwire.TranscriptionDevice, nil)
	if put, _ := e.PutRecording(other, dev, now); put.Transcription != TranscriptionHub || put.TranscriptionProblem != "" {
		t.Fatalf("device after hub %+v", put)
	}
	if tr := transcriptionNow(t, e, other); tr.Mode != TranscriptionHub || tr.State != recordingwire.TranscriptionLive {
		t.Fatalf("hub stored %+v", tr)
	}
}

// meeting.json keeps the transcription, never meta.json: after the uploads, the manager's changes and the trim.
func TestTranscriptionNeverInMeta(t *testing.T) {
	now := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	e, _ := hubEngine(t, nil)
	tl := &recordingwire.Timeline{Roles: []recordingwire.TimelineRole{{At: 0, Attendance: recordingwire.AttendanceListener}}, Speaking: spk(3, 4)}
	noMeta := func(step string) {
		t.Helper()
		for _, k := range []string{"transcription", "timeline", "mode", "gaps"} {
			if _, ok := metaKeys(t, e, recID)[k]; ok {
				t.Errorf("%s: meta.json carries %s", step, k)
			}
		}
	}
	putRecording(t, e, recID, hubUpload(1, now.Add(-time.Hour), true, tl, "第一句"), now)
	noMeta("upload")
	if _, err := e.TranscriptionUpdate(recID, func(t *MeetingTranscription, _ Recording) error {
		t.State, t.Problem = recordingwire.TranscriptionFinal, "upstream_failed"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	noMeta("update")
	putMeetingMinutes(t, e, recID, Minutes{Version: 1, Title: "Design review", When: "w"})
	trimRecording(t, e, recID, now)
	noMeta("trim")
}

// Every hub-mode upload adds its timeline to the union (the complete one too), never a stale one, never in device
// mode; the first complete upload sets CompleteAt and the end from its duration, unless the socket gave the end.
func TestTranscriptionUploadTimeline(t *testing.T) {
	now := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	start := now.Add(-time.Hour)
	e, changed := hubEngine(t, nil)
	putRecording(t, e, recID, hubUpload(1, start, false, &recordingwire.Timeline{Roles: []recordingwire.TimelineRole{{At: 0, Attendance: recordingwire.AttendanceListener}}}), now)
	if len(*changed) != 1 || !slices.Contains((*changed)[0], recordingwire.PartTranscription) {
		t.Fatalf("first upload told %v", *changed)
	}
	putRecording(t, e, recID, hubUpload(3, start, false, &recordingwire.Timeline{Speaking: spk(5, 6)}), now)
	if put, _ := e.PutRecording(recID, hubUpload(2, start, false, &recordingwire.Timeline{Speaking: spk(50, 60)}), now); put.Status != recordingwire.RecordingStale {
		t.Fatalf("stale %+v", put)
	}
	tr := transcriptionNow(t, e, recID)
	if !timelineEqual(*tr.Timeline, recordingwire.Timeline{Roles: []recordingwire.TimelineRole{{At: 0, Attendance: recordingwire.AttendanceListener}}, Speaking: spk(5, 6)}) {
		t.Fatalf("union %+v", tr.Timeline)
	}
	if len(*changed) != 1 {
		t.Fatalf("a timeline alone moved the transcription part: %v", *changed)
	}
	if !tr.CompleteAt.IsZero() || tr.End != 0 {
		t.Fatalf("not complete yet %+v", tr)
	}
	u := hubUpload(4, start, true, &recordingwire.Timeline{Speaking: spk(6, 7)}, "一句")
	u.Duration = 3733.5
	putRecording(t, e, recID, u, now)
	tr = transcriptionNow(t, e, recID)
	if !tr.CompleteAt.Equal(now) || tr.End != 3733.5 || !slices.Equal(tr.Timeline.Speaking, spk(5, 7)) {
		t.Fatalf("complete %+v", tr)
	}
	// The socket's end wins over a later complete upload's duration, and CompleteAt stays the first one's.
	if _, err := e.TranscriptionUpdate(recID, func(t *MeetingTranscription, _ Recording) error { t.End = 3700; return nil }); err != nil {
		t.Fatal(err)
	}
	u = hubUpload(5, start, true, nil, "一句")
	u.Duration = 3800
	putRecording(t, e, recID, u, now.Add(time.Minute))
	if tr = transcriptionNow(t, e, recID); tr.End != 3700 || !tr.CompleteAt.Equal(now) {
		t.Fatalf("end %+v", tr)
	}

	// Device mode keeps no timeline.
	const other = "5b6c3f4a-1d2e-4f80-9a1b-2c3d4e5f6a7b"
	dev := recUpload(1, start, true)
	dev.Kind = RecordingMeeting
	dev.SetTranscription(recordingwire.TranscriptionDevice, &recordingwire.Timeline{Speaking: spk(1, 2)})
	putRecording(t, e, other, dev, now)
	if tr := transcriptionNow(t, e, other); tr.Mode != recordingwire.TranscriptionDevice || tr.Timeline != nil || !tr.CompleteAt.IsZero() {
		t.Fatalf("device %+v", tr)
	}
}

// TranscriptionUpdate changes a hub-mode recording's transcription: never its mode, PartTranscription only for what
// the apps read (mode, state, gaps, problem, fallback), FinalAt and FinalFrom once final; ErrNotHub otherwise, and the
// contract's refusals.
func TestTranscriptionUpdate(t *testing.T) {
	now := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	e, changed := hubEngine(t, nil)
	clock := now
	e.SetClock(func() time.Time { return clock })
	if _, err := e.TranscriptionUpdate(recID, func(*MeetingTranscription, Recording) error { return nil }); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no upload: %v", err)
	}
	putRecording(t, e, recID, hubUpload(1, now.Add(-time.Hour), false, nil), now)
	*changed = nil
	parts := func() []string {
		var all []string
		for _, p := range *changed {
			all = append(all, p...)
		}
		*changed = nil
		return all
	}
	upd := func(fn func(t *MeetingTranscription)) MeetingTranscription {
		t.Helper()
		tr, err := e.TranscriptionUpdate(recID, func(t *MeetingTranscription, _ Recording) error { fn(t); return nil })
		if err != nil {
			t.Fatal(err)
		}
		return tr
	}
	upd(func(t *MeetingTranscription) {
		t.Mode = recordingwire.TranscriptionDevice
		t.UsageSeconds = 12
		t.FinalTries = 1
	})
	if tr := transcriptionNow(t, e, recID); tr.Mode != TranscriptionHub || tr.UsageSeconds != 12 {
		t.Fatalf("mode changed or bookkeeping lost: %+v", tr)
	}
	if p := parts(); len(p) != 0 {
		t.Fatalf("bookkeeping moved %v", p)
	}
	upd(func(t *MeetingTranscription) { t.Timeline = &recordingwire.Timeline{Speaking: spk(1, 2)} })
	if p := parts(); len(p) != 0 {
		t.Fatalf("the timeline moved %v", p)
	}
	for _, fn := range []func(t *MeetingTranscription){
		func(t *MeetingTranscription) { t.Problem = "upstream_failed" },
		func(t *MeetingTranscription) { t.Fallback = true },
		func(t *MeetingTranscription) { t.State = recordingwire.TranscriptionFinishing },
	} {
		upd(fn)
		if p := parts(); !slices.Equal(p, []string{recordingwire.PartTranscription}) {
			t.Fatalf("told %v", p)
		}
	}
	m, _, _ := e.MeetingRead(recID)
	if m.Revs[recordingwire.PartTranscription] != 4 {
		t.Fatalf("revs %v", m.Revs)
	}
	tr := upd(func(t *MeetingTranscription) { t.State = recordingwire.TranscriptionFinal })
	if !tr.FinalAt.Equal(now) || !tr.FinalFrom.Equal(now) {
		t.Fatalf("final %+v", tr)
	}
	// Device mode, and an unknown mode, take no update.
	const other = "5b6c3f4a-1d2e-4f80-9a1b-2c3d4e5f6a7b"
	dev := recUpload(1, now.Add(-time.Hour), false)
	dev.Kind = RecordingMeeting
	dev.SetTranscription(recordingwire.TranscriptionDevice, nil)
	putRecording(t, e, other, dev, now)
	if _, err := e.TranscriptionUpdate(other, func(*MeetingTranscription, Recording) error { return nil }); !errors.Is(err, ErrNotHub) {
		t.Fatalf("device: %v", err)
	}
	if _, err := e.DeleteRecording(other, now); err != nil {
		t.Fatal(err)
	}
	if _, err := e.TranscriptionUpdate(other, func(*MeetingTranscription, Recording) error { return nil }); !errors.Is(err, ErrRecordingDeleted) {
		t.Fatalf("deleted: %v", err)
	}
}

// transcription in detail.meta: {"mode": "device"} in device mode; in hub mode its state and gaps, always present.
// Recordings whose mode was never decided have none. HubRecordings lists those the manager still has work for.
func TestTranscriptionDetailAndHubRecordings(t *testing.T) {
	now := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	e, _ := hubEngine(t, nil)
	const dev, none = "5b6c3f4a-1d2e-4f80-9a1b-2c3d4e5f6a7b", "6c7d4e5b-2e3f-4a91-8b2c-3d4e5f6a7b8c"
	putRecording(t, e, recID, hubUpload(1, now.Add(-time.Hour), false, nil, "一句"), now)
	u := meetingUpload(1, now.Add(-2*time.Hour), "一句")
	u.SetTranscription(recordingwire.TranscriptionDevice, nil)
	putRecording(t, e, dev, u, now)
	putRecording(t, e, none, meetingUpload(1, now.Add(-3*time.Hour), "一句"), now)
	meta := func(id string) map[string]json.RawMessage {
		t.Helper()
		d, err := e.Get(KindRecording, id, 0)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(d.Meta, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if got := string(meta(recID)["transcription"]); got != `{"mode":"hub","state":"live","gaps":[]}` {
		t.Errorf("hub %s", got)
	}
	if got := string(meta(dev)["transcription"]); got != `{"mode":"device"}` {
		t.Errorf("device %s", got)
	}
	if _, ok := meta(none)["transcription"]; ok {
		t.Error("undecided recording has a transcription")
	}
	if ids, _ := e.HubRecordings(); !slices.Equal(ids, []string{recID}) {
		t.Fatalf("hub recordings %v", ids)
	}
	// Final with its audio gone: the manager is done with it.
	if _, err := e.TranscriptionUpdate(recID, func(t *MeetingTranscription, _ Recording) error {
		t.State, t.Gaps, t.Problem = recordingwire.TranscriptionFinal, [][2]float64{{0, 4.2}}, "upstream_failed"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := string(meta(recID)["transcription"]); got != `{"mode":"hub","state":"final","gaps":[[0,4.2]],"problem":"upstream_failed"}` {
		t.Errorf("final %s", got)
	}
	if ids, _ := e.HubRecordings(); !slices.Equal(ids, []string{recID}) {
		t.Fatalf("final, audio not gone: %v", ids)
	}
	if err := e.AudioDelete(recID); err != nil {
		t.Fatal(err)
	}
	if ids, _ := e.HubRecordings(); len(ids) != 0 {
		t.Fatalf("done: %v", ids)
	}
	// The view: the gaps meeting.json keeps once the final pass started, whatever the caller passes.
	m, _, _ := e.MeetingRead(recID)
	if v := m.TranscriptionView([][2]float64{{1, 2}}); v == nil || !sameSpans(*v.Gaps, [][2]float64{{0, 4.2}}) {
		t.Fatalf("view %+v", v)
	}
	if v := (Meeting{}).TranscriptionView(nil); v != nil {
		t.Fatalf("no mode: %+v", v)
	}
	if v := (Meeting{Transcription: &MeetingTranscription{Mode: TranscriptionHub}}).TranscriptionView([][2]float64{{1, 2}}); v.State != recordingwire.TranscriptionLive || !sameSpans(*v.Gaps, [][2]float64{{1, 2}}) {
		t.Fatalf("live view %+v", v)
	}
	if _, err := os.Stat(e.Store.Path(KindRecording, recID, meetingFile)); err != nil {
		t.Fatal(err)
	}
}

// In hub mode the live log is the hub's: the phone's POST …/live is refused (after not_attending), and the hub's
// sentences are appended at the end, each with its source, within the live log's limits; LiveChanged and
// MeetingChanged (lines) hear of them.
func TestLiveInHubMode(t *testing.T) {
	now := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	e, changed := hubEngine(t, nil)
	kicked := 0
	e.OnLive(func(string) { kicked++ })
	if _, err := e.LiveAppendHub(recID, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no upload: %v", err)
	}
	putRecording(t, e, recID, hubUpload(1, now.Add(-time.Hour), false, nil), now)
	if _, _, err := e.LiveAppend(recID, 0, []LiveSegment{{Text: "x"}}); !errors.Is(err, recordingwire.ErrNotAttending) {
		t.Fatalf("not attended: %v", err)
	}
	u := hubUpload(2, now.Add(-time.Hour), false, nil)
	u.SetRoom(recordingwire.AttendanceParticipant)
	putRecording(t, e, recID, u, now)
	if _, _, err := e.LiveAppend(recID, 0, []LiveSegment{{Text: "x"}}); !errors.Is(err, recordingwire.ErrHubTranscribes) {
		t.Fatalf("attended, hub mode: %v", err)
	}
	*changed, kicked = nil, 0
	first, err := e.LiveAppendHub(recID, []LiveSegment{{Start: 1, End: 3, Text: "我们先把\nexport cap 保持在 4 MB。"}, {Start: 4, End: 5, Text: "Bo", Source: recordingwire.SourceDevice}})
	if err != nil || first != 0 {
		t.Fatalf("append %d %v", first, err)
	}
	if first, err = e.LiveAppendHub(recID, []LiveSegment{{Start: 6, End: 7, Text: "next", Source: "other"}}); err != nil || first != 2 {
		t.Fatalf("append again %d %v", first, err)
	}
	if first, err = e.LiveAppendHub(recID, nil); err != nil || first != 3 {
		t.Fatalf("append nothing %d %v", first, err)
	}
	l, _, _ := e.LiveRead(recID)
	if len(l.Segments) != 3 || l.Segments[0].Text != "我们先把 export cap 保持在 4 MB。" || l.Segments[0].Source != SourceHub ||
		l.Segments[1].Source != recordingwire.SourceDevice || l.Segments[2].Source != SourceHub {
		t.Fatalf("live log %+v", l.Segments)
	}
	if kicked != 2 || len(*changed) != 2 || !slices.Equal((*changed)[0], []string{recordingwire.PartLines}) {
		t.Fatalf("kicked %d, told %v", kicked, *changed)
	}
	big := make([]LiveSegment, maxLivePositions)
	if _, err := e.LiveAppendHub(recID, big); !errors.Is(err, recordingwire.ErrLiveFull) {
		t.Fatalf("full: %v", err)
	}
	if l, _, _ := e.LiveRead(recID); len(l.Segments) != 3 || !l.Full {
		t.Fatalf("after full %d %v", len(l.Segments), l.Full)
	}

	// Not hub, trimmed, deleted.
	const dev = "5b6c3f4a-1d2e-4f80-9a1b-2c3d4e5f6a7b"
	du := attendedUpload(1, now.Add(-time.Hour), recordingwire.AttendanceListener)
	du.SetTranscription(recordingwire.TranscriptionDevice, nil)
	putRecording(t, e, dev, du, now)
	if _, err := e.LiveAppendHub(dev, []LiveSegment{{Text: "x"}}); !errors.Is(err, ErrNotHub) {
		t.Fatalf("device: %v", err)
	}
	if _, _, err := e.LiveAppend(dev, 0, []LiveSegment{{Text: "x"}}); err != nil {
		t.Fatalf("device mode, the phone's: %v", err)
	}
	putMeetingMinutes(t, e, recID, Minutes{Version: 1, Title: "Design review", When: "w"})
	trimRecording(t, e, recID, now)
	if _, err := e.LiveAppendHub(recID, []LiveSegment{{Text: "x"}}); !errors.Is(err, ErrTranscriptExpired) {
		t.Fatalf("trimmed: %v", err)
	}
	if _, err := e.DeleteRecording(recID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := e.LiveAppendHub(recID, []LiveSegment{{Text: "x"}}); !errors.Is(err, ErrRecordingDeleted) {
		t.Fatalf("deleted: %v", err)
	}
}

// PutRecording never changes the caller's upload (its segments' backing array included) and returns the segments as
// stored: trimmed, the empty and blank ones dropped, in order.
func TestPutRecordingLeavesTheUploadAlone(t *testing.T) {
	now := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	e, _ := hubEngine(t, nil)
	u := hubUpload(1, now.Add(-time.Hour), false, nil)
	u.Segments = []RecordingSegment{{Start: 6, End: 9, Text: "Two."}, {Start: 0, End: 3, Text: " One. "}, {Start: 3, End: 6, Text: ""},
		{Start: 9, End: 10, Text: "   "}, {Start: 10, End: 12, Text: "Three."}}
	sent := slices.Clone(u.Segments)
	put, err := e.PutRecording(recID, u, now)
	if err != nil || put.Status != recordingwire.RecordingStored {
		t.Fatalf("put %v %v", put, err)
	}
	if !slices.Equal(u.Segments, sent) {
		t.Fatalf("the caller's segments changed: %+v", u.Segments)
	}
	var got []string
	for _, sg := range put.Segments {
		got = append(got, sg.Text)
	}
	if !slices.Equal(got, []string{"One.", "Two.", "Three."}) {
		t.Fatalf("stored segments %q", got)
	}
}

// A phone killed mid-segment loses the segment it was writing and uploads its end before it, while the hub got that
// audio over the socket (#9587): an ended hub-mode meeting's duration, ended_at and the transcription's end are the
// longer of the upload's and the audio held. The upload's stand when they are longer; a device-mode meeting, whose
// audio the hub never holds, keeps the upload's.
func TestHubMeetingEndsNoEarlierThanItsAudio(t *testing.T) {
	now := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	start := now.Add(-time.Hour).UTC()
	at := func(sec float64) time.Time { return start.Add(time.Duration(sec * float64(time.Second))) }
	for _, c := range []struct {
		name                 string
		uploaded, held, want float64
	}{
		{"the hub holds more", 24, 25.4, 25.4},
		{"the upload is longer", 24, 20, 24},
		{"nothing held", 24, 0, 24},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, _ := hubEngine(t, nil)
			hubMeeting(t, e, recID, now)
			if c.held > 0 {
				if _, err := e.AudioPut(recID, 0, pcmAt(0, secs(c.held))); err != nil {
					t.Fatal(err)
				}
			}
			u := hubUpload(2, start, true, nil, "一句", "两句")
			u.Duration = c.uploaded
			end := at(c.uploaded)
			u.EndedAt = &end
			putRecording(t, e, recID, u, now)
			m, r, err := e.MeetingRead(recID)
			if err != nil {
				t.Fatal(err)
			}
			if r.Duration != c.want || r.EndedAt == nil || !r.EndedAt.Equal(at(c.want)) {
				t.Fatalf("duration %v ended %v, want %v", r.Duration, r.EndedAt, c.want)
			}
			if m.Transcription == nil || m.Transcription.End != c.want {
				t.Fatalf("transcription end %+v, want %v", m.Transcription, c.want)
			}
			// A later upload of the same short length keeps what the audio says.
			u.Revision = 3
			putRecording(t, e, recID, u, now.Add(time.Minute))
			if _, r, _ = e.MeetingRead(recID); r.Duration != c.want {
				t.Fatalf("a later upload took it back to %v", r.Duration)
			}
		})
	}
	t.Run("device mode", func(t *testing.T) {
		e, _ := hubEngine(t, nil)
		u := recUpload(1, start, true, "一句", "两句")
		u.Kind = RecordingMeeting
		u.SetTranscription(recordingwire.TranscriptionDevice, nil)
		u.Duration = 24
		end := at(24)
		u.EndedAt = &end
		putRecording(t, e, recID, u, now)
		if _, r, err := e.MeetingRead(recID); err != nil || r.Duration != 24 || !r.EndedAt.Equal(end) {
			t.Fatalf("device mode: duration %v ended %v %v", r.Duration, r.EndedAt, err)
		}
	})
}

// Through an upload, a timeline over the limits is none and the upload stands (the timeline's own cases are
// recordingwire's TestParseTimeline).
func TestUploadIgnoresATimelineOverTheLimits(t *testing.T) {
	roles := func(n int) string {
		var parts []string
		for i := range n {
			parts = append(parts, `{"at":`+strconv.Itoa(i)+`,"attendance":"listener"}`)
		}
		return `{"roles":[` + strings.Join(parts, ",") + `]}`
	}
	now := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	e, _ := hubEngine(t, nil)
	u := roomUploadJSON(t, `{"revision":1,"started_at":"`+now.Add(-time.Hour).UTC().Format(time.RFC3339)+`","device":"phone","segments":[],"kind":"meeting","transcription":"hub","timeline":`+roles(101)+`}`)
	if put, err := e.PutRecording(recID, u, now); err != nil || put.Status != recordingwire.RecordingStored || put.Transcription != TranscriptionHub {
		t.Fatalf("put %+v %v", put, err)
	}
	if tr := transcriptionNow(t, e, recID); tr.Timeline != nil {
		t.Fatalf("a timeline over the limits was kept: %+v", tr.Timeline)
	}
}

func timelineEqual(a, b recordingwire.Timeline) bool {
	return slices.Equal(a.Roles, b.Roles) && len(a.Speaking) == len(b.Speaking) && (len(a.Speaking) == 0 || slices.Equal(a.Speaking, b.Speaking))
}

// spk is the speaking spans of pairs of numbers (start, end), without text.
func spk(pairs ...float64) []recordingwire.SpokenSpan {
	var out []recordingwire.SpokenSpan
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, recordingwire.SpokenSpan{Start: pairs[i], End: pairs[i+1]})
	}
	return out
}
