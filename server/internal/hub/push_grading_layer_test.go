package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/i18n"
)

func gradingLayerHTTP(t *testing.T, h *Hub, p *principal, method, target, body string) (int, []byte) {
	t.Helper()
	mux := http.NewServeMux()
	h.pushGradingRoutes(mux)
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	if p != nil {
		r = r.WithContext(context.WithValue(r.Context(), principalKey{}, *p))
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w.Code, w.Body.Bytes()
}

func extGradingPrincipal(manager, ext string) *principal {
	return &principal{
		ID:        "extension:" + ext,
		Name:      ext,
		Role:      roleExtension,
		Extension: ext,
		Manager:   manager,
		Scopes:    []string{extension.ScopePushGrading},
	}
}

// Scope enforcement through the full hub routes and authorization middleware:
// push.grading is required for /api/v1/push/grading/layer and /api/v1/push/grading/preview,
// is excluded from notify and legacy every-scope / all-scope tokens, and never opens PUT /api/v1/push/grading.
func TestPushGradingLayerScopeAuthorization(t *testing.T) {
	h := newTestHub(t)
	h.push.store = newGradingStore()
	mux := h.routes()
	tokens := extTokensOf(t, h)

	routes := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/api/v1/push/grading/layer", ""},
		{http.MethodPut, "/api/v1/push/grading/layer", `{"grading_enabled":true}`},
		{http.MethodDelete, "/api/v1/push/grading/layer", ""},
		{http.MethodPost, "/api/v1/push/grading/preview", `{"notification":{"kind":"assistant","level":"P1"}}`},
	}

	for name, tok := range map[string]string{
		"none":         tokens.issue("ext-none", "None", nil),
		"notify-only":  tokens.issue("ext-notify", "Notify", []string{extension.ScopeNotify}),
		"legacy-every": tokens.issue("ext-every", "Every", extension.EveryScope()),
	} {
		t.Run("denied_"+name, func(t *testing.T) {
			for _, rt := range routes {
				code, body := call(t, mux, rt.method, rt.path, tok, rt.body)
				if code != http.StatusForbidden || body["code"] != "permission_denied" || body["scope"] != extension.ScopePushGrading {
					t.Fatalf("%s %s (%s): got %d %v, want 403 permission_denied scope=%s", rt.method, rt.path, name, code, body, extension.ScopePushGrading)
				}
			}
		})
	}

	granted := tokens.issue("ext-grader", "Grader", []string{extension.ScopePushGrading})
	for _, rt := range routes {
		code, body := call(t, mux, rt.method, rt.path, granted, rt.body)
		if code != http.StatusOK {
			t.Fatalf("%s %s with push.grading: got %d %v, want 200", rt.method, rt.path, code, body)
		}
	}

	// push.grading never opens the owner's switch writes.
	for _, switchPath := range []string{"/api/v1/push/grading", "/api/v1/push/ln-mode"} {
		code, body := call(t, mux, http.MethodPut, switchPath, granted, `{"enabled":true}`)
		if code != http.StatusForbidden || body["code"] != "permission_denied" {
			t.Fatalf("PUT %s with push.grading: got %d %v, want 403 permission_denied", switchPath, code, body)
		}
	}

	// Answer-only and scoped credentials are refused on all four routes.
	for _, role := range []string{roleAnswer, roleScoped} {
		p := &principal{ID: "dev-" + role, Role: role}
		for _, rt := range routes {
			code, body := gradingLayerHTTP(t, h, p, rt.method, rt.path, rt.body)
			if code != http.StatusForbidden {
				t.Fatalf("%s %s as %s: got %d %s, want 403", rt.method, rt.path, role, code, body)
			}
		}
	}

	// Full credentials can GET, DELETE, and preview, but cannot PUT an extension-owned layer.
	master := h.cfg.Token
	if code, body := call(t, mux, http.MethodPut, "/api/v1/push/grading/layer", master, `{"grading_enabled":true}`); code != http.StatusForbidden {
		t.Fatalf("full credential PUT layer: got %d %v, want 403", code, body)
	}
	if code, body := call(t, mux, http.MethodGet, "/api/v1/push/grading/layer", master, ""); code != http.StatusOK {
		t.Fatalf("full credential GET layer: got %d %v, want 200", code, body)
	}
	if code, body := call(t, mux, http.MethodPost, "/api/v1/push/grading/preview", master, `{"notification":{"kind":"assistant","level":"P1"}}`); code != http.StatusOK {
		t.Fatalf("full credential POST preview: got %d %v, want 200", code, body)
	}
	if code, body := call(t, mux, http.MethodDelete, "/api/v1/push/grading/layer", master, ""); code != http.StatusOK {
		t.Fatalf("full credential DELETE layer: got %d %v, want 200", code, body)
	}
}

