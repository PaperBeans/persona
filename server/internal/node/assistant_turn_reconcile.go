package node

import (
	"time"
)

// A conversation's row says working while its process is busy (assistantSessions), and only the turn's result on its
// stdout clears busy (readAssistant). Whatever kept that result from being read left the row working with no turn
// running, until the conversation's next turn ended hours later (#14351): a result line the reader could not decode, a
// message written that started no turn, a turn that ended without one. Nothing else looked: the list repeats the flag,
// and a busy process is never let go (reapable). The manager keeps the messages other conversations sent queued for a
// working row (assistant/conversations.go drainRelays), and whatever waits for the conversation to be idle waits too.
//
// reconcileAssistantTurns is that look, once a sweep (a minute): a busy process that printed nothing for
// assistantBusyQuiet is checked against Claude Code's own word, the status in the sessions/<pid>.json of the claude
// that has the conversation open (busy during a turn, a tool call that prints nothing for an hour included; idle, or
// shell with background commands running, once it ended; measured on 2.1.331 in print mode). Idle there means no
// turn runs: busy is cleared and the list goes out, as after a result. Anything else keeps the flag: a turn Claude
// Code says is running, a dialog it waits on, a compaction (its own timeout, sweepAssistants), a recovery holding a
// failed turn (assistant_recovery.go), or no file at all (an older Claude Code, a process on another machine).

// assistantBusyQuiet is how long a busy process prints nothing, and takes no message, before its turn is checked
// against Claude Code's own state. A variable so tests can shorten it.
var assistantBusyQuiet = 2 * time.Minute

// assistantClaudeStatus is the status Claude Code itself reports for conversation id (liveClaudes), "" when no live
// claude has it open. Two claudes with it open (never Persona's doing) are idle only if both are. Replaceable in tests.
var assistantClaudeStatus = func(n *Node, id string) string {
	status := ""
	for _, c := range n.liveClaudes() {
		if c.SessionID != id {
			continue
		}
		if c.Status != "idle" && c.Status != "shell" {
			return c.Status
		}
		status = c.Status
	}
	return status
}

// busyMark is what a busy process had printed and been sent when reconcileAssistantTurns looked at it.
type busyMark struct {
	out    int64
	active time.Time
}

// unheardLocked: p is held busy by a turn nothing has been heard of for assistantBusyQuiet (p.mu held). Not while a
// line is being read, a compaction runs (it has its own timeout) or a message was just written.
func (p *assistantProc) unheardLocked(now time.Time) (busyMark, bool) {
	if p.agent != "claude" || !p.busy || p.compacting || p.manual || !p.alive() || p.reading.Load() > 0 ||
		p.quiet() < assistantBusyQuiet || now.Sub(p.lastActive) < assistantBusyQuiet {
		return busyMark{}, false
	}
	return busyMark{out: p.lastOut.Load(), active: p.lastActive}, true
}

// reconcileAssistantTurns clears the busy flag of every conversation whose turn Claude Code says is over.
func (n *Node) reconcileAssistantTurns() {
	now := time.Now()
	n.mu.Lock()
	marks := map[*assistantProc]busyMark{}
	for id, p := range n.procs {
		if n.recoveries[id] != nil {
			continue // its busy is the recovery's hold on the failed turn
		}
		p.mu.Lock()
		if m, ok := p.unheardLocked(now); ok {
			marks[p] = m
		}
		p.mu.Unlock()
	}
	n.mu.Unlock()
	for p, mark := range marks {
		status := assistantClaudeStatus(n, p.id)
		if status != "idle" && status != "shell" {
			continue
		}
		// Only if nothing happened while the file was read: a line printed or a message sent since is a turn's own.
		p.mu.Lock()
		quiet := p.quiet()
		ended := p.busy && !p.compacting && !p.manual && p.reading.Load() == 0 && p.lastOut.Load() == mark.out && p.lastActive.Equal(mark.active)
		if ended {
			p.busy = false
			p.lastActive = time.Now()
			p.partial.Reset()
		}
		p.mu.Unlock()
		if !ended {
			continue
		}
		n.logf("assistant %s: listed working with no turn running (Claude Code says %s, nothing printed for %s): taken as ended", short(p.id), status, quiet.Round(time.Second))
		n.ttft.clearPending(p.id)
		n.emitDelta(p.id, "")
		n.notifySessionsSoon()
	}
}
