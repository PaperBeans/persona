package writers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/recordingwire"
)

// titleModel answers each call with the next reply and keeps what it was asked; an error reply is returned instead.
type titleModel struct {
	replies []string
	errs    []error
	calls   [][2]string
}

func (m *titleModel) JSONMax(_ context.Context, system, user string, _ int, out any) error {
	m.calls = append(m.calls, [2]string{system, user})
	i := len(m.calls) - 1
	if i < len(m.errs) && m.errs[i] != nil {
		return m.errs[i]
	}
	reply := m.replies[min(i, len(m.replies)-1)]
	return json.Unmarshal([]byte(reply), out)
}

// talk is n lines of about 50 characters each, numbered so each upload's text is new.
func talk(from, n int) []string {
	var out []string
	for i := from; i < from+n; i++ {
		out = append(out, strings.Repeat("导出上限按条还是按录音算要先定下来，", 2)+"第"+string(rune('A'+i%26))+"段")
	}
	return out
}

// The owner's title and the calendar's come first: no live title is asked for, the hub says where the name comes
// from, and a title the phone sends later wins over one the model wrote.
func TestLiveTitleNeverOverTheOwnersOrTheCalendars(t *testing.T) {
	m := &titleModel{replies: []string{`{"title":"导出上限","keep":false}`}}
	e := goldenEngine(t, m)
	start := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	now := start.Add(5 * time.Minute)
	e.SetClock(func() time.Time { return now })
	ctx := context.Background()

	u := recUpload(1, start, false, talk(0, 20)...)
	u.Calendar = &recordingwire.RecordingCalendar{Title: "周会"}
	u.Title = "周会"
	if put, _ := e.PutRecording(recID, u, now); put.TitleDue || put.Name != "周会" || put.NameSource != recordingwire.NamedByCalendar {
		t.Fatalf("calendar: %+v", put)
	}
	u.Revision, u.Title = 2, "我的标题"
	if put, _ := e.PutRecording(recID, u, now); put.TitleDue || put.Name != "我的标题" || put.NameSource != recordingwire.NamedByTitle {
		t.Fatalf("the owner's: %+v", put)
	}
	if n := e.TitleRecordings(ctx, 2); n != 0 {
		t.Fatalf("titled a named recording: %d", n)
	}
	// Named by the model first, then by the owner: the owner's.
	const other = "1f8fad5b-d9cb-469f-a165-70867728950e"
	putRecording(t, e, other, recUpload(1, start, false, talk(0, 20)...), now)
	if n := e.TitleRecordings(ctx, 2); n != 1 || recordingNow(t, e, other).Name != "导出上限" {
		t.Fatalf("live title %d %q", n, recordingNow(t, e, other).Name)
	}
	u = recUpload(2, start, false, talk(0, 20)...)
	u.Title = "改过的名字"
	if put, _ := e.PutRecording(other, u, now); put.Name != "改过的名字" || put.NameSource != recordingwire.NamedByTitle || put.TitleDue {
		t.Fatalf("renamed: %+v", put)
	}
	// An all-day recording keeps its day's name.
	if liveTitleDue(&Recording{Kind: RecordingDay, AITitle: "x", Chars: 5000}, now) {
		t.Error("a day recording is due a live title")
	}
	// A lost phone's recording is left as it is.
	if liveTitleDue(&Recording{Chars: 5000, Updated: now.Add(-13 * time.Hour)}, now) {
		t.Error("a recording with no upload for 13 hours is due a live title")
	}
}

