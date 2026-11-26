package larksync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/botcard"
	"github.com/nwverse/persona/server/internal/larksyncwire"
)

// Pending linked-document queue constants and retry schedule.
const (
	// pendingLinksMost caps the per-binding persisted queue in lark_sync_cursors.
	pendingLinksMost = 5000

	// recentLinkWindow: during an initial chat/search backfill (no prior cursor), messages within the last 24 hours
	// still receive live (non-backfill) priority so fresh links are never stuck behind older historical messages.
	recentLinkWindow = 24 * time.Hour

	linkStatusPending      = ""
	linkStatusForbidden    = "forbidden"
	linkStatusScopeMissing = "scope_missing"
	linkStatusFailed       = "failed"

	transientLinkRetryWait = 15 * time.Minute
)

// linkRetryBackoff is the wait between automatic retries of forbidden (403) or scope_missing links when the binding's
// authorization has not visibly changed yet. Links are never permanently abandoned; the wait caps at 24 hours.
var linkRetryBackoff = []time.Duration{
	time.Hour,
	6 * time.Hour,
	24 * time.Hour,
}

// pendingLink is one cloud document or wiki link persisted in lark_sync_cursors[larksyncwire.PendingLinksCursor].
type pendingLink struct {
	Token         string `json:"token"`
	ObjType       string `json:"obj_type"`
	URL           string `json:"url,omitempty"`
	Alias         string `json:"alias,omitempty"`
	Title         string `json:"title,omitempty"`
	Edited        int64  `json:"edited,omitempty"`
	MessageAt     int64  `json:"message_at,omitempty"`
	Backfill      bool   `json:"backfill,omitempty"`
	Status        string `json:"status,omitempty"`
	Reason        string `json:"reason,omitempty"`
	LastAttemptAt int64  `json:"last_attempt_at,omitempty"`
	RetryAfter    int64  `json:"retry_after,omitempty"`
	Attempts      int    `json:"attempts,omitempty"`
}

// pendingLinksState is the JSON envelope stored under larksyncwire.PendingLinksCursor.
type pendingLinksState struct {
	AuthEpoch string        `json:"auth_epoch,omitempty"`
	Items     []pendingLink `json:"items,omitempty"`
}

// ExtractDocLinks extracts Feishu / Lark cloud document and wiki links (excluding minutes, handled by readMinutes)
// from a synced message text (larksyncwire.PendingLinkBackfiller).
func (*Syncer) ExtractDocLinks(text string) []larksyncwire.PendingLinkCandidate {
	if !strings.Contains(text, "http") {
		return nil
	}
	var out []larksyncwire.PendingLinkCandidate
	for _, raw := range linkPattern.FindAllString(text, -1) {
		u := trimLinkURL(raw)
		objType, tok, ok := docLinkFromURL(u)
		if !ok || objType == "minutes" || tok == "" {
			continue
		}
		out = append(out, larksyncwire.PendingLinkCandidate{
			Token:   tok,
			ObjType: larksyncwire.LinkObjType(objType),
			URL:     u,
		})
	}
	return out
}

// MergePendingLinks merges historical remediation candidates into an existing PendingLinksCursor payload as Backfill
// entries without duplicating tokens (or resolved wiki aliases) or downgrading existing live entries.
func (*Syncer) MergePendingLinks(existingCursor string, add []larksyncwire.PendingLinkCandidate) (string, int) {
	if len(add) == 0 {
		return existingCursor, 0
	}
	st := parsePendingLinksState(existingCursor)
	byToken := map[string]*pendingLink{}
	byAlias := map[string]string{}
	for i := range st.Items {
		it := st.Items[i]
		if it.Token == "" || it.ObjType == "" {
			continue
		}
		cp := it
		byToken[cp.Token] = &cp
		if cp.Alias != "" {
			byAlias[cp.Alias] = cp.Token
		}
	}
	added := 0
	for _, c := range add {
		objType := string(c.ObjType)
		if c.Token == "" || objType == "" || objType == "minutes" {
			continue
		}
		if _, ok := byToken[c.Token]; ok {
			continue
		}
		if _, ok := byAlias[c.Token]; ok {
			continue
		}
		msgAt := int64(0)
		if !c.MessageAt.IsZero() {
			msgAt = c.MessageAt.Unix()
		}
		cp := pendingLink{
			Token:     c.Token,
			ObjType:   objType,
			URL:       c.URL,
			MessageAt: msgAt,
			Backfill:  true,
		}
		byToken[cp.Token] = &cp
		added++
	}
	if added == 0 {
		return existingCursor, 0
	}
	items := collectAndTrimPendingLinks(byToken)
	raw, err := marshalPendingLinksState(st.AuthEpoch, items)
	if err != nil {
		return existingCursor, 0
	}
	return raw, added
}

