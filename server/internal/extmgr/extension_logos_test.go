package extmgr

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/nwverse/persona/server/internal/extension"
)

func testLogoPNG(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewNRGBA(image.Rect(0, 0, 512, 512))); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func logoRequest(handler http.Handler, path, token, etag string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("If-None-Match", etag)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestBundledLogosCatalogInstalledAndAuthentication(t *testing.T) {
	useOfficialMarket(t, "", "")
	h, phone, reader := marketHub(t)
	root := uiPackage(t)
	data := testLogoPNG(t)
	if err := os.Mkdir(filepath.Join(root, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, extension.IconPath), data, 0o600); err != nil {
		t.Fatal(err)
	}
	pkg := tarDir(t, root)
	entry := marketEntry(t, root, "packages/ui.tar.gz", pkg)
	entry["icon"], entry["icon_url"] = extension.IconPath, "https://example.com/obsolete.png"
	gh := &fakeGitHub{token: "ghp_synthetic", files: map[string][]byte{extension.IndexFile: marketIndex(t, "Example", entry), "packages/ui.tar.gz": pkg}}
	var downloads atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/packages/ui.tar.gz") {
			downloads.Add(1)
		}
		gh.ServeHTTP(w, r)
	}))
	defer srv.Close()
	h.extensions.markets.client, h.extensions.markets.githubAPI = srv.Client(), srv.URL
	mux := h.routes()
	code, market := call(t, mux, "POST", "/api/v1/extensions/-/markets", phone, `{"spec":"github:example/plugins","token":"ghp_synthetic"}`)
	if code != 200 {
		t.Fatal(code, market)
	}
	mid := market["id"].(string)
	items := h.catalog("", mid)
	if len(items) != 1 || !strings.HasPrefix(items[0].IconURL, "/api/v1/extensions/-/catalog/") {
		t.Fatal(items)
	}
	path := items[0].IconURL
	if downloads.Load() != 0 {
		t.Fatal("catalog JSON eagerly downloaded artwork packages")
	}
	for _, token := range []string{"", reader} {
		if w := logoRequest(mux, path, token, ""); w.Code == 200 {
			t.Fatal("unauthorized artwork", w.Code)
		}
	}
	direct := http.NewServeMux()
	h.extensionLogoRoutes(direct)
	extRequest := httptest.NewRequest("GET", path, nil)
	extRequest = extRequest.WithContext(withPrincipal(extRequest.Context(), principal{Role: roleFull, Extension: "example-ui"}))
	extResponse := httptest.NewRecorder()
	direct.ServeHTTP(extResponse, extRequest)
	if extResponse.Code != http.StatusForbidden {
		t.Fatal("extension token fetched private catalog artwork")
	}
	var wg sync.WaitGroup
	errs := make(chan string, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := logoRequest(mux, path, phone, "")
			if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), data) {
				errs <- fmt.Sprintf("%d", w.Code)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if downloads.Load() != 1 {
		t.Fatal("package downloaded more than once", downloads.Load())
	}
	w := logoRequest(mux, path, phone, "")
	if w.Header().Get("Content-Type") != "image/png" || w.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(w.Header().Get("Cache-Control"), "private,") {
		t.Fatal(w.Header())
	}
	if cached := logoRequest(mux, path, phone, w.Header().Get("ETag")); cached.Code != 304 || cached.Body.Len() != 0 {
		t.Fatal("etag", cached.Code)
	}
	if wrong := logoRequest(mux, strings.Replace(path, sum(pkg), strings.Repeat("a", 64), 1), phone, ""); wrong.Code != 404 {
		t.Fatal("wrong package identity", wrong.Code)
	}
	code, pv := call(t, mux, "POST", "/api/v1/extensions/-/catalog/preview", phone, fmt.Sprintf(`{"market":%q,"extension":"example-ui"}`, mid))
	if code != 200 {
		t.Fatal(code, pv)
	}
	code, installed := call(t, mux, "POST", "/api/v1/extensions/-/install", phone, fmt.Sprintf(`{"preview_id":%q,"permissions_digest":%q}`, pv["preview_id"], pv["permissions_digest"]))
	if code != 200 {
		t.Fatal(code, installed)
	}
	installedPath, ok := installed["icon_url"].(string)
	if !ok || !strings.HasPrefix(installedPath, "/api/v1/extensions/example-ui/-/icon?v=") {
		t.Fatal("installed logo", installed)
	}
	if got := logoRequest(mux, installedPath, phone, ""); got.Code != 200 || !bytes.Equal(got.Body.Bytes(), data) {
		t.Fatal("installed bytes", got.Code)
	}
	if stale := logoRequest(mux, strings.Replace(installedPath, "v=1.0.0", "v=9.0.0", 1), phone, ""); stale.Code != 404 {
		t.Fatal("stale installed identity", stale.Code)
	}
	p := h.extensions.reg.GetExact("example-ui")
	if err := os.Remove(filepath.Join(p.Dir, extension.IconPath)); err != nil {
		t.Fatal(err)
	}
	if got := logoRequest(mux, installedPath, phone, ""); got.Code != 404 || got.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing installed logo", got.Code)
	}
	if view := h.extensions.view(context.Background(), p); view.IconURL != "" {
		t.Fatal("missing icon has external fallback", view.IconURL)
	}
	ms := h.marketsState()
	ms.mu.Lock()
	ms.byID[mid].rec.Signature.State = extension.SigInvalid
	ms.mu.Unlock()
	if got := logoRequest(mux, path, phone, ""); got.Code != 404 {
		t.Fatal("cached untrusted artwork", got.Code)
	}
	ms.mu.Lock()
	delete(ms.byID, mid)
	ms.mu.Unlock()
	if got := logoRequest(mux, path, phone, ""); got.Code != 404 {
		t.Fatal("removed market artwork", got.Code)
	}
}

