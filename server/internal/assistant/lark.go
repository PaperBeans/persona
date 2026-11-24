package assistant

import (
	"context"
	"sync"

	"github.com/nwverse/persona/server/internal/botcard"
	"github.com/nwverse/persona/server/internal/larkhost"
)

// The Feishu / Lark module's assistant layer (internal/modules/lark, docs/architecture/core-and-modules.md "Lark",
// #13404). Core code reaches the owner's chat only through the outbound channel port (s.chat(), larkhost.Outbound):
// what the channel supports (Status, Features), a card (Post), a line under a message (Note, Reply), a card edited in
// place (UpdateCard) and a line for a to-do's chat thread (ThreadPost). With the module in the build the port is the
// module's layer; without it (persona_without_lark, persona_core_only) it is larkhost.None, which posts nothing and
// says so. The port's implementation still lives in this package's feishu*.go and is served to the module through
// larkHost until it moves there.

// larkLayer is the module's layer, made once per service on first use (a service a test makes without New has one
// too).
type larkLayer struct {
	once sync.Once
	out  larkhost.Outbound
}

// chat is the outbound channel port: the lark module's layer, or larkhost.None without the module.
func (s *Service) chat() larkhost.Outbound {
	s.lark.once.Do(func() {
		s.lark.out = larkhost.None{}
		if l, ok := larkhost.New(larkHost{s}); ok {
			s.lark.out = l
		}
	})
	return s.lark.out
}

// larkHost is the core as the lark module reaches it (larkhost.Host).
type larkHost struct{ s *Service }

func (h larkHost) Status(ctx context.Context) (FeishuStatus, error) { return h.s.feishuStatus(ctx) }

func (h larkHost) Features(ctx context.Context) (FeishuFeatures, bool) {
	return h.s.feishuFeatures(ctx)
}

func (h larkHost) Post(ctx context.Context, to larkhost.Target, replyTo string, card *botcard.Card, fallback, key string) (string, error) {
	return h.s.botPostTo(ctx, to, replyTo, card, fallback, key)
}

func (h larkHost) Note(ctx context.Context, to larkhost.Target, replyTo, text string) {
	h.s.botNoteTo(ctx, to, replyTo, text)
}

func (h larkHost) Reply(ctx context.Context, to larkhost.Target, replyTo, text, key string, full bool) error {
	return h.s.botReplyTo(ctx, to, replyTo, text, key, full)
}

func (h larkHost) UpdateCard(ctx context.Context, messageID string, card *botcard.Card) error {
	return h.s.botUpdateCard(ctx, messageID, card)
}

func (h larkHost) ThreadPost(todoID string, ev larkhost.ThreadEvent) {
	h.s.threadPost(todoID, threadEvent{kind: threadEventKind(ev.Kind), agent: ev.Agent, text: ev.Text, dm: ev.DM})
}