func parsePendingLinksState(raw string) pendingLinksState {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return pendingLinksState{}
	}
	var st pendingLinksState
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return pendingLinksState{}
	}
	return st
}

func marshalPendingLinksState(epoch string, items []pendingLink) (string, error) {
	if len(items) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(pendingLinksState{AuthEpoch: epoch, Items: items})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// authEpoch fingerprints the binding's current user authorization (AuthorizedAt + sorted unique GrantedScopes).
// When the owner re-authorizes or gains new scopes, the epoch changes and blocked links wake up immediately.
func authEpoch(b larksyncwire.Binding) string {
	if b.AuthorizedAt.IsZero() && len(b.GrantedScopes) == 0 {
		return ""
	}
	scopes := append([]string(nil), b.GrantedScopes...)
	sort.Strings(scopes)
	uniq := scopes[:0]
	for _, s := range scopes {
		if s != "" && (len(uniq) == 0 || uniq[len(uniq)-1] != s) {
			uniq = append(uniq, s)
		}
	}
	var sb strings.Builder
	if !b.AuthorizedAt.IsZero() {
		sb.WriteString(strconv.FormatInt(b.AuthorizedAt.UTC().Unix(), 10))
	}
	sb.WriteByte('|')
	sb.WriteString(strings.Join(uniq, ","))
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:8])
}

func retryBackoffFor(attempts int) time.Duration {
	if attempts <= 1 {
		return linkRetryBackoff[0]
	}
	idx := min(attempts-1, len(linkRetryBackoff)-1)
	return linkRetryBackoff[idx]
}

// loadPendingLinks loads the binding's persisted pending linked-doc queue from cursors once per run, waking
// scope_missing and forbidden entries immediately whenever the binding's authorization epoch changed.
func (r *run) loadPendingLinks() {
	if r.pendingLinksLoaded {
		return
	}
	r.pendingLinksLoaded = true
	r.pendingLinks = map[string]*pendingLink{}
	curEpoch := authEpoch(r.b)
	r.pendingLinksAuth = curEpoch

	st := parsePendingLinksState(r.cursors[larksyncwire.PendingLinksCursor])
	recovered := curEpoch != "" && st.AuthEpoch != curEpoch
	if curEpoch != "" && st.AuthEpoch != curEpoch && len(st.Items) > 0 {
		r.pendingLinksDirty = true
	}
	for _, it := range st.Items {
		if it.Token == "" || it.ObjType == "" {
			continue
		}
		if recovered {
			switch it.Status {
			case linkStatusScopeMissing:
				it.Status = linkStatusPending
				it.RetryAfter = 0
				it.Attempts = 0
				r.pendingLinksDirty = true
			case linkStatusForbidden:
				it.RetryAfter = 0
				r.pendingLinksDirty = true
			}
		}
		r.upsertPendingLink(it, false)
	}
}

// enqueueDocLeads merges all valid docLeads discovered in this run's messages into r.pendingLinks once per run.
func (r *run) enqueueDocLeads() {
	r.loadPendingLinks()
	if r.docLeadsEnqueued {
		return
	}
	r.docLeadsEnqueued = true
	for _, l := range r.docLeads {
		if l.token == "" || l.objType == "" || !r.valid(l.resource) {
			continue
		}
		r.upsertPendingLink(pendingLink{
			Token:     l.token,
			ObjType:   l.objType,
			URL:       l.url,
			MessageAt: l.messageAt,
			Backfill:  l.backfill,
		}, true)
	}
}

