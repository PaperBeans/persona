package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

// delayedWaitsKey holds only work that must outlive this manager process. The scheduler and task ledger remain the
// authorities for whether an item is still due; these records preserve its original deadline and claim.
const delayedWaitsKey = "assistant.delayed_waits"
const delayedWaitRecoveryDelay = time.Second

type delayedWaitKind string

const (
	delayedScheduledRetry delayedWaitKind = "scheduled_usage_limit_retry"
	delayedReceiptRecheck delayedWaitKind = "task_receipt_recheck"
	delayedFanoutWake     delayedWaitKind = "conversation_fanout_wake"
)

type delayedWait struct {
	ID      string               `json:"id"`
	Kind    delayedWaitKind      `json:"kind"`
	At      time.Time            `json:"at"`
	Claim   string               `json:"claim,omitempty"`
	Unknown bool                 `json:"unknown,omitempty"`
	Retry   *scheduledLimitRetry `json:"retry,omitempty"`
	Receipt *receiptRecheck      `json:"receipt,omitempty"`
	Fanout  *fanoutWake          `json:"fanout,omitempty"`
	// replace is local-only: persistence superseded an older binding, so its earlier map entry must not block this timer.
	replace bool
}

type delayedWaitDoc struct {
	Revision int64         `json:"revision"`
	Waits    []delayedWait `json:"waits"`
}

var (
	errDelayedWaitStore    = errors.New("delayed wait store unavailable")
	errDelayedWaitConflict = errors.New("delayed wait update kept conflicting")
)

type delayedWaitCAS interface {
	CompareSetting(context.Context, string, int64, any) (bool, error)
}

// updateDelayedWaits serializes local writers and uses the setting revision to fence other managers. repositorystore
// exposes the database's CompareSetting; the PutSetting fallback keeps small test stores compatible.
func (s *Service) updateDelayedWaits(ctx context.Context, change func(*delayedWaitDoc) (bool, error)) error {
	st := s.store()
	if st == nil {
		return errDelayedWaitStore
	}
	s.delayedMu.Lock()
	defer s.delayedMu.Unlock()
	for range 32 {
		var doc delayedWaitDoc
		if _, err := st.Setting(ctx, delayedWaitsKey, &doc); err != nil {
			return err
		}
		changed, err := change(&doc)
		if err != nil || !changed {
			return err
		}
		revision := doc.Revision
		doc.Revision = revision + 1
		if cas, ok := st.(delayedWaitCAS); ok {
			saved, err := cas.CompareSetting(ctx, delayedWaitsKey, revision, doc)
			if err != nil {
				return err
			}
			if saved {
				return nil
			}
			continue
		}
		return st.PutSetting(ctx, delayedWaitsKey, doc)
	}
	return errDelayedWaitConflict
}

func (s *Service) readDelayedWaits(ctx context.Context) (delayedWaitDoc, error) {
	var doc delayedWaitDoc
	st := s.store()
	if st == nil {
		return doc, errDelayedWaitStore
	}
	_, err := st.Setting(ctx, delayedWaitsKey, &doc)
	return doc, err
}

func (s *Service) persistScheduledRetry(ctx context.Context, w delayedWait) error {
	return s.updateDelayedWaits(ctx, func(doc *delayedWaitDoc) (bool, error) {
		for _, existing := range doc.Waits {
			if existing.ID == w.ID {
				return false, nil
			}
		}
		doc.Waits = append(doc.Waits, w)
		return true, nil
	})
}

// rescheduleDelayedWait persists a backoff before its local timer is armed. The expected deadline and pending state
// fence newer claims; a missing row is recreated only while the initial persist is still unacknowledged.
func (s *Service) rescheduleDelayedWait(ctx context.Context, w delayedWait, nextAt time.Time, allowInsert bool) (rescheduled bool, err error) {
	err = s.updateDelayedWaits(ctx, func(doc *delayedWaitDoc) (bool, error) {
		rescheduled = false
		for i := range doc.Waits {
			existing := &doc.Waits[i]
			if existing.ID != w.ID {
				continue
			}
			if existing.Claim != "" || existing.Unknown || !existing.At.Equal(w.At) {
				return false, nil
			}
			existing.At = nextAt
			rescheduled = true
			return true, nil
		}
		if !allowInsert {
			return false, nil
		}
		w.At = nextAt
		doc.Waits = append(doc.Waits, w)
		rescheduled = true
		return true, nil
	})
	return rescheduled, err
}

