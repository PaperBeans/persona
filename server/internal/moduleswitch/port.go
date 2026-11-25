package moduleswitch

import "sync"

// Port is a provider seam the composition root fills: one provider of type T, kept under the id of the module that
// provided it, and handed out only to a caller whose Set has that module on. The zero Port has no provider (a build
// without the module). Declare it as a package variable named after its port: `var port = moduleswitch.NewPort[T]("githubport")`.
type Port[T any] struct {
	name   string
	mu     sync.RWMutex
	module string
	v      T
	ok     bool
}

// NewPort is the port named name, without a provider.
func NewPort[T any](name string) *Port[T] { return &Port[T]{name: name} }

// Provide sets the module's provider; the composition root calls it from init. It records the seam (Provided).
func (p *Port[T]) Provide(module string, v T) {
	p.mu.Lock()
	if p.ok {
		p.mu.Unlock()
		panic("moduleswitch: port " + p.name + " provided twice")
	}
	p.module, p.v, p.ok = module, v, true
	p.mu.Unlock()
	Provided(p.name, module, func(s Set) bool { _, ok := p.Get(s); return ok })
}

// Get is the provider, and true, when this build has it and s has its module on. A switched-off module is absent
// exactly as one built out.
func (p *Port[T]) Get(s Set) (T, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if !p.ok || !s.On(p.module) {
		var zero T
		return zero, false
	}
	return p.v, true
}

// On reports whether the port has a provider and s has its module on: Get without the value.
func (p *Port[T]) On(s Set) bool {
	_, ok := p.Get(s)
	return ok
}

// Module is the id of the module that provided the port, "" when none did.
func (p *Port[T]) Module() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.module
}
