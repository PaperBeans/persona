// Package badwire is a contract that carries a core's type.
//
//persona:layer contract
package badwire

import "github.com/nwverse/persona/server/internal/assistant/fleetcount" // core-importers

// Count is a count with its reason.
type Count struct{ Why fleetcount.Why }
