package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// capturedLog has h's log written to a buffer it returns.
func capturedLog(h *Hub) *bytes.Buffer {
	var b bytes.Buffer
	h.logger = log.New(&b, "", 0)
	return &b
}

// A server error's request line says the code and the technical words of the error the hub wrote itself (apierr): the
// detail a localized failure keeps apart from its generic error, else the error; one line, quoted, clipped. Other
// answers' lines are as they were, and so is the line of a server error the hub passes on as it is, whatever its body
// holds: a manager's, an extension's or a linked hub's answer, through a reverse proxy or as bytes (#13468, #14490).
func TestServerErrorRequestLineSaysTheError(t *testing.T) {
	h := newTestHub(t)
	logs := capturedLog(h)
	const secret = "passed-on-secret"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"code":"internal","error":"`+secret+`","detail":"`+secret+`"}`)
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	relayed := httputil.NewSingleHostReverseProxy(target)
	closed := httptest.NewServer(http.NotFoundHandler())
	gone, _ := url.Parse(closed.URL)
	closed.Close()
	unreachable := httputil.NewSingleHostReverseProxy(gone)
	unreachable.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) { writeErr(w, http.StatusBadGateway, err) }
	handler := logRequests(h, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/node":
			apierr.Call(w, errors.New("no such session"))
		case "/long":
			writeErr(w, http.StatusInternalServerError, errors.New("first line\nsecond "+strings.Repeat("x", 400)))
		case "/words":
			apierr.WriteBody(w, http.StatusServiceUnavailable, map[string]any{"code": "assistant_offline", "error": "the manager is not connected"})
		case "/send":
			status, body := callErrBytes("", unknownDelivery("", "hub-send.lost_during"))
			writeRaw(w, status, body)
		case "/bytes":
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"code":"assistant_offline","error":"`+secret+`"}`)
		case "/relayed":
			relayed.ServeHTTP(w, r)
		case "/unreachable":
			unreachable.ServeHTTP(w, r)
		case "/plain":
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, "upstream says no")
		case "/refused":
			writeErr(w, http.StatusConflict, errors.New("already handled"))
		}
	}))
	paths := []string{"/node", "/long", "/words", "/send", "/bytes", "/relayed", "/unreachable", "/plain", "/refused"}
	for _, path := range paths {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != len(paths) || strings.Contains(logs.String(), secret) {
		t.Fatalf("log:\n%s", logs.String())
	}
	if !strings.Contains(lines[0], "GET /node -> 502 (") || !strings.HasSuffix(lines[0], ` code=internal detail="no such session"`) {
		t.Errorf("node error line: %s", lines[0])
	}
	_, quoted, ok := strings.Cut(lines[1], ` detail=`)
	if !ok || !strings.Contains(lines[1], "-> 500 (") || !strings.HasPrefix(quoted, `"first line second xxx`) ||
		utf8.RuneCountInString(quoted) > loggedErrorText+3 || strings.Contains(quoted, `\n`) {
		t.Errorf("long error line: %s", lines[1])
	}
	if !strings.HasSuffix(lines[2], ` code=assistant_offline error="the manager is not connected"`) {
		t.Errorf("an error without a detail: %s", lines[2])
	}
	if !strings.Contains(lines[3], "GET /send -> 502 (") || !strings.Contains(lines[3], " code=") {
		t.Errorf("a send's answer the hub built: %s", lines[3])
	}
	for i, path := range []string{"/bytes", "/relayed"} {
		if line := lines[4+i]; !strings.Contains(line, "GET "+path+" -> ") || !strings.HasSuffix(line, ")") {
			t.Errorf("a body passed on as it is is not logged: %s", line)
		}
	}
	if !strings.Contains(lines[6], "GET /unreachable -> 502 (") || !strings.Contains(lines[6], " code=") {
		t.Errorf("the hub's own error when the proxy finds no one: %s", lines[6])
	}
	if !strings.HasSuffix(lines[7], ")") || strings.Contains(lines[7], "upstream says no") {
		t.Errorf("a body that is not the hub's error is not logged: %s", lines[7])
	}
	if !strings.Contains(lines[8], "-> 409 (") || strings.Contains(lines[8], "already handled") {
		t.Errorf("a refusal's line is as it was: %s", lines[8])
	}
}

