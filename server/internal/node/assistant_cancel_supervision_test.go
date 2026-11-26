package node

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// writeSupervision writes text as a supervision message, as assistantSend does for the manager's supervision source,
// and has claude queue it.
func writeSupervision(t *testing.T, n *Node, p *assistantProc, f *fakeClaudeIn, text string) string {
	t.Helper()
	in := assistantInput{text: text, supervision: true, content: []map[string]any{{"type": "text", "text": text}}}
	if err := p.writeInput(in); err != nil {
		t.Fatal(err)
	}
	lines := f.userLines()
	id := lines[len(lines)-1].id
	lifecycle(n, p, id, "queued")
	return id
}

func cancelledIDs(f *fakeClaudeIn) []string {
	var out []string
	for _, r := range f.requests() {
		if r["subtype"] == "cancel_async_message" {
			out = append(out, r["message_uuid"].(string))
		}
	}
	return out
}

// The manager's switch for automatic supervision messages takes back, by the structured source recorded when the message
// was written, the supervision input that has not started: the owner's own queued message stays.
func TestAssistantCancelSupervisionTakesOnlyUnstartedSupervisionInput(t *testing.T) {
	n, p, f := queueTestProc(t, "mine")
	first := writeSupervision(t, n, p, f, "督办消息 1")
	second := writeSupervision(t, n, p, f, "督办消息 2")
	started := writeSupervision(t, n, p, f, "督办消息 3")
	lifecycle(n, p, started, "started") // it began its turn: nothing to take back, and it must not be asked about

	cancelled, err := n.assistantCancelSupervision(context.Background(), queueTestID)
	if err != nil || cancelled != 2 {
		t.Fatalf("cancelled %d (%v), want the 2 that had not started", cancelled, err)
	}
	if got := cancelledIDs(f); !slices.Equal(got, []string{first, second}) {
		t.Fatalf("cancel_async_message for %v, want exactly %v by message_uuid", got, []string{first, second})
	}
	if got := queuedNow(p); !slices.Equal(got, []string{"mine"}) {
		t.Fatalf("still queued %v, want only the owner's own message", got)
	}
	// Nothing left to take: no control request is made.
	before := len(f.requests())
	if cancelled, err := n.assistantCancelSupervision(context.Background(), queueTestID); err != nil || cancelled != 0 || len(f.requests()) != before {
		t.Fatalf("second call: cancelled %d (%v), %d new request(s)", cancelled, err, len(f.requests())-before)
	}
}

// A message that Claude Code took in between the listing and the cancel stays where it went: it is not counted and is not
// written again.
func TestAssistantCancelSupervisionLeavesWhatClaudeTook(t *testing.T) {
	n, p, f := queueTestProc(t)
	taken := writeSupervision(t, n, p, f, "taken")
	waiting := writeSupervision(t, n, p, f, "waiting")
	f.taken[taken] = true
	cancelled, err := n.assistantCancelSupervision(context.Background(), queueTestID)
	if err != nil || cancelled != 1 {
		t.Fatalf("cancelled %d (%v), want 1", cancelled, err)
	}
	if got := cancelledIDs(f); !slices.Equal(got, []string{taken, waiting}) {
		t.Fatalf("asked about %v", got)
	}
	if len(f.userLines()) != 2 {
		t.Fatalf("a message was written again: %d user lines", len(f.userLines()))
	}
}

// Without a live claude process, or on a Claude Code without the control request, there is nothing to take back or the
// refusal is told, never panicked on.
func TestAssistantCancelSupervisionWithoutAQueue(t *testing.T) {
	n := assistantNode(t)
	if cancelled, err := n.assistantCancelSupervision(context.Background(), "no-such-conversation"); err != nil || cancelled != 0 {
		t.Fatalf("an unknown conversation: %d, %v", cancelled, err)
	}
	n, p, f := queueTestProc(t)
	writeSupervision(t, n, p, f, "督办消息")
	f.refuse = "unsupported control request"
	if cancelled, err := n.assistantCancelSupervision(context.Background(), queueTestID); err == nil || cancelled != 0 {
		t.Fatalf("a Claude Code without cancel_async_message: %d, %v; want the error", cancelled, err)
	}
	n, p, _ = queueTestProc(t)
	p.agent = "codex"
	if cancelled, err := n.assistantCancelSupervision(context.Background(), queueTestID); err != nil || cancelled != 0 {
		t.Fatalf("a conversation that is not claude: %d, %v", cancelled, err)
	}
}

// The node method the manager calls, through the node's own dispatch.
func TestAssistantCancelSupervisionMethod(t *testing.T) {
	n, p, f := queueTestProc(t)
	writeSupervision(t, n, p, f, "督办消息")
	raw, _ := json.Marshal(map[string]any{"session_id": queueTestID})
	out, err := n.handle(context.Background(), "assistant.cancel_supervision", raw)
	res, _ := out.(map[string]any)
	if err != nil || res["cancelled"] != 1 {
		t.Fatalf("assistant.cancel_supervision = %v, %v", out, err)
	}
	if len(queuedNow(p)) != 0 {
		t.Fatalf("still queued: %v", queuedNow(p))
	}
}

// What marks an input as supervision is the message source the manager sent with it (the structured source), never its
// words: a message sent with the supervision source is recorded as such, the owner's own message is not.
func TestAssistantSendRecordsTheSupervisionSource(t *testing.T) {
	f := cliTest(t)
	id, err := f.n.assistantSend(context.Background(), "", t.TempDir(), "claude", "hello", nil, "", "", cliExec())
	if err != nil {
		t.Fatal(err)
	}
	f.idle(t, id)
	supervised := withSupervisionDelivery(context.Background(), &proto.MessageSource{Kind: proto.SourceSupervision})
	if _, err := f.n.assistantSend(supervised, id, "", "", "督办消息", nil, "", ""); err != nil {
		t.Fatal(err)
	}
	f.idle(t, id)
	other := withSupervisionDelivery(context.Background(), &proto.MessageSource{Kind: proto.SourceConversation})
	if _, err := f.n.assistantSend(other, id, "", "", "督办消息 (said by another conversation)", nil, "", ""); err != nil {
		t.Fatal(err)
	}
	f.idle(t, id)
	if _, err := f.n.assistantSend(context.Background(), id, "", "", "owner words", nil, "", ""); err != nil {
		t.Fatal(err)
	}
	f.idle(t, id)
	p := f.proc(id)
	p.mu.Lock()
	defer p.mu.Unlock()
	var got []bool
	for _, w := range p.written {
		got = append(got, w.supervision)
	}
	if len(got) < 3 || !slices.Equal(got[len(got)-3:], []bool{true, false, false}) {
		t.Fatalf("recorded supervision flags %v, want the last three to be [true false false]", got)
	}
}
