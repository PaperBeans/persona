package rpc

import (
	"context"
	"sync"
)

type replyKey struct{}
type replyHooks struct {
	mu        sync.Mutex
	callbacks []func()
}

// AfterReply runs done only after the handler's reply write finishes, even if the caller cancels earlier.
// False means ctx came from a direct handler call rather than Peer.Serve; its caller must release the work itself.
func AfterReply(ctx context.Context, done func()) bool {
	hooks, ok := ctx.Value(replyKey{}).(*replyHooks)
	if !ok {
		return false
	}
	hooks.mu.Lock()
	hooks.callbacks = append(hooks.callbacks, done)
	hooks.mu.Unlock()
	return true
}

func (h *replyHooks) run() {
	h.mu.Lock()
	callbacks := h.callbacks
	h.callbacks = nil
	h.mu.Unlock()
	for _, done := range callbacks {
		done()
	}
}
