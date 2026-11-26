package transcript

import (
	"encoding/base64"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestClaudePublicNarration(t *testing.T) {
	const narration = "Eg0KC0IJbmFycmF0aW9u"
	encode := base64.StdEncoding.EncodeToString
	for _, tc := range []struct {
		sig    string
		public bool
	}{
		{narration, true}, {"", false}, {"invalid!", false}, {encode([]byte("narration")), false},
		{encode([]byte("\\x12\\x0d\\x0a\\x0b\\x4a\\x09narration")), false},      // same label, different field
		{encode([]byte("\\x12\\x0d\\x0a\\x0b\\x42\\x09narration\\x80")), false}, // invalid outer tail
	} {
		raw := fmt.Sprintf(`{"type":"assistant","message":{"role":"assistant","model":"claude","content":[{"type":"thinking","thinking":"Example","signature":%q}]}}`, tc.sig)
		got := ParseLine(1, []byte(raw), &Meta{})
		if len(got) != 1 || got[0].Narration != tc.public || (got[0].Kind == proto.KindText) != tc.public {
			t.Fatalf("wrong public classification: %+v", got)
		}
	}
	for _, reason := range []string{"tool_use", "end_turn", ""} {
		raw := fmt.Sprintf(`{"type":"assistant","message":{"role":"assistant","model":"claude","stop_reason":%q,"content":[{"type":"text","text":"Words"}]}}`, reason)
		got := ParseLine(1, []byte(raw), &Meta{})
		if len(got) != 1 || got[0].Narration != (reason == "tool_use") {
			t.Fatal(got)
		}
	}
}

func TestClaudePlanAndPublicParagraphsOnFoldedPages(t *testing.T) {
	var m Meta
	var p planLines
	now := time.Unix(10, 0)
	call := p.call("plan", claudeDesktopPlan, `{"scope":"continuation","items":[{"content":"Inspect","status":"in_progress"},{"content":"Report","status":"pending"}]}`, now)
	plan := ParseLine(1, []byte(call), &m)
	if m.Plan != nil {
		t.Fatal("pending plan became visible")
	}
	items := append(plan, ParseLine(2, []byte(p.result("plan", "Plan shown (2 steps).", false, now)), &m)...)
	if m.Plan == nil || m.Plan.Total != 2 || m.Plan.Current != "Inspect" {
		t.Fatal(m.Plan)
	}
	items = append(items, proto.Item{ID: "tool-1", Kind: proto.KindThinking, Text: "thought"},
		proto.Item{ID: "slow", Kind: proto.KindToolUse, ToolName: "Bash", ToolUseID: "slow"},
		proto.Item{ID: "progress", Role: "assistant", Kind: proto.KindText, Text: "In progress", Narration: true},
		proto.Item{ID: "receipt", Kind: proto.KindToolResult, ToolUseID: "slow"},
		proto.Item{ID: "answer", Role: "assistant", Kind: proto.KindText, Text: "Done"})
	for _, latest := range []bool{true, false} {
		page := new(File).FoldPage(items, latest)
		if len(page) != 5 || page[0].Fold == nil || len(page[0].Fold.Tasks) != 1 || len(page[0].Fold.Tasks[0].Entries) != 2 ||
			page[1].ID != "slow" || !page[2].Narration || page[3].ID != "receipt" || page[4].ID != "answer" {
			t.Fatalf("incomplete page: %+v", page)
		}
	}
}

func TestDesktopPlanAdvancesAndRetainsNextStepOnFoldedPage(t *testing.T) {
	var m Meta
	var p planLines
	now := time.Unix(100, 0)
	var all []proto.Item
	pair := func(name, input, reply string, failed bool) {
		id := fmt.Sprint(p.n + 1)
		all = append(all, ParseLine(p.n+1, []byte(p.call(id, name, input, now)), &m)...)
		now = now.Add(time.Second)
		all = append(all, ParseLine(p.n+1, []byte(p.result(id, reply, failed, now)), &m)...)
	}
	pair(claudeDesktopPlan, `{"items":[{"content":"A","status":"in_progress"},{"content":"B","status":"pending"},{"content":"C","status":"pending"},{"content":"D","status":"pending"},{"content":"E","status":"pending"},{"content":"F","status":"pending"}]}`, "Plan shown (6 steps).", false)
	for n := 1; n <= 3; n++ {
		pair(claudeDesktopPlan, `{"advance":true}`, "Plan updated.", false)
		if m.Plan.Done != n {
			t.Fatal(m.Plan)
		}
	}
	pair(claudeDesktopPlan, `{"done":[4,5],"start":6}`, "Plan updated: step 6 of 6 is in progress.", false)
	pair(claudeDesktopPlan, `{"advance":true}`, "denied", true)
	pair(claudeDesktopPlan, `{"done":[6,18]}`, "Nothing changed", false)
	pair(claudeDesktopWrapUp, `{"ask":"Continue?","recap":"Tests passed","actions":[{"label":"Yes"}]}`, "Shown in the next-step box.", false)
	if m.Plan.Total != 6 || m.Plan.Done != 5 || m.Plan.Current != "F" {
		t.Fatal(m.Plan)
	}
	for _, latest := range []bool{false, true} {
		page := new(File).FoldPage(all, latest)
		for _, it := range all[2:] {
			if !slices.ContainsFunc(page, func(x proto.Item) bool { return x.ID == it.ID }) {
				t.Fatalf("missing incremental or wrap-up item %s on folded page", it.ID)
			}
		}
	}
}
