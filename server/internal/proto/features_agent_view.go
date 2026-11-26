package proto

// FeatureAgentView advertises Session.view: each listed session's five-phase view (phase, reason, counted, rank) the
// manager computes (docs/protocol/agent-states.md "Five phases", #15397). A client that sees it draws and sorts by
// view, and falls back to state for a row without one (a manager that does not compute it yet); without it, it reads
// state and status as before.
const FeatureAgentView = "agent_view"

func init() { Features = append(Features, FeatureAgentView) }
