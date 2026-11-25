package agentref_test

import (
	"testing"

	"github.com/nwverse/persona/server/internal/agentref"
	"github.com/nwverse/persona/server/internal/domainkit/defect"
	"github.com/nwverse/persona/server/internal/domainkit/domaintest"
)

// someRef is a valid session.
func someRef(t *testing.T) agentref.Ref {
	t.Helper()
	return domaintest.Ok(t, agentref.ParseRef("machine 1", "session 1"))
}

// someMachineID is a valid machine.
func someMachineID(t *testing.T) agentref.MachineID {
	t.Helper()
	return domaintest.Ok(t, agentref.ParseMachineID("machine 1"))
}

// raises fails the test unless do raises the defect want ("<op>: <cause>").
func raises(t *testing.T, want string, do func()) {
	t.Helper()
	defer func() {
		t.Helper()
		r := recover()
		if d, ok := r.(defect.Defect); !ok || d.Error() != want {
			t.Errorf("raised %v, want the defect %q", r, want)
		}
	}()
	do()
}

func TestParseRefNeverMakesTheZeroRef(t *testing.T) {
	var zero agentref.Ref
	if someRef(t) == zero {
		t.Error("ParseRef made the zero Ref")
	}
}

// The zero Ref is no session and the zero MachineID no machine: every method handed one raises a defect, so neither
// stands for "none" (that is opt.Option[Ref]) and neither leaves for the wire as empty text.
func TestTheZeroRefIsADefect(t *testing.T) {
	var zero agentref.Ref
	var noMachine agentref.MachineID
	raises(t, "agentref.Ref.OnMachine: the zero Ref", func() { zero.OnMachine(someMachineID(t)) })
	raises(t, "agentref.Ref.OnMachine: the zero MachineID", func() { someRef(t).OnMachine(noMachine) })
	raises(t, "agentref.Ref.IsSame: the zero Ref", func() { zero.IsSame(someRef(t)) })
	raises(t, "agentref.Ref.IsSame: the zero Ref", func() { someRef(t).IsSame(zero) })
	raises(t, "agentref.Ref.Accept: the zero Ref", func() { zero.Accept(nil) })
}

func TestParseRefNamesEachEmptyPart(t *testing.T) {
	for _, c := range []struct {
		machine, session, want string
	}{
		{"", "session 1", "invalid agentref.machine.empty machine"},
		{"machine 1", " ", "invalid agentref.session.empty session"},
		{" ", "", "invalid agentref.machine.empty machine; invalid agentref.session.empty session"},
	} {
		if got := domaintest.Fails(t, agentref.ParseRef(c.machine, c.session)); got != c.want {
			t.Errorf("ParseRef(%q, %q) = %q, want %q", c.machine, c.session, got, c.want)
		}
	}
}

func TestParseMachineIDAndParseInteractionIDRefuseEmptyIDs(t *testing.T) {
	var noMachine agentref.MachineID
	if someMachineID(t) == noMachine {
		t.Error("ParseMachineID made the zero MachineID")
	}
	if got, want := domaintest.Fails(t, agentref.ParseMachineID(" ")), "invalid agentref.machine.empty machine"; got != want {
		t.Errorf("ParseMachineID = %q, want %q", got, want)
	}
	var noCard agentref.InteractionID
	if domaintest.Ok(t, agentref.ParseInteractionID("card 1")) == noCard {
		t.Error("ParseInteractionID made the zero InteractionID")
	}
	if got, want := domaintest.Fails(t, agentref.ParseInteractionID("")), "invalid agentref.card.empty card"; got != want {
		t.Errorf("ParseInteractionID = %q, want %q", got, want)
	}
}

func TestRefsKeyMaps(t *testing.T) {
	other := domaintest.Ok(t, agentref.ParseRef("machine 1", "session 2"))
	seen := map[agentref.Ref]int{someRef(t): 1, other: 2}
	if seen[someRef(t)] != 1 || seen[other] != 2 || len(seen) != 2 {
		t.Errorf("two sessions keyed %v", seen)
	}
	if !someRef(t).IsSame(someRef(t)) || someRef(t).IsSame(other) {
		t.Error("IsSame does not tell sessions apart by machine and session")
	}
}

func TestOnMachine(t *testing.T) {
	elsewhere := domaintest.Ok(t, agentref.ParseRef("machine 2", "session 1"))
	if !someRef(t).OnMachine(someMachineID(t)) || elsewhere.OnMachine(someMachineID(t)) {
		t.Error("OnMachine does not tell a session's machine")
	}
}

// refParts is the test's own visitor: it keeps the parts it is told.
type refParts struct {
	machine agentref.MachineID
	told    bool
}

func (r *refParts) VisitRef(machine agentref.MachineID, _ agentref.SessionID) {
	r.machine, r.told = machine, true
}

func TestAcceptTellsTheMachineAndTheSession(t *testing.T) {
	var got refParts
	someRef(t).Accept(&got)
	if !got.told || got.machine != someMachineID(t) {
		t.Error("Accept told another machine")
	}
}
