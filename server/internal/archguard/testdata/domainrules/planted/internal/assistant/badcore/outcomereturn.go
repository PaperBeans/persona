package badcore

import (
	"github.com/nwverse/persona/server/internal/domainkit/fail"
	"github.com/nwverse/persona/server/internal/domainkit/opt"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
)

func ignore(raw string) {
	_ = ParseWords(raw) // core-outcome-return: a discarded outcome
}

func unset(raw string) (out outcome.Of[Words, Fail]) {
	return // core-outcome-return: an unset outcome
}

// lost keeps one of two failures: With returns a new list, and the statement drops it.
func lost(first, second fail.Invalid) fail.Invalids {
	all := fail.NewInvalids(first)
	all.With(second) // core-outcome-return: a kit method's result dropped
	return all
}

func dropped(raw string) {
	ParseWords(raw) // core-outcome-return: this package's function's outcome dropped
	opt.Some(raw)   // core-outcome-return: a kit function's result dropped
}
