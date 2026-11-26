//go:build !windows

package main

import (
	"errors"
	"time"
)

// The tray is Windows' (tray_windows.go); the Mac has its menu bar app (apple/PersonaLink), Linux servers have no taskbar.

func trayCmd(*env, []string) error {
	return errors.New(terminalText("link-cli.tray_windows_only"))
}

var errNoTray = errors.New("no tray here")

// systemTray: nothing to touch; install and uninstall only reach it on Windows.
func systemTray() trayControl {
	return trayControl{
		setAutostart:    func(string, bool) error { return errNoTray },
		autostart:       func() bool { return false },
		registerLinks:   func(string) error { return errNoTray },
		unregisterLinks: func() {},
		start:           func(string) error { return errNoTray },
		quit:            func(string, time.Duration) bool { return true },
		running:         func(string) bool { return false },
		removeData:      func() error { return nil },
	}
}