// What the model is asked, in Chinese and for a recording made in English; a long transcript sends its start and its
// latest part, and a paused connection is not counted as a call.
func TestGoldenLiveTitle(t *testing.T) {
	m := &captureModel{reply: `{"title":"导出上限","keep":false}`}
	e := goldenEngine(t, m)
	start := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	now := start.Add(3 * time.Minute)
	e.SetClock(func() time.Time { return now })
	u := recUpload(1, start, false, "大家好，今天评审导出", "Ada 说导出超过 4 MB 的条目会被丢掉", strings.Repeat("上限这一版先保留，", 50))
	u.Duration = 180
	putRecording(t, e, recID, u, now)
	if n := e.TitleRecordings(context.Background(), 2); n != 1 {
		t.Fatalf("calls %d", n)
	}
	m.golden(t, "recording_title")

	const en = "1f8fad5b-d9cb-469f-a165-70867728950e"
	u = recUpload(1, start, false, "Hi all, the export review", strings.Repeat("The cap stays for this release. ", 20))
	u.Language, u.Kind = "en-US", RecordingMeeting
	putRecording(t, e, en, u, now)
	if n := e.TitleRecordings(context.Background(), 2); n != 1 {
		t.Fatalf("calls %d", n)
	}
	golden(t, "recording_title.system.en", m.calls[0][0])
	if !strings.Contains(m.calls[0][0], "都用English写") {
		t.Errorf("not asked in English:\n%s", m.calls[0][0])
	}
	m.calls = nil

	// A paused connection puts it off without counting a call.
	const paused = "2f8fad5b-d9cb-469f-a165-70867728950e"
	putRecording(t, e, paused, recUpload(1, start, false, talk(0, 20)...), now)
	// The model client's paused connection is the knowledge base's to tell (recordingwire.Writer.Deferred; its own
	// test is TestRecordingCoreDefersAPausedModel): here the port says so of this error.
	errPaused := errors.New("paused until the quota is back")
	e.Model = &titleModel{errs: []error{errPaused}}
	New(deferring{Core: kb.RecordingCore(e.Engine), err: errPaused}, nil).TitleRecordings(context.Background(), 2)
	if r := recordingNow(t, e, paused); r.LiveTitles != 0 || !liveTitleDue(r, now) {
		t.Errorf("after a pause: %d calls, due %v", r.LiveTitles, liveTitleDue(r, now))
	}

	if got := headAndTail(strings.Repeat("a", 10)+strings.Repeat("b", 10), 3, 4); got != "aaa\n…（中间省略）…\nbbbb" {
		t.Errorf("headAndTail %q", got)
	}
	if got := titleLine("  “**导出**  上限” "); got != "导出 上限" {
		t.Errorf("titleLine %q", got)
	}
	if got := titleLine(strings.Repeat("长", 80)); len([]rune(got)) != titleRunes || !strings.HasSuffix(got, "…") {
		t.Errorf("a long title: %q", got)
	}
}

// A name given on the web (#8877) names the recording before the phone's title and the generated one, and stops a live
// title; an upload with the phone's title unchanged keeps it, one that changes it (renamed on the phone since) takes it
// back; "" takes it back too. Errors: not one line or too long, no recording, a deleted one.
func TestWebRename(t *testing.T) {
	e := goldenEngine(t, &titleModel{replies: []string{`{"title":"导出上限","keep":false}`}})
	start := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	now := start.Add(5 * time.Minute)
	e.SetClock(func() time.Time { return now })
	if _, err := e.RenameRecording(recID, "网页名", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no recording: %v", err)
	}
	if _, err := e.RenameRecording("nope", "网页名", now); !errors.Is(err, ErrBadRecording) {
		t.Fatalf("bad id: %v", err)
	}
	putRecording(t, e, recID, recUpload(1, start, false, talk(0, 20)...), now)
	for _, bad := range []string{"两\n行", strings.Repeat("长", 201)} {
		if _, err := e.RenameRecording(recID, bad, now); !errors.Is(err, ErrBadTitle) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	r, err := e.RenameRecording(recID, " 网页名 ", now)
	if err != nil || r.Name != "网页名" || r.NameSource() != recordingwire.NamedByRename || r.RenamedAt == nil {
		t.Fatalf("renamed: %+v %v", r, err)
	}
	if liveTitleDue(recordingNow(t, e, recID), now) {
		t.Error("a renamed recording is due a live title")
	}
	if states, _ := e.RecordingStates(); states[0].Name != "网页名" || states[0].NameSource != recordingwire.NamedByRename {
		t.Errorf("states %+v", states)
	}
	// The phone's title as it was: the web's name stays.
	if put, _ := e.PutRecording(recID, recUpload(2, start, false, talk(0, 20)...), now); put.Name != "网页名" || put.NameSource != recordingwire.NamedByRename {
		t.Fatalf("an upload with the title as it was: %+v", put)
	}
	// The phone renamed it since: its name wins and the web's goes.
	u := recUpload(3, start, false, talk(0, 20)...)
	u.Title = "手机名"
	if put, _ := e.PutRecording(recID, u, now); put.Name != "手机名" || put.NameSource != recordingwire.NamedByTitle {
		t.Fatalf("the phone's newer name: %+v", put)
	}
	if r := recordingNow(t, e, recID); r.Renamed != "" || r.RenamedAt != nil {
		t.Fatalf("the web's name stayed: %+v", r)
	}
	// Cleared on the web.
	if _, err := e.RenameRecording(recID, "网页名", now); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.RenameRecording(recID, "", now); r.Name != "手机名" || r.Renamed != "" {
		t.Fatalf("cleared: %q %q", r.Name, r.Renamed)
	}
	if _, err := e.DeleteRecording(recID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RenameRecording(recID, "网页名", now); !errors.Is(err, ErrRecordingDeleted) {
		t.Fatalf("deleted: %v", err)
	}
}

// deferring is the storage port that also says of err that it puts the work off.
type deferring struct {
	recordingwire.Core
	err error
}

func (c deferring) Deferred(ctx context.Context, err error) bool {
	return errors.Is(err, c.err) || c.Core.Deferred(ctx, err)
}
