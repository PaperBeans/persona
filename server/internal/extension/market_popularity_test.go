package extension

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func popularityPayload(t *testing.T, listKey string, metadata map[string]any, updated string) []byte {
	t.Helper()
	entry := map[string]any{
		"manifest": json.RawMessage(exampleManifest),
		"packages": []any{map[string]any{"url": "notes.tar.gz", "sha256": strings.Repeat("a", 64)}},
	}
	for k, v := range metadata {
		entry[k] = v
	}
	b, err := json.Marshal(map[string]any{"market_version": 1, "name": "Example", listKey: []any{entry}, "downloads_updated_at": updated})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCatalogPopularityOptionalAndSigned(t *testing.T) {
	key, sign := testKey(t)
	for _, listKey := range []string{"extensions", "plugins"} {
		for _, count := range []int64{0, 12345, 1 << 40} {
			payload := popularityPayload(t, listKey, map[string]any{"icon_url": "https://example.com/logo.png", "downloads": count}, "2026-11-11T12:00:00Z")
			raw := sign(payload)
			f, err := ParseCatalog(raw, key)
			if err != nil {
				t.Fatal(err)
			}
			e := f.Index.Extensions[0]
			if e.IconURL != "https://example.com/logo.png" || e.Downloads == nil || *e.Downloads != count || f.Index.DownloadsUpdatedAt != "2026-11-11T12:00:00Z" {
				t.Fatalf("metadata not preserved: %+v %+v", e, f.Index)
			}
			if !bytes.Equal(f.Raw, raw) || !bytes.Equal(f.Payload, payload) {
				t.Fatal("signed bytes changed")
			}
			if _, err := ParseCatalog(bytes.Replace(raw, []byte("logo.png"), []byte("other.png"), 1), key); err == nil {
				t.Fatal("tampered icon accepted")
			}
		}
		f, err := ParseCatalog(sign(popularityPayload(t, listKey, nil, "")), key)
		if err != nil || f.Index.Extensions[0].Downloads != nil || f.Index.Extensions[0].IconURL != "" || f.Index.DownloadsUpdatedAt != "" {
			t.Fatalf("legacy metadata: %v %+v", err, f)
		}
	}
}

func TestCatalogPopularityInvalidValuesDegrade(t *testing.T) {
	for _, icon := range []string{"", "http://example.com/logo.png", "/logo.png", "data:image/png;base64,eA==", "https://user:password@example.com/logo.png", "https:///logo.png", "https://example.com/" + strings.Repeat("a", 2048)} {
		ix, _, err := ParseIndex(popularityPayload(t, "extensions", map[string]any{"icon_url": icon, "downloads": -1}, "not-a-time"))
		if err != nil || len(ix.Extensions) != 1 || ix.Extensions[0].IconURL != "" || ix.Extensions[0].Downloads != nil || ix.DownloadsUpdatedAt != "" {
			t.Fatalf("invalid decoration hid package: %v %+v", err, ix)
		}
	}
}

func TestCatalogPopularityRejectsAmbiguousFields(t *testing.T) {
	key, sign := testKey(t)
	valid := popularityPayload(t, "extensions", map[string]any{"icon_url": "https://example.com/logo.png", "downloads": 20}, "2026-11-11T12:00:00Z")
	for _, pair := range [][2]string{{`"downloads":20`, `"Downloads":20`}, {`"icon_url"`, `"Icon_URL"`}, {`"downloads_updated_at"`, `"Downloads_Updated_At"`}, {`"downloads":20`, `"downloads":20,"downloads":21`}, {`"downloads":20`, `"downloads":1.5`}} {
		if _, err := ParseCatalog(sign(bytes.Replace(valid, []byte(pair[0]), []byte(pair[1]), 1)), key); err == nil {
			t.Fatalf("ambiguous decoration accepted: %v", pair)
		}
	}
}
