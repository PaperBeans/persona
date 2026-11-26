package hub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extwire"
)

// Guarded idle archive-and-close and its audit read require the named-only agents.archive_idle scope (excluded from
// legacy every-scope and agents.control grants).
func TestIdleArchiveRouteScopes(t *testing.T) {
	h := newTestHub(t)
	mux := h.routes()
	tokens := extTokensOf(t, h)

	if extension.InEveryScope(extension.ScopeAgentsArchiveIdle) {
		t.Fatal("legacy every-scope grant must not include agents.archive_idle")
	}
	if title, ok := extension.ScopeTitle(extension.ScopeAgentsArchiveIdle); !ok || title == "" {
		t.Fatalf("agents.archive_idle scope title: %q %v", title, ok)
	}

	routes := []struct {
		method  string
		pattern string
		path    string
	}{
		{http.MethodPost, assistantwire.RouteAgentIdleArchive, "/api/v1/sessions/mac/sess-1/archive-idle"},
		{http.MethodGet, assistantwire.RouteAgentIdleArchiveAudit, "/api/v1/sessions/archive-idle/audit"},
	}

	for _, rt := range routes {
		req := httptest.NewRequest(rt.method, rt.path, nil)
		if _, gotPattern := h.apiMux.Handler(req); gotPattern != rt.pattern {
			t.Fatalf("mux pattern for %s %s = %q, want %q", rt.method, rt.path, gotPattern, rt.pattern)
		}
		if got := extwire.RouteScopes[rt.pattern]; got != extension.ScopeAgentsArchiveIdle {
			t.Fatalf("RouteScopes[%q] = %q, want %q", rt.pattern, got, extension.ScopeAgentsArchiveIdle)
		}
	}

	for name, grants := range map[string][]string{
		"none":           nil,
		"agents-read":    {extension.ScopeAgentsRead},
		"agents-control": {extension.ScopeAgentsControl},
		"legacy-every":   extension.EveryScope(),
	} {
		t.Run("denied-"+name, func(t *testing.T) {
			tok := tokens.issue("idle-denied-"+name, "Denied "+name, grants)
			for _, rt := range routes {
				status, body := call(t, mux, rt.method, rt.path, tok, `{"idle_minutes":30}`)
				if status != http.StatusForbidden || body["code"] != "permission_denied" || body["scope"] != extension.ScopeAgentsArchiveIdle {
					t.Fatalf("%s %s with %s: got %d %v", rt.method, rt.path, name, status, body)
				}
			}
		})
	}

	tok := tokens.issue("idle-granted", "Guardrail", []string{extension.ScopeAgentsArchiveIdle})
	for _, rt := range routes {
		status, body := call(t, mux, rt.method, rt.path, tok, `{"idle_minutes":30}`)
		if status == http.StatusForbidden && body["code"] == "permission_denied" {
			t.Fatalf("agents.archive_idle was denied %s %s: %d %v", rt.method, rt.path, status, body)
		}
	}
}

func TestIdleArchiveCapabilityAndManagerBoundary(t *testing.T) {
	h, srv, a, b := archiveRelayHub(t)

	// Older manager without CapabilityAgentSessionIdleArchive is rejected 501 without forwarding.
	archiveCloseCapability(t, b, true)
	before := len(archiveHTTPCalls(t, b))
	code, body := ownershipGet(t, h, srv, http.MethodPost, "/api/v1/sessions/mb/same/archive-idle", nil)
	if code != http.StatusNotImplemented || body["code"] != "agent_idle_archive_unsupported" {
		t.Fatalf("old manager post: got %d %+v", code, body)
	}
	if len(archiveHTTPCalls(t, b)) != before {
		t.Fatal("archive-idle reached manager without capability")
	}

	// Before every manager announces agent_session_idle_archive, Hub must NOT advertise it,
	// and an extension report cannot spoof it into Hub features.
	if slices.Contains(h.features(), featureAgentSessionIdleArchive) {
		t.Fatal("hub advertised agent_session_idle_archive before managers announced it")
	}
	spoof, _ := json.Marshal(extwire.Report{Features: []string{featureAgentSessionIdleArchive}})
	busEvents[extwire.EventReport](h, nil, spoof)
	if slices.Contains(h.features(), featureAgentSessionIdleArchive) {
		t.Fatal("extension report spoofed manager-owned agent_session_idle_archive feature")
	}

	// Advertising the capability on every manager allows forwarding and advertises the feature.
	caps := []string{
		assistantwire.CapabilityConfigOwn,
		assistantwire.CapabilityAgentSessionArchives,
		assistantwire.CapabilityAgentSessionArchiveClose,
		assistantwire.CapabilityAgentSessionIdleArchive,
	}
	if err := b.call(t, busHello, map[string]any{"id": b.id, "capabilities": caps}, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.call(t, busHello, map[string]any{"id": a.id, "capabilities": caps}, nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(h.features(), featureAgentSessionIdleArchive) {
		t.Fatal("hub did not advertise agent_session_idle_archive when every manager announced it")
	}
	if code, body := ownershipGet(t, h, srv, http.MethodPost, "/api/v1/sessions/mb/same/archive-idle", nil); code != http.StatusOK {
		t.Fatalf("capable manager post: got %d %+v", code, body)
	}
	if code, body := ownershipGet(t, h, srv, http.MethodGet, "/api/v1/sessions/archive-idle/audit", nil); code != http.StatusOK {
		t.Fatalf("capable manager audit: got %d %+v", code, body)
	}
}
