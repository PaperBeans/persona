package chore

import (
	"github.com/nwverse/persona/server/internal/domainkit/defect"
	"github.com/nwverse/persona/server/internal/domainkit/instant"
)

// Priority is a priority as chore knows it: low, normal or urgent. The zero Priority is no priority; only its
// constructors make one, and Accept raises a defect on it.
type Priority struct{ priority uint8 }

// PriorityLow is the low priority.
func PriorityLow() Priority { return Priority{priority: 1} }

// PriorityNormal is the normal priority.
func PriorityNormal() Priority { return Priority{priority: 2} }

// PriorityUrgent is the urgent priority.
func PriorityUrgent() Priority { return Priority{priority: 3} }

// PriorityVisitor is told which priority it is.
type PriorityVisitor interface {
	VisitLow()
	VisitNormal()
	VisitUrgent()
}

// Accept tells v which priority it is; the zero Priority is a defect.
func (p Priority) Accept(v PriorityVisitor) {
	switch p.priority {
	case 1:
		v.VisitLow()
	case 2:
		v.VisitNormal()
	case 3:
		v.VisitUrgent()
	default:
		defect.Raise("chore.Priority.Accept", "the zero Priority")
	}
}

// Reminder is a reminder as chore knows it: silent or ring. The zero Reminder is nil; only its constructors make one.
type Reminder interface {
	Accept(v ReminderVisitor)
	reminder()
}

// ReminderVisitor is told which reminder it is, with what it holds.
type ReminderVisitor interface {
	VisitSilent()
	VisitRing(at instant.Instant)
}

// reminderSilent is the silent reminder.
type reminderSilent struct{}

// reminderRing is the ring reminder, with its at.
type reminderRing struct{ at instant.Instant }

// ReminderSilent is the silent reminder.
func ReminderSilent() Reminder { return reminderSilent{} }

// ReminderRing is the ring reminder with its at; no at is a defect.
func ReminderRing(at instant.Instant) Reminder {
	var noInstant instant.Instant
	if at == noInstant {
		defect.Raise("chore.ReminderRing", "the zero Instant")
	}
	return reminderRing{at: at}
}

func (reminderSilent) Accept(v ReminderVisitor) { v.VisitSilent() }
func (x reminderRing) Accept(v ReminderVisitor) { v.VisitRing(x.at) }
func (reminderSilent) reminder()                {}
func (reminderRing) reminder()                  {}
