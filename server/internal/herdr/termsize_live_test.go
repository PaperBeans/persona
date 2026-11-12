//go:build herdrlive && !windows

package herdr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Against a throwaway herdr session with no client attached (never the one you work in):
//
//	herdr --session persona-home-test      # start it, then quit the client (the server keeps running)
//	PERSONA_LIVE_HOME_SESSION=persona-home-test go test -tags herdrlive -run TestLiveControlGivesTheSizeBack ./internal/herdr/
//
// It reads the pane's size, controls it at 57 × 41 and lets go twice: with Close (herdr, no client attached, leaves the
// pane at 57 × 41: #5339) and with CloseAt the size from before (the pane gets it back), resize and release back to back.
func TestLiveControlGivesTheSizeBack(t *testing.T) {
	c := liveHomeSession(t)
	ctx := context.Background()
	snap, err := c.Snapshot(ctx)
	if err != nil || len(snap.Panes) == 0 {
		t.Fatalf("snapshot: %v", err)
	}
	pane := snap.Panes[0].PaneID
	cols, rows, err := c.TermSize(ctx, pane)
	if err != nil {
		t.Fatal(err)
	}
	if cols == 57 && rows == 41 {
		t.Fatalf("%s is at 57×41 already (left there by an earlier run?): resize it first", pane)
	}
	lw, lh, _ := c.PaneSize(ctx, pane)
	t.Logf("%s: terminal %d×%d, layout %d×%d", pane, cols, rows, lw, lh)
	control := func() *Control {
		s, err := c.StartControl(ctx, pane, 57, 41, false, func(Frame) {})
		if err != nil {
			t.Fatal(err)
		}
		if w, h, _ := c.TermSize(ctx, pane); w != 57 || h != 41 {
			t.Fatalf("while controlled: %d×%d", w, h)
		}
		return s
	}
	ended := func(s *Control) (int, int) {
		select {
		case <-s.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("the control did not end")
		}
		var w, h int
		deadline := time.Now().Add(5 * time.Second)
		for {
			w, h, err = c.TermSize(ctx, pane)
			if err != nil {
				t.Fatal(err)
			}
			if w == cols && h == rows || time.Now().After(deadline) {
				return w, h
			}
			<-time.After(20 * time.Millisecond)
		}
	}

	s := control()
	s.Close()
	if w, h := ended(s); w == cols && h == rows {
		t.Logf("Close gave %d×%d back: a herdr client is attached (or herdr now does it itself)", w, h)
	} else {
		t.Logf("Close left %d×%d (no client attached)", w, h)
	}
	s = control()
	s.CloseAt(cols, rows)
	if w, h := ended(s); w != cols || h != rows {
		t.Fatalf("after CloseAt(%d, %d): %d×%d", cols, rows, w, h)
	}
}

// #5409, same session (attach a wide client once, quit it: the pane keeps that size, the layout says 120×40):
//
//	PERSONA_LIVE_HOME_SESSION=persona-home-test go test -tags herdrlive -run TestLiveObserveAtTheTerminalsSize ./internal/herdr/
//
// Markers end in the pane's last column and sit on its last row; observing at TermSize shows both.
func TestLiveObserveAtTheTerminalsSize(t *testing.T) {
	c := liveHomeSession(t)
	ctx := context.Background()
	snap, err := c.Snapshot(ctx)
	if err != nil || len(snap.Panes) == 0 {
		t.Fatalf("snapshot: %v", err)
	}
	pane := snap.Panes[0].PaneID
	cols, rows, err := c.TermSize(ctx, pane)
	if err != nil {
		t.Fatal(err)
	}
	lw, lh, _ := c.PaneSize(ctx, pane)
	t.Logf("%s: terminal %d×%d, layout %d×%d", pane, cols, rows, lw, lh)
	edge := strings.Repeat("R", cols-6) + "EDGE!|"
	cmd := fmt.Sprintf("clear; printf '%%s' '%s'; for i in $(seq 2 %d); do echo; done; printf BOTTOMROW", edge, rows-1)
	if err := c.PaneSendText(ctx, pane, cmd); err != nil {
		t.Fatal(err)
	}
	if err := c.PaneSendKeys(ctx, pane, "Enter"); err != nil {
		t.Fatal(err)
	}
	var text string
	for deadline := time.Now().Add(10 * time.Second); ; {
		rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		var err error
		text, err = c.PaneRead(rctx, pane, "visible", rows)
		cancel()
		if err == nil && strings.Contains(text, "EDGE!|") && strings.Contains(text, "BOTTOMROW") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the marker command did not draw the terminal: %q, %v", text, err)
		}
		<-time.After(20 * time.Millisecond)
	}
	seen := func(w, h int) string {
		octx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		var got string
		_ = c.Observe(octx, pane, w, h, func(f Frame) {
			if f.Full && got == "" {
				b, _ := base64.StdEncoding.DecodeString(f.Data)
				got = string(b)
				cancel()
			}
		})
		return got
	}
	if got := seen(cols, rows); !strings.Contains(got, "EDGE!|") || !strings.Contains(got, "BOTTOMROW") {
		t.Fatalf("observed at the terminal's %d×%d: edge %v, bottom row %v", cols, rows, strings.Contains(got, "EDGE!|"), strings.Contains(got, "BOTTOMROW"))
	}
	if lw < cols || lh < rows {
		got := seen(lw, lh)
		t.Logf("at the layout's %d×%d: edge %v, bottom row %v (cut: the size the node used to observe at)", lw, lh,
			strings.Contains(got, "EDGE!|"), strings.Contains(got, "BOTTOMROW"))
	}
}

// liveHomeSession is a client of the throwaway session named by PERSONA_LIVE_HOME_SESSION (never the default one).
func liveHomeSession(t *testing.T) *Client {
	t.Helper()
	name := os.Getenv("PERSONA_LIVE_HOME_SESSION")
	if name == "" || name == "default" {
		t.Skip("set PERSONA_LIVE_HOME_SESSION to a throwaway session's name (not the default one)")
	}
	out, err := exec.Command("herdr", "session", "list", "--json").Output()
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Sessions []struct {
			Name       string `json:"name"`
			SocketPath string `json:"socket_path"`
		} `json:"sessions"`
	}
	_ = json.Unmarshal(out, &list)
	var socket string
	for _, s := range list.Sessions {
		if s.Name == name {
			socket = s.SocketPath
		}
	}
	if socket == "" {
		t.Fatalf("no session %q", name)
	}
	// Session set: the CLI gets this session's socket, never the one in the environment.
	return &Client{Bin: "herdr", Socket: socket, Session: name}
}
