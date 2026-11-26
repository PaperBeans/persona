package lark

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/botcard"
	"github.com/nwverse/persona/server/internal/i18n"
)

// The progress card's lines: the newest call still running and its age, the last steps, the subagents; counters in a
// note. The core hands the run's notes over as a view (assistantwire.RunProgress); the core's own test checks that the
// view is made from the items as they come (TestRunProgressViewsTheLiveNotes).
func TestProgressCardLines(t *testing.T) {
	t0 := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name  string
		v     assistantwire.RunProgress
		steps []string
		want  []string // each must be in the card
		not   []string
	}{
		{"open call and its age", assistantwire.RunProgress{Calls: 1, Open: []assistantwire.RunOpenCall{{Step: "运行 `go test ./...`", Since: t0.Add(-40 * time.Second)}}},
			[]string{"运行 `go test ./...`"}, []string{"正在：运行 `go test ./...`（已 40 秒）", "调用了 1 次工具"}, []string{"等 ", "读了", "子任务"}},
		{"just seen: no time yet", assistantwire.RunProgress{Calls: 1, Open: []assistantwire.RunOpenCall{{Step: "运行 `ls`", Since: t0}}},
			nil, []string{"正在：运行 `ls`（已 0 秒）"}, nil},
		{"several open: the newest", assistantwire.RunProgress{Calls: 3, Open: []assistantwire.RunOpenCall{
			{Step: "运行 `a`", Since: t0.Add(-time.Minute)}, {Step: "读 `c.go`", Since: t0.Add(-5 * time.Second)}}},
			nil, []string{"正在：读 `c.go`（已 5 秒） 等 1 个"}, []string{"`a`（"}},
		{"none open: no now line", assistantwire.RunProgress{Calls: 1},
			[]string{"运行 `a`"}, []string{"- 运行 `a`"}, []string{"正在："}},
		{"counters: distinct files", assistantwire.RunProgress{Calls: 5, Read: 2, Edited: 2},
			nil, []string{"调用了 5 次工具 · 读了 2 个文件 · 改了 2 个文件"}, nil},
		{"subtasks running and done", assistantwire.RunProgress{Calls: 2, Subs: []assistantwire.RunSubtask{{Desc: "查 *日志*", Kind: "Explore", Done: true}, {Desc: "写测试"}}},
			nil, []string{"子任务", "- 查 &#42;日志&#42; `Explore` · 完成", "- 写测试 · 进行中"}, nil},
		{"no steps at all", assistantwire.RunProgress{},
			nil, []string{"工作中 · 已用 3 分 12 秒"}, []string{"正在：", "调用了", "子任务", "前面还有"}},
	} {
		c.v.Now = t0
		b, _ := json.Marshal(progressCard("", "Persona", 192*time.Second, c.steps, "", c.v))
		var s string
		for _, md := range progressLines("", c.steps, c.v) {
			s += md + "\n"
		}
		all := string(b) + "\n" + s
		for _, w := range c.want {
			if !strings.Contains(all, w) {
				t.Errorf("%s: no %q in\n%s\n%s", c.name, w, s, b)
			}
		}
		for _, w := range c.not {
			if strings.Contains(all, w) {
				t.Errorf("%s: %q should not be in\n%s", c.name, w, all)
			}
		}
	}
}

// Many subtasks: the last six, then "and N more"; the "N earlier steps" of more than five steps stays.
func TestProgressCardManySubtasks(t *testing.T) {
	var subs []assistantwire.RunSubtask
	for i := 10; i < 60; i++ { // the core keeps the newest 50 of 60 and says 10 were dropped
		subs = append(subs, assistantwire.RunSubtask{Desc: fmt.Sprintf("t%d", i)})
	}
	steps := []string{"1", "2", "3", "4", "5", "6", "7"}
	s := strings.Join(progressLines("", steps, assistantwire.RunProgress{Subs: subs, SubsMore: 10}), "\n")
	if !strings.Contains(s, "- t59 · 进行中") || strings.Contains(s, "- t53 ") || !strings.Contains(s, "等 54 个") ||
		!strings.Contains(s, "前面还有 2 步") || strings.Contains(s, "- 2\n") {
		t.Fatalf("%s", s)
	}
}

// A file counter at its cap says so: "1000+".
func TestProgressCardFileCountAtItsCap(t *testing.T) {
	b, _ := json.Marshal(progressCard("", "Persona", time.Second, nil, "", assistantwire.RunProgress{Calls: 1, Read: assistantwire.RunFilesMost, Edited: 3}))
	if !strings.Contains(string(b), "读了 1000+ 个文件 · 改了 3 个文件") {
		t.Fatalf("%s", b)
	}
	b, _ = json.Marshal(progressCard(i18n.En, "Persona", time.Second, nil, "", assistantwire.RunProgress{Calls: 1, Edited: 1}))
	if !strings.Contains(string(b), "1 tool call · changed 1 file") {
		t.Fatalf("%s", b)
	}
}

