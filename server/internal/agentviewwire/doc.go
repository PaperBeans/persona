// Package agentviewwire is the contract of an Agent's five-phase view: AgentPhase, AgentReason and AgentView, which the
// manager computes onto Session.view (docs/protocol/agent-states.md "Five phases", docs/plans/plan-typed-core.md 2.1,
// #15397). It is generated from schema/wire/agent-view.schema.json (agent_view_gen.go, never edited by hand) in every
// client's language too, so clients draw and sort the view the manager sends instead of deriving their own.
//
//persona:layer contract
package agentviewwire
