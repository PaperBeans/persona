package assistant

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// The switch for automatic supervision messages (Settings.AutoSupervisionMessages, docs/assistant/unified-entry.md).
//
// It stops only the messages the assistant sends on its own, the ones that wake the main, a leader or the background
// supervisor conversation (Tool == superviseTool, and a task leader Agent's supervision message). What it never
// touches: the task ledger and every task's state, the owner's own questions and acceptance (task_review and the
// owner's cards), permission cards and the answers to them, and the safety checks. With it off nothing is sent, queued
// or scheduled; what was gathered or queued is withdrawn once, and turning it back on takes only events that happen
// afterwards: it does not replay what was withdrawn.

// errSupervisionSuppressed is what a delivery step answers when the switch is off at the moment of sending. It is a
// third outcome, neither a delivery nor a failure: nothing was sent, nothing is retried, the owner is not pushed and
// no receipt, dedup record or acceptance is written for it.
var errSupervisionSuppressed = errors.New("automatic supervision messages are off: the message was suppressed")

// relaySuppressed reports whether err is the suppressed outcome.
func relaySuppressed(err error) bool { return errors.Is(err, errSupervisionSuppressed) }

// autoSupervisionOf is the switch in v: unset is on, so every assistant that never saw it behaves as before.
func autoSupervisionOf(v Settings) bool {
	return v.AutoSupervisionMessages == nil || *v.AutoSupervisionMessages
}

// autoSupervisionOn reports whether automatic supervision messages are on. A settings read that fails serves the last
// good ones (settings), and none at all reads as on.
func (s *Service) autoSupervisionOn(ctx context.Context) bool {
	if s.storeAccessor() == nil {
		return true // a Service that was never set up has no settings to turn anything off
	}
	return autoSupervisionOf(s.settings(ctx))
}

// supervisionWithdrawn is what one withdrawal took out.
type supervisionWithdrawn struct {
	Queued   int // supervision messages taken off the relay queues (or marked withdrawn when a send is in flight)
	Pending  int // gathered signals, in memory or in the durable outbox
	Deferred int // failed starts held back by the failed-start cooldown
}

func (w supervisionWithdrawn) any() bool { return w.Queued+w.Pending+w.Deferred > 0 }

// nodeCancelEvery is how often the manager asks one node conversation again to cancel the supervision input still
// waiting in its queue while the switch is off.
const nodeCancelEvery = 20 * time.Second

// supervisionOff is the switch's memory-only state in superviseBook.
type supervisionOff struct {
	mu     sync.Mutex
	was    bool                 // the last sync found it off
	unsave bool                 // a withdrawal changed the relay queue but its write failed: write again
	cancel map[string]time.Time // machine|session → when the node was last asked to cancel its waiting supervision input
}

// syncAutoSupervision applies the switch: off withdraws what automatic supervision holds (idempotent, cheap when
// there is nothing), on forgets the off state. The saved settings change goes through settingsChanged, a restart
// and the watch's tick come here too, so a switch left off is applied on whichever happens first. It reports the switch.
func (s *Service) syncAutoSupervision(ctx context.Context) bool {
	off := &s.supervise.off
	if s.autoSupervisionOn(ctx) {
		off.mu.Lock()
		off.was = false
		off.mu.Unlock()
		return true
	}
	off.mu.Lock()
	first := !off.was
	off.was = true
	off.mu.Unlock()
	w := s.withdrawSupervision(ctx, first)
	if first || w.any() {
		s.logf("supervise: automatic supervision messages are off: withdrew %d queued message(s), %d gathered signal(s), %d deferred failed start(s)",
			w.Queued, w.Pending, w.Deferred)
	}
	if first {
		s.cancelNodeSupervision(ctx, "", nil)
	}
	if w.Queued > 0 {
		s.publish(ctx)
	}
	return false
}

