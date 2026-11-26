package assistant

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// managerCheckValues is the list the manager migration's CHECK (column IN (...)) constraint named name allows, read
// from server/internal/db/migrations/manager. It fails the test when no migration, or more than one, adds it.
func managerCheckValues(t *testing.T, name string) []string {
	t.Helper()
	pattern := regexp.MustCompile(`(?is)CONSTRAINT\s+` + regexp.QuoteMeta(name) + `\s+CHECK\s*\(\s*\w+\s+IN\s*\(([^)]*)\)\s*\)`)
	files, err := filepath.Glob(filepath.Join("..", "db", "migrations", "manager", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no manager migrations found: %v", err)
	}
	var found [][]string
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range pattern.FindAllStringSubmatch(string(raw), -1) {
			var values []string
			for _, v := range regexp.MustCompile(`'((?:[^']|'')*)'`).FindAllStringSubmatch(m[1], -1) {
				values = append(values, strings.ReplaceAll(v[1], "''", "'"))
			}
			found = append(found, values)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d manager migrations add constraint %s, want 1", len(found), name)
	}
	return found[0]
}

// TestStorageChecksMatchTheCode keeps the CHECKs on the assistant's enum-like columns (#15397,
// docs/plans/plan-typed-core.md step P9) and the constants the assistant writes together; db.TestStorageChecksMatchTheCode
// has the ones the db package can see.
func TestStorageChecksMatchTheCode(t *testing.T) {
	for _, c := range []struct {
		name string
		want []string
	}{
		{"a2a_tasks_state_check", []string{a2aSubmitted, a2aWorking, a2aInput, a2aAuth, a2aComplete, a2aFailed, a2aCanceled, a2aQueued}},
		{"urgent_calls_state_check", []string{callRinging, callAnswered, callDeclined, callMissed, callEnded, callFailed, callCancelled}},
		{"task_plans_state_check", []string{planOpen, planStarted, planDismissed}},
		{"assistant_captures_kind_check", captureKinds},
		{"assistant_captures_status_check", captureStatuses},
	} {
		got, want := slices.Clone(managerCheckValues(t, c.name)), slices.Clone(c.want)
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s allows %q, the code writes %q: change the code and add a migration with the new CHECK together",
				c.name, got, want)
		}
	}
}
