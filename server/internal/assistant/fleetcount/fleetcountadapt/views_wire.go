package fleetcountadapt

import (
	"maps"
	"strings"

	"github.com/nwverse/persona/server/internal/agentviewwire"
	"github.com/nwverse/persona/server/internal/assistant/agentview"
	"github.com/nwverse/persona/server/internal/proto"
)

// ViewWire is a view as Session.view carries it. The texts a reason shows (what it asks, the error's code, the
// refusal's details, when a limit resets) are read from the session the view was decided for, s.
func ViewWire(v agentview.View, s *proto.Session) *agentviewwire.AgentView {
	return pair(viewOut(v, s))
}

// viewOut is a view and its reason as the wire carries them (ViewWire); out.Reason is left for the caller to point.
func viewOut(v agentview.View, s *proto.Session) (agentviewwire.AgentView, agentviewwire.AgentReason) {
	w := viewWired{s: s}
	v.Accept(&w)
	return w.out, w.reason
}

// viewWired writes a view out.
type viewWired struct {
	s      *proto.Session
	out    agentviewwire.AgentView
	reason agentviewwire.AgentReason
}

func (w *viewWired) VisitView(phase agentview.Phase, reason agentview.Reason, counting agentview.Counting, rank agentview.Rank) {
	phase.Accept(w)
	w.reason = agentviewwire.AgentReason{}
	reason.Accept(w)
	counting.Accept(&viewCounted{to: &w.out.Counted})
	w.out.Rank = rank.Wire()
}

func (w *viewWired) VisitNeedsYou() { w.out.Phase = agentviewwire.AgentPhaseNeedsYou }
func (w *viewWired) VisitWorking()  { w.out.Phase = agentviewwire.AgentPhaseWorking }
func (w *viewWired) VisitDone()     { w.out.Phase = agentviewwire.AgentPhaseDone }
func (w *viewWired) VisitError()    { w.out.Phase = agentviewwire.AgentPhaseError }
func (w *viewWired) VisitIdle()     { w.out.Phase = agentviewwire.AgentPhaseIdle }

func (w *viewWired) VisitCards(cards agentview.CardCount, holding agentview.Holding) {
	held := viewHeldBy{}
	holding.Accept(&held)
	w.reason = agentviewwire.AgentReason{Kind: agentviewwire.AgentReasonCards, N: cards.Wire(), Held: held.held}
}
func (w *viewWired) VisitTerminalDialog() { w.reason.Kind = agentviewwire.AgentReasonTerminalDialog }
func (w *viewWired) VisitTurn()           { w.reason.Kind = agentviewwire.AgentReasonTurn }
func (w *viewWired) VisitBackground(b agentview.Background) {
	waits := viewWaitsIn{}
	b.Accept(&waits)
	w.reason = agentviewwire.AgentReason{Kind: agentviewwire.AgentReasonBackground, Waiting: waits.waits}
}
func (w *viewWired) VisitUnseen()       { w.reason.Kind = agentviewwire.AgentReasonUnseen }
func (w *viewWired) VisitJustFinished() { w.reason.Kind = agentviewwire.AgentReasonJustFinished }
func (w *viewWired) VisitAsks(seeing agentview.Seeing) {
	unseen := viewUnseenIn{}
	seeing.Accept(&unseen)
	w.reason = agentviewwire.AgentReason{Kind: agentviewwire.AgentReasonAsks, Question: strings.TrimSpace(w.s.Asks),
		Unseen: unseen.unseen}
}
func (w *viewWired) VisitUsageLimit(f agentview.Failure) {
	w.reason = agentviewwire.AgentReason{Kind: agentviewwire.AgentReasonUsageLimit,
		Resumes: f == agentview.FailureUsageLimitResuming()}
	if at := w.s.TurnErrorResetsAt; at != nil {
		w.reason.ResetsAt = at.UTC()
	}
}
func (w *viewWired) VisitModel() {
	w.reason = agentviewwire.AgentReason{Kind: agentviewwire.AgentReasonModel, Code: w.s.TurnErrorCode}
}
func (w *viewWired) VisitStartRefused() {
	w.reason = agentviewwire.AgentReason{Kind: agentviewwire.AgentReasonStartRefused, Code: w.s.StartErrorCode}
	if len(w.s.StartErrorArgs) > 0 {
		w.reason.Args = maps.Clone(w.s.StartErrorArgs)
	}
}
func (w *viewWired) VisitRestartInterrupted(r agentview.Resumability) {
	w.reason = agentviewwire.AgentReason{Kind: agentviewwire.AgentReasonRestartInterrupted,
		Resumable: r == agentview.ResumabilityResumable()}
}
func (w *viewWired) VisitSeen()        { w.reason.Kind = agentviewwire.AgentReasonSeen }
func (w *viewWired) VisitNotRunning()  { w.reason.Kind = agentviewwire.AgentReasonNotRunning }
func (w *viewWired) VisitHostUnknown() { w.reason.Kind = agentviewwire.AgentReasonHostUnknown }

// viewCounted writes whether a view counts.
type viewCounted struct{ to *bool }

func (c *viewCounted) VisitCounted()   { *c.to = true }
func (c *viewCounted) VisitUncounted() { *c.to = false }

// viewHeldBy reads whether none of a row's cards is the owner's.
type viewHeldBy struct{ held bool }

func (h *viewHeldBy) VisitOwners() { h.held = false }
func (h *viewHeldBy) VisitHeld()   { h.held = true }

// viewWaitsIn reads whether background work only waits.
type viewWaitsIn struct{ waits bool }

func (w *viewWaitsIn) VisitNone()    {}
func (w *viewWaitsIn) VisitRunning() {}
func (w *viewWaitsIn) VisitWaiting() { w.waits = true }

// viewUnseenIn reads whether the owner has not seen a reply.
type viewUnseenIn struct{ unseen bool }

func (u *viewUnseenIn) VisitUnseen() { u.unseen = true }
func (u *viewUnseenIn) VisitSeen()   {}
