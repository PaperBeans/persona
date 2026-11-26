package defect_test

import (
	"errors"
	"testing"

	"github.com/nwverse/persona/server/internal/domainkit/defect"
)

func TestRaisePanicsWithADefect(t *testing.T) {
	defer func() {
		r := recover()
		d, ok := r.(defect.Defect)
		if !ok {
			t.Fatalf("Raise panicked with %#v, want a defect.Defect", r)
		}
		if d.Error() != "outcome.Unpack: unset outcome" {
			t.Errorf("Error() = %q", d.Error())
		}
	}()
	defect.Raise("outcome.Unpack", "unset outcome")
}

func TestRaiseRefusesAnEmptyOpOrCause(t *testing.T) {
	for _, c := range [][2]string{{"", "cause"}, {"op", ""}} {
		func() {
			defer func() {
				d, ok := recover().(defect.Defect)
				if !ok || d.Error() != "defect.Raise: a defect needs its operation and its cause" {
					t.Errorf("Raise(%q, %q): %v", c[0], c[1], d)
				}
			}()
			defect.Raise(c[0], c[1])
		}()
	}
}

func TestCatchReportsDefectsOnly(t *testing.T) {
	var got []string
	func() {
		defer defect.Catch(func(d defect.Defect) { got = append(got, d.Error()) })
		defect.Raise("op", "cause")
	}()
	if len(got) != 1 || got[0] != "op: cause" {
		t.Fatalf("caught %q", got)
	}

	other := errors.New("not a defect")
	defer func() {
		if r := recover(); r != other {
			t.Fatalf("Catch let through %v, want the original panic", r)
		}
	}()
	func() {
		defer defect.Catch(func(defect.Defect) { t.Error("reported a panic that is no defect") })
		panic(other)
	}()
}

func TestCatchWithoutPanicReportsNothing(t *testing.T) {
	func() {
		defer defect.Catch(func(defect.Defect) { t.Error("reported without a panic") })
	}()
}
