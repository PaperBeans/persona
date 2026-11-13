package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// PaneSize is a pane's rect in pane.layout: the grid its programs see while a herdr client is attached. With none,
// the layout holds herdr's default (120 × 40) and the pane keeps the last client's size: TermSize says that.
func (c *Client) PaneSize(ctx context.Context, pane string) (cols, rows int, err error) {
	var res struct {
		Layout struct {
			Panes []struct {
				PaneID string `json:"pane_id"`
				Rect   struct {
					Width  int `json:"width"`
					Height int `json:"height"`
				} `json:"rect"`
			} `json:"panes"`
		} `json:"layout"`
	}
	if err := c.call(ctx, "pane.layout", map[string]any{"pane_id": pane}, &res, "pane", "layout", "--pane", pane); err != nil {
		return 0, 0, err
	}
	for _, p := range res.Layout.Panes {
		if p.PaneID == pane {
			return p.Rect.Width, p.Rect.Height, nil
		}
	}
	return 0, 0, &Error{Code: "pane_not_found", Message: "pane " + pane + " not in its layout"}
}

// Frame is one frame of `herdr terminal session observe`: herdr redrawing the pane's grid as VT sequences — the whole
// screen when Full, else only the cells that changed — ending with the cursor's place and visibility. Data is the
// base64 of those bytes, as herdr sends it.
type Frame struct {
	Seq    int64  `json:"seq"`
	Full   bool   `json:"full"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Data   string `json:"bytes"`
}

// Observe streams a pane's terminal to fn until ctx ends or herdr stops, and returns why. An observer takes no attach
// slot and never resizes the pane; cols × rows must be the pane's own size (TermSize; PaneSize only while a client is
// attached): a smaller grid is the top left of the screen cut out, not the screen laid out again.
func (c *Client) Observe(ctx context.Context, pane string, cols, rows int, fn func(Frame)) error {
	cmd := exec.CommandContext(ctx, c.Bin, "terminal", "session", "observe", pane, "--cols", fmt.Sprint(cols), "--rows", fmt.Sprint(rows))
	cmd.Env = c.env()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("herdr terminal session observe: %w", err)
	}
	r := bufio.NewReaderSize(stdout, 256<<10)
	frames := 0
	for {
		line, err := readLine(r, maxLine)
		if err != nil {
			break
		}
		var f struct {
			Type string `json:"type"`
			Frame
		}
		if json.Unmarshal(line, &f) != nil || f.Type != "terminal.frame" {
			continue
		}
		frames++
		fn(f.Frame)
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	msg := strings.TrimSpace(stderr.String())
	if frames == 0 && msg != "" {
		return fmt.Errorf("herdr terminal session observe: %s", msg)
	}
	if err != nil {
		return fmt.Errorf("herdr terminal session observe: %v %s", err, msg)
	}
	return fmt.Errorf("herdr terminal session observe: ended")
}
