package badapp

import (
	"github.com/nwverse/persona/server/internal/agentref"
	"github.com/nwverse/persona/server/internal/agentref/agentrefadapt"
	"github.com/nwverse/persona/server/internal/assistantwire"
)

// waitingOn unwraps the session to compare its wire shape: app-bridge-call counts the call.
func waitingOn(agent agentref.Ref, waiting assistantwire.SessionRef) bool {
	return agentrefadapt.Wire(agent) == waiting
}
