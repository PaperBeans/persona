package badapp

import "github.com/nwverse/persona/server/internal/assistant/fleetcount"

func role(s fleetcount.State) fleetcount.Role {
	return fleetcount.Role(s) // out-conversion
}
