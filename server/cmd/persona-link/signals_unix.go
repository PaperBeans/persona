//go:build unix

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/nwverse/persona/server/internal/link"
	"github.com/nwverse/persona/server/internal/osproc"
)

// notifySignals: SIGUSR1 (the network changed) calls check, SIGUSR2 (reconnect now) calls redial. The menu bar app
// sends them to its helper.
func notifySignals(_ context.Context, _ string, check, redial func()) {
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGUSR1, syscall.SIGUSR2)
	go func() {
		for s := range sigs {
			if s == syscall.SIGUSR2 {
				redial()
			} else {
				check()
			}
		}
	}()
}

// pokeRunning asks a running `persona-link run` (holding the run lock at path) to reconnect now.
func pokeRunning(path string) {
	if !link.Running(path) {
		return
	}
	if st, err := link.ReadStatus(path); err == nil && st.PID > 0 && st.PID != os.Getpid() && osproc.AliveMine(st.PID) {
		_ = syscall.Kill(st.PID, syscall.SIGUSR2)
	}
}
