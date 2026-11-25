package badadapt

import (
	"github.com/nwverse/persona/server/internal/assistant/fleetcount"
	"github.com/nwverse/persona/server/internal/domainkit/opt"
	"github.com/nwverse/persona/server/internal/domainkit/slot"
)

// View is a struct of the adapter's own, filled for application code to read.
type View struct {
	Why     string // adapter-exported-result
	Counted bool   // adapter-exported-result
}

// Fill hands the reason out through an out-parameter of the adapter's own struct.
func Fill(w fleetcount.Why, v *View) { // adapter-exported-result
	v.Why, v.Counted = WhyWord(w), w.Counted()
}

// WhyInto hands the reason out through a pointer to a word.
func WhyInto(w fleetcount.Why, dst *string) { // adapter-exported-result
	*dst = WhyWord(w)
}

// EachWhy hands the reason out to a callback.
func EachWhy(w fleetcount.Why, f func(word string)) { // adapter-exported-result
	f(WhyWord(w))
}

// Sink is what application code implements to be told the reason as a word.
type Sink interface{ Why(word string) }

// Tell hands the reason out through an interface the adapter declares.
func Tell(w fleetcount.Why, s Sink) { // adapter-exported-result
	s.Why(WhyWord(w))
}

// Words hands the reason out into a map, by the row's place.
func Words(w fleetcount.Why, at slot.Slot, into map[slot.Slot]string) { // adapter-exported-result
	into[at] = WhyWord(w)
}

// Into hands the reason out through a parameter that can hold a pointer to anything.
func Into(w fleetcount.Why, dst any) { // adapter-exported-result
	if p, ok := dst.(*string); ok {
		*p = WhyWord(w)
	}
}

// IntoOf hands the reason out through a type parameter, which can be a pointer to anything too.
func IntoOf[T any](w fleetcount.Why, dst T) { // adapter-exported-result
	if p, ok := any(dst).(*string); ok {
		*p = WhyWord(w)
	}
}

// WordOf is WhyWord kept in a variable: a getter all the same, and one any caller can replace.
var WordOf = WhyWord // adapter-exported-result

// ByWord hands the reasons out keyed by their words: a map's keys are handed out with its values.
func ByWord(ws []fleetcount.Why) map[string]fleetcount.Why { // adapter-exported-result
	out := map[string]fleetcount.Why{}
	for _, w := range ws {
		out[WhyWord(w)] = w
	}
	return out
}

// MaybeByWord hands them out keyed by their words inside an option: a kit type is no wrapper for a map's raw keys.
func MaybeByWord(ws []fleetcount.Why) opt.Option[map[string]fleetcount.Why] { // adapter-exported-result
	return opt.Some(ByWord(ws))
}
