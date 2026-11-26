package agentview

import "github.com/nwverse/persona/server/internal/domainkit/defect"

// Phase is where an Agent stands, one of five. The zero Phase is none; Build refuses it.
type Phase struct{ phase uint8 }

// PhaseNeedsYou is an Agent that waits for the owner, or whose row waits for a child or side folded under it.
func PhaseNeedsYou() Phase { return Phase{phase: 1} }

// PhaseWorking is an Agent at work, its turn or only its background work.
func PhaseWorking() Phase { return Phase{phase: 2} }

// PhaseDone is an Agent whose turn finished: not seen yet, asking the owner, or just finished.
func PhaseDone() Phase { return Phase{phase: 3} }

// PhaseError is an Agent stopped on an error: a model error, a usage limit, a refused start, or a restart of its computer's node.
func PhaseError() Phase { return Phase{phase: 4} }

// PhaseIdle is an Agent with nothing to show: seen, not running, or in a state its host does not say.
func PhaseIdle() Phase { return Phase{phase: 5} }

// PhaseVisitor is told which phase it is.
type PhaseVisitor interface {
	VisitNeedsYou()
	VisitWorking()
	VisitDone()
	VisitError()
	VisitIdle()
}

// Accept tells v which phase it is; the zero Phase is a defect.
func (p Phase) Accept(v PhaseVisitor) {
	switch p {
	case PhaseNeedsYou():
		v.VisitNeedsYou()
	case PhaseWorking():
		v.VisitWorking()
	case PhaseDone():
		v.VisitDone()
	case PhaseError():
		v.VisitError()
	case PhaseIdle():
		v.VisitIdle()
	default:
		defect.Raise("agentview.Phase.Accept", "the zero Phase")
	}
}

// Host is where the hub says the session's turn stands (Session.state, with Session.status for an interrupted one). The zero Host is none; Build refuses it.
type Host struct{ host uint8 }

// HostWorking is the hub says it works (needs_you aside, which the Row decides).
func HostWorking() Host { return Host{host: 1} }

// HostWaiting is the hub says it needs the owner.
func HostWaiting() Host { return Host{host: 2} }

// HostUnread is it finished and the owner has not seen the result.
func HostUnread() Host { return Host{host: 3} }

// HostSeen is it finished and was seen.
func HostSeen() Host { return Host{host: 4} }

// HostOffline is no process runs it.
func HostOffline() Host { return Host{host: 5} }

// HostInterrupted is it was running when its computer's node restarted.
func HostInterrupted() Host { return Host{host: 6} }

// HostUnknown is its host does not say, or says something newer than this hub knows.
func HostUnknown() Host { return Host{host: 7} }

// HostVisitor is told which host it is.
type HostVisitor interface {
	VisitWorking()
	VisitWaiting()
	VisitUnread()
	VisitSeen()
	VisitOffline()
	VisitInterrupted()
	VisitUnknown()
}

// Accept tells v which host it is; the zero Host is a defect.
func (h Host) Accept(v HostVisitor) {
	switch h {
	case HostWorking():
		v.VisitWorking()
	case HostWaiting():
		v.VisitWaiting()
	case HostUnread():
		v.VisitUnread()
	case HostSeen():
		v.VisitSeen()
	case HostOffline():
		v.VisitOffline()
	case HostInterrupted():
		v.VisitInterrupted()
	case HostUnknown():
		v.VisitUnknown()
	default:
		defect.Raise("agentview.Host.Accept", "the zero Host")
	}
}

// Background is what runs in the background once the session's turn ended. The zero Background is none; Build refuses it.
type Background struct{ background uint8 }

// BackgroundNone is nothing runs in the background, or its turn still runs.
func BackgroundNone() Background { return Background{background: 1} }

// BackgroundRunning is background work runs: commands, subagents, workflows.
func BackgroundRunning() Background { return Background{background: 2} }

// BackgroundWaiting is only background commands run, and they only wait.
func BackgroundWaiting() Background { return Background{background: 3} }

// BackgroundVisitor is told which background it is.
type BackgroundVisitor interface {
	VisitNone()
	VisitRunning()
	VisitWaiting()
}

