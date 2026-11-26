package node

import (
	"context"
	"encoding/json"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

type sessionNavigation interface {
	InputPreviews(context.Context, proto.InputPreviewRequest) (proto.InputPreviews, error)
	ItemsWindow(context.Context, proto.ItemsWindowRequest) (proto.ItemsWindow, error)
}

func serveSessionNavigation(ctx context.Context, source sessionNavigation, method string, raw json.RawMessage) (any, error) {
	if method == proto.MethodSessionInputPreviews {
		var p proto.InputPreviewRequest
		if json.Unmarshal(raw, &p) != nil || p.SessionID == "" || !p.Valid() {
			return nil, refusal("bad_request", i18n.T(i18n.From(ctx), "chat-navigation.invalid"), nil)
		}
		out, err := source.InputPreviews(ctx, p)
		if err != nil {
			return nil, err
		}
		return out, navigationHistory(ctx, p.History, out.History)
	}
	var p proto.ItemsWindowRequest
	if json.Unmarshal(raw, &p) != nil || p.SessionID == "" || !p.Valid() {
		return nil, refusal("bad_request", i18n.T(i18n.From(ctx), "chat-navigation.invalid"), nil)
	}
	out, err := source.ItemsWindow(ctx, p)
	if err != nil {
		return nil, err
	}
	return out, navigationHistory(ctx, p.History, out.History)
}

func navigationHistory(ctx context.Context, expected, actual string) error {
	if expected != "" && expected != actual {
		return refusal(proto.CodeNavigationChanged, i18n.T(i18n.From(ctx), "chat-navigation.history_changed"), nil)
	}
	return nil
}

func (n *Node) transcriptNavigation(ctx context.Context, id, method string, raw json.RawMessage) (any, error) {
	f, err := n.file(id)
	if err != nil {
		if n.startingSession(id) {
			return serveSessionNavigation(ctx, new(transcript.File), method, raw)
		}
		return nil, n.readErr(id, err)
	}
	if err := n.refreshForPage(id, f); err != nil {
		return nil, err
	}
	out, err := serveSessionNavigation(ctx, f, method, raw)
	if page, ok := out.(proto.ItemsWindow); ok && err == nil {
		n.toolViews.attach(id, page.Items, false, nil)
		n.ttft.annotate(id, page.Items, false)
		page.Items = n.echoes.tag(id, echoInTranscript, page.Items)
		return page, nil
	}
	return out, err
}

func (h agyHistory) InputPreviews(ctx context.Context, p proto.InputPreviewRequest) (proto.InputPreviews, error) {
	return transcript.ReadInputPreviews(ctx, p, h.token, func(from, through int) ([]proto.Item, error) {
		return h.items[agyIndex(h.items, from):agyAfter(h.items, through)], nil
	})
}

func (h agyHistory) ItemsWindow(_ context.Context, p proto.ItemsWindowRequest) (proto.ItemsWindow, error) {
	end := h.seq
	if p.BeforeSeq > 0 {
		end = min(end, p.BeforeSeq-1)
	}
	from, to := min(p.FromSeq-1, end), min(p.ThroughSeq, end)
	items := h.items[agyAfter(h.items, from):agyAfter(h.items, to)]
	return proto.ItemsWindow{Items: agyLightItems(items), History: h.token, FromSeq: from, ToSeq: to,
		HasMore: from > 0, MoreAfter: to < end}, nil
}
