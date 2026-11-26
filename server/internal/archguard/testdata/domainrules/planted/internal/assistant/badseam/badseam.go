// Package badseam is a registration seam with an open field and an untyped port.
//
//persona:layer application seam
package badseam

import "sync"

// Hooks is the modules' hooks.
type Hooks struct {
	mu    sync.Mutex
	Named map[string]func() // seam-exported-field
}

// Register keeps a module's port.
func (h *Hooks) Register(name string, port any) {} // seam-any

type Store interface {
	Load() any           // seam-any
	Save(map[string]any) // seam-any
}

type Events func(map[string]any) // seam-any
type Payload map[string]any      // seam-any
type Value = interface{}         // seam-any

type Embedded interface {
	interface {
		Send(any) // seam-any
	}
}
