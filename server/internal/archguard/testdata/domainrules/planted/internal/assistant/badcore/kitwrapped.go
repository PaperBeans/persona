package badcore

import (
	"github.com/nwverse/persona/server/internal/domainkit/opt"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
)

// Draft is a note being written. The zero Draft is no draft.
type Draft struct {
	title Words
	alias opt.Option[string] // core-primitive-field
	seen  opt.Option[[]int]  // core-primitive-field
}

// NewDraft takes raw text inside an option, which is raw input all the same.
func NewDraft(t Words, alias opt.Option[string]) Draft { // core-primitive-signature
	return Draft{title: t, alias: alias}
}

// Retitle takes raw text inside an outcome.
func (d Draft) Retitle(t outcome.Of[string, Fail]) Draft { return d } // core-primitive-signature

// Tagged takes a slice inside an option.
func (d Draft) Tagged(tags opt.Option[[]Words]) Draft { return d } // core-collection

// DraftVisitor is told a draft's alias as raw text inside an option.
type DraftVisitor interface {
	VisitAlias(alias opt.Option[string]) // core-primitive-signature
}
