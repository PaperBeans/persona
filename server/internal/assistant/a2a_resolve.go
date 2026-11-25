package assistant

import (
	"context"
	"slices"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// a2aResolve is the session an Agent names, as its computer lists it now (#14395): ref itself when a row has its id,
// else the one row whose aliases name it. A row's aliases are the other ids its conversation went by on that computer,
// none of them listed (proto.Session.Aliases, set by the node). An Antigravity CLI conversation is listed under its
// terminal pane's row while a pane shows it and under its own id otherwise (node/agyaliases.go), so an address an Agent
// took from a quote or from a2a_sessions while one was listed still names the conversation once the other is. ref is
// left as it is when no row names it, when two rows do, and on a computer out of A2A scope: the checks that follow
// answer for it as before.
func (s *Service) a2aResolve(ctx context.Context, cfg assistantwire.A2AConfig, ref a2aRef) a2aRef {
	if ref.Session == "" || !s.a2aMachineAllowed(ctx, cfg, ref.Machine) {
		return ref
	}
	if _, listed := s.nodeSession(ref.Machine, ref.Session); listed {
		return ref // the usual case, without copying the machine's list
	}
	found, twice := "", false
	for _, sess := range s.a2aSessionsOn(ctx, ref.Machine) {
		if sess.SessionID == ref.Session {
			return ref
		}
		if slices.Contains(sess.Aliases(), ref.Session) {
			twice = twice || found != "" && found != sess.SessionID
			found = sess.SessionID
		}
	}
	if found == "" || twice {
		return ref
	}
	s.logf("a2a: %s on %s is listed as %s now", ref.Session, ref.Machine, found)
	return a2aRef{Machine: ref.Machine, Session: found}
}