func TestBundledIconURLDoesNotFallBackToAnExternalHost(t *testing.T) {
	e := &extension.MarketEntry{Icon: extension.IconPath, IconURL: "https://example.com/legacy.png", Parsed: &extension.Manifest{ID: "example", Version: "1.0.0"}, Packages: []extension.MarketPackage{{Platforms: []string{"other-platform"}, URL: "package.tgz"}}}
	if got := catalogIconURL("market", e); got != "" {
		t.Fatal("unavailable bundled artwork fell back to an external host", got)
	}
}

func TestBundledCatalogLogoRejectsDigestMismatchAndMalformedPNG(t *testing.T) {
	useOfficialMarket(t, "", "")
	h, phone, _ := marketHub(t)
	root := uiPackage(t)
	if err := os.Mkdir(filepath.Join(root, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, extension.IconPath), []byte("not a png"), 0o600); err != nil {
		t.Fatal(err)
	}
	pkg := tarDir(t, root)
	entry := marketEntry(t, root, "packages/ui.tar.gz", pkg)
	entry["icon"] = extension.IconPath
	gh := &fakeGitHub{files: map[string][]byte{extension.IndexFile: marketIndex(t, "Example", entry), "packages/ui.tar.gz": pkg}}
	srv := httptest.NewTLSServer(gh)
	defer srv.Close()
	h.extensions.markets.client, h.extensions.markets.githubAPI = srv.Client(), srv.URL
	mux := h.routes()
	code, market := call(t, mux, "POST", "/api/v1/extensions/-/markets", phone, `{"spec":"github:example/plugins"}`)
	if code != 200 {
		t.Fatal(code, market)
	}
	mid := market["id"].(string)
	path := h.catalog("", mid)[0].IconURL
	if got := logoRequest(mux, path, phone, ""); got.Code != 404 {
		t.Fatal("malformed png", got.Code)
	}
	ms := h.marketsState()
	ms.mu.Lock()
	ms.byID[mid].index.Extensions[0].Packages[0].SHA256 = strings.Repeat("f", 64)
	ms.mu.Unlock()
	path = h.catalog("", mid)[0].IconURL
	if got := logoRequest(mux, path, phone, ""); got.Code != 404 {
		t.Fatal("digest mismatch", got.Code)
	}
}
