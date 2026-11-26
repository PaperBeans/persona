package lark

import (
	"context"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// turnOver says whether the conversation's last turn ended (its result unseen or already seen) and nothing is being
// opened: no turn to stop. An interrupt there would only press Escape into an idle agent's terminal (the node's
// session.interrupt sends that key, node/node.go) and the owner would be told a turn was stopped that had already
// finished. A state that cannot be told is not over: the owner asked to stop, so it is tried.
func turnOver(s proto.Session) bool {
	return !s.Starting && (s.State == proto.StateUnread || s.State == proto.StateIdle)
}

// stopConversation is typed /stop: it targets the conversation's current turn, independently of any progress card
// (the progress card's own press is the core's, which checks its exact ownership first). The core's TurnPending says
// whether this chat sent a message that has not been seen working, or seen to end a turn, since: the conversation may
// still be read as quiet, so its state cannot say the turn is over. A /stop right after the message is exactly the
// case it is sent for.
func (s core) stopConversation(ctx context.Context, f FeishuFeatures, t botTarget) (kind, message string) {
	l := s.botLang(ctx)
	if err := s.botAllowed(l, f, t); err != nil {
		return "error", err.Error()
	}
	rt, err := s.h.ResolveTarget(ctx, t)
	if err != nil || rt.IsNew || (turnOver(rt.Session) && !s.h.TurnPending(rt.Machine, rt.Session.SessionID)) {
		return "info", i18n.T(l, "assistant-feishu_cards.nothing_running")
	}
	return s.h.InterruptConversation(ctx, rt)
}
