package agentview

import "github.com/nwverse/persona/server/internal/domainkit/defect"

// View is a session as every client draws it: its phase, why, whether its row counts in the Agents list's Needs you,
// and its rank. The zero View is no view; only Decide makes one.
type View struct {
	phase    Phase
	reason   Reason
	counting Counting
	rank     Rank
}

// ViewVisitor is told the view.
type ViewVisitor interface {
	VisitView(phase Phase, reason Reason, counting Counting, rank Rank)
}

// Accept tells v the view; the zero View is a defect.
func (w View) Accept(v ViewVisitor) {
	if w.reason == nil {
		defect.Raise("agentview.View.Accept", "the zero View")
	}
	v.VisitView(w.phase, w.reason, w.counting, w.rank)
}

// The ranks, lower first (docs/protocol/agent-states.md "Rank"): each phase starts at a multiple of ten, in the order
// needs you, error, working, done, idle, and its reasons take the places after it; a muted session in error, working
// or done takes its phase's last place (mutedIn).
const (
	rankNeedsYouCounted   = 0
	rankNeedsYouUncounted = 1
	rankError             = 10 // a model error, a refused start, an interrupted one, a usage limit nothing goes on after
	rankErrorResuming     = 11 // a usage limit its CLI goes on after by itself
	rankWorkingTurn       = 20
	rankWorkingBackground = 21
	rankWorkingWaiting    = 22
	rankDoneUnseen        = 30 // not seen yet, asking the owner or not
	rankDoneAsks          = 31 // seen, asking the owner
	rankDoneJustFinished  = 32
	rankIdleSeen          = 40
	rankIdleHostUnknown   = 41
	rankIdleNotRunning    = 42
)

// mutedIn is the last rank of the phase rank r is in, where a muted session goes.
func mutedIn(r Rank) Rank { return Rank{rank: r.rank/10*10 + 9} }

// Decide is the session's view. Needs you comes first, from its row: fleetcount's row for it, its cards held elsewhere,
// or its supervisor's. Then an interrupted session and an error are the error phase (D-C), work the working phase, and
// a finished turn done or idle: asking the owner is done, uncounted (D-A); a host that does not say is idle (D-B). A
// muted session in error, working or done sorts last in its phase; a muted one that waits or is idle sorts as any other. The zero Session is a defect.
func Decide(s Session) View {
	if s.row == nil {
		defect.Raise("agentview.Decide", "the zero Session")
	}
	v := byRow(s)
	if v.reason == nil {
		v = byTurn(s)
	}
	if v.phase != PhaseNeedsYou() && v.phase != PhaseIdle() && s.muting == MutingMuted() {
		v.rank = mutedIn(v.rank)
	}
	return v
}

// waits is the needs-you view for a reason, counted as fleetcount decided.
func waits(reason Reason, counting Counting) View {
	rank := Rank{rank: rankNeedsYouUncounted}
	if counting == CountingCounted() {
		rank = Rank{rank: rankNeedsYouCounted}
	}
	return View{phase: PhaseNeedsYou(), reason: reason, counting: counting, rank: rank}
}

// shown is a view that does not count in Needs you.
func shown(phase Phase, reason Reason, rank Rank) View {
	return View{phase: phase, reason: reason, counting: CountingUncounted(), rank: rank}
}

// byRow is the view fleetcount's row gives the session; one with no reason when it has no row and no card.
func byRow(s Session) View {
	switch r := s.row.(type) {
	case rowByCards:
		return waits(reasonCards{cards: r.cards, holding: r.holding}, r.counting)
	case rowByDialog:
		return waits(reasonTerminalDialog{}, r.counting)
	case rowElsewhere:
		return waits(reasonCards{cards: r.cards, holding: r.holding}, CountingUncounted())
	case rowSupervised: // every card it asked is its task's supervisor's: the owner sees it at work
		return shown(PhaseWorking(), reasonTurn{}, Rank{rank: rankWorkingTurn})
	}
	return View{}
}

// byTurn is the view of a session in no Needs you row, from its own turn.
func byTurn(s Session) View {
	switch {
	case s.host == HostWaiting(): // no row and no card: one of the assistant's, or a hidden one
		return waits(reasonTerminalDialog{}, CountingUncounted())
	case s.host == HostWorking() && s.background == BackgroundRunning():
		return shown(PhaseWorking(), reasonBackground{background: s.background}, Rank{rank: rankWorkingBackground})
	case s.host == HostWorking() && s.background == BackgroundWaiting():
		return shown(PhaseWorking(), reasonBackground{background: s.background}, Rank{rank: rankWorkingWaiting})
	case s.host == HostWorking():
		return shown(PhaseWorking(), reasonTurn{}, Rank{rank: rankWorkingTurn})
	case s.host == HostInterrupted():
		return shown(PhaseError(), reasonInterrupted{resumability: s.resumability}, Rank{rank: rankError})
	case s.failure == FailureUsageLimitResuming():
		return shown(PhaseError(), reasonUsageLimit{failure: s.failure}, Rank{rank: rankErrorResuming})
	case s.failure == FailureUsageLimit():
		return shown(PhaseError(), reasonUsageLimit{failure: s.failure}, Rank{rank: rankError})
	case s.failure == FailureStartRefused():
		return shown(PhaseError(), reasonStartRefused{}, Rank{rank: rankError})
	case s.failure == FailureModel():
		return shown(PhaseError(), reasonModel{}, Rank{rank: rankError})
	case s.host == HostUnread() && s.asking == AskingOwner():
		return shown(PhaseDone(), reasonAsks{seeing: SeeingUnseen()}, Rank{rank: rankDoneUnseen})
	case s.host == HostUnread():
		return shown(PhaseDone(), reasonUnseen{}, Rank{rank: rankDoneUnseen})
	case s.host == HostSeen() && s.asking == AskingOwner():
		return shown(PhaseDone(), reasonAsks{seeing: SeeingSeen()}, Rank{rank: rankDoneAsks})
	case s.host == HostSeen() && s.recency == RecencyJustFinished():
		return shown(PhaseDone(), reasonJustFinished{}, Rank{rank: rankDoneJustFinished})
	case s.host == HostSeen():
		return shown(PhaseIdle(), reasonSeen{}, Rank{rank: rankIdleSeen})
	case s.host == HostOffline():
		return shown(PhaseIdle(), reasonNotRunning{}, Rank{rank: rankIdleNotRunning})
	}
	return shown(PhaseIdle(), reasonHostUnknown{}, Rank{rank: rankIdleHostUnknown})
}
