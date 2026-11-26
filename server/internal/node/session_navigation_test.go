package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

func TestTranscriptSessionNavigationRPC(t *testing.T) {
	n, _ := testNode(t)
	const sid = "11111111-2222-3333-4444-555555555555"
	path := filepath.Join(t.TempDir(), sid+".jsonl")
	content := `{"type":"user","uuid":"question","message":{"role":"user","content":"Earlier question"}}
{"type":"assistant","uuid":"reply","message":{"role":"assistant","content":[{"type":"text","text":"Visible answer"}]}}
{"type":"user","uuid":"another","message":{"role":"user","content":"Another question"}}
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	n.files[sid] = transcript.NewFile(path)

	raw, err := nodeCall(t, n, "session.items", map[string]any{"session_id": sid, "user_inputs": true, "meta_only": true, "before_seq": 3})
	var index proto.UserInputIndex
	if err != nil || json.Unmarshal(raw, &index) != nil || index.Total != 1 || len(index.Inputs) != 1 ||
		index.Inputs[0] != (proto.UserInputPosition{ID: "question#0", Seq: 1}) {
		t.Fatalf("transcript compact index %s, %v", raw, err)
	}
	p := proto.InputPreviewRequest{SessionID: sid, History: index.History, BeforeSeq: 2,
		Positions: []proto.UserInputRef{index.Inputs[0].Ref()}}
	raw, err = nodeCall(t, n, proto.MethodSessionInputPreviews, p)
	if err != nil || !strings.Contains(string(raw), "Earlier question") || strings.Contains(string(raw), "Visible answer") {
		t.Fatalf("exclusive bound on transcript preview %s, %v", raw, err)
	}
	p.History = "older-generation"
	if _, err := nodeCall(t, n, proto.MethodSessionInputPreviews, p); rpcCode(err) != proto.CodeNavigationChanged {
		t.Fatalf("stale generation: %v", err)
	}
	win := proto.ItemsWindowRequest{SessionID: sid, FromSeq: 2, ThroughSeq: 2, History: index.History}
	raw, err = nodeCall(t, n, proto.MethodSessionItemsWindow, win)
	var page proto.ItemsWindow
	if err != nil || json.Unmarshal(raw, &page) != nil || len(page.Items) != 1 ||
		page.Items[0].ID != "reply#0" || page.FromSeq != 1 || page.ToSeq != 2 || !page.HasMore || !page.MoreAfter {
		t.Fatalf("transcript window %s, %v", raw, err)
	}
	win.ThroughSeq = 500
	if _, err := nodeCall(t, n, proto.MethodSessionItemsWindow, win); rpcCode(err) != "bad_request" {
		t.Fatalf("unbounded RPC window: %v", err)
	}
}
