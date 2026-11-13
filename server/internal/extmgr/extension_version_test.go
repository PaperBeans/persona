package extmgr

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/i18n"
)

// A loaded extension keeps its incompatibility across requests while the displayed
// reason follows each request's language, including after switching it back on.
func TestExtensionHubVersionMessages(t *testing.T) {
	h := newTestManager(t)
	h.build.Number = 202611041200
	h.opts.Lang = func() i18n.Lang { return i18n.En }
	store := newExtensionStore()
	h.store = store
	raw := []byte(`{"manifest_version":1,"id":"example-version","name":"Example Version","version":"1.0.0",
		"engines":{"persona":">=202612010000"},
		"contributes":{"providers":[{"id":"example-provider","name":"Example Provider","base_url":"https://provider.example.com"}]}}`)
	m, err := extension.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(h.cfg.dir, "extensions", "installed", m.ID, m.Version)
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, extension.ManifestFile), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	store.rows[m.ID] = extension.Record{ID: m.ID, Source: extension.SourceInstalled, Origin: "market:example-market/" + m.ID,
		Version: m.Version, Manifest: raw, Enabled: true}
	startTestExtensions(t, h)

	// A fresh, in-memory market avoids downloads while exercising the catalog API.
	marketRaw := []byte(strings.Replace(string(raw), `"version":"1.0.0"`, `"version":"1.1.0"`, 1))
	marketManifest, err := extension.Parse(marketRaw)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ms := h.marketsState()
	ms.mu.Lock()
	ms.loaded = true
	ms.byID = map[string]*marketState{}
	ms.byID["example-market"] = &marketState{
		rec: extension.MarketRecord{ID: "example-market", Spec: "github:example/extensions", Name: "Example Market", FetchedAt: &now},
		index: &extension.MarketIndex{MarketVersion: 1, Extensions: []extension.MarketEntry{{
			Manifest: marketRaw, Parsed: marketManifest, Packages: []extension.MarketPackage{{URL: "https://packages.example.com/extension.tar.gz"}},
		}}},
	}
	ms.mu.Unlock()
	mux := h.routes()
	request := func(t *testing.T, lang i18n.Lang, method, path, body string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Authorization", "Bearer "+h.cfg.Token)
		req.Header.Set("Accept-Language", string(lang))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s %s: %v: %s", method, path, err, w.Body.String())
		}
		return w.Code, out
	}
	for _, tc := range []struct {
		lang i18n.Lang
		want string
	}{
		{i18n.ZhHans, "这个扩展需要更新版本的 hub，请先更新 hub（需要 >=202612010000，现在是 202611041200）"},
		{i18n.En, "This extension needs a newer hub. Update the hub first (required: >=202612010000, current: 202611041200)."},
		{i18n.Ja, "この拡張機能には新しいバージョンのハブが必要です。先にハブを更新してください（必要なビルド：>=202612010000、現在：202611041200）"},
	} {
		t.Run(string(tc.lang), func(t *testing.T) {
			checkUpgrade := func(up map[string]any) {
				t.Helper()
				if up["id"] != m.ID || up["from"] != "1.0.0" || up["to"] != "1.1.0" || up["state"] != upgradeIncompatible || up["detail"] != tc.want {
					t.Fatalf("incompatible upgrade: %v; want %q", up, tc.want)
				}
			}
			checkView := func(code int, view map[string]any) {
				t.Helper()
				if code != http.StatusOK || view["id"] != m.ID || view["status"] != extensionIncompatible || view["error"] != tc.want {
					t.Fatalf("incompatible extension: HTTP %d, %v; want %q", code, view, tc.want)
				}
				upgrade, _ := view["upgrade"].(map[string]any)
				checkUpgrade(upgrade)
			}
			t.Run("list", func(t *testing.T) {
				code, out := request(t, tc.lang, "GET", "/api/v1/extensions", "")
				items, _ := out["extensions"].([]any)
				for _, item := range items {
					view := item.(map[string]any)
					if view["id"] == m.ID {
						checkView(code, view)
						return
					}
				}
				t.Fatalf("loaded extension absent: HTTP %d, %v", code, out)
			})
			t.Run("detail", func(t *testing.T) {
				checkView(request(t, tc.lang, "GET", "/api/v1/extensions/"+m.ID+"/-/", ""))
			})
			t.Run("enable", func(t *testing.T) {
				path := "/api/v1/extensions/" + m.ID + "/-/enabled"
				code, disabled := request(t, tc.lang, "PUT", path, `{"enabled":false}`)
				if code != http.StatusOK || disabled["status"] != extensionDisabled || disabled["error"] != nil {
					t.Fatalf("disabled extension: HTTP %d, %v", code, disabled)
				}
				checkView(request(t, tc.lang, "PUT", path, `{"enabled":true}`))
			})
			t.Run("catalog", func(t *testing.T) {
				code, out := request(t, tc.lang, "GET", "/api/v1/extensions/-/catalog?market=example-market", "")
				items, _ := out["extensions"].([]any)
				if code != http.StatusOK || len(items) != 1 {
					t.Fatalf("catalog: HTTP %d, %v", code, out)
				}
				if item := items[0].(map[string]any); item["problem"] != tc.want {
					t.Fatalf("catalog problem = %v; want %q", item["problem"], tc.want)
				}
			})
			t.Run("upgrades", func(t *testing.T) {
				code, out := request(t, tc.lang, "GET", "/api/v1/extensions/-/upgrades", "")
				items, _ := out["upgrades"].([]any)
				if code != http.StatusOK || len(items) != 1 {
					t.Fatalf("upgrades: HTTP %d, %v", code, out)
				}
				checkUpgrade(items[0].(map[string]any))
			})
			t.Run("preview", func(t *testing.T) {
				body := fmt.Sprintf(`{"source":%q}`, "dir:"+pkg)
				code, out := request(t, tc.lang, "POST", "/api/v1/extensions/-/preview", body)
				if code != http.StatusBadRequest || out["code"] != "invalid_plugin" || out["error"] != tc.want {
					t.Fatalf("preview: HTTP %d, %v; want %q", code, out, tc.want)
				}
			})
			t.Run("async_preview", func(t *testing.T) {
				body := fmt.Sprintf(`{"source":%q,"async":true}`, "dir:"+pkg)
				code, out := request(t, tc.lang, "POST", "/api/v1/extensions/-/preview", body)
				id := opID(t, code, out)
				op := waitOp(t, h, id, opFailed)
				if op.Error != tc.want || op.ErrorCode != "invalid_package" || stepState(op, extension.StepCheck).State != extension.StepFailed {
					t.Fatalf("failed preview: %+v; want %q", op, tc.want)
				}
			})
		})
	}
	// The command line speaks the terminal's language (#5520), here Chinese, not the user language an app set (English).
	t.Run("cli_keeps_terminal_language", func(t *testing.T) {
		srv := httptest.NewServer(mux)
		defer srv.Close()
		client := extensionClient{cfg: &Config{Listen: strings.TrimPrefix(srv.URL, "http://"), Token: h.cfg.Token}, lang: i18n.ZhHans}
		err := client.call(t.Context(), "POST", "/api/v1/extensions/-/preview", map[string]string{"source": "dir:" + pkg}, nil)
		want := "这个扩展需要更新版本的 hub，请先更新 hub（需要 >=202612010000，现在是 202611041200）（HTTP 400）"
		if err == nil || err.Error() != want {
			t.Fatalf("CLI error = %v; want %q", err, want)
		}
		var upgrades upgradesView
		if err := client.call(t.Context(), "GET", "/api/v1/extensions/-/upgrades", nil, &upgrades); err != nil {
			t.Fatal(err)
		}
		if len(upgrades.Upgrades) != 1 || upgrades.Upgrades[0].Detail != strings.TrimSuffix(want, "（HTTP 400）") {
			t.Fatalf("CLI upgrades = %+v", upgrades.Upgrades)
		}
	})
}
