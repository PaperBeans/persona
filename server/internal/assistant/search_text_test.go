package assistant

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// The index keeps what spec/fixtures/chat-search says of each item (its "index"), the rule the web's search in chat
// reads too (#6281, #6282).
func TestSearchIndexTextFollowsTheSharedRule(t *testing.T) {
	raw, err := os.ReadFile("../../../spec/fixtures/chat-search/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name  string     `json:"name"`
			Item  proto.Item `json:"item"`
			Index *string    `json:"index"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) < 10 {
		t.Fatalf("only %d cases read", len(fixture.Cases))
	}
	for _, c := range fixture.Cases {
		got, ok := searchIndexText(c.Item)
		switch {
		case c.Index == nil && ok:
			t.Errorf("%s: indexed %q, want nothing", c.Name, got)
		case c.Index != nil && (!ok || got != *c.Index):
			t.Errorf("%s: indexed %q (%v), want %q", c.Name, got, ok, *c.Index)
		}
	}
}

func TestSearchIndexTextLeavesOutWhatIsNotTheUsersWords(t *testing.T) {
	notice := "🔔 任务「README 去掉扩展列表」做完了（公司电脑）\n（这是 Persona 的任务通知，不是用户发的消息。）" // i18n-ignore: an injected template's shape
	for name, it := range map[string]proto.Item{
		"another session's message": {Role: "user", Kind: proto.KindText, Text: "from the other one", From: "worker-1"},
		"only view errors":          {Role: "user", Kind: proto.KindText, Text: "<persona-view-errors>\n- bad\n</persona-view-errors>"},
		"a thought":                 {Role: "assistant", Kind: proto.KindThinking, Text: "thinking it over"},
		"a tool's output":           {Role: "user", Kind: proto.KindToolResult, Text: "ok"},
	} {
		if got, ok := searchIndexText(it); ok {
			t.Errorf("%s: indexed %q", name, got)
		}
	}
	// read off the text when the hub has not marked it, as the hub's withInjected does; a message the app sent is the user's
	if prompt := (proto.Item{Role: "user", Kind: proto.KindText, Text: notice}); searchInjected(prompt) {
		if _, ok := searchIndexText(prompt); ok {
			t.Error("an unmarked injected notice was indexed")
		}
	}
	if _, ok := searchIndexText(proto.Item{Role: "user", Kind: proto.KindText, Text: notice, ClientMsgID: "c1"}); !ok {
		t.Error("the user's own message was left out because it reads like a notice")
	}
}

func TestSearchWordsFoldCaseAndWidth(t *testing.T) {
	if got := searchWords("  ＷＥＥＫＬＹ Report  weekly 日历 "); !reflect.DeepEqual(got, []string{"weekly", "report", "日历"}) {
		t.Fatalf("words %q", got)
	}
	if got := searchWords("   "); got != nil {
		t.Fatalf("blank query gave %q", got)
	}
	if got := searchWords("a b c d e f g h i j"); len(got) != searchMaxWords {
		t.Fatalf("%d words kept, want %d", len(got), searchMaxWords)
	}
	if !searchHolds(searchFold("周末去青石巷 BlueLane 看看"), searchWords("bluelane 青石")) || searchHolds("abc", nil) {
		t.Fatal("searchHolds")
	}
}

func TestSearchSnippetMarksMatchesInUTF16(t *testing.T) {
	snippet, ranges := searchSnippet("我想去青石巷 BlueLane 看看", searchWords("bluelane"))
	if snippet != "我想去青石巷 BlueLane 看看" || !reflect.DeepEqual(ranges, [][2]int{{7, 15}}) {
		t.Fatalf("snippet %q ranges %v", snippet, ranges)
	}
	// a character outside the BMP counts two before the match, as the apps' strings do
	if _, r := searchSnippet("😀 Ｋｅｙ here", searchWords("key")); !reflect.DeepEqual(r, [][2]int{{3, 6}}) {
		t.Fatalf("ranges %v", r)
	}
	// cut around the first match, on one line, with ellipses; every match marked
	long := ""
	for range 30 {
		long += "前文"
	}
	snippet, ranges = searchSnippet(long+"\n\n铜钥匙 挂在门后。\n"+long+long, searchWords("铜钥匙"))
	if []rune(snippet)[0] != '…' || []rune(snippet)[len([]rune(snippet))-1] != '…' {
		t.Fatalf("not cut: %q", snippet)
	}
	// "…", then the 40 characters before the match: 38 of the first line and its two line breaks as one space
	if !reflect.DeepEqual(ranges, [][2]int{{40, 43}}) {
		t.Fatalf("ranges %v in %q", ranges, snippet)
	}
}
