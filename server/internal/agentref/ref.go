package agentref

import (
	"strings"

	"github.com/nwverse/persona/server/internal/domainkit/defect"
	"github.com/nwverse/persona/server/internal/domainkit/fail"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
)

// Fail is every failure agentref returns. The zero Fail is nil; no Parse returns it as a failure.
type Fail interface {
	fail.Fail
	agentrefFail()
}

// invalid is input that names no session or card: every part that broke a rule, in order.
type invalid struct{ fail.Invalids }

func (invalid) agentrefFail() {}

// MachineID is one of the owner's computers. The zero MachineID is no machine; ParseMachineID and ParseRef never make
// one, and OnMachine raises a defect on it.
type MachineID struct{ machine string }

// SessionID is one session on a machine. The zero SessionID is no session; ParseRef never makes one, and only a Ref
// holds one: a session id names nothing without its machine.
type SessionID struct{ session string }

// InteractionID is one pending card. The zero InteractionID is no card; ParseInteractionID never makes one.
type InteractionID struct{ interaction string }

// Ref is one session on one machine. It is comparable, so it keys maps. The zero Ref is no session; ParseRef never
// makes one, every method raises a defect on it, and where a session may be absent the type is opt.Option[Ref].
type Ref struct {
	machine MachineID
	session SessionID
}

// RefVisitor is told the machine and the session a Ref names.
type RefVisitor interface {
	VisitRef(machine MachineID, session SessionID)
}

// ParseRef is the session a machine id and a session id name; it reports every empty part, in order.
func ParseRef(machine, session string) (out outcome.Of[Ref, Fail]) {
	var parts []fail.Invalid
	parts = machineRule(parts, machine)
	parts = wrong(parts, strings.TrimSpace(session) != "", "agentref.session.empty", "session")
	if len(parts) > 0 {
		return out.Err(invalidParts(parts))
	}
	return out.Ok(Ref{machine: MachineID{machine: machine}, session: SessionID{session: session}})
}

// ParseMachineID is the computer a machine id names; an empty id is invalid.
func ParseMachineID(raw string) (out outcome.Of[MachineID, Fail]) {
	if parts := machineRule(nil, raw); len(parts) > 0 {
		return out.Err(invalidParts(parts))
	}
	return out.Ok(MachineID{machine: raw})
}

// ParseInteractionID is the card an id names; an empty id is invalid.
func ParseInteractionID(raw string) (out outcome.Of[InteractionID, Fail]) {
	if parts := wrong(nil, strings.TrimSpace(raw) != "", "agentref.card.empty", "card"); len(parts) > 0 {
		return out.Err(invalidParts(parts))
	}
	return out.Ok(InteractionID{interaction: raw})
}

// OnMachine says whether r is a session on m.
func (r Ref) OnMachine(m MachineID) bool {
	r.known("agentref.Ref.OnMachine")
	if m == (MachineID{}) {
		defect.Raise("agentref.Ref.OnMachine", "the zero MachineID")
	}
	return r.machine == m
}

// IsSame says whether r and other name the same session.
func (r Ref) IsSame(other Ref) bool {
	r.known("agentref.Ref.IsSame")
	other.known("agentref.Ref.IsSame")
	return r == other
}

// Accept tells v the machine and the session.
func (r Ref) Accept(v RefVisitor) {
	r.known("agentref.Ref.Accept")
	v.VisitRef(r.machine, r.session)
}

// known raises a defect when r is the zero Ref, which no function takes; op names the function it was handed to.
func (r Ref) known(op string) {
	if r == (Ref{}) {
		defect.Raise(op, "the zero Ref")
	}
}

// Wire is the machine's id as the wire carries it. Adapters only.
func (m MachineID) Wire() string { return m.machine }

// Wire is the session's id as the wire carries it. Adapters only.
func (s SessionID) Wire() string { return s.session }

// Wire is the card's id as the wire carries it. Adapters only.
func (i InteractionID) Wire() string { return i.interaction }

// machineRule adds what is wrong with raw as a machine id to parts.
func machineRule(parts []fail.Invalid, raw string) []fail.Invalid {
	return wrong(parts, strings.TrimSpace(raw) != "", "agentref.machine.empty", "machine")
}

// wrong adds the part to parts unless ok holds: each rule adds its own, so a caller reports every wrong part at once.
// A part names the input in agentref's words, never a wire field's.
func wrong(parts []fail.Invalid, ok bool, code, part string) []fail.Invalid {
	if ok {
		return parts
	}
	return append(parts, fail.NewInvalid(fail.NewCode(code), fail.NewPart(part)))
}

// invalidParts is the failure for the parts a rule refused; parts is never empty.
func invalidParts(parts []fail.Invalid) Fail {
	all := fail.NewInvalids(parts[0])
	for _, p := range parts[1:] {
		all = all.With(p)
	}
	return invalid{all}
}
