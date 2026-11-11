//go:build !unix && !windows

package main

import "context"

// notifySignals: there is no SIGUSR1 / SIGUSR2 here; persona-link still notices a changed network by itself
// (heartbeats, netaddr.Watch's poll).
func notifySignals(context.Context, string, func(), func()) {}

func pokeRunning(string) {}
