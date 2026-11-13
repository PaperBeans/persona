package herdr

import (
	"context"
	"fmt"
)

// TermSize is the size a pane's programs see now: the window size of its terminal, read from the tty of the pane's
// process. PaneSize (its rect in pane.layout) is that only while a herdr client is attached: with none, the layout
// holds herdr's own default (120 × 40) while each pane keeps the size the last client gave it — which is the size to
// give back after a phone drove the pane (docs/integrations/terminal.md section 11).
func (c *Client) TermSize(ctx context.Context, pane string) (cols, rows int, err error) {
	var out struct {
		ProcessInfo struct {
			ShellPID int `json:"shell_pid"`
		} `json:"process_info"`
	}
	// pane_id, never target: given a target herdr answers for the focused pane.
	if err := c.call(ctx, "pane.process_info", map[string]any{"pane_id": pane}, &out, "pane", "process-info", "--pane", pane); err != nil {
		return 0, 0, err
	}
	if out.ProcessInfo.ShellPID <= 0 {
		return 0, 0, fmt.Errorf("herdr: pane %s has no process", pane)
	}
	return ttySize(ctx, out.ProcessInfo.ShellPID)
}
