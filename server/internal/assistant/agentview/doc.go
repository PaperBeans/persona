// Package agentview decides where an Agent stands in one of five phases (needs you, working, done, error, idle), why,
// whether its row counts in the Agents list's Needs you, and where it sorts (docs/protocol/agent-states.md "Five
// phases", docs/plans/plan-typed-core.md 2.1, #15397). Whether a row counts is fleetcount's decision, handed in as
// the session's Row; this core never decides it again.
//
//persona:layer core
package agentview
