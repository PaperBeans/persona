package agentref

import (
	"strings"

	"github.com/nwverse/persona/server/internal/domainkit/fail"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
)

// Fail is every failure agentref returns. The zero Fail is nil; no Parse returns it as a failure.
type Fail interface {
	fail.Fail
	agentrefFail()
}

// invalid is input that names no session.
type invalid struct{ fail.Invalid }

func (invalid) agentrefFail() {}

// wrong is the failure for a part of the input that is empty.
func wrong(code, part string) Fail {
	return invalid{fail.NewInvalid(fail.NewCode(code), fail.NewPart(part))}
}

// MachineID is one of the owner's computers. The zero MachineID is no machine; ParseRef never makes one.
type MachineID struct{ machine string }

// SessionID is one session on a machine. The zero SessionID is no session; ParseRef never makes one.
type SessionID struct{ session string }

// InteractionID is one pending card. The zero InteractionID is no card; ParseInteractionID never makes one.
type InteractionID struct{ interaction string }

// Ref is one session on one machine. It is comparable, so it keys maps. The zero Ref is no session; ParseRef never
// makes one.
type Ref struct {
	machine MachineID
	session SessionID
}

// ParseRef is the session a machine id and a session id name; either one empty is invalid.
func ParseRef(machine, session string) (out outcome.Of[Ref, Fail]) {
	switch {
	case strings.TrimSpace(machine) == "":
		return out.Err(wrong("agentref.machine.empty", "machine"))
	case strings.TrimSpace(session) == "":
		return out.Err(wrong("agentref.session.empty", "session"))
	}
	return out.Ok(Ref{machine: MachineID{machine: machine}, session: SessionID{session: session}})
}

// ParseInteractionID is the card an id names; an empty id is invalid.
func ParseInteractionID(raw string) (out outcome.Of[InteractionID, Fail]) {
	if strings.TrimSpace(raw) == "" {
		return out.Err(wrong("agentref.card.empty", "card"))
	}
	return out.Ok(InteractionID{interaction: raw})
}

// RefVisitor is told the machine and the session a Ref names.
type RefVisitor interface {
	VisitRef(machine MachineID, session SessionID)
}

// OnMachine says whether r is a session on m.
func (r Ref) OnMachine(m MachineID) bool { return r.machine == m }

// IsSame says whether r and other name the same session.
func (r Ref) IsSame(other Ref) bool { return r == other }

// Accept tells v the machine and the session.
func (r Ref) Accept(v RefVisitor) { v.VisitRef(r.machine, r.session) }

// Wire is the machine's id as the wire carries it. Adapters only.
func (m MachineID) Wire() string { return m.machine }

// Wire is the session's id as the wire carries it. Adapters only.
func (s SessionID) Wire() string { return s.session }

// Wire is the card's id as the wire carries it. Adapters only.
func (i InteractionID) Wire() string { return i.interaction }
