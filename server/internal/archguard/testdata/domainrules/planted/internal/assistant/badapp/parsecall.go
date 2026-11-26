package badapp

import "github.com/nwverse/persona/server/internal/agentref"

func ref(machine, session string) bool {
	return agentref.ParseRef(machine, session).IsOk() // out-parse-call
}