// Strict layer ownership and manager isolation: an extension can read, write, delete and preview only its own layer
// on its own manager; naming another extension or manager is rejected with 403.
func TestPushGradingLayerOwnershipIsolation(t *testing.T) {
	h, f := gradingHub(t)
	extA := extGradingPrincipal(assistantwire.AssistantID, "ext-a")
	extB := extGradingPrincipal(assistantwire.AssistantID, "ext-b")
	extOtherMgr := extGradingPrincipal("manager-b", "ext-a")

	// Ext A stores its layer.
	code, body := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer",
		`{"grading_enabled":true,"rules":[{"id":"mute-ci","when":{"related_key_prefix":"ci:"},"action":"silent","reason":"ext_ci_silent"}]}`)
	if code != http.StatusOK {
		t.Fatalf("extA PUT: %d %s", code, body)
	}

	// Ext B sees only its own (absent) layer, never Ext A's.
	code, body = gradingLayerHTTP(t, h, extB, http.MethodGet, "/api/v1/push/grading/layer", "")
	if code != http.StatusOK {
		t.Fatalf("extB GET: %d %s", code, body)
	}
	var gotB struct {
		Layer *PushGradingLayer `json:"layer"`
	}
	if err := json.Unmarshal(body, &gotB); err != nil || gotB.Layer != nil {
		t.Fatalf("extB saw another extension's layer: %s (%v)", body, err)
	}

	// Ext B cannot read, overwrite, delete, or preview Ext A's layer or another manager.
	denied := []struct {
		name, method, target, body, wantCode string
	}{
		{"foreign GET query ext", http.MethodGet, "/api/v1/push/grading/layer?extension=ext-a", "", codeLayerOwnershipDenied},
		{"foreign GET query mgr", http.MethodGet, "/api/v1/push/grading/layer?manager=manager-b", "", codeOtherManager},
		{"foreign PUT query ext", http.MethodPut, "/api/v1/push/grading/layer?extension=ext-a", `{"grading_enabled":false}`, codeLayerOwnershipDenied},
		{"foreign PUT body ext", http.MethodPut, "/api/v1/push/grading/layer", `{"extension":"ext-a","grading_enabled":false}`, codeLayerOwnershipDenied},
		{"foreign PUT body mgr", http.MethodPut, "/api/v1/push/grading/layer", `{"manager":"manager-b","grading_enabled":false}`, codeOtherManager},
		{"foreign DELETE query ext", http.MethodDelete, "/api/v1/push/grading/layer?extension=ext-a", "", codeLayerOwnershipDenied},
		{"foreign DELETE query mgr", http.MethodDelete, "/api/v1/push/grading/layer?manager=manager-b", "", codeOtherManager},
		{"foreign preview draft ext", http.MethodPost, "/api/v1/push/grading/preview", `{"notification":{"level":"P1"},"layer":{"extension":"ext-a","enabled":false}}`, codeLayerOwnershipDenied},
		{"foreign preview query ext", http.MethodPost, "/api/v1/push/grading/preview?extension=ext-a", `{"notification":{"level":"P1"}}`, codeLayerOwnershipDenied},
		{"foreign preview notif mgr", http.MethodPost, "/api/v1/push/grading/preview", `{"notification":{"level":"P1","manager":"manager-b"}}`, codeOtherManager},
	}
	for _, tc := range denied {
		code, raw := gradingLayerHTTP(t, h, extB, tc.method, tc.target, tc.body)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		if code != http.StatusForbidden || out["code"] != tc.wantCode {
			t.Fatalf("%s: got %d %s, want 403 code=%q", tc.name, code, raw, tc.wantCode)
		}
	}

	// Extension principal without an extension ID is refused.
	noExt := &principal{Role: roleExtension, Manager: assistantwire.AssistantID}
	if code, raw := gradingLayerHTTP(t, h, noExt, http.MethodPut, "/api/v1/push/grading/layer", `{"grading_enabled":true}`); code != http.StatusForbidden {
		t.Fatalf("empty extension identity: got %d %s, want 403", code, raw)
	}

	// Ext B deleting its own (non-existent) layer leaves Ext A's layer untouched.
	code, body = gradingLayerHTTP(t, h, extB, http.MethodDelete, "/api/v1/push/grading/layer", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"removed":false`) {
		t.Fatalf("extB DELETE own empty: %d %s", code, body)
	}
	if _, ok := h.push.gradingLayer(gradingLayerKey{Manager: assistantwire.AssistantID, Extension: "ext-a"}); !ok {
		t.Fatal("extB DELETE removed extA's layer")
	}

	// Same extension ID on manager-b owns a completely separate layer and cannot affect assistant's pushes.
	if code, raw := gradingLayerHTTP(t, h, extOtherMgr, http.MethodPut, "/api/v1/push/grading/layer",
		`{"rules":[{"when":{"levels":["P0"]},"action":"hold","reason":"ext_mgr_b_hold"}]}`); code != http.StatusOK {
		t.Fatalf("extOtherMgr PUT: %d %s", code, raw)
	}
	attempts := gradingAttempts(h)
	// Assistant P0 delivers normally (manager-b's hold rule does not cross managers).
	if _, err := h.deliverPushFrom(assistantwire.AssistantID, gradingNotification(assistantwire.LevelP0, "k-asst", "asst p0")); err != nil {
		t.Fatal(err)
	}
	// Manager-b P0 is held by manager-b's layer.
	outB, err := h.deliverPushFrom("manager-b", gradingNotification(assistantwire.LevelP0, "k-b", "b p0"))
	if err != nil {
		t.Fatal(err)
	}
	if heldOf(outB) != "ext_mgr_b_hold" {
		t.Fatalf("manager-b P0 held = %q, want ext_mgr_b_hold", heldOf(outB))
	}
	if *attempts != 1 {
		t.Fatalf("got %d push attempts, want 1 (assistant only)", *attempts)
	}
	f.calls(t, 1)

	// Full credential lists both layers sorted by manager, extension.
	code, body = gradingLayerHTTP(t, h, nil, http.MethodGet, "/api/v1/push/grading/layer", "")
	if code != http.StatusOK {
		t.Fatalf("owner GET all: %d %s", code, body)
	}
	var all struct {
		Layers []PushGradingLayer `json:"layers"`
	}
	if err := json.Unmarshal(body, &all); err != nil || len(all.Layers) != 2 ||
		all.Layers[0].Manager != assistantwire.AssistantID || all.Layers[1].Manager != "manager-b" {
		t.Fatalf("owner GET all = %s (%v), want 2 sorted layers", body, err)
	}
}

// Layer updates persist durably in hub_settings across a pusher reload, and failed writes leave memory unchanged.
func TestPushGradingLayerPersistenceAcrossReload(t *testing.T) {
	h, _ := gradingHub(t)
	store := h.push.store.(*gradingStore)
	extA := extGradingPrincipal(assistantwire.AssistantID, "ext-a")

	code, body := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer",
		`{"grading_enabled":true,"dedup_window_seconds":1800,"rules":[{"id":"r1","when":{"related_key_prefix":"build:"},"level":"P2","reason":"ext_build_quiet"}]}`)
	if code != http.StatusOK {
		t.Fatalf("PUT v1: %d %s", code, body)
	}

	// Reload into a brand-new Pusher reading the same store.
	restarted := newPusher(t.TempDir(), store, t.Logf)
	got, ok := restarted.gradingLayer(gradingLayerKey{Manager: assistantwire.AssistantID, Extension: "ext-a"})
	if !ok || !got.Enabled || got.GradingEnabled == nil || !*got.GradingEnabled ||
		got.DedupWindowSeconds == nil || *got.DedupWindowSeconds != 1800 ||
		len(got.Rules) != 1 || got.Rules[0].ID != "r1" || got.Rules[0].Reason != "ext_build_quiet" {
		t.Fatalf("reloaded layer = %+v (ok=%v)", got, ok)
	}
	// Verify the reloaded pusher actually applies the persisted layer.
	allow, reason := restarted.applyPushGrading(pushPolicy{
		manager: assistantwire.AssistantID, level: assistantwire.LevelP1, relatedKey: "build:42",
	})
	if allow || reason != "ext_build_quiet" {
		t.Fatalf("reloaded applyPushGrading = allow:%v reason:%q, want false ext_build_quiet", allow, reason)
	}

	// Update the layer and verify the replacement persists.
	code, body = gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer",
		`{"rules":[{"id":"r2","when":{"related_key_prefix":"deploy:"},"action":"hold","reason":"ext_deploy_hold"}]}`)
	if code != http.StatusOK {
		t.Fatalf("PUT v2: %d %s", code, body)
	}
	restarted2 := newPusher(t.TempDir(), store, t.Logf)
	got2, ok := restarted2.gradingLayer(gradingLayerKey{Manager: assistantwire.AssistantID, Extension: "ext-a"})
	if !ok || got2.GradingEnabled != nil || len(got2.Rules) != 1 || got2.Rules[0].ID != "r2" {
		t.Fatalf("reloaded v2 = %+v (ok=%v)", got2, ok)
	}

	// A store write failure returns 503 and does NOT mutate in-memory state.
	h.push.store = &flakyPutStore{gradingStore: store, failsLeft: 1}
	code, body = gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer", `{"grading_enabled":true}`)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("PUT with failing store: %d %s, want 503", code, body)
	}
	cur, ok := h.push.gradingLayer(gradingLayerKey{Manager: assistantwire.AssistantID, Extension: "ext-a"})
	if !ok || cur.GradingEnabled != nil || len(cur.Rules) != 1 || cur.Rules[0].ID != "r2" {
		t.Fatalf("in-memory layer changed after failed store write: %+v", cur)
	}
	h.push.store = store

	// Delete persists across reload too.
	if code, body = gradingLayerHTTP(t, h, extA, http.MethodDelete, "/api/v1/push/grading/layer", ""); code != http.StatusOK {
		t.Fatalf("DELETE: %d %s", code, body)
	}
	restarted3 := newPusher(t.TempDir(), store, t.Logf)
	if _, ok := restarted3.gradingLayer(gradingLayerKey{Manager: assistantwire.AssistantID, Extension: "ext-a"}); ok {
		t.Fatal("deleted layer returned after reload")
	}
}

// Removing a layer (by the extension or by the owner) OR disabling / uninstalling the extension
// (via extwire.EventReport) cleanly restores the owner's settings both when hub.ln_mode is off and when it is on.
func TestPushGradingLayerRemovalAndExtensionTeardownRestoresOwnerSettings(t *testing.T) {
	t.Run("owner switch off restored on layer delete", func(t *testing.T) {
		h, f := gradingHub(t)
		attempts := gradingAttempts(h)
		extA := extGradingPrincipal(assistantwire.AssistantID, "ext-a")

		// Default (owner switch off): P1 delivers.
		if err := h.deliverPush(gradingNotification(assistantwire.LevelP1, "k1", "before")); err != nil {
			t.Fatal(err)
		}
		if *attempts != 1 {
			t.Fatalf("before layer: %d attempts, want 1", *attempts)
		}

		// Extension adds a layer activating grading: P1 is held, while owner switch stays false.
		if code, body := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer", `{"grading_enabled":true}`); code != http.StatusOK {
			t.Fatalf("PUT: %d %s", code, body)
		}
		if h.push.pushGradingOn() {
			t.Fatal("extension layer flipped owner's hub.ln_mode switch")
		}
		if code, body := gradingLayerHTTP(t, h, nil, http.MethodGet, "/api/v1/push/grading", ""); code != http.StatusOK || string(body) != "{\"enabled\":false}\n" {
			t.Fatalf("GET /api/v1/push/grading = %d %s, want enabled:false", code, body)
		}
		out, err := h.deliverPushFrom(assistantwire.AssistantID, gradingNotification(assistantwire.LevelP1, "k2", "during"))
		if err != nil || heldOf(out) != interceptReasonP1 || *attempts != 1 {
			t.Fatalf("during layer: out=%+v err=%v attempts=%d", out, err, *attempts)
		}

		// Delete layer -> P1 immediately delivers again.
		if code, body := gradingLayerHTTP(t, h, extA, http.MethodDelete, "/api/v1/push/grading/layer", ""); code != http.StatusOK {
			t.Fatalf("DELETE: %d %s", code, body)
		}
		out, err = h.deliverPushFrom(assistantwire.AssistantID, gradingNotification(assistantwire.LevelP1, "k3", "after"))
		if err != nil || heldOf(out) != "" || *attempts != 2 {
			t.Fatalf("after delete: out=%+v err=%v attempts=%d, want delivered", out, err, *attempts)
		}
		f.calls(t, 2)
	})

	t.Run("owner switch on restored on owner delete", func(t *testing.T) {
		h, f := gradingHub(t)
		attempts := gradingAttempts(h)
		if err := h.push.setPushGrading(context.Background(), true); err != nil {
			t.Fatal(err)
		}
		extA := extGradingPrincipal(assistantwire.AssistantID, "ext-a")

		// With owner switch on and no layer, vip P1 is held.
		out, err := h.deliverPushFrom(assistantwire.AssistantID, gradingNotification(assistantwire.LevelP1, "vip:1", "held before"))
		if err != nil || heldOf(out) != interceptReasonP1 {
			t.Fatalf("before layer: out=%+v err=%v", out, err)
		}

		// Extension promotes vip: notifications to P0.
		if code, body := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer",
			`{"rules":[{"id":"vip","when":{"related_key_prefix":"vip:"},"level":"P0"}]}`); code != http.StatusOK {
			t.Fatalf("PUT: %d %s", code, body)
		}
		out, err = h.deliverPushFrom(assistantwire.AssistantID, gradingNotification(assistantwire.LevelP1, "vip:2", "promoted"))
		if err != nil || heldOf(out) != "" || *attempts != 1 {
			t.Fatalf("during vip layer: out=%+v err=%v attempts=%d, want delivered", out, err, *attempts)
		}
		// Unmatched P1 is still held by owner's switch.
		out, err = h.deliverPushFrom(assistantwire.AssistantID, gradingNotification(assistantwire.LevelP1, "other:1", "unmatched"))
		if err != nil || heldOf(out) != interceptReasonP1 || *attempts != 1 {
			t.Fatalf("unmatched under owner on: out=%+v err=%v attempts=%d", out, err, *attempts)
		}

		// Owner removes the extension's layer with full credentials -> owner's switch resumes holding vip: P1.
		if code, body := gradingLayerHTTP(t, h, nil, http.MethodDelete, "/api/v1/push/grading/layer?extension=ext-a", ""); code != http.StatusOK {
			t.Fatalf("owner DELETE: %d %s", code, body)
		}
		out, err = h.deliverPushFrom(assistantwire.AssistantID, gradingNotification(assistantwire.LevelP1, "vip:3", "after owner delete"))
		if err != nil || heldOf(out) != interceptReasonP1 || *attempts != 1 {
			t.Fatalf("after owner delete: out=%+v err=%v attempts=%d, want held by owner", out, err, *attempts)
		}
		f.calls(t, 1)
	})

	t.Run("extension disable or uninstall via EventReport prunes layer durably", func(t *testing.T) {
		h, f := gradingHub(t)
		store := h.push.store.(*gradingStore)
		attempts := gradingAttempts(h)
		extA := extGradingPrincipal(assistantwire.AssistantID, "ext-a")
		extB := extGradingPrincipal(assistantwire.AssistantID, "ext-b")

		if code, body := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer", `{"grading_enabled":true}`); code != http.StatusOK {
			t.Fatalf("extA PUT: %d %s", code, body)
		}
		if code, body := gradingLayerHTTP(t, h, extB, http.MethodPut, "/api/v1/push/grading/layer",
			`{"rules":[{"when":{"related_key_prefix":"b:"},"action":"silent"}]}`); code != http.StatusOK {
			t.Fatalf("extB PUT: %d %s", code, body)
		}

		// An older manager report without PushGradingDeclared leaves both layers intact.
		rawOld, _ := json.Marshal(extwire.Report{EventsDeclared: true})
		busEvents[extwire.EventReport](h, nil, rawOld)
		if len(h.push.listGradingLayers(assistantwire.AssistantID)) != 2 {
			t.Fatal("legacy report without PushGradingDeclared pruned layers")
		}

		// Report with ext-a disabled/uninstalled (only ext-b active) prunes ext-a and keeps ext-b.
		rawDropA, _ := json.Marshal(extwire.Report{PushGradingDeclared: true, PushGradingExtensions: []string{"ext-b"}})
		busEvents[extwire.EventReport](h, nil, rawDropA)
		if _, ok := h.push.gradingLayer(gradingLayerKey{Manager: assistantwire.AssistantID, Extension: "ext-a"}); ok {
			t.Fatal("ext-a layer still present after EventReport dropped ext-a")
		}
		if _, ok := h.push.gradingLayer(gradingLayerKey{Manager: assistantwire.AssistantID, Extension: "ext-b"}); !ok {
			t.Fatal("ext-b layer was pruned while ext-b is still active")
		}
		// Ordinary P1 delivers now that ext-a's grading_enabled layer is gone, while b: is still silenced by ext-b.
		out, err := h.deliverPushFrom(assistantwire.AssistantID, gradingNotification(assistantwire.LevelP1, "plain:1", "plain"))
		if err != nil || heldOf(out) != "" || *attempts != 1 {
			t.Fatalf("after ext-a gone: plain P1 out=%+v err=%v attempts=%d", out, err, *attempts)
		}
		out, err = h.deliverPushFrom(assistantwire.AssistantID, gradingNotification(assistantwire.LevelP1, "b:1", "from b"))
		if err != nil || heldOf(out) != interceptReasonP2 || *attempts != 1 {
			t.Fatalf("ext-b rule should still hold b:1: out=%+v err=%v", out, err)
		}

		// Wait for async flush and verify persistence across restart.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if !h.push.waitGradingLayersSaved(ctx) {
			t.Fatal("reconciled layer prune was not flushed to store")
		}
		restarted := newPusher(t.TempDir(), store, t.Logf)
		if _, ok := restarted.gradingLayer(gradingLayerKey{Manager: assistantwire.AssistantID, Extension: "ext-a"}); ok {
			t.Fatal("pruned ext-a layer returned after reload")
		}
		if _, ok := restarted.gradingLayer(gradingLayerKey{Manager: assistantwire.AssistantID, Extension: "ext-b"}); !ok {
			t.Fatal("active ext-b layer missing after reload")
		}

		// Drop ext-b too -> owner's settings (switch off) completely restored.
		rawDropAll, _ := json.Marshal(extwire.Report{PushGradingDeclared: true})
		busEvents[extwire.EventReport](h, nil, rawDropAll)
		if !h.push.waitGradingLayersSaved(ctx) {
			t.Fatal("final layer prune was not flushed")
		}
		out, err = h.deliverPushFrom(assistantwire.AssistantID, gradingNotification(assistantwire.LevelP1, "b:2", "after all gone"))
		if err != nil || heldOf(out) != "" || *attempts != 2 {
			t.Fatalf("after all extensions gone: out=%+v err=%v attempts=%d", out, err, *attempts)
		}
		f.calls(t, 2)
	})
}

// Combination safety: system alerts always pass, active owner attention modes are never weakened by extension layers,
// multiple extension layers compose deterministically (stricter wins), disabled layers are inert, and custom dedup works.
func TestPushGradingLayerCombinesSafelyWithOwnerControls(t *testing.T) {
	t.Run("system alerts bypass catch-all silent layer", func(t *testing.T) {
		h, f := gradingHub(t)
		attempts := gradingAttempts(h)
		extA := extGradingPrincipal(assistantwire.AssistantID, "ext-a")
		if code, body := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer",
			`{"grading_enabled":true,"rules":[{"when":{},"action":"silent"}]}`); code != http.StatusOK {
			t.Fatalf("PUT: %d %s", code, body)
		}
		h.push.sendLocalized(func(i18n.Lang) map[string]any {
			return map[string]any{"aps": map[string]any{"alert": map[string]any{"title": "Computer joined"}, "sound": "default"}}
		}, "", nil)
		if *attempts != 1 {
			t.Fatalf("system alert attempts = %d, want 1", *attempts)
		}
		f.calls(t, 1)
	})

	t.Run("owner attention mode is never weakened by extension allow or P0 promotion", func(t *testing.T) {
		h, f := gradingHub(t)
		attempts := gradingAttempts(h)
		extA := extGradingPrincipal(assistantwire.AssistantID, "ext-a")
		if code, body := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer",
			`{"rules":[{"id":"force-allow","when":{"related_key_prefix":"vip:"},"level":"P0","action":"allow"},{"id":"hold-focused","when":{"related_key_prefix":"noisy:"},"action":"hold","reason":"ext_noisy"}]}`); code != http.StatusOK {
			t.Fatalf("PUT: %d %s", code, body)
		}
		// Immersive focus on qa-plain: out-of-focus vip push is STILL held by immersive mode.
		setAttention(h, assistantwire.AttentionImmersive, time.Hour, &assistantwire.AttentionFocus{Track: "qa-plain"})
		nOut := trackNotification(assistantwire.LevelP1, "qa-other", "", "out of focus vip")
		nOut.RelatedKey = "vip:1"
		out, err := h.deliverPushFrom(assistantwire.AssistantID, nOut)
		if err != nil || heldOf(out) != assistantwire.AttentionReasonImmersiveHeld || *attempts != 0 {
			t.Fatalf("immersive out-of-focus vip: out=%+v err=%v attempts=%d", out, err, *attempts)
		}
		// In-focus P0 that extension explicitly holds IS held (stricter wins).
		nNoisy := trackNotification(assistantwire.LevelP0, "qa-plain", "", "in focus noisy")
		nNoisy.RelatedKey = "noisy:1"
		out, err = h.deliverPushFrom(assistantwire.AssistantID, nNoisy)
		if err != nil || heldOf(out) != "ext_noisy" || *attempts != 0 {
			t.Fatalf("immersive in-focus extension hold: out=%+v err=%v attempts=%d", out, err, *attempts)
		}
		// Quiet mode: even an allowed P0 is STILL hushed (no sound, passive).
		setAttention(h, assistantwire.AttentionQuiet, time.Hour, nil)
		nQuiet := gradingNotification(assistantwire.LevelP0, "vip:quiet", "quiet p0")
		if err := h.deliverPush(nQuiet); err != nil {
			t.Fatal(err)
		}
		calls := f.calls(t, 1)
		if aps := apsOf(t, calls[0]); aps["sound"] != nil || aps["interruption-level"] != "passive" {
			t.Fatalf("quiet mode P0 was not hushed: %+v", aps)
		}
	})

	t.Run("multiple layers compose deterministically with stricter winning", func(t *testing.T) {
		h, _ := gradingHub(t)
		extAllow := extGradingPrincipal(assistantwire.AssistantID, "ext-alpha")
		extHold := extGradingPrincipal(assistantwire.AssistantID, "ext-beta")
		extSilent := extGradingPrincipal(assistantwire.AssistantID, "ext-gamma")

		if code, body := gradingLayerHTTP(t, h, extAllow, http.MethodPut, "/api/v1/push/grading/layer",
			`{"rules":[{"id":"allow-pr","when":{"related_key_prefix":"pr:"},"action":"allow"}]}`); code != http.StatusOK {
			t.Fatalf("PUT alpha: %d %s", code, body)
		}
		if code, body := gradingLayerHTTP(t, h, extHold, http.MethodPut, "/api/v1/push/grading/layer",
			`{"rules":[{"id":"hold-pr","when":{"related_key_prefix":"pr:"},"action":"hold","reason":"ext_hold_pr"}]}`); code != http.StatusOK {
			t.Fatalf("PUT beta: %d %s", code, body)
		}
		dec := h.push.previewPushGrading(pushPolicy{
			kind: assistantwire.NotifyPush, manager: assistantwire.AssistantID, level: assistantwire.LevelP1, relatedKey: "pr:9",
		}, nil, false)
		if dec.Allow || dec.Action != pushGradingActionHold || dec.Reason != "ext_hold_pr" || dec.MatchedLayer == nil || dec.MatchedLayer.Extension != "ext-beta" {
			t.Fatalf("allow + hold composition = %+v, want hold by ext-beta", dec)
		}

		// Adding a silent rule beats hold.
		if code, body := gradingLayerHTTP(t, h, extSilent, http.MethodPut, "/api/v1/push/grading/layer",
			`{"rules":[{"id":"silent-pr","when":{"related_key_prefix":"pr:"},"action":"silent","reason":"ext_silent_pr"}]}`); code != http.StatusOK {
			t.Fatalf("PUT gamma: %d %s", code, body)
		}
		dec2 := h.push.previewPushGrading(pushPolicy{
			kind: assistantwire.NotifyPush, manager: assistantwire.AssistantID, level: assistantwire.LevelP1, relatedKey: "pr:9",
		}, nil, false)
		if dec2.Allow || dec2.Action != pushGradingActionSilent || dec2.Reason != "ext_silent_pr" || dec2.MatchedLayer == nil || dec2.MatchedLayer.Extension != "ext-gamma" {
			t.Fatalf("allow + hold + silent composition = %+v, want silent by ext-gamma", dec2)
		}
	})

	t.Run("disabled layer is inert and custom dedup window works without suppressing P0", func(t *testing.T) {
		h, f := gradingHub(t)
		attempts := gradingAttempts(h)
		extA := extGradingPrincipal(assistantwire.AssistantID, "ext-a")

		// Disabled layer does not filter pushes.
		if code, body := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer",
			`{"enabled":false,"grading_enabled":true}`); code != http.StatusOK {
			t.Fatalf("PUT disabled: %d %s", code, body)
		}
		if err := h.deliverPush(gradingNotification(assistantwire.LevelP1, "k-dis", "disabled layer")); err != nil {
			t.Fatal(err)
		}
		if *attempts != 1 {
			t.Fatalf("disabled layer held push: attempts=%d", *attempts)
		}

		// Custom 20h dedup window (72000s): after 15h (>12h default, <20h layer), non-P0 repeat is still deduped,
		// while P0 still interrupts.
		if code, body := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer",
			`{"enabled":true,"grading_enabled":true,"dedup_window_seconds":72000}`); code != http.StatusOK {
			t.Fatalf("PUT 20h dedup: %d %s", code, body)
		}
		base := h.push.now()
		if err := h.deliverPush(gradingNotification(assistantwire.LevelP0, "pr:dedup", "first p0")); err != nil {
			t.Fatal(err)
		}
		moveGradingClock(h, base.Add(15*time.Hour))
		out, err := h.deliverPushFrom(assistantwire.AssistantID, gradingNotification(assistantwire.LevelP1, "pr:dedup", "repeat p1"))
		if err != nil || heldOf(out) != interceptReasonDedup {
			t.Fatalf("15h repeat under 20h layer: out=%+v err=%v, want ln_dedup_12h", out, err)
		}
		// P0 with the same key still interrupts.
		out, err = h.deliverPushFrom(assistantwire.AssistantID, gradingNotification(assistantwire.LevelP0, "pr:dedup", "second p0"))
		if err != nil || heldOf(out) != "" || *attempts != 3 {
			t.Fatalf("P0 under 20h dedup: out=%+v err=%v attempts=%d, want delivered", out, err, *attempts)
		}
		f.calls(t, 3)
	})

	t.Run("HTTP notify matches alert kind and calling extension", func(t *testing.T) {
		h, _ := gradingHub(t)
		extA := extGradingPrincipal(assistantwire.AssistantID, "ext-a")
		if code, body := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer",
			`{"rules":[{"id":"mute-ext-bot","when":{"kinds":["alert"],"extension":"noisy-bot"},"action":"silent","reason":"ext_bot_muted"}]}`); code != http.StatusOK {
			t.Fatalf("PUT: %d %s", code, body)
		}
		res, err := h.deliverNotify(notifyRequest{Title: "Ping", Body: "hello", Level: levelActive, PushLevel: assistantwire.LevelP0}, "", "noisy-bot", assistantwire.AssistantID)
		if err != nil || res["sent"] != false || res["held"] != true || res["reason"] != "ext_bot_muted" {
			t.Fatalf("deliverNotify = %+v, err=%v; want held ext_bot_muted", res, err)
		}
	})
}

// POST /api/v1/push/grading/preview returns the combined decision (including draft layers, dedup hits, main
// conversation promotion, and active attention modes) with ZERO side effects on any notification, dedup,
// intercept, delivered-push, layer, WebSocket, or store state.
func TestPushGradingPreviewMatchesDeliveryAndWritesZeroState(t *testing.T) {
	h, f := gradingHub(t)
	h.clients = map[*client]struct{}{}
	mainGradingProfile(h)
	store := h.push.store.(*gradingStore)
	wsClient := &client{device: "web-preview", out: make(chan []byte, 8), meta: clientMeta{features: map[string]bool{"ws_notify": true}}}
	addTestClient(h, wsClient)
	extA := extGradingPrincipal(assistantwire.AssistantID, "ext-a")

	// Seed one real P0 push (with owner switch on) so dedup state & sent counts are non-empty before previewing.
	if err := h.push.setPushGrading(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if err := h.deliverPush(gradingNotification(assistantwire.LevelP0, "pr:existing", "real p0")); err != nil {
		t.Fatal(err)
	}
	f.calls(t, 1)
	// Drain the WS notify frame emitted by the initial real push before snapshotting.
	<-wsClient.out
	if !h.push.waitInterceptsSaved(2 * time.Second) {
		t.Fatal("initial push state did not save")
	}
	if code, body := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer",
		`{"rules":[{"id":"vip-p0","when":{"related_key_prefix":"vip:"},"level":"P0"}]}`); code != http.StatusOK {
		t.Fatalf("PUT layer: %d %s", code, body)
	}

	// Snapshot every piece of mutable state before running previews.
	snapshotState := func() (intercepts []heldPushRecord, stats []pushDayStats, lastAlert map[string]time.Time, layers []PushGradingLayer, storeVals map[string][]byte, sentRows int) {
		intercepts = h.push.heldPushesView("", 0)
		stats = h.push.pushDailyStats(h.push.now().Add(-24*time.Hour), "")
		h.push.gradingMu.Lock()
		lastAlert = maps.Clone(h.push.gradingLastAlert)
		h.push.gradingMu.Unlock()
		layers = h.push.listGradingLayers("")
		store.mu.Lock()
		storeVals = make(map[string][]byte, len(store.vals))
		for k, v := range store.vals {
			storeVals[k] = append([]byte(nil), v...)
		}
		store.mu.Unlock()
		sentRows = len(store.sentTable())
		return
	}
	beforeIntercepts, beforeStats, beforeLastAlert, beforeLayers, beforeStore, beforeSentRows := snapshotState()
	attempts := gradingAttempts(h)

	preview := func(p *principal, body string) PushGradingDecision {
		t.Helper()
		code, raw := gradingLayerHTTP(t, h, p, http.MethodPost, "/api/v1/push/grading/preview", body)
		if code != http.StatusOK {
			t.Fatalf("preview %s: got %d %s", body, code, raw)
		}
		var dec PushGradingDecision
		if err := json.Unmarshal(raw, &dec); err != nil {
			t.Fatalf("decode preview %s: %v", raw, err)
		}
		return dec
	}

	// 1. Persisted layer promotes vip: P1 to P0 -> allowed, source=extension, matched_layer=ext-a/vip-p0.
	d1 := preview(extA, `{"notification":{"kind":"assistant","level":"P1","related_key":"vip:urgent"}}`)
	if !d1.Allow || d1.Held || d1.EffectiveLevel != assistantwire.LevelP0 || d1.Source != pushGradingSourceExtension ||
		d1.MatchedLayer == nil || d1.MatchedLayer.Extension != "ext-a" || d1.MatchedLayer.RuleID != "vip-p0" ||
		d1.MatchedLayer.RuleIndex == nil || *d1.MatchedLayer.RuleIndex != 0 {
		t.Fatalf("preview 1 (persisted vip) = %+v", d1)
	}

	// 2. Unmatched P1 -> held by owner switch.
	d2 := preview(extA, `{"notification":{"kind":"assistant","level":"P1","related_key":"regular:1"}}`)
	if d2.Allow || !d2.Held || d2.Reason != interceptReasonP1 || d2.Source != pushGradingSourceOwner || d2.MatchedLayer != nil {
		t.Fatalf("preview 2 (unmatched P1) = %+v", d2)
	}

	// 3. Existing P0 key -> dedup hit detected read-only.
	d3 := preview(extA, `{"notification":{"kind":"assistant","level":"P1","related_key":"pr:existing"}}`)
	if d3.Allow || !d3.Held || !d3.DedupHit || d3.Reason != interceptReasonDedup {
		t.Fatalf("preview 3 (dedup hit) = %+v", d3)
	}

	// 4. Previewing a fresh P0 key returns allow=true AND does NOT record it into gradingLastAlert!
	d4 := preview(extA, `{"notification":{"kind":"assistant","level":"P0","related_key":"pr:never-sent"}}`)
	if !d4.Allow || d4.EffectiveLevel != assistantwire.LevelP0 {
		t.Fatalf("preview 4 (fresh P0) = %+v", d4)
	}

	// 5. Main conversation level-less notify_user promotes to P0 in preview just like live delivery.
	d5 := preview(extA, `{"notification":{"kind":"assistant","machine_id":"mac","session_id":"main-conv"}}`)
	if !d5.Allow || d5.InputLevel != assistantwire.LevelP0 || d5.EffectiveLevel != assistantwire.LevelP0 {
		t.Fatalf("preview 5 (main conversation) = %+v", d5)
	}

	// 6. Draft layer overrides ext-a's persisted layer for this preview only (without saving it).
	d6 := preview(extA, `{"notification":{"kind":"assistant","level":"P1","related_key":"vip:urgent"},"layer":{"enabled":false}}`)
	if d6.Allow || !d6.Held || d6.Reason != interceptReasonP1 || d6.Source != pushGradingSourceOwner {
		t.Fatalf("preview 6 (draft disabled layer) = %+v", d6)
	}
	d7 := preview(extA, `{"notification":{"kind":"assistant","level":"P1","related_key":"draft:1"},"layer":{"rules":[{"id":"draft-silent","when":{"related_key_prefix":"draft:"},"action":"silent","reason":"ext_draft_mute"}]}}`)
	if d7.Allow || d7.Action != pushGradingActionSilent || d7.Reason != "ext_draft_mute" || d7.MatchedLayer == nil || d7.MatchedLayer.RuleID != "draft-silent" {
		t.Fatalf("preview 7 (draft silent rule) = %+v", d7)
	}

	// Verify ZERO state mutation after all 7 preview calls.
	afterIntercepts, afterStats, afterLastAlert, afterLayers, afterStore, afterSentRows := snapshotState()
	if *attempts != 0 {
		t.Fatalf("preview triggered %d APNs attempts, want 0", *attempts)
	}
	f.calls(t, 0)
	select {
	case msg := <-wsClient.out:
		t.Fatalf("preview emitted WebSocket frame: %s", msg)
	default:
	}
	if !reflect.DeepEqual(beforeIntercepts, afterIntercepts) {
		t.Fatalf("preview mutated intercepts: before=%+v after=%+v", beforeIntercepts, afterIntercepts)
	}
	if !reflect.DeepEqual(beforeStats, afterStats) {
		t.Fatalf("preview mutated daily stats: before=%+v after=%+v", beforeStats, afterStats)
	}
	if !reflect.DeepEqual(beforeLastAlert, afterLastAlert) {
		t.Fatalf("preview mutated gradingLastAlert: before=%+v after=%+v", beforeLastAlert, afterLastAlert)
	}
	if !reflect.DeepEqual(beforeLayers, afterLayers) {
		t.Fatalf("preview mutated persisted layers: before=%+v after=%+v", beforeLayers, afterLayers)
	}
	if !reflect.DeepEqual(beforeStore, afterStore) || beforeSentRows != afterSentRows {
		t.Fatalf("preview wrote to store: sentRows %d→%d, store changed=%v", beforeSentRows, afterSentRows, !reflect.DeepEqual(beforeStore, afterStore))
	}
}

// Strict input validation for PUT /api/v1/push/grading/layer and POST /api/v1/push/grading/preview.
func TestPushGradingLayerValidation(t *testing.T) {
	h, _ := gradingHub(t)
	extA := extGradingPrincipal(assistantwire.AssistantID, "ext-a")

	badLayers := map[string]string{
		"empty object":             `{}`,
		"not an object":            `[]`,
		"unknown field":            `{"grading_enabled":true,"extra":1}`,
		"duplicate key":            `{"enabled":true,"enabled":false}`,
		"case colliding key":       `{"enabled":true,"Enabled":false}`,
		"trailing json":            `{"enabled":true}{"enabled":false}`,
		"dedup too small":          `{"dedup_window_seconds":10}`,
		"dedup too large":          `{"dedup_window_seconds":100000}`,
		"rule no effect":           `{"rules":[{"when":{"levels":["P1"]}}]}`,
		"bad level":                `{"rules":[{"when":{},"level":"P9"}]}`,
		"bad action":               `{"rules":[{"when":{},"action":"drop"}]}`,
		"reason with allow":        `{"rules":[{"when":{},"action":"allow","reason":"ext_ok"}]}`,
		"bad reason format":        `{"rules":[{"when":{},"action":"hold","reason":"Bad Reason!"}]}`,
		"bad rule id":              `{"rules":[{"id":"bad id","when":{},"action":"hold"}]}`,
		"duplicate rule id":        `{"rules":[{"id":"r1","when":{},"action":"hold"},{"id":"r1","when":{},"action":"silent"}]}`,
		"bad kind":                 `{"rules":[{"when":{"kinds":["sms"]},"action":"hold"}]}`,
		"duplicate kind":           `{"rules":[{"when":{"kinds":["assistant","assistant"]},"action":"hold"}]}`,
		"bad when level":           `{"rules":[{"when":{"levels":["P3"]},"action":"hold"}]}`,
		"duplicate when level":     `{"rules":[{"when":{"levels":["P1","P1"]},"action":"hold"}]}`,
		"bad depth":                `{"rules":[{"when":{"depth":"medium"},"action":"hold"}]}`,
		"control char in prefix":   `{"rules":[{"when":{"related_key_prefix":"a\u0007b"},"action":"hold"}]}`,
		"leading space in session": `{"rules":[{"when":{"session_id":" s1"},"action":"hold"}]}`,
	}
	for name, body := range badLayers {
		code, raw := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer", body)
		if code != http.StatusBadRequest {
			t.Errorf("PUT %s: got %d %s, want 400", name, code, raw)
		}
	}

	// Too many rules (>32).
	many := make([]PushGradingRule, pushGradingLayerMaxRules+1)
	for i := range many {
		many[i] = PushGradingRule{ID: fmt.Sprintf("r%d", i), Action: pushGradingActionHold}
	}
	rawMany, _ := json.Marshal(map[string]any{"rules": many})
	if code, raw := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer", string(rawMany)); code != http.StatusBadRequest {
		t.Fatalf("PUT >max rules: got %d %s, want 400", code, raw)
	}

	// Oversized request body (>16KiB) returns 413.
	huge := `{"rules":[{"id":"r1","when":{"related_key_prefix":"` + strings.Repeat("x", pushGradingLayerBodyLimit+128) + `"},"action":"hold"}]}`
	if code, raw := gradingLayerHTTP(t, h, extA, http.MethodPut, "/api/v1/push/grading/layer", huge); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("PUT oversized: got %d %s, want 413", code, raw)
	}

	// Bad preview inputs.
	badPreviews := map[string]string{
		"unknown field":     `{"notification":{"level":"P1"},"unknown":true}`,
		"bad kind":          `{"notification":{"kind":"bogus"}}`,
		"bad level":         `{"notification":{"level":"P9"}}`,
		"bad depth":         `{"notification":{"depth":"huge"}}`,
		"bad draft layer":   `{"notification":{"level":"P1"},"layer":{"dedup_window_seconds":1}}`,
		"control char key":  `{"notification":{"related_key":"a\u0001b"}}`,
		"duplicate preview": `{"notification":{"level":"P1"},"notification":{"level":"P2"}}`,
	}
	for name, body := range badPreviews {
		code, raw := gradingLayerHTTP(t, h, extA, http.MethodPost, "/api/v1/push/grading/preview", body)
		if code != http.StatusBadRequest {
			t.Errorf("preview %s: got %d %s, want 400", name, code, raw)
		}
	}

	// Layer capacity cap (64 layers).
	for i := range pushGradingLayersMax {
		p := extGradingPrincipal(assistantwire.AssistantID, fmt.Sprintf("ext-%02d", i))
		if code, raw := gradingLayerHTTP(t, h, p, http.MethodPut, "/api/v1/push/grading/layer", `{"grading_enabled":true}`); code != http.StatusOK {
			t.Fatalf("fill layer %d: %d %s", i, code, raw)
		}
	}
	extra := extGradingPrincipal(assistantwire.AssistantID, "ext-overflow")
	if code, raw := gradingLayerHTTP(t, h, extra, http.MethodPut, "/api/v1/push/grading/layer", `{"grading_enabled":true}`); code != http.StatusServiceUnavailable {
		t.Fatalf("overflow layer: got %d %s, want 503", code, raw)
	}
}

// Concurrent PUT/GET/DELETE/preview/applyPushGrading/reconcileGradingLayers must be race-free.
func TestPushGradingLayerConcurrentAccess(t *testing.T) {
	h, _ := gradingHub(t)
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := range 6 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			extName := fmt.Sprintf("ext-%d", id%3)
			p := extGradingPrincipal(assistantwire.AssistantID, extName)
			for j := range 25 {
				switch j % 5 {
				case 0:
					code, raw := gradingLayerHTTP(t, h, p, http.MethodPut, "/api/v1/push/grading/layer",
						`{"grading_enabled":true,"rules":[{"when":{"related_key_prefix":"k:"},"action":"hold"}]}`)
					if code != http.StatusOK {
						errs <- fmt.Errorf("PUT %s: %d %s", extName, code, bytes.TrimSpace(raw))
						return
					}
				case 1:
					code, raw := gradingLayerHTTP(t, h, p, http.MethodGet, "/api/v1/push/grading/layer", "")
					if code != http.StatusOK {
						errs <- fmt.Errorf("GET %s: %d %s", extName, code, bytes.TrimSpace(raw))
						return
					}
				case 2:
					code, raw := gradingLayerHTTP(t, h, p, http.MethodPost, "/api/v1/push/grading/preview",
						`{"notification":{"kind":"assistant","level":"P1","related_key":"k:1"}}`)
					if code != http.StatusOK {
						errs <- fmt.Errorf("preview %s: %d %s", extName, code, bytes.TrimSpace(raw))
						return
					}
				case 3:
					h.push.applyPushGrading(pushPolicy{
						kind: assistantwire.NotifyPush, manager: assistantwire.AssistantID, level: assistantwire.LevelP1, relatedKey: "k:1",
					})
				case 4:
					if id%2 == 0 {
						code, raw := gradingLayerHTTP(t, h, p, http.MethodDelete, "/api/v1/push/grading/layer", "")
						if code != http.StatusOK {
							errs <- fmt.Errorf("DELETE %s: %d %s", extName, code, bytes.TrimSpace(raw))
							return
						}
					} else {
						h.push.reconcileGradingLayers(assistantwire.AssistantID, []string{"ext-0", "ext-2"})
					}
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !h.push.waitGradingLayersSaved(ctx) {
		t.Fatal("background layer flush did not settle")
	}
}
