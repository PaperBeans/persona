package assistant

import (
	"context"
	"maps"
	"strings"
	"time"
)

const (
	supervisionHoldsKey         = "assistant.supervision_holds"
	supervisionStallsHandledKey = "assistant.supervision_stalls_handled"
)

// supervisionStallRef identifies one stalled or no_progress stretch delivered in a supervision round.
type supervisionStallRef struct {
	Todo    string `json:"todo"`
	Kind    string `json:"kind"`
	Key     string `json:"key"`
	Machine string `json:"machine,omitempty"`
	Session string `json:"session,omitempty"`
}

type handledStall struct {
	Key string    `json:"key"`
	At  time.Time `json:"at"`
}

type supervisionHold struct {
	At      time.Time             `json:"at"`
	Visible bool                  `json:"visible,omitempty"`
	Stalled []supervisionStallRef `json:"stalled,omitempty"`
}

// supervisionEnd carries a turn's hold to the push and channel decisions after the active hold has been cleared.
type supervisionEnd struct {
	key      string
	hold     supervisionHold
	retained bool // the turn still had background work when it stopped
}

type supervisionEndKey struct{}

func (h supervisionHold) active(now time.Time) bool {
	return !h.At.IsZero() && now.Sub(h.At) <= superviseHoldMax
}

// loadSupervisionHoldsLocked reads the manager's hold document before its first use. supervise.mu is held.
func (s *Service) loadSupervisionHoldsLocked(ctx context.Context) error {
	b := &s.supervise
	if b.holdsLoaded {
		return nil
	}
	var holds map[string]supervisionHold
	if st := s.store(); st != nil {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := st.Setting(cctx, supervisionHoldsKey, &holds); err != nil {
			s.logf("supervise: load push holds: %v", err)
			return err
		}
	}
	if holds == nil {
		holds = make(map[string]supervisionHold)
	}
	b.holds, b.holdsLoaded = holds, true
	return nil
}

// saveSupervisionHoldsLocked serializes each whole-document write with the in-memory transition.
// A failed write remains dirty so the next change or supervision sweep can retry it.
func (s *Service) saveSupervisionHoldsLocked(ctx context.Context) error {
	b := &s.supervise
	if !b.holdsDirty {
		return nil
	}
	if st := s.store(); st != nil {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := st.PutSetting(cctx, supervisionHoldsKey, b.holds); err != nil {
			s.logf("supervise: save push holds: %v", err)
			return err
		}
	}
	b.holdsDirty = false
	return nil
}

// expireSupervisionHolds drops orphaned entries on startup and during the supervision sweep.
func (s *Service) expireSupervisionHolds(ctx context.Context, now time.Time) bool {
	b := &s.supervise
	b.mu.Lock()
	defer b.mu.Unlock()
	loaded := b.holdsLoaded
	if s.loadSupervisionHoldsLocked(ctx) != nil {
		return false
	}
	changed := !loaded && len(b.holds) > 0
	for id, h := range b.holds {
		if !h.active(now) {
			delete(b.holds, id)
			b.holdsDirty = true
			changed = true
		}
	}
	_ = s.saveSupervisionHoldsLocked(ctx)
	return changed
}

// stallRefsOf extracts the stalled and no_progress signal stretches carried by a supervision relay message.
func stallRefsOf(msg relayMsg) []supervisionStallRef {
	signals, ok := decodeDigestSignals(msg.Signals)
	if !ok || len(signals) == 0 {
		return nil
	}
	var out []supervisionStallRef
	for _, g := range signals {
		if (g.Kind == sigStalled || g.Kind == sigNoProgress) && g.Todo != "" && g.Session != "" && g.Key != "" {
			out = append(out, supervisionStallRef{
				Todo:    g.Todo,
				Kind:    g.Kind,
				Key:     g.Key,
				Machine: g.Machine,
				Session: g.Session,
			})
		}
	}
	return out
}

func stalledHandledMapKey(todoID, kind, session string) string {
	return todoID + "|" + kind + "|" + session
}

// loadStallsHandledLocked reads handled stall stretches before first use. supervise.mu is held.
func (s *Service) loadStallsHandledLocked(ctx context.Context) error {
	b := &s.supervise
	if b.stallsHandledLoaded {
		return nil
	}
	var handled map[string]handledStall
	if st := s.store(); st != nil {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := st.Setting(cctx, supervisionStallsHandledKey, &handled); err != nil {
			s.logf("supervise: load handled stalls: %v", err)
			return err
		}
	}
	if handled == nil {
		handled = make(map[string]handledStall)
	}
	b.stallsHandled, b.stallsHandledLoaded = handled, true
	return nil
}

func (s *Service) saveStallsHandledLocked(ctx context.Context) error {
	b := &s.supervise
	if !b.stallsHandledDirty {
		return nil
	}
	if st := s.store(); st != nil {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := st.PutSetting(cctx, supervisionStallsHandledKey, b.stallsHandled); err != nil {
			s.logf("supervise: save handled stalls: %v", err)
			return err
		}
	}
	b.stallsHandledDirty = false
	return nil
}

