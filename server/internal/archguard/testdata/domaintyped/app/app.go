// Package app is the type-aware domain rules' planted application code (TestDomainTypedPlanted): a line a rule
// refuses ends with a comment naming the rule, and the twins beside it pass.
package app

import (
	"encoding/json"
	"fmt"

	"github.com/nwverse/persona/server/internal/agentref"
	"github.com/nwverse/persona/server/internal/agentref/agentrefadapt"
	"github.com/nwverse/persona/server/internal/assistant/fleetcount"
	"github.com/nwverse/persona/server/internal/assistant/fleetcount/fleetcountadapt"
	"github.com/nwverse/persona/server/internal/domainkit/fail"
	"github.com/nwverse/persona/server/internal/domainkit/instant"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
	"github.com/nwverse/persona/server/internal/domainkit/slot"
	"github.com/nwverse/persona/server/internal/proto"
	"planted/helper"
)

// peek decides on a tally's total in application code, whatever its methods' parameters are spelled.
type peek struct{ total fleetcount.RowCount } // planted: typed-visitor-impl

func (p *peek) VisitAgent(slot.Slot, fleetcount.Why, instant.Instant) {}
func (p *peek) VisitCard(slot.Slot, fleetcount.Why)                   {}
func (p *peek) VisitAgentByCard(_, _ slot.Slot, _ fleetcount.Why)     {}
func (p *peek) VisitCardUnder(slot.Slot)                              {}
func (p *peek) VisitTotal(total fleetcount.RowCount)                  { p.total = total }

// Peeker is a helper's generic type, a tally visitor once instantiated with the core's types.
func Peeker(total *fleetcount.RowCount) fleetcount.TallyVisitor {
	return helper.NewPeek[slot.Slot, fleetcount.Why, instant.Instant](total) // planted: typed-visitor-impl
}

// host has a method of a visitor's name and no visitor's set: it implements none.
type host struct{}

func (host) VisitAgent(s *proto.Session) {}

// holder keeps a core value in a field, which %v prints through reflection.
type holder struct{ ref agentref.Ref }

// Run drops outcomes and failures, prints core values and compares failures, next to the twins that do not.
func Run(s *proto.Session, f1, f2 fail.Fail, ref agentref.Ref) string {
	_ = fleetcountadapt.Agent(s, false, 0) // planted: typed-discard
	fleetcountadapt.Agent(s, false, 0)     // planted: typed-discard
	if _, failed := outcome.Unpack(fleetcountadapt.Agent(s, false, 0)); failed != nil {
		return fleetcountadapt.Log(failed)
	}
	if b, err := json.Marshal(ref); err == nil { // planted: typed-codec
		return string(b)
	}
	if f1 == f2 { // planted: typed-fail-compare
		return ""
	}
	if f1 == nil {
		return fmt.Sprintf("%s", agentrefadapt.Log(ref))
	}
	return fmt.Sprintf("%v", holder{ref: ref}) // planted: typed-codec
}
