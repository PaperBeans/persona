package writers

import (
	"context"
	"errors"
	"github.com/nwverse/persona/server/internal/recordingwire"
	"strings"
	"testing"
	"time"
)

// A recording nobody named gets a live title once it has enough text, another only when the text has doubled and ten
// minutes have passed (the model may keep the one it has), at most four; the summary then settles it, and a meeting's
// minutes over that, which a later summary does not undo.
func TestLiveTitleWrittenRefreshedAndSettled(t *testing.T) {
	m := &titleModel{replies: []string{`{"title":"「导出上限」","keep":false}`}}
	e := goldenEngine(t, m)
	start := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	now := start.Add(time.Minute)
	e.SetClock(func() time.Time { return now })
	ctx := context.Background()

	// Too little said: nothing due, the name is the start time, and the hub says no name.
	u := recUpload(1, start, false, talk(0, 3)...)
	u.Kind = recordingwire.RecordingMeeting
	put, err := e.PutRecording(recID, u, now)
	if err != nil || put.TitleDue || put.Name != "" || put.NameSource != "" {
		t.Fatalf("a short start: %+v %v", put, err)
	}
	if n := e.TitleRecordings(ctx, 2); n != 0 || len(m.calls) != 0 {
		t.Fatalf("titled a short start: %d", n)
	}
	// Enough: due, one call, its title (quotes gone) names it in lists and in the answers.
	u = recUpload(2, start, false, talk(0, 12)...)
	u.Kind = recordingwire.RecordingMeeting
	if put, _ = e.PutRecording(recID, u, now); !put.TitleDue {
		t.Fatalf("not due at %d chars", recordingNow(t, e, recID).Chars)
	}
	if n := e.TitleRecordings(ctx, 2); n != 1 {
		t.Fatalf("calls %d, want 1", n)
	}
	r := recordingNow(t, e, recID)
	if r.Name != "导出上限" || r.AITitleFrom != AITitleLive || r.NameSource() != recordingwire.NamedByModel || r.LiveTitles != 1 {
		t.Fatalf("after the first title: %q from %q (%s), %d calls", r.Name, r.AITitleFrom, r.NameSource(), r.LiveTitles)
	}
	if !strings.Contains(m.calls[0][1], "一场会议") || strings.Contains(m.calls[0][1], "现在的标题") {
		t.Errorf("the first call's input:\n%s", m.calls[0][1])
	}
	states, _ := e.RecordingStates()
	if len(states) != 1 || states[0].Name != "导出上限" || states[0].NameSource != recordingwire.NamedByModel {
		t.Fatalf("states %+v", states)
	}
	d, err := e.Get(KindRecording, recID, 100)
	if err != nil || !strings.Contains(string(d.Meta), `"name_source":"model"`) {
		t.Fatalf("detail.meta: %s %v", d.Meta, err)
	}
	if put, _ = e.PutRecording(recID, u, now); put.Name != "导出上限" || put.NameSource != recordingwire.NamedByModel || put.TitleDue {
		t.Fatalf("the answer after: %+v", put)
	}

	// Doubled but too soon: not asked. Ten minutes on: asked with the title it has, which it keeps.
	u = recUpload(3, start, false, talk(0, 25)...)
	u.Kind = recordingwire.RecordingMeeting
	now = start.Add(5 * time.Minute)
	putRecording(t, e, recID, u, now)
	if n := e.TitleRecordings(ctx, 2); n != 0 {
		t.Fatalf("asked again after 4 minutes: %d", n)
	}
	now = start.Add(12 * time.Minute)
	putRecording(t, e, recID, u, now)
	m.replies = []string{`{"title":"导出上限","keep":true}`}
	if n := e.TitleRecordings(ctx, 2); n != 1 || !strings.Contains(m.calls[1][1], "现在的标题：「导出上限」") {
		t.Fatalf("second call %d:\n%s", n, m.calls[len(m.calls)-1][1])
	}
	// Ten more minutes but the same text: not doubled, not asked.
	now = start.Add(25 * time.Minute)
	putRecording(t, e, recID, u, now)
	if n := e.TitleRecordings(ctx, 2); n != 0 {
		t.Fatalf("asked without new text: %d", n)
	}
	// The talk moves on: a new title.
	m.replies = []string{`{"title":"发布日期","keep":false}`}
	u = recUpload(4, start, false, talk(0, 60)...)
	u.Kind = recordingwire.RecordingMeeting
	putRecording(t, e, recID, u, now)
	if n := e.TitleRecordings(ctx, 2); n != 1 || recordingNow(t, e, recID).Name != "发布日期" {
		t.Fatalf("third call %d, name %q", n, recordingNow(t, e, recID).Name)
	}
	// A failure counts: the fourth is the last.
	m.errs = []error{nil, nil, nil, errors.New("unusable")}
	u = recUpload(5, start, false, talk(0, 130)...)
	u.Kind = recordingwire.RecordingMeeting
	now = start.Add(40 * time.Minute)
	putRecording(t, e, recID, u, now)
	if n := e.TitleRecordings(ctx, 2); n != 1 {
		t.Fatalf("fourth call %d", n)
	}
	if r := recordingNow(t, e, recID); r.Name != "发布日期" || r.LiveTitles != liveTitleMax {
		t.Fatalf("after a failure: %q, %d calls", r.Name, r.LiveTitles)
	}
	u = recUpload(6, start, false, talk(0, 300)...)
	u.Kind = recordingwire.RecordingMeeting
	now = start.Add(60 * time.Minute)
	if put, _ = e.PutRecording(recID, u, now); put.TitleDue {
		t.Fatal("due after four calls")
	}

	// Complete: the summary's title settles it, and the minutes' over that.
	sum := &captureModel{reply: `{"title":"导出评审","summary":"## 这是什么\n评审","one_line":"导出","importance":"P2","relevance":50,"tags":["导出"]}`}
	e.Model = sum
	u = recUpload(7, start, true, talk(0, 20)...)
	u.Kind = recordingwire.RecordingMeeting
	putRecording(t, e, recID, u, now)
	if n := e.SummarizeRecordings(ctx, 3); n != 1 {
		t.Fatalf("summarized %d", n)
	}
	if r := recordingNow(t, e, recID); r.Name != "导出评审" || r.AITitleFrom != AITitleSummary {
		t.Fatalf("after the summary: %q from %q", r.Name, r.AITitleFrom)
	}
	e.Model = &captureModel{reply: minutesReply}
	if n := e.WriteMinutes(ctx, 2); n != 1 {
		t.Fatalf("minutes %d", n)
	}
	if r := recordingNow(t, e, recID); r.Name != "发布评审" || r.AITitleFrom != AITitleMinutes || r.NameSource() != recordingwire.NamedByModel {
		t.Fatalf("after the minutes: %q from %q", r.Name, r.AITitleFrom)
	}
	e.Model = sum
	u.Revision = 8
	putRecording(t, e, recID, u, now)
	if n := e.SummarizeRecordings(ctx, 3); n != 1 || recordingNow(t, e, recID).Name != "发布评审" {
		t.Fatalf("a later summary renamed it: %d %q", n, recordingNow(t, e, recID).Name)
	}
}
