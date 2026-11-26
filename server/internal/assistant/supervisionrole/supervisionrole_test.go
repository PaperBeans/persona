package supervisionrole_test

import (
	"slices"
	"testing"

	"github.com/nwverse/persona/server/internal/assistant/supervisionrole"
	"github.com/nwverse/persona/server/internal/domainkit/defect"
)

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

func TestTheZeroRoleIsADefect(t *testing.T) {
	var zero supervisionrole.Role
	raises(t, "supervisionrole.Role.Accept: the zero Role", func() { zero.Accept(nil) })
	raises(t, "supervisionrole.Role.IsLeader: the zero Role", func() { zero.IsLeader() })
	raises(t, "supervisionrole.Role.IsBackground: the zero Role", func() { zero.IsBackground() })
}

// roleNames is the test's own visitor: it keeps the name of each role it is told.
type roleNames struct{ names []string }

func (r *roleNames) VisitMain()       { r.names = append(r.names, "main") }
func (r *roleNames) VisitLeader()     { r.names = append(r.names, "leader") }
func (r *roleNames) VisitBackground() { r.names = append(r.names, "background") }
func (r *roleNames) VisitSide()       { r.names = append(r.names, "side") }

func TestRoleAcceptTellsEachRole(t *testing.T) {
	var got roleNames
	for _, x := range []supervisionrole.Role{supervisionrole.RoleMain(), supervisionrole.RoleLeader(), supervisionrole.RoleBackground(), supervisionrole.RoleSide()} {
		x.Accept(&got)
	}
	if want := []string{"main", "leader", "background", "side"}; !slices.Equal(got.names, want) {
		t.Errorf("Accept told %q, want %q", got.names, want)
	}
}

func TestOnlyALeaderAndABackgroundSupervisorCarryTheirOwnParagraphs(t *testing.T) {
	for _, tc := range []struct {
		name               string
		role               supervisionrole.Role
		leader, background bool
	}{
		{"main", supervisionrole.RoleMain(), false, false},
		{"leader", supervisionrole.RoleLeader(), true, false},
		{"background", supervisionrole.RoleBackground(), false, true},
		{"side", supervisionrole.RoleSide(), false, false},
	} {
		if got := tc.role.IsLeader(); got != tc.leader {
			t.Errorf("%s: IsLeader = %v, want %v", tc.name, got, tc.leader)
		}
		if got := tc.role.IsBackground(); got != tc.background {
			t.Errorf("%s: IsBackground = %v, want %v", tc.name, got, tc.background)
		}
	}
}

func TestRolesAreDistinct(t *testing.T) {
	all := []supervisionrole.Role{supervisionrole.RoleMain(), supervisionrole.RoleLeader(), supervisionrole.RoleBackground(), supervisionrole.RoleSide()}
	for i := range all {
		for j := range all {
			if (all[i] == all[j]) != (i == j) {
				t.Errorf("roles %d and %d compare %v", i, j, all[i] == all[j])
			}
		}
	}
}
