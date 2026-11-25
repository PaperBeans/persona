package assistant

import (
	"context"

	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// stopProgressCard accepts the existing button payload only while the callback's
// actual message still belongs to a current, watched Feishu run. History and thread
// bindings cannot grant this ownership. Owner/chat authorization remains in extmgr.
func (s *Service) stopProgressCard(ctx context.Context, f FeishuFeatures, t botTarget, mid string) stopOutcome {
	l := s.botLang(ctx)
	if err := botAllowed(l, f, t); err != nil {
		return stopOutcome{kind: "error", message: err.Error()}
	}
	expired := stopOutcome{kind: "info", message: i18n.T(l, "assistant-feishu_cards.card_expired")}
	if mid == "" || t.MachineID == "" || t.SessionID == "" {
		return expired
	}
	c := &s.chans
	c.mu.Lock()
	var run *channel.Run
	var prog *chanLive
	for _, r := range c.st.Runs {
		if r.Channel == channel.Feishu && botTargetOf(r) == t && r.Ext["card"] == mid {
			run, prog = r, c.live[r.ID]
			break
		}
	}
	c.mu.Unlock()
	if run == nil || prog == nil {
		return expired
	}
	rt, err := s.resolveBotTarget(ctx, t)
	if err != nil || rt.isNew || rt.machine != t.MachineID || rt.session.SessionID != t.SessionID {
		return expired
	}
	// Match the lifecycle's lock order (progress, then channel state). Resolution
	// can yield to a finish, fold, recall or replacement; recheck the actual objects.
	prog.mu.Lock()
	defer prog.mu.Unlock()
	c.mu.Lock()
	current := false
	for _, r := range c.st.Runs {
		if r == run {
			current = c.live[r.ID] == prog && r.Channel == channel.Feishu && botTargetOf(r) == t &&
				r.Ext["card"] == mid && !r.Recalled && !c.answering[r.ID] && !prog.finished && prog.failing.IsZero() &&
				prog.stoppedCard != mid && (!turnOver(rt.session) || !r.Seen && !r.Held)
			break
		}
	}
	c.mu.Unlock()
	if !current {
		return expired
	}
	// Keep the progress lock through delivery: concurrent presses act once, while a
	// failed RPC stays retryable. This is an assistant-side ownership check; the
	// node's conversation-level RPC has no atomic turn-generation precondition.
	out := s.interruptBotConversation(ctx, rt)
	if out.kind == "success" {
		prog.stoppedCard = mid
	}
	return out
}

// stopOutcome is what interrupting a conversation's current turn came to; the caller renders it as a command note
// (/stop) or a card toast (the progress card's Stop button).
type stopOutcome struct {
	kind    string // toast kind: success | info | error
	message string
}

// turnOver says whether the conversation's last turn ended (its result unseen or already seen) and nothing is being
// opened: no turn to stop. An interrupt there would only press Escape into an idle agent's terminal (the node's
// session.interrupt sends that key, node/node.go) and the owner would be told a turn was stopped that had already
// finished. A state that cannot be told is not over: the owner asked to stop, so it is tried.
func turnOver(s proto.Session) bool {
	return !s.Starting && (s.State == proto.StateUnread || s.State == proto.StateIdle)
}

// turnPending says whether this chat sent a message into the conversation that has not been seen working, or seen to
// end a turn, since: the conversation may still be read as quiet (channel.Run.Seen), so its state cannot say the turn
// is over. A /stop right after the message is exactly the case it is sent for.
func (s *Service) turnPending(machine, session string) bool {
	if session == "" {
		return false
	}
	for _, r := range s.channelRuns(channel.Feishu) {
		if r.MachineID == machine && r.SessionID == session && !r.Seen {
			return true
		}
	}
	return false
}

func (s *Service) interruptBotConversation(ctx context.Context, rt resolvedTarget) stopOutcome {
	l := s.botLang(ctx)
	if err := s.call(ctx, rt.machine, "session.interrupt", map[string]any{"session_id": rt.session.SessionID}, nil); err != nil {
		return stopOutcome{kind: "error", message: i18n.T(l, "assistant-feishu_cards.not_stopped", i18n.Args{"error": clip(oneLine(err.Error()), 200)})}
	}
	return stopOutcome{kind: "success", message: i18n.T(l, "assistant-feishu_cards.stopped", i18n.Args{"label": rt.label})}
}
