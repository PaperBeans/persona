package assistant

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

func TestLogicalNavigationProjectsCompactIndexAndScopedPreviews(t *testing.T) {
	s, f, d, prepared := logicalPair(t, "pi")
	ctx := t.Context()
	first := []proto.Item{
		{ID: "question#0", Seq: 5, Role: "user", Kind: proto.KindText, Text: "First question"},
		{ID: "reply#0", Seq: 6, Role: "assistant", Kind: proto.KindText, Text: "Visible reply"},
		{ID: "private#0", Seq: 7, Role: "assistant", Kind: proto.KindText, Text: "Private tail"},
		{ID: "late#0", Seq: 8, Role: "user", Kind: proto.KindText, Text: "Private input"},
	}
	second := []proto.Item{
		{ID: "question#0", Seq: 1, Role: "user", Kind: proto.KindImage},
		{ID: "reply#0", Seq: 2, Role: "assistant", Kind: proto.KindText, Text: "I can see the photo."},
	}
	f.answerNode(DeviceSessionItems, func(_ string, raw json.RawMessage) (any, error) {
		var p historyParams
		if json.Unmarshal(raw, &p) != nil || !p.UserInputs || !p.MetaOnly {
			t.Fatalf("missing compact request: %s", raw)
		}
		inputs, history := transcript.UserInputPositions(second), "h2"
		if p.SessionID == "side-chat" {
			if p.Before == nil || *p.Before != 7 {
				t.Fatalf("missing frozen cursor: %s", raw)
			}
			// An older node can return an unbounded, excerpt-rich index.
			inputs, history = transcript.UserInputPositions(first), "h1"
		}
		return proto.UserInputIndex{Inputs: inputs, Total: len(inputs), History: history}, nil
	})
	var previewed []string
	f.answerNode(proto.MethodSessionInputPreviews, func(_ string, raw json.RawMessage) (any, error) {
		var p proto.InputPreviewRequest
		if json.Unmarshal(raw, &p) != nil || !p.Valid() {
			t.Fatalf("invalid source preview request: %s", raw)
		}
		previewed = append(previewed, string(p.SessionID))
		if len(p.Positions) != 1 || p.Positions[0].ID != "question#0" {
			t.Fatalf("forwarded an unrelated request: %s", raw)
		}
		if p.SessionID == "side-chat" {
			if p.BeforeSeq != 7 || p.Through(5) != 6 {
				t.Fatalf("frozen preview can reach past line 6: %s", raw)
			}
			return proto.InputPreviews{Inputs: transcript.UserInputPreviewsFor(first[:2], p.Positions), History: "h1"}, nil
		}
		if p.BeforeSeq != 0 || p.Positions[0].Seq != 1 {
			t.Fatalf("active preview sent in public coordinates: %s", raw)
		}
		return proto.InputPreviews{Inputs: transcript.UserInputPreviewsFor(second, p.Positions), History: "h2"}, nil
	})
	secondID := proto.SegmentItemID("two", "question#0")
	preview := func(history string) proto.InputPreviews {
		t.Helper()
		p := proto.InputPreviewRequest{SessionID: "side-chat", History: history, Positions: []proto.UserInputRef{
			{ID: "question#0", Seq: 5}, {ID: proto.UserInputItemID(secondID), Seq: 7}, {ID: "late#0", Seq: 8},
		}}
		b, _ := json.Marshal(p)
		raw, handled, err := s.logicalCall(ctx, "server", proto.MethodSessionInputPreviews, b)
		var out proto.InputPreviews
		if !handled || err != nil || json.Unmarshal(raw, &out) != nil || out.History != history {
			t.Fatalf("logical previews %s, handled %t, %v", raw, handled, err)
		}
		return out
	}
	// The prepared segment is private and the active segment already has its frozen boundary.
	p := proto.InputPreviewRequest{SessionID: "side-chat", History: prepared.History,
		Positions: []proto.UserInputRef{{ID: proto.UserInputItemID(secondID), Seq: 7}}}
	b, _ := json.Marshal(p)
	raw, _, err := s.logicalCall(ctx, "server", proto.MethodSessionInputPreviews, b)
	var preparedPreviews proto.InputPreviews
	if err != nil || json.Unmarshal(raw, &preparedPreviews) != nil || len(preparedPreviews.Inputs) != 0 || len(previewed) != 0 {
		t.Fatalf("prepared segment became readable: %s, %v, %v", raw, previewed, err)
	}
	previewed = nil
	c := activatePair(t, d)
	raw, _, err = s.logicalCall(ctx, "server", DeviceSessionItems, json.RawMessage(`{"session_id":"side-chat","user_inputs":true,"meta_only":true}`))
	var index proto.UserInputIndex
	wantKeys := []proto.UserInputPosition{{ID: "question#0", Seq: 5}, {ID: secondID, Seq: 7}}
	if err != nil || json.Unmarshal(raw, &index) != nil || index.Total != 2 || index.History != c.History || !slices.Equal(index.Inputs, wantKeys) {
		t.Fatalf("logical compact index %s, %v", raw, err)
	}
	got := preview(c.History)
	want := []proto.UserInputPosition{
		{ID: "question#0", Seq: 5, Preview: "First question", Answer: "Visible reply"},
		{ID: secondID, Seq: 7, Attachments: 1, Answer: "I can see the photo."},
	}
	if !slices.Equal(got.Inputs, want) || !slices.Equal(previewed, []string{"side-chat", "target-chat"}) {
		t.Fatalf("previewed %v, got %+v, want %+v", previewed, got, want)
	}
}

