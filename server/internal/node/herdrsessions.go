package node

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/herdr"
	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

// Blocked named herdr sessions (#14898, node.configure herdr_sessions_blocked, nodeproto.HerdrSessionsBlocked):
// named herdr sessions (`herdr --session <name>`) kept out of Persona on this computer.
//
// When a named session is blocked:
// - herdrHost skips it in session discovery, snapshots, workspaces, lookups, and event streams;
// - hooks, standby replay, Claude Code plugin permissions, outside Claude process scans, Codex process/rollout scans,
//   and runtime rows carrying that session are skipped, and their session IDs are mapped to the session name so their
//   transcripts on disk do not fall through to the offline list either;
// - unblocking a session name immediately restores both live driving and remembered session IDs;
// - the assistant's own conversations and kept sessions remain exempt.

const (
	blockedHerdrSessionsFile = "herdr-sessions-blocked.json"
	maxHerdrBlockedIDs       = 2000
)

type blockedHerdrSessionsDisk struct {
	Blocked  []string          `json:"blocked"`
	Sessions map[string]string `json:"sessions,omitempty"`
}

// setBlockedHerdrSessions applies the hub's blocked named herdr sessions list.
func (n *Node) setBlockedHerdrSessions(names []string) {
	clean := proto.CleanBlockedHerdrSessions(names)
	n.mu.Lock()
	before := n.herdrSessionsBlocked
	newlyBlocked := map[string]bool{}
	for _, name := range clean {
		if !slices.Contains(before, name) {
			newlyBlocked[name] = true
		}
	}
	// Remember live sessions currently bound to newly blocked named herdr sessions so their transcripts on disk do not
	// immediately fall through to the offline list once herdrHost stops listing their panes.
	if len(newlyBlocked) > 0 {
		if n.herdrBlockedIDs == nil {
			n.herdrBlockedIDs = map[string]string{}
		}
		for sid, a := range n.agents {
			if sess := n.paneHerdrSession(a.PaneID); newlyBlocked[sess] {
				n.herdrBlockedIDs[sid] = sess
			}
		}
		for pane, sid := range n.panes {
			if sess := n.paneHerdrSession(pane); newlyBlocked[sess] && sid != "" {
				n.herdrBlockedIDs[sid] = sess
			}
		}
		trimHerdrBlockedIDsLocked(n.herdrBlockedIDs)
	}
	n.herdrSessionsBlocked = clean
	changed := !slices.Equal(before, clean)
	n.mu.Unlock()

	n.syncHerdrHostBlocked()
	if !changed {
		return
	}
	n.saveBlockedHerdrSessions()
	if len(newlyBlocked) > 0 {
		n.endBlockedHerdrStreams(newlyBlocked)
	}
	forgetOfflineScan()
}

// syncHerdrHostBlocked hands the node's blocked named session list to the active herdrHost (if any).
func (n *Node) syncHerdrHostBlocked() {
	if n.hosts == nil {
		return
	}
	h, ok := n.hosts.Get(host.Herdr).(*herdrHost)
	if !ok || h.named == nil {
		return
	}
	n.mu.Lock()
	blocked := slices.Clone(n.herdrSessionsBlocked)
	n.mu.Unlock()
	h.named.setBlocked(blocked)
}

func (n *Node) endBlockedHerdrStreams(blocked map[string]bool) {
	n.mu.Lock()
	var toEnd []struct {
		id string
		s  *termStream
	}
	for id, s := range n.terms {
		sess := ""
		if pane, ok := strings.CutPrefix(id, "pane:"); ok {
			sess = n.paneHerdrSession(pane)
		} else if a, ok := n.agents[id]; ok {
			sess = n.paneHerdrSession(a.PaneID)
		} else if mapped := n.herdrBlockedIDs[id]; mapped != "" {
			sess = mapped
		}
		if blocked[sess] {
			toEnd = append(toEnd, struct {
				id string
				s  *termStream
			}{id, s})
			delete(n.terms, id)
		}
	}
	n.mu.Unlock()
	for _, item := range toEnd {
		item.s.mu.Lock()
		control := item.s.ctl != nil
		item.s.mu.Unlock()
		item.s.cancel()
		if n.peer != nil {
			_ = n.peer.Notify("terminal.end", map[string]any{
				"session_id": item.id,
				"error":      "this herdr session is blocked in Persona settings",
				"control":    control,
			})
		}
	}
}

