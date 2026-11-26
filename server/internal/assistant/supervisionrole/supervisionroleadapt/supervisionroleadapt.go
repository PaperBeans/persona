package supervisionroleadapt

import (
	"github.com/nwverse/persona/server/internal/assistant/supervisionrole"
	"github.com/nwverse/persona/server/internal/domainkit/opt"
)

// Record is a supervision role as the manager stores it: a direction's role_in_effect and each value of the
// assistant.supervision_roles setting hold its word, "main", "leader", "background" or "side".
type Record struct{ Role string }

// Restore is the role a stored word names. An empty word, or one this manager does not know (a newer manager's, or
// another spelling), names none, and its readers fail closed rather than pick a role for it.
func Restore(r Record) opt.Option[supervisionrole.Role] {
	switch r.Role {
	case "main":
		return opt.Some(supervisionrole.RoleMain())
	case "leader":
		return opt.Some(supervisionrole.RoleLeader())
	case "background":
		return opt.Some(supervisionrole.RoleBackground())
	case "side":
		return opt.Some(supervisionrole.RoleSide())
	}
	return opt.None[supervisionrole.Role]()
}

// Store is x as stored.
func Store(x supervisionrole.Role) Record {
	var w roleWord
	x.Accept(&w)
	return Record{Role: w.word}
}

// roleWord is a role as the store spells it.
type roleWord struct{ word string }

func (x *roleWord) VisitMain()       { x.word = "main" }
func (x *roleWord) VisitLeader()     { x.word = "leader" }
func (x *roleWord) VisitBackground() { x.word = "background" }
func (x *roleWord) VisitSide()       { x.word = "side" }
