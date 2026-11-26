// Package badadapt converts fleetcount's values the way an adapter must not.
//
//persona:layer adapter
package badadapt

import (
	"github.com/nwverse/persona/server/internal/assistant" // adapter-no-application
	"github.com/nwverse/persona/server/internal/assistant/fleetcount"
	"github.com/nwverse/persona/server/internal/assistant/inbox/item/itemadapt" // adapter-import
	"github.com/nwverse/persona/server/internal/domainkit/fail"
	"github.com/nwverse/persona/server/internal/domainkit/opt"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
)

// Agent is a record named like the core's own type.
type Agent struct { // adapter-name
	Service *assistant.Service // adapter-exported-result
	Record  itemadapt.Record   // adapter-exported-result
}

// WhyWord hands the reason out as a word.
func WhyWord(w fleetcount.Why) string { // adapter-exported-result
	if w.Counted() {
		return "needs_you"
	}
	return "held"
}

// WhyText hands the reason out as text inside an outcome: a kit type is no wrapper for a raw value.
func WhyText(w fleetcount.Why) (out outcome.Of[string, fail.Fail]) { // adapter-exported-result
	return out.Ok(WhyWord(w))
}

// MaybeWords hands the reasons out as words inside an option.
func MaybeWords(w fleetcount.Why) opt.Option[[]string] { // adapter-exported-result
	return opt.Some([]string{WhyWord(w)})
}
