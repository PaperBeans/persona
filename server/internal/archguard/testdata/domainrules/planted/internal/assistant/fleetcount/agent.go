package fleetcount

import (
	"slices"

	"github.com/nwverse/persona/server/internal/agentref"
	"github.com/nwverse/persona/server/internal/domainkit/fail"
	"github.com/nwverse/persona/server/internal/domainkit/instant"
	"github.com/nwverse/persona/server/internal/domainkit/opt"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
	"github.com/nwverse/persona/server/internal/domainkit/slot"
)

// Fail is every failure fleetcount returns. The zero Fail is nil; Build never returns it as a failure.
type Fail interface {
	fail.Fail
	fleetcountFail()
}

// invalidAgent is every part of an agent Build refused, in order.
type invalidAgent struct{ fail.Invalids }

func (invalidAgent) fleetcountFail() {}

// Agent is one agent as the fleet sees it. The zero Agent is no agent; only Build makes one.
type Agent struct {
	ref     agentref.Ref
	at      slot.Slot
	state   State
	since   opt.Option[instant.Instant]
	updated instant.Instant
	role    Role
	lineage Lineage
	muting  Muting
}

// AgentStateStep is the builder's first stage: the agent's state. The zero AgentStateStep is a skipped stage, and
// Build refuses what it leaves out.
type AgentStateStep struct{ agentStateStep Agent }

// AgentRoleStep is the builder's second stage: the agent's role. The zero AgentRoleStep is a skipped stage, and Build
// refuses what it leaves out.
type AgentRoleStep struct{ agentRoleStep Agent }

// AgentBuilder is the builder's last stage. The zero AgentBuilder is a skipped stage, and Build refuses what it leaves
// out.
type AgentBuilder struct{ agentBuilder Agent }

// NewAgentBuilder starts an agent: the session it is and the caller's place for it.
func NewAgentBuilder(ref agentref.Ref, at slot.Slot) AgentStateStep {
	return AgentStateStep{agentStateStep: Agent{ref: ref, at: at}}
}

// State is the agent's state, since when it holds (none when unknown) and when the agent last changed.
func (s AgentStateStep) State(state State, since opt.Option[instant.Instant], updated instant.Instant) AgentRoleStep {
	a := s.agentStateStep
	a.state, a.since, a.updated = state, since, updated
	return AgentRoleStep{agentRoleStep: a}
}

// Role is whose conversation the agent is, whether it is a subtask and whether the owner muted it.
func (s AgentRoleStep) Role(role Role, lineage Lineage, muting Muting) AgentBuilder {
	a := s.agentRoleStep
	a.role, a.lineage, a.muting = role, lineage, muting
	return AgentBuilder{agentBuilder: a}
}

// Build is the agent, or every part a skipped stage or a zero value left out, in order.
func (b AgentBuilder) Build() (out outcome.Of[Agent, Fail]) {
	a := b.agentBuilder
	var none agentref.Ref
	var wrong []fail.Invalid
	check := func(ok bool, code, part string) {
		if !ok {
			wrong = append(wrong, fail.NewInvalid(fail.NewCode(code), fail.NewPart(part)))
		}
	}
	check(!a.ref.IsSame(none), "fleetcount.agent.no_session", "agent")
	check(a.state != State{}, "fleetcount.agent.no_state", "state")
	check(a.updated.Known(), "fleetcount.agent.no_update", "updated")
	check(a.role != Role{}, "fleetcount.agent.no_role", "role")
	check(a.lineage != Lineage{}, "fleetcount.agent.no_lineage", "lineage")
	check(a.muting != Muting{}, "fleetcount.agent.no_muting", "muting")
	if len(wrong) == 0 {
		return out.Ok(a)
	}
	all := fail.NewInvalids(wrong[0])
	for _, w := range wrong[1:] {
		all = all.With(w)
	}
	return out.Err(invalidAgent{all})
}

// ID is the session the agent is.
func (a Agent) ID() agentref.Ref { return a.ref }

// Counts says whether the agent counts on a surface: a muted agent never does, and the Live Activity leaves subtasks
// out.
func (a Agent) Counts(on Surface) bool {
	if a.muting == MutingMuted() {
		return false
	}
	return !(on == SurfaceLiveActivity() && a.lineage == LineageSubtask())
}

// waitsAlone says whether the agent waits for the owner with no card standing for it.
func (a Agent) waitsAlone() bool { return a.state == StateNeedsYou() && a.role != RoleAssistant() }

// Card is one pending card. The zero Card is a card on no session at the caller's first place.
type Card struct {
	at    slot.Slot
	asker opt.Option[agentref.Ref]
}

// NewCard is a card at the caller's place, asked by a session or by none.
func NewCard(at slot.Slot, asker opt.Option[agentref.Ref]) Card { return Card{at: at, asker: asker} }

// Agents is the fleet's agents, in the caller's order. The zero Agents is none.
type Agents struct{ agents []Agent }

// NewAgents is the agents in list; it copies list, so the caller may reuse its slice.
func NewAgents(list []Agent) Agents { return Agents{agents: slices.Clone(list)} }

// Cards is the pending cards, in the caller's order. The zero Cards is none.
type Cards struct{ cards []Card }

// NewCards is the cards in list; it copies list, so the caller may reuse its slice.
func NewCards(list []Card) Cards { return Cards{cards: slices.Clone(list)} }
