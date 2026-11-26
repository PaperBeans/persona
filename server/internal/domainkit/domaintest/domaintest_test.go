package domaintest_test

import (
	"testing"

	"github.com/nwverse/persona/server/internal/domainkit/domaintest"
	"github.com/nwverse/persona/server/internal/domainkit/fail"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
)

type itemFail interface {
	fail.Fail
	itemFail()
}

type notDone struct{ fail.Refusal }

func (notDone) itemFail() {}

func reopen(done bool) (out outcome.Of[string, itemFail]) {
	if !done {
		return out.Err(notDone{fail.NewRefusal(fail.NewCode("inbox.item.not_done"))})
	}
	return out.Ok("reopened")
}

func TestOkAndFails(t *testing.T) {
	if domaintest.Ok(t, reopen(true)) != "reopened" {
		t.Error("Ok")
	}
	if got := domaintest.Fails(t, reopen(false)); got != "refused inbox.item.not_done" {
		t.Errorf("Fails = %q", got)
	}
	if domaintest.Describe(nil) != "none" {
		t.Error("Describe(nil)")
	}
	invalids := fail.NewInvalids(fail.NewInvalid(fail.NewCode("a.b"), fail.NewPart("machine"))).
		With(fail.NewInvalid(fail.NewCode("a.c"), fail.NewPart("session")))
	if got := domaintest.Describe(invalids); got != "invalid a.b machine; invalid a.c session" {
		t.Errorf("Describe of a list = %q", got)
	}
}
