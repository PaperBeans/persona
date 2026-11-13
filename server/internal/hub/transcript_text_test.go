package hub

import (
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

// userLine is a transcript line with a user message saying s, as Claude Code writes it.
func userLine(t *testing.T, s string) []proto.Item {
	t.Helper()
	content, _ := json.Marshal(s)
	var meta transcript.Meta
	items := transcript.ParseLine(1, []byte(`{"type":"user","uuid":"u1","message":{"role":"user","content":`+string(content)+`}}`), &meta)
	if len(items) != 1 {
		t.Fatalf("%q: %d items", s, len(items))
	}
	return items
}

// The node's lines, parsed by the node's own code, read in the app's language; zh-Hans gets them as they were.
func TestLocalizedTranscriptItems(t *testing.T) {
	note := func(xml string) string { return "<task-notification>" + xml + "</task-notification>" }
	cases := []struct {
		in     string
		en, ja string
	}{
		{note(`<task-id>a</task-id><status>failed</status><summary>Background command "npm test" failed with exit code 1</summary>`),
			"Background command failed (exit code 1) · npm test", "バックグラウンドコマンド失敗（終了コード1） · npm test"},
		{note(`<task-id>a</task-id><status>killed</status><summary>Background command "tail -f x" was stopped</summary>`),
			"Background command stopped · tail -f x", "バックグラウンドコマンド停止 · tail -f x"},
		{note(`<task-id>a</task-id><status>completed</status><summary>Agent "Review the diff" finished</summary><result>ok</result>`),
			"Subagent finished · Review the diff", "サブエージェント完了 · Review the diff"},
		{note(`<task-id>m</task-id><summary>Monitor event: "Watch CI"</summary><event>build 42 passed</event>`),
			"Monitor event · build 42 passed", "Monitorイベント · build 42 passed"},
		// A background workflow's end (#5511): the node writes it in Chinese since #3731.
		{note("\n<task-id>w</task-id>\n<tool-use-id>toolu_w</tool-use-id>\n<status>completed</status>\n" +
			`<summary>Dynamic workflow "parity-audit" completed</summary>` + "\n<result>ok</result>\n"),
			"Background workflow finished · parity-audit", "バックグラウンド workflow 完了 · parity-audit"},
		{note(`<task-id>w</task-id><status>failed</status><summary>Dynamic workflow "parity-audit" failed</summary>`),
			"Background workflow failed · parity-audit", "バックグラウンド workflow 失敗 · parity-audit"},
		{note(`<task-id>w</task-id><status>killed</status><summary>Dynamic workflow "parity-audit" was stopped</summary>`),
			"Background workflow stopped · parity-audit", "バックグラウンド workflow 停止 · parity-audit"},
		{note(`<task-id>z</task-id><summary>Something new happened</summary>`), "Something new happened", "Something new happened"},
		{"[Request interrupted by user]", "Interrupted", "中断しました"},
	}
	for _, c := range cases {
		items := userLine(t, c.in)
		zh := items[0].Text
		if got := localizedItems(i18n.ZhHans, items); got[0].Text != zh {
			t.Fatalf("zh-Hans changed %q into %q", zh, got[0].Text)
		}
		for l, want := range map[i18n.Lang]string{i18n.En: c.en, i18n.Ja: c.ja} {
			got := localizedItems(l, items)
			if got[0].Text != want {
				t.Errorf("%s: %q → %q, want %q", l, zh, got[0].Text, want)
			}
		}
		if items[0].Text != zh {
			t.Fatal("the node's items were written to")
		}
	}
}

// Every heading the node writes for a task note (transcript/tasknote.go noteText) is one the hub knows: its zh-Hans
// text is the node's, word for word, so en and ja get theirs instead of the node's Chinese.
func TestTaskNoteHeadsMatchTheNode(t *testing.T) {
	for _, kind := range []string{"Background command", "Monitor", "Agent", "Dynamic workflow"} {
		for _, c := range []struct{ status, outcome string }{
			{"completed", "completed"}, {"failed", "failed"}, {"killed", "was stopped"}, {"stopped", "was stopped"},
			{"", "timed out"}, {"", "stream ended"}, {"", "is waiting on its own background work"},
		} {
			xml := "<task-notification><task-id>t</task-id>"
			if c.status != "" {
				xml += "<status>" + c.status + "</status>"
			}
			xml += "<summary>" + kind + ` "the title" ` + c.outcome + "</summary></task-notification>"
			items := userLine(t, xml)
			if items[0].Task == nil || items[0].Task.Kind == "task" {
				t.Fatalf("%s: not a task note: %+v", xml, items[0])
			}
			key, zh := mustHead(t, items[0].Task), items[0].Text
			if want := tt(i18n.ZhHans, key) + " · the title"; zh != want {
				t.Errorf("%s/%s: the node writes %q, the hub's zh-Hans %q is %q", items[0].Task.Kind, items[0].Task.Status, zh, key, want)
			}
			for _, l := range []i18n.Lang{i18n.En, i18n.Ja} {
				if got, want := localizedItems(l, items)[0].Text, tt(l, key)+" · the title"; got != want {
					t.Errorf("%s %s/%s: %q → %q, want %q", l, items[0].Task.Kind, items[0].Task.Status, zh, got, want)
				}
			}
		}
	}
}

func mustHead(t *testing.T, n *proto.TaskNote) string {
	t.Helper()
	key, args := taskNoteHead(n)
	if key == "" || args != nil {
		t.Fatalf("no plain heading for %+v", n)
	}
	return key
}

func TestLocalizedNotices(t *testing.T) {
	for _, c := range []struct{ zh, en string }{
		{"/loop 到点，开始新一轮", "/loop is due, starting a new round"},
		{"/loop 到点，开始新一轮：check the build", "/loop is due, starting a new round: check the build"},
		{"Codex 事件：mcp_progress（完整记录在原会话中）", "Codex event: mcp_progress (the full record is in the original session)"},
		{"Codex 扩展事件：x.y（完整记录在原会话中）", "Codex extension event: x.y (the full record is in the original session)"},
		{"上下文已压缩", "上下文已压缩"},                                   // the apps find the compaction divider by it
		{"API 错误：Overloaded（正在重试）", "API 错误：Overloaded（正在重试）"}, // the apps' API error card parses it
		{"something else", "something else"},
	} {
		it := proto.Item{Role: "system", Kind: proto.KindNotice, Text: c.zh}
		if got := localizedItems(i18n.En, []proto.Item{it})[0].Text; got != c.en {
			t.Errorf("%q → %q, want %q", c.zh, got, c.en)
		}
	}
}

func TestLocalizedTeammateAndLinks(t *testing.T) {
	items := []proto.Item{
		{Role: "user", Kind: proto.KindText, From: "队友", Text: "**出错停下**：tests failed"},
		{Role: "user", Kind: proto.KindText, From: "docs", Text: "**分配任务 #3**：Write the docs\n\nDetails here"},
		{Role: "user", Kind: proto.KindText, From: "docs", Text: "空闲，等待新的任务"},
		{Role: "user", Kind: proto.KindText, From: "另一个会话", Text: "**计划已批准**\n\nGo ahead"},
		{Role: "assistant", Kind: proto.KindText, Text: "Done: [打开新建的对话](persona://session/m1/abc)"},
		{Role: "user", Kind: proto.KindToolResult, Text: "已加载工具 Read\n已加载工具 Grep"},
	}
	got := localizedItems(i18n.En, items)
	want := []proto.Item{
		{Role: "user", Kind: proto.KindText, From: "Teammate", Text: "**Stopped on an error**: tests failed"},
		{Role: "user", Kind: proto.KindText, From: "docs", Text: "**Task #3 assigned**: Write the docs\n\nDetails here"},
		{Role: "user", Kind: proto.KindText, From: "docs", Text: "Idle, waiting for a new task"},
		{Role: "user", Kind: proto.KindText, From: "Another session", Text: "**Plan approved**\n\nGo ahead"},
		{Role: "assistant", Kind: proto.KindText, Text: "Done: [Open the new chat](persona://session/m1/abc)"},
		{Role: "user", Kind: proto.KindToolResult, Text: "Loaded tool Read\nLoaded tool Grep"},
	}
	for i := range want {
		if got[i].From != want[i].From || got[i].Text != want[i].Text {
			t.Errorf("%d: %q %q, want %q %q", i, got[i].From, got[i].Text, want[i].From, want[i].Text)
		}
	}
	if items[0].From != "队友" {
		t.Fatal("the node's items were written to")
	}
}

// The phone's Stop of a session outside Persona's terminals (transcript.StoppedFromPhone) reads in the app's language;
// the user's own words from Persona stay as they are.
func TestStoppedFromPhoneLocalized(t *testing.T) {
	stop := proto.Item{Role: "user", Kind: proto.KindText, From: transcript.PersonaPeer, Text: transcript.StoppedFromPhone}
	words := proto.Item{Role: "user", Kind: proto.KindText, From: transcript.PersonaPeer, Text: transcript.StoppedFromPhone + "?"}
	for l, want := range map[i18n.Lang]string{i18n.En: "Stopped from the phone", i18n.Ja: "スマホから停止しました", i18n.ZhHans: transcript.StoppedFromPhone} {
		got := localizedItems(l, []proto.Item{stop, words})
		if got[0].Text != want || got[0].From != transcript.PersonaPeer || got[1].Text != words.Text {
			t.Errorf("%s: %+v", l, got)
		}
	}
}