// A manager's server error goes back to the app as it is, and nothing of its body reaches the hub's log: the request
// line names only an error the hub wrote itself (#14490).
func TestRelayedServerErrorStaysOutOfTheLog(t *testing.T) {
	h, srv, _ := newBusHub(t)
	var mu sync.Mutex
	var logs bytes.Buffer
	h.logger = log.New(lockedWriter{&mu, &logs}, "", 0)
	const secret = "relayed-secret-0f4e2d1c"
	answer := []byte(`{"code":"internal","error":"` + secret + `","detail":"` + secret + `"}`)
	id := assistantwire.AssistantID
	token := busToken(t, h, id, id)
	// The manager answers the same over a relay stream, which a reverse proxy reads (relayTo: an extension route such as
	// /api/v1/messaging), and as an http.request (relayHTTP: the assistant's routes, /api/v1/assistant).
	x := &relayingExt{serve: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write(answer)
	}), dial: func(ctx context.Context, query string) (*websocket.Conn, error) {
		return bus.DialAt(ctx, srv.URL, token, srv.Client(), devicewire.RelayPath, query)
	}}
	manager := dialRawBusWith(t, srv, token, func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		switch method {
		case extwire.MethodAccept:
			out, _, err := x.Request(ctx, method, params)
			return out, err
		case assistantwire.MethodHTTP:
			return assistantwire.HTTPResponse{Status: http.StatusInternalServerError, Header: map[string][]string{"Content-Type": {"application/json"}}, Body: answer}, nil
		}
		return nil, &rpc.Error{Code: "no_such_thing", Message: "the fake manager has no " + method}
	})
	if err := manager.call("manager.hello", map[string]any{"id": id}, nil); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/assistant", "/api/v1/messaging"} {
		if status, body := ownershipGet(t, h, srv, http.MethodGet, path, nil); status != http.StatusInternalServerError || body["error"] != secret {
			t.Fatalf("%s: the manager's answer is passed on as it is: %d %v", path, status, body)
		}
		logged := func() string {
			mu.Lock()
			defer mu.Unlock()
			for _, line := range strings.Split(logs.String(), "\n") {
				if strings.Contains(line, "GET "+path+" -> 500 (") {
					return line
				}
			}
			return ""
		}
		eventually(t, 5*time.Second, path+"'s request line", func() bool { return logged() != "" })
		if line := logged(); !strings.HasSuffix(line, ")") {
			t.Errorf("%s: %s", path, line)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Contains(logs.String(), secret) {
		t.Fatalf("the manager's body is in the log:\n%s", logs.String())
	}
}

// itemsNode is a node whose session.items answers page as it is: a runtime's page, which the node passes on raw.
func itemsNode(t *testing.T, h *Hub, page string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h2nR, h2nW := io.Pipe()
	n2hR, n2hW := io.Pipe()
	hubSide, nodeSide := rpc.New("h", h2nW), rpc.New("n", n2hW)
	nodeSide.OnRequest = func(_ context.Context, method string, _ json.RawMessage) (any, error) {
		if method == "session.items" {
			return json.RawMessage(page), nil
		}
		return nil, fmt.Errorf("unknown method %s", method)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = hubSide.Serve(ctx, n2hR) }()
	go func() { defer wg.Done(); _ = nodeSide.Serve(ctx, h2nR) }()
	t.Cleanup(func() { cancel(); h2nW.Close(); n2hW.Close(); wg.Wait() })
	m := newMachine(h, MachineConfig{ID: "m1", Name: "dev box"})
	m.peer, m.online = hubSide, true
	m.info = proto.NodeInfo{Version: "test", Protocol: 1, Capabilities: []string{nodeproto.Sessions}}
	h.byID["m1"] = m
}

func itemsRequest(h *Hub) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/m1/agy-0f4e2d1c/items?limit=50&fold_work=1", nil)
	r.SetPathValue("mid", "m1")
	r.SetPathValue("sid", "agy-0f4e2d1c")
	r = r.WithContext(context.WithValue(r.Context(), principalKey{}, principal{Role: roleFull, ID: "dev"}))
	w := httptest.NewRecorder()
	logRequests(h, h.withMachine(h.sessionItems)).ServeHTTP(w, r)
	return w
}

// A runtime page with an item field of another JSON type than proto.Item's fails the whole page in the hub's own
// decode (the node passes a runtime's page on raw): 502 "internal", the decoder's words in detail. The request line
// names them; a page that decodes is served and leaves no line.
func TestRuntimeItemsPageThatDoesNotDecodeIsLogged(t *testing.T) {
	h := newTestHub(t)
	logs := capturedLog(h)
	itemsNode(t, h, `{"items":[{"id":"1#0","seq":"1","ts":"2026-11-22T03:12:16Z","role":"assistant","kind":"text","text":"done"}],"has_more":false}`)
	w := itemsRequest(h)
	var body struct{ Code, Detail string }
	if w.Code != http.StatusBadGateway || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Code != "internal" ||
		!strings.Contains(body.Detail, "cannot unmarshal string into Go struct field") || !strings.Contains(body.Detail, "seq of type int") {
		t.Fatalf("answer %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(logs.String(), `/items -> 502 (`) || !strings.Contains(logs.String(), `code=internal detail="`+body.Detail+`"`) {
		t.Fatalf("log:\n%s", logs.String())
	}

	h2 := newTestHub(t)
	logs2 := capturedLog(h2)
	itemsNode(t, h2, `{"items":[{"id":"1#0","seq":1,"ts":"2026-11-22T03:12:16Z","role":"assistant","kind":"text","text":"done"}],"has_more":false}`)
	if w := itemsRequest(h2); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"text":"done"`) || logs2.Len() != 0 {
		t.Fatalf("a page that decodes: %d %s, log %q", w.Code, w.Body.String(), logs2.String())
	}
}
