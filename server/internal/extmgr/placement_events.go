package extmgr

import (
	"encoding/json"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// placement.pending is a one-way wake hint emitted when a task awaits automatic machine placement. Only the three
// opaque identifiers are forwarded, and only to subscribers granted the named-only tasks.schedule scope.
func init() {
	onAssistantEvent(assistantwire.EventPlacementPending, func(h *Manager, raw json.RawMessage) {
		var p assistantwire.PlacementPendingEvent
		if json.Unmarshal(raw, &p) != nil || p.PlanID == "" || p.TodoID == "" || p.BatchSize <= 0 {
			return
		}
		if h.extensions.todoWanted(assistantwire.EventPlacementPending) {
			h.extensions.queueOn(&h.extensions.todoEvents, extensionNotice{
				topic: assistantwire.EventPlacementPending,
				payload: map[string]any{
					"plan_id":    string(p.PlanID),
					"todo_id":    string(p.TodoID),
					"batch_size": p.BatchSize,
				},
			})
		}
	})
}
