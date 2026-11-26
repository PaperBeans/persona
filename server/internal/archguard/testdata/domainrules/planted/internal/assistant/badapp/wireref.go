package badapp

import "github.com/nwverse/persona/server/internal/assistant/fleetcount"

func count(n fleetcount.RowCount) int {
	return n.Wire() // out-wire-ref
}
