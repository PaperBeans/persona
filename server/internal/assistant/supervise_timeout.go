package assistant

import (
	"context"
	"fmt"
	"slices"
	"time"
)

// supervisorRelayEscalateAfter bounds how long an actionable supervision relay waits for an assigned Assistant
// conversation that is not in the node's session inventory. Working conversations are present and keep their queue;
// tried or doubtful sends keep the existing transcript-reconciliation path instead of risking a duplicate.
var supervisorRelayEscalateAfter = 30 * time.Minute

type staleSupervisorRelay struct {
	to  string
	msg relayMsg
}

// escalateStaleSupervisorRelays covers queues whose recipient cannot appear in an ordinary session-list drain.
// Rechecking the original signal before replacing it drops resolved work, while the queue rewrite moves every affected
// signal and removes its source atomically. No task ownership changes: main is asked to handle the undeliverable notice.
func (s *Service) escalateStaleSupervisorRelays(ctx context.Context, now time.Time) {
	here := s.settings(ctx)
	if here.Main == "" || here.MachineID == "" {
		return
	}
	b := &s.relays
	b.mu.Lock()
	if !s.relayLocked(ctx) {
		b.mu.Unlock()
		return
	}
	var stale []staleSupervisorRelay
	for to, queue := range b.st.Queue {
		if to == here.Main || to == here.BackgroundSupervisor || b.draining[to] || b.replacing[to] {
			continue
		}
		for _, msg := range queue {
			if msg.Tool != superviseTool || msg.Withdrawn || !msg.TriedAt.IsZero() || !msg.DoubtedAt.IsZero() ||
				msg.Looks != 0 || !msg.LookAfter.IsZero() || msg.Due.After(now) || !b.doubted[relayMsgID(to, msg)].IsZero() {
				continue
			}
			signals, structured := decodeDigestSignals(msg.Signals)
			if !structured || len(signals) == 0 || !slices.ContainsFunc(signals, func(g superviseSignal) bool { return g.waitsOnDecision() }) {
				continue // informational-only messages never become a new decision for main
			}
			queuedAt := msg.QueuedAt
			if queuedAt.IsZero() {
				queuedAt = msg.At // queues written before QueuedAt was persisted still have their original timestamp
			}
			if queuedAt.IsZero() || now.Sub(queuedAt) < supervisorRelayEscalateAfter {
				continue
			}
			stale = append(stale, staleSupervisorRelay{to: to, msg: msg})
		}
	}
	b.mu.Unlock()
	if len(stale) == 0 {
		return
	}
	list, err := s.sessions(ctx, here.MachineID)
	if err != nil {
		// Unknown inventory is not proof that a supervisor disappeared. Keep the durable source queue for the next pass.
		return
	}
	listed := make(map[string]bool, len(list))
	for _, c := range list {
		listed[c.SessionID] = true
	}
	changed := false
	for _, candidate := range stale {
		if listed[candidate.to] {
			continue // idle, working, starting and background-work conversations all remain reachable
		}
		to := candidate.to
		_, _, keep := s.recheckQueuedDigestAt(ctx, to, candidate.msg, true, func(g superviseSignal) (superviseSignal, bool) {
			if !g.waitsOnDecision() {
				return g, false // keep progress and other informational entries with their assigned supervisor
			}
			if s.recheckDigestTarget(ctx, here, g) != to {
				return g, false // a reassignment follows current routing, not the old recipient's timeout
			}
			why := fmt.Sprintf("Its assigned supervisor conversation %s could not receive this supervision within %s; take over the worker's follow-up (%s: %s).",
				short(to), supervisorRelayEscalateAfter, g.Kind, clip(oneLine(g.Detail), 300))
			return superviseSignal{Todo: g.Todo, Kind: sigDecide, To: here.Main, Unreachable: true, Recipient: to,
				Key: "supervisor-unreachable@" + g.Todo + "@" + g.Kind + "@" + g.Key, Detail: why, At: g.At}, true
		})
		changed = changed || !keep
	}
	if changed {
		// A missing source has no list event of its own. Deliver to main (or a newly assigned recipient) from this list.
		s.drainRelays(here.MachineID, list)
	}
}
