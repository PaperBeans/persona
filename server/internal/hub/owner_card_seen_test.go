package hub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestInteractionSeenRouteRequiresFullDevice(t *testing.T) {
	h, mgr := handoffBusHub(t)
	h.takeFleetInteractions([]proto.Interaction{{
		ID: "ix_seen_route", MachineID: "box", SessionID: "s1",
		Kind: proto.IxPermission, ToolName: proto.SecretRunTool,
	}})
	mux := http.NewServeMux()
	h.fleetRoutesRelayed(mux)

	do := func(p principal) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/interactions/ix_seen_route/seen", nil)
		req = req.WithContext(context.WithValue(req.Context(), principalKey{}, p))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w.Code
	}

	// Answer-only (watch) and extension tokens cannot mark an owner-only card seen.
	if code := do(principal{ID: "w1", Role: roleAnswer}); code != http.StatusForbidden {
		t.Fatalf("watch POST /seen = %d, want 403", code)
	}
	if code := do(principal{ID: "x1", Role: roleExtension, Extension: "helper", Manager: assistantwire.AssistantID}); code != http.StatusForbidden {
		t.Fatalf("extension POST /seen = %d, want 403", code)
	}
	if reqs, _, _ := mgr.took(); len(reqs) != 0 {
		t.Fatalf("manager unexpectedly received %d relayed requests from restricted callers", len(reqs))
	}

	// Full user device is relayed to the owning manager.
	if code := do(principal{ID: "d1", Name: "iPhone", Role: roleFull}); code != http.StatusOK {
		t.Fatalf("phone POST /seen = %d, want 200", code)
	}
	reqs, _, _ := mgr.took()
	if len(reqs) != 1 || reqs[0].Path != "/api/v1/interactions/ix_seen_route/seen" {
		t.Fatalf("relayed = %+v, want POST /api/v1/interactions/ix_seen_route/seen", reqs)
	}
}

func TestHasFullAppOnlineExcludesRestrictedAndRuntimeCLI(t *testing.T) {
	h := newTestHub(t)
	if h.hasFullAppOnline() {
		t.Fatal("empty hub reported app online")
	}

	watch := &client{role: roleAnswer, out: make(chan []byte, 4)}
	cli := &client{role: roleFull, runtimeCLI: true, out: make(chan []byte, 4)}
	h.mu.Lock()
	h.clients[watch] = struct{}{}
	h.clients[cli] = struct{}{}
	h.mu.Unlock()
	if h.hasFullAppOnline() {
		t.Fatal("watch and runtimeCLI should not count as full user app online")
	}

	phone := &client{role: roleFull, out: make(chan []byte, 4)}
	h.mu.Lock()
	h.clients[phone] = struct{}{}
	h.mu.Unlock()
	if !h.hasFullAppOnline() {
		t.Fatal("connected full device should report app online")
	}

	h.dropClient(phone)
	if h.hasFullAppOnline() {
		t.Fatal("disconnected full device should no longer report app online")
	}
}
