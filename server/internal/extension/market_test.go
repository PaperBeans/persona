package extension

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseMarket(t *testing.T) {
	for in, want := range map[string]string{
		"github:example/plugins":                      "github:example/plugins",
		"github:example/plugins@release":              "github:example/plugins@release",
		"https://github.com/example/plugins":          "github:example/plugins",
		"https://github.com/example/plugins/tree/dev": "github:example/plugins@dev",
		"https://example.com/plugins":                 "https://example.com/plugins/persona-extensions.json",
		"https://example.com/plugins/":                "https://example.com/plugins/persona-extensions.json",
		"https://example.com/v100/market.json#x":      "https://example.com/v100/market.json",
		" https://example.com ":                       "https://example.com/persona-extensions.json",
	} {
		s, err := ParseMarket(in)
		if err != nil || s.String() != want {
			t.Errorf("%q: %q %v", in, s.String(), err)
		}
	}
	for _, in := range []string{"http://example.com/plugins", "github:example", "github:../x/y", "ftp://example.com", "https://user:pw@example.com/", "example.com"} {
		if _, err := ParseMarket(in); err == nil {
			t.Errorf("%q: parsed", in)
		}
	}
	a, _ := ParseMarket("github:example/plugins")
	b, _ := ParseMarket("https://github.com/example/plugins")
	if MarketID(a) != MarketID(b) || len(MarketID(a)) != 10 {
		t.Fatal(MarketID(a), MarketID(b))
	}
}

func TestParseIndex(t *testing.T) {
	entry := func(change func(e map[string]any)) map[string]any {
		var m map[string]any
		_ = json.Unmarshal([]byte(exampleManifest), &m)
		e := map[string]any{"manifest": m, "tags": []string{"笔记"}, "packages": []any{
			map[string]any{"platforms": []string{"linux-amd64"}, "url": "dist/notes-linux.tar.gz", "sha256": strings.Repeat("a", 64)},
			map[string]any{"url": "https://example.com/notes.tar.gz", "sha256": strings.Repeat("b", 64)}}}
		if change != nil {
			change(e)
		}
		return e
	}
	b, _ := json.Marshal(map[string]any{"market_version": 1, "name": "Example", "homepage": "http://example.com", "plugins": []any{
		entry(nil),
		entry(nil), // twice
		entry(func(e map[string]any) { e["packages"] = []any{} }),
		entry(func(e map[string]any) {
			e["packages"] = []any{map[string]any{"url": "http://example.com/x.tar.gz", "sha256": strings.Repeat("a", 64)}}
		}),
		entry(func(e map[string]any) { e["packages"] = []any{map[string]any{"url": "x.tar.gz", "sha256": "short"}} }),
		entry(func(e map[string]any) { e["manifest"] = map[string]any{"id": "x"} }),
	}})
	ix, warnings, err := ParseIndex(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Extensions) != 1 || len(warnings) != 5 || ix.Homepage != "" {
		t.Fatalf("%d %v %q", len(ix.Extensions), warnings, ix.Homepage)
	}
	e := ix.Extensions[0]
	if e.Parsed.ID != "example-notes" || e.PackageFor("linux-amd64").URL != "dist/notes-linux.tar.gz" || e.PackageFor("darwin-arm64").URL != "https://example.com/notes.tar.gz" {
		t.Fatalf("%+v", e)
	}
	for _, bad := range []string{`{"market_version": 2, "name": "x", "plugins": []}`, `{"market_version": 1, "plugins": []}`, `[]`} {
		if _, _, err := ParseIndex([]byte(bad)); err == nil {
			t.Errorf("%s: parsed", bad)
		}
	}
	if FromMarket(MarketOrigin("abc", "example-notes")) != "abc" || FromMarket("dir:/tmp/x") != "" {
		t.Fatal("origin")
	}
}

// An entry without tags of its own shows its manifest's; its own win.
func TestIndexTagsFromTheManifest(t *testing.T) {
	var m map[string]any
	_ = json.Unmarshal([]byte(exampleManifest), &m)
	m["tags"] = []string{"笔记", "示例"}
	pkg := []any{map[string]any{"url": "notes.tar.gz", "sha256": strings.Repeat("a", 64)}}
	for _, c := range []struct {
		entry map[string]any
		want  []string
	}{
		{map[string]any{"manifest": m, "packages": pkg}, []string{"笔记", "示例"}},
		{map[string]any{"manifest": m, "tags": []string{"自己的"}, "packages": pkg}, []string{"自己的"}},
	} {
		b, _ := json.Marshal(map[string]any{"market_version": 1, "name": "Example", "extensions": []any{c.entry}})
		ix, warnings, err := ParseIndex(b)
		if err != nil || len(warnings) != 0 || len(ix.Extensions) != 1 || !slices.Equal(ix.Extensions[0].Tags, c.want) {
			t.Fatalf("%v %v %+v", err, warnings, ix)
		}
	}
}

