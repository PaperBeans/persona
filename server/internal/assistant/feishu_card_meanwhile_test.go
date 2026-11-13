package assistant

import (
	"context"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// #5430: an interaction answered while its card was being made (a program that answers permission cards at once) is
// not posted to Lark; nobody needs to look at it.
func TestFeishuCardAnsweredWhileBeingMadeIsNotPosted(t *testing.T) {
	e := newThreadEnv(t, nil)
	ix := proto.Interaction{ID: "ix_auto", Kind: proto.IxPermission, MachineID: "server", SessionID: "m1", ToolName: "Bash"}
	fired, allow := false, true
	e.f.mu.Lock()
	e.f.onDevicesList = func() { // the card's "where" reads the machines: the answer lands meanwhile
		fired = true
		e.s.InteractionEnded(context.Background(), InteractionEnded{Interaction: ix, Resolution: &proto.Resolution{Allow: &allow}})
	}
	e.f.mu.Unlock()
	e.s.postInteractionCard(context.Background(), ix)
	if !fired {
		t.Fatal("the card was made without reading the machines; the test does not end the interaction meanwhile")
	}
	for _, p := range e.f.appParams(AppFeishu, FeishuPost) {
		if card, _ := p["card"].(string); strings.Contains(card, `"id":"ix_auto"`) {
			t.Fatalf("a card was posted for an interaction already answered: %v", p)
		}
	}
	e.s.fbot.mu.Lock()
	_, kept := e.s.fbot.cards[ix.ID]
	e.s.fbot.mu.Unlock()
	if kept {
		t.Fatal("the card's claim is kept for an interaction that is over")
	}
}
