package assistant

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func TestIntegrationsV2SettingsLifecycle(t *testing.T) {
	s, _, _ := briefService(t, "Verify unified integrations toggle")
	if out, bad := tool(t, s, "todo_create", map[string]any{"title": "Existing workflow item"}); bad {
		t.Fatal(out)
	}
	item, ok := todoByTitle(s, "Existing workflow item")
	if !ok {
		t.Fatal("missing workflow item")
	}

	put := func(manager, role, body string, status int) string {
		t.Helper()
		w, _ := permissionSettingsRequest(t, s, "PUT", "/api/v1/assistant", manager, role, body)
		if w.Code != status {
			t.Fatalf("PUT %s (manager=%q role=%q): got %d, want %d: %s", body, manager, role, w.Code, status, w.Body.String())
		}
		return w.Body.String()
	}

	// Default is false.
	w, state := permissionSettingsRequest(t, s, "GET", "/api/v1/assistant", s.managerIdentity(), "full", "")
	if w.Code != http.StatusOK || state.IntegrationsV2 {
		t.Fatalf("GET default: code=%d integrations_v2=%t", w.Code, state.IntegrationsV2)
	}

	// Cross-manager and read-only role are rejected with 403; missing manager target returns 400.
	put("another", "full", `{"integrations_v2":true}`, http.StatusForbidden)
	put("", "full", `{"integrations_v2":true}`, http.StatusBadRequest)
	put(s.managerIdentity(), "read", `{"integrations_v2":true}`, http.StatusForbidden)

	// Enable with matching optimistic lock.
	put(s.managerIdentity(), "full", `{"integrations_v2":true,"expected_integrations_v2":false}`, http.StatusOK)
	w, state = permissionSettingsRequest(t, s, "GET", "/api/v1/assistant", s.managerIdentity(), "full", "")
	if w.Code != http.StatusOK || !state.IntegrationsV2 {
		t.Fatalf("GET after enable: code=%d integrations_v2=%t", w.Code, state.IntegrationsV2)
	}

	// Stale expected_integrations_v2 returns 409 configuration_conflict without changing state.
	conflictBody := put(s.managerIdentity(), "full", `{"integrations_v2":false,"expected_integrations_v2":false}`, http.StatusConflict)
	var errResp struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(conflictBody), &errResp); err != nil || errResp.Code != "configuration_conflict" {
		t.Fatalf("unexpected conflict response: %s", conflictBody)
	}
	w, state = permissionSettingsRequest(t, s, "GET", "/api/v1/assistant", s.managerIdentity(), "full", "")
	if w.Code != http.StatusOK || !state.IntegrationsV2 {
		t.Fatalf("GET after conflict: code=%d integrations_v2=%t", w.Code, state.IntegrationsV2)
	}

	// Lossless rollback to false with matching optimistic lock.
	put(s.managerIdentity(), "full", `{"integrations_v2":false,"expected_integrations_v2":true}`, http.StatusOK)
	w, state = permissionSettingsRequest(t, s, "GET", "/api/v1/assistant", s.managerIdentity(), "full", "")
	if w.Code != http.StatusOK || state.IntegrationsV2 {
		t.Fatalf("GET after rollback: code=%d integrations_v2=%t", w.Code, state.IntegrationsV2)
	}

	// Toggling performs zero destructive migration on existing state.
	after, ok := todoByTitle(s, "Existing workflow item")
	if !ok || !reflect.DeepEqual(after, item) {
		t.Fatalf("setting change mutated saved item: got %+v, want %+v", after, item)
	}
}

func TestIntegrationsV2PersistsForSelectedManager(t *testing.T) {
	st := &memStore{}
	f := newFakeHub().withMachine()
	s := newServiceOn(t, f, st)
	s.id = "example-manager"

	w, _ := permissionSettingsRequest(t, s, "PUT", "/api/v1/assistant", s.id, "full", `{"integrations_v2":true,"expected_integrations_v2":false}`)
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}

	reloaded := newServiceOn(t, f, st)
	reloaded.id = s.id
	w, view := permissionSettingsRequest(t, reloaded, "GET", "/api/v1/assistant", s.id, "full", "")
	if w.Code != http.StatusOK || view.Manager != s.id || !view.IntegrationsV2 {
		t.Fatalf("lost manager preference on reload: code=%d manager=%q integrations_v2=%t", w.Code, view.Manager, view.IntegrationsV2)
	}

	// Roll back to false and verify lossless persistence across reload.
	w, _ = permissionSettingsRequest(t, reloaded, "PUT", "/api/v1/assistant", s.id, "full", `{"integrations_v2":false,"expected_integrations_v2":true}`)
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}

	reloadedOff := newServiceOn(t, f, st)
	reloadedOff.id = s.id
	w, view = permissionSettingsRequest(t, reloadedOff, "GET", "/api/v1/assistant", s.id, "full", "")
	if w.Code != http.StatusOK || view.Manager != s.id || view.IntegrationsV2 {
		t.Fatalf("rollback preference did not persist on reload: code=%d manager=%q integrations_v2=%t", w.Code, view.Manager, view.IntegrationsV2)
	}
}
