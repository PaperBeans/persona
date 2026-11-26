package transcript

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestUserInputPositionsCountsInputsNotTurns(t *testing.T) {
	source := &proto.MessageSource{Kind: proto.SourceConversation}
	text := func(id string, seq int, value string) proto.Item {
		return proto.Item{ID: id, Seq: seq, Role: "user", Kind: proto.KindText, Text: value}
	}
	first := text("same-a#0", 1, "Repeated input")
	queued := text("same-b#0", 2, "Repeated input")
	queued.Queued = true
	auto := text("auto#0", 3, "Scheduled input")
	auto.Source = source
	peer := text("peer#0", 4, "Peer input")
	peer.From = "example-peer"
	wake := text("wake#0", 5, "Wake input")
	wake.Wake = true
	unknown := text("unknown#0", 6, "Unknown source")
	unknown.Source = &proto.MessageSource{Kind: "future-source"}
	protected := text("protected#0", 7, prompts.MessageSourceText("An authored input with context", nil))
	protected.ClientMsgID = "synthetic-send"
	img := proto.Item{ID: "picture#0", Seq: 8, Role: "user", Kind: proto.KindImage}
	imageSibling := img
	imageSibling.ID = "picture#1"
	toolImage := img
	toolImage.ID = "tool-image#0"
	toolImage.Seq = 9
	toolImage.ToolUseID = "tool"
	autoImg := img
	autoImg.ID = "auto#1"
	autoImg.Seq = 3
	inputs := []proto.Item{first, queued, auto, autoImg, peer, wake, unknown, protected, img, imageSibling, toolImage, text("loop#0", 10, "/loop check again"), text("empty#0", 11, " ")}
	want := []proto.UserInputPosition{
		{ID: first.ID, Seq: 1, Preview: "Repeated input"},
		{ID: queued.ID, Seq: 2, Preview: "Repeated input"},
		{ID: protected.ID, Seq: 7, Preview: "An authored input with context"},
		{ID: img.ID, Seq: 8, Attachments: 2},
	}
	if got := UserInputPositions(inputs); !reflect.DeepEqual(got, want) {
		t.Fatalf("positions %v, want %v", got, want)
	}
}

func TestUserInputPositionsProjectsOnlyTheTurnsVisibleText(t *testing.T) {
	items := []proto.Item{
		{ID: "ask#0", Seq: 1, Role: "user", Kind: proto.KindText, Text: "<persona-context>private snapshot</persona-context>\n" +
			"<USER_REQUEST>\n\t> quoted passage\n\n  Is the build ready?  \nsecond line\n</USER_REQUEST>\n" +
			"<ADDITIONAL_METADATA>private metadata</ADDITIONAL_METADATA>"},
		{ID: "ask#1", Seq: 1, Role: "user", Kind: proto.KindImage, Image: &proto.Image{Data: "fixture-image-bytes"}},
		{ID: "progress#0", Seq: 2, Role: "assistant", Kind: proto.KindText, Text: "I am checking", Narration: true},
		{ID: "first#0", Seq: 3, Role: "assistant", Kind: proto.KindText, Text: "<!-- private note -->\n## **Build** passed."},
		{ID: "tool#0", Seq: 4, Role: "assistant", Kind: proto.KindToolUse, ToolUseID: "tool", ToolInput: json.RawMessage(`{"fixture":"tool input"}`)},
		{ID: "tool#1", Seq: 5, Role: "user", Kind: proto.KindToolResult, ToolUseID: "tool", Text: "tool output"},
		{ID: "tool#2", Seq: 5, Role: "user", Kind: proto.KindImage, ToolUseID: "tool", Image: &proto.Image{Data: "tool image bytes"}},
		{ID: "thinking#0", Seq: 6, Role: "assistant", Kind: proto.KindThinking, Text: "private thinking"},
		{ID: "feedback#0", Seq: 7, Role: "user", Kind: proto.KindText, Text: "<persona-view-errors>hidden feedback</persona-view-errors>"},
		{ID: "system#0", Seq: 8, Role: "system", Kind: proto.KindText, Text: "system text"},
		{ID: "second#0", Seq: 9, Role: "assistant", Kind: proto.KindText, Text: "~~~log\nraw output\n~~~\n" +
			"- Read the [report](https://example.com/report) using `check`.\n\n![hidden screenshot](data:image/png;base64,hidden-bytes)"},
		{ID: "tool-text#0", Seq: 10, Role: "assistant", Kind: proto.KindText, ToolUseID: "tool", Text: "another tool output"},
		{ID: "images#0", Seq: 11, Role: "user", Kind: proto.KindImage},
		{ID: "images#1", Seq: 11, Role: "user", Kind: proto.KindText, Text: "[Image #1]\n<attached_images>\n/example/photo.png\npath: /example/photo.png\n</attached_images>\n" +
			`<persona-file kind="file" path="/example/notes.txt" />`},
		{ID: "third#0", Seq: 12, Role: "assistant", Kind: proto.KindText, Text: "I see the attachments."},
		{ID: "wake#0", Seq: 13, Role: "system", Kind: proto.KindNotice, Wake: true},
		{ID: "wake-reply#0", Seq: 14, Role: "assistant", Kind: proto.KindText, Text: "Unrelated background reply"},
		{ID: "quote#0", Seq: 15, Role: "user", Kind: proto.KindText, Text: "> <USER_REQUEST>quote only</USER_REQUEST>"},
		{ID: "pending#0", Seq: 16, Role: "user", Kind: proto.KindText, Text: "Still working?", Queued: true},
		{ID: "thinking#1", Seq: 17, Role: "assistant", Kind: proto.KindThinking, Text: "private thought"},
	}
	want := []proto.UserInputPosition{
		{ID: "ask#0", Seq: 1, Preview: "Is the build ready?", Answer: "Build passed. Read the report using check."},
		{ID: "images#0", Seq: 11, Attachments: 3, Answer: "I see the attachments."},
		{ID: "quote#0", Seq: 15},
		{ID: "pending#0", Seq: 16, Preview: "Still working?"},
	}
	if got := UserInputPositions(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("positions %+v, want %+v", got, want)
	}
}

