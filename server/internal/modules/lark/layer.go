package lark

import (
	"context"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/botcard"
)

// Host is what the module's assistant layer needs of the core: the assistant's Service, through
// internal/larkhost.Host, which has the same methods (internal/modules/all checks it). Each takes and gives only what
// both sides can name: the standard library's, internal/botcard's and the contract packages'.
//
// While the outbound channel port's implementation still lives in the core (internal/assistant's feishu*.go), the
// host serves it under the port's names and Layer forwards to it; each move into this module (#13404) replaces a
// forwarding method with the code and drops what the host no longer needs to serve.
type Host interface {
	Status(ctx context.Context) (assistantwire.FeishuStatus, error)
	Features(ctx context.Context) (assistantwire.FeishuFeatures, bool)
	Post(ctx context.Context, to assistantwire.ChannelTarget, replyTo string, card *botcard.Card, fallback, key string) (string, error)
	Note(ctx context.Context, to assistantwire.ChannelTarget, replyTo, text string)
	Reply(ctx context.Context, to assistantwire.ChannelTarget, replyTo, text, key string, full bool) error
	UpdateCard(ctx context.Context, messageID string, card *botcard.Card) error
	ThreadPost(todoID string, ev assistantwire.ThreadEvent)
}

// Layer is the module's assistant layer, what the core asks of it (internal/larkhost.Layer): the outbound channel port
// through which core code reaches the owner's chat.
type Layer struct{ h Host }

// New is the layer of one manager's host.
func New(h Host) *Layer { return &Layer{h: h} }

// Status is the bot as the hub has it now: on and paired, its switches.
func (l *Layer) Status(ctx context.Context) (assistantwire.FeishuStatus, error) {
	return l.h.Status(ctx)
}

// Features are the bot's switches and whether it can post now.
func (l *Layer) Features(ctx context.Context) (assistantwire.FeishuFeatures, bool) {
	return l.h.Features(ctx)
}

// Post sends a card as a reply to replyTo, else into to's chat.
func (l *Layer) Post(ctx context.Context, to assistantwire.ChannelTarget, replyTo string, card *botcard.Card, fallback, key string) (string, error) {
	return l.h.Post(ctx, to, replyTo, card, fallback, key)
}

// Note writes a line of text under replyTo; a failure is only logged.
func (l *Layer) Note(ctx context.Context, to assistantwire.ChannelTarget, replyTo, text string) {
	l.h.Note(ctx, to, replyTo, text)
}

// Reply writes a line of text under replyTo and says whether it went.
func (l *Layer) Reply(ctx context.Context, to assistantwire.ChannelTarget, replyTo, text, key string, full bool) error {
	return l.h.Reply(ctx, to, replyTo, text, key, full)
}

// UpdateCard edits a card the bot sent.
func (l *Layer) UpdateCard(ctx context.Context, messageID string, card *botcard.Card) error {
	return l.h.UpdateCard(ctx, messageID, card)
}

// ThreadPost queues a line for a to-do's chat thread.
func (l *Layer) ThreadPost(todoID string, ev assistantwire.ThreadEvent) { l.h.ThreadPost(todoID, ev) }