// markStalledHandledLocked durably records that the supervisor handled these stalled/no_progress stretches.
// supervise.mu is held.
func (s *Service) markStalledHandledLocked(ctx context.Context, refs []supervisionStallRef, now time.Time) error {
	if len(refs) == 0 {
		return nil
	}
	if err := s.loadStallsHandledLocked(ctx); err != nil {
		return err
	}
	b := &s.supervise
	previous := maps.Clone(b.stallsHandled)
	changed := false
	at := now.UTC()
	for _, ref := range refs {
		if ref.Todo == "" || ref.Kind == "" || ref.Session == "" || ref.Key == "" {
			continue
		}
		k := stalledHandledMapKey(ref.Todo, ref.Kind, ref.Session)
		if cur, ok := b.stallsHandled[k]; ok && cur.Key == ref.Key {
			continue
		}
		b.stallsHandled[k] = handledStall{Key: ref.Key, At: at}
		changed = true
	}
	if !changed {
		return nil
	}
	b.stallsHandledDirty = true
	if err := s.saveStallsHandledLocked(ctx); err != nil {
		b.stallsHandled = previous
		b.stallsHandledDirty = false
		return err
	}
	return nil
}

// stalledHandled reports whether the supervisor already handled this exact stalled or no_progress stretch.
// The second return is false when persisted handling state could not be read; callers stopping an Agent fail safe.
func (s *Service) stalledHandled(ctx context.Context, todoID, kind, session, key string) (handled, known bool) {
	if todoID == "" || kind == "" || session == "" || key == "" {
		return false, true
	}
	b := &s.supervise
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := s.loadStallsHandledLocked(ctx); err != nil {
		return false, false
	}
	entry, ok := b.stallsHandled[stalledHandledMapKey(todoID, kind, session)]
	return ok && entry.Key == key, true
}

// pruneHandledStalls drops handled stall markers for closed or deleted todos.
func (s *Service) pruneHandledStalls(ctx context.Context, openIDs map[string]bool) {
	if openIDs == nil {
		return
	}
	b := &s.supervise
	b.mu.Lock()
	defer b.mu.Unlock()
	if s.loadStallsHandledLocked(ctx) != nil || len(b.stallsHandled) == 0 {
		return
	}
	changed := false
	for k := range b.stallsHandled {
		todoID, _, _ := strings.Cut(k, "|")
		if !openIDs[todoID] {
			delete(b.stallsHandled, k)
			changed = true
		}
	}
	if changed {
		b.stallsHandledDirty = true
		_ = s.saveStallsHandledLocked(ctx)
	}
}

// holdPushes records a supervision wake before dispatch so that even its first tool call can see the hold.
func (s *Service) holdPushes(ctx context.Context, id string) error {
	return s.holdPushesFor(ctx, id, relayMsg{})
}

// holdPushesFor records a supervision wake and any stalled/no_progress stretches carried by msg.
func (s *Service) holdPushesFor(ctx context.Context, id string, msg relayMsg) error {
	b := &s.supervise
	b.mu.Lock()
	if err := s.loadSupervisionHoldsLocked(ctx); err != nil {
		b.mu.Unlock()
		return err
	}
	previous, existed := b.holds[id]
	b.holds[id] = supervisionHold{At: time.Now().UTC(), Stalled: stallRefsOf(msg)}
	b.holdsDirty = true
	if err := s.saveSupervisionHoldsLocked(ctx); err != nil {
		if existed {
			b.holds[id] = previous
		} else {
			delete(b.holds, id)
		}
		b.mu.Unlock()
		return err
	}
	delete(b.sideLinks, id)
	b.mu.Unlock()
	s.publish(ctx)
	return nil
}

// releasePushes clears the hold when another message takes over the conversation.
func (s *Service) releasePushes(ctx context.Context, id string) error {
	_, err := s.clearSupervisionHold(ctx, s.supervisionHoldKey(ctx, id), time.Time{})
	return err
}

// clearSupervisionHold takes the ended turn's hold, leaving any hold installed after its end.
// A zero end time clears the conversation for a new input, including any side-supervision links.
func (s *Service) clearSupervisionHold(ctx context.Context, id string, endedAt time.Time) (supervisionHold, error) {
	b := &s.supervise
	b.mu.Lock()
	if err := s.loadSupervisionHoldsLocked(ctx); err != nil {
		b.mu.Unlock()
		return supervisionHold{}, err
	}
	h, held := b.holds[id]
	if endedAt.IsZero() {
		delete(b.sideLinks, id)
	} else if h.At.After(endedAt) {
		h, held = supervisionHold{}, false
	}
	if held {
		delete(b.holds, id)
		b.holdsDirty = true
	}
	err := s.saveSupervisionHoldsLocked(ctx)
	b.mu.Unlock()
	if held {
		s.publish(ctx)
	}
	if !h.active(time.Now()) {
		h = supervisionHold{}
	}
	return h, err
}

func (s *Service) endSupervisionTurn(ctx context.Context, id string, at time.Time, continuing bool) supervisionEnd {
	key := s.supervisionHoldKey(ctx, id)
	if !continuing {
		hold, _ := s.clearSupervisionHold(ctx, key, at)
		return supervisionEnd{key: key, hold: hold}
	}
	b := &s.supervise
	b.mu.Lock()
	defer b.mu.Unlock()
	if hold := b.holds[key]; hold.active(at) && !hold.At.After(at) {
		return supervisionEnd{key: key, hold: hold, retained: true}
	}
	return supervisionEnd{}
}

// supervising reports whether a supervision message started the conversation's current turn.
func (s *Service) supervising(id string) bool {
	b := &s.supervise
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.holds[id].active(time.Now())
}

func (s *Service) heldConversations() []string {
	b := &s.supervise
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	out := make([]string, 0, len(b.holds))
	for id, hold := range b.holds {
		if hold.active(now) {
			out = append(out, id)
		}
	}
	return out
}
