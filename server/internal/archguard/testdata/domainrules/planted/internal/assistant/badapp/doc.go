// Package badapp is application code that reaches into the cores, the kit and the adapters the way it must not: one
// file per rule. It carries the application counts' habits too.
//
//persona:layer application
package badapp

import "sync"

// locked runs under a lock: the effect that makes this package application.
func locked(mu *sync.Mutex) {
	mu.Lock()
	mu.Unlock()
}
