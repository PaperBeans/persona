package db

import "github.com/nwverse/persona/server/internal/domainkit/instant" // path-no-core, layer-library

// day is a row's day as the kit's instant.
func day(u Usage) instant.Instant { return instant.At(u.Day) }