// upsertPendingLink inserts or merges item into r.pendingLinks, deduplicating by Token (obj_token) and resolved wiki
// Alias. Live references promote backfill items and clear retry delays when referenced again in a newer message.
func (r *run) upsertPendingLink(item pendingLink, fromLiveRun bool) {
	if item.Token == "" || item.ObjType == "" {
		return
	}
	cur := r.pendingLinks[item.Token]
	if cur == nil {
		for _, existing := range r.pendingLinks {
			if existing.Alias != "" && existing.Alias == item.Token {
				cur = existing
				break
			}
		}
	}
	if cur == nil {
		cp := item
		r.pendingLinks[cp.Token] = &cp
		if fromLiveRun {
			r.pendingLinksDirty = true
		}
		return
	}
	changed := false
	if item.MessageAt > cur.MessageAt {
		cur.MessageAt = item.MessageAt
		changed = true
	}
	if !item.Backfill && cur.Backfill {
		cur.Backfill = false
		changed = true
	}
	if item.Alias != "" && cur.Alias == "" {
		cur.Alias = item.Alias
		changed = true
	}
	if item.Title != "" && cur.Title == "" {
		cur.Title = item.Title
		changed = true
	}
	if item.Edited > cur.Edited {
		cur.Edited = item.Edited
		changed = true
	}
	if item.URL != "" && (cur.URL == "" || (strings.Contains(cur.URL, "/wiki/") && !strings.Contains(item.URL, "/wiki/"))) {
		cur.URL = item.URL
		changed = true
	}
	if fromLiveRun && cur.Status != linkStatusPending && (item.MessageAt == 0 || item.MessageAt > cur.LastAttemptAt) {
		if cur.RetryAfter != 0 {
			cur.RetryAfter = 0
			changed = true
		}
	}
	if changed && fromLiveRun {
		r.pendingLinksDirty = true
	}
}

// resolvePendingWiki migrates a queued wiki node_token entry to its resolved obj_token, deduplicating against any
// existing obj_token entry in r.pendingLinks.
func (r *run) resolvePendingWiki(nodeToken string, node wikiNodeInfo) *pendingLink {
	old := r.pendingLinks[nodeToken]
	if old == nil {
		return nil
	}
	delete(r.pendingLinks, nodeToken)
	r.pendingLinksDirty = true

	merged := pendingLink{
		Token:     node.objToken,
		ObjType:   node.objType,
		URL:       old.URL,
		Alias:     nodeToken,
		Title:     cmpString(old.Title, node.title),
		Edited:    max(old.Edited, node.edited),
		MessageAt: old.MessageAt,
		Backfill:  old.Backfill,
		Status:    old.Status,
		Reason:    old.Reason,
		Attempts:  old.Attempts,
	}
	if existing := r.pendingLinks[node.objToken]; existing != nil {
		if merged.MessageAt > existing.MessageAt {
			existing.MessageAt = merged.MessageAt
		}
		if !merged.Backfill {
			existing.Backfill = false
		}
		if existing.Alias == "" {
			existing.Alias = nodeToken
		}
		if existing.Title == "" {
			existing.Title = merged.Title
		}
		if merged.Edited > existing.Edited {
			existing.Edited = merged.Edited
		}
		if existing.URL == "" {
			existing.URL = merged.URL
		}
		if merged.Status == linkStatusPending && existing.Status != linkStatusPending {
			existing.RetryAfter = 0
		}
		return existing
	}
	r.pendingLinks[node.objToken] = &merged
	return &merged
}

func (r *run) markPendingFailure(token, status, reason string, wait time.Duration) {
	it := r.pendingLinks[token]
	if it == nil {
		return
	}
	now := r.s.now()
	it.Status = status
	it.Reason = reason
	it.LastAttemptAt = now.Unix()
	it.Attempts++
	if wait <= 0 {
		wait = retryBackoffFor(it.Attempts)
	}
	it.RetryAfter = now.Add(wait).Unix()
	r.pendingLinksDirty = true
}

