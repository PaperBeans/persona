// Package fleetcounttest builds fleetcount's values for tests, through the adapter, as the wire sends them.
//
//persona:layer testkit
package fleetcounttest

import (
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/fleetcount"
	"github.com/nwverse/persona/server/internal/assistant/fleetcount/fleetcountadapt"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/domainkit/domaintest"
	"github.com/nwverse/persona/server/internal/domainkit/slot"
)

// Waiting is an agent that waits for the owner, at place i of the caller's list.
func Waiting(t testing.TB, machine, session string, i int) fleetcount.Agent {
	t.Helper()
	s := assistantwire.Session{MachineID: machine, SessionID: session, State: "needs_you", UpdatedAt: time.Unix(1, 0)}
	return domaintest.Ok(t, fleetcountadapt.Agent(s, slot.NewSlot(i)))
}
