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
