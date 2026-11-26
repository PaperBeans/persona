package assistant

import (
	"context"

	"github.com/nwverse/persona/server/internal/botcard"
	"github.com/nwverse/persona/server/internal/proto"
)

// The agents' cards are the lark module's (its asks.go and ask_cards.go, #13404). The tests here stay black-box
// tests of the assistant: they drive the interaction port (larkhost.Asks) with the real core behind it, so what a
// card says of the core (who asks, the task, the conversation's page) and where it goes are checked end to end. They
// keep the names the code had in the core; each needs the module (needsLarkLayer).

// The cards' limits the tests name (the lark module's ask_cards.go).
const (
	maxFormQuestions = 6    // more questions than this are answered in the app
	maxPlanRunes     = 5000 // runes of a plan shown on its card
)

// askCards are the cards as the module lays them out, beside the port: the layer's own methods for these tests.
type askCards interface {
	AskCard(ctx context.Context, ix proto.Interaction) *botcard.Card
	AnsweredCard(ctx context.Context, ix proto.Interaction, how string) *botcard.Card
}

// interactionCard is the card asking ix, as the chat would post it.
func (s *Service) interactionCard(ctx context.Context, ix proto.Interaction) *botcard.Card {
	return s.larkModule().(askCards).AskCard(ctx, ix)
}

// answeredCard is ix's card once it is over.
func (s *Service) answeredCard(ctx context.Context, ix proto.Interaction, how string) *botcard.Card {
	return s.larkModule().(askCards).AnsweredCard(ctx, ix, how)
}

// feishuInteraction: a new card (interaction.announced), posted before it returns.
func (s *Service) feishuInteraction(ix proto.Interaction) { s.larkModule().AskAnnounced(ix) }

// feishuInteractionDone: a card is over.
func (s *Service) feishuInteractionDone(ix proto.Interaction, how string) {
	s.larkModule().AskEnded(ix, how, false)
}

// postInteractionCard posts ix's card whatever the switches say.
func (s *Service) postInteractionCard(ctx context.Context, ix proto.Interaction) {
	s.larkModule().AskPost(ctx, ix)
}

// feishuCardWanted: the bot posts a card for ix.
func (s *Service) feishuCardWanted(ctx context.Context, ix proto.Interaction) bool {
	return s.larkModule().AskWanted(ctx, ix)
}

// feishuDetach: the cards ids are about to end because their node went away.
func (s *Service) feishuDetach(ids []string) { s.larkModule().AskDetached(ids, resumeGrace) }
