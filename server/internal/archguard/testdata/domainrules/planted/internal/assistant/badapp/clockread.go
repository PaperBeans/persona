package badapp

import (
	"time"

	"github.com/nwverse/persona/server/internal/domainkit/instant"
)

func now() instant.Now {
	return instant.ReadClock(time.Now()) // out-clock-read
}
