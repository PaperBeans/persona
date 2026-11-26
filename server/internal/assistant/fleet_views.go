package assistant

import "github.com/nwverse/persona/server/internal/proto"

// attachViews puts each row of machine's list its five-phase view (Session.view, docs/protocol/agent-states.md "Five
// phases", #15397) before the list is told: fleetcount's Agents list over the rows and the pending cards the apps see
// decides which count in Needs you, and agentview the phase, reason and rank. The rows are the list's own copies; a
// view is written copy-on-write (Session.SetView). A child folded under a row on another computer is decided with its
// own computer's rows, as each machine's list is told on its own. sentMu is held (publishList).
func (s *Service) attachViews(machine string, list []proto.Session) {
	cards, _ := s.visiblePendingCards()
	s.fleet.views.Attach(list, func(sess *proto.Session) bool {
		return s.mutedAs(machine, sess.SessionID, sess.SpaceName)
	}, cards)
}

// attachArchivedViews is attachViews for the archived rows told apart from the list (tellArchived): a hub that takes
// them apart shows them with the same views as one that gets them in the list. sentMu is held.
func (s *Service) attachArchivedViews(machine string, rows []proto.Session) {
	cards, _ := s.visiblePendingCards()
	s.fleet.archViews.Attach(rows, func(sess *proto.Session) bool {
		return s.mutedAs(machine, sess.SessionID, sess.SpaceName)
	}, cards)
}
