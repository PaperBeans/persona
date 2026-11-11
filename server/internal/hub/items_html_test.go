package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/transcript"
)

// htmlToolInput is an MCP call's input of 200 HTML snippets: 61,011 bytes as Claude wrote it, 160,011 when every hop
// wrote < > & as six bytes each.
func htmlToolInput() string {
	rows := make([]string, 200)
	for i := range rows {
		rows[i] = `{"text":"` + strings.Repeat("<b>&amp;</b> ", 22) + `<p></p>"}`
	}
	return `{"items":[` + strings.Join(rows, ",") + `]}`
}

// A tool input of HTML reaches the apps no longer than it was written, and as the same value: no hop on the way (the
// transcript's clip, the node's rpc reply and push, the hub's page, full item, push and subscribe backlog) writes
// < > & as six bytes each (#2907). It went out at 160,011 bytes for these 61,011.
func TestItemsKeepHTMLCharactersOnTheWire(t *testing.T) {
	in := htmlToolInput()
	line := `{"type":"assistant","uuid":"a1","timestamp":"2026-11-10T12:00:00Z","message":{"id":"m","role":"assistant",` +
		`"content":[{"type":"tool_use","id":"t1","name":"mcp__x__y","input":` + in + `}]}}`
	items := transcript.ParseLine(1, []byte(line), &transcript.Meta{})
	if len(items) != 1 {
		t.Fatalf("ParseLine gave %d items, want 1", len(items))
	}
	checkToolInput(t, "ParseLine", items[0].ToolInput, in)

	c := &client{out: make(chan []byte, 1), subs: map[[2]string]bool{{"m1", "s1"}: true}}
	c.canonicalContracts.Store(true)
	h := &Hub{clients: map[*client]struct{}{c: {}}}
	m, node := itemsWire(t, h, func(method string) any {
		if method == "session.item" {
			return items[0]
		}
		return map[string]any{"items": items, "has_more": false}
	})

	req := httptest.NewRequest("GET", "/api/v1/sessions/m1/s1/items", nil)
	req.SetPathValue("sid", "s1")
	w := httptest.NewRecorder()
	h.sessionItems(w, req, m)
	checkToolInput(t, "page", firstToolInput(t, "page", w.Body.Bytes()), in)

	req = httptest.NewRequest("GET", "/api/v1/sessions/m1/s1/items/i1", nil)
	req.SetPathValue("sid", "s1")
	req.SetPathValue("iid", "i1")
	w = httptest.NewRecorder()
	h.sessionItemFull(w, req, m)
	var full struct {
		ToolInput json.RawMessage `json:"tool_input"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &full); err != nil {
		t.Fatalf("full item: %v: %.200s", err, w.Body.Bytes())
	}
	checkToolInput(t, "full item", full.ToolInput, in)

	if err := node.Notify("session.items", map[string]any{"session_id": "s1", "items": items}); err != nil {
		t.Fatal(err)
	}
	select {
	case b := <-c.out:
		checkToolInput(t, "push", firstToolInput(t, "push", b), in)
	case <-time.After(10 * time.Second):
		t.Fatal("no push reached the client")
	}

	// The catch-up a subscribe starts with: how an app first gets a session's recent items.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h.sendBacklog(ctx, c, m, "s1", 0)
	select {
	case b := <-c.out:
		checkToolInput(t, "backlog", firstToolInput(t, "backlog", b), in)
	default:
		t.Fatal("no backlog reached the client")
	}
}

// checkToolInput fails unless got is no longer than want and holds the same value.
func checkToolInput(t *testing.T, hop string, got json.RawMessage, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("%s: tool_input is not JSON: %v", hop, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if len(got) > len(want) {
		t.Errorf("%s: tool_input is %d bytes, longer than the %d it was written as: %.80s", hop, len(got), len(want), got)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("%s: tool_input changed: %.200s", hop, got)
	}
}

// firstToolInput is the first item's tool_input in a page or push.
func firstToolInput(t *testing.T, hop string, body []byte) json.RawMessage {
	t.Helper()
	var v struct {
		Items []struct {
			ToolInput json.RawMessage `json:"tool_input"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &v); err != nil || len(v.Items) == 0 {
		t.Fatalf("%s: no items (%v): %.200s", hop, err, body)
	}
	return v.Items[0].ToolInput
}

// itemsWire is a machine of h wired to a node over real rpc lines: the node answers each request with reply(method),
// and what it notifies reaches the machine's onEvent.
func itemsWire(t *testing.T, h *Hub, reply func(method string) any) (*Machine, *rpc.Peer) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h2nR, h2nW := io.Pipe()
	n2hR, n2hW := io.Pipe()
	hp, np := rpc.New("h", h2nW), rpc.New("n", n2hW)
	m := newMachine(h, MachineConfig{ID: "m1"})
	m.peer = hp
	hp.OnEvent = m.onEvent
	np.OnRequest = func(_ context.Context, method string, _ json.RawMessage) (any, error) {
		if method != "session.items" && method != "session.item" {
			return nil, fmt.Errorf("unexpected method: %s", method)
		}
		return reply(method), nil
	}
	done := make(chan struct{}, 2)
	go func() { defer func() { done <- struct{}{} }(); _ = hp.Serve(ctx, n2hR) }()
	go func() { defer func() { done <- struct{}{} }(); _ = np.Serve(ctx, h2nR) }()
	t.Cleanup(func() {
		cancel()
		for _, c := range []io.Closer{h2nR, h2nW, n2hR, n2hW} {
			_ = c.Close()
		}
		for range 2 {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("rpc cleanup timed out")
			}
		}
	})
	return m, np
}