// persistReceiptRecheck keeps one earliest pending look per agent and binding. A new task/reopen generation replaces
// its older pending look and marks the local timer to replace its map entry too. A claimed look is separate: its handler
// may schedule the next look without the old completion deleting it. arm is false only for a duplicate of a running look.
func (s *Service) persistReceiptRecheck(ctx context.Context, w delayedWait) (selected delayedWait, arm bool, err error) {
	err = s.updateDelayedWaits(ctx, func(doc *delayedWaitDoc) (bool, error) {
		selected, arm = delayedWait{}, false
		earliest := -1
		for i, existing := range doc.Waits {
			if existing.ID == w.ID && existing.Claim != "" {
				selected = existing
				return false, nil
			}
			if existing.Kind == delayedReceiptRecheck && existing.Claim == "" && sameReceiptBinding(existing, w) &&
				(earliest < 0 || existing.At.Before(doc.Waits[earliest].At)) {
				earliest = i
			}
		}
		if earliest >= 0 && !doc.Waits[earliest].At.After(w.At) {
			selected = doc.Waits[earliest]
			arm = true
			return false, nil
		}
		kept := doc.Waits[:0]
		superseded := false
		for _, existing := range doc.Waits {
			if existing.Kind == delayedReceiptRecheck && existing.Claim == "" && sameReceiptAgent(existing, w) {
				superseded = superseded || !sameReceiptBinding(existing, w)
				continue
			}
			kept = append(kept, existing)
		}
		doc.Waits = append(kept, w)
		selected, arm = w, true
		selected.replace = superseded
		return true, nil
	})
	return selected, arm, err
}

func sameReceiptAgent(a, b delayedWait) bool {
	return a.Receipt != nil && b.Receipt != nil && a.Receipt.MachineID == b.Receipt.MachineID &&
		a.Receipt.SessionID == b.Receipt.SessionID
}

func sameReceiptBinding(a, b delayedWait) bool {
	return sameReceiptAgent(a, b) && a.Receipt.TodoID == b.Receipt.TodoID &&
		a.Receipt.LinkedAt.Equal(b.Receipt.LinkedAt) && sameOptionalTime(a.Receipt.ReopenedAt, b.Receipt.ReopenedAt)
}

func (s *Service) claimDelayedWait(ctx context.Context, id, token string, now time.Time) (claimed delayedWait, ok bool, err error) {
	err = s.updateDelayedWaits(ctx, func(doc *delayedWaitDoc) (bool, error) {
		claimed, ok = delayedWait{}, false
		for i := range doc.Waits {
			w := &doc.Waits[i]
			if w.ID != id {
				continue
			}
			claimed = *w
			if w.Claim != "" || w.Unknown || w.At.After(now) {
				return false, nil
			}
			w.Claim = token
			claimed, ok = *w, true
			return true, nil
		}
		return false, nil
	})
	return claimed, ok, err
}

// completeDelayedWait cannot remove a newer claim or a claim recovery has marked unknown.
func (s *Service) completeDelayedWait(ctx context.Context, id, token string) error {
	return s.updateDelayedWaits(ctx, func(doc *delayedWaitDoc) (bool, error) {
		for i, w := range doc.Waits {
			if w.ID == id && w.Claim == token && !w.Unknown {
				doc.Waits = append(doc.Waits[:i], doc.Waits[i+1:]...)
				return true, nil
			}
		}
		return false, nil
	})
}

// releaseDelayedWait returns a pre-dispatch claim to pending. It is never used after dispatch starts: an ambiguous
// send outcome stays claimed for conservative recovery.
func (s *Service) releaseDelayedWait(ctx context.Context, id, token string, nextAt time.Time) (released bool, err error) {
	err = s.updateDelayedWaits(ctx, func(doc *delayedWaitDoc) (bool, error) {
		released = false
		for i := range doc.Waits {
			w := &doc.Waits[i]
			if w.ID == id && w.Claim == token && !w.Unknown {
				w.Claim, w.At = "", nextAt
				released = true
				return true, nil
			}
		}
		return false, nil
	})
	return released, err
}

// markScheduledRetryUnknown fences an old worker before recovery records and reports its uncertain outcome.
func (s *Service) markScheduledRetryUnknown(ctx context.Context, id, token string) (found bool, err error) {
	err = s.updateDelayedWaits(ctx, func(doc *delayedWaitDoc) (bool, error) {
		found = false
		for i := range doc.Waits {
			w := &doc.Waits[i]
			if w.Kind == delayedScheduledRetry && w.ID == id && w.Claim == token {
				found = true
				if !w.Unknown {
					w.Unknown = true
					return true, nil
				}
				return false, nil
			}
		}
		return false, nil
	})
	return found, err
}

