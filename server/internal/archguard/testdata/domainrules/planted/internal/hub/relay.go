// Package hub is the planted hub: it relays contracts between the apps and the managers and imports no core.
//
//persona:layer application
package hub

import (
	"encoding/json"
	"net/http"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// relay answers the apps with the manager's Needs you list, as the manager sent it.
func relay(w http.ResponseWriter, list assistantwire.NeedsYou) { _ = json.NewEncoder(w).Encode(list) }
