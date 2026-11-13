package i18n

import "testing"

// A market's stored error or warning (#5597) is written again in the reader's language, with the messages inside it;
// text no marketplace catalog wrote stays as it is.
func TestMarketText(t *testing.T) {
	limited := T(ZhHans, "extension-market_read.rate_limited_github_add_token", Args{"status": 403,
		"retry": T(ZhHans, "extension-market_read.retry_minutes", Args{"count": 20})})
	skipped := T(ZhHans, "extension-market_read.entry_skipped", Args{"n": 3,
		"err": T(ZhHans, "extension-market_read.entry_package_count", Args{"id": "example-notes", "most": 16})})
	foreign := T(ZhHans, "extension-market_read.entry_skipped", Args{"n": 31, "err": "plugin weather: configuration: an object only at the first level"})
	for _, c := range []struct {
		l          Lang
		text, want string
	}{
		{En, limited, "GitHub is limiting requests from this hub (HTTP 403). Try again in about 20 minutes. A GitHub token raises the limit, for public repositories too."},
		{Ja, limited, "GitHubがこのハブからのリクエストを制限しています（HTTP 403）。約20分後にもう一度お試しください。GitHubトークンを入力すると上限が上がります（公開リポジトリでも有効です）。"},
		{ZhHans, limited, limited},
		{En, skipped, "Extension 3 isn't listed: example-notes: it needs 1–16 packages."},
		{En, foreign, "Extension 31 isn't listed: plugin weather: configuration: an object only at the first level"},
		{En, T(En, "extension-market_read.not_catalog_said", Args{"message": "Forbidden"}), "This address didn't return an extension marketplace; it answered “Forbidden”. If the address is right, a proxy or sign-in page on the hub machine's network may be answering in its place."},
		{Ja, T(ZhHans, "extension-market_security.cache"), T(Ja, "extension-market_security.cache")},
		{En, "索引的 market_version 是 0，这个 hub 只认 1", "索引的 market_version 是 0，这个 hub 只认 1"}, // a hub before #5597 wrote it
		{En, "", ""},
	} {
		if got := MarketText(c.l, c.text); got != c.want {
			t.Errorf("%s %q:\n got %q\nwant %q", c.l, c.text, got, c.want)
		}
	}
}