func (s *Service) finishUnknownScheduledRetry(ctx context.Context, id, token string) error {
	return s.updateDelayedWaits(ctx, func(doc *delayedWaitDoc) (bool, error) {
		for i, w := range doc.Waits {
			if w.Kind == delayedScheduledRetry && w.ID == id && w.Claim == token && w.Unknown {
				doc.Waits = append(doc.Waits[:i], doc.Waits[i+1:]...)
				return true, nil
			}
		}
		return false, nil
	})
}

func (s *Service) requeueClaimedReceiptRechecks(ctx context.Context) error {
	return s.updateDelayedWaits(ctx, func(doc *delayedWaitDoc) (bool, error) {
		changed := false
		for i := range doc.Waits {
			if (doc.Waits[i].Kind == delayedReceiptRecheck || doc.Waits[i].Kind == delayedFanoutWake) && doc.Waits[i].Claim != "" {
				doc.Waits[i].Claim = ""
				changed = true
			}
		}
		return changed, nil
	})
}

// persistFanoutWake keeps one unclaimed deferred wake per (sender, target), scheduling a new one at the back of
// sender's wake queue so broadcasts stay spread out over time (#14352).
func (s *Service) persistFanoutWake(ctx context.Context, sender, target, machine string, now time.Time) (selected delayedWait, err error) {
	err = s.updateDelayedWaits(ctx, func(doc *delayedWaitDoc) (bool, error) {
		selected = delayedWait{}
		for _, existing := range doc.Waits {
			if existing.Kind == delayedFanoutWake && existing.Claim == "" && !existing.Unknown &&
				existing.Fanout != nil && existing.Fanout.Sender == sender && existing.Fanout.Target == target {
				selected = existing
				return false, nil
			}
		}
		at := nextFanoutSlot(s.sendFanout.senderWindow(sender, now), doc.Waits, sender, "", time.Time{}, now)
		selected = delayedWait{
			ID:     "fanout_" + randomHex(12),
			Kind:   delayedFanoutWake,
			At:     at.UTC(),
			Fanout: &fanoutWake{Sender: sender, Target: target, MachineID: machine},
		}
		doc.Waits = append(doc.Waits, selected)
		return true, nil
	})
	return selected, err
}

// persistFanoutWakeExisting persists a fallback deferred wake whose initial save failed transiently, deduplicating
// against any unclaimed wait already persisted for the same (sender, target).
func (s *Service) persistFanoutWakeExisting(ctx context.Context, w delayedWait) (selected delayedWait, err error) {
	if w.Fanout == nil {
		return w, nil
	}
	err = s.updateDelayedWaits(ctx, func(doc *delayedWaitDoc) (bool, error) {
		selected = delayedWait{}
		for _, existing := range doc.Waits {
			if existing.ID == w.ID || (existing.Kind == delayedFanoutWake && existing.Claim == "" && !existing.Unknown &&
				existing.Fanout != nil && existing.Fanout.Sender == w.Fanout.Sender && existing.Fanout.Target == w.Fanout.Target) {
				selected = existing
				return false, nil
			}
		}
		doc.Waits = append(doc.Waits, w)
		selected = w
		return true, nil
	})
	return selected, err
}

// releaseFanoutWakeWait returns a claimed deferred wake to pending at minAt or the next back-of-queue slot for sender.
func (s *Service) releaseFanoutWakeWait(ctx context.Context, w delayedWait, token string, minAt time.Time, useQueue bool) (nextAt time.Time, released bool, err error) {
	err = s.updateDelayedWaits(ctx, func(doc *delayedWaitDoc) (bool, error) {
		nextAt, released = time.Time{}, false
		for i := range doc.Waits {
			cur := &doc.Waits[i]
			if cur.ID == w.ID && cur.Claim == token && !cur.Unknown {
				targetAt := minAt.UTC()
				if useQueue && cur.Fanout != nil {
					now := time.Now().UTC()
					slot := nextFanoutSlot(s.sendFanout.senderWindow(cur.Fanout.Sender, now), doc.Waits, cur.Fanout.Sender, cur.ID, cur.At, now)
					if slot.After(targetAt) {
						targetAt = slot
					}
				}
				cur.Claim, cur.At = "", targetAt
				nextAt, released = targetAt, true
				return true, nil
			}
		}
		return false, nil
	})
	return nextAt, released, err
}

