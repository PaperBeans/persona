// Package outcome is the one channel for expected failures: an Of is a value or a failure of the producer's sealed
// family, never both, and an Of nobody set is a defect wherever it is read, never a success. Producers name their
// result and set it on every path (`func Parse(raw string) (out outcome.Of[ID, Fail]) { ... return out.Ok(id) }`);
// callers read it through Fold, Unpack or the adapters' Widen and Then (docs/server/architecture.md "Domain model",
// "Failures").
//
//persona:layer kit
package outcome

import (
	"github.com/nwverse/persona/server/internal/domainkit/defect"
	"github.com/nwverse/persona/server/internal/domainkit/fail"
)

// side is which side of an Of holds: a closed set over its own field, so no primitive stands in for it. The zero side
// is neither, an unset Of's.
type side struct{ side uint8 }

// sideValue is an Of that holds its value.
func sideValue() side { return side{side: 1} }

// sideFailure is an Of that holds its failure.
func sideFailure() side { return side{side: 2} }

// Of is a value of type T or a failure of family F. The zero Of is unset: every reader raises a defect on it.
type Of[T any, F fail.Fail] struct {
	side    side
	value   T
	failure F
}

// Ok is the outcome holding v. Producers call it on their named result: return out.Ok(v).
func (o *Of[T, F]) Ok(v T) Of[T, F] { return Of[T, F]{side: sideValue(), value: v} }

// Err is the outcome holding f. Producers call it on their named result: return out.Err(f). A nil failure is a
// defect, and so is a zero one: Err visits f, and a base's Accept raises on its zero value.
func (o *Of[T, F]) Err(f F) Of[T, F] {
	if any(f) == nil {
		defect.Raise("outcome.Err", "a nil failure")
	}
	f.Accept(checked{})
	return Of[T, F]{side: sideFailure(), failure: f}
}

// checked is the visitor Err hands a failure to: visiting is the check, so it does nothing.
type checked struct{}

func (checked) VisitInvalid(fail.Code, fail.Part) {}
func (checked) VisitRefused(fail.Code)            {}
func (checked) VisitMissing(fail.Code)            {}
func (checked) VisitConflict(fail.Code)           {}

// IsOk says whether o holds a value.
func (o Of[T, F]) IsOk() bool { return o.held("outcome.IsOk") == sideValue() }

// held is which side o holds; an unset o is a defect.
func (o Of[T, F]) held(op string) side {
	o.expectSet(op)
	return o.side
}

// expectSet raises a defect, naming op, when o is unset: a reader that needs no side still never reads an unset o.
func (o Of[T, F]) expectSet(op string) {
	if o.side == (side{}) {
		defect.Raise(op, "an unset outcome: its producer returned without Ok or Err")
	}
}

// Unpack is o's value and failure; exactly one of them is set (the failure is nil when o holds a value).
func Unpack[T any, F fail.Fail](o Of[T, F]) (T, F) {
	o.expectSet("outcome.Unpack")
	return o.value, o.failure
}

// Fold is onOk of o's value or onFail of its failure.
func Fold[T any, F fail.Fail, R any](o Of[T, F], onFail func(F) R, onOk func(T) R) R {
	if o.held("outcome.Fold") == sideFailure() {
		return onFail(o.failure)
	}
	return onOk(o.value)
}

// Widen is o with its failure as a fail.Fail, so an adapter can return failures of several families in one Of.
func Widen[T any, F fail.Fail](o Of[T, F]) Of[T, fail.Fail] {
	if o.held("outcome.Widen") == sideFailure() {
		return Of[T, fail.Fail]{side: sideFailure(), failure: o.failure}
	}
	return Of[T, fail.Fail]{side: sideValue(), value: o.value}
}

// Then is next of o's value, or o's failure: a step that may fail with another family, as an adapter chains a
// shared kernel's Parse and its own core's constructor.
func Then[A, B any, F, G fail.Fail](o Of[A, F], next func(A) Of[B, G]) Of[B, fail.Fail] {
	if o.held("outcome.Then") == sideFailure() {
		return Of[B, fail.Fail]{side: sideFailure(), failure: o.failure}
	}
	return Widen(next(o.value))
}

// Map is f of o's value, or o's failure.
func Map[A, B any, F fail.Fail](o Of[A, F], f func(A) B) Of[B, F] {
	if o.held("outcome.Map") == sideFailure() {
		return Of[B, F]{side: sideFailure(), failure: o.failure}
	}
	return Of[B, F]{side: sideValue(), value: f(o.value)}
}

// Must is o's value, for input the program made itself (a constant, an id it generated): a failure there is a
// programmer error, so it raises a defect naming op and the failure's kind.
func Must[T any, F fail.Fail](op string, o Of[T, F]) T {
	if o.held(op) == sideFailure() {
		o.failure.Accept(raising{op: op})
	}
	return o.value
}

// raising is the visitor Must hands a failure to: told the failure's kind, it raises the defect.
type raising struct{ op string }

func (r raising) VisitInvalid(fail.Code, fail.Part) { r.raise("invalid") }
func (r raising) VisitRefused(fail.Code)            { r.raise("refused") }
func (r raising) VisitMissing(fail.Code)            { r.raise("missing") }
func (r raising) VisitConflict(fail.Code)           { r.raise("conflict") }

func (r raising) raise(kind string) {
	defect.Raise(r.op, "a "+kind+" failure on input the program made")
}
