package extmgr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nwverse/persona/server/internal/extension/denohost"
)

// sourceMarket is a market with one source extension (runtime.mode "deno") and one ordinary one, counting the
// package downloads it serves.
func sourceMarket(t *testing.T) (h *Manager, phone, market string, downloads *atomic.Int32) {
	t.Helper()
	h, phone, _ = marketHub(t)
	dir := uiPackage(t)
	pkg := tarDir(t, dir)
	source := json.RawMessage(`{"manifest_version": 1, "id": "example-source", "name": "Example Source", "version": "1.0.0", "engines": {"extension_protocol": 1}, "contributes": {"connectors": [{"id": "examplesource", "name": "Example Source", "capabilities": ["status", "tools"]}]},
		"runtime": {"mode": "deno", "source": {"entry": "src/main.ts", "bundle": "bundle/main.mjs", "sha256": "` + strings.Repeat("0", 64) + `"}}}`)
	downloads = &atomic.Int32{}
	var ix []byte
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".tar.gz") {
			downloads.Add(1)
			_, _ = w.Write(pkg)
			return
		}
		_, _ = w.Write(ix)
	}))
	t.Cleanup(srv.Close)
	ix = marketIndex(t, "Example", marketEntry(t, dir, srv.URL+"/ui.tar.gz", pkg),
		map[string]any{"manifest": source, "packages": []any{map[string]any{"url": srv.URL + "/source.tar.gz", "sha256": sum(pkg), "size": len(pkg)}}})
	h.extensions.markets.client = srv.Client()
	code, out := call(t, h.routes(), "POST", "/api/v1/extensions/-/markets", phone, `{"spec": "`+srv.URL+`/persona-extensions.json"}`)
	if code != 200 {
		t.Fatalf("add market: %d %v", code, out)
	}
	if w, _ := out["warnings"].([]any); len(w) > 0 {
		t.Fatalf("index warnings: %v", w)
	}
	return h, phone, out["id"].(string), downloads
}

func catalogProblems(t *testing.T, h *Manager, phone string) map[string]string {
	t.Helper()
	code, out := call(t, h.routes(), "GET", "/api/v1/extensions/-/catalog", phone, "")
	if code != 200 {
		t.Fatalf("catalog: %d %v", code, out)
	}
	problems := map[string]string{}
	for _, it := range out["extensions"].([]any) {
		m := it.(map[string]any)
		problems[m["id"].(string)], _ = m["problem"].(string)
	}
	return problems
}

// A source extension this hub can't run is marked in the catalog with the reason, before anything is downloaded
// (#5639): off Linux, and on Linux without a pinned source runtime; one with the runtime is offered as usual.
func TestCatalogMarksSourceExtensionsThisHubCantRun(t *testing.T) {
	h, phone, _, _ := sourceMarket(t)
	was := sourceRuntimeOS
	t.Cleanup(func() { sourceRuntimeOS = was })

	sourceRuntimeOS = "darwin"
	p := catalogProblems(t, h, phone)
	if !strings.Contains(p["example-source"], "这台 hub 还不能装") || !strings.Contains(p["example-source"], "Linux") || p["example-ui"] != "" {
		t.Fatalf("macOS: %q", p)
	}
	sourceRuntimeOS = "linux"
	if p = catalogProblems(t, h, phone); !strings.Contains(p["example-source"], "extension_runtime") || p["example-ui"] != "" {
		t.Fatalf("Linux without a runtime: %q", p)
	}
	h.cfg.ExtensionRuntime = &denohost.Config{Path: "/opt/deno/bin/deno", Version: "2.9.6", SHA256: strings.Repeat("a", 64)}
	t.Cleanup(func() { h.cfg.ExtensionRuntime = nil })
	if p = catalogProblems(t, h, phone); p["example-source"] != "" {
		t.Fatalf("Linux with a runtime: %q", p)
	}
}

// Previewing one is refused with the same reason and downloads nothing.
func TestSourcePreviewRefusedBeforeDownload(t *testing.T) {
	h, phone, market, downloads := sourceMarket(t)
	was := sourceRuntimeOS
	sourceRuntimeOS = "darwin"
	t.Cleanup(func() { sourceRuntimeOS = was })
	code, out := call(t, h.routes(), "POST", "/api/v1/extensions/-/catalog/preview", phone, `{"market": "`+market+`", "extension": "example-source"}`)
	if msg, _ := out["error"].(string); code != http.StatusBadRequest || out["code"] != "cant_run_here" || !strings.Contains(msg, "这台 hub 还不能装") {
		t.Fatalf("preview: %d %v", code, out)
	}
	if n := downloads.Load(); n != 0 {
		t.Fatalf("downloaded %d packages for an extension this hub can't run", n)
	}
	// An ordinary one still previews.
	if code, out := call(t, h.routes(), "POST", "/api/v1/extensions/-/catalog/preview", phone, `{"market": "`+market+`", "extension": "example-ui"}`); code != 200 {
		t.Fatalf("ordinary preview: %d %v", code, out)
	}
}
