package kb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/nwverse/persona/server/internal/ai"
)

// Behaviour check for the language of what is written from a recording (docs/assistant/prompts.md "Behaviour checks"):
// TestMinutesLanguageLive runs one synthetic meeting that mixes Chinese, English and Japanese through the real minutes,
// summary and day recap paths, once as recorded in each of zh-CN, en-US and ja-JP, and prints how much of the written
// text is in which script. It asks a real model and runs only when PERSONA_LIVE_AI is set, never in CI:
//
//	PERSONA_LIVE_AI='{"base_url":"…","api_key_file":"…","model":"…"}' go test -run MinutesLanguageLive -v -count=1 ./internal/kb
//
// Placeholders only: the people are Ada, Bo and Cy, the work an export, a release and a cache bug.

// liveMeeting is a product sync said in three languages, each line a few seconds of the recording.
var liveMeeting = []string{
	"好，我们开始吧，今天主要看三件事：导出、发布时间、还有缓存的问题。",
	"First, the export. Ada, can you give us the status?",
	"Sure. The export drops any entry over 4 MB, and users only notice when the file is incomplete.",
	"那我们这一版先保留 4 MB 的上限，但是要在界面上提示用户。",
	"Agreed. Let's keep the cap for this release and show a warning when an entry is skipped.",
	"Bo，你周五之前把提示的文案和回归测试补上，可以吗？",
	"はい、金曜日までに文言と回帰テストを追加します。",
	"Next, the release date. Cy, are we still on track for the 20th?",
	"20 日は厳しいかもしれません。キャッシュのバグがまだ再現できていません。",
	"缓存的问题只有在弱网的时候才出现，我这边昨天复现了一次。",
	"Can you share the steps? If we can reproduce it by Wednesday, we keep the 20th.",
	"好的，我今天下班前把复现步骤发到群里。",
	"水曜日までに再現できなければ、リリースを一週間延ばしましょう。",
	"那就这么定：周三之前复现不了，就推迟一周。",
	"One open question: should the cap be per entry or per recording?",
	"それはまだ決まっていません。来週のミーティングで決めましょう。",
	"OK, that's all for today. Thanks everyone.",
}

// liveNames are the people of liveMeeting, left out of the counts: a name stays as it was said.
var liveNames = regexp.MustCompile(`\b(Ada|Bo|Cy)\b`)

// scripts is the share of Han, Kana and Latin letters among the letters of text, names left out.
type scripts struct{ han, kana, latin, letters int }

func countScripts(text string) scripts {
	var s scripts
	for _, r := range liveNames.ReplaceAllString(text, "") {
		switch {
		case unicode.Is(unicode.Han, r):
			s.han++
		case unicode.In(r, unicode.Hiragana, unicode.Katakana) || r == 'ー':
			s.kana++
		case unicode.Is(unicode.Latin, r):
			s.latin++
		default:
			continue
		}
		s.letters++
	}
	return s
}

func (s scripts) share(n int) float64 {
	if s.letters == 0 {
		return 0
	}
	return float64(n) / float64(s.letters)
}

func (s scripts) String() string {
	return fmt.Sprintf("Han %3.0f%%  Kana %3.0f%%  Latin %3.0f%%", 100*s.share(s.han), 100*s.share(s.kana), 100*s.share(s.latin))
}

// wrongLanguage says why text counted as s is not in lang ("" when it is).
func (s scripts) wrongLanguage(lang string) string {
	switch {
	case s.letters == 0:
		return "nothing written"
	case lang == "en" && s.share(s.han+s.kana) > 0.05:
		return "English with more than 5% Han and Kana"
	case lang == "zh-Hans" && s.share(s.han) < 0.5:
		return "Chinese with less than 50% Han"
	case lang == "zh-Hans" && s.share(s.kana) > 0.05:
		return "Chinese with more than 5% Kana"
	case lang == "ja" && s.share(s.kana) < 0.05:
		return "Japanese with less than 5% Kana"
	}
	return ""
}

// minutesText is the written text of m: its title and every item (names apart).
func minutesText(m *Minutes) (string, string) {
	parts := []string{m.Title}
	for _, p := range m.Points {
		parts = append(parts, p.Text)
	}
	for _, d := range m.Decisions {
		parts = append(parts, d.Text)
	}
	for _, a := range m.Actions {
		parts = append(parts, a.What, a.By)
	}
	for _, o := range m.Open {
		parts = append(parts, o.Text)
	}
	counts := fmt.Sprintf("%d/%d/%d/%d", len(m.Points), len(m.Decisions), len(m.Actions), len(m.Open))
	return strings.Join(parts, "\n"), counts
}