// Accept tells v which background it is; the zero Background is a defect.
func (b Background) Accept(v BackgroundVisitor) {
	switch b {
	case BackgroundNone():
		v.VisitNone()
	case BackgroundRunning():
		v.VisitRunning()
	case BackgroundWaiting():
		v.VisitWaiting()
	default:
		defect.Raise("agentview.Background.Accept", "the zero Background")
	}
}

// Failure is what error ended the session's last turn or its start. The zero Failure is none; Build refuses it.
type Failure struct{ failure uint8 }

// FailureNone is no error.
func FailureNone() Failure { return Failure{failure: 1} }

// FailureModel is a model API error ended its turn.
func FailureModel() Failure { return Failure{failure: 2} }

// FailureUsageLimit is a subscription's usage limit ended its turn, and nothing goes on by itself.
func FailureUsageLimit() Failure { return Failure{failure: 3} }

// FailureUsageLimitResuming is a subscription's usage limit ended its turn, and its CLI goes on by itself once it resets.
func FailureUsageLimitResuming() Failure { return Failure{failure: 4} }

// FailureStartRefused is its start was refused or failed.
func FailureStartRefused() Failure { return Failure{failure: 5} }

// FailureVisitor is told which failure it is.
type FailureVisitor interface {
	VisitNone()
	VisitModel()
	VisitUsageLimit()
	VisitUsageLimitResuming()
	VisitStartRefused()
}

// Accept tells v which failure it is; the zero Failure is a defect.
func (f Failure) Accept(v FailureVisitor) {
	switch f {
	case FailureNone():
		v.VisitNone()
	case FailureModel():
		v.VisitModel()
	case FailureUsageLimit():
		v.VisitUsageLimit()
	case FailureUsageLimitResuming():
		v.VisitUsageLimitResuming()
	case FailureStartRefused():
		v.VisitStartRefused()
	default:
		defect.Raise("agentview.Failure.Accept", "the zero Failure")
	}
}

// Asking is whether the session stopped on a question to the owner nobody answered yet. The zero Asking is none; Build refuses it.
type Asking struct{ asking uint8 }

// AskingNothing is it asks the owner nothing.
func AskingNothing() Asking { return Asking{asking: 1} }

// AskingOwner is it asks the owner (Awaiting your reply, D-A).
func AskingOwner() Asking { return Asking{asking: 2} }

// AskingVisitor is told which asking it is.
type AskingVisitor interface {
	VisitNothing()
	VisitOwner()
}

// Accept tells v which asking it is; the zero Asking is a defect.
func (a Asking) Accept(v AskingVisitor) {
	switch a {
	case AskingNothing():
		v.VisitNothing()
	case AskingOwner():
		v.VisitOwner()
	default:
		defect.Raise("agentview.Asking.Accept", "the zero Asking")
	}
}

// Recency is whether a seen session's turn finished before anybody looked at it, within the triage's window. The zero Recency is none; Build refuses it.
type Recency struct{ recency uint8 }

// RecencyEarlier is it did not, or long ago.
func RecencyEarlier() Recency { return Recency{recency: 1} }

// RecencyJustFinished is it did, a short while ago.
func RecencyJustFinished() Recency { return Recency{recency: 2} }

// RecencyVisitor is told which recency it is.
type RecencyVisitor interface {
	VisitEarlier()
	VisitJustFinished()
}

// Accept tells v which recency it is; the zero Recency is a defect.
func (r Recency) Accept(v RecencyVisitor) {
	switch r {
	case RecencyEarlier():
		v.VisitEarlier()
	case RecencyJustFinished():
		v.VisitJustFinished()
	default:
		defect.Raise("agentview.Recency.Accept", "the zero Recency")
	}
}

// Muting is whether the owner muted the session (itself, or its space). The zero Muting is none; Build refuses it.
type Muting struct{ muting uint8 }

// MutingHeard is the owner hears from it.
func MutingHeard() Muting { return Muting{muting: 1} }

// MutingMuted is the owner muted it.
func MutingMuted() Muting { return Muting{muting: 2} }

