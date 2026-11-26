// Package clock is where the application reads the time: a Clock gives one instant.Now per operation, which the
// application passes into the cores it calls. Cores never import it (core-import); tests use clock/clocktest
// (docs/server/architecture.md "Domain model", "The functional core").
//
//persona:layer application
package clock

import (
	"time"

	"github.com/nwverse/persona/server/internal/domainkit/instant"
)

// Clock reads the time once for an operation.
type Clock interface{ Now() instant.Now }

// System is the machine's clock.
type System struct{}

// Now reads the machine's clock.
func (System) Now() instant.Now { return instant.ReadClock(time.Now()) }