// herdrSessionBlocked reports whether named herdr session name is blocked.
func (n *Node) herdrSessionBlocked(name string) bool {
	if name == "" {
		return false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Contains(n.herdrSessionsBlocked, name)
}

// paneHerdrSession returns the named herdr session of pane ("" for the default server or non-herdr panes).
func (n *Node) paneHerdrSession(pane string) string {
	if pane == "" {
		return ""
	}
	ref := host.ParsePane(pane)
	if ref.Kind != host.Herdr {
		return ""
	}
	session, _ := herdr.SplitID(ref.ID)
	return session
}

// paneHerdrSessionBlocked reports whether pane belongs to a blocked named herdr session.
func (n *Node) paneHerdrSessionBlocked(pane string) bool {
	return n.herdrSessionBlocked(n.paneHerdrSession(pane))
}

// noteHerdrSessionID remembers that session sid ran inside named herdr session sess (while sess is blocked).
func (n *Node) noteHerdrSessionID(sid, sess string) {
	if sid == "" || sess == "" {
		return
	}
	n.mu.Lock()
	if !slices.Contains(n.herdrSessionsBlocked, sess) {
		n.mu.Unlock()
		return
	}
	if n.herdrBlockedIDs == nil {
		n.herdrBlockedIDs = map[string]string{}
	}
	if n.herdrBlockedIDs[sid] == sess {
		n.mu.Unlock()
		return
	}
	n.herdrBlockedIDs[sid] = sess
	trimHerdrBlockedIDsLocked(n.herdrBlockedIDs)
	n.mu.Unlock()
	n.saveBlockedHerdrSessions()
	forgetOfflineScan()
}

// herdrSessionIDBlocked reports whether session sid is known to belong to a currently blocked named herdr session,
// preserving exemptions for assistant conversations and kept sessions.
func (n *Node) herdrSessionIDBlocked(sid string) bool {
	if sid == "" {
		return false
	}
	n.mu.Lock()
	if len(n.herdrSessionsBlocked) == 0 {
		n.mu.Unlock()
		return false
	}
	sess := n.herdrBlockedIDs[sid]
	blocked := sess != "" && slices.Contains(n.herdrSessionsBlocked, sess)
	n.mu.Unlock()
	if !blocked && n.state != nil {
		if st, ok := n.state.get(sid); ok && st.Pane != "" {
			blocked = n.paneHerdrSessionBlocked(st.Pane)
		}
	}
	if !blocked {
		return false
	}
	return !n.isAssistantSession(sid) && !n.keptSessions()[sid]
}

// claudeProcHerdrSession reads the named herdr session of a running claude process from its environment ("" if none).
func (n *Node) claudeProcHerdrSession(c claudeProc) string {
	if paneEnvOfPID == nil || c.pid <= 0 {
		return ""
	}
	env := paneEnvOfPID(c.pid)
	if len(env) == 0 {
		return ""
	}
	return herdr.EnvSession(func(name string) string { return env[name] })
}

func trimHerdrBlockedIDsLocked(m map[string]string) {
	if len(m) <= maxHerdrBlockedIDs {
		return
	}
	for k := range m {
		delete(m, k)
		if len(m) <= maxHerdrBlockedIDs {
			return
		}
	}
}

func (n *Node) loadBlockedHerdrSessions() {
	if n.dir == "" {
		return
	}
	b, err := os.ReadFile(filepath.Join(n.dir, blockedHerdrSessionsFile))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			n.logger.Printf("blocked herdr sessions: %v", err)
		}
		return
	}
	var disk blockedHerdrSessionsDisk
	if err := json.Unmarshal(b, &disk); err != nil {
		n.logger.Printf("blocked herdr sessions: %v", err)
		return
	}
	n.mu.Lock()
	n.herdrSessionsBlocked = proto.CleanBlockedHerdrSessions(disk.Blocked)
	if len(disk.Sessions) > 0 {
		n.herdrBlockedIDs = make(map[string]string, len(disk.Sessions))
		for sid, sess := range disk.Sessions {
			if name, ok := proto.CleanHerdrSessionName(sess); ok && sid != "" {
				n.herdrBlockedIDs[sid] = name
			}
		}
	}
	n.mu.Unlock()
}

func (n *Node) saveBlockedHerdrSessions() {
	if n.dir == "" {
		return
	}
	n.mu.Lock()
	disk := blockedHerdrSessionsDisk{
		Blocked:  slices.Clone(n.herdrSessionsBlocked),
		Sessions: make(map[string]string, len(n.herdrBlockedIDs)),
	}
	for sid, sess := range n.herdrBlockedIDs {
		disk.Sessions[sid] = sess
	}
	n.mu.Unlock()
	if disk.Blocked == nil {
		disk.Blocked = []string{}
	}
	b, _ := json.Marshal(disk)
	if err := writeFileAtomic(filepath.Join(n.dir, blockedHerdrSessionsFile), b); err != nil {
		n.logger.Printf("blocked herdr sessions: %v", err)
	}
}

// The herdr.sessions.list node RPC: configurable named herdr sessions on this computer (#14898). Not bound to a
// session, so it is answered from the method table (node_methods.go), not a case in Node.handleMethod.
func init() { nodeRPC("herdr.sessions.list", (*Node).listHerdrSessions) }

// listHerdrSessions answers the herdr.sessions.list node RPC: every configurable named herdr session known to herdr on
// this computer (excluding default and persona-* data-directory sessions), sorted by name. It takes no params.
func (n *Node) listHerdrSessions(ctx context.Context, _ json.RawMessage) (any, error) {
	type item struct {
		Name    string `json:"name"`
		Running bool   `json:"running"`
	}
	var c *herdr.Client
	if n.hosts != nil {
		if h, ok := n.hosts.Get(host.Herdr).(*herdrHost); ok && h.c != nil {
			c = h.c
		}
	}
	if c == nil {
		c = herdr.New()
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	infos, err := c.ListSessions(cctx)
	if err != nil {
		return nil, err
	}
	out := make([]item, 0, len(infos))
	for _, info := range infos {
		if info.Default || herdr.IsOwnSession(info.Name) {
			continue
		}
		name, ok := proto.CleanHerdrSessionName(info.Name)
		if !ok {
			continue
		}
		out = append(out, item{Name: name, Running: info.Running})
	}
	slices.SortFunc(out, func(a, b item) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	return map[string]any{"sessions": out}, nil
}
