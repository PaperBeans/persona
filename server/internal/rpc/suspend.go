package rpc

import (
	"context"
	"errors"
	"time"
)

// ErrSuspended is a call refused before registration or writing, while a transport handover drains the old peer.
var ErrSuspended = errors.New("rpc: calls suspended")

func (p *Peer) SuspendCalls() {
	p.mu.Lock()
	p.suspended = true
	p.mu.Unlock()
}

func (p *Peer) ResumeCalls() {
	p.mu.Lock()
	p.suspended = false
	p.mu.Unlock()
}

// WaitPending waits for the replies of calls registered before SuspendCalls. The read loop must continue meanwhile.
func (p *Peer) WaitPending(ctx context.Context) error {
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		p.mu.Lock()
		idle := len(p.pending) == 0
		p.mu.Unlock()
		if idle {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.Done():
			return ErrClosed
		case <-tick.C:
		}
	}
}
