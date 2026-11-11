//go:build windows

package main

import (
	"context"
	"log"

	"github.com/nwverse/persona/server/internal/link"
)

// notifySignals: Windows has no SIGUSR1 / SIGUSR2, so the tray sets named events instead (link.Poke): recheck calls
// check, reconnect calls redial.
func notifySignals(ctx context.Context, path string, check, redial func()) {
	if err := link.WatchPokes(ctx, path, check, redial); err != nil {
		log.Printf("pokes: %v", err) // the link still works; only the tray's "Reconnect now" does not
	}
}

func pokeRunning(path string) {
	_ = link.Poke(path, link.PokeRedial)
}
