package externalbinding

import "github.com/nwverse/persona/server/internal/domainkit/fail"

// Fail is every failure externalbinding returns. The zero Fail is nil; no operation returns it as a failure.
type Fail interface {
	fail.Fail
	externalbindingFail()
}

// invalid is input externalbinding refuses.
type invalid struct{ fail.Invalid }

func (invalid) externalbindingFail() {}

func wrong(code, part string) Fail {
	return invalid{fail.NewInvalid(fail.NewCode("externalbinding."+code), fail.NewPart(part))}
}
