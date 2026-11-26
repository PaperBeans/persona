package kb

import (
	"context"
	"strings"
	"testing"
	"time"
)

// mixedMeeting is a meeting said mostly in Chinese, with English and a little Japanese in it.
var mixedMeeting = []string{
	"大家好，我们先看 export 的 bug",
	"Ada said the export drops entries over 4 MB",
	"那这一版先保留上限",
	"Bo will add the regression test by Friday",
	"上限は件ごとか録音ごとか、まだ決まっていません",
}

// A recording's summary of a mixed-language recording made in English is asked for in English, its fixed sections'
// names and 「无」 too.
func TestRecordingSummaryInTheRecordingsLanguage(t *testing.T) {
	m := &captureModel{reply: `{"title":"Export review","summary":"## What this is\nAn export review.\n\n## Key points\n- 14:00 The cap stays\n\n## To-dos and open questions\nNone",` +
		`"one_line":"The export cap stays for now","importance":"P2","relevance":50,"tags":["export"]}`}
	e := goldenEngine(t, m)
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, goldenZone)
	u := recUpload(1, now.Add(-time.Hour), true, mixedMeeting...)
	u.Language = "en-US"
	putRecording(t, e, recID, u, now)
	if n := e.SummarizeRecordings(context.Background(), 3); n != 1 || len(m.calls) != 1 {
		t.Fatalf("summarized %d in %d calls", n, len(m.calls))
	}
	system := m.calls[0][0]
	for _, want := range []string{"都用English写", "不管原文里混着几种语言", "小节名和「不确定」「无」都换成English的说法"} {
		if !strings.Contains(system, want) {
			t.Errorf("the system prompt lacks %q:\n%s", want, system)
		}
	}
	if strings.Contains(system, "全部用中文") || strings.Contains(system, "用中文写") {
		t.Errorf("the system prompt still asks for Chinese:\n%s", system)
	}
	golden(t, "recording.system.en", system)
	golden(t, "recording.user.en", m.calls[0][1])
}