func TestUserInputPositionsCapsRunesAndKeepsAuthoredContexts(t *testing.T) {
	items := []proto.Item{
		{ID: "input#0", Seq: 1, Role: "user", Kind: proto.KindText, Text: strings.Repeat("问", 110)},
		{ID: "reply#0", Seq: 2, Role: "assistant", Kind: proto.KindText, Text: "**" + strings.Repeat("答", 110) + "**"},
		{ID: "reply#1", Seq: 3, Role: "assistant", Kind: proto.KindText, Text: strings.Repeat("文", 100)},
		{ID: "context#0", Seq: 4, Role: "user", Kind: proto.KindText, Text: `<persona-context kind="doc" title="Notes">hidden text</persona-context>`},
		{ID: "progress#0", Seq: 5, Role: "assistant", Kind: proto.KindText, Text: "Latest progress", Narration: true},
		{ID: "clipped#0", Seq: 6, Role: "user", Kind: proto.KindText, Text: "Own words\n<persona-context kind=\"doc\">truncated context"},
		{ID: "clipped#1", Seq: 7, Role: "assistant", Kind: proto.KindText, Text: "Public words <!-- clipped internal comment"},
	}
	got := UserInputPositions(items)
	want := []proto.UserInputPosition{
		{ID: "input#0", Seq: 1, Preview: strings.Repeat("问", 99) + "…", Answer: strings.Repeat("答", 110) + " " + strings.Repeat("文", 69)},
		{ID: "context#0", Seq: 4, Answer: "Latest progress"},
		{ID: "clipped#0", Seq: 6, Preview: "Own words", Answer: "Public words"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("positions %+v, want %+v", got, want)
	}
	if p := got[0]; !utf8.ValidString(p.Preview) || utf8.RuneCountInString(p.Preview) != 100 || len(p.Preview) > 400 ||
		!utf8.ValidString(p.Answer) || utf8.RuneCountInString(p.Answer) != 180 || len(p.Answer) > 720 {
		t.Fatalf("invalid preview or answer bounds: %+v", p)
	}
}

func TestUserInputPositionsKeepsAutomationInItsOwnExchange(t *testing.T) {
	items := []proto.Item{
		{ID: "first#0", Seq: 1, Role: "user", Kind: proto.KindText, Text: "First"},
		{ID: "ignored#0", Seq: 2, Role: "user", Kind: proto.KindText, Text: "Internal note", Queued: true, Injected: &proto.Injected{Kind: proto.InjectedTaskNotice}},
		{ID: "reply#0", Seq: 3, Role: "assistant", Kind: proto.KindText, Text: "Owner reply"},
		{ID: "auto#0", Seq: 4, Role: "user", Kind: proto.KindText, Text: "Automatic run", Source: &proto.MessageSource{Kind: proto.SourcePatrol}},
		{ID: "auto-reply#0", Seq: 5, Role: "assistant", Kind: proto.KindText, Text: "Automatic reply"},
		{ID: "loop#0", Seq: 6, Role: "user", Kind: proto.KindText, Text: "<USER_REQUEST>/loop check again</USER_REQUEST>"},
		{ID: "second#0", Seq: 7, Role: "user", Kind: proto.KindText, Text: "Second"},
		{ID: "tool#0", Seq: 8, Role: "assistant", Kind: proto.KindToolUse, ToolUseID: "pending"},
	}
	want := []proto.UserInputPosition{
		{ID: "first#0", Seq: 1, Preview: "First", Answer: "Owner reply"},
		{ID: "second#0", Seq: 7, Preview: "Second"},
	}
	if got := UserInputPositions(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("positions %+v, want %+v", got, want)
	}
}

func TestCompactInputIndexAndPreviewsShareLegacyClassification(t *testing.T) {
	items := []proto.Item{
		{ID: "question#0", Seq: 1, Role: "user", Kind: proto.KindText, Text: "Question"},
		{ID: "reply#0", Seq: 2, Role: "assistant", Kind: proto.KindText, Text: "**Visible** reply"},
		{ID: "pictures#0", Seq: 3, Role: "user", Kind: proto.KindImage},
		{ID: "pictures#1", Seq: 3, Role: "user", Kind: proto.KindImage},
		{ID: "loop#0", Seq: 4, Role: "user", Kind: proto.KindText, Text: "[Image #1] /loop daily"},
		{ID: "unrelated#0", Seq: 5, Role: "assistant", Kind: proto.KindText, Text: "Scheduled reply"},
	}
	var compact CompactInputIndex
	compact.Add(items[4:]) // A runtime reads newest pages first and keeps siblings together.
	compact.Add(items[:4])
	got := compact.Index("fixture")
	want := []proto.UserInputPosition{{ID: "question#0", Seq: 1}, {ID: "pictures#0", Seq: 3}}
	if got.Total != len(want) || got.History != "fixture" || !reflect.DeepEqual(got.Inputs, want) {
		t.Fatalf("compact index: %+v", got)
	}
	previews := UserInputPreviewsFor(items, []proto.UserInputRef{
		{ID: "pictures#0", Seq: 3}, {ID: "question#0", Seq: 1}, {ID: "question#0", Seq: 5},
	})
	if legacy := UserInputPositions(items); !reflect.DeepEqual(previews, legacy) {
		t.Fatalf("previews %+v, legacy %+v", previews, legacy)
	}
}

func TestUserInputsIncludesColdCompactedHistoryAndRewrite(t *testing.T) {
	old := windowBytes
	windowBytes = 512
	t.Cleanup(func() { windowBytes = old })
	path := filepath.Join(t.TempDir(), "synthetic.jsonl")
	var content strings.Builder
	for i := 1; i <= 36; i++ {
		record := map[string]any{"type": "user", "uuid": fmt.Sprintf("input-%03d", i), "message": map[string]any{"role": "user", "content": fmt.Sprintf("Synthetic input %03d.\n%s", i, strings.Repeat("hidden-full-body ", 20))}}
		b, _ := json.Marshal(record)
		content.Write(b)
		content.WriteByte('\n')
		content.WriteString(fmt.Sprintf(`{"type":"assistant","uuid":"answer-%03d","message":{"role":"assistant","content":[{"type":"text","text":"Synthetic answer %03d."}]}}`+"\n", i, i))
		if i%6 == 0 {
			content.WriteString(`{"type":"system","subtype":"compact_boundary","content":"Synthetic compaction"}` + "\n")
		}
	}
	if err := os.WriteFile(path, []byte(content.String()), 0600); err != nil {
		t.Fatal(err)
	}
	f := NewFile(path)
	if _, err := f.Refresh(); err != nil {
		t.Fatal(err)
	}
	initial, _, _ := f.PageTurns(0, 2, 2000)
	if got := len(UserInputPositions(initial)); got != 2 {
		t.Fatalf("tail inputs = %d", got)
	}
	coldBase := f.base
	if coldBase <= 10 {
		t.Fatalf("fixture unexpectedly warm: %d", coldBase)
	}
	// A fresh node can seek an old position before it has served a full index or any earlier page.
	previews, err := f.InputPreviews(t.Context(), proto.InputPreviewRequest{Positions: []proto.UserInputRef{
		{ID: "input-001#0", Seq: 1}, {ID: "input-002#0", Seq: 3}, {ID: "stale#0", Seq: 4},
	}, BeforeSeq: 4})
	wantPreviews := []proto.UserInputPosition{
		{ID: "input-001#0", Seq: 1, Preview: "Synthetic input 001.", Answer: "Synthetic answer 001."},
		{ID: "input-002#0", Seq: 3, Preview: "Synthetic input 002."},
	}
	if err != nil || !reflect.DeepEqual(previews.Inputs, wantPreviews) {
		t.Fatalf("bounded cold previews %+v, %v", previews, err)
	}
	window, err := f.ItemsWindow(t.Context(), proto.ItemsWindowRequest{FromSeq: 3, ThroughSeq: 5})
	if err != nil || window.FromSeq != 2 || window.ToSeq != 5 || !window.HasMore || !window.MoreAfter ||
		len(window.Items) != 3 || window.Items[0].ID != "input-002#0" || window.History != previews.History {
		t.Fatalf("detached cold window %+v, %v", window, err)
	}
	compact, err := f.CompactUserInputs(t.Context(), 0)
	if err != nil || compact.Total != 36 || f.base != coldBase {
		t.Fatalf("compact count %d backed the file up from %d to %d, error %v", compact.Total, coldBase, f.base, err)
	}
	compactFrozen, err := f.CompactUserInputs(t.Context(), 4)
	if err != nil || compactFrozen.Total != 2 || compactFrozen.Inputs[1] != wantPreviews[1].WithoutExcerpts() {
		t.Fatalf("exclusive compact bound: %+v, %v", compactFrozen, err)
	}
	index, err := f.UserInputs(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if index.Total != 36 || len(index.Inputs) != 36 || index.Inputs[0].ID != "input-001#0" {
		t.Fatalf("index %+v", index)
	}
	for i, it := range index.Inputs {
		if compact.History != index.History || compact.Inputs[i] != it.WithoutExcerpts() {
			t.Fatalf("compact position %d differs from legacy: %+v vs %+v", i, compact.Inputs[i], it)
		}
	}
	if first, last := index.Inputs[0], index.Inputs[35]; first.Preview != "Synthetic input 001." || first.Answer != "Synthetic answer 001." ||
		last.Preview != "Synthetic input 036." || last.Answer != "Synthetic answer 036." {
		t.Fatalf("cold and latest positions: %+v / %+v", first, last)
	}
	raw, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "hidden-full-body") || strings.Contains(string(raw), `"message"`) {
		t.Fatalf("body leaked: %s", raw)
	}
	frozen, err := f.UserInputs(context.Background(), 2)
	wantFrozen := proto.UserInputPosition{ID: index.Inputs[0].ID, Seq: 1, Preview: "Synthetic input 001."}
	if err != nil || frozen.Total != 1 || frozen.Inputs[0] != wantFrozen || frozen.History != index.History {
		t.Fatalf("frozen index %+v, %v", frozen, err)
	}
	if err := os.WriteFile(path, []byte(`{"type":"user","uuid":"replacement","message":{"role":"user","content":"A new synthetic history"}}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Refresh(); err != nil {
		t.Fatal(err)
	}
	replaced, err := f.UserInputs(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Total != 1 || replaced.History == index.History {
		t.Fatalf("rewrite %+v", replaced)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.UserInputs(ctx, 0); err == nil {
		t.Fatal("cancelled scan returned a total")
	}
}