func (r *run) removePendingLink(token string) {
	if _, ok := r.pendingLinks[token]; ok {
		delete(r.pendingLinks, token)
		r.pendingLinksDirty = true
	}
}

// wakeScopeType clears RetryAfter on any remaining queued scope_missing links of objType once a read of that type
// succeeds (proving the missing scope was restored).
func (r *run) wakeScopeType(objType string) {
	for _, it := range r.pendingLinks {
		if it.Status == linkStatusScopeMissing && it.ObjType == objType && it.RetryAfter != 0 {
			it.Status = linkStatusPending
			it.RetryAfter = 0
			it.Attempts = 0
			r.pendingLinksDirty = true
		}
	}
}

// comparePendingLinkPriority orders links for fetching:
// 1. Live / recent links (!Backfill) before historical backfill / remediation links (Backfill).
// 2. Fresh never-attempted links (Attempts == 0) before retries (Attempts > 0).
// 3. Newer message timestamps (MessageAt DESC) before older ones.
// 4. Deterministic token tie-breaker.
func comparePendingLinkPriority(a, b pendingLink) bool {
	if a.Backfill != b.Backfill {
		return !a.Backfill
	}
	aFresh := a.Attempts == 0 && a.Status == linkStatusPending
	bFresh := b.Attempts == 0 && b.Status == linkStatusPending
	if aFresh != bFresh {
		return aFresh
	}
	if a.MessageAt != b.MessageAt {
		return a.MessageAt > b.MessageAt
	}
	return a.Token < b.Token
}

func collectAndTrimPendingLinks(byToken map[string]*pendingLink) []pendingLink {
	items := make([]pendingLink, 0, len(byToken))
	for _, it := range byToken {
		items = append(items, *it)
	}
	sort.Slice(items, func(i, j int) bool {
		return comparePendingLinkPriority(items[i], items[j])
	})
	if len(items) > pendingLinksMost {
		items = items[:pendingLinksMost]
	}
	return items
}

// savePendingLinks merges any unfetched valid leads (even if readLinkedDocs was skipped due to budget exhaustion),
// prunes links already fetched with content in this run, and stages the updated queue cursor when changed.
func (r *run) savePendingLinks() {
	r.enqueueDocLeads()
	for tok, it := range r.pendingLinks {
		docTok := tok
		if it.ObjType == "wiki" {
			if node, ok := r.wikiNodes[tok]; ok && node.objToken != "" {
				docTok = node.objToken
			}
		}
		if st := r.docs[larksyncwire.KeyDoc+docTok]; st != nil && st.fetched && st.lastErr == nil && (st.doc.Body != "" || st.doc.Revision > 0) {
			st.sightings = append(st.sightings, sighting{
				resource: larksyncwire.PendingLinksCursor,
				ref:      r.ref(larksyncwire.EntryMessage),
				revision: st.doc.Revision,
			})
			delete(r.pendingLinks, tok)
			r.pendingLinksDirty = true
		}
	}
	prev := strings.TrimSpace(r.cursors[larksyncwire.PendingLinksCursor])
	if len(r.pendingLinks) == 0 && (prev == "" || prev == "{}") {
		return
	}
	if !r.pendingLinksDirty && prev != "" {
		return
	}
	items := collectAndTrimPendingLinks(r.pendingLinks)
	raw, err := marshalPendingLinksState(r.pendingLinksAuth, items)
	if err != nil {
		return
	}
	if raw != prev {
		r.moveCursor(larksyncwire.PendingLinksCursor, raw)
	}
}

func isPermissionCode(code int) bool {
	switch code {
	case 1770032, // docx forbidden / no permission
		131006,  // wiki permission denied
		1061004, // drive / file forbidden / export disabled / no permission
		1061005, // drive no permission
		1062007, // drive permission denied
		1254302, // bitable permission denied
		90202:   // sheets permission denied
		return true
	}
	return false
}

