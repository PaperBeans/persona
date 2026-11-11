package hub

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/rpc"
)

// restoreNode is a node that answers session.restore_model with answer/err and keeps every call it was sent.
type restoreNode struct {
	mu      sync.Mutex
	methods []string
	params  []string
	answer  any
	err     error
	hub     *Hub
}

func newRestoreNode(t *testing.T) *restoreNode {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h2nR, h2nW := io.Pipe()
	n2hR, n2hW := io.Pipe()
	hubSide, nodeSide := rpc.New("h", h2nW), rpc.New("n", n2hW)
	rn := &restoreNode{answer: map[string]any{"restore": "next_start", "restore_to": "configured-model", "closed": true}} // the node's real answer
	nodeSide.OnRequest = func(_ context.Context, method string, params json.RawMessage) (any, error) {
		rn.mu.Lock()
		defer rn.mu.Unlock()
		rn.methods = append(rn.methods, method)
		if method != "session.restore_model" {
			return map[string]any{}, nil
		}
		rn.params = append(rn.params, string(params))
		return rn.answer, rn.err
	}
	go func() { _ = hubSide.Serve(ctx, n2hR) }()
	go func() { _ = nodeSide.Serve(ctx, h2nR) }()
	t.Cleanup(func() { cancel(); _ = h2nW.Close(); _ = n2hW.Close() })
	h := &Hub{byID: map[string]*Machine{}, logger: log.New(io.Discard, "", 0)}
	m := newMachine(h, MachineConfig{ID: "m1", Name: "m1"})
	m.peer = hubSide
	h.byID["m1"] = m
	rn.hub = h
	return rn
}

func (rn *restoreNode) post(role, sid, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/api/v1/sessions/m1/"+sid+"/restore-model", strings.NewReader(body))
	r.SetPathValue("mid", "m1")
	r.SetPathValue("sid", sid)
	r = r.WithContext(context.WithValue(r.Context(), principalKey{}, principal{Role: role, ID: "dev"}))
	w := httptest.NewRecorder()
	rn.hub.withMachine(rn.hub.restoreSessionModel)(w, r)
	return w
}

func (rn *restoreNode) calls() ([]string, []string) {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	return append([]string(nil), rn.methods...), append([]string(nil), rn.params...)
}

// Closing a session's process is a full device's control: the watch, an extension and a link are refused before the
// node hears of it, and the route is full-credentials-only in the extension scopes.
func TestRestoreModelNeedsAFullDevice(t *testing.T) {
	rn := newRestoreNode(t)
	for _, role := range []string{roleAnswer, roleExtension} {
		if w := rn.post(role, "s1", ``); w.Code != http.StatusForbidden {
			t.Errorf("%s was let through: %d", role, w.Code)
		}
	}
	if allowed(principal{ID: "dev", Role: roleAnswer}, http.MethodPost, "/api/v1/sessions/m1/s1/restore-model") {
		t.Error("an answer-only device may POST the route")
	}
	if allowed(principal{ID: "dev_link", Role: roleLink}, http.MethodPost, "/api/v1/sessions/m1/s1/restore-model") {
		t.Error("a link may POST the route")
	}
	if w := rn.post(roleFull, "pane:w1:p2", ``); w.Code != http.StatusBadRequest {
		t.Errorf("a pane address: %d", w.Code)
	}
	if methods, _ := rn.calls(); len(methods) != 0 {
		t.Fatalf("the node heard of a refused request: %q", methods)
	}
	data, err := os.ReadFile("testdata/route_scopes.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "-                    POST /api/v1/sessions/{mid}/{sid}/restore-model") {
		t.Error("the route is not full-credentials-only in the extension scopes")
	}
}

// The node is sent session.restore_model with the session id alone, once: whatever a body says (a message, a model,
// keys, another session) never reaches it, and nothing else is called — no message, no replay.
func TestRestoreModelSendsOnlyTheSession(t *testing.T) {
	rn := newRestoreNode(t)
	w := rn.post(roleFull, "s1", `{"prompt":"do it again","text":"continue","model":"other-model","keys":["enter"],"session_id":"s2","replay":true}`)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"closed":true,"restore":"next_start","restore_to":"configured-model"}` {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	methods, params := rn.calls()
	if len(methods) != 1 || methods[0] != "session.restore_model" {
		t.Fatalf("the node was called %q", methods)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(params[0]), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["session_id"] != "s1" {
		t.Fatalf("the node was sent %v", got)
	}
}

// The node's refusals (busy, a terminal session, no record) come back as 409 with their words; an older node is
// node_outdated, never a success.
func TestRestoreModelRefusalsAndOldNodes(t *testing.T) {
	rn := newRestoreNode(t)
	for _, code := range []string{"restore_busy", "restore_unsupported", "restore_nothing", "restore_unknown", "restore_gone"} {
		rn.mu.Lock()
		rn.err = &rpc.Error{Code: code, Message: "说明 " + code}
		rn.mu.Unlock()
		w := rn.post(roleFull, "s1", ``)
		var body struct{ Code, Error string }
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if w.Code != http.StatusConflict || body.Code != code || body.Error != "说明 "+code {
			t.Errorf("%s: %d %s", code, w.Code, w.Body.String())
		}
	}
	rn.mu.Lock()
	rn.err = &rpc.Error{Message: "unknown method session.restore_model"}
	rn.mu.Unlock()
	w := rn.post(roleFull, "s1", ``)
	var old struct{ Code, Error string }
	_ = json.Unmarshal(w.Body.Bytes(), &old)
	if w.Code != http.StatusConflict || old.Code != "node_outdated" || old.Error == "" || strings.Contains(old.Error, "hub-restoremodel") {
		t.Fatalf("old node: %d %s", w.Code, w.Body.String())
	}
}

// The route is registered on the hub's mux, as a relay with the session ops (session_routes.go).
func TestRestoreModelRouteRegistered(t *testing.T) {
	data, err := os.ReadFile("session_routes.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"POST /api/v1/sessions/{mid}/{sid}/restore-model", h.withClientOp("restore_model", h.withMachine(h.restoreSessionModel))`) {
		t.Fatal("restore-model is not registered as a relay of restoreSessionModel")
	}
}
