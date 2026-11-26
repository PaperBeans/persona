package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/extwire"
)

func TestHealthTellAnswerRouteBeforeForwarding(t *testing.T) {
	h, srv := healthIntakeHub(t)
	m := connectGuardManager(t, h, srv.URL, assistantwire.AssistantID, func(method string, raw json.RawMessage) (any, error) {
		if method == assistantwire.MethodHTTP {
			var p assistantwire.HTTPRequest
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Error(err)
			}
			if http.Header(p.Header).Get(assistantwire.RoleHeader) != roleAnswer {
				t.Errorf("watch role: %v", p.Header)
			}
			return assistantwire.HTTPResponse{Status: 202, Body: []byte(`{"id":"tell_synthetic","status":"received"}`)}, nil
		}
		return map[string]any{}, nil
	})
	mux := h.routes()
	if code, body := call(t, mux, "POST", "/api/v1/assistant/health/tell", "", `{}`); code != 401 {
		t.Fatalf("unauthenticated: %d %v", code, body)
	}
	if strings.Contains(m.log(), assistantwire.MethodHTTP+" ") {
		t.Fatal("unauthenticated request forwarded")
	}
	code, watch := call(t, mux, "POST", "/api/v1/auth/tokens", h.cfg.Token, `{"name":"Synthetic watch","kind":"watch"}`)
	if code != 200 || watch["role"] != roleAnswer {
		t.Fatalf("watch: %d %v", code, watch)
	}
	token := watch["token"].(string)
	for _, r := range []struct{ method, path string }{{"POST", "/api/v1/assistant/health/tell"}, {"GET", "/api/v1/assistant/health/intake"}, {"POST", "/api/v1/assistant/health/intake"}} {
		if code, body := call(t, mux, r.method, r.path, token, `{"client_id":"watch-client","text":"note"}`); code != 202 {
			t.Fatalf("watch %s %s: %d %v", r.method, r.path, code, body)
		}
	}
	for _, r := range []struct{ method, path string }{{"GET", "/api/v1/assistant/health/tell"}, {"PUT", "/api/v1/assistant/health/tell"}, {"POST", "/api/v1/assistant/health/tell/x"}, {"POST", "/api/v1/assistant/health/workouts"}, {"POST", "/api/v1/assistant/conversations"}, {"GET", "/api/v1/assistant/health/items"}, {"POST", "/api/v1/assistant/health/intake/ui"}} {
		if code, body := call(t, mux, r.method, r.path, token, `{}`); code != 403 {
			t.Fatalf("forbidden %s %s: %d %v", r.method, r.path, code, body)
		}
	}
	if n := strings.Count(m.log(), assistantwire.MethodHTTP+" "); n != 3 {
		t.Fatalf("forwarded %d HTTP calls: %s", n, m.log())
	}
}

func TestHealthTellExtensionsCannotReach(t *testing.T) {
	h, srv := healthIntakeHub(t)
	m := connectGuardManager(t, h, srv.URL, assistantwire.AssistantID, func(method string, raw json.RawMessage) (any, error) {
		if method == extwire.MethodAuthorize {
			var p extwire.Authorize
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Error(err)
			}
			if scope := extwire.RouteScopes[p.Pattern]; scope != "" {
				t.Errorf("Tell Health scope: %q", scope)
			}
			return extwire.Authorized{Extension: "synthetic", Name: "Synthetic"}, nil
		}
		return assistantwire.HTTPResponse{Status: 202, Body: []byte(`{}`)}, nil
	})
	if code, body := call(t, h.routes(), "POST", "/api/v1/assistant/health/tell", extTokenPrefix+"synthetic-token", `{"client_id":"extension-client","text":"note"}`); code != 403 {
		t.Fatalf("extension: %d %v", code, body)
	}
	if strings.Contains(m.log(), assistantwire.MethodHTTP+" ") {
		t.Fatalf("extension forwarded: %s", m.log())
	}
}

func TestHealthTellOnceSurvivesHubRestart(t *testing.T) {
	store := newSentStore()
	var typed atomic.Int32
	for range 2 {
		h := newTestHub(t)
		h.store = store
		pipeNode(t, h, "box", func(context.Context) error { typed.Add(1); return nil })
		p := assistantwire.DeviceCall{Device: "box", Method: assistantwire.DeviceSessionSendOnce, Params: json.RawMessage(`{"session_id":"health-chat","text":"private sentence","client_msg_id":"health-tell:tell_replayed"}`)}
		if _, handled, err := h.channelsDeviceCall(t.Context(), h.byID["box"], p); err != nil || !handled {
			t.Fatalf("once call: %v %v", handled, err)
		}
	}
	if typed.Load() != 1 {
		t.Fatalf("typed %d times after restart", typed.Load())
	}
}
