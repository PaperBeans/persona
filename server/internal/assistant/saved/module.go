package saved

import (
	"context"
	"net/http"

	"github.com/nwverse/persona/server/internal/assistant/kernel/seam"
	"github.com/nwverse/persona/server/internal/domainkit/defect"
)

// Module is saved as a module of the assistant's kernel (kernel/seam): it holds what saved takes from the assistant,
// given once through NewModule, and registers saved's routes through its Kernel. Host stays saved's own adapter,
// built here from those dependencies and the Kernel's services. The zero Module has no store; only NewModule makes
// one.
type Module struct {
	store  func() any
	events func(ctx context.Context, ev map[string]any)
}

// NewModule is saved with the assistant's store (store returns nil when there is none) and the events that tell the
// apps what changed. A nil function is a defect.
//
// Its parameters are saved's legacy shape, not an example to copy: a new module takes typed port interfaces through
// its NewModule (docs/server/architecture.md "Validated and sealed"). The store is untyped and the events are maps
// because saved.Host always took them that way and open drafts edit saved.go and the files that assert saved's ports
// back out of the store; #15222 gives saved typed ports once they land.
func NewModule(store func() any, events func(ctx context.Context, ev map[string]any)) Module {
	if store == nil {
		defect.Raise("saved.NewModule", "the store is nil")
	}
	if events == nil {
		defect.Raise("saved.NewModule", "the events are nil")
	}
	return Module{store: store, events: events}
}

// Name is saved's name in the kernel.
func (Module) Name() seam.ModuleName { return seam.NewModuleName("saved") }

// Register adds saved's routes through k. A zero Module is a defect.
func (m Module) Register(k seam.Kernel) {
	if m.store == nil {
		defect.Raise("saved.Module.Register", "a Module comes from NewModule")
	}
	Register(Host{
		Store:   m.store,
		Event:   m.events,
		Handle:  func(pattern string, fn http.HandlerFunc) { k.Route(seam.NewRoute(pattern, fn)) },
		Full:    k.Full,
		BodyErr: k.BodyError,
	})
}
