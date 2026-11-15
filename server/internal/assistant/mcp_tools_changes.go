package assistant

import (
	"context"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// A cursor is process-local: rebuilding it after a manager restart invalidates old
// watches. Take it before listing tools so a concurrent link cannot be missed.
type mcpToolChanges struct {
	mu      sync.Mutex
	cursor  string
	changed chan struct{}
}

func (c *mcpToolChanges) snapshot() (string, <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.changed == nil {
		c.cursor, c.changed = randomHex(16), make(chan struct{})
	}
	return c.cursor, c.changed
}

func (c *mcpToolChanges) notify() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.changed != nil {
		close(c.changed)
	}
	c.cursor, c.changed = randomHex(16), make(chan struct{})
}

// The heartbeat lets the node re-resolve a pane whose identity arrived after its
// first list, and bounds a watch across a lost link. It is not a discovery deadline.
const mcpToolsHeartbeat = 30 * time.Second

func (s *Service) sessionMCPToolsSince(ctx context.Context, machine, session string, assistant, guest bool, since string) map[string]any {
	cursor, changed := s.mcpChanges.snapshot()
	if since == cursor {
		timer := time.NewTimer(mcpToolsHeartbeat)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return map[string]any{"revision": cursor, "unchanged": true}
		case <-timer.C:
			return map[string]any{"revision": cursor, "unchanged": true}
		case <-changed:
			cursor, _ = s.mcpChanges.snapshot()
		}
	}
	tools, err := s.sessionMCPToolsChecked(ctx, machine, session, assistant, guest)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	if tools == nil {
		tools = []proto.Tool{}
	}
	return map[string]any{"tools": tools, "revision": cursor}
}
