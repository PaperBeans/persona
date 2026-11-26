package assistant

import (
	"context"
	"math"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Owner-only timed cards (secret_run password cards, secret_env / secret_handoff / secret_store
// hand-off cards, and any card carrying secret fields) must not expire before the user opens and
// views them in an app. While unseen they stay pending in "Needs you" up to ownerCardMaxWait (24h).
// Once the user views the card (via POST /api/v1/interactions/{id}/seen, WebSocket interaction_seen,
// or opening its session), the short countdown (10m) begins, capped by the unseen maximum.

var (
	// ownerCardMaxWait caps how long an unseen owner-only card can remain pending so stale cards
	// do not linger indefinitely (variable so tests can shorten it).
	ownerCardMaxWait = 24 * time.Hour
	// ownerCardDefaultSeenWait is the fallback post-view countdown when a card does not carry one.
	ownerCardDefaultSeenWait = 10 * time.Minute
)

// ownerOnlyTimedCard reports whether ix can only be completed by the user themselves and should
// defer its short expiry countdown until the user actually views the card in an app.
func ownerOnlyTimedCard(ix proto.Interaction) bool {
	return ix.ToolName == proto.SecretRunTool ||
		ix.ToolName == proto.SecretHandoffTool ||
		ix.Secret ||
		hasSecretField(ix)
}

// ownerCardSeenTTL returns the exact post-view countdown duration for an owner-only card.
func ownerCardSeenTTL(ix proto.Interaction) time.Duration {
	switch ix.ToolName {
	case proto.SecretRunTool:
		if secretRunCardWait > 0 {
			return secretRunCardWait
		}
	case proto.SecretHandoffTool:
		if handoffCardWait > 0 {
			return handoffCardWait
		}
	}
	if ix.SeenTTLSeconds > 0 {
		return time.Duration(ix.SeenTTLSeconds) * time.Second
	}
	return ownerCardDefaultSeenWait
}

func secondsCeil(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return max(1, int(math.Ceil(d.Seconds())))
}

// applyOwnerCardDeadline configures an unseen owner-only card so its initial ExpiresAt is the
// maximum unseen cap (CreatedAt + ownerCardMaxWait) and returns the exact post-view countdown.
func applyOwnerCardDeadline(ix proto.Interaction, now time.Time) (proto.Interaction, time.Duration) {
	if !ownerOnlyTimedCard(ix) || persistentOwnerProposal(ix) {
		return ix, 0
	}
	ttl := ownerCardSeenTTL(ix)
	if ttl > 0 && ix.SeenTTLSeconds <= 0 {
		ix.SeenTTLSeconds = secondsCeil(ttl)
	}
	if ix.SeenAt != nil {
		return ix, ttl
	}
	if ix.CreatedAt.IsZero() {
		ix.CreatedAt = now.UTC()
	}
	ix.ExpiresAt = ix.CreatedAt.Add(ownerCardMaxWait)
	return ix, ttl
}

// markInteractionSeen records that the user opened/viewed pending card id in an app. For an
// unseen owner-only timed card this starts its short post-view countdown (capped by the existing
// max deadline), wakes await to reset its timer, and republishes the updated card to the hub.
// Calling it more than once is idempotent and never resets an already-running countdown.
func (s *Service) markInteractionSeen(ctx context.Context, id string) bool {
	if id == "" {
		return false
	}
	var updated proto.Interaction
	changed := false
	s.fleet.mu.Lock()
	p := s.fleet.interactions[id]
	if p == nil || p.claimed || !ownerOnlyTimedCard(p.ix) {
		s.fleet.mu.Unlock()
		return false
	}
	if p.ix.SeenAt == nil {
		now := time.Now().UTC()
		p.ix.SeenAt = &now
		ttl := p.seenTTL
		if ttl <= 0 {
			ttl = ownerCardSeenTTL(p.ix)
		}
		if ttl > 0 {
			next := now.Add(ttl)
			if p.ix.ExpiresAt.IsZero() || next.Before(p.ix.ExpiresAt) {
				p.ix.ExpiresAt = next
			}
		}
		updated = p.ix
		changed = true
		if p.deadlineChanged != nil {
			select {
			case p.deadlineChanged <- struct{}{}:
			default:
			}
		}
	}
	s.fleet.mu.Unlock()
	if changed {
		s.tellCard(s.withTask(updated))
	}
	return true
}

// markSessionOwnerCardsSeen marks any unseen owner-only timed cards belonging to (machine, session)
// as seen when the user opens that session in an app.
func (s *Service) markSessionOwnerCardsSeen(ctx context.Context, machine, session string) {
	if machine == "" || session == "" {
		return
	}
	var ids []string
	s.fleet.mu.Lock()
	for id, p := range s.fleet.interactions {
		if !p.claimed && p.ix.MachineID == machine && p.ix.SessionID == session && ownerOnlyTimedCard(p.ix) && p.ix.SeenAt == nil {
			ids = append(ids, id)
		}
	}
	s.fleet.mu.Unlock()
	for _, id := range ids {
		s.markInteractionSeen(ctx, id)
	}
}
