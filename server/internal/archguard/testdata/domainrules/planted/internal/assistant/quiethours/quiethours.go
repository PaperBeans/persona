// Package quiethours decides whether a notice is delivered now or held.
//
//persona:layer core
package quiethours

import "github.com/nwverse/persona/server/internal/domainkit/instant"

// Decision is what to do with a notice. The zero Decision is nil; Decide never returns it.
type Decision interface {
	Accept(v DecisionVisitor) // only adapters implement DecisionVisitor
	decision()                // sealed: only this package implements Decision
}

type DecisionVisitor interface {
	VisitDeliver()
	VisitHoldUntil(until instant.Instant)
}

type deliver struct{}
type hold struct{ until instant.Instant }

func DecisionDeliver() Decision                        { return deliver{} }
func DecisionHoldUntil(until instant.Instant) Decision { return hold{until: until} }

func (deliver) Accept(v DecisionVisitor) { v.VisitDeliver() }
func (h hold) Accept(v DecisionVisitor)  { v.VisitHoldUntil(h.until) }
func (deliver) decision()                {}
func (hold) decision()                   {}
