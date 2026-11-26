package supervisionrole

import "github.com/nwverse/persona/server/internal/domainkit/defect"

// Role is the supervision role the manager authorized for a conversation: main, leader, background or side. It is
// comparable. The zero Role is no role; only its constructors make one, and every method raises a defect on it.
type Role struct{ role uint8 }

// RoleMain is the owner's main chat.
func RoleMain() Role { return Role{role: 1} }

// RoleLeader is a direction's leader.
func RoleLeader() Role { return Role{role: 2} }

// RoleBackground is a background supervisor.
func RoleBackground() Role { return Role{role: 3} }

// RoleSide is a side supervisor: a supervising conversation that is no main chat, leader or background supervisor.
func RoleSide() Role { return Role{role: 4} }

// RoleVisitor is told which role it is.
type RoleVisitor interface {
	VisitMain()
	VisitLeader()
	VisitBackground()
	VisitSide()
}

// Accept tells v which role it is; the zero Role is a defect.
func (x Role) Accept(v RoleVisitor) {
	switch x.role {
	case 1:
		v.VisitMain()
	case 2:
		v.VisitLeader()
	case 3:
		v.VisitBackground()
	case 4:
		v.VisitSide()
	default:
		defect.Raise("supervisionrole.Role.Accept", "the zero Role")
	}
}

// IsLeader says whether x is a direction's leader, whose policy carries the leader's own paragraphs.
func (x Role) IsLeader() bool {
	x.known("supervisionrole.Role.IsLeader")
	return x == RoleLeader()
}

// IsBackground says whether x is a background supervisor, whose policy carries the background supervisor's own
// paragraphs. The main chat and a side supervisor are neither leader nor background: they carry only the common policy.
func (x Role) IsBackground() bool {
	x.known("supervisionrole.Role.IsBackground")
	return x == RoleBackground()
}

// known raises a defect when x is the zero Role, which no method takes; op names the method it was handed to.
func (x Role) known(op string) {
	var zero Role
	if x == zero {
		defect.Raise(op, "the zero Role")
	}
}
