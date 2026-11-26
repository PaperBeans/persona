package node

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestRuntimeUserInputsDrainsPagesAndDeduplicates(t *testing.T) {
	n := &Node{}
	calls := []int{}
	index, err := n.runtimeUserInputs(context.Background(), "synthetic", 0, false, func(params any) (json.RawMessage, error) {
		p := params.(map[string]any)
		before, _ := p["before_seq"].(int)
		calls = append(calls, before)
		if p["lazy_images"] != true || p["limit"] != 500 {
			t.Fatalf("params %v", p)
		}
		items := []proto.Item{
			{ID: "reply-a2#0", Seq: 18, Role: "assistant", Kind: proto.KindText, Text: "- Next step is clear."},
			{ID: "input-b#0", Seq: 20, Role: "user", Kind: proto.KindImage},
			{ID: "input-b#1", Seq: 20, Role: "user", Kind: proto.KindImage},
			{ID: "reply-b#0", Seq: 21, Role: "assistant", Kind: proto.KindText, Text: "The images arrived."},
		}
		more := true
		if before == 18 {
			items = []proto.Item{
				{ID: "input-a#0", Seq: 5, Role: "user", Kind: proto.KindText, Text: "<USER_REQUEST>\n> Skip this\nCheck the run\n</USER_REQUEST>", Queued: true},
				{ID: "reply-a1#0", Seq: 10, Role: "assistant", Kind: proto.KindText, Text: "**Run passed.**"},
				{ID: "result#0", Seq: 12, Role: "user", Kind: proto.KindToolResult, ToolUseID: "check", Text: "Raw tool output"},
				{ID: "reply-a2#0", Seq: 18, Role: "assistant", Kind: proto.KindText, Text: "- Next step is clear."},
			}
			more = false
		}
		out, e := json.Marshal(map[string]any{"items": items, "has_more": more, "history": "fixture-history"})
		return out, e
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []proto.UserInputPosition{
		{ID: "input-a#0", Seq: 5, Preview: "Check the run", Answer: "Run passed. Next step is clear."},
		{ID: "input-b#0", Seq: 20, Attachments: 2, Answer: "The images arrived."},
	}
	if index.Total != len(want) || !reflect.DeepEqual(index.Inputs, want) || !reflect.DeepEqual(calls, []int{0, 18}) {
		t.Fatalf("index %+v calls %v", index, calls)
	}
}

func TestRuntimeUserInputsBoundsFrozenHistory(t *testing.T) {
	n := &Node{}
	index, err := n.runtimeUserInputs(t.Context(), "synthetic", 9, false, func(params any) (json.RawMessage, error) {
		if p := params.(map[string]any); p["before_seq"] != 9 {
			t.Fatalf("unbounded source request: %v", p)
		}
		items := []proto.Item{
			{ID: "input#0", Seq: 7, Role: "user", Kind: proto.KindText, Text: "A question"},
			{ID: "reply#0", Seq: 8, Role: "assistant", Kind: proto.KindText, Text: "Visible reply."},
			{ID: "late#0", Seq: 9, Role: "assistant", Kind: proto.KindText, Text: "Private retired reply."},
		}
		return json.Marshal(map[string]any{"items": items, "has_more": false, "history": "fixture"})
	})
	want := []proto.UserInputPosition{{ID: "input#0", Seq: 7, Preview: "A question", Answer: "Visible reply."}}
	if err != nil || index.Total != len(want) || !reflect.DeepEqual(index.Inputs, want) {
		t.Fatalf("frozen index = %+v, %v", index, err)
	}
}

func TestRuntimeUserInputsRefusesPartialOrRewrittenTotals(t *testing.T) {
	for _, scenario := range []string{"empty", "stuck", "rewrite"} {
		t.Run(scenario, func(t *testing.T) {
			n := &Node{}
			calls := 0
			_, err := n.runtimeUserInputs(context.Background(), "synthetic", 0, false, func(any) (json.RawMessage, error) {
				calls++
				history := "first"
				if scenario == "rewrite" && calls > 1 {
					history = "second"
				}
				items := []proto.Item{{ID: "input#0", Seq: 20, Role: "user", Kind: proto.KindText, Text: "Synthetic"}}
				if scenario == "empty" {
					items = nil
				}
				return json.Marshal(map[string]any{"items": items, "has_more": true, "history": history})
			})
			if err == nil {
				t.Fatal("incomplete index reported a total")
			}
		})
	}
}
