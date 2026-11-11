package assistant

import (
	"context"
	"strconv"
	"strings"

	"github.com/nwverse/persona/server/internal/prompts"
)

// New Inbox messages about a supervised task reach its supervisor (#4293; docs/assistant/unified-entry.md
// "Supervision (#3124)" and "Message cards link to tasks"). The AI twin's pass takes new messages from other people into
// an item, and the judge links the item to one of the user's tasks (linkTaskFor). When that task is supervised, its
// supervisor gets the signal inbox through the usual digest: routed to the task's leader conversation or the main one,
// gathered, deduplicated and spaced like any other signal, and several messages about one task before the message goes
// out are one entry, the newest item's, with how many came. Off unless assistant.inbox_signals is on: each one is a
// supervisor turn the user pays for.
//
// Nothing Persona says itself raises one, so a supervisor's turn cannot feed the next: not the reply Persona sent for a
// confirmed suggestion (SentOf), not a bot chat (where Persona's own bot posts its pushes and cards), and not the twin's
// handling chat (what it is told goes into that chat, never into the Inbox). A task waiting for the owner's decision
// hears nothing, as with the other signals, and neither does a task leader's worker: a task leader is a coding agent
// working without permission prompts, and the Inbox's text is other people's.

// sigInbox: new Inbox messages from other people about the task.
const sigInbox = "inbox"

// inboxSignalsOn: assistant.inbox_signals is on (off by default).
func (s *Service) inboxSignalsOn(ctx context.Context) bool {
	v := s.settings(ctx).InboxSignals
	return v != nil && *v
}

// superviseInbox raises the inbox signal for those of items (the twin's pass just took new content into them) that are
// linked to a supervised task, while assistant.inbox_signals is on. linked are the items the judge linked in this pass:
// all they hold is new to the supervisor.
func (s *Service) superviseInbox(ctx context.Context, items []*InboxItem, linked map[*InboxItem]bool) {
	if len(items) == 0 || !s.inboxSignalsOn(ctx) {
		return
	}
	b := &s.inbox
	var out []superviseSignal
	b.mu.Lock()
	for _, it := range items {
		if g, ok := inboxSignalOf(it, linked[it]); ok {
			out = append(out, g)
		}
	}
	b.mu.Unlock()
	for _, g := range out {
		if t, ok := s.todoNow(g.Todo); ok && s.inboxSignalHolds(t) {
			s.signal(g)
		}
	}
}

// inboxSignalHolds: an inbox signal about task t is told its supervisor: t is open and supervised by a conversation, and
// does not wait for the owner's decision.
func (s *Service) inboxSignalHolds(t Todo) bool {
	v := t.Supervision
	return v != nil && t.DoneAt == nil && v.state(t) != reviewNeedsUser && !s.agentSupervised(t)
}

// inboxSignalOf is the inbox signal of item it, whose new content the twin's pass just took; false when it is linked to
// no task or its newest message is not someone else's. It counts the messages received since the last signal it gave
// (receivedSince), and remembers how far it counted. Under b.mu.
func inboxSignalOf(it *InboxItem, linked bool) (superviseSignal, bool) {
	a := it.Agent
	if a == nil || a.TodoID == "" || !it.open() || it.Kind == KindBot || a.SentOf != "" && a.SentOf == it.Last {
		return superviseSignal{}, false
	}
	n, seq := receivedSince(it, a.signaled, linked)
	a.signaled = seq
	return superviseSignal{Todo: a.TodoID, Kind: sigInbox, Key: it.ID + "@" + it.Last, Inbox: &prompts.SuperviseInbox{
		Item: it.ID, Title: clip(oneLine(it.Title), 80), Sender: clip(oneLine(it.Sender), 40), Preview: clip(oneLine(it.Preview), 140),
		Count: n}}, true
}

// receivedSince counts the messages of the item's received batches after chain entry since (the handling chain numbers
// its entries c1, c2…), at least one, and answers the newest batch's number. since 0 (nothing counted yet in this run of
// the manager): an item linked just now counts every batch its chain holds, any other only the newest, the one surely
// new.
func receivedSince(it *InboxItem, since int, linked bool) (n, seq int) {
	seq, last := since, 0
	for _, e := range it.Chain {
		k, err := strconv.Atoi(strings.TrimPrefix(e.ID, "c"))
		if e.Kind != ChainReceived || err != nil || k <= since {
			continue
		}
		seq, last = max(seq, k), max(e.Count, 1)
		n += last
	}
	if since == 0 && !linked {
		n = last
	}
	return max(n, 1), seq
}

// joinInbox is inbox signal g in the place of cur, which still waits to be told: the newest item, with the messages of
// both.
func joinInbox(cur, g superviseSignal) superviseSignal {
	if cur.Inbox == nil || g.Inbox == nil {
		return g
	}
	in := *g.Inbox
	in.Count += cur.Inbox.Count
	g.Inbox = &in
	return g
}
