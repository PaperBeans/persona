package node

import (
	"encoding/json"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// A runtime that runs Codex itself (Paseo, docs/integrations/paseo.md) has sessions whose conversation is a Codex thread
// with a rollout on this machine. The node finds that rollout on its own too and listed it a second time, as Codex
// history: the same conversation under another id, read-only, with the thread's own subagents under it (#4495). The
// runtime's row names the thread (runtime.sessions' codex_thread: the thread id) and the node lists the conversation
// once, as the runtime's session: the thread's history row is not listed, and its subagents hang off that session
// (codexSessionsIn). A thread another program has taken up since (a shared app-server has it loaded, the Codex App
// runs it) while the runtime's session is not running is that program's too: its own row is listed beside the
// session's, and a message from the phone goes where the thread is open. A thread is matched by its id, in whichever
// Codex home its rollout is. Not for a daemon's sessions: their threads are not on this machine.

// namesCodexThread: a row of session id names Codex thread t as its conversation.
func namesCodexThread(id, t string) bool {
	return rolloutUUID(t) && runtimeIDRe.MatchString(id)
}

// runtimeCodexThreads reads the codex_thread of each session in a runtime.sessions push: session → thread.
func runtimeCodexThreads(params json.RawMessage) map[string]string {
	var p struct {
		Sessions []struct {
			SessionID   string `json:"session_id"`
			CodexThread string `json:"codex_thread"`
		} `json:"sessions"`
	}
	if json.Unmarshal(params, &p) != nil {
		return nil
	}
	var out map[string]string
	for _, s := range p.Sessions {
		if namesCodexThread(s.SessionID, s.CodexThread) {
			if out == nil {
				out = map[string]string{}
			}
			out[s.SessionID] = s.CodexThread
		}
	}
	return out
}

// setCodexThreads replaces the Codex threads this runtime's sessions are (nil: none).
func (h *runtimeConn) setCodexThreads(m map[string]string) {
	h.mu.Lock()
	h.codexThreads = m
	h.mu.Unlock()
}

// codexClaim is the runtime session a Codex thread is listed as, and whether that session runs (is not offline).
type codexClaim struct {
	session string
	live    bool
}

// codexThreads is the session each named Codex thread is, as the runtime answering for the session lists it:
// thread → session; nil when no session names one. Two sessions naming one thread (an agent closed and another that
// took its conversation over): the one that is not offline, then the one used last, then the lower id, so the answer
// does not change from list to list. It runs for every list: what it compares is kept by value, not as a copy of
// each row.
func (b *runtimeRegistry) codexThreads() map[string]codexClaim {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var out map[string]codexClaim
	var used map[string]time.Time // thread → when the session picked for it was last used
	for _, h := range b.conns {
		h.mu.Lock()
		if len(h.codexThreads) > 0 {
			for i := range h.sessions {
				s := &h.sessions[i]
				t := h.codexThreads[s.SessionID]
				if t == "" || b.owner[s.SessionID] != h {
					continue
				}
				if out == nil {
					out, used = map[string]codexClaim{}, map[string]time.Time{}
				}
				c := codexClaim{session: s.SessionID, live: s.Status != proto.StatusOffline}
				if p, ok := out[t]; !ok || runsThreadBefore(c, s.UpdatedAt, p, used[t]) {
					out[t], used[t] = c, s.UpdatedAt
				}
			}
		}
		h.mu.Unlock()
	}
	return out
}

// runsThreadBefore: of two sessions naming one Codex thread, a (last used at) is the one the thread is listed as.
func runsThreadBefore(a codexClaim, at time.Time, b codexClaim, bt time.Time) bool {
	if a.live != b.live {
		return a.live
	}
	if !at.Equal(bt) {
		return at.After(bt)
	}
	return a.session < b.session
}
