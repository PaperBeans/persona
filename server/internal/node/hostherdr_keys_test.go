package node

import (
	"context"
	"testing"

	"github.com/nwverse/persona/server/internal/herdr"
	"github.com/nwverse/persona/server/internal/host"
)

// herdr 0.9 answers invalid_key for home, end, page up and page down: those go as their xterm bytes through
// pane.send_text, in order among the named keys, which herdr is given by name as before (#4537).
func TestHerdrSendKeysWritesKeysWithoutHerdrNames(t *testing.T) {
	f := newFakeHerdr(t)
	h := &herdrHost{c: &herdr.Client{Socket: f.sock}}
	pane := host.PaneRef{Kind: host.Herdr, ID: "w1:p1"}
	if err := h.SendKeys(context.Background(), pane, "ctrl+x", "ctrl+s"); err != nil {
		t.Fatal(err)
	}
	if err := h.SendKeys(context.Background(), pane, "ctrl+end"); err != nil {
		t.Fatal(err)
	}
	if err := h.SendKeys(context.Background(), pane, "up", "pageup", "shift+home", "enter"); err != nil {
		t.Fatal(err)
	}
	assertTrace(t, f.trace("pane.send_keys", "pane.send_text"), []string{
		call("pane.send_keys", map[string]any{"pane_id": "w1:p1", "keys": []string{"ctrl+x", "ctrl+s"}}),
		call("pane.send_text", map[string]any{"pane_id": "w1:p1", "text": "\x1b[1;5F"}),
		call("pane.send_keys", map[string]any{"pane_id": "w1:p1", "keys": []string{"up"}}),
		call("pane.send_text", map[string]any{"pane_id": "w1:p1", "text": "\x1b[5~"}),
		call("pane.send_text", map[string]any{"pane_id": "w1:p1", "text": "\x1b[1;2H"}),
		call("pane.send_keys", map[string]any{"pane_id": "w1:p1", "keys": []string{"enter"}}),
	})
}
