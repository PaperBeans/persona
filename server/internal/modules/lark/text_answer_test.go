package lark

import (
	"context"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// A question card closed into the ended cards in a topic (#5461) is read once: free words written there after it are
// not taken as its answer, and the card is forgotten whatever the words were. (The words starting a turn and nothing
// being resolved is the assistant's TestTypedFreeAnswerInTopic.)
func TestEndedCardIsReadOnce(t *testing.T) {
	s := newTestService(t, newFakeHub())
	ctx := context.Background()
	free := proto.Interaction{ID: "ix_free", MachineID: "server", SessionID: "m1", Kind: proto.IxQuestion,
		Questions: []proto.Question{{Question: "为什么？"}}}
	card := botCard{MessageID: "om_ix_free", Thread: "om_topic", Ix: free, At: time.Unix(1000, 0)}
	s.chat().book(ctx, func(b chatBook) {
		b.KeepCard(free.ID, &card)
		b.KeepCard(free.ID, nil) // it ended elsewhere: its card is closed (feishuInteractionEnded)
		b.NoteEnded(card)
	})
	c := s.chat()
	if _, ok := c.fbot().ended["ix_free"]; !ok {
		t.Fatal("the ended card was not kept for a late answer")
	}
	if c.botAnswerQuestion(ctx, "om_in3", "帮我看看别的", "oa_1", "om_topic", "") {
		t.Fatal("free words were taken as the ended card's answer")
	}
	if _, left := c.fbot().ended["ix_free"]; left {
		t.Fatal("the ended card was not forgotten after reading it once")
	}
}
