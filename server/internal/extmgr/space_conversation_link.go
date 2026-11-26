package extmgr

import (
	"net/url"
	"strings"
)

// spaceConversationLink says whether raw is the narrow handoff the space-conversations extension may open
// (docs/protocol/deep-links.md): persona://tracks/<machine>/<space>/<session>, three non-empty bounded ASCII segments,
// no user info, query or fragment, and never the health space. It mirrors the apps' ExtensionConversationLink, so an
// answer the hub lets through is one every app can open.
func spaceConversationLink(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "persona") || u.Host != "tracks" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if !strings.HasPrefix(u.Path, "/") || len(parts) != 3 || parts[1] == "health" {
		return false
	}
	for _, part := range parts {
		if part == "" || len(part) > 256 {
			return false
		}
		for _, r := range part {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_.:-", r)) {
				return false
			}
		}
	}
	return true
}
