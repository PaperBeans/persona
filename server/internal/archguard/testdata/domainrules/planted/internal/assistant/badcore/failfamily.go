package badcore

import "github.com/nwverse/persona/server/internal/domainkit/fail"

// stray embeds a fail base but is outside the family.
type stray struct{ fail.Conflict } // core-fail-family