func TestLogicalNavigationWindowCrossesFrozenBoundary(t *testing.T) {
	s, f, d, _ := logicalPair(t, "pi")
	c := activatePair(t, d)
	var calls []proto.ItemsWindowRequest
	f.answerNode(proto.MethodSessionItemsWindow, func(_ string, raw json.RawMessage) (any, error) {
		var p proto.ItemsWindowRequest
		if json.Unmarshal(raw, &p) != nil || !p.Valid() {
			t.Fatalf("invalid window request: %s", raw)
		}
		calls = append(calls, p)
		page := proto.ItemsWindow{Items: []proto.Item{}, FromSeq: p.FromSeq - 1, ToSeq: p.ThroughSeq, HasMore: p.FromSeq > 1}
		if p.SessionID == "side-chat" {
			if p.ThroughSeq > 6 || p.BeforeSeq != 7 {
				t.Fatalf("read past frozen end: %s", raw)
			}
			page.History = "h1"
			if p.FromSeq <= 5 && p.ThroughSeq >= 5 {
				page.Items = append(page.Items, proto.Item{ID: "old#0", Seq: 5, Role: "user", Kind: proto.KindText, Text: "Earlier question"})
			}
		} else {
			page.History, page.MoreAfter = "h2", true
			page.Items = append(page.Items, proto.Item{ID: "new#0", Seq: 1, Role: "user", Kind: proto.KindImage,
				Image: &proto.Image{Mime: "image/png", Data: "fixture"}})
		}
		return page, nil
	})
	p := proto.ItemsWindowRequest{SessionID: "side-chat", FromSeq: 4, ThroughSeq: 9, History: c.History}
	body, _ := json.Marshal(p)
	raw, handled, err := s.logicalCall(t.Context(), "server", proto.MethodSessionItemsWindow, body)
	var window proto.ItemsWindow
	if !handled || err != nil || json.Unmarshal(raw, &window) != nil || !window.Matches(p) ||
		window.History != c.History || window.FromSeq != 3 || window.ToSeq != 9 || !window.HasMore || !window.MoreAfter ||
		len(window.Items) != 2 || window.Items[0].Seq != 5 || window.Items[1].Seq != 7 ||
		window.Items[1].ID != proto.SegmentItemID("two", "new#0") || window.Items[1].Image.Data != "" {
		t.Fatalf("logical window %s, %v", raw, err)
	}
	want := []proto.ItemsWindowRequest{
		{SessionID: "side-chat", FromSeq: 4, ThroughSeq: 6, BeforeSeq: 7},
		{SessionID: "target-chat", FromSeq: 1, ThroughSeq: 3},
	}
	if !slices.Equal(calls, want) {
		t.Fatalf("called %+v, want %+v", calls, want)
	}
}

func TestLogicalNavigationWindowContinuationFromRetiredSource(t *testing.T) {
	s, f, d, _ := logicalPair(t, "pi")
	c := activatePair(t, d)
	ctx := t.Context()
	if err := d.PutSetting(ctx, conversationHistoryKey, map[string]bool{"enabled": false}); err != nil {
		t.Fatal(err)
	}
	more := false
	f.answerNode(DeviceSessionItems, func(_ string, raw json.RawMessage) (any, error) {
		var p historyParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		if p.SessionID == "side-chat" {
			if intValue(p.From) != 5 || intValue(p.Through) != 6 {
				t.Fatalf("legacy range crosses the frozen end: %s", raw)
			}
			return historyPage{Items: []proto.Item{}, More: false}, nil
		}
		from, to := 0, 1
		return historyPage{History: "h2", FromSeq: &from, ToSeq: &to, Items: []proto.Item{
			{ID: "next", Seq: 1, Role: "user", Kind: proto.KindText, Text: "Next segment"},
		}, HasMore: &more}, nil
	})
	var bounds [][2]int
	f.answerNode(proto.MethodSessionItemsWindow, func(_ string, raw json.RawMessage) (any, error) {
		var p proto.ItemsWindowRequest
		if json.Unmarshal(raw, &p) != nil || p.SessionID != "side-chat" || p.BeforeSeq != 7 || p.ThroughSeq > 6 {
			t.Fatalf("unexpected source window %s", raw)
		}
		bounds = append(bounds, [2]int{p.FromSeq, p.ThroughSeq})
		return proto.ItemsWindow{History: "h1", Items: []proto.Item{{ID: "older", Seq: p.ThroughSeq}},
			FromSeq: p.FromSeq - 1, ToSeq: p.ThroughSeq, HasMore: true, MoreAfter: p.ThroughSeq < 6}, nil
	})
	raw, err := s.logicalHistoryCall(ctx, d, c, proto.MethodSessionItemsWindow, json.RawMessage(`{"from_seq":3,"through_seq":4}`))
	var window proto.ItemsWindow
	if err != nil || json.Unmarshal(raw, &window) != nil || window.ToSeq != 4 || !window.MoreAfter {
		t.Fatalf("targeted window: %s, %v", raw, err)
	}
	page, err := s.logicalHistoryPage(ctx, d, c, historyParams{After: &window.ToSeq, LazyImages: true})
	if err != nil || page.Partial || page.MoreAfter || intValue(page.FromSeq) != 4 || intValue(page.ToSeq) != 7 ||
		len(page.Items) != 2 || page.Items[0].Seq != 6 || page.Items[1].Seq != 7 ||
		!slices.Equal(bounds, [][2]int{{3, 4}, {5, 6}}) {
		t.Fatalf("noncontiguous continuation: %+v, source windows %v, %v", page, bounds, err)
	}
}

