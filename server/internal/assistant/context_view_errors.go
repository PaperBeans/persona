package assistant

import (
	"context"
	"strings"
	"time"
	"unicode"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/prompts"
)

// The problems of the rich views a reply drew (views_inline.go) reach the assistant with its next message, in a
// <persona-view-errors> block after the <persona-context> one (docs/assistant/assistant-views.md "Next-turn feedback"). Reading them
// for a message consumes nothing: the node acks the receipts once the message is delivered, so a read for a message
// that fails to go out (or a debug read) tells nobody, and problems recorded after the read stay for the next one.
// Everything is scoped to the conversation on the machine the hub says asked, never to what the node passed.

type viewErrorStore interface {
	PeekAssistantViewErrors(context.Context, string, string, int) ([]db.AssistantViewErrorRow, error)
	AckAssistantViewErrors(context.Context, string, string, []string, []string) (int64, error)
}

const (
	viewErrorReplies = 5   // replies per message; the rest wait for the next
	viewErrorLines   = 20  // problems per message, as many as one reply keeps (db.RecordAssistantViewErrors)
	viewErrorRunes   = 300 // per problem
)

// viewErrors is the block for conversation p.SessionID's next message and its receipts; "" when there is nothing to
// tell or the asker is unknown. A reply is told whole or not at all: only replies whose every
// problem is in the block get a receipt, so an ack never marks a problem the model was not shown. One that does not
// fit waits for the next message (the first always fits: a reply keeps at most viewErrorLines).
func (s *Service) viewErrors(ctx context.Context, p ContextParams) (string, []assistantwire.ViewErrorReceipt) {
	if p.SessionID == "" || p.MachineID == "" {
		return "", nil
	}
	st, ok := s.store().(viewErrorStore)
	if !ok {
		return "", nil
	}
	rows, err := st.PeekAssistantViewErrors(ctx, p.SessionID, p.MachineID, viewErrorReplies+1)
	if err != nil {
		s.logf("assistant view errors could not be read: %v", err)
		return "", nil
	}
	var v prompts.ViewErrors
	var receipts []assistantwire.ViewErrorReceipt
	loc := s.location(ctx)
	lines := 0
	for _, r := range rows {
		reply := prompts.ViewErrorReply{At: r.CreatedAt.In(loc).Format("01-02 15:04")}
		for _, e := range r.Errors {
			if line := viewErrorLine(e); line != "" {
				reply.Errors = append(reply.Errors, line)
			}
		}
		if len(receipts) == viewErrorReplies || (lines > 0 && lines+len(reply.Errors) > viewErrorLines) {
			v.Later = true
			break
		}
		if len(reply.Errors) > viewErrorLines { // more than a reply keeps: only from an older hub
			reply.Errors = reply.Errors[:viewErrorLines]
		}
		lines += len(reply.Errors)
		receipts = append(receipts, assistantwire.ViewErrorReceipt{ItemID: r.ItemID, Digest: r.Digest})
		if len(reply.Errors) > 0 {
			v.Replies = append(v.Replies, reply)
		}
	}
	if len(v.Replies) == 0 {
		return "", receipts // nothing worth saying: acking clears them all the same
	}
	return prompts.RenderViewErrors(v), receipts
}

// ackViewErrors marks the problems a delivered message carried as told.
func (s *Service) ackViewErrors(ctx context.Context, p ContextParams) {
	st, ok := s.store().(viewErrorStore)
	if p.SessionID == "" || p.MachineID == "" || !ok {
		return
	}
	var items, digests []string
	for _, r := range p.Ack {
		if r.ItemID != "" && r.Digest != "" && len(items) < viewErrorReplies {
			items, digests = append(items, r.ItemID), append(digests, r.Digest)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := st.AckAssistantViewErrors(ctx, p.SessionID, p.MachineID, items, digests); err != nil {
		s.logf("assistant view errors could not be acked: %v", err)
	}
}

// quotedValue is a quoted value in a check's message: a field or action name is kept, anything else (a link, HTML,
// JSON, a sentence the view had) is not repeated back to the model. One cut before its closing quote (a message kept
// only in part) runs to the end.
var quotedValue = lazyre.New(`"(?:[^"\\]|\\.)*(?:"|\\?$)`)
var plainName = lazyre.New(`^"[A-Za-z0-9_.\-]{1,40}"$`)

// longToken is a run without spaces longer than any name or word a check writes itself: a name or value of the view's.
var longToken = lazyre.New(`[^\s.:]{65,}`)

// scrubViewError is a check's message without the view's own text, on one line. It runs when the problem is recorded,
// before the store keeps only its start, and again when it is read.
func scrubViewError(e string) string {
	e = quotedValue.ReplaceAllStringFunc(e, func(q string) string {
		if plainName.MatchString(q) {
			return q
		}
		return `"…"`
	})
	e = strings.Join(strings.FieldsFunc(e, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
	e = longToken.ReplaceAllString(e, "…")
	return strings.NewReplacer("<", "‹", ">", "›").Replace(e) // cannot close or open the block it goes in
}

// viewErrorLine is one problem as told (scrubViewError; the page names and action ids were scrubbed when it was
// recorded, scrubViewNames), cut to viewErrorRunes in the middle: the reason a check gives comes last.
func viewErrorLine(e string) string {
	e = scrubViewError(e)
	if r := []rune(e); len(r) > viewErrorRunes {
		head := viewErrorRunes / 4
		e = string(r[:head]) + "…" + string(r[len(r)-(viewErrorRunes-head-1):])
	}
	return e
}
