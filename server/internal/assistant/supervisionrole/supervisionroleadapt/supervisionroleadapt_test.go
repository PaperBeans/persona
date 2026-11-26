package supervisionroleadapt_test

import (
	"testing"

	"github.com/nwverse/persona/server/internal/assistant/supervisionrole"
	"github.com/nwverse/persona/server/internal/assistant/supervisionrole/supervisionroleadapt"
	"github.com/nwverse/persona/server/internal/domainkit/opt"
)

// roles are every role with the word the manager stores for it; stored directions and settings already hold these
// words, so they never change.
var roles = []struct {
	word string
	role supervisionrole.Role
}{
	{"main", supervisionrole.RoleMain()},
	{"leader", supervisionrole.RoleLeader()},
	{"background", supervisionrole.RoleBackground()},
	{"side", supervisionrole.RoleSide()},
}

// restored is the role o holds, and whether it holds one.
func restored(o opt.Option[supervisionrole.Role]) (role supervisionrole.Role, ok bool) {
	o.Match(func(r supervisionrole.Role) { role, ok = r, true }, func() {})
	return role, ok
}

func TestStoreAndRestoreKeepEveryStoredWord(t *testing.T) {
	for _, tc := range roles {
		if got := supervisionroleadapt.Store(tc.role).Role; got != tc.word {
			t.Errorf("Store(%s) = %q", tc.word, got)
		}
		role, ok := restored(supervisionroleadapt.Restore(supervisionroleadapt.Record{Role: tc.word}))
		if !ok || role != tc.role {
			t.Errorf("Restore(%q) is not the %s role", tc.word, tc.word)
			continue
		}
		if again := supervisionroleadapt.Store(role).Role; again != tc.word {
			t.Errorf("%q restored and stored again is %q", tc.word, again)
		}
	}
}

func TestRestoreNamesNoRoleForAnUnknownWord(t *testing.T) {
	for _, word := range []string{"", "worker", "MAIN", " main", "lead"} {
		if _, ok := restored(supervisionroleadapt.Restore(supervisionroleadapt.Record{Role: word})); ok {
			t.Errorf("Restore(%q) names a role; want none, so its readers fail closed", word)
		}
	}
}
