package fleetcountadapt

import (
	"github.com/nwverse/persona/server/internal/agentref"
	"github.com/nwverse/persona/server/internal/assistant/fleetcount"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/domainkit/fail"
	"github.com/nwverse/persona/server/internal/domainkit/instant"
	"github.com/nwverse/persona/server/internal/domainkit/opt"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
	"github.com/nwverse/persona/server/internal/domainkit/slot"
)

// Agent is the agent a session is, at the caller's place: a session without a machine or a session id fails with
// agentref's failure, one the builder refuses with fleetcount's.
func Agent(s assistantwire.Session, at slot.Slot) outcome.Of[fleetcount.Agent, fail.Fail] {
	return outcome.Then(agentref.ParseRef(s.MachineID, s.SessionID),
		func(ref agentref.Ref) outcome.Of[fleetcount.Agent, fleetcount.Fail] {
			since := opt.None[instant.Instant]()
			if !s.StateSince.IsZero() {
				since = opt.Some(instant.At(s.StateSince))
			}
			role, lineage, muting := fleetcount.RoleWorker(), fleetcount.LineageRoot(), fleetcount.MutingHeard()
			if s.Assistant {
				role = fleetcount.RoleAssistant()
			}
			if s.Subtask {
				lineage = fleetcount.LineageSubtask()
			}
			if s.Muted {
				muting = fleetcount.MutingMuted()
			}
			return fleetcount.NewAgentBuilder(ref, at).
				State(state(s.State), since, instant.At(s.UpdatedAt)).
				Role(role, lineage, muting).
				Build()
		})
}

// state is a wire state as fleetcount knows it; a state from a newer peer is idle.
func state(wire string) fleetcount.State {
	switch wire {
	case "working":
		return fleetcount.StateWorking()
	case "needs_you":
		return fleetcount.StateNeedsYou()
	case "finished":
		return fleetcount.StateFinished()
	}
	return fleetcount.StateIdle()
}

// Card is the card an interaction is, at the caller's place; a card on no session has no asker.
func Card(ix assistantwire.Interaction, at slot.Slot) outcome.Of[fleetcount.Card, fail.Fail] {
	if ix.SessionID == "" {
		var out outcome.Of[fleetcount.Card, fail.Fail]
		return out.Ok(fleetcount.NewCard(at, opt.None[agentref.Ref]()))
	}
	return outcome.Widen(outcome.Map(agentref.ParseRef(ix.MachineID, ix.SessionID), func(ref agentref.Ref) fleetcount.Card {
		return fleetcount.NewCard(at, opt.Some(ref))
	}))
}