// cancelFanoutWakeWait removes unclaimed deferred wakes for (sender, target) once stillNeeded reports that no unread
// DeferredWake message from sender remains for target. Evaluated under delayedMu so a concurrent new send is never lost.
func (s *Service) cancelFanoutWakeWait(ctx context.Context, sender, target string, stillNeeded func() bool) (removed []string, err error) {
	err = s.updateDelayedWaits(ctx, func(doc *delayedWaitDoc) (bool, error) {
		removed = nil
		if stillNeeded != nil && stillNeeded() {
			return false, nil
		}
		kept := doc.Waits[:0]
		for _, w := range doc.Waits {
			if w.Kind == delayedFanoutWake && w.Claim == "" && !w.Unknown &&
				w.Fanout != nil && w.Fanout.Sender == sender && w.Fanout.Target == target {
				removed = append(removed, w.ID)
				continue
			}
			kept = append(kept, w)
		}
		if len(removed) == 0 {
			return false, nil
		}
		doc.Waits = kept
		return true, nil
	})
	return removed, err
}

// releaseReceiptRecheck retains uncertainty, but drops the old look when its handler already scheduled a successor.
func (s *Service) releaseReceiptRecheck(ctx context.Context, w delayedWait, token string, nextAt time.Time) (bool, error) {
	released := false
	err := s.updateDelayedWaits(ctx, func(doc *delayedWaitDoc) (bool, error) {
		released = false
		index := -1
		for i, other := range doc.Waits {
			if other.ID != w.ID && other.Kind == delayedReceiptRecheck && sameReceiptAgent(other, w) {
				return false, nil
			}
			if other.ID == w.ID && other.Claim == token && !other.Unknown {
				index = i
			}
		}
		if index < 0 {
			return false, nil
		}
		doc.Waits[index].Claim = ""
		doc.Waits[index].At = nextAt
		released = true
		return true, nil
	})
	return released, err
}

// restoreDelayedWaits re-arms exact deadlines. Receipt claims are replayable through the durable turn ledger;
// scheduled claims are not, so their recovery reports an unknown outcome instead of sending again.
func (s *Service) restoreDelayedWaits(ctx context.Context) bool {
	s.delayedRecoveryMu.Lock()
	defer s.delayedRecoveryMu.Unlock()
	if s.storeFn.Load() == nil {
		return true
	}
	if s.store() == nil {
		return false
	}
	if !s.delayedReceiptsRecovered {
		if err := s.requeueClaimedReceiptRechecks(ctx); err != nil {
			s.logf("restore receipt rechecks: %v", err)
			return false
		}
		s.delayedReceiptsRecovered = true
	}
	doc, err := s.readDelayedWaits(ctx)
	if err != nil {
		s.logf("restore delayed waits: %v", err)
		return false
	}
	if s.delayedRestored == nil {
		s.delayedRestored = map[string]bool{}
	}
	restored := true
	for _, w := range doc.Waits {
		w := w
		if s.delayedRestored[w.ID] {
			continue
		}
		switch w.Kind {
		case delayedScheduledRetry:
			if w.Retry == nil {
				continue
			}
			if w.Claim != "" {
				found, err := s.markScheduledRetryUnknown(ctx, w.ID, w.Claim)
				if err != nil {
					s.logf("recover scheduled retry %s: %v", w.ID, err)
					restored = false
					continue
				}
				if found && !s.recoverScheduledLimitRetry(ctx, w) {
					restored = false
					continue
				}
				s.delayedRestored[w.ID] = true
				continue
			}
			s.delayedRestored[w.ID] = true
			go s.runScheduledLimitRetry(ctx, w, true)
		case delayedReceiptRecheck:
			if w.Receipt != nil && w.Claim == "" {
				s.delayedRestored[w.ID] = true
				s.armReceiptRecheck(w, true)
			}
		case delayedFanoutWake:
			if w.Fanout != nil && w.Claim == "" {
				s.ensureFanoutLoaded(ctx)
				s.delayedRestored[w.ID] = true
				s.armFanoutWake(w, true)
			}
		}
	}
	if restored {
		s.delayedRestored = nil
	}
	return restored
}

// delayedWaitsRecoveryLoop does not strand every persisted wait when the database is briefly unavailable at startup.
func (s *Service) delayedWaitsRecoveryLoop(ctx context.Context) {
	for {
		if s.restoreDelayedWaits(ctx) {
			return
		}
		timer := time.NewTimer(delayedWaitRecoveryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// waitForDelayedWait retains the watcher's ownership until its deadline, while a service stop leaves the durable
// record pending for the next manager.
func waitForDelayedWait(ctx context.Context, at time.Time) bool {
	timer := time.NewTimer(max(time.Until(at), 0))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func receiptRecheckWaitID(machine, sid string, at time.Time) string {
	sum := sha256.Sum256([]byte(machine + "\x00" + sid + "\x00" + at.UTC().Format(time.RFC3339Nano)))
	return "receipt_" + hex.EncodeToString(sum[:12])
}

func delayedWaitRetryAt() time.Time { return time.Now().Add(5 * time.Second) }
