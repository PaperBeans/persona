package transcript

import (
	"cmp"
	"strconv"

	"github.com/nwverse/persona/server/internal/proto"
)

// FoldToolViews collects out-of-band declarative views carried by the tool results in run, preserving order and
// deduplicating repeated views on the same item or tool_use_id, capped at proto.FoldToolViewsMax. Nil when none.
func FoldToolViews(run []proto.Item, uses map[string]proto.Item) []proto.FoldToolView {
	var out []proto.FoldToolView
	seen := map[string]bool{}
	for _, it := range run {
		if it.Kind != proto.KindToolResult || len(it.ToolViews) == 0 {
			continue
		}
		use := uses[it.ToolUseID]
		tool := cmp.Or(use.ToolName, it.ToolName)
		baseKey := "t:" + it.ToolUseID
		if it.ID != "" {
			baseKey = "i:" + it.ID
		} else if it.ToolUseID == "" {
			continue
		}
		for idx, view := range it.ToolViews {
			key := baseKey + "#" + strconv.Itoa(idx)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, proto.FoldToolView{
				ItemID:    it.ID,
				ToolUseID: it.ToolUseID,
				Tool:      tool,
				View:      view,
			})
			if len(out) >= proto.FoldToolViewsMax {
				return out
			}
		}
	}
	return out
}

// ToolUse looks up the most recent KindToolUse item matching toolUseID in f's parsed items.
func (f *File) ToolUse(toolUseID string) (proto.Item, bool) {
	if f == nil || toolUseID == "" {
		return proto.Item{}, false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.items) - 1; i >= 0; i-- {
		it := f.items[i]
		if it.Kind == proto.KindToolUse && it.ToolUseID == toolUseID {
			return it, true
		}
	}
	return proto.Item{}, false
}
