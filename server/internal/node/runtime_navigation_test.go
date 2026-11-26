package node

import (
	"encoding/json"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestRuntimeNavigationUsesBoundedSourceReads(t *testing.T) {
	n, _ := runtimeNode(t)
	host := dialRuntime(t, n, dshHello())
	const sid = "session-navigation"
	items := []proto.Item{
		{ID: "first#0", Seq: 5, Role: "user", Kind: proto.KindText, Text: "First question"},
		{ID: "first-reply#0", Seq: 6, Role: "assistant", Kind: proto.KindText, Text: "First answer"},
		{ID: "second#0", Seq: 210, Role: "user", Kind: proto.KindImage, Image: &proto.Image{Mime: "image/png", Data: "fixture"}},
		{ID: "second-reply#0", Seq: 211, Role: "assistant", Kind: proto.KindText, Text: "Second answer"},
		{ID: "latest#0", Seq: 1000, Role: "user", Kind: proto.KindText, Text: "Far from the target"},
	}
	var probes atomic.Int32
	host.answer = func(method string, raw json.RawMessage) (any, error) {
		if method != "session.items" {
			return nil, fmt.Errorf("unexpected method %s", method)
		}
		var p struct {
			Before int  `json:"before_seq"`
			From   int  `json:"from_seq"`
			To     int  `json:"through_seq"`
			Limit  int  `json:"limit"`
			Lazy   bool `json:"lazy_images"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		if !p.Lazy {
			return nil, fmt.Errorf("source did not request lazy images: %s", raw)
		}
		page := []proto.Item{}
		if p.From > 0 {
			for _, it := range items {
				if it.Seq >= p.From && it.Seq <= p.To {
					page = append(page, it)
				}
			}
			// The older SDK's range response has no history token.
			return map[string]any{"items": page, "more": false}, nil
		}
		history := "source-history"
		if p.Limit == 1 {
			probes.Add(1)
		}
		for _, it := range items {
			if p.Before <= 0 || it.Seq < p.Before {
				page = append(page, it)
			}
		}
		if p.Limit == 1 && len(page) > 0 {
			page = page[len(page)-1:]
		}
		return map[string]any{"items": page, "history": history, "has_more": false}, nil
	}
	_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{{"session_id": sid, "status": "idle"}}})
	waitFor(t, "runtime session listed", func() bool { _, ok := listed(t, n)[sid]; return ok })

	raw, err := nodeCall(t, n, "session.items", map[string]any{"session_id": sid, "user_inputs": true, "meta_only": true, "before_seq": 211})
	var index proto.UserInputIndex
	wantPositions := []proto.UserInputPosition{{ID: "first#0", Seq: 5}, {ID: "second#0", Seq: 210}}
	if err != nil || json.Unmarshal(raw, &index) != nil || index.Total != 2 || !slices.Equal(index.Inputs, wantPositions) {
		t.Fatalf("runtime compact index %s, %v", raw, err)
	}
	raw, err = nodeCall(t, n, proto.MethodSessionInputPreviews, proto.InputPreviewRequest{SessionID: sid, History: index.History, BeforeSeq: 211,
		Positions: []proto.UserInputRef{wantPositions[1].Ref(), wantPositions[0].Ref(), {ID: "latest#0", Seq: 1000}}})
	var p proto.InputPreviews
	want := []proto.UserInputPosition{{ID: "first#0", Seq: 5, Preview: "First question", Answer: "First answer"}, {ID: "second#0", Seq: 210, Attachments: 1}}
	if err != nil || json.Unmarshal(raw, &p) != nil || !slices.Equal(p.Inputs, want) {
		t.Fatalf("runtime previews %s, %v", raw, err)
	}
	if got := host.got("session.items"); len(got) != 5 || probes.Load() != 2 {
		t.Fatalf("expected one compact scan, two ranges and two probes, got %s", got)
	} else {
		for i, v := range []struct{ before, from, to int }{{211, 0, 0}, {0, 5, 132}, {0, 210, 210}, {211, 0, 0}} {
			var q struct {
				Before int `json:"before_seq"`
				From   int `json:"from_seq"`
				To     int `json:"through_seq"`
			}
			if json.Unmarshal(got[i+1], &q) != nil || q.Before != v.before || q.From != v.from || q.To != v.to {
				t.Fatalf("runtime read %d: %s, want %+v", i, got[i+1], v)
			}
		}
	}
	raw, err = nodeCall(t, n, proto.MethodSessionItemsWindow, proto.ItemsWindowRequest{SessionID: sid, FromSeq: 208, ThroughSeq: 212})
	var window proto.ItemsWindow
	if err != nil || json.Unmarshal(raw, &window) != nil || window.FromSeq != 207 || window.ToSeq != 212 ||
		!window.MoreAfter || !window.HasMore || len(window.Items) != 2 || window.Items[0].Image == nil || window.Items[0].Image.Data != "" {
		t.Fatalf("runtime context window %s, %v", raw, err)
	}
	if got := host.got("session.items"); len(got) != 8 {
		t.Fatalf("context fetch should take a single range and two probes: %s", got)
	}
}

func TestRuntimeNavigationRejectsRewritesAndIgnoredRanges(t *testing.T) {
	for _, rewrite := range []bool{true, false} {
		t.Run(fmt.Sprint("rewrite=", rewrite), func(t *testing.T) {
			calls := 0
			n := &Node{}
			source := runtimeNavigation{node: n, read: func(params any) (json.RawMessage, error) {
				calls++
				if params.(map[string]any)["through_seq"] != nil {
					if rewrite {
						return json.RawMessage(`{"items":[{"id":"input","seq":10,"role":"user","kind":"text","text":"Question"}],"more":false}`), nil
					}
					return json.RawMessage(`{"items":[{"id":"outside","seq":200}],"has_more":false,"history":"first","to_seq":200}`), nil
				}
				history := "first"
				if calls > 1 {
					history = "replacement"
				}
				return json.Marshal(map[string]any{"items": []proto.Item{}, "history": history, "to_seq": 200})
			}}
			result, err := source.InputPreviews(t.Context(), proto.InputPreviewRequest{SessionID: "fixture",
				Positions: []proto.UserInputRef{{ID: "input", Seq: 10}}})
			want := "unsupported"
			if rewrite {
				want = proto.CodeNavigationChanged
			}
			if rpcCode(err) != want {
				t.Fatalf("runtime returned %+v, %v, want %s", result, err, want)
			}
		})
	}
}
