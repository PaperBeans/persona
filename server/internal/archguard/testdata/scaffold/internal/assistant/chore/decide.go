package chore

import (
	"slices"

	"github.com/nwverse/persona/server/internal/domainkit/defect"
	"github.com/nwverse/persona/server/internal/domainkit/fail"
	"github.com/nwverse/persona/server/internal/domainkit/instant"
	"github.com/nwverse/persona/server/internal/domainkit/opt"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
)

// Command is what someone asks of a chore, named for the action. The zero Command is nil; Decide raises a defect on it.
type Command interface {
	decide(c Chore, now instant.Now) outcome.Of[Decided, Fail]
}

// closeCommand closes an open chore.
type closeCommand struct{}

// reopenCommand opens a closed chore again.
type reopenCommand struct{}

// CommandClose closes the chore.
func CommandClose() Command { return closeCommand{} }

// CommandReopen opens the chore again.
func CommandReopen() Command { return reopenCommand{} }

// Event is what a decision changed. The zero Event is nil; only Decide makes one.
type Event interface {
	Accept(v EventVisitor)
	event()
}

// EventVisitor is told what changed.
type EventVisitor interface {
	VisitClosed(at instant.Instant)
	VisitReopened()
}

// choreClosed is a chore closed at a moment.
type choreClosed struct{ at instant.Instant }

// choreReopened is a chore opened again.
type choreReopened struct{}

func (x choreClosed) Accept(v EventVisitor) { v.VisitClosed(x.at) }
func (choreReopened) Accept(v EventVisitor) { v.VisitReopened() }
func (choreClosed) event()                  {}
func (choreReopened) event()                {}

// Decided is a chore after a command, with the events that took it there. The zero Decided is no decision; only Decide
// makes one, always with an event, and Then and Accept raise a defect on it.
type Decided struct {
	chore  Chore
	events []Event
}

// DecidedVisitor is told the chore as decided, then each event in order.
type DecidedVisitor interface {
	VisitChore(chore Chore)
	VisitEvent(event Event)
}

// Decide is what a command does to a chore at now: the next chore with its events, or why the chore refuses it.
func Decide(c Chore, cmd Command, now instant.Now) outcome.Of[Decided, Fail] {
	c.known("chore.Decide")
	if cmd == nil {
		defect.Raise("chore.Decide", "the zero Command")
	}
	return cmd.decide(c, now)
}

// Then decides a further command on the chore as decided: both decisions' events in order, or why the chore refuses the
// second.
func (d Decided) Then(cmd Command, now instant.Now) (out outcome.Of[Decided, Fail]) {
	d.known("chore.Decided.Then")
	next, f := outcome.Unpack(Decide(d.chore, cmd, now))
	if f != nil {
		return out.Err(f)
	}
	return out.Ok(Decided{chore: next.chore, events: slices.Concat(d.events, next.events)})
}

// Accept tells v the chore as decided, then each event in order.
func (d Decided) Accept(v DecidedVisitor) {
	d.known("chore.Decided.Accept")
	v.VisitChore(d.chore)
	for _, e := range d.events {
		v.VisitEvent(e)
	}
}

// known raises a defect when d is the zero Decided, which holds no event; op names the function it was handed to.
func (d Decided) known(op string) {
	if len(d.events) == 0 {
		defect.Raise(op, "the zero Decided")
	}
}

func (closeCommand) decide(c Chore, now instant.Now) (out outcome.Of[Decided, Fail]) {
	if !c.CanClose() {
		return out.Err(refused{fail.NewRefusal(fail.NewCode("chore.chore.already_closed"))})
	}
	return out.Ok(decided(c, choreClosed{at: now.Stamp()}))
}

func (reopenCommand) decide(c Chore, _ instant.Now) (out outcome.Of[Decided, Fail]) {
	if !c.CanReopen() {
		return out.Err(refused{fail.NewRefusal(fail.NewCode("chore.chore.already_open"))})
	}
	return out.Ok(decided(c, choreReopened{}))
}

// decided is c after e, with e as its event.
func decided(c Chore, e Event) Decided { return Decided{chore: evolve(c, e), events: []Event{e}} }

// evolve is c after e. It stays unexported: only Decide applies an event, to the state it was decided on.
func evolve(c Chore, e Event) Chore {
	switch e := e.(type) {
	case choreClosed:
		c.closed = opt.Some(e.at)
	case choreReopened:
		c.closed = opt.None[instant.Instant]()
	}
	return c
}
