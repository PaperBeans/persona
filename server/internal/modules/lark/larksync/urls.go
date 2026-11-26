package larksync

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/lazyre"
)

// linkPattern finds web links in a message or a document, stopping before whitespace, quotes, brackets, parentheses
// (Markdown / post links) and non-ASCII characters (such as CJK punctuation or adjacent text).
var linkPattern = lazyre.New(`https?://[A-Za-z0-9._~:/?#@!$&'*+,;=%-]+`)

// docHosts are the sites of the two clouds' documents and minutes (a tenant's site is a subdomain).
var docHosts = []string{"feishu.cn", "larksuite.com", "larkoffice.com"}

// pathKinds are a document link's path segment, and what TokenFromURL calls its token.
var pathKinds = map[string]string{
	"docx": "doc", "docs": "doc", "doc": "doc", "sheets": "doc", "sheet": "doc", "base": "doc", "bitable": "doc",
	"file": "doc", "mindnotes": "doc", "slides": "doc",
	"wiki":    "wiki",
	"minutes": "minutes",
}

// pathObjTypes maps a Feishu / Lark URL path segment to its canonical object type for API calls.
var pathObjTypes = map[string]string{
	"docx":      "docx",
	"docs":      "doc",
	"doc":       "doc",
	"sheets":    "sheet",
	"sheet":     "sheet",
	"base":      "bitable",
	"bitable":   "bitable",
	"file":      "file",
	"mindnotes": "mindnote",
	"slides":    "slides",
	"wiki":      "wiki",
	"minutes":   "minutes",
}

// TokenFromURL recognises a Feishu / Lark document, wiki or minutes link and returns its kind ("doc", "wiki" for a
// wiki node token, "minutes") and its token; "", "" for anything else. The knowledge base and the manager use it to
// line a link up with the content it names (a stored item's URL, a link in a message).
func TokenFromURL(u string) (kind, token string) {
	seg, tok, ok := parseDocURL(u)
	if !ok {
		return "", ""
	}
	return pathKinds[seg], tok
}

// docLinkFromURL recognises a Feishu / Lark cloud document, wiki or minutes link and returns its canonical object type
// ("docx", "doc", "sheet", "bitable", "file", "mindnote", "slides", "wiki", "minutes") and token.
func docLinkFromURL(u string) (objType, token string, ok bool) {
	seg, tok, ok := parseDocURL(u)
	if !ok {
		return "", "", false
	}
	t := pathObjTypes[seg]
	if t == "wiki" && (strings.EqualFold(tok, "space") || strings.EqualFold(tok, "settings")) {
		return "", "", false
	}
	return t, tok, t != ""
}

func parseDocURL(u string) (seg, token string, ok bool) {
	u = strings.TrimSpace(u)
	if u == "" {
		return "", "", false
	}
	if !strings.Contains(u, "://") {
		u = "https://" + u
	}
	p, err := url.Parse(u)
	if err != nil || p.Host == "" {
		return "", "", false
	}
	host := strings.ToLower(p.Hostname())
	known := false
	for _, h := range docHosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			known = true
			break
		}
	}
	if !known {
		return "", "", false
	}
	segs := strings.Split(strings.Trim(p.Path, "/"), "/")
	for i := 0; i+1 < len(segs); i++ {
		s := strings.ToLower(segs[i])
		if _, exists := pathKinds[s]; !exists {
			continue
		}
		if tok := leadingToken(segs[i+1]); tok != "" {
			return s, tok, true
		}
		return "", "", false
	}
	return "", "", false
}

func trimLinkURL(u string) string {
	return strings.TrimRight(u, ".,;:!?)]}>\"'")
}

// leadingToken is s's leading run of letters and digits (a token, without what a sentence put after it).
func leadingToken(s string) string {
	end := 0
	for end < len(s) {
		c := s[end]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			end++
			continue
		}
		break
	}
	return s[:end]
}

// MessageFingerprint identifies a chat message by what any reader of it sees, for lining up the same message read
// through two ways that know it by different ids (this sync and the Lark gateway): the sha256 hex of the normalized
// chat name, the normalized sender, the send time in two-second buckets (at.Unix()/2) and the sha256 hex of the
// normalized body, joined by NUL. Normalized: whitespace runs collapsed to one space, trimmed, lower case. An empty
// sender yields the senderless fallback key used when one side has no display name (for example a bot/app sender or a
// binding without contact scope). The knowledge base computes the same (internal/kb, which cannot import this module):
// change both or neither.
func MessageFingerprint(chatName, sender string, at time.Time, body string) string {
	bodySum := sha256.Sum256([]byte(normalizeText(body)))
	s := normalizeText(chatName) + "\x00" + normalizeText(sender) + "\x00" + strconv.FormatInt(at.Unix()/2, 10) + "\x00" + hex.EncodeToString(bodySum[:])
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func normalizeText(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}
