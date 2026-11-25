package fleetcount

import "github.com/nwverse/persona/server/internal/domainkit/defect"

// State is where an agent's turn stands. The zero State is no state; Build refuses it.
type State struct{ state uint8 }

// StateWorking is an agent at work.
func StateWorking() State { return State{state: 1} }

// StateNeedsYou is an agent waiting for the owner.
func StateNeedsYou() State { return State{state: 2} }

// StateFinished is an agent whose turn ended.
func StateFinished() State { return State{state: 3} }

// StateIdle is an agent with no turn.
func StateIdle() State { return State{state: 4} }

// Role is whose conversation an agent is. The zero Role is no role; Build refuses it.
type Role struct{ role uint8 }

// RoleWorker is an agent doing a task.
func RoleWorker() Role { return Role{role: 1} }

// RoleAssistant is the assistant's own conversation.
func RoleAssistant() Role { return Role{role: 2} }

// Lineage says whether an agent is a subtask. The zero Lineage is none; Build refuses it.
type Lineage struct{ lineage uint8 }

// LineageRoot is an agent nobody started.
func LineageRoot() Lineage { return Lineage{lineage: 1} }

// LineageSubtask is an agent another agent started.
func LineageSubtask() Lineage { return Lineage{lineage: 2} }

// Muting says whether the owner muted an agent. The zero Muting is none; Build refuses it.
type Muting struct{ muting uint8 }

// MutingHeard is an agent the owner hears from.
func MutingHeard() Muting { return Muting{muting: 1} }

// MutingMuted is an agent the owner muted.
func MutingMuted() Muting { return Muting{muting: 2} }

// Surface is where a Needs you number is shown. The zero Surface is none; AgentsNeedsYou raises a defect on it.
type Surface struct{ surface uint8 }

// SurfaceBadge is the app icon's badge.
func SurfaceBadge() Surface { return Surface{surface: 1} }

// SurfaceLiveActivity is the Live Activity.
func SurfaceLiveActivity() Surface { return Surface{surface: 2} }

// SurfaceLink is Persona Link's menu.
func SurfaceLink() Surface { return Surface{surface: 3} }

// Why is why a row needs the owner. The zero Why is none; a Tally never holds it.
type Why struct{ why uint8 }

// WhyNeedsYou is a row waiting for the owner's answer.
func WhyNeedsYou() Why { return Why{why: 1} }

// WhyHeld is a row held back until the owner is available.
func WhyHeld() Why { return Why{why: 2} }

// Counted says whether a row for this reason counts in the number.
func (w Why) Counted() bool { return w.why == 1 }

// WhyVisitor is told which reason it is.
type WhyVisitor interface {
	VisitNeedsYou()
	VisitHeld()
}

// Accept tells v the reason; the zero Why is a defect.
func (w Why) Accept(v WhyVisitor) {
	switch w.why {
	case 1:
		v.VisitNeedsYou()
	case 2:
		v.VisitHeld()
	default:
		defect.Raise("fleetcount.Why.Accept", "the zero Why")
	}
}

// RowCount is how many rows a tally has. The zero RowCount is no rows.
type RowCount struct{ rowCount int }

// ParseRowCount is n rows; a negative n is a defect.
func ParseRowCount(n int) RowCount {
	if n < 0 {
		defect.Raise("fleetcount.ParseRowCount", "a negative count")
	}
	return RowCount{rowCount: n}
}

// Wire is the count as the wire carries it. Adapters only.
func (r RowCount) Wire() int { return r.rowCount }
