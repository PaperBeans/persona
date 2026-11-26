package node

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

const (
	idleCompactionDeliveryWait = 30 * time.Second
	idleCompactionRetryDelay   = 5 * time.Minute
)

type idleCompactionInputKey struct{}

type idleCompactionInput struct {
	At     time.Time
	Policy *assistantwire.IdleCompaction
	Source *proto.MessageSource
}

type idleCompactionState struct {
	Policy           assistantwire.IdleCompaction `json:"policy"`
	Since            time.Time                    `json:"since"`
	Tokens           int                          `json:"tokens"`
	Attempted        bool                         `json:"attempted"`
	AttemptStartedAt time.Time                    `json:"attempt_started_at,omitzero"`
	Retried          bool                         `json:"retried,omitempty"`
}

func (a *idleCompactionState) resetAttempt() {
	a.Attempted, a.AttemptStartedAt, a.Retried = false, time.Time{}, false
}

func (a *idleCompactionState) recordAttempt(at time.Time) {
	a.Retried = a.Attempted
	a.Attempted, a.AttemptStartedAt = true, at
}

func (a idleCompactionState) canRetry(run *proto.Compaction, now time.Time) bool {
	return !a.Retried && !a.AttemptStartedAt.IsZero() && run != nil && run.State == proto.CompactionFailed &&
		run.StartedAt.Equal(a.AttemptStartedAt) && run.FinishedAt != nil &&
		now.Sub(*run.FinishedAt) >= idleCompactionRetryDelay &&
		strings.Contains(strings.ToLower(run.Error), "summarization produced empty response")
}

// Record user activity on receipt, before typing/lifecycle locks that may be held
// by a background send waiting for compaction. A later replay cannot move it back.
func (n *Node) noteIdleCompactionInput(id string, input idleCompactionInput) {
	n.state.update(id, func(st *sessionState) bool {
		if !st.IsMain {
			return false
		}
		a := idleCompactionState{Since: input.At}
		if st.IdleCompaction != nil {
			a = *st.IdleCompaction
		}
		if input.Policy != nil && a.Policy != *input.Policy && !input.At.Before(a.Since) {
			a.Policy, a.Since = *input.Policy, input.At
			a.resetAttempt()
		}
		if input.Source == nil && input.At.After(a.Since) {
			a.Since = input.At
			a.resetAttempt()
		}
		if st.IdleCompaction != nil && a == *st.IdleCompaction {
			return false
		}
		st.IdleCompaction = &a
		return true
	})
}

// Tokens are the last real main-model request (input + both cache components),
// never result.usage/modelUsage's cumulative totals. A reduction restarts the
// idle interval; dropping below the threshold also rearms the attempt latch.
func (n *Node) recordIdleCompactionContext(id string, tokens int) {
	if tokens < 0 || !transcript.PlausibleContext(tokens, 0) {
		return
	}
	n.state.update(id, func(st *sessionState) bool {
		if !st.IsMain || st.IdleCompaction == nil || st.IdleCompaction.Tokens == tokens {
			return false
		}
		a := *st.IdleCompaction
		if tokens < a.Tokens {
			a.Since = time.Now()
			if tokens < a.Policy.ContextTokens {
				a.resetAttempt()
			}
		}
		a.Tokens = tokens
		st.IdleCompaction = &a
		return true
	})
}

// Called with typing and assistantLife held, after resuming a stopped process
// but before computing the snapshot delta or writing the unattended message.
func (n *Node) idleCompactBeforeInput(ctx context.Context, p *assistantProc) error {
	if _, a2a := ctx.Value(a2aIdleKey{}).(bool); a2a {
		p.mu.Lock()
		busy := p.busy || p.compacting
		p.mu.Unlock()
		if busy {
			return a2aBusy()
		}
		// An A2A delivery has a short deadline and cannot start compaction.
		return nil
	}
	input, ok := ctx.Value(idleCompactionInputKey{}).(idleCompactionInput)
	if !ok {
		return nil
	}
	n.noteIdleCompactionInput(p.id, input) // a newly started main chat now has a record
	c := input.Policy
	if c == nil || !c.Enabled || c.IdleMinutes < 1 || c.IdleMinutes > 1440 ||
		c.ContextTokens < 10000 || c.ContextTokens > 10000000 || input.Source == nil || p.agent != "claude" {
		return nil
	}
	p.mu.Lock()
	busy, compacting, tokens := p.busy, p.compacting, p.context
	p.mu.Unlock()
	if busy && !compacting {
		return nil
	}
	if tokens > 0 {
		n.recordIdleCompactionContext(p.id, tokens)
	}
	if !compacting {
		now := time.Now().UTC()
		start := false
		n.state.update(p.id, func(st *sessionState) bool {
			if !st.IsMain || st.IdleCompaction == nil {
				return false
			}
			a := *st.IdleCompaction
			if a.Since.IsZero() || now.Sub(a.Since) < time.Duration(c.IdleMinutes)*time.Minute || a.Tokens < c.ContextTokens {
				return false
			}
			if a.Attempted && !a.canRetry(st.Compaction, now) {
				return false
			}
			a.recordAttempt(now)
			start = true
			st.IdleCompaction = &a
			return true
		})
		if !start {
			return nil
		}
		if err := n.compactAssistantProc(ctx, p, now); err != nil {
			return err
		}
		n.logf("assistant %s: compacting before unattended delivery", short(p.id))
	} else {
		return refusal("compacting", i18n.T(i18n.From(ctx), "assistant-start.compacting"), nil)
	}

	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	wait := n.idleCompactWait
	if wait <= 0 {
		wait = idleCompactionDeliveryWait
	}
	timeout := time.NewTimer(wait)
	defer timeout.Stop()
	for {
		p.mu.Lock()
		compacting = p.compacting
		p.mu.Unlock()
		if !compacting {
			// Release the original message even after a failure. An eligible retry
			// requires a later unattended delivery.
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.exited:
			return errors.New("assistant exited during idle compaction")
		case <-timeout.C:
			return refusal("compacting", i18n.T(i18n.From(ctx), "assistant-start.compacting"), nil)
		case <-tick.C:
		}
	}
}
