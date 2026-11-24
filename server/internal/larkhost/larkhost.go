// Package larkhost is the seam between the core and the Feishu / Lark module's assistant layer (internal/modules/lark,
// docs/architecture/core-and-modules.md "Lark", #13404): what the core asks of the module (Layer, whose outbound
// half is the channel port Outbound) and what the module asks of the core (Host), each in terms both sides name (the
// standard library, internal/botcard and the contracts).
//
// The composition root (internal/modules/all) provides the module in the init of its file, under the module's build
// constraint; without it nothing is provided and the core reaches the owner's chat through None, a channel switched
// off: its status says neither on nor ready (no error: nothing to try again), it supports nothing, every post answers
// ErrNoChannel, and notes and thread lines are dropped.
//
// The module is provided while the program initializes, before main runs, and only read afterwards.
package larkhost

import (
	"context"
	"errors"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/botcard"
)

// ModuleID is the lark module's id (its module.json).
const ModuleID = "lark"

// Target says where a post or a note goes (assistantwire.ChannelTarget: the zero value is the owner's private chat).
type Target = assistantwire.ChannelTarget

// ThreadEvent is one thing to tell the chat thread of a to-do (assistantwire.ThreadEvent).
type ThreadEvent = assistantwire.ThreadEvent

// What a line in a to-do's chat thread tells (assistantwire.ThreadKind).
const (
	ThreadDecision      = assistantwire.ThreadDecision
	ThreadAgentFinished = assistantwire.ThreadAgentFinished
	ThreadAgentNeeds    = assistantwire.ThreadAgentNeeds
	ThreadTodoAccepted  = assistantwire.ThreadTodoAccepted
	ThreadTodoDone      = assistantwire.ThreadTodoDone
	ThreadTodoFailed    = assistantwire.ThreadTodoFailed
)

// Outbound is the outbound channel port: how core code reaches the owner's chat. Status and Features say what the
// channel supports now; Post, Note, Reply and UpdateCard send; ThreadPost queues a line for a to-do's chat thread (nothing
// for a to-do bound to none).
type Outbound interface {
	Status(ctx context.Context) (assistantwire.FeishuStatus, error)
	Features(ctx context.Context) (f assistantwire.FeishuFeatures, ready bool)
	// Post sends card as a reply to replyTo, else into to's chat; fallback is the rich text it goes as when the card
	// is refused, and key, when not "", makes a send that may be made again land once.
	Post(ctx context.Context, to Target, replyTo string, card *botcard.Card, fallback, key string) (messageID string, err error)
	// Note writes a line of text under replyTo (a failure is only logged); Reply does and says whether it went, with
	// key as Post's and full when the text is a task's whole answer.
	Note(ctx context.Context, to Target, replyTo, text string)
	Reply(ctx context.Context, to Target, replyTo, text, key string, full bool) error
	UpdateCard(ctx context.Context, messageID string, card *botcard.Card) error
	ThreadPost(todoID string, ev ThreadEvent)
}

// ErrNoChannel: this build has no chat channel (the lark module is left out).
var ErrNoChannel = errors.New("no chat channel in this build")

// None is Outbound without a channel: as a bot that is switched off.
type None struct{}

func (None) Status(context.Context) (assistantwire.FeishuStatus, error) {
	return assistantwire.FeishuStatus{}, nil
}
func (None) Features(context.Context) (assistantwire.FeishuFeatures, bool) {
	return assistantwire.FeishuFeatures{}, false
}
func (None) Post(context.Context, Target, string, *botcard.Card, string, string) (string, error) {
	return "", ErrNoChannel
}
func (None) Note(context.Context, Target, string, string)                      {}
func (None) Reply(context.Context, Target, string, string, string, bool) error { return ErrNoChannel }
func (None) UpdateCard(context.Context, string, *botcard.Card) error           { return ErrNoChannel }
func (None) ThreadPost(string, ThreadEvent)                                    {}

// Layer is what the core asks of the lark module: for now the outbound port. It grows as the assistant's Feishu
// files move into the module (#13404).
type Layer interface {
	Outbound
}

// Host is what the lark module asks of the core. While the outbound port's implementation still lives in the core
// (internal/assistant's feishu*.go), the host serves it under the same names, and the module's layer forwards to it;
// each later move takes code into the module and drops the host methods it no longer needs.
type Host interface {
	Outbound
}

var provided func(Host) Layer

// Provide provides the module: new makes its layer for one manager's host (init only).
func Provide(new func(Host) Layer) {
	if provided != nil {
		panic("larkhost: the lark module provided twice")
	}
	provided = new
}

// InBuild says whether the lark module is in this build.
func InBuild() bool { return provided != nil }

// New is the module's layer for h; false when the module is not in this build.
func New(h Host) (Layer, bool) {
	if provided == nil {
		return nil, false
	}
	return provided(h), true
}
