package extmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/i18n"
)

func TestOfficialCatalogPopularityOrderAndInstalledMetadata(t *testing.T) {
	h, phone, _ := marketHub(t)
	dir := uiPackage(t)
	pkg := tarDir(t, dir)
	base := marketEntry(t, dir, "packages/ui.tar.gz", pkg)
	base["downloads"], base["icon_url"] = 1200, "https://example.com/logo.png"
	entry := func(id, name string, downloads any) map[string]any {
		e := marketEntry(t, dir, "packages/ui.tar.gz", pkg)
		var m map[string]any
		if err := json.Unmarshal(e["manifest"].(json.RawMessage), &m); err != nil {
			t.Fatal(err)
		}
		m["id"], m["name"] = id, name
		e["manifest"] = m
		if downloads != nil {
			e["downloads"] = downloads
		}
		return e
	}
	entries := []any{entry("z-missing", "Zulu", nil), entry("beta", "Beta", 1200), entry("alpha", "Alpha", 1200), base, entry("a-zero", "Alpha zero", 0), entry("popular", "Popular", 2400)}
	updated := "2026-11-11T12:00:00Z"
	payload, _ := json.Marshal(map[string]any{"market_version": 1, "name": "Example", "downloads_updated_at": updated, "extensions": entries})
	key := newMarketKey(t)
	gh := &fakeGitHub{files: map[string][]byte{extension.IndexFile: key.sign(payload), "packages/ui.tar.gz": pkg}, assets: map[string][]byte{}}
	srv := httptest.NewTLSServer(gh)
	defer srv.Close()
	h.extensions.markets.client, h.extensions.markets.githubAPI = srv.Client(), srv.URL
	useOfficialMarket(t, "github:example/plugins", key.text)
	list := marketList(t, h, phone)
	waitMarketsRead(t, h)
	mid := list[0]["id"].(string)
	want := []string{"popular", "alpha", "beta", "example-ui", "a-zero", "z-missing"}
	assertCatalog := func() {
		t.Helper()
		items := h.catalogIn(i18n.En, "", mid)
		ids := make([]string, len(items))
		for i, it := range items {
			ids[i] = it.ID
		}
		if !slices.Equal(ids, want) {
			t.Fatalf("order: %v, want %v", ids, want)
		}
		if it := items[3]; it.IconURL != "https://example.com/logo.png" || it.Downloads == nil || *it.Downloads != 1200 || it.DownloadsUpdatedAt != updated {
			t.Fatalf("catalog metadata: %+v", it)
		}
		if items[4].Downloads == nil || *items[4].Downloads != 0 || items[5].Downloads != nil {
			t.Fatal("zero and unknown counts conflated")
		}
		if got := h.catalogIn(i18n.En, "alpha", mid); len(got) != 2 || got[0].ID != "alpha" || got[1].ID != "a-zero" {
			t.Fatalf("filtered popularity: %+v", got)
		}
	}
	assertCatalog()
	for _, prefix := range []string{"extensions", "plugins"} {
		code, out := call(t, h.routes(), "GET", "/api/v1/"+prefix+"/-/catalog", phone, "")
		if code != 200 {
			t.Fatalf("wire catalog %s: %d %+v", prefix, code, out)
		}
		items := out[prefix].([]any)
		if items[0].(map[string]any)["downloads"] != float64(2400) || items[3].(map[string]any)["icon_url"] != "https://example.com/logo.png" {
			t.Fatalf("wire metadata: %+v", items)
		}
		if _, ok := items[5].(map[string]any)["downloads"]; ok {
			t.Fatal("unknown count serialized")
		}
	}
	list = marketList(t, h, phone)
	if list[0]["downloads_updated_at"] != updated {
		t.Fatalf("market timestamp: %+v", list)
	}
	code, pv := call(t, h.routes(), "POST", "/api/v1/extensions/-/catalog/preview", phone, fmt.Sprintf(`{"market":%q,"extension":"example-ui"}`, mid))
	if code != 200 {
		t.Fatalf("preview: %d %+v", code, pv)
	}
	code, installed := call(t, h.routes(), "POST", "/api/v1/extensions/-/install", phone, fmt.Sprintf(`{"preview_id":%q,"permissions_digest":%q}`, pv["preview_id"], pv["permissions_digest"]))
	if code != 200 || installed["icon_url"] != "https://example.com/logo.png" || installed["downloads"] != float64(1200) || installed["downloads_updated_at"] != updated {
		t.Fatalf("installed metadata: %d %+v", code, installed)
	}
	for _, path := range []string{"extensions", "plugins"} {
		code, out := call(t, h.routes(), "GET", "/api/v1/"+path+"/example-ui/-/", phone, "")
		if code != 200 || out["icon_url"] != "https://example.com/logo.png" || out["downloads"] != float64(1200) {
			t.Fatalf("installed detail: %d %+v", code, out)
		}
		code, out = call(t, h.routes(), "GET", "/api/v1/"+path, phone, "")
		if code != 200 {
			t.Fatal(code, out)
		}
		found := false
		for _, raw := range out[path].([]any) {
			it := raw.(map[string]any)
			if it["id"] == "example-ui" {
				found = it["icon_url"] == "https://example.com/logo.png" && it["downloads"] == float64(1200)
			}
		}
		if !found {
			t.Fatalf("installed list metadata absent: %+v", out)
		}
	}
	reloadMarkets(h)
	if err := h.marketsLoad(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitMarketsRead(t, h)
	assertCatalog()
	gh.mu.Lock()
	delete(gh.files, extension.IndexFile)
	gh.mu.Unlock()
	if _, err := h.marketRead(context.Background(), mid); err == nil {
		t.Fatal("missing catalog was not a read failure")
	}
	assertCatalog()
	gh.mu.Lock()
	gh.files[extension.IndexFile] = []byte(`{"payload":"{}","signatures":[]}`)
	gh.mu.Unlock()
	if _, err := h.marketRead(context.Background(), mid); err == nil {
		t.Fatal("bad signature accepted")
	}
	v := h.extensions.view(context.Background(), h.extensions.reg.Get("example-ui"))
	if v.IconURL != "" || v.Downloads != nil || v.DownloadsUpdatedAt != "" {
		t.Fatalf("quarantined metadata shown: %+v", v)
	}
}

func TestMarketPopularityLegacyAndNonofficialOrdering(t *testing.T) {
	h, _, _ := marketHub(t)
	useOfficialMarket(t, "github:example/plugins", newMarketKey(t).text)
	makeEntry := func(id, name string) extension.MarketEntry {
		return extension.MarketEntry{Parsed: &extension.Manifest{ID: id, Name: name, Version: "1.0.0"}, Packages: []extension.MarketPackage{{URL: "package.tar.gz"}}}
	}
	entries := []extension.MarketEntry{makeEntry("zulu", "Zulu"), makeEntry("alpha", "Alpha")}
	h.extensions.markets.loaded = true
	h.extensions.markets.byID = map[string]*marketState{
		"own":      {rec: extension.MarketRecord{ID: "own", Spec: "https://example.com/persona-extensions.json", AddedAt: time.Now().Add(-time.Hour)}, index: &extension.MarketIndex{Extensions: entries}},
		"official": {rec: extension.MarketRecord{ID: "official", Spec: "github:example/plugins", Signature: extension.MarketSignature{State: extension.SigSigned, KeyID: keyIDOf(extension.Official.SigningKey)}}, index: &extension.MarketIndex{Extensions: entries}},
	}
	items := h.catalogIn(i18n.En, "", "")
	if len(items) != 4 || items[0].ID != "alpha" || items[1].ID != "zulu" || items[2].ID != "zulu" || items[3].ID != "alpha" {
		t.Fatalf("legacy official name fallback or private order: %+v", items)
	}
	if h.extensions.markets.byID["official"].index.Extensions[0].Parsed.ID != "zulu" {
		t.Fatal("sorting mutated verified catalog")
	}
	count := int64(50)
	private := h.extensions.markets.byID["own"]
	private.index.Extensions[1].IconURL = "https://example.com/private-logo.png"
	private.index.Extensions[1].Downloads = &count
	for _, origin := range []string{"file:/package.tar.gz", "market:missing/alpha", "market:own/other"} {
		v := ExtensionView{}
		p := &extension.Extension{Record: extension.Record{ID: "alpha", Source: extension.SourceInstalled, Origin: origin}}
		h.decorateMarketMetadata(context.Background(), &v, p)
		if v.IconURL != "" || v.Downloads != nil {
			t.Fatalf("metadata leaked across origins: %s %+v", origin, v)
		}
	}
}
