package item

import (
	"slices"

	"github.com/nwverse/persona/server/internal/domainkit/defect"
	"github.com/nwverse/persona/server/internal/domainkit/fail"
	"github.com/nwverse/persona/server/internal/domainkit/instant"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
)

// Command is what someone asks of an item, named for the action. The zero Command is nil; Decide raises a defect on
// it.
type Command interface {
	decide(i Item, now instant.Now) outcome.Of[Decided, Fail]
}

// markDone marks an open item done.
type markDone struct{ by Doer }

// reopen opens a done item again.
type reopen struct{}

// CommandMarkDone marks the item done, by the owner or by a review.
func CommandMarkDone(by Doer) Command {
	if by == (Doer{}) {
		defect.Raise("item.CommandMarkDone", "the zero Doer")
	}
	return markDone{by: by}
}

// CommandReopen opens a done item again.
func CommandReopen() Command { return reopen{} }

// Event is what a decision changed. The zero Event is nil; only Decide makes one.
type Event interface {
	Accept(v EventVisitor)
	event()
}

// EventVisitor is told what changed.
type EventVisitor interface {
	VisitMarkedDone(at instant.Instant, by Doer)
	VisitReopened()
}

// markedDone is an item marked done.
type markedDone struct {
	at instant.Instant
	by Doer
}

// reopened is an item opened again.
type reopened struct{}

func (e markedDone) Accept(v EventVisitor) { v.VisitMarkedDone(e.at, e.by) }
func (reopened) Accept(v EventVisitor)     { v.VisitReopened() }
func (markedDone) event()                  {}
func (reopened) event()                    {}

// Decided is an item after a command, with the events that took it there. The zero Decided is no decision; only
// Decide makes one.
type Decided struct {
	item   Item
	events []Event
}

// DecidedVisitor is told the item as decided, then each event in order.
type DecidedVisitor interface {
	VisitItem(item Item)
	VisitEvent(event Event)
}

// Decide is what a command does to an item at now: the next item with its events, or why the item refuses it.
func Decide(i Item, c Command, now instant.Now) outcome.Of[Decided, Fail] {
	if c == nil {
		defect.Raise("item.Decide", "the zero Command")
	}
	return c.decide(i, now)
}

// Then decides a further command on the item as decided: both decisions' events in order, or why the item refuses
// the second.
func (d Decided) Then(c Command, now instant.Now) (out outcome.Of[Decided, Fail]) {
	next, f := outcome.Unpack(Decide(d.item, c, now))
	if f != nil {
		return out.Err(f)
	}
	return out.Ok(Decided{item: next.item, events: slices.Concat(d.events, next.events)})
}

// Accept tells v the item as decided, then each event in order.
func (d Decided) Accept(v DecidedVisitor) {
	v.VisitItem(d.item)
	for _, e := range d.events {
		v.VisitEvent(e)
	}
}

func (c markDone) decide(i Item, now instant.Now) (out outcome.Of[Decided, Fail]) {
	if !i.NeedsOwner() {
		return out.Err(refused{fail.NewRefusal(fail.NewCode("item.done.already"))})
	}
	return out.Ok(decided(i, markedDone{at: now.Stamp(), by: c.by}))
}

func (reopen) decide(i Item, _ instant.Now) (out outcome.Of[Decided, Fail]) {
	if !i.CanReopen() {
		return out.Err(refused{fail.NewRefusal(fail.NewCode("item.open.already"))})
	}
	return out.Ok(decided(i, reopened{}))
}

// decided is i after e, with e as its event.
func decided(i Item, e Event) Decided { return Decided{item: evolve(i, e), events: []Event{e}} }

// evolve is i after e. It stays unexported: only Decide applies an event, to the state it was decided on.
func evolve(i Item, e Event) Item {
	switch e := e.(type) {
	case markedDone:
		i.state = done{at: e.at, by: e.by}
	case reopened:
		i.state = open{}
	}
	return i
}
