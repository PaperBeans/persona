package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/nwverse/persona/server/internal/link"
	"github.com/nwverse/persona/server/internal/linktray"
)

// The Windows tray as install, uninstall and status see it (docs/windows.md "Tray"). What it touches on the computer
// (the user's registry, the running tray) is trayControl: systemTray on a real computer, a fake in tests.

// trayControl is what install, uninstall and status do to the tray.
type trayControl struct {
	// setAutostart starts the tray from exe at sign-in, or stops doing so.
	setAutostart func(exe string, on bool) error
	// autostart says whether it starts at sign-in.
	autostart func() bool
	// registerLinks makes pairing links open the tray's pairing window; unregisterLinks takes that back.
	registerLinks   func(exe string) error
	unregisterLinks func()
	// start runs the tray from exe, outliving whoever started it.
	start func(exe string) error
	// quit asks the tray of the data directory dir to go and says whether it went within timeout.
	quit func(dir string, timeout time.Duration) bool
	// running says whether a tray runs for dir.
	running func(dir string) bool
	// removeData deletes what the tray keeps for itself (WebView2's profile).
	removeData func() error
}

// startTray installs the tray for this user (at sign-in, and for pairing links) and starts it now; with on false
// it takes the sign-in entry away. Part of install on Windows.
func startTray(e *env, on bool) {
	exe := e.serviceExePath()
	if !on {
		_ = e.tray.setAutostart(exe, false)
		fmt.Fprintln(e.stdout, "托盘图标：不装（--no-tray）。")
		return
	}
	if err := e.tray.setAutostart(exe, true); err != nil {
		fmt.Fprintf(e.stdout, "托盘图标没设成登录时启动：%v\n", err)
	}
	if err := e.tray.registerLinks(exe); err != nil {
		fmt.Fprintf(e.stdout, "配对链接没交给托盘打开：%v\n", err)
	}
	if err := e.tray.start(exe); err != nil {
		fmt.Fprintf(e.stdout, "托盘图标没启动：%v（下次登录 Windows 时会启动）\n", err)
		return
	}
	fmt.Fprintln(e.stdout, "托盘图标已启动：在任务栏右下角，登录时自动出现；点它看状态、重连、配对。")
}

// stopTray makes the running tray go before its program is replaced or removed.
func stopTray(e *env) {
	if !e.tray.quit(e.personaDir(), 5*time.Second) {
		fmt.Fprintln(e.stdout, "注意：托盘图标没有退出，它的程序文件可能换不掉；从托盘菜单里退出它再试。")
	}
}

// removeTray is uninstall's part: no tray at sign-in, pairing links no longer ours, nothing of it left behind.
func removeTray(e *env) {
	stopTray(e)
	_ = e.tray.setAutostart("", false)
	e.tray.unregisterLinks()
	// WebView2's processes can outlive the tray by a moment, holding its files.
	for i := 0; i < 10 && e.tray.removeData() != nil; i++ {
		e.sleep(200 * time.Millisecond)
	}
}

// pairError marks the pairing failures the tray explains in the user's language: explain's two, which the command
// line says in its own words.
func pairError(err error) error {
	var se *link.SSHError
	if errors.As(err, &se) {
		return se
	}
	switch {
	case rejected(err):
		return &linktray.PairError{Rejected: true, Err: err}
	case unreachable(err):
		return &linktray.PairError{Hosts: hostsOf(err), Err: err}
	}
	return err
}

// trayStatus is status's line about the tray.
func trayStatus(e *env) *trayState {
	return &trayState{Running: e.tray.running(e.personaDir()), Autostart: e.tray.autostart()}
}
