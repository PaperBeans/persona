package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// A subscriber sees the provider's start while the call is still outstanding.
// A second socket joining it receives the current snapshot without another node event.
func TestLiveToolsBeforeCompletionOverWebSocket(t *testing.T) {
	h := newTestHub(t)
	node, m := newWatchNode(t)
	m.hub = h
	m.online = true
	registerMachine(h, m)
	srv := httptest.NewServer(h.routes())
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	subscribe := func() *websocket.Conn {
		t.Helper()
		url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/ws?features=session_live_tools"
		ws, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + h.cfg.Token}}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ws.CloseNow() })
		body, _ := json.Marshal(map[string]string{"type": "subscribe", "machine_id": m.cfg.ID, "session_id": "work"})
		if err := ws.Write(ctx, websocket.MessageText, body); err != nil {
			t.Fatal(err)
		}
		return ws
	}
	expect := func(ws *websocket.Conn, count int) {
		t.Helper()
		for {
			_, data, err := ws.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var frame liveToolsPush
			if err := json.Unmarshal(data, &frame); err != nil {
				t.Fatal(err)
			}
			if frame.Type != "live_tools" {
				continue
			}
			if frame.MachineID != m.cfg.ID || frame.SessionID != "work" || frame.Tools == nil || len(frame.Tools) != count || count == 1 && (frame.Tools[0].ID != "call" || frame.Tools[0].Name != "Bash") {
				t.Fatalf("wrong live tools frame: %s", data)
			}
			return
		}
	}
	first := subscribe()
	eventually(t, 3*time.Second, "watch active at node", func() bool { return node.isWatched("work") })
	m.onEvent("session.live_tools", json.RawMessage(`{"session_id":"work","tools":[{"id":"call","name":"Bash"}],"active":true}`))
	expect(first, 1)
	second := subscribe()
	expect(second, 1)
	m.onEvent("session.live_tools", json.RawMessage(`{"session_id":"work","tools":[]}`))
	expect(first, 0)
	expect(second, 0)
}