// recapText is the written text of rc: its summary, the entries' titles and points, and the actions.
func recapText(rc *DayRecap) (string, string) {
	parts := []string{rc.Summary}
	points := 0
	for _, en := range rc.Timeline {
		parts = append(parts, en.Title)
		for _, p := range en.Points {
			parts = append(parts, p.Text)
			points++
		}
	}
	for _, a := range rc.Actions {
		parts = append(parts, a.What, a.By)
	}
	return strings.Join(parts, "\n"), fmt.Sprintf("%d entries/%d points/%d actions", len(rc.Timeline), points, len(rc.Actions))
}

// TestMinutesLanguageLive asks a real model (PERSONA_LIVE_AI) for the minutes, summary and day recap of liveMeeting
// recorded in each language, prints a row each, and fails when one is not written in the recording's language or its
// Markdown's headings are not.
func TestMinutesLanguageLive(t *testing.T) {
	raw := os.Getenv("PERSONA_LIVE_AI")
	if raw == "" {
		t.Skip("PERSONA_LIVE_AI not set")
	}
	var cfg ai.Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	model := ai.New(cfg)
	ctx := context.Background()
	var rows []string
	row := func(tag, kind, title, counts string, s scripts, miss string) {
		mark := "ok  "
		if miss != "" {
			mark = "MISS"
			t.Errorf("%s %s: %s (%s)", tag, kind, miss, s)
		}
		rows = append(rows, fmt.Sprintf("%s %-6s %-8s %-30.30q %-28s %s", mark, tag, kind, title, counts, s))
	}
	for _, tc := range []struct{ tag, lang, heading, recapHeading string }{
		{"zh-CN", "zh-Hans", "## 讨论要点", "全天回顾"},
		{"en-US", "en", "## Key points", "# Day recap · "},
		{"ja-JP", "ja", "## 要点", "の1日のまとめ"},
	} {
		e := goldenEngine(t, model)
		e.Logf = t.Logf
		start := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
		u := meetingUpload(1, start, liveMeeting...)
		u.Language = tc.tag
		putRecording(t, e, recID, u, start.Add(time.Hour))

		// Minutes.
		if n := e.WriteMinutes(ctx, 1); n != 1 {
			t.Fatalf("%s: wrote %d minutes", tc.tag, n)
		}
		m, err := e.Store.ReadMinutes(recID)
		if err != nil {
			t.Fatalf("%s: %v", tc.tag, err)
		}
		text, counts := minutesText(m)
		s := countScripts(text)
		miss := s.wrongLanguage(tc.lang)
		if m.Language != tc.lang || !strings.Contains(m.Markdown, tc.heading) {
			miss = strings.TrimSpace(miss + fmt.Sprintf(" language %q, headings %q", m.Language, m.Markdown[:min(len(m.Markdown), 200)]))
		}
		row(tc.tag, "minutes", m.Title, counts, s, miss)

		// The recording's summary (a meeting gets one besides its minutes).
		if n := e.SummarizeRecordings(ctx, 1); n != 1 {
			t.Fatalf("%s: summarized %d", tc.tag, n)
		}
		r := recordingNow(t, e, recID)
		sum, _ := os.ReadFile(e.Store.Path(KindRecording, recID, "summary.md"))
		// The section headings count too: they are asked for in the recording's language.
		s = countScripts(strings.Join([]string{r.AITitle, r.OneLine, string(sum)}, "\n"))
		row(tc.tag, "summary", r.AITitle, fmt.Sprintf("%d runes", len([]rune(string(sum)))), s, s.wrongLanguage(tc.lang))

		// A day recorded with the same words, a minute a line: an hour's notes, then the recap.
		now := dayClock.Add(2 * time.Hour)
		e.clock = func() time.Time { return now }
		du := dayUpload(1, liveMeeting...)
		du.Language = tc.tag
		du.Coverage.To = 7200
		mustPut(t, e, dayID, du, now)
		if err := e.AskMinutes(dayID); err != nil {
			t.Fatal(err)
		}
		e.WriteDayRecaps(ctx, 1)
		rc, err := e.ReadDayRecap(dayID)
		if err != nil {
			t.Fatalf("%s: recap: %v", tc.tag, err)
		}
		text, counts = recapText(rc)
		s = countScripts(text)
		miss = s.wrongLanguage(tc.lang)
		if rc.Language != tc.lang || !strings.Contains(rc.Markdown, tc.recapHeading) {
			miss = strings.TrimSpace(miss + fmt.Sprintf(" language %q, heading %q", rc.Language, strings.SplitN(rc.Markdown, "\n", 2)[0]))
		}
		title := ""
		if len(rc.Timeline) > 0 {
			title = rc.Timeline[0].Title
		}
		row(tc.tag, "recap", title, counts, s, miss)
	}
	t.Logf("model %s\n%s", cfg.Model, strings.Join(rows, "\n"))
}
