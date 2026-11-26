package extmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extwire"
)

// The manager's report declares every enabled, compatible extension granted push.grading, and drops it immediately
// when the extension is disabled or uninstalled so the hub prunes its persisted grading layer and restores owner
// settings. Legacy hub_api extensions are never included.
func TestReportTracksPushGradingExtensionsAcrossLifecycle(t *testing.T) {
	h, _ := extensionTestHub(t)
	mux := h.routes()
	master := h.cfg.Token

	var mu sync.Mutex
	var reports []extwire.Report
	h.opts.Hub = &recordingHubNotify{onNotify: func(method string, params any) {
		if method != extwire.EventReport {
			return
		}
		raw, err := json.Marshal(params)
		if err != nil {
			return
		}
		var r extwire.Report
		if json.Unmarshal(raw, &r) == nil {
			mu.Lock()
			reports = append(reports, r)
			mu.Unlock()
		}
	}}
	reportCount := func() (int, extwire.Report) {
		mu.Lock()
		defer mu.Unlock()
		if len(reports) == 0 {
			return 0, extwire.Report{}
		}
		return len(reports), reports[len(reports)-1]
	}

	// Before any push.grading extension is installed, PushGradingDeclared is true and the list is empty.
	if r := h.report(); !r.PushGradingDeclared || len(r.PushGradingExtensions) != 0 {
		t.Fatalf("initial report = declared:%v list:%v, want declared:true empty", r.PushGradingDeclared, r.PushGradingExtensions)
	}

	// Install an extension with the named push.grading scope.
	code, pv := call(t, mux, http.MethodPost, "/api/v1/extensions/-/preview", master,
		`{"source": "dir:`+scopedPackage(t, `["push.grading"]`)+`"}`)
	if code != http.StatusOK {
		t.Fatalf("preview: %d %v", code, pv)
	}
	if code, out := call(t, mux, http.MethodPost, "/api/v1/extensions/-/install", master,
		fmt.Sprintf(`{"preview_id": %q, "permissions_digest": %q}`, pv["preview_id"], pv["permissions_digest"])); code != http.StatusOK {
		t.Fatalf("install: %d %v", code, out)
	}
	if r := h.report(); !r.PushGradingDeclared || !slices.Equal(r.PushGradingExtensions, []string{"scoped"}) {
		t.Fatalf("after install report = declared:%v list:%v, want [scoped]", r.PushGradingDeclared, r.PushGradingExtensions)
	}

	// Disabling the extension drops it from PushGradingExtensions and emits a fresh EventReport.
	beforeDisable, _ := reportCount()
	if code, out := call(t, mux, http.MethodPut, "/api/v1/extensions/scoped/-/enabled", master, `{"enabled": false}`); code != http.StatusOK {
		t.Fatalf("disable: %d %v", code, out)
	}
	if r := h.report(); !r.PushGradingDeclared || len(r.PushGradingExtensions) != 0 {
		t.Fatalf("after disable report = declared:%v list:%v, want empty", r.PushGradingDeclared, r.PushGradingExtensions)
	}
	if n, last := reportCount(); n <= beforeDisable || slices.Contains(last.PushGradingExtensions, "scoped") {
		t.Fatalf("disable did not notify hub without scoped: reports=%d last=%+v", n, last)
	}

	// Re-enabling restores it to PushGradingExtensions.
	if code, out := call(t, mux, http.MethodPut, "/api/v1/extensions/scoped/-/enabled", master, `{"enabled": true}`); code != http.StatusOK {
		t.Fatalf("re-enable: %d %v", code, out)
	}
	if r := h.report(); !r.PushGradingDeclared || !slices.Equal(r.PushGradingExtensions, []string{"scoped"}) {
		t.Fatalf("after re-enable report = declared:%v list:%v, want [scoped]", r.PushGradingDeclared, r.PushGradingExtensions)
	}

	// Uninstalling drops it from PushGradingExtensions and emits a fresh EventReport.
	beforeUninstall, _ := reportCount()
	if code, out := call(t, mux, http.MethodDelete, "/api/v1/extensions/scoped/-/", master, ""); code != http.StatusOK {
		t.Fatalf("uninstall: %d %v", code, out)
	}
	if r := h.report(); !r.PushGradingDeclared || len(r.PushGradingExtensions) != 0 {
		t.Fatalf("after uninstall report = declared:%v list:%v, want empty", r.PushGradingDeclared, r.PushGradingExtensions)
	}
	if n, last := reportCount(); n <= beforeUninstall || slices.Contains(last.PushGradingExtensions, "scoped") {
		t.Fatalf("uninstall did not notify hub without scoped: reports=%d last=%+v", n, last)
	}

	// A legacy hub_api extension is NOT reported as push.grading.
	legacyDir := scopedPackageOf(t, `"hub_api": true`)
	code, pv = call(t, mux, http.MethodPost, "/api/v1/extensions/-/preview", master, `{"source": "dir:`+legacyDir+`"}`)
	if code != http.StatusOK {
		t.Fatalf("legacy preview: %d %v", code, pv)
	}
	if code, out := call(t, mux, http.MethodPost, "/api/v1/extensions/-/install", master,
		fmt.Sprintf(`{"preview_id": %q, "permissions_digest": %q}`, pv["preview_id"], pv["permissions_digest"])); code != http.StatusOK {
		t.Fatalf("legacy install: %d %v", code, out)
	}
	if r := h.report(); !r.PushGradingDeclared || len(r.PushGradingExtensions) != 0 {
		t.Fatalf("legacy hub_api extension appeared in push_grading_extensions: %+v", r.PushGradingExtensions)
	}

	// Confirm the scope constant and title are exposed properly.
	if title, ok := extension.ScopeTitle(extension.ScopePushGrading); !ok || title == "" {
		t.Fatalf("ScopeTitle(push.grading) = %q %v", title, ok)
	}
}

type recordingHubNotify struct {
	onNotify func(method string, params any)
}

func (n *recordingHubNotify) Call(context.Context, string, any, any) error { return nil }

func (n *recordingHubNotify) Notify(method string, params any) error {
	if n.onNotify != nil {
		n.onNotify(method, params)
	}
	return nil
}
