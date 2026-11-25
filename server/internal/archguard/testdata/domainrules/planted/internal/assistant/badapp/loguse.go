package badapp

import (
	"log/slog"

	"github.com/nwverse/persona/server/internal/agentref"
	"github.com/nwverse/persona/server/internal/agentref/agentrefadapt"
)

func settled(agent agentref.Ref) {
	name := agentrefadapt.Log(agent) // out-log-use
	if name != "" {
		slog.Info("turn settled", "agent", name)
	}
}
