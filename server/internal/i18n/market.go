package i18n

import (
	"strconv"
	"strings"
	"sync"
)

// MarketText renders an extension market's stored error or warning in l. A market keeps the text of its last read
// (in the language it was written in, usually the default) with its record; a text one of the marketplace catalogs
// wrote is written again in l, the messages inside it too (a rate limit's "try again in …", a skipped entry's
// reason). Anything else, such as a manifest's own validation text or a server's answer, stays as it is.
func MarketText(l Lang, text string) string { return marketText(l, text, 0) }

var marketTemplates = sync.OnceValue(func() []deploymentTemplate {
	return makeCatalogTemplates(func(key string) bool {
		return strings.HasPrefix(key, "extension-market_read.") || strings.HasPrefix(key, "extension-market_security.") ||
			strings.HasPrefix(key, "extension-market_git.") || key == "hub-extension_market.pin_rotated_pending" ||
			key == "extension-status.entry_translation_skipped"
	})
})

func marketText(l Lang, text string, depth int) string {
	if text == "" || depth >= 8 {
		return text
	}
	for _, form := range marketTemplates() {
		match := form.pattern.FindStringSubmatch(text)
		if match == nil {
			continue
		}
		args := Args{}
		for i, name := range form.names {
			value := match[i+1]
			if name == "count" {
				n, err := strconv.ParseInt(value, 10, 64)
				if err != nil {
					return text
				}
				args[name] = n
				continue
			}
			args[name] = marketText(l, value, depth+1)
		}
		return T(l, form.key, args)
	}
	return text
}
