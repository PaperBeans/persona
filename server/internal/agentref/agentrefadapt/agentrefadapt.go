package agentrefadapt

import (
	"github.com/nwverse/persona/server/internal/agentref"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/domainkit/opt"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
	"github.com/nwverse/persona/server/internal/proto"
)

// FromSession is the session a row of a computer's list is; a row without a machine or a session id fails.
func FromSession(s *proto.Session) outcome.Of[agentref.Ref, agentref.Fail] {
	return agentref.ParseRef(s.MachineID, s.SessionID)
}

// FromCard is the session that asked for a pending card, or none: a card with no session_id comes from no Agent (the
// assistant's own questions, a script's ask). A card that names a session without its machine fails.
func FromCard(ix *proto.Interaction) outcome.Of[opt.Option[agentref.Ref], agentref.Fail] {
	if ix.SessionID == "" {
		var out outcome.Of[opt.Option[agentref.Ref], agentref.Fail]
		return out.Ok(opt.None[agentref.Ref]())
	}
	return outcome.Map(agentref.ParseRef(ix.MachineID, ix.SessionID), opt.Some[agentref.Ref])
}

// FromWire is the session a wire reference names.
func FromWire(r assistantwire.SessionRef) outcome.Of[agentref.Ref, agentref.Fail] {
	return agentref.ParseRef(r.MachineID, r.SessionID)
}

// Wire is r as the wire carries it, for the callers that still take the contract. The zero Ref is a defect: a session
// that may be absent is an opt.Option[agentref.Ref], and its absence is the caller's to spell on the wire.
func Wire(r agentref.Ref) assistantwire.SessionRef {
	var w refWire
	r.Accept(&w)
	return assistantwire.SessionRef{MachineID: w.machine, SessionID: w.session}
}

// Log is r for a log line, and only for one: "<machine>/<session>". The zero Ref is a defect.
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
