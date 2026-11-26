package writers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
)

// A meeting that says no language is written in the owner's, and so is one in a language the hub has no catalog for
// (not in English); with no owner's language either it stays Chinese, its system prompt the one the zh golden has.
func TestMinutesInTheOwnersLanguage(t *testing.T) {
	zh, err := os.ReadFile(filepath.Join("testdata", "prompts", "minutes.system.golden"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, tag string
		owner     func() i18n.Lang
		ask       string
		want      []string
		code      string
	}{
		{name: "ja", owner: func() i18n.Lang { return i18n.Ja }, ask: "都用日本語写", want: []string{"## 要点\n", "- 日時："}, code: "ja"},
		{name: "unsupported", tag: "ko-KR", owner: func() i18n.Lang { return i18n.Ja }, ask: "都用日本語写",
			want: []string{"## 要点\n", "- 日時："}, code: "ja"},
		{name: "none", ask: string(zh), want: []string{"## 讨论要点\n", "- 时间："}, code: "zh-Hans"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &captureModel{reply: `{"title":"t","points":[{"text":"第一句","line":1}]}`}
			e := goldenEngine(t, m)
			e.OwnerLanguage = tc.owner
			start := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
			u := meetingUpload(1, start, mixedMeeting...)
			u.Language = tc.tag
			putRecording(t, e, recID, u, start.Add(time.Hour))
			if n := e.WriteMinutes(context.Background(), 2); n != 1 || len(m.calls) != 1 {
				t.Fatalf("wrote %d in %d calls", n, len(m.calls))
			}
			if !strings.Contains(m.calls[0][0], tc.ask) {
				t.Errorf("the system prompt lacks %q:\n%s", tc.ask, m.calls[0][0])
			}
			got, err := e.Store.ReadMinutes(recID)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.want {
				if !strings.Contains(got.Markdown, want) {
					t.Errorf("markdown lacks %q:\n%s", want, got.Markdown)
				}
			}
			if got.Language != tc.code {
				t.Errorf("language %q, want %q", got.Language, tc.code)
			}
		})
	}
}
