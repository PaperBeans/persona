//go:build !windows

package node

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// fakeHomeHerdr is fakeControlHerdr with the pane's shell on a real terminal (pid; 0: pane process-info fails) and a
// layout of 120 × 40 (herdr's headless default): the node gives back the terminal's size, else the layout's. A control
// at 33 columns fails to start.
func fakeHomeHerdr(t *testing.T, pid int) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	bin, log = filepath.Join(dir, "herdr"), filepath.Join(dir, "log")
	script := fmt.Sprintf(`#!/bin/sh
case "$1 $2" in
"pane process-info")
  [ %[2]d -gt 0 ] || exit 1
  echo '{"result":{"type":"pane_process_info","process_info":{"pane_id":"w1:p1","shell_pid":%[2]d,"foreground_processes":[]}}}' ;;
"pane layout") echo '{"result":{"layout":{"panes":[{"pane_id":"w1:p1","rect":{"width":120,"height":40}}]}}}' ;;
"terminal session")
  [ "$4" = "--help" ] && exit 0
  case "$*" in *"--cols 33 "*) echo "herdr: cannot attach" >&2; exit 1 ;; esac
  echo "start $*" >> %[1]q
  echo '{"type":"terminal.frame","seq":1,"full":true,"width":44,"height":30,"encoding":"ansi","bytes":"RlVMTA=="}'
  while read -r line; do
    printf "%%s\n" "$line" >> %[1]q
    case "$line" in
    *terminal.resize*) echo '{"type":"terminal.frame","seq":2,"full":true,"width":40,"height":20,"encoding":"ansi","bytes":"RlVMTA=="}' ;;
    esac
  done
  echo released >> %[1]q
  echo '{"type":"terminal.closed","reason":"detached"}' ;;
*) exit 1 ;;
esac
`, log, pid)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

// paneShell is a process on a terminal of cols × rows, as herdr's pane shell is.
func paneShell(t *testing.T, cols, rows uint16) (*exec.Cmd, *os.File) {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait(); _ = f.Close() })
	return cmd, f
}

// lines is the log after its line starting with from (the whole log when from is "").
func logAfter(log, from string) []string {
	var out []string
	on := from == ""
	for _, l := range strings.Split(strings.TrimSpace(log), "\n") {
		if on {
			out = append(out, l)
		} else if strings.HasPrefix(l, from) {
			on = true
		}
	}
	return out
}

const homeResize = `{"cols":189,"rows":52,"type":"terminal.resize"}`

// #5339: with no herdr client attached herdr leaves a released pane at the controller's size, so the node resizes it
// back to what its programs saw before the phone took it, and only then lets go. The frame that resize brings is not
// sent: the phone is gone.
func TestTerminalControlGivesThePaneItsSizeBack(t *testing.T) {
	shell, _ := paneShell(t, 189, 52)
	bin, log := fakeHomeHerdr(t, shell.Process.Pid)
	n, hub, events := controlNode(t, bin)
	if _, err := n.terminalWatch(context.Background(), "s1", &termControl{Cols: 44, Rows: 30}); err != nil {
		t.Fatal(err)
	}
	_ = hub.Notify("terminal.resize", map[string]any{"session_id": "s1", "cols": 40, "rows": 20}) // the phone rotates
	waitFor(t, "the phone's resize", func() bool { return strings.Contains(readLog(log), `"cols":40`) })
	waitFor(t, "its frame", func() bool { return countFrames(events()) == 2 })
	stream := n.termStreamOf("s1")
	if stream == nil {
		t.Fatal("the control is not streaming")
	}
	n.terminalUnwatch("s1")
	waitFor(t, "the release", func() bool { return strings.Contains(readLog(log), "released") })
	got := logAfter(readLog(log), "start ")
	if len(got) < 3 || got[len(got)-2] != homeResize || got[len(got)-1] != "released" {
		t.Fatalf("herdr got %q, want the resize to 189×52 right before the release", got)
	}
	if !stream.isLeaving() {
		t.Fatal("the control did not begin leaving")
	}
	waitFor(t, "the last frame's timer", func() bool {
		stream.mu.Lock()
		defer stream.mu.Unlock()
		return time.Since(stream.lease) >= 30*time.Millisecond
	})
	if k := countFrames(events()); k != 2 {
		t.Fatalf("%d frames went to the hub, want 2 (none after letting go)", k)
	}
}

// A control that runs out of lease gives the size back too; one with no terminal size to read gives back the layout's.
func TestTerminalControlGivesTheSizeBackWhenForgotten(t *testing.T) {
	old := termLease
	termLease = 100 * time.Millisecond
	t.Cleanup(func() { termLease = old })
	shell, _ := paneShell(t, 189, 52)
	bin, log := fakeHomeHerdr(t, shell.Process.Pid)
	n, _, events := controlNode(t, bin)
	if _, err := n.terminalWatch(context.Background(), "s1", &termControl{Cols: 44, Rows: 30}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the lease to run out", func() bool {
		for _, e := range events() {
			if e.Event == "terminal.end" && e.Control {
				return true
			}
		}
		return false
	})
	waitFor(t, "the release", func() bool { return strings.Contains(readLog(log), "released") })
	if got := logAfter(readLog(log), "start "); len(got) != 2 || got[0] != homeResize {
		t.Fatalf("herdr got %q", got)
	}

	termLease = old
	bin, log = fakeHomeHerdr(t, 0)
	n, _, _ = controlNode(t, bin)
	if _, err := n.terminalWatch(context.Background(), "s1", &termControl{Cols: 44, Rows: 30}); err != nil {
		t.Fatal(err)
	}
	n.terminalUnwatch("s1")
	waitFor(t, "the release", func() bool { return strings.Contains(readLog(log), "released") })
	if got := logAfter(readLog(log), "start "); len(got) != 2 || got[0] != `{"cols":120,"rows":40,"type":"terminal.resize"}` {
		t.Fatalf("no terminal size: herdr got %q, want the layout's size back", got)
	}
}

