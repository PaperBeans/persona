package badcore

import (
	"github.com/nwverse/persona/server/internal/domainkit/fail"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
)

// Fail is every failure badcore returns. The zero Fail is nil.
type Fail interface {
	fail.Fail
	badcoreFail()
}

// invalid is input that broke a rule.
type invalid struct{ fail.Invalid }

func (invalid) badcoreFail() {}

// Words is some text. The zero Words is no text.
type Words struct{ words string }

// ParseWords is raw as Words; empty text is invalid.
func ParseWords(raw string) (out outcome.Of[Words, Fail]) {
	if raw == "" {
		return out.Err(invalid{fail.NewInvalid(fail.NewCode("badcore.words.empty"), fail.NewPart("words"))})
	}
	return out.Ok(Words{words: raw})
}