// MutingVisitor is told which muting it is.
type MutingVisitor interface {
	VisitHeard()
	VisitMuted()
}

// Accept tells v which muting it is; the zero Muting is a defect.
func (m Muting) Accept(v MutingVisitor) {
	switch m {
	case MutingHeard():
		v.VisitHeard()
	case MutingMuted():
		v.VisitMuted()
	default:
		defect.Raise("agentview.Muting.Accept", "the zero Muting")
	}
}

// Resumability is whether Continue can resume an interrupted session's conversation. The zero Resumability is none; Build refuses it.
type Resumability struct{ resumability uint8 }

// ResumabilityResumable is it can.
func ResumabilityResumable() Resumability { return Resumability{resumability: 1} }

// ResumabilityNotResumable is it cannot: start a new one.
func ResumabilityNotResumable() Resumability { return Resumability{resumability: 2} }

// ResumabilityVisitor is told which resumability it is.
type ResumabilityVisitor interface {
	VisitResumable()
	VisitNotResumable()
}

// Accept tells v which resumability it is; the zero Resumability is a defect.
func (r Resumability) Accept(v ResumabilityVisitor) {
	switch r {
	case ResumabilityResumable():
		v.VisitResumable()
	case ResumabilityNotResumable():
		v.VisitNotResumable()
	default:
		defect.Raise("agentview.Resumability.Accept", "the zero Resumability")
	}
}

// Holding is whose a row's cards are to answer. The zero Holding is none; Build refuses it.
type Holding struct{ holding uint8 }

// HoldingOwners is one of them at least is the owner's.
func HoldingOwners() Holding { return Holding{holding: 1} }

// HoldingHeld is none is the owner's yet: questions the assistant is still deciding on (D11).
func HoldingHeld() Holding { return Holding{holding: 2} }

// HoldingVisitor is told which holding it is.
type HoldingVisitor interface {
	VisitOwners()
	VisitHeld()
}

// Accept tells v which holding it is; the zero Holding is a defect.
func (h Holding) Accept(v HoldingVisitor) {
	switch h {
	case HoldingOwners():
		v.VisitOwners()
	case HoldingHeld():
		v.VisitHeld()
	default:
		defect.Raise("agentview.Holding.Accept", "the zero Holding")
	}
}

// Counting is whether a session's row counts in the Agents list's Needs you, as fleetcount decided. The zero Counting is none; Build refuses it.
type Counting struct{ counting uint8 }

// CountingCounted is it counts.
func CountingCounted() Counting { return Counting{counting: 1} }

// CountingUncounted is it does not: no row, or one Needs you lists without counting (muted, quiet, held).
func CountingUncounted() Counting { return Counting{counting: 2} }

// CountingVisitor is told which counting it is.
type CountingVisitor interface {
	VisitCounted()
	VisitUncounted()
}

// Accept tells v which counting it is; the zero Counting is a defect.
func (c Counting) Accept(v CountingVisitor) {
	switch c {
	case CountingCounted():
		v.VisitCounted()
	case CountingUncounted():
		v.VisitUncounted()
	default:
		defect.Raise("agentview.Counting.Accept", "the zero Counting")
	}
}

// Seeing is whether the owner saw a finished session's reply. The zero Seeing is none; Build refuses it.
type Seeing struct{ seeing uint8 }

// SeeingUnseen is not yet.
func SeeingUnseen() Seeing { return Seeing{seeing: 1} }

// SeeingSeen is the owner saw it.
func SeeingSeen() Seeing { return Seeing{seeing: 2} }

// SeeingVisitor is told which seeing it is.
type SeeingVisitor interface {
	VisitUnseen()
	VisitSeen()
}

// Accept tells v which seeing it is; the zero Seeing is a defect.
func (s Seeing) Accept(v SeeingVisitor) {
	switch s {
	case SeeingUnseen():
		v.VisitUnseen()
	case SeeingSeen():
		v.VisitSeen()
	default:
		defect.Raise("agentview.Seeing.Accept", "the zero Seeing")
	}
}