// A control handed straight to the next (the phone subscribing again, another phone taking over) gives nothing back in
// between — no redraw at the computer's width between two phone sizes — and the size from before the first is the one
// given back at the end, not the first phone's that the pane has meanwhile.
func TestTerminalControlHandedOverKeepsTheFirstSize(t *testing.T) {
	shell, tty := paneShell(t, 189, 52)
	bin, log := fakeHomeHerdr(t, shell.Process.Pid)
	n, _, _ := controlNode(t, bin)
	if _, err := n.terminalWatch(context.Background(), "s1", &termControl{Cols: 44, Rows: 30}); err != nil {
		t.Fatal(err)
	}
	if err := pty.Setsize(tty, &pty.Winsize{Cols: 44, Rows: 30}); err != nil { // what herdr does to the pane
		t.Fatal(err)
	}
	if _, err := n.terminalWatch(context.Background(), "s1", &termControl{Cols: 40, Rows: 20, Takeover: true}); err != nil {
		t.Fatal(err)
	}
	first := logAfter(readLog(log), "")
	if len(first) < 3 || !strings.HasPrefix(first[0], "start ") || first[1] != "released" ||
		!strings.Contains(first[2], "--cols 40 --rows 20") {
		t.Fatalf("handing over: herdr got %q, want the first released with no resize, then the second started", first)
	}
	n.terminalUnwatch("s1")
	waitFor(t, "the second release", func() bool { return strings.Count(readLog(log), "released") == 2 })
	if got := logAfter(readLog(log), "start terminal session control w1:p1 --cols 40"); len(got) != 2 || got[0] != homeResize {
		t.Fatalf("the second control: herdr got %q, want the size from before the first back", got)
	}
}

// A handover whose next control does not start: the first let go at the phone's size for it, so the node puts the
// pane back to the size from before with a control at that size, let go at once.
func TestTerminalControlHandOverThatFailsGivesTheSizeBack(t *testing.T) {
	shell, _ := paneShell(t, 189, 52)
	bin, log := fakeHomeHerdr(t, shell.Process.Pid)
	n, _, _ := controlNode(t, bin)
	if _, err := n.terminalWatch(context.Background(), "s1", &termControl{Cols: 44, Rows: 30}); err != nil {
		t.Fatal(err)
	}
	if _, err := n.terminalWatch(context.Background(), "s1", &termControl{Cols: 33, Rows: 20}); err == nil {
		t.Fatal("the fake's control at 33 columns started")
	}
	waitFor(t, "the size given back", func() bool { return strings.Count(readLog(log), "released") == 2 })
	got := logAfter(readLog(log), "")
	if len(got) != 4 || got[1] != "released" || got[2] != "start terminal session control w1:p1 --cols 189 --rows 52" || got[3] != "released" {
		t.Fatalf("herdr got %q, want the first released, then a control at 189×52 let go at once", got)
	}
}

// #5409: a pane is mirrored at its terminal's size, which pane.layout (120 × 40 here, herdr's default while no client
// is attached) is not; when the terminal changes size the observe starts over at the new one.
func TestTerminalWatchObservesAtTheTerminalsSize(t *testing.T) {
	shell, tty := paneShell(t, 189, 52)
	bin, log := fakeHomeHerdr(t, shell.Process.Pid)
	n, _, _ := controlNode(t, bin)
	t.Cleanup(func() { n.terminalUnwatch("s1") })
	res, err := n.terminalWatch(context.Background(), "s1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res["cols"] != 189 || res["rows"] != 52 || res["control"] != false {
		t.Fatalf("watch = %v", res)
	}
	waitFor(t, "the observe", func() bool { return strings.Contains(readLog(log), "observe w1:p1 --cols 189 --rows 52") })
	if err := pty.Setsize(tty, &pty.Winsize{Cols: 150, Rows: 45}); err != nil { // a herdr client attached at its size
		t.Fatal(err)
	}
	waitFor(t, "the observe at the new size", func() bool { return strings.Contains(readLog(log), "observe w1:p1 --cols 150 --rows 45") })
	n.terminalUnwatch("s1")
	if strings.Contains(readLog(log), "--cols 120") {
		t.Fatalf("observed at the layout's size: %q", readLog(log))
	}
}

func countFrames(events []termEvent) int {
	k := 0
	for _, e := range events {
		if e.Event == "terminal.frame" {
			k++
		}
	}
	return k
}
