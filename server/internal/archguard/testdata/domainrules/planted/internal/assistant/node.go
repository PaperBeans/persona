package assistant

import "context"

// host is the node an agent runs on.
type host struct{ name string }

// Agent asks the node for its agent; a method named like a concept is no visitor.
func (h *host) Agent(ctx context.Context) error { return ctx.Err() }
