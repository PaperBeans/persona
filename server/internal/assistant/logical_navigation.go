package assistant

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/nwverse/persona/server/internal/conversationlog"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func invalidNavigation(ctx context.Context) error {
	return &rpc.Error{Code: CodeBadRequest, Message: i18n.T(i18n.From(ctx), "chat-navigation.invalid")}
}

func (s *Service) logicalInputPreviews(ctx context.Context, st logicalConversationStore, c *db.LogicalConversation, raw json.RawMessage) (json.RawMessage, error) {
	var p proto.InputPreviewRequest
	if json.Unmarshal(raw, &p) != nil || p.BeforeSeq != 0 || !p.Valid() {
		return nil, invalidNavigation(ctx)
	}
	if p.History != "" && c.History != p.History {
		return nil, db.ErrConversationHistoryChanged
	}
	out := proto.InputPreviews{History: c.History, Inputs: []proto.UserInputPosition{}}
	for _, seg := range visibleSegments(c) {
		scoped := proto.InputPreviewRequest{SessionID: proto.NavigationSessionID(seg.SessionID)}
		if seg.FrozenEnd != nil {
			scoped.BeforeSeq = *seg.FrozenEnd + 1
		}
		for _, ref := range p.Positions {
			seq := ref.Seq - seg.Offset
			if seq <= 0 || (scoped.BeforeSeq > 0 && seq >= scoped.BeforeSeq) {
				continue
			}
			sourceID := string(ref.ID)
			if seg.Ordinal > 1 {
				segmentID, itemID, ok := proto.ParseSegmentItemID(sourceID)
				if !ok || segmentID != seg.ID || seg.ItemID(itemID) != sourceID {
					continue
				}
				sourceID = itemID
			}
			scoped.Positions = append(scoped.Positions, proto.UserInputRef{ID: proto.UserInputItemID(sourceID), Seq: seq})
		}
		if len(scoped.Positions) == 0 {
			continue
		}
		var source proto.InputPreviews
		if err := s.physicalHistoryCall(ctx, seg.MachineID, proto.MethodSessionInputPreviews, scoped, &source); err != nil {
			return nil, err
		}
		if !source.Matches(scoped) {
			return nil, errors.New("invalid source input previews")
		}
		if err := observeNavigationSource(ctx, st, seg, source.History); err != nil {
			return nil, err
		}
		for _, pos := range source.Inputs {
			seq, err := seg.Seq(pos.Seq)
			if err != nil {
				return nil, err
			}
			pos.ID, pos.Seq = seg.ItemID(pos.ID), seq
			out.Inputs = append(out.Inputs, pos)
		}
	}
	if err := s.checkLogicalRead(ctx, st, c); err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

func (s *Service) logicalItemsWindow(ctx context.Context, st logicalConversationStore, c *db.LogicalConversation, raw json.RawMessage) (json.RawMessage, error) {
	var p proto.ItemsWindowRequest
	if json.Unmarshal(raw, &p) != nil || p.BeforeSeq != 0 || !p.Valid() {
		return nil, invalidNavigation(ctx)
	}
	if p.History != "" && c.History != p.History {
		return nil, db.ErrConversationHistoryChanged
	}
	out := proto.ItemsWindow{History: c.History, Items: []proto.Item{}}
	haveCoverage, hasGap := false, false
	var knownEnd int
	for _, seg := range visibleSegments(c) {
		scoped := proto.ItemsWindowRequest{SessionID: proto.NavigationSessionID(seg.SessionID),
			FromSeq: max(1, p.FromSeq-seg.Offset), ThroughSeq: p.ThroughSeq - seg.Offset}
		if seg.FrozenEnd != nil {
			end, err := seg.Seq(*seg.FrozenEnd)
			if err != nil {
				return nil, err
			}
			knownEnd = max(knownEnd, end)
			scoped.BeforeSeq = *seg.FrozenEnd + 1
			scoped.ThroughSeq = min(scoped.ThroughSeq, *seg.FrozenEnd)
		}
		// A window ending exactly at the active segment's offset needs only its first line to establish a newer cursor.
		probeNext := seg.FrozenEnd == nil && seg.Offset == p.ThroughSeq
		if probeNext {
			scoped.ThroughSeq = 1
		}
		if scoped.ThroughSeq < scoped.FromSeq {
			continue
		}
		var source proto.ItemsWindow
		if err := s.physicalHistoryCall(ctx, seg.MachineID, proto.MethodSessionItemsWindow, scoped, &source); err != nil {
			if isMissingTranscriptErr(err) && s.isZeroTurnPreparedSegment(ctx, c, seg) {
				knownEnd = max(knownEnd, seg.Offset)
				continue
			}
			return nil, err
		}
		if source.History == "" && len(source.Items) == 0 && source.ToSeq == 0 && s.isZeroTurnPreparedSegment(ctx, c, seg) {
			knownEnd = max(knownEnd, seg.Offset)
			continue
		}
		if !source.Matches(scoped) {
			return nil, errors.New("invalid source item window")
		}
		if err := observeNavigationSource(ctx, st, seg, source.History); err != nil {
			return nil, err
		}
		from, to, err := seg.Span(source.FromSeq, source.ToSeq)
		if err != nil {
			return nil, err
		}
		knownEnd = max(knownEnd, to)
		if probeNext {
			continue
		}
		if haveCoverage {
			hasGap = hasGap || out.ToSeq != from
		} else {
			out.FromSeq = from
			haveCoverage = true
		}
		out.ToSeq = to
		out.MoreAfter = out.MoreAfter || (seg.FrozenEnd == nil && source.MoreAfter)
		for _, it := range source.Items {
			projected, visible, err := seg.Project(it, c.MachineID, c.SessionID, c.History)
			if err != nil {
				return nil, err
			}
			if visible {
				out.Items = append(out.Items, projected)
			}
		}
	}
	if err := s.checkLogicalRead(ctx, st, c); err != nil {
		return nil, err
	}
	if hasGap {
		return nil, errors.New("incomplete logical item window")
	}
	if !haveCoverage {
		out.FromSeq = min(p.FromSeq-1, knownEnd)
		out.ToSeq = out.FromSeq
	}
	out.HasMore = out.FromSeq > 0
	out.MoreAfter = out.MoreAfter || out.ToSeq < knownEnd
	out.Items = proto.WithoutImageData(out.Items)
	if !out.Matches(p) {
		return nil, errors.New("invalid logical item window")
	}
	return json.Marshal(out)
}

func observeNavigationSource(ctx context.Context, st logicalConversationStore, seg conversationlog.Segment, history string) error {
	if history == "" {
		return logicalRefusal(ctx, "missing_generation")
	}
	return st.ObserveConversationSource(ctx, seg.MachineID, seg.SessionID, history)
}
