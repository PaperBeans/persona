package proto

// FeatureAgentStartHold is what a client declares (client_hello's features, or X-Persona-Features on the request)
// when it draws a new Agent's start held while its computer is away (#10674).
const FeatureAgentStartHold = "agent_start_hold"

func init() { Features = append(Features, FeatureAgentStartHold) }
