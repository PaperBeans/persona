package kb

import (
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/kbwire"
	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// Canonical items: one piece of content handed over by several sources.
//
// An item ID has no source prefix (<kind>/<id>.json is shared by every source), and an item stays with the source
// that first stored it (CollectItems' owner check). The Lark user sync reads the same documents and minutes the Lark
// gateway (source "lark") already put in the knowledge base, often under another ID: the gateway keys a document by the
// token in its link, which for a wiki page is the node token, while the sync keys it by the document's obj_token. An
// item handed over with a CanonicalKey is therefore matched against what is stored by its ID, then by its aliases, then
// by the token in the links of stored items, and merged into the one it matches: the stored item keeps its ID and its
// owner, gains the new source in Sources, and takes the new body only when the revision is higher. Items without a
// CanonicalKey never come here, so every other source is collected exactly as before.

// sourceEntry is SourceRef.Entry for the owner of an item stored before items had sources, other than the gateway.
const sourceEntry = "source"

// gatewaySource is the Lark gateway's source name: what it stores came in through the gateway (SourceRef.Entry
// larksyncwire.EntryGateway).
const gatewaySource = "lark"

// itemTokens finds stored items by the other names they are known by: their aliases and the token in their link.
// Built on the first lookup of a kind within one CollectItems call, then kept up to date by add.
type itemTokens struct {
	byKind map[string]map[string]string // kind → alias or link token → stored item ID
}

// find is the stored item of its kind that its ID, one of its aliases or the token in its link names, by the
// stored items' own IDs, aliases and link tokens.
func (t *itemTokens) find(s *Store, it *Item) (*Item, bool) {
	idx := t.kind(s, it.Kind)
	names := append([]string{it.ID}, it.Aliases...)
	if tok := linkToken(it.URL); tok != "" {
		names = append(names, tok)
	}
	for _, n := range names {
		if n == "" {
			continue
		}
		if id, ok := idx[SafeID(n)]; ok {
			if old, ok := s.Item(it.Kind, id); ok {
				return old, true
			}
		}
	}
	return nil, false
}

// add makes an item just written findable by the items after it in the same call.
func (t *itemTokens) add(it *Item) {
	if idx, ok := t.byKind[it.Kind]; ok {
		indexItem(idx, it)
	}
}

func (t *itemTokens) kind(s *Store, kind string) map[string]string {
	if idx, ok := t.byKind[kind]; ok {
		return idx
	}
	if t.byKind == nil {
		t.byKind = map[string]map[string]string{}
	}
	idx := map[string]string{}
	for _, it := range s.Items(kind) {
		indexItem(idx, it)
	}
	t.byKind[kind] = idx
	return idx
}

// indexItem adds an item's names to idx; a name another item already has stays with that one (the first stored).
func indexItem(idx map[string]string, it *Item) {
	put := func(n string) {
		if n = SafeID(n); n != "" {
			if _, taken := idx[n]; !taken {
				idx[n] = it.ID
			}
		}
	}
	put(it.ID)
	for _, a := range it.Aliases {
		put(a)
	}
	put(linkToken(it.URL))
}

// linkKinds are the path segments of a Feishu or Lark link that a document's, wiki node's or minutes' token follows.
var linkKinds = map[string]bool{
	"docx": true, "docs": true, "doc": true, "wiki": true, "sheets": true, "sheet": true, "base": true, "bitable": true,
	"mindnotes": true, "slides": true, "minutes": true, "file": true,
}

// linkToken is the token in a Feishu or Lark document, wiki or minutes link ("https://<tenant>.feishu.cn/docx/<token>"),
// "" for any other link. The same rule as noteDoc's stub IDs, so a stub and the synced item it stands for match.
func linkToken(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	known := false
	for _, h := range []string{"feishu.cn", "larksuite.com", "larkoffice.com"} {
		if host == h || strings.HasSuffix(host, "."+h) {
			known = true
			break
		}
	}
	if !known {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		if linkKinds[parts[i]] && parts[i+1] != "" {
			return parts[i+1]
		}
	}
	return ""
}

// mergeCanonical merges it, handed over by source with a CanonicalKey, into old, the stored item it matched (by ID,
// alias or link token). old keeps its ID, its owner (Source) and its tagging; Sources gains its sources (an item
// stored before items had sources first gets one for its owner). Its title, link, time, body and keys replace old's
// only when its revision is newer (larksyncwire.Decide: higher, or the same revision with another body) or old had
// no body yet; an older revision, arrived late, adds only its sources. The item is tagged again only when its title
// or body changed.
//
// It returns the item as stored now (old when nothing changed).
func (e *Engine) mergeCanonical(old, it *Item, source string) (*Item, error) {
	owner := e.itemSource(old)
	have := old.Sources
	if len(have) == 0 && owner != "" {
		entry := sourceEntry
		if owner == gatewaySource {
			entry = larksyncwire.EntryGateway
		}
		have = []kbwire.SourceRef{{Channel: owner, Entry: entry}}
	}
	next := *old
	next.Sources = larksyncwire.MergeSources(have, it.Sources)
	changed := len(next.Sources) != len(old.Sources)
	decision := larksyncwire.Decide(larksyncwire.IndexRecord{Revision: old.Revision, BodyHash: old.BodyHash}, true, it.Revision, it.BodyHash)
	if decision == larksyncwire.DecideSame && old.Content == "" && old.BodyHash == "" && it.Content != "" {
		// A stored item that never had a body (a link's stub, the gateway's metadata) takes the first one handed
		// over, whatever revision it comes with.
		decision = larksyncwire.DecideNewer
	}
	if decision != larksyncwire.DecideStale {
		// Why it concerns the owner: as its owner says now, or every other source's added to the owner's.
		signals := it.Signals
		if owner != source {
			signals = kbwire.CanonicalSignals(append(slices.Clone(old.Signals), it.Signals...))
		}
		if !slices.Equal(kbwire.CanonicalSignals(old.Signals), signals) {
			next.Signals, changed = signals, true
		}
	}
	if decision == larksyncwire.DecideNewer {
		retag := it.Title != "" && it.Title != old.Title || it.Content != old.Content
		if it.Title != "" {
			next.Title = it.Title
		}
		if it.URL != "" {
			next.URL = it.URL
		}
		if !it.At.IsZero() {
			next.At = it.At
		}
		if owner == source && it.Where != "" {
			// Where it was seen is the owner's to say: another source's chat would hand the item to that source's
			// chat (itemSource).
			next.Where = it.Where
		}
		if it.Note != "" {
			next.Note = it.Note
		} else if it.Content != "" && strings.HasPrefix(old.Note, "unreadable:") {
			next.Note = ""
		}
		next.Content, next.Revision, next.BodyHash, next.CanonicalKey = it.Content, it.Revision, it.BodyHash, it.CanonicalKey
		next.Aliases = mergeAliases(old.ID, old.Aliases, append([]string{it.ID}, it.Aliases...))
		if retag {
			next.TaggedAt = time.Time{}
		}
		changed = true
	} else if aliases := mergeAliases(old.ID, old.Aliases, append([]string{it.ID}, it.Aliases...)); len(aliases) != len(old.Aliases) {
		// The other names it is known by are kept whatever the revision: a later item must find it by them.
		next.Aliases, changed = aliases, true
	}
	if !changed {
		return old, nil
	}
	if err := e.Store.PutItem(&next); err != nil {
		return nil, err
	}
	return &next, nil
}

// keepCanonical keeps what a canonical source gave a stored item (its sources, other names, revision and body) when
// its owner updates it without them (a source that sends no canonical key, such as the Lark gateway, after the sync
// merged into its item). An item that never had them is unchanged, so such a source is collected as before.
func keepCanonical(it, old *Item) {
	if it.CanonicalKey != "" || old.CanonicalKey == "" && len(old.Sources) == 0 && len(old.Aliases) == 0 {
		return
	}
	it.CanonicalKey, it.Aliases, it.Sources = old.CanonicalKey, mergeAliases(it.ID, old.Aliases, it.Aliases), old.Sources
	it.Revision = max(it.Revision, old.Revision)
	if it.Content == "" && it.BodyHash == "" {
		it.Content, it.BodyHash = old.Content, old.BodyHash
	}
}

// mergeAliases is have with every name of add that is new and is not the item's own ID (id).
func mergeAliases(id string, have, add []string) []string {
	out := slices.Clone(have)
	for _, a := range add {
		if a == "" || SafeID(a) == SafeID(id) || slices.Contains(out, a) {
			continue
		}
		out = append(out, a)
	}
	return out
}
