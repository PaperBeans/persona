package node

import (
	"context"
	"net"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// A runtime that serves an app-server-compatible daemon's chats itself (a companion that ships with the daemon and
// talks to its app-server like any client) lists those chats as its own sessions. The node attaches to every
// app-server it finds (codex_remote.go), so it would follow the same endpoint a second time: it reads the daemon's
// loaded threads every 5 s and lists those the runtime's rows do not name (codex_thread, runtimecodex.go). So the
// runtime claims the instance in runtime.hello (codex_claims: its Codex home, its endpoint, or both; the feature
// codex_claims): while the claim holds, the node keeps no connection to a matching instance, one it finds later too,
// and calls nothing on it. It is deduplication, not a lock: the daemon still orders the writes of all its clients, and
// every client can still write. The claim stands for codexClaimKeep after the runtime's last connection left, so a
// runtime that restarts does not have the node attach to the instance, list its threads under the node's own rows and
// drop those again once the runtime is back. The runtime's own rows still leave the list while it is gone: as for any
// runtime, only rows whose conversation is a Claude transcript here stay (runtimeaway.go). A hello without claims ends
// them at once. Only a runtime on this machine's socket claims: a daemon's (daemons.go) app-servers are another
// machine's.
// docs/integrations/runtimes.md ("Claiming a Codex app-server instance (optional)").

// codexClaimKeep is how long a runtime's claims stand once its last connection left.
const codexClaimKeep = time.Minute

// maxCodexClaims bounds what one hello claims; maxCodexClaimText bounds each home and endpoint.
const (
	maxCodexClaims    = 16
	maxCodexClaimText = 4096
)

// runtimeCodexClaim is one entry of runtime.hello's codex_claims: a Codex home, an app-server endpoint, or both, which
// then name only the instance that has both. Kept as the runtime wrote them; the node resolves them as it resolves its
// own instances when it compares (resolveCodexClaims).
type runtimeCodexClaim struct {
	Home     string `json:"home,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
}

// cleanCodexClaims is the entries of a hello's codex_claims that name something, trimmed, each once, at most
// maxCodexClaims of them; nil when none does.
func cleanCodexClaims(in []runtimeCodexClaim) []runtimeCodexClaim {
	var out []runtimeCodexClaim
	for _, c := range in {
		c.Home, c.Endpoint = strings.TrimSpace(c.Home), strings.TrimSpace(c.Endpoint)
		if c.Home == "" && c.Endpoint == "" || len(c.Home) > maxCodexClaimText || len(c.Endpoint) > maxCodexClaimText || slices.Contains(out, c) {
			continue
		}
		if len(out) == maxCodexClaims {
			break
		}
		out = append(out, c)
	}
	return out
}

// codexClaimsOf is what a runtime claims, when its last live connection left (zero while a live connection holds it),
// and the newest connection serial that updated it.
type codexClaimsOf struct {
	claims []runtimeCodexClaim
	gone   time.Time
	serial int
}

// heldCodexClaim is a claim that holds now, and the runtime that made it.
type heldCodexClaim struct {
	runtime string
	runtimeCodexClaim
}

// claimCodex records what h's runtime claims with the hello it just said (none: it claims nothing from now on), and has
// the Node serving the hub leave the instances it claims before the hello is answered. A daemon claims nothing.
func (rh *RuntimeHost) claimCodex(h *runtimeConn, claims []runtimeCodexClaim) {
	if h.isDaemon() {
		claims = nil
	}
	claims = cleanCodexClaims(claims)
	if !rh.reg.setCodexClaims(h, claims) || len(claims) == 0 {
		return
	}
	rh.logger.Printf("runtime %s #%d claims %d Codex app-server instance(s) it serves", h.id(), h.serial, len(claims))
	if n := rh.current(); n != nil {
		n.releaseClaimedCodex()
	}
}

// codexConnLiveLocked reports whether h is still registered, has not been superseded as stale by a newer hello, and
// whose peer has not closed yet. Callers hold b.mu.
func (b *runtimeRegistry) codexConnLiveLocked(h *runtimeConn) bool {
	if h == nil || b.conns[h.serial] != h || h.superseded {
		return false
	}
	if h.peer != nil {
		select {
		case <-h.peer.Done():
			return false
		default:
		}
	}
	return true
}

// hasLiveCodexConnLocked reports whether runtime id still has any live connection. Callers hold b.mu.
func (b *runtimeRegistry) hasLiveCodexConnLocked(id string) bool {
	for _, o := range b.conns {
		if b.codexConnLiveLocked(o) && o.id() == id {
			return true
		}
	}
	return false
}

// setCodexClaims replaces what h's runtime claims; none: it claims nothing from now on, with no grace.
// A connection already superseded by a newer hello is ignored so a stale hello cannot overwrite newer claims or clear a
// departed claimant's keep timestamp. Returns false when h was ignored.
func (b *runtimeRegistry) setCodexClaims(h *runtimeConn, claims []runtimeCodexClaim) bool {
	id := h.id()
	if b == nil || id == "" {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if h.superseded {
		return false
	}
	prev, had := b.codexClaims[id]
	if had && prev.serial > h.serial {
		return false
	}
	for _, o := range b.conns {
		if o != h && o.serial > h.serial && o.id() == id {
			return false
		}
	}
	if len(claims) == 0 {
		delete(b.codexClaims, id)
		return true
	}
	if b.codexClaims == nil {
		b.codexClaims = map[string]codexClaimsOf{}
	}
	c := codexClaimsOf{claims: claims, serial: h.serial}
	if !b.codexConnLiveLocked(h) && !b.hasLiveCodexConnLocked(id) {
		// h closed while its hello was being answered (remove ran or peer closed first) and no live connection remains:
		// preserve an existing grace timestamp so a dead hello cannot extend or clear it, or start the keep now.
		if had && !prev.gone.IsZero() {
			c.gone = prev.gone
		} else {
			c.gone = time.Now()
		}
	}
	b.codexClaims[id] = c
	return true
}

// leftCodexClaims notes that runtime id has no live connection any more: its claims stand for codexClaimKeep. Callers
// hold b.mu.
func (b *runtimeRegistry) leftCodexClaims(id string) {
	c, ok := b.codexClaims[id]
	if !ok || id == "" {
		return
	}
	if b.hasLiveCodexConnLocked(id) {
		return // another live connection of the same runtime (it reconnected before the old one was gone)
	}
	if c.gone.IsZero() {
		c.gone = time.Now()
		b.codexClaims[id] = c
	}
}

// heldCodexClaims is every claim that holds now: those of the connected runtimes, and for codexClaimKeep those of the
// runtimes that left; ordered by runtime, so the same one is named for an instance two of them claim.
func (b *runtimeRegistry) heldCodexClaims() []heldCodexClaim {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []heldCodexClaim
	for id, c := range b.codexClaims {
		if !c.gone.IsZero() && time.Since(c.gone) > codexClaimKeep {
			delete(b.codexClaims, id)
			continue
		}
		for _, claim := range c.claims {
			out = append(out, heldCodexClaim{runtime: id, runtimeCodexClaim: claim})
		}
	}
	slices.SortStableFunc(out, func(a, b heldCodexClaim) int { return strings.Compare(a.runtime, b.runtime) })
	return out
}

// resolveCodexClaims is held resolved as the node resolves its instances: a home absolute with its links followed
// (canonicalCodexHome), an endpoint as canonicalCodexEndpoint has it. A claim with a part that resolves to nothing
// names no instance and is left out.
func (n *Node) resolveCodexClaims(held []heldCodexClaim) []heldCodexClaim {
	var out []heldCodexClaim
	for _, c := range held {
		if c.Home != "" {
			if c.Home = canonicalCodexHome(c.Home, n.home); c.Home == "" {
				continue
			}
		}
		if c.Endpoint != "" {
			if c.Endpoint = canonicalCodexEndpoint(c.Endpoint, n.home); c.Endpoint == "" {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// canonicalCodexEndpoint is an app-server endpoint as a claim and an instance are compared: a Unix socket's path
// absolute with its links followed as far as it exists (an alias, codexSocketPath, comes out as its target either
// way), a WebSocket's literal IP and port; "" for anything else.
func canonicalCodexEndpoint(endpoint, base string) string {
	if rest, ok := strings.CutPrefix(endpoint, "unix://"); ok {
		p := proto.ExpandHome(rest, base)
		if !filepath.IsAbs(p) {
			return ""
		}
		return "unix://" + realPath(filepath.Clean(p))
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "ws" || u.Port() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return ""
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil {
		return ""
	}
	return "ws://" + net.JoinHostPort(ip.String(), u.Port())
}

// codexClaimant is the runtime that claims instance s, "" when none does: the first claim naming its home, its
// endpoint, or both.
func (n *Node) codexClaimant(s proto.CodexServer, claims []heldCodexClaim) string {
	if len(claims) == 0 {
		return ""
	}
	endpoint := canonicalCodexEndpoint(s.Endpoint, n.home)
	for _, c := range claims {
		if (c.Home == "" || c.Home == s.Home) && (c.Endpoint == "" || c.Endpoint == endpoint) {
			return c.runtime
		}
	}
	return ""
}

// attachCodexServers keeps a connection to each of servers that no runtime claims (ensureCodexRemote) and none to one a
// runtime does. servers is the whole inventory: an instance that left it is no longer noted as claimed, and is logged
// as claimed again if it comes back while a runtime still claims it.
func (n *Node) attachCodexServers(ctx context.Context, servers []proto.CodexServer) {
	r := n.codices
	if r == nil {
		return
	}
	r.attach.Lock()
	defer r.attach.Unlock()
	claims := n.resolveCodexClaims(n.runtimes.heldCodexClaims())
	for _, s := range servers {
		if by := n.codexClaimant(s, claims); by != "" {
			n.leaveClaimedCodex(s, by)
			continue
		}
		n.codexClaimEnded(s)
		n.ensureCodexRemote(ctx, s)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for id := range r.claimed {
		if !slices.ContainsFunc(servers, func(s proto.CodexServer) bool { return s.ID == id }) {
			delete(r.claimed, id)
		}
	}
}

// releaseClaimedCodex closes the connections to the instances a runtime claims now, as runCodex's next round would, up
// to 5 s later: a runtime just said hello with claims, and from its answer on the node calls nothing on them. The
// instances it has no connection to are left to that round.
func (n *Node) releaseClaimedCodex() {
	r := n.codices
	if r == nil {
		return
	}
	r.attach.Lock()
	defer r.attach.Unlock()
	claims := n.resolveCodexClaims(n.runtimes.heldCodexClaims())
	if len(claims) == 0 {
		return
	}
	r.mu.Lock()
	connected := make([]proto.CodexServer, 0, len(r.remotes))
	for _, c := range r.remotes {
		connected = append(connected, c.spec)
	}
	r.mu.Unlock()
	for _, s := range connected {
		if by := n.codexClaimant(s, claims); by != "" {
			n.leaveClaimedCodex(s, by)
		}
	}
}

// leaveClaimedCodex: runtime by claims instance s. Its connection, if there is one, closes, and its threads leave the
// list with it as when the app-server goes away; its backoff is forgotten, so it is dialled at once when the claim
// ends. Callers hold codices.attach.
func (n *Node) leaveClaimedCodex(s proto.CodexServer, by string) {
	r := n.codices
	r.mu.Lock()
	c := r.remotes[s.ID]
	delete(r.remotes, s.ID) // c's run no longer counts its end as a failed attempt (codex_backoff.go)
	delete(r.retries, s.ID)
	was := r.claimed[s.ID]
	if r.claimed == nil {
		r.claimed = map[string]string{}
	}
	r.claimed[s.ID] = by
	r.mu.Unlock()
	if c != nil {
		c.close()
	}
	if was != by {
		n.logf("codex %s: runtime %s serves it and claims it (codex_claims); not attaching while the claim holds", s.ID, by)
	}
}

// codexClaimEnded: no runtime claims instance s now; the log says so if one did. Callers hold codices.attach.
func (n *Node) codexClaimEnded(s proto.CodexServer) {
	r := n.codices
	r.mu.Lock()
	by, was := r.claimed[s.ID]
	delete(r.claimed, s.ID)
	r.mu.Unlock()
	if was {
		n.logf("codex %s: runtime %s no longer claims it; attaching again", s.ID, by)
	}
}