func isPermissionError(err error) bool {
	if err == nil || errors.Is(err, botcard.ErrTooLarge) {
		return false
	}
	e := asSyncError(err)
	if e == nil || e.Kind == larksyncwire.ErrKindScope {
		return false
	}
	if e.HTTPStatus == http.StatusForbidden || isPermissionCode(e.Code) {
		return true
	}
	msg := strings.ToLower(e.Msg)
	return strings.Contains(msg, "forbidden") ||
		strings.Contains(msg, "permission") ||
		strings.Contains(msg, "no access") ||
		strings.Contains(msg, "export disabled")
}

// readLinkedDocs drains the persistent per-binding pending linked-doc queue merged with links discovered in this run's
// messages, prioritizing newer live links ahead of historical backfill links. Unfinished links on budget exhaustion or
// rate limits remain in the queue for the next run (including ModeIM), and forbidden (403) / scope_missing links record
// their reason and stay queued for automatic retry after permission recovery.
func (r *run) readLinkedDocs(ctx context.Context) error {
	r.enqueueDocLeads()
	if len(r.pendingLinks) == 0 {
		return nil
	}

	nowSec := r.s.now().Unix()
	ready := make([]string, 0, len(r.pendingLinks))
	for tok, it := range r.pendingLinks {
		if it.RetryAfter > nowSec {
			continue
		}
		ready = append(ready, tok)
	}
	if len(ready) == 0 {
		return nil
	}
	sort.Slice(ready, func(i, j int) bool {
		return comparePendingLinkPriority(*r.pendingLinks[ready[i]], *r.pendingLinks[ready[j]])
	})
	capped := false
	if len(ready) > linkedDocsMost {
		ready = ready[:linkedDocsMost]
		capped = true
	}

	const linkRes = larksyncwire.PendingLinksCursor
	scopeBlocked := map[string]error{} // "wiki" | "meta" | objType -> scope error

	for start := 0; start < len(ready); start += metaBatchMost {
		if r.overBudget() {
			r.partial = true
			return nil
		}
		chunkTokens := ready[start:min(start+metaBatchMost, len(ready))]

		var direct []linkedCandidate
		directIdx := map[string]int{} // objToken -> index in direct
		addDirect := func(c linkedCandidate) {
			if c.objToken == "" {
				return
			}
			if i, ok := directIdx[c.objToken]; ok {
				if direct[i].alias == "" && c.alias != "" {
					direct[i].alias = c.alias
				}
				if direct[i].title == "" && c.title != "" {
					direct[i].title = c.title
				}
				if direct[i].edited == 0 && c.edited > 0 {
					direct[i].edited = c.edited
				}
				if direct[i].url == "" || (strings.Contains(direct[i].url, "/wiki/") && c.alias == "" && c.url != "") {
					direct[i].url = cmpString(c.url, direct[i].url)
				}
				return
			}
			directIdx[c.objToken] = len(direct)
			direct = append(direct, c)
		}

		// Step 1: resolve wiki links in this chunk to their underlying obj_token + obj_type.
		for _, tok := range chunkTokens {
			if r.overBudget() {
				r.partial = true
				return nil
			}
			it := r.pendingLinks[tok]
			if it == nil {
				continue // already resolved/deduplicated earlier in this run
			}
			if it.ObjType != "wiki" {
				addDirect(linkedCandidate{
					docCandidate: docCandidate{
						objToken: it.Token,
						objType:  it.ObjType,
						title:    it.Title,
						url:      it.URL,
						alias:    it.Alias,
						edited:   it.Edited,
					},
					resource: linkRes,
					entry:    larksyncwire.EntryMessage,
				})
				continue
			}
			if serr := scopeBlocked["wiki"]; serr != nil {
				reason := unreadableReason(serr)
				r.recordUnreadableDoc(linkRes, larksyncwire.EntryMessage, it.Token, "wiki", it.Token, it.URL, reason)
				r.markPendingFailure(it.Token, linkStatusScopeMissing, reason, 0)
				continue
			}
			node, ok := r.wikiNodes[it.Token]
			if !ok {
				var data struct {
					Node struct {
						NodeToken   string `json:"node_token"`
						ObjToken    string `json:"obj_token"`
						ObjType     string `json:"obj_type"`
						Title       string `json:"title"`
						ObjEditTime string `json:"obj_edit_time"`
					} `json:"node"`
				}
				err := r.sess.getJSON(ctx, request{
					family: famWiki,
					op:     "get wiki node",
					path:   "/open-apis/wiki/v2/spaces/get_node",
					query:  map[string]string{"token": it.Token},
				}, &data)
				if err != nil {
					if fatal(err) {
						return err
					}
					if e := asSyncError(err); e != nil {
						switch e.Kind {
						case larksyncwire.ErrKindRateLimited:
							r.fail("links", err)
							return nil
						case larksyncwire.ErrKindScope:
							r.fail("links:wiki", err)
							scopeBlocked["wiki"] = err
							reason := unreadableReason(err)
							r.recordUnreadableDoc(linkRes, larksyncwire.EntryMessage, it.Token, "wiki", it.Token, it.URL, reason)
							r.markPendingFailure(it.Token, linkStatusScopeMissing, reason, 0)
							continue
						case larksyncwire.ErrKindTransient:
							r.fail("links:wiki:"+it.Token, err)
							r.markPendingFailure(it.Token, linkStatusFailed, unreadableReason(err), transientLinkRetryWait)
							continue
						}
					}
					reason := unreadableReason(err)
					r.recordUnreadableDoc(linkRes, larksyncwire.EntryMessage, it.Token, "wiki", it.Token, it.URL, reason)
					if isPermissionError(err) {
						r.markPendingFailure(it.Token, linkStatusForbidden, reason, 0)
					} else {
						r.removePendingLink(it.Token)
					}
					continue
				}
				node = wikiNodeInfo{
					objToken: data.Node.ObjToken,
					objType:  strings.ToLower(data.Node.ObjType),
					title:    data.Node.Title,
					edited:   parseUnix(data.Node.ObjEditTime),
				}
				r.wikiNodes[it.Token] = node
				r.wakeScopeType("wiki")
			}
			if node.objType == "minutes" && node.objToken != "" {
				r.leads = append(r.leads, minuteLead{token: node.objToken, url: it.URL, resource: linkRes, entry: larksyncwire.EntryMessage})
				r.removePendingLink(it.Token)
				continue
			}
			if !docType(node.objType) || node.objToken == "" {
				r.removePendingLink(it.Token)
				continue
			}
			resolved := r.resolvePendingWiki(it.Token, node)
			if resolved == nil {
				continue
			}
			addDirect(linkedCandidate{
				docCandidate: docCandidate{
					objToken: resolved.Token,
					objType:  resolved.ObjType,
					title:    resolved.Title,
					url:      resolved.URL,
					alias:    resolved.Alias,
					edited:   resolved.Edited,
				},
				resource: linkRes,
				entry:    larksyncwire.EntryMessage,
			})
		}

		if len(direct) == 0 {
			continue
		}

		// Step 2: batch-query Drive metadata for direct candidates in this chunk that still need title / edit time.
		var needMeta []int
		for i, c := range direct {
			if st := r.docs[larksyncwire.KeyDoc+c.objToken]; st != nil && st.fetched {
				continue // already read in readDrive / readWiki this run
			}
			if c.title == "" || c.edited == 0 {
				needMeta = append(needMeta, i)
			}
		}
		if len(needMeta) > 0 && scopeBlocked["meta"] == nil {
			metas, failed, err := r.batchDocMeta(ctx, direct, needMeta)
			if err != nil {
				if fatal(err) {
					return err
				}
				if e := asSyncError(err); e != nil {
					switch e.Kind {
					case larksyncwire.ErrKindRateLimited:
						r.fail("links", err)
						return nil
					case larksyncwire.ErrKindScope:
						r.fail("links:meta", err)
						scopeBlocked["meta"] = err
					}
				}
			}
			for _, i := range needMeta {
				tok := direct[i].objToken
				if m, ok := metas[tok]; ok {
					if direct[i].title == "" {
						direct[i].title = m.title
					}
					if m.edited > direct[i].edited {
						direct[i].edited = m.edited
					}
					if m.url != "" && (direct[i].url == "" || strings.Contains(direct[i].url, "/wiki/")) {
						direct[i].url = m.url
					}
					if m.docType != "" && docType(m.docType) {
						direct[i].objType = m.docType
					}
					if pl := r.pendingLinks[tok]; pl != nil {
						if pl.Title == "" && direct[i].title != "" {
							pl.Title = direct[i].title
							r.pendingLinksDirty = true
						}
						if direct[i].edited > pl.Edited {
							pl.Edited = direct[i].edited
							r.pendingLinksDirty = true
						}
						if direct[i].url != "" && pl.URL != direct[i].url {
							pl.URL = direct[i].url
							r.pendingLinksDirty = true
						}
						if direct[i].objType != "" && pl.ObjType != direct[i].objType {
							pl.ObjType = direct[i].objType
							r.pendingLinksDirty = true
						}
					}
				} else if code, ok := failed[tok]; ok {
					direct[i].metaFailCode = code
				}
			}
		}

		// Step 3: read each resolved document via consider, isolating per-document failures and keeping retryable
		// permission / scope / transient errors in the persistent queue.
		for _, c := range direct {
			if r.overBudget() {
				r.partial = true
				return nil
			}
			if serr := scopeBlocked[c.objType]; serr != nil {
				reason := unreadableReason(serr)
				r.recordUnreadableDoc(c.resource, c.entry, c.objToken, c.objType, c.alias, c.url, reason)
				r.markPendingFailure(c.objToken, linkStatusScopeMissing, reason, 0)
				continue
			}
			if c.objType == "file" && c.title == "" {
				if serr := scopeBlocked["meta"]; serr != nil {
					reason := unreadableReason(serr)
					r.recordUnreadableDoc(c.resource, c.entry, c.objToken, c.objType, c.alias, c.url, reason)
					r.markPendingFailure(c.objToken, linkStatusScopeMissing, reason, 0)
					continue
				}
				if c.metaFailCode != 0 {
					reason := fmt.Sprintf("unreadable: drive metadata failed (code %d)", c.metaFailCode)
					r.recordUnreadableDoc(c.resource, c.entry, c.objToken, c.objType, c.alias, c.url, reason)
					if isPermissionCode(c.metaFailCode) {
						r.markPendingFailure(c.objToken, linkStatusForbidden, reason, 0)
					} else {
						r.removePendingLink(c.objToken)
					}
					continue
				}
			}
			if err := r.consider(ctx, c.resource, c.entry, c.docCandidate); err != nil {
				if fatal(err) {
					return err
				}
				if e := asSyncError(err); e != nil {
					switch e.Kind {
					case larksyncwire.ErrKindRateLimited:
						r.fail("links", err)
						return nil
					case larksyncwire.ErrKindScope:
						r.fail("links:"+c.objType, err)
						scopeBlocked[c.objType] = err
						reason := unreadableReason(err)
						r.recordUnreadableDoc(c.resource, c.entry, c.objToken, c.objType, c.alias, c.url, reason)
						r.markPendingFailure(c.objToken, linkStatusScopeMissing, reason, 0)
						continue
					}
				}
				r.fail("links:"+c.objToken, err)
				r.markPendingFailure(c.objToken, linkStatusFailed, unreadableReason(err), transientLinkRetryWait)
				continue
			}
			st := r.docs[larksyncwire.KeyDoc+c.objToken]
			if st != nil && isPermissionError(st.lastErr) {
				r.markPendingFailure(c.objToken, linkStatusForbidden, cmpString(st.doc.Note, unreadableReason(st.lastErr)), 0)
				continue
			}
			if st != nil && st.fetched && st.doc.Body != "" {
				r.wakeScopeType(c.objType)
			}
			r.removePendingLink(c.objToken)
		}
	}
	if capped {
		r.partial = true
	}
	return nil
}
