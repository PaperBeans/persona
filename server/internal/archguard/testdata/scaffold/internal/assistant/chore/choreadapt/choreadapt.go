package choreadapt

import (
	"time"

	"github.com/nwverse/persona/server/internal/assistant/chore"
	"github.com/nwverse/persona/server/internal/domainkit/fail"
	"github.com/nwverse/persona/server/internal/domainkit/instant"
	"github.com/nwverse/persona/server/internal/domainkit/opt"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
)

// Record is the stored chore: plain and tagged, owned by this adapter.
type Record struct {
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Effort   int        `json:"effort"`
	Snooze   int        `json:"snooze,omitempty"`
	Priority string     `json:"priority"`
	ClosedAt *time.Time `json:"closed_at,omitempty"`
}

// Restore is the stored chore r holds: a part that does not parse fails with chore's failure, and a stored word this
// adapter does not know goes in as the zero value, which chore refuses.
func Restore(r Record) (out outcome.Of[chore.Chore, fail.Fail]) {
	id, f := outcome.Unpack(chore.ParseChoreID(r.ID))
	if f != nil {
		return out.Err(f)
	}
	title, f := outcome.Unpack(chore.ParseTitle(r.Title))
	if f != nil {
		return out.Err(f)
	}
	effort, f := outcome.Unpack(chore.ParseEffort(r.Effort))
	if f != nil {
		return out.Err(f)
	}
	snooze, f := outcome.Unpack(chore.ParseSnooze(r.Snooze))
	if f != nil {
		return out.Err(f)
	}
	return outcome.Widen(chore.RestoreChore(chore.NewChoreBuilder(id).
		Title(title).
		Effort(effort).
		Snooze(snooze).
		Priority(storedPriority(r.Priority)), closedAt(r.ClosedAt)))
}

// closedAt is when a stored chore closed: none while it is open. A stored zero time is an unknown instant, which
// Restore refuses.
func closedAt(t *time.Time) opt.Option[instant.Instant] {
	if t == nil {
		return opt.None[instant.Instant]()
	}
	return opt.Some(instant.At(*t))
}

// Store is x as stored. prev is the record x was restored from (the zero Record for a new chore): a closing time x did
// not change keeps prev's bytes, so the stored zone stays as it was.
func Store(prev Record, x chore.Chore) Record {
	r := recorder{prev: prev}
	x.Accept(&r)
	return r.record
}

// recorder fills a Record from a chore. The visitors it reads closed sets with are fields, so the nested Accept calls
// do not allocate.
type recorder struct {
	record   Record
	prev     Record
	priority priorityWord
}

func (r *recorder) VisitChoreID(id chore.ChoreID) { r.record.ID = id.Wire() }

func (r *recorder) VisitTitle(x chore.Title) { r.record.Title = x.Wire() }

func (r *recorder) VisitEffort(x chore.Effort) { r.record.Effort = x.Wire() }

func (r *recorder) VisitSnooze(x chore.Snooze) { r.record.Snooze = x.Wire() }

func (r *recorder) VisitPriority(x chore.Priority) {
	x.Accept(&r.priority)
	r.record.Priority = r.priority.word
}

func (r *recorder) VisitOpen() { r.record.ClosedAt = nil }

func (r *recorder) VisitClosedAt(at instant.Instant) {
	if r.prev.ClosedAt != nil && instant.At(*r.prev.ClosedAt).Equal(at) {
		kept := *r.prev.ClosedAt
		r.record.ClosedAt = &kept
		return
	}
	closed := time.Unix(0, at.Wire()).UTC()
	r.record.ClosedAt = &closed
}

// Log is x for a log line, and only for one: its identity.
func Log(x chore.Chore) string { return x.ID().Wire() }

// Act stores what a decision decided: the chore's record after it, from prev, written through save.
func Act(prev Record, d chore.Decided, save func(Record) error) error {
	a := actor{prev: prev}
	d.Accept(&a)
	return save(a.record)
}

// actor reads a decision: the record of what it decided, from prev.
type actor struct {
	prev   Record
	record Record
}

func (a *actor) VisitChore(x chore.Chore) { a.record = Store(a.prev, x) }

// VisitEvent is told each event the decision made. The record already holds what they changed; an effect beyond the
// store, such as a notice, is read here.
func (a *actor) VisitEvent(chore.Event) {}

// storedPriority is a stored priority as chore knows it; a word this adapter does not know is the zero Priority, which
// chore refuses.
func storedPriority(word string) chore.Priority {
	switch word {
	case "low":
		return chore.PriorityLow()
	case "normal":
		return chore.PriorityNormal()
	case "urgent":
		return chore.PriorityUrgent()
	}
	var unknown chore.Priority
	return unknown
}

// priorityWord is a priority as the store spells it.
type priorityWord struct{ word string }

func (x *priorityWord) VisitLow()    { x.word = "low" }
func (x *priorityWord) VisitNormal() { x.word = "normal" }
func (x *priorityWord) VisitUrgent() { x.word = "urgent" }
