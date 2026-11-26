package node

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

// Runtimes already read bounded line ranges. Older SDKs return no history on those ranges, so compare a one-item
// probe on either side of the read. The probes are also bounded by the frozen end of a retired source.
type runtimeNavigation struct {
	node *Node
	read func(any) (json.RawMessage, error)
}

type runtimeNavigationPage struct {
	Items   []proto.Item `json:"items"`
	History string       `json:"history"`
	ToSeq   int          `json:"to_seq"`
	More    *bool        `json:"more"`
}

func runtimeNavigationUnavailable(ctx context.Context) error {
	return refusal("unsupported", i18n.T(i18n.From(ctx), "chat-navigation.unavailable"), nil)
}

func (r runtimeNavigation) page(ctx context.Context, params map[string]any) (runtimeNavigationPage, error) {
	var page runtimeNavigationPage
	if err := ctx.Err(); err != nil {
		return page, err
	}
	raw, err := r.read(params)
	if err != nil {
		return page, err
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return page, err
	}
	if page.Items == nil {
		return page, runtimeNavigationUnavailable(ctx)
	}
	return page, nil
}

func (r runtimeNavigation) probe(ctx context.Context, sid string, before int) (runtimeNavigationPage, error) {
	params := map[string]any{"session_id": sid, "limit": 1, "lazy_images": true}
	if before > 0 {
		params["before_seq"] = before
	}
	page, err := r.page(ctx, params)
	if err != nil {
		return page, err
	}
	for _, it := range page.Items {
		if it.Seq <= 0 || (before > 0 && it.Seq >= before) {
			return page, runtimeNavigationUnavailable(ctx)
		}
		page.ToSeq = max(page.ToSeq, it.Seq)
	}
	if page.History == "" && (page.ToSeq > 0 || before > 0) {
		return page, runtimeNavigationUnavailable(ctx)
	}
	return page, nil
}

func (r runtimeNavigation) rangePage(ctx context.Context, sid string, from, through int, history string) (runtimeNavigationPage, error) {
	page, err := r.page(ctx, map[string]any{
		"session_id": sid, "from_seq": from, "through_seq": through, "lazy_images": true,
	})
	if err != nil {
		return page, err
	}
	if page.More == nil || *page.More {
		return page, runtimeNavigationUnavailable(ctx)
	}
	if page.History != "" {
		if err := navigationHistory(ctx, history, page.History); err != nil {
			return page, err
		}
	}
	for _, it := range page.Items {
		if it.Seq < from || it.Seq > through {
			return page, runtimeNavigationUnavailable(ctx)
		}
	}
	sort.SliceStable(page.Items, func(i, j int) bool { return page.Items[i].Seq < page.Items[j].Seq })
	page.Items = r.node.echoes.tag(sid, echoInRuntime, page.Items)
	return page, nil
}

func (r runtimeNavigation) verify(ctx context.Context, sid string, before int, history string) error {
	page, err := r.probe(ctx, sid, before)
	if err != nil {
		return err
	}
	return navigationHistory(ctx, history, page.History)
}

func (r runtimeNavigation) InputPreviews(ctx context.Context, p proto.InputPreviewRequest) (proto.InputPreviews, error) {
	sid := string(p.SessionID)
	tail, err := r.probe(ctx, sid, p.BeforeSeq)
	if err != nil {
		return proto.InputPreviews{}, err
	}
	if err := navigationHistory(ctx, p.History, tail.History); err != nil {
		return proto.InputPreviews{}, err
	}
	verify := false
	out, err := transcript.ReadInputPreviews(ctx, p, tail.History, func(from, through int) ([]proto.Item, error) {
		through = min(tail.ToSeq, through)
		if through < from {
			return nil, nil
		}
		page, err := r.rangePage(ctx, sid, from, through, tail.History)
		verify = verify || page.History == ""
		return page.Items, err
	})
	if err == nil && verify {
		err = r.verify(ctx, sid, p.BeforeSeq, tail.History)
	}
	return out, err
}

func (r runtimeNavigation) ItemsWindow(ctx context.Context, p proto.ItemsWindowRequest) (proto.ItemsWindow, error) {
	sid := string(p.SessionID)
	tail, err := r.probe(ctx, sid, p.BeforeSeq)
	if err != nil {
		return proto.ItemsWindow{}, err
	}
	if err := navigationHistory(ctx, p.History, tail.History); err != nil {
		return proto.ItemsWindow{}, err
	}
	end := tail.ToSeq
	if p.BeforeSeq > 0 {
		end = p.BeforeSeq - 1 // the frozen interval can end with lines that have no items
	}
	from, to := min(p.FromSeq-1, end), min(p.ThroughSeq, end)
	out := proto.ItemsWindow{Items: []proto.Item{}, History: tail.History, FromSeq: from, ToSeq: to,
		HasMore: from > 0, MoreAfter: to < end}
	if from == to {
		return out, nil
	}
	page, err := r.rangePage(ctx, sid, from+1, to, tail.History)
	if err != nil {
		return proto.ItemsWindow{}, err
	}
	if page.History == "" {
		if err := r.verify(ctx, sid, p.BeforeSeq, tail.History); err != nil {
			return proto.ItemsWindow{}, err
		}
	}
	out.Items = proto.WithoutImageData(page.Items)
	r.node.toolViews.attach(sid, out.Items, false, nil)
	r.node.ttft.annotate(sid, out.Items, false)
	return out, nil
}
