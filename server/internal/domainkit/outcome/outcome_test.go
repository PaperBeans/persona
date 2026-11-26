package outcome_test

import (
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/domainkit/defect"
	"github.com/nwverse/persona/server/internal/domainkit/domaintest"
	"github.com/nwverse/persona/server/internal/domainkit/fail"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
)

// Two sealed families, as two cores would declare them.
type alphaFail interface {
	fail.Fail
	alphaFail()
}

type alphaRefused struct{ fail.Refusal }

func (alphaRefused) alphaFail() {}

type betaFail interface {
	fail.Fail
	betaFail()
}

type betaMissing struct{ fail.Absence }

func (betaMissing) betaFail() {}

func parseAlpha(raw string) (out outcome.Of[int, alphaFail]) {
	if raw == "" {
		return out.Err(alphaRefused{fail.NewRefusal(fail.NewCode("alpha.empty"))})
	}
	return out.Ok(len(raw))
}

func findBeta(n int) (out outcome.Of[string, betaFail]) {
	if n > 3 {
		return out.Err(betaMissing{fail.NewAbsence(fail.NewCode("beta.none"))})
	}
	return out.Ok(strings.Repeat("b", n))
}

// raises says whether f raised a defect whose text starts with op.
func raises(t *testing.T, op string, f func()) {
	t.Helper()
	defer func() {
		t.Helper()
		d, ok := recover().(defect.Defect)
		if !ok || !strings.HasPrefix(d.Error(), op+": ") {
			t.Errorf("%s: want a defect from %s, got %v", op, op, d)
		}
	}()
	f()
}

func TestAnUnsetOutcomeIsADefect(t *testing.T) {
	var o outcome.Of[int, alphaFail] // the producer forgot Ok and Err
	raises(t, "outcome.Unpack", func() { outcome.Unpack(o) })
	raises(t, "outcome.IsOk", func() { o.IsOk() })
	raises(t, "outcome.Fold", func() {
		outcome.Fold(o, func(alphaFail) bool { return false }, func(int) bool { return true })
	})
	raises(t, "outcome.Widen", func() { outcome.Widen(o) })
	raises(t, "outcome.Then", func() { outcome.Then(o, findBeta) })
	raises(t, "outcome.Map", func() { outcome.Map(o, func(n int) int { return n }) })
	raises(t, "the.op", func() { outcome.Must("the.op", o) })
	// A next step that returns an unset outcome is a defect too.
	raises(t, "outcome.Widen", func() {
		outcome.Then(parseAlpha("x"), func(int) (out outcome.Of[string, betaFail]) { return out })
	})
}

func TestErrRefusesANilFailure(t *testing.T) {
	raises(t, "outcome.Err", func() {
		var out outcome.Of[int, alphaFail]
		out.Err(nil)
	})
}

// A family type built empty is no failure: Err visits what it is given, so it raises where the failure is made.
func TestErrRefusesAZeroFailure(t *testing.T) {
	raises(t, "fail.Refusal.Accept", func() {
		var out outcome.Of[int, alphaFail]
		out.Err(alphaRefused{})
	})
	raises(t, "fail.Absence.Accept", func() {
		var out outcome.Of[string, betaFail]
		out.Err(betaMissing{})
	})
}

func TestValueAndFailure(t *testing.T) {
	ok := parseAlpha("abc")
	if !ok.IsOk() || domaintest.Ok(t, ok) != 3 {
		t.Errorf("parseAlpha(abc) is not 3")
	}
	if v, f := outcome.Unpack(ok); v != 3 || f != nil {
		t.Errorf("Unpack = %v, %v", v, f)
	}
	failed := parseAlpha("")
	if failed.IsOk() || domaintest.Fails(t, failed) != "refused alpha.empty" {
		t.Errorf("parseAlpha('') is not refused alpha.empty")
	}
	got := outcome.Fold(failed, func(f alphaFail) string { return domaintest.Describe(f) }, func(int) string { return "ok" })
	if got != "refused alpha.empty" {
		t.Errorf("Fold of a failure = %q", got)
	}
	if n := domaintest.Ok(t, outcome.Map(ok, func(n int) int { return n * 2 })); n != 6 {
		t.Errorf("Map = %d", n)
	}
	if s := domaintest.Fails(t, outcome.Map(failed, func(n int) int { return n })); s != "refused alpha.empty" {
		t.Errorf("Map of a failure = %q", s)
	}
}

func TestMust(t *testing.T) {
	if outcome.Must("test.must", parseAlpha("ab")) != 2 {
		t.Error("Must of a value")
	}
	raises(t, "test.must", func() { outcome.Must("test.must", parseAlpha("")) })
	defer func() {
		d, _ := recover().(defect.Defect)
		if d.Error() != "test.must: a refused failure on input the program made" {
			t.Errorf("Must's defect: %q", d.Error())
		}
	}()
	outcome.Must("test.must", parseAlpha(""))
}

// Widen and Then let one adapter return the failures of two families.
func TestWidenAndThenCarryBothFamilies(t *testing.T) {
	both := func(raw string) outcome.Of[string, fail.Fail] { return outcome.Then(parseAlpha(raw), findBeta) }
	if s := domaintest.Ok(t, both("ab")); s != "bb" {
		t.Errorf("Then of two values = %q", s)
	}
	if s := domaintest.Fails(t, both("")); s != "refused alpha.empty" {
		t.Errorf("Then of the first family's failure = %q", s)
	}
	if s := domaintest.Fails(t, both("abcd")); s != "missing beta.none" {
		t.Errorf("Then of the second family's failure = %q", s)
	}
	if s := domaintest.Fails(t, outcome.Widen(parseAlpha(""))); s != "refused alpha.empty" {
		t.Errorf("Widen of a failure = %q", s)
	}
	if n := domaintest.Ok(t, outcome.Widen(parseAlpha("a"))); n != 1 {
		t.Errorf("Widen of a value = %d", n)
	}
	// The widened failure is still its family's value.
	_, f := outcome.Unpack(both("abcd"))
	if _, ok := f.(betaFail); !ok {
		t.Errorf("Then's failure %T is not the second family's", f)
	}
}
