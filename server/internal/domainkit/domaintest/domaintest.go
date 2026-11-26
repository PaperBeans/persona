// Package domaintest is short test lines for cores: the value of an outcome that must hold one, and a failure by its
// kind and code ("refused inbox.item.not_done"), so a test asserts what a core did without a getter added for it
// (docs/server/architecture.md "Domain model", "Tests").
//
//persona:layer testkit
package domaintest

import (
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/domainkit/fail"
	"github.com/nwverse/persona/server/internal/domainkit/outcome"
)

// Ok is o's value; the test fails at once when o holds a failure.
func Ok[T any, F fail.Fail](t testing.TB, o outcome.Of[T, F]) T {
	t.Helper()
	v, f := outcome.Unpack(o)
	if any(f) != nil {
		t.Fatalf("want a value, got the failure %s", Describe(f))
	}
	return v
}

// Fails is o's failure as Describe gives it; the test fails at once when o holds a value.
func Fails[T any, F fail.Fail](t testing.TB, o outcome.Of[T, F]) string {
	t.Helper()
	v, f := outcome.Unpack(o)
	if any(f) == nil {
		t.Fatalf("want a failure, got the value %+v", v)
	}
	return Describe(f)
}

// Describe is a failure's kind and code ("refused inbox.item.not_done", "invalid agentref.machine.empty machine"),
// one per failure joined by "; " for a list of invalid parts. A nil failure is "none".
func Describe(f fail.Fail) string {
	if f == nil {
		return "none"
	}
	var d describe
	f.Accept(&d)
	return strings.Join(d.lines, "; ")
}

type describe struct{ lines []string }

func (d *describe) VisitInvalid(code fail.Code, part fail.Part) {
	d.lines = append(d.lines, "invalid "+code.Wire()+" "+part.Wire())
}
func (d *describe) VisitRefused(code fail.Code)  { d.lines = append(d.lines, "refused "+code.Wire()) }
func (d *describe) VisitMissing(code fail.Code)  { d.lines = append(d.lines, "missing "+code.Wire()) }
func (d *describe) VisitConflict(code fail.Code) { d.lines = append(d.lines, "conflict "+code.Wire()) }
