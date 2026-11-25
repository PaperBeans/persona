package chore

import (
	"github.com/nwverse/persona/server/internal/domainkit/defect"
	"github.com/nwverse/persona/server/internal/domainkit/fail"
	"github.com/nwverse/persona/server/internal/domainkit/instant"
	"github.com/nwverse/persona/server/internal/domainkit/opt"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
)

// Chore is one chore as chore knows it, open or closed: it holds when it closed only while it is closed.
// The zero Chore is no chore; only Build makes one, and every method raises a defect on it.
type Chore struct {
	id       ChoreID
	title    Title
	effort   Effort
	snooze   Snooze
	priority Priority
	closed   opt.Option[instant.Instant]
}

// ChoreTitleStep is the builder's stage for the title. The zero ChoreTitleStep is a skipped stage, and Build refuses
// what it leaves out.
type ChoreTitleStep struct{ choreTitleStep Chore }

// ChoreEffortStep is the builder's stage for the effort. The zero ChoreEffortStep is a skipped stage, and Build refuses
// what it leaves out.
type ChoreEffortStep struct{ choreEffortStep Chore }

// ChoreSnoozeStep is the builder's stage for the snooze. The zero ChoreSnoozeStep is a skipped stage, and Build refuses
// what it leaves out.
type ChoreSnoozeStep struct{ choreSnoozeStep Chore }

// ChorePriorityStep is the builder's stage for the priority. The zero ChorePriorityStep is a skipped stage, and Build
// refuses what it leaves out.
type ChorePriorityStep struct{ chorePriorityStep Chore }

// ChoreBuilder is the builder's last stage. The zero ChoreBuilder is a skipped stage, and Build refuses what it leaves
// out.
type ChoreBuilder struct{ choreBuilder Chore }

// NewChoreBuilder starts a chore: its identity, then one stage per part.
func NewChoreBuilder(id ChoreID) ChoreTitleStep {
	return ChoreTitleStep{choreTitleStep: Chore{id: id}}
}

// Title is the chore's title.
func (stage ChoreTitleStep) Title(title Title) ChoreEffortStep {
	c := stage.choreTitleStep
	c.title = title
	return ChoreEffortStep{choreEffortStep: c}
}

// Effort is the chore's effort.
func (stage ChoreEffortStep) Effort(effort Effort) ChoreSnoozeStep {
	c := stage.choreEffortStep
	c.effort = effort
	return ChoreSnoozeStep{choreSnoozeStep: c}
}

// Snooze is the chore's snooze.
func (stage ChoreSnoozeStep) Snooze(snooze Snooze) ChorePriorityStep {
	c := stage.choreSnoozeStep
	c.snooze = snooze
	return ChorePriorityStep{chorePriorityStep: c}
}

// Priority is the chore's priority.
func (stage ChorePriorityStep) Priority(priority Priority) ChoreBuilder {
	c := stage.chorePriorityStep
	c.priority = priority
	return ChoreBuilder{choreBuilder: c}
}

// Build is the chore, open, or every part a skipped stage or a value that is none left out, in order.
func (b ChoreBuilder) Build() (out outcome.Of[Chore, Fail]) {
	c := b.choreBuilder
	if parts := c.wrongParts(); len(parts) > 0 {
		return out.Err(invalidParts(parts))
	}
	return out.Ok(c)
}

// RestoreChore is a stored chore: the parts b holds, and when it closed (none while it is open). Only adapters call it;
// it refuses what Build refuses, and a closing time that is unknown.
func RestoreChore(b ChoreBuilder, closed opt.Option[instant.Instant]) (out outcome.Of[Chore, Fail]) {
	c := b.choreBuilder
	c.closed = closed
	if parts := c.wrongParts(); len(parts) > 0 {
		return out.Err(invalidParts(parts))
	}
	return out.Ok(c)
}

// wrongParts is every part of c a rule refuses, in order.
func (c Chore) wrongParts() []fail.Invalid {
	var noChoreID ChoreID
	var noTitle Title
	var noEffort Effort
	var noPriority Priority
	var parts []fail.Invalid
	parts = wrong(parts, c.id != noChoreID, "chore.chore.no_id", "id")
	parts = wrong(parts, c.title != noTitle, "chore.chore.no_title", "title")
	parts = wrong(parts, c.effort != noEffort, "chore.chore.no_effort", "effort")
	parts = wrong(parts, c.priority != noPriority, "chore.chore.no_priority", "priority")
	c.closed.Match(func(at instant.Instant) {
		parts = wrong(parts, at.Known(), "chore.chore.closed_unknown", "closed")
	}, func() {})
	return parts
}

// known raises a defect when c is the zero Chore, which no function takes; op names the function it was handed to.
// Every constructor refuses a zero identity, so the identity tells.
func (c Chore) known(op string) {
	if c.id == (ChoreID{}) {
		defect.Raise(op, "the zero Chore")
	}
}

// ID is the chore's identity, which stores and other chores refer to it by.
func (c Chore) ID() ChoreID {
	c.known("chore.Chore.ID")
	return c.id
}

// IsSame says whether c and other are the same chore, whatever their parts.
func (c Chore) IsSame(other Chore) bool {
	c.known("chore.Chore.IsSame")
	other.known("chore.Chore.IsSame")
	return c.id == other.id
}

// CanClose says whether CommandClose applies: the chore is open.
func (c Chore) CanClose() bool {
	c.known("chore.Chore.CanClose")
	return c.closed.IsNone()
}

// CanReopen says whether CommandReopen applies: the chore is closed.
func (c Chore) CanReopen() bool {
	c.known("chore.Chore.CanReopen")
	return !c.closed.IsNone()
}

// ChoreVisitor is told the chore's identity and parts, then whether it is open or when it closed.
type ChoreVisitor interface {
	VisitChoreID(id ChoreID)
	VisitTitle(title Title)
	VisitEffort(effort Effort)
	VisitSnooze(snooze Snooze)
	VisitPriority(priority Priority)
	VisitOpen()
	VisitClosedAt(at instant.Instant)
}

// Accept tells v the chore's identity and parts, then whether it is open or when it closed.
func (c Chore) Accept(v ChoreVisitor) {
	c.known("chore.Chore.Accept")
	v.VisitChoreID(c.id)
	v.VisitTitle(c.title)
	v.VisitEffort(c.effort)
	v.VisitSnooze(c.snooze)
	v.VisitPriority(c.priority)
	c.closed.Match(v.VisitClosedAt, v.VisitOpen)
}
