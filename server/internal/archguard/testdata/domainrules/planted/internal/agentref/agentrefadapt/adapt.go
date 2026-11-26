// Package agentrefadapt converts between agentref and the wire's session references (assistantwire.SessionRef).
//
//persona:layer adapter
package agentrefadapt

import (
	"github.com/nwverse/persona/server/internal/agentref"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
)

// FromWire is the session a wire reference names.
func FromWire(r assistantwire.SessionRef) outcome.Of[agentref.Ref, agentref.Fail] {
	return agentref.ParseRef(r.MachineID, r.SessionID)
}

// Wire is r as the wire carries it.
func Wire(r agentref.Ref) assistantwire.SessionRef {
	var w refWire
	r.Accept(&w)
	return assistantwire.SessionRef{MachineID: w.machine, SessionID: w.session}
}

// Log is r for a log line, and only for one: "<machine>/<session>".
func Log(r agentref.Ref) string {
	var w refWire
	r.Accept(&w)
	return w.machine + "/" + w.session
}

// refWire reads a Ref's parts as the wire spells them.
type refWire struct{ machine, session string }

func (w *refWire) VisitRef(m agentref.MachineID, s agentref.SessionID) {
	w.machine, w.session = m.Wire(), s.Wire()
}