// withdrawSupervision takes out of every place the switch must empty: the gathered signals and their timer, the durable
// outbox, the supervision messages in the relay queues and (deferred) the failed starts held for the cooldown. A send already in
// flight is not cut off; its head stays marked withdrawn, so the drain that sent it neither retries it nor replays it
// (sendRelay refuses a send that has not gone yet). Nothing it takes out is sent later.
func (s *Service) withdrawSupervision(ctx context.Context, deferred bool) supervisionWithdrawn {
	var out supervisionWithdrawn
	b := &s.supervise
	b.mu.Lock()
	b.init()
	out.Pending += len(b.pending)
	clear(b.pending)
	clear(b.batch)
	clear(b.queued)
	if b.timer != nil {
		b.timer.Stop()
	}
	b.timer, b.timerAt = nil, time.Time{}
	b.noMainSince, b.noMainLogged = time.Time{}, time.Time{}
	b.mu.Unlock()

	r := &s.relays
	r.mu.Lock()
	if s.relayLocked(ctx) {
		out.Pending += len(r.st.SupervisePending)
		changed := r.st.SupervisePending != nil
		r.st.SupervisePending = nil
		for to, queue := range r.st.Queue {
			kept := make([]relayMsg, 0, len(queue))
			for i, m := range queue {
				switch {
				case m.Tool != superviseTool:
					kept = append(kept, m)
				case i == 0 && r.draining[to] || m.Withdrawn || !m.DoubtedAt.IsZero():
					// A send in flight (or whose answer was lost) still owns its outcome: keep the head so a
					// late-visible line is recognised, but with nothing left to replay.
					if empty := encodeDigestSignals([]superviseSignal{}); !m.Withdrawn || m.Signals != empty {
						m.Withdrawn, m.Signals = true, empty
						changed = true
					}
					kept = append(kept, m)
				default:
					out.Queued++
					changed = true
				}
			}
			if len(kept) == 0 {
				delete(r.st.Queue, to)
			} else {
				r.st.Queue[to] = kept
			}
		}
		off := &s.supervise.off
		off.mu.Lock()
		unsaved := off.unsave
		off.mu.Unlock()
		if changed || unsaved {
			err := s.saveRelayLocked()
			off.mu.Lock()
			off.unsave = err != nil
			off.mu.Unlock()
		}
	}
	r.mu.Unlock()

	// Failed starts held for a cooldown would come back on the first digest after the switch is turned on again. Only
	// the first withdrawal reads that document: nothing adds to it while the switch is off.
	if !deferred {
		return out
	}
	if err := s.updateFailedStartBatches(ctx, func(doc *failedStartBatchDoc) (bool, error) {
		changed := false
		for _, batch := range doc.Batches {
			if n := len(batch.Deferred); n > 0 {
				out.Deferred += n
				batch.Deferred = nil
				changed = true
			}
		}
		return changed, nil
	}); err != nil {
		s.logf("supervise: could not clear the deferred failed starts: %v", err)
	}
	return out
}

// cancelNodeSupervision asks the nodes to cancel the supervision input their conversations still hold unstarted
// (assistant.cancel_supervision). Given a machine and its session list, it covers those conversations; with none, every
// machine the assistant uses. It only reaches claude conversations with a waiting queue, and a turn that has started is
// never taken back.
func (s *Service) cancelNodeSupervision(ctx context.Context, machine string, list []proto.Session) {
	if machine != "" {
		s.cancelNodeSupervisionOn(ctx, machine, list)
		return
	}
	v := s.settings(ctx)
	seen := map[string]bool{}
	for _, m := range []string{v.MachineID, v.MainMachineID, v.BackgroundSupervisorMachineID} {
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		sessions, err := s.sessions(ctx, m)
		if err != nil {
			continue
		}
		s.cancelNodeSupervisionOn(ctx, m, sessions)
	}
}

func (s *Service) cancelNodeSupervisionOn(ctx context.Context, machine string, list []proto.Session) {
	off := &s.supervise.off
	now := time.Now()
	for _, c := range list {
		if !c.Assistant || len(c.Queued) == 0 {
			continue
		}
		key := machine + "|" + c.SessionID
		off.mu.Lock()
		if last, ok := off.cancel[key]; ok && now.Sub(last) < nodeCancelEvery {
			off.mu.Unlock()
			continue
		}
		if off.cancel == nil {
			off.cancel = map[string]time.Time{}
		}
		off.cancel[key] = now
		off.mu.Unlock()
		var res struct {
			Cancelled int `json:"cancelled"`
		}
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		err := s.call(cctx, machine, deviceAssistantCancelSupervision, map[string]any{"session_id": c.SessionID}, &res)
		cancel()
		switch {
		case err != nil:
			// An older node does not know the method: its waiting input then goes in as it always did.
			s.logf("supervise: could not cancel the waiting supervision input of %s: %v", short(c.SessionID), err)
		case res.Cancelled > 0:
			s.logf("supervise: cancelled %d waiting supervision input(s) in %s", res.Cancelled, short(c.SessionID))
		}
	}
}

// deviceAssistantCancelSupervision is the node method that cancels a conversation's supervision input that waits
// unstarted in its queue, by its structured message source (docs/protocol/protocol.md).
const deviceAssistantCancelSupervision = "assistant.cancel_supervision"
