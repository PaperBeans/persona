package node

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

// Runtime sessions use their existing item paging, without requiring #2071 or a new runtime SDK method.
// The server reduces a complete, coherent read to the input index. A partial scan never supplies a total.
func (n *Node) runtimeUserInputs(ctx context.Context, sid string, before int, metaOnly bool, read func(any) (json.RawMessage, error)) (proto.UserInputIndex, error) {
	var compact transcript.CompactInputIndex
	var all []proto.Item
	history := ""
	for {
		if err := ctx.Err(); err != nil {
			return proto.UserInputIndex{}, err
		}
		params := map[string]any{"session_id": sid, "limit": 500, "lazy_images": true}
		if before > 0 {
			params["before_seq"] = before
		}
		raw, err := read(params)
		if err != nil {
			return proto.UserInputIndex{}, err
		}
		raw = n.echoedRaw(sid, raw, "items")
		var page struct {
			Items   []proto.Item `json:"items"`
			HasMore bool         `json:"has_more"`
			History string       `json:"history"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return proto.UserInputIndex{}, err
		}
		if history != "" && page.History != "" && history != page.History {
			return proto.UserInputIndex{}, errors.New("user input history changed")
		}
		if page.History != "" {
			history = page.History
		}
		visible := page.Items[:0]
		for _, it := range page.Items {
			if before <= 0 || it.Seq < before {
				visible = append(visible, it)
			}
		}
		if metaOnly {
			sort.SliceStable(visible, func(i, j int) bool { return visible[i].Seq < visible[j].Seq })
			compact.Add(visible)
		} else {
			all = append(all, visible...)
		}
		if !page.HasMore {
			break
		}
		next := before
		for _, it := range visible {
			if next == 0 || it.Seq < next {
				next = it.Seq
			}
		}
		if next <= 0 || (before > 0 && next >= before) {
			return proto.UserInputIndex{}, errors.New("user input history made no progress")
		}
		before = next
	}
	if metaOnly {
		return compact.Index(history), nil
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Seq < all[j].Seq })
	positions := transcript.UserInputPositions(all)
	return proto.UserInputIndex{Total: len(positions), Inputs: positions, History: history}, nil
}
