package extmgr

import (
	"context"
	"encoding/json"

	"github.com/nwverse/persona/server/internal/extension"
)

// DomainTrackKinds returns the enabled extensions' declarative track kinds.
// The assistant validates their configurations before applying them. Disabled
// and removed contributions disappear without deleting any track records.
func (h *Manager) DomainTrackKinds(context.Context) ([]extension.TrackKindItem, error) {
	if h.extensions == nil || h.extensions.reg == nil {
		return nil, nil
	}
	var out []extension.TrackKindItem
	for _, p := range h.extensions.reg.List() {
		if !p.Record.Enabled || p.Manifest == nil {
			continue
		}
		out = append(out, p.Manifest.TrackKinds()...)
	}
	return out, nil
}

// DomainTrackKindsJSON is the small in-process seam used by the assistant.
func (h *Manager) DomainTrackKindsJSON(ctx context.Context) (json.RawMessage, error) {
	items, err := h.DomainTrackKinds(ctx)
	if err != nil {
		return nil, err
	}
	return json.Marshal(items)
}
