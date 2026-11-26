package node

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/synctest"
)

func TestPreviewWaitsForInspectorBeforeDispatchingMouse(t *testing.T) {
	for _, tc := range []struct{ name, response string }{
		{"ready", `"result":{}`}, {"rejected", `"error":{"code":-32000,"message":"inspector unavailable"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				in, browser := io.Pipe()
				defer browser.Close()
				wire, panel := &nopWriteCloser{}, &nopWriteCloser{}
				cdp := newCDPConn(in, wire, nil)
				tab := &previewTab{b: &previewBrowser{cdp: cdp}, conn: panel}
				mouse := func() *previewIn {
					return &previewIn{T: "mouse", Type: "down", mouseSequence: tab.mouseSequence.Add(1)}
				}
				setModeAndClick := func(on bool) {
					pressed := mouse()
					go func() {
						tab.fromWeb(&previewIn{T: "pick", On: on})
						tab.fromWeb(pressed)
					}()
					synctest.Wait()
					if !strings.Contains(wire.String(), "Overlay.setInspectMode") || strings.Contains(wire.String(), "Input.dispatchMouseEvent") {
						t.Fatalf("before Chrome confirmed the transition: %s", wire)
					}
				}
				reply := func(response string) {
					if _, err := fmt.Fprintf(browser, "{\"id\":%d,%s}\x00", cdp.next.Load(), response); err != nil {
						t.Fatal(err)
					}
					synctest.Wait()
				}
				setModeAndClick(true)
				reply(tc.response)
				sent := strings.Contains(wire.String(), "Input.dispatchMouseEvent")
				if tc.name == "rejected" {
					if tab.picking || sent || !strings.Contains(panel.String(), "pick_failed") {
						t.Fatal("a rejected pick must report the error and discard its queued click")
					}
					tab.fromWeb(mouse())
					if !strings.Contains(wire.String(), "Input.dispatchMouseEvent") {
						t.Fatal("the next ordinary click should reach the page")
					}
					return
				}
				if !tab.picking || !sent {
					t.Fatal("the inspector should receive the waiting click after activation")
				}
				wire.Reset()
				setModeAndClick(false)
				reply(`"result":{}`)
				if tab.picking || !strings.Contains(wire.String(), "Input.dispatchMouseEvent") {
					t.Fatal("a click following cancellation should reach the page after the inspector leaves")
				}
			})
		})
	}
}
