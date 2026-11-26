package hubcli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/update"
)

// startRecordScript returns a script whose command line is published only after the complete write. A direct
// redirection creates an empty file first, which the polling test can otherwise mistake for the final record.
func startRecordScript(command, started string) string {
	return fmt.Sprintf("#!/bin/sh\ntmp=%q\ntrap 'rm -f \"$tmp\"' EXIT\necho %q > \"$tmp\" && mv \"$tmp\" %q\n", started+".tmp", command, started)
}

// Without a service manager the watchdog stops the hub that would not come up and starts the restored binary itself,
// with the arguments the hub had (real processes: a sleeping stand-in for the hub, a script for the old binary).
func TestRestartHubWithoutAServiceManager(t *testing.T) {
	t.Setenv("INVOCATION_ID", "")
	t.Setenv("XPC_SERVICE_NAME", "")
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	started := filepath.Join(dir, "started")
	target := filepath.Join(dir, "bin", "personad")
	script := startRecordScript("$@", started)
	if err := os.WriteFile(target, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	hub := exec.Command("sleep", "60")
	if err := hub.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = hub.Wait(); close(exited) }()
	standInHub(t, hub.Process.Pid, exited)
	if err := restartHub(update.Pending{PID: hub.Process.Pid, Target: target, Args: []string{"/old/path/personad", "hub", "run"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(15 * time.Second):
		hub.Process.Signal(syscall.SIGKILL)
		t.Fatal("the hub that would not come up was not stopped")
	}
	// Only a guard against a hang: on macOS the first run of a freshly written script waits for the system's checks,
	// up to 10 s while go test ./... links its test binaries (#2307).
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if b, err := os.ReadFile(started); err == nil {
			if string(b) != "hub run\n" {
				t.Fatalf("started with %q", b)
			}
			return
		}
	}
	t.Fatal("the restored binary was not started")
}

// A hub that ran as persona-hub is started again as persona-hub (Pending.Run), with its own arguments.
func TestRestartHubAsPersonaHub(t *testing.T) {
	t.Setenv("INVOCATION_ID", "")
	t.Setenv("XPC_SERVICE_NAME", "")
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	run := filepath.Join(dir, "persona-hub")
	if err := os.WriteFile(run, []byte(startRecordScript("$0 $1", started)), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "personad")
	if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := restartHub(update.Pending{Target: target, Run: run, Args: []string{run, "run"}}); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if b, err := os.ReadFile(started); err == nil {
			if string(b) != run+" run\n" {
				t.Fatalf("started %q", b)
			}
			return
		}
	}
	t.Fatal("persona-hub was not started")
}

// standInHub makes pid this process's parent, as the hub is the watchdog's, until gone is closed.
func standInHub(t *testing.T, pid int, gone <-chan struct{}) {
	old := hubIsParent
	hubIsParent = func(p int) bool {
		select {
		case <-gone:
			return false
		default:
			return p == pid
		}
	}
	t.Cleanup(func() { hubIsParent = old })
}

// recordSignals records what stopHub sends, and still sends it.
func recordSignals(t *testing.T) func() []os.Signal {
	var mu sync.Mutex
	var sent []os.Signal
	old := signalHub
	signalHub = func(p *os.Process, sig os.Signal) error {
		mu.Lock()
		sent = append(sent, sig)
		mu.Unlock()
		return old(p, sig)
	}
	t.Cleanup(func() { signalHub = old })
	return func() []os.Signal {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(sent)
	}
}

// startProcess starts a process that ignores SIGTERM (ready once it does) and reaps it when it exits.
func startProcess(t *testing.T) (*exec.Cmd, <-chan struct{}) {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command("sh", "-c", `trap "" TERM; : > "$1"; exec sleep 60`, "sh", ready)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-exited })
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(ready); err == nil {
			return cmd, exited
		}
		if time.Now().After(deadline) {
			t.Fatal("the stand-in process did not start")
		}
	}
}

func alive(exited <-chan struct{}) bool {
	select {
	case <-exited:
		return false
	case <-time.After(100 * time.Millisecond):
		return true
	}
}

// A hub that ignores SIGTERM is killed once the grace period is over, and is gone when stopHub returns: the hub
// started next must not find the old one still holding its port.
func TestStopHubKillsAHubThatStays(t *testing.T) {
	sent := recordSignals(t)
	cmd, exited := startProcess(t)
	standInHub(t, cmd.Process.Pid, exited)
	stopHub(cmd.Process.Pid, 500*time.Millisecond, 10*time.Millisecond)
	select {
	case <-exited:
	default:
		t.Fatal("stopHub returned while the killed hub was still there")
	}
	if got := sent(); !slices.Equal(got, []os.Signal{syscall.SIGTERM, os.Kill}) {
		t.Fatalf("sent %v, want SIGTERM then SIGKILL", got)
	}
}

// The hub exits on SIGTERM and another process takes its pid before the grace period is over: the watchdog is no
// longer the hub's child, and the process now holding the pid is left alone.
func TestStopHubLeavesAProcessThatTookThePid(t *testing.T) {
	sent := recordSignals(t)
	other, exited := startProcess(t) // stands in for the process that took the pid; ignores SIGTERM
	termed := make(chan struct{})
	standInHub(t, other.Process.Pid, termed)
	old := signalHub
	signalHub = func(p *os.Process, sig os.Signal) error {
		err := old(p, sig)
		if sig == syscall.SIGTERM {
			close(termed) // the hub went: this process has another parent now
		}
		return err
	}
	stopHub(other.Process.Pid, 200*time.Millisecond, 10*time.Millisecond)
	if got := sent(); !slices.Equal(got, []os.Signal{syscall.SIGTERM}) {
		t.Fatalf("sent %v, want SIGTERM only", got)
	}
	if !alive(exited) {
		t.Fatal("the process that took the hub's pid was killed")
	}
}

// The hub had already exited when the watchdog gave up on it: nothing is signalled, whoever has its pid now.
func TestStopHubSignalsNothingOnceTheHubIsGone(t *testing.T) {
	sent := recordSignals(t)
	other, exited := startProcess(t)
	gone := make(chan struct{})
	close(gone)
	standInHub(t, other.Process.Pid, gone)
	stopHub(other.Process.Pid, 200*time.Millisecond, 10*time.Millisecond)
	if got := sent(); len(got) != 0 {
		t.Fatalf("sent %v to a process that is not the hub", got)
	}
	if !alive(exited) {
		t.Fatal("the process that took the hub's pid was killed")
	}
}
