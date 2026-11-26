package hub

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extwire"
)

// Machine resource summary requires machines.read; placement scheduling routes require the named-only
// tasks.schedule scope (excluded from legacy every-scope and broad to-do grants).
func TestPlacementAndMachineResourcesRouteScopes(t *testing.T) {
	h := newTestHub(t)
	mux := h.routes()
	tokens := extTokensOf(t, h)

	if extension.InEveryScope(extension.ScopeTasksSchedule) {
		t.Fatal("legacy every-scope grant must not include tasks.schedule")
	}
	if title, ok := extension.ScopeTitle(extension.ScopeTasksSchedule); !ok || title == "" {
		t.Fatalf("tasks.schedule scope title: %q %v", title, ok)
	}

	type routeCase struct {
		method  string
		pattern string
		path    string
		scope   string
	}
	placementRoutes := []routeCase{
		{http.MethodGet, "GET /api/v1/assistant/placement/pending", "/api/v1/assistant/placement/pending", extension.ScopeTasksSchedule},
		{http.MethodPost, "POST /api/v1/assistant/placement/{todo_id}/assign", "/api/v1/assistant/placement/todo_example/assign", extension.ScopeTasksSchedule},
		{http.MethodPost, "POST /api/v1/assistant/placement/{todo_id}/defer", "/api/v1/assistant/placement/todo_example/defer", extension.ScopeTasksSchedule},
	}
	resourceRoute := routeCase{
		http.MethodGet, "GET /api/v1/assistant/machine-resources", "/api/v1/assistant/machine-resources", extension.ScopeMachinesRead,
	}

	for _, rt := range append(placementRoutes, resourceRoute) {
		req := httptest.NewRequest(rt.method, rt.path, nil)
		if _, gotPattern := h.apiMux.Handler(req); gotPattern != rt.pattern {
			t.Fatalf("mux pattern for %s %s = %q, want %q", rt.method, rt.path, gotPattern, rt.pattern)
		}
		if got := extwire.RouteScopes[rt.pattern]; got != rt.scope {
			t.Fatalf("RouteScopes[%q] = %q, want %q", rt.pattern, got, rt.scope)
		}
	}

	for name, grants := range map[string][]string{
		"none":         nil,
		"todos":        {extension.ScopeTodosRead, extension.ScopeTodosWrite},
		"machines":     {extension.ScopeMachinesRead},
		"legacy-every": extension.EveryScope(),
	} {
		t.Run("placement-denied-"+name, func(t *testing.T) {
			tok := tokens.issue("sched-denied-"+name, "Denied "+name, grants)
			for _, rt := range placementRoutes {
				status, body := call(t, mux, rt.method, rt.path, tok, `{}`)
				if status != http.StatusForbidden || body["code"] != "permission_denied" || body["scope"] != extension.ScopeTasksSchedule {
					t.Fatalf("%s %s with %s: got %d %v", rt.method, rt.path, name, status, body)
				}
			}
		})
	}

	schedTok := tokens.issue("sched-granted", "Scheduler", []string{extension.ScopeTasksSchedule})
	for _, rt := range placementRoutes {
		status, body := call(t, mux, rt.method, rt.path, schedTok, `{}`)
		if status == http.StatusForbidden && body["code"] == "permission_denied" {
			t.Fatalf("tasks.schedule was denied %s %s: %d %v", rt.method, rt.path, status, body)
		}
	}
	// tasks.schedule alone does not open machines.read or owner todo actions.
	if status, body := call(t, mux, resourceRoute.method, resourceRoute.path, schedTok, ""); status != http.StatusForbidden || body["code"] != "permission_denied" || body["scope"] != extension.ScopeMachinesRead {
		t.Fatalf("tasks.schedule opened machine-resources: %d %v", status, body)
	}

	machinesTok := tokens.issue("machines-granted", "Machines", []string{extension.ScopeMachinesRead})
	status, body := call(t, mux, resourceRoute.method, resourceRoute.path, machinesTok, "")
	if status == http.StatusForbidden && body["code"] == "permission_denied" {
		t.Fatalf("machines.read was denied machine-resources: %d %v", status, body)
	}
}