func TestLogicalNavigationRejectsChangedHistoryWithoutRetry(t *testing.T) {
	for _, method := range []string{proto.MethodSessionInputPreviews, proto.MethodSessionItemsWindow} {
		t.Run(method, func(t *testing.T) {
			s, f, d, _ := logicalPair(t, "pi")
			c := activatePair(t, d)
			calls := 0
			f.answerNode(method, func(_ string, _ json.RawMessage) (any, error) {
				calls++
				if method == proto.MethodSessionInputPreviews {
					return proto.InputPreviews{History: "replaced", Inputs: []proto.UserInputPosition{{ID: "first", Seq: 3, Preview: "Replaced"}}}, nil
				}
				return proto.ItemsWindow{History: "replaced", FromSeq: 2, ToSeq: 4, Items: []proto.Item{}}, nil
			})
			p := any(proto.InputPreviewRequest{SessionID: proto.NavigationSessionID(c.SessionID), History: c.History, Positions: []proto.UserInputRef{{ID: "first", Seq: 3}}})
			if method == proto.MethodSessionItemsWindow {
				p = proto.ItemsWindowRequest{SessionID: proto.NavigationSessionID(c.SessionID), FromSeq: 3, ThroughSeq: 4, History: c.History}
			}
			b, _ := json.Marshal(p)
			for range 2 {
				raw, handled, err := s.logicalCall(t.Context(), c.MachineID, method, b)
				if raw != nil || !handled || rpcCode(err) != proto.CodeNavigationChanged || calls != 1 {
					t.Fatalf("stale lookup: %s, handled %t, calls %d, %v", raw, handled, calls, err)
				}
			}
		})
	}
}

func TestLogicalNavigationRejectsReadThatRacesPreparingFreeze(t *testing.T) {
	s, f, d, _ := historyFixture(t)
	ctx := t.Context()
	if err := d.AdoptConversation(ctx, "server", "side-chat", "one", "claude", "h1"); err != nil {
		t.Fatal(err)
	}
	p, _ := json.Marshal(logicalSwitchRequest{OperationID: "switch", ExpectedEpoch: 1, MachineID: "server", Launcher: "pi"})
	if err := d.BeginConversationSwitch(ctx, "server", "side-chat", db.ConversationSwitch{
		OperationID: "switch", ExpectedEpoch: 1, SourceSegment: "one", Request: p,
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.PutConversationHandoff(ctx, "server", "side-chat", "switch", json.RawMessage(`{"summary":"Source summary","recent":[]}`)); err != nil {
		t.Fatal(err)
	}
	before, err := d.LogicalConversation(ctx, "server", "side-chat")
	if err != nil {
		t.Fatal(err)
	}
	f.answerNode(proto.MethodSessionInputPreviews, func(_ string, raw json.RawMessage) (any, error) {
		var req proto.InputPreviewRequest
		if json.Unmarshal(raw, &req) != nil || req.BeforeSeq != 0 {
			t.Fatalf("source was already frozen: %s", raw)
		}
		if err := d.PrepareConversationSegment(ctx, "server", "side-chat", "switch", logicalFixtureTarget("pi"), 6); err != nil {
			t.Fatal(err)
		}
		return proto.InputPreviews{History: "h1", Inputs: []proto.UserInputPosition{{ID: "question", Seq: 5, Answer: "After the freeze"}}}, nil
	})
	req, _ := json.Marshal(proto.InputPreviewRequest{SessionID: "side-chat", History: before.History,
		Positions: []proto.UserInputRef{{ID: "question", Seq: 5}}})
	raw, handled, err := s.logicalCall(ctx, "server", proto.MethodSessionInputPreviews, req)
	if raw != nil || !handled || rpcCode(err) != proto.CodeNavigationChanged {
		t.Fatalf("response crossed new freeze: %s, handled %t, %v", raw, handled, err)
	}
	after, err := d.LogicalConversation(ctx, "server", "side-chat")
	if err != nil || after.History != before.History || after.Epoch != before.Epoch || after.Segments[0].FrozenEnd == nil {
		t.Fatalf("expected the bound to move within the same history and epoch: %+v, %v", after, err)
	}
}
