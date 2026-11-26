package connector

import (
	"encoding/json"

	"github.com/nwverse/persona/sdk/go/wire"
)

const (
	// ToolViewsMost bounds how many out-of-band views a single connector tool call may carry.
	ToolViewsMost = 4
	// ToolViewsBytesMost bounds the total JSON byte size of kept out-of-band views per tool call.
	ToolViewsBytesMost = 128 << 10
)

// SanitizeToolViews deep-clones, strips assistant-only fields and html components, rejects extension-signed image refs
// and schema/semantic failures, and bounds the kept views by ToolViewsMost and ToolViewsBytesMost.
func SanitizeToolViews(views []wire.View) []wire.View {
	if len(views) == 0 {
		return nil
	}
	var kept []wire.View
	for _, v := range views {
		if len(kept) >= ToolViewsMost {
			break
		}
		raw, err := json.Marshal(v)
		if err != nil {
			continue
		}
		var clone wire.View
		if err := json.Unmarshal(raw, &clone); err != nil {
			continue
		}
		wire.StripAssistantFields(&clone)
		sections := make([]wire.Section, 0, len(clone.Sections))
		hasRef := false
		for _, s := range clone.Sections {
			if len(s.Items) == 0 {
				continue
			}
			for i := range s.Items {
				if wire.HasImageRef(&s.Items[i]) {
					hasRef = true
					break
				}
			}
			if hasRef {
				break
			}
			sections = append(sections, s)
		}
		if hasRef || len(sections) == 0 {
			continue
		}
		clone.Sections = sections
		if err := wire.CheckView(&clone); err != nil {
			continue
		}
		next := make([]wire.View, len(kept)+1)
		copy(next, kept)
		next[len(kept)] = clone
		encoded, err := json.Marshal(next)
		if err != nil || len(encoded) > ToolViewsBytesMost {
			continue
		}
		kept = next
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}
