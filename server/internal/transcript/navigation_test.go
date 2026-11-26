package transcript

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func BenchmarkUserInputIndexProjection(b *testing.B) {
	const turns = 1000
	answer := strings.Repeat("**The check passed.** Here is [the report](https://example.com/report).\n", 20)
	items := make([]proto.Item, 0, 2*turns)
	for i := range turns {
		items = append(items, proto.Item{ID: fmt.Sprintf("input-%d#0", i), Seq: 2*i + 1, Role: "user", Kind: proto.KindText, Text: "Please check the build."},
			proto.Item{ID: fmt.Sprintf("answer-%d#0", i), Seq: 2*i + 2, Role: "assistant", Kind: proto.KindText, Text: answer})
	}
	b.Run("legacy", func(b *testing.B) {
		var index proto.UserInputIndex
		b.ReportAllocs()
		for b.Loop() {
			index.Inputs = UserInputPositions(items)
			index.Total = len(index.Inputs)
		}
		raw, _ := json.Marshal(index)
		b.ReportMetric(float64(len(raw)), "wire-B")
	})
	b.Run("compact", func(b *testing.B) {
		var index proto.UserInputIndex
		b.ReportAllocs()
		for b.Loop() {
			var compact CompactInputIndex
			compact.Add(items)
			index = compact.Index("")
		}
		raw, _ := json.Marshal(index)
		b.ReportMetric(float64(len(raw)), "wire-B")
	})
}

func TestNavigationColdClaudeRewindAcrossWindows(t *testing.T) {
	old := windowBytes
	windowBytes = 512
	t.Cleanup(func() { windowBytes = old })
	path := filepath.Join(t.TempDir(), "rewound.jsonl")
	var rows strings.Builder
	fmt.Fprintln(&rows, `{"type":"user","uuid":"keep","message":{"role":"user","content":"Keep this question"}}`)
	fmt.Fprintln(&rows, `{"type":"assistant","uuid":"anchor","parentUuid":"keep","message":{"role":"assistant","content":[{"type":"text","text":"Keep this reply."}]}}`)
	fmt.Fprintln(&rows, `{"type":"user","uuid":"gone","parentUuid":"anchor","message":{"role":"user","content":"Rewound question"}}`)
	fmt.Fprintln(&rows, `{"type":"assistant","uuid":"hidden","parentUuid":"gone","message":{"role":"assistant","content":[{"type":"text","text":"Rewound reply."}]}}`)
	for i := 0; i < 80; i++ {
		fmt.Fprintln(&rows, `{"type":"unknown","text":"`+strings.Repeat("filler ", 30)+`"}`)
	}
	fmt.Fprintln(&rows, `{"type":"user","uuid":"replace","parentUuid":"anchor","message":{"role":"user","content":"Replacement question"}}`)
	if err := os.WriteFile(path, []byte(rows.String()), 0600); err != nil {
		t.Fatal(err)
	}
	makeFile := func() *File {
		f := NewFile(path)
		if _, err := f.Refresh(); err != nil || f.base <= 4 {
			t.Fatalf("cold fixture: base %d, %v", f.base, err)
		}
		return f
	}
	want, err := makeFile().UserInputs(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	f := makeFile()
	compact, err := f.CompactUserInputs(t.Context(), 0)
	if err != nil || compact.History != want.History || compact.Total != 2 ||
		compact.Inputs[0] != want.Inputs[0].WithoutExcerpts() || compact.Inputs[1] != want.Inputs[1].WithoutExcerpts() {
		t.Fatalf("cold branch index %+v, legacy %+v, %v", compact, want, err)
	}
	p, err := f.InputPreviews(t.Context(), proto.InputPreviewRequest{Positions: []proto.UserInputRef{
		{ID: "gone#0", Seq: 3}, {ID: "keep#0", Seq: 1},
	}})
	if err != nil || !slices.Equal(p.Inputs, want.Inputs[:1]) || p.History != want.History {
		t.Fatalf("cold branch previews %+v, %v", p, err)
	}
	win, err := f.ItemsWindow(t.Context(), proto.ItemsWindowRequest{FromSeq: 1, ThroughSeq: 4})
	if err != nil || len(win.Items) != 2 || win.Items[1].ID != "anchor#0" || win.ToSeq != 4 {
		t.Fatalf("cold branch window %+v, %v", win, err)
	}
	legacy, err := f.UserInputs(t.Context(), 0)
	if err != nil || legacy.History != want.History || !slices.Equal(legacy.Inputs, want.Inputs) {
		t.Fatalf("backfill after navigation changed legacy history: %+v, %v", legacy, err)
	}
}

func TestNavigationColdCodexCompactionAndDuplicateItems(t *testing.T) {
	old := windowBytes
	windowBytes = 800
	t.Cleanup(func() { windowBytes = old })
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	var rows strings.Builder
	rows.WriteString("{\"type\":\"session_meta\",\"payload\":{}}\n")
	for turn := 1; turn <= 24; turn++ {
		fmt.Fprintf(&rows, `{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-%d"}}`+"\n", turn)
		fmt.Fprintf(&rows, `{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u-%d","content":[{"type":"text","text":"Question %d"}]}}}`+"\n", turn, turn)
		fmt.Fprintf(&rows, `{"type":"history_mutation","payload":{"display_completions":[{"item":{"type":"UserMessage","id":"u-%d","content":[{"type":"text","text":"Duplicate question"}]}}]}}`+"\n", turn)
		rows.WriteString("{\"type\":\"event_msg\",\"payload\":{\"type\":\"context_compacted\"}}\n")
		fmt.Fprintf(&rows, `{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"AgentMessage","id":"a-%d","phase":"final_answer","content":[{"type":"text","text":"Answer %d."}]}}}`+"\n", turn, turn)
	}
	if err := os.WriteFile(path, []byte(rows.String()), 0600); err != nil {
		t.Fatal(err)
	}
	f := NewFile(path)
	if _, err := f.Refresh(); err != nil || f.base < 6 {
		t.Fatalf("cold Codex fixture: base %d, %v", f.base, err)
	}
	base := f.base
	index, err := f.CompactUserInputs(t.Context(), 0)
	if err != nil || index.Total != 24 || index.Inputs[0] != (proto.UserInputPosition{ID: "u-1#0", Seq: 3}) {
		t.Fatalf("compact Codex index %+v, %v", index, err)
	}
	previews, err := f.InputPreviews(t.Context(), proto.InputPreviewRequest{Positions: []proto.UserInputRef{
		index.Inputs[0].Ref(), {ID: "u-1#0", Seq: 4},
	}})
	if err != nil || len(previews.Inputs) != 1 || previews.Inputs[0].Answer != "Answer 1." || f.base != base {
		t.Fatalf("bounded Codex previews %+v, base %d, %v", previews, f.base, err)
	}
	win, err := f.ItemsWindow(t.Context(), proto.ItemsWindowRequest{FromSeq: 3, ThroughSeq: 6})
	if err != nil || len(win.Items) != 2 || win.Items[1].ID != "a-1#0" || win.History != index.History {
		t.Fatalf("Codex context window %+v, %v", win, err)
	}
}