// A market GitHub rate-limits says so and when to try again, not that the token is wrong (#3450); a 401/403 to a
// market without a token asks for one instead of calling it wrong.
func TestMarketRefusalSaysWhy(t *testing.T) {
	cases := []struct {
		name, token string
		status      int
		header      map[string]string
		want, not   string
	}{
		{"primary limit", "", http.StatusForbidden, map[string]string{"X-RateLimit-Remaining": "0"}, "GitHub 在限制这台 hub 的请求（HTTP 403）", "token 不对"},
		{"secondary limit", "secret", http.StatusForbidden, map[string]string{"Retry-After": "120"}, "约 2 分钟后再试。", "token 不对"},
		{"429", "", http.StatusTooManyRequests, nil, "（HTTP 429）。过一会儿再试。", "token 不对"},
		{"no token", "", http.StatusForbidden, map[string]string{"X-RateLimit-Remaining": "59"}, "要登录才能读（HTTP 403）", "token 不对"},
		{"wrong token", "secret", http.StatusUnauthorized, nil, "token 不对", "限流"},
		// A token's own hour used up: rate-limited, and no advice to add the token it has.
		{"token limit", "secret", http.StatusForbidden, map[string]string{"X-RateLimit-Remaining": "0"}, "GitHub 在限制这台 hub 的请求（HTTP 403）", "填一个 GitHub token"},
		// The secondary limit, at times with no header at all: only the body says it.
		{"secondary limit body", "secret", http.StatusForbidden, map[string]string{"body": "You have exceeded a secondary rate limit."}, "（HTTP 403）。过一会儿再试。", "token 不对"},
		{"reset", "", http.StatusForbidden, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": fmt.Sprint(time.Now().Add(30 * time.Minute).Unix())}, "约 30 分钟后再试。填一个 GitHub token 额度更高", "token 不对"},
	}
	for _, c := range cases {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for k, v := range c.header {
				if k != "body" {
					w.Header().Set(k, v)
				}
			}
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(c.header["body"]))
		}))
		s, err := ParseMarket("github:example/market")
		if err != nil {
			t.Fatal(err)
		}
		mk := &Market{Spec: s, Token: c.token, Client: srv.Client(), GitHubAPI: srv.URL}
		_, _, err = mk.Fetch(context.Background())
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), c.want) || strings.Contains(err.Error(), c.not) {
			t.Errorf("%s: %v, want %q and not %q", c.name, err, c.want, c.not)
		}
	}
}

func TestRetryIn(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	for _, c := range []struct {
		header map[string]string
		want   string
	}{
		{nil, "过一会儿再试。"},
		{map[string]string{"Retry-After": "30"}, "1 分钟后再试。"},
		{map[string]string{"Retry-After": "61"}, "约 2 分钟后再试。"},
		{map[string]string{"X-RateLimit-Reset": "1001800"}, "约 30 分钟后再试。"},
		{map[string]string{"X-RateLimit-Reset": "999000"}, "1 分钟后再试。"}, // already past: the clocks differ
		{map[string]string{"Retry-After": "soon"}, "过一会儿再试。"},
	} {
		h := http.Header{}
		for k, v := range c.header {
			h.Set(k, v)
		}
		if got := retryIn(h, now).Error(); got != c.want {
			t.Errorf("%v: %q, want %q", c.header, got, c.want)
		}
	}
}

// A 404 is still notFound (an index then falls back to its old name), with or without a token.
func TestMarket404StaysNotFound(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()
	for _, token := range []string{"", "secret"} {
		mk := &Market{Spec: MarketSpec{Kind: "github", Owner: "example", Repo: "market"}, Token: token, Client: srv.Client(), GitHubAPI: srv.URL}
		var b strings.Builder
		if err := mk.get(context.Background(), mk.contentsURL(IndexFile), "", &b, 1<<10); !errors.As(err, new(notFound)) {
			t.Errorf("token %q: %v, want notFound", token, err)
		}
	}
}
