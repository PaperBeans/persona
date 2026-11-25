package node

import (
	"strings"
	"testing"
)

// A result line with a field of another type than the node reads (a gateway's token count as a string) used to be
// dropped whole, and the turn it ended stayed working (#14351). Its type and words are decoded all the same.
func TestAssistantResultWithAnUnreadableFieldStillEndsTheTurn(t *testing.T) {
	n := assistantNode(t)
	p := &assistantProc{id: "11111111-2222-3333-4444-555555555555", busy: true}
	n.readAssistant(p, strings.NewReader(`{"type":"result","subtype":"success","is_error":false,"result":"done","modelUsage":{"m":{"inputTokens":"12","contextWindow":200000.5}}}`+"\n"))
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.busy {
		t.Fatal("the turn is still working after its result")
	}
	if p.turnError != "" {
		t.Fatalf("a successful result left the turn error %q", p.turnError)
	}
}

// Any other line that does not decode is still dropped: a message_start with an unreadable usage starts nothing.
func TestAssistantOtherUnreadableLinesAreStillDropped(t *testing.T) {
	n := assistantNode(t)
	p := &assistantProc{id: "11111111-2222-3333-4444-555555555555"}
	n.readAssistant(p, strings.NewReader(`{"type":"stream_event","event":{"type":"message_start","message":{"usage":{"input_tokens":"5"}}}}`+"\n"+`{"type":"result"`+"\n"))
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.busy {
		t.Fatal("a line that did not decode marked the conversation busy")
	}
}