// The progress card says its state with a coloured tag, not an hourglass.
func TestProgressCardLooks(t *testing.T) {
	c := progressCard("zh-Hans", "Persona", 12*time.Second, []string{"运行 `ls`"}, "", assistantwire.RunProgress{Target: assistantwire.ChatTarget{Kind: assistantwire.ChatPersona}})
	js := c.JSON()
	if strings.Contains(js, "⏳") || !strings.Contains(js, "<text_tag color='blue'>") {
		t.Fatalf("progress card %s", js)
	}
	q := progressCard("zh-Hans", "Persona", time.Second, nil, "", assistantwire.RunProgress{Waiting: "queued"}).JSON()
	if !strings.Contains(q, "<text_tag color='neutral'>") {
		t.Fatalf("queued card %s", q)
	}
}

// A run's progress card carries the Stop button with the run's own conversation, in the card's language; a card with
// no live run carries no button (the answer and idle cards never do).
func TestProgressCardStopButton(t *testing.T) {
	target := assistantwire.ChatTarget{Kind: assistantwire.ChatPersona, MachineID: "server", SessionID: "conv0001"}
	b, _ := json.Marshal(progressCard("", "Persona", time.Second, nil, "", assistantwire.RunProgress{Target: target}))
	card := string(b)
	for _, want := range []string{`"a":"stop"`, `"k":"persona"`, `"m":"server"`, `"s":"conv0001"`, `"type":"danger"`, "停止"} {
		if !strings.Contains(card, want) {
			t.Fatalf("running card missing %q: %s", want, card)
		}
	}
	b, _ = json.Marshal(progressCard(i18n.En, "Persona", time.Second, nil, "", assistantwire.RunProgress{Target: target}))
	if en := string(b); !strings.Contains(en, "Stop") || strings.Contains(en, "停止") {
		t.Fatalf("English card button not translated: %s", en)
	}
	b, _ = json.Marshal(progressCard("", "Persona", time.Second, nil, "", assistantwire.RunProgress{}))
	if strings.Contains(string(b), `"a":"stop"`) {
		t.Fatalf("a card with no live run carries a stop button: %s", b)
	}
}

func TestClipPanelLine(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"ascii", strings.Repeat("a", 300), strings.Repeat("a", 120) + "…"},
		{"runes", strings.Repeat("字", 200), strings.Repeat("字", 120) + "…"},
		{"entity whole", strings.Repeat("a", 114) + "&#124;bbb", strings.Repeat("a", 114) + "&#124;…"},
		{"entity split", strings.Repeat("a", 115) + "&#124;bbb", strings.Repeat("a", 115) + "…"},
		{"literal ampersand", strings.Repeat("a", 116) + "Q&A" + strings.Repeat("b", 100), strings.Repeat("a", 116) + "Q&Ab…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clipPanelLine(tc.in, 120); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// pathologicalSteps builds n botStep-like lines made of card-escape entities (pathological raw parameters).
func pathologicalSteps(n int) []string {
	steps := make([]string, n)
	for i := range steps {
		steps[i] = fmt.Sprintf("运行%02d %s", i, strings.Repeat("&#124;", 49))
	}
	return steps
}

// Ordinary short steps render verbatim (41 steps: the newest 40 show, one hidden).
func TestProcessPanelShortUnchanged(t *testing.T) {
	var steps []string
	for i := 0; i < 41; i++ {
		steps = append(steps, fmt.Sprintf("运行 `cmd%02d`", i))
	}
	c := botcard.NewCard("", "", "")
	processPanel(i18n.ZhHans, c, steps)
	js := c.JSON()
	for _, want := range []string{"已调用 41 次工具", "…前面还有 1 步", "- 运行 `cmd40`"} {
		if !strings.Contains(js, want) {
			t.Fatalf("missing %q in %s", want, js)
		}
	}
	if strings.Contains(js, "cmd00") || strings.Count(js, "collapsible_panel") != 1 {
		t.Fatalf("oldest step or panel count wrong: %s", js)
	}
}

// A pathological panel bounds itself: the oldest calls hide first, the newest stay.
func TestProcessPanelPathologicalBounded(t *testing.T) {
	c := botcard.NewCard("", "", "")
	processPanel(i18n.ZhHans, c, pathologicalSteps(40))
	js := c.JSON()
	if got := len(js) - len(botcard.NewCard("", "", "").JSON()); got > processPanelMaxBytes {
		t.Fatalf("panel is %d JSON bytes, want <= %d", got, processPanelMaxBytes)
	}
	for _, want := range []string{"已调用 40 次工具", "…前面还有 20 步", "运行20", "运行39"} {
		if !strings.Contains(js, want) {
			t.Fatalf("missing %q in %s", want, js)
		}
	}
	if strings.Contains(js, "运行19") {
		t.Fatalf("an oldest step was kept: %s", js)
	}
}

func TestMissingPiecesNote(t *testing.T) {
	if got := missingPiecesNote("en", []int{2, 4}); !strings.Contains(got, "2 parts") || !strings.Contains(got, "2, 4") {
		t.Fatalf("en %q", got)
	}
	if got := missingPiecesNote("zh-Hans", []int{3}); !strings.Contains(got, "1 段") || !strings.Contains(got, "第 3 段") {
		t.Fatalf("zh %q", got)
	}
	if got := missingPiecesNote("zh-Hans", []int{2, 4}); !strings.Contains(got, "第 2、4 段") {
		t.Fatalf("zh list %q", got)
	}
	if got := missingPiecesNote("ja", []int{2, 4}); !strings.Contains(got, "2、4 番目") || strings.Contains(got, "件") {
		t.Fatalf("ja %q", got)
	}
}
