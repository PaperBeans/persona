//go:build !windows

package herdr

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

// The size a pane's programs see comes from its terminal: a process on a terminal of 189 × 52 (the pane's shell, as
// pane.process_info names it) while the layout would say herdr's headless default.
func TestTermSizeReadsThePanesTerminal(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 189, Rows: 52})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait(); _ = f.Close() })
	sock := fakeServer(t, func(method string, params map[string]any) string {
		if method == "pane.process_info" && params["pane_id"] == "w1:p1" {
			return fmt.Sprintf(`"result":{"type":"pane_process_info","process_info":{"pane_id":"w1:p1","shell_pid":%d,"foreground_processes":[]}}`, cmd.Process.Pid)
		}
		if method == "pane.process_info" && params["pane_id"] == "w1:p2" {
			return `"result":{"type":"pane_process_info","process_info":{"pane_id":"w1:p2","shell_pid":0,"foreground_processes":[]}}`
		}
		return `"error":{"code":"pane_not_found","message":"pane not found"}`
	})
	c := &Client{Bin: "/nonexistent/herdr", Socket: sock}
	ctx := context.Background()
	if cols, rows, err := c.TermSize(ctx, "w1:p1"); err != nil || cols != 189 || rows != 52 {
		t.Fatalf("TermSize = %d×%d, %v", cols, rows, err)
	}
	if err := pty.Setsize(f, &pty.Winsize{Cols: 57, Rows: 41}); err != nil {
		t.Fatal(err)
	}
	if cols, rows, err := c.TermSize(ctx, "w1:p1"); err != nil || cols != 57 || rows != 41 {
		t.Fatalf("after a resize: TermSize = %d×%d, %v", cols, rows, err)
	}
	if _, _, err := c.TermSize(ctx, "w1:p2"); err == nil {
		t.Fatal("a pane with no process has a size")
	}
	if _, _, err := c.TermSize(ctx, "w9:p9"); !IsNotFound(err) {
		t.Fatalf("a pane herdr does not know: %v", err)
	}
}

// A process with no terminal (started in a session of its own, stdin not a tty) has no size; nor has one that is gone.
// On macOS the device is looked up by number each call (no ps): a second read is the same.
func TestTTYSizeWithoutATerminal(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	if _, _, err := ttySize(context.Background(), pid); err == nil {
		t.Fatal("a process with no terminal has a size")
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if _, _, err := ttySize(context.Background(), pid); err == nil {
		t.Fatal("a process that is gone has a size")
	}

	shell := exec.Command("sleep", "30")
	f, err := pty.StartWithSize(shell, &pty.Winsize{Cols: 100, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shell.Process.Kill(); _ = shell.Wait(); _ = f.Close() })
	for range 2 {
		if cols, rows, err := ttySize(context.Background(), shell.Process.Pid); err != nil || cols != 100 || rows != 30 {
			t.Fatalf("ttySize = %d×%d, %v", cols, rows, err)
		}
	}
}

// fakeControl is herdr's terminal session control: a whole frame, then each stdin line to log, then "released" when
// stdin closes.
func fakeControl(t *testing.T) (*Client, string) {
	t.Helper()
	dir := t.TempDir()
	bin, log := filepath.Join(dir, "herdr"), filepath.Join(dir, "log")
	script := fmt.Sprintf(`#!/bin/sh
echo '{"type":"terminal.frame","seq":1,"full":true,"width":57,"height":41,"encoding":"ansi","bytes":"RlVMTA=="}'
while read -r line; do printf '%%s\n' "$line" >> %[1]q; done
echo released >> %[1]q
`, log)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Client{Bin: bin}, log
}

func closedLog(t *testing.T, s *Control, log string) string {
	t.Helper()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the control did not end")
	}
	b, _ := os.ReadFile(log)
	return string(b)
}

// CloseAt resizes the pane back before stdin closes (herdr with no client attached keeps a released pane's last size);
// at the size the pane already has it is a plain release.
func TestControlCloseAtResizesThenReleases(t *testing.T) {
	c, log := fakeControl(t)
	s, err := c.StartControl(context.Background(), "w1:p1", 57, 41, false, func(Frame) {})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Input("a")
	s.CloseAt(189, 52)
	got := closedLog(t, s, log)
	want := `{"text":"a","type":"terminal.input"}` + "\n" + `{"cols":189,"rows":52,"type":"terminal.resize"}` + "\nreleased\n"
	if got != want {
		t.Fatalf("herdr got %q, want %q", got, want)
	}

	c, log = fakeControl(t)
	if s, err = c.StartControl(context.Background(), "w1:p1", 57, 41, false, func(Frame) {}); err != nil {
		t.Fatal(err)
	}
	s.CloseAt(57, 41)
	if got := closedLog(t, s, log); strings.Contains(got, "terminal.resize") || !strings.Contains(got, "released") {
		t.Fatalf("at its own size: herdr got %q", got)
	}

	c, log = fakeControl(t)
	if s, err = c.StartControl(context.Background(), "w1:p1", 57, 41, false, func(Frame) {}); err != nil {
		t.Fatal(err)
	}
	s.CloseAt(0, 0)
	if got := closedLog(t, s, log); got != "released\n" {
		t.Fatalf("no size: herdr got %q", got)
	}
}
