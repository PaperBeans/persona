package conflict

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestConflictStaysPure guards the package against accidental IO, network, clock,
// or internal/external dependencies: conflict detection must remain a pure function of its inputs.
func TestConflictStaysPure(t *testing.T) {
	allowed := []string{"cmp", "path", "slices", "sort", "strings"}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if !slices.Contains(allowed, p) {
				t.Errorf("%s imports %s: conflict package must stay pure (allowed stdlib only: %v)", name, p, allowed)
			}
		}
	}
}

func TestNormalizePrefix(t *testing.T) {
	cases := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"web/src/views/", "web/src/views", true},
		{"./web/src/views/", "web/src/views", true},
		{"web//src///views", "web/src/views", true},
		{`web\src\views\Todo.vue`, "web/src/views/Todo.vue", true},
		{"  server/internal/db/todos.go  ", "server/internal/db/todos.go", true},
		{"a/b/../c", "a/c", true},
		{".", ".", true},
		{"./", ".", true},
		{"*", ".", true},
		{"./*", ".", true},
		{"**", ".", true},
		{"web/src/*", "web/src", true},
		{"web/src/**", "web/src", true},
		{"", "", false},
		{"   ", "", false},
		{"..", "", false},
		{"../outside", "", false},
		{"a/../../outside", "", false},
		{"/etc/passwd", "", false},
		{`\windows\abs`, "", false},
	}
	for _, tc := range cases {
		got, ok := NormalizePrefix(tc.raw)
		if ok != tc.ok || got != tc.want {
			t.Errorf("NormalizePrefix(%q) = (%q, %v), want (%q, %v)", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}

func TestNormalizeFile(t *testing.T) {
	cases := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"./server/internal/db/todos.go", "server/internal/db/todos.go", true},
		{`web\src\views\Home.vue`, "web/src/views/Home.vue", true},
		{"web//src/lib/util.ts", "web/src/lib/util.ts", true},
		{"", "", false},
		{".", "", false},
		{"./", "", false},
		{"*", "", false},
		{"..", "", false},
		{"../secret.txt", "", false},
		{"/abs/file.go", "", false},
	}
	for _, tc := range cases {
		got, ok := NormalizeFile(tc.raw)
		if ok != tc.ok || got != tc.want {
			t.Errorf("NormalizeFile(%q) = (%q, %v), want (%q, %v)", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}

func TestPrefixesOverlapBoundaries(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		// Strict segment boundary: lib must NOT conflict with lib2.
		{"web/src/lib", "web/src/lib2/a.ts", false},
		{"web/src/lib/", "web/src/lib2/a.ts", false},
		{"web/src/lib", "web/src/lib2", false},
		// Exact match and dir vs dir/ equivalence.
		{"web/src/lib", "web/src/lib", true},
		{"web/src/lib/", "web/src/lib", true},
		{"./web//src/lib/", `web\src\lib`, true},
		// Parent-child containment (both directions).
		{"web/src/lib", "web/src/lib/a.ts", true},
		{"web/src/lib/a.ts", "web/src/lib/", true},
		// Whole-repo wildcards.
		{".", "server/internal/db/todos.go", true},
		{"*", "web/src/views/Home.vue", true},
		{".", "*", true},
		// Disjoint siblings.
		{"server/internal/hub", "server/internal/manager", false},
		// Invalid / escaping paths never match.
		{"../outside", "outside", false},
		{"../outside", ".", false},
		{"", ".", false},
	}
	for _, tc := range cases {
		if got := PrefixesOverlap(tc.a, tc.b); got != tc.want {
			t.Errorf("PrefixesOverlap(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
		if got := PrefixesOverlap(tc.b, tc.a); got != tc.want {
			t.Errorf("PrefixesOverlap(%q, %q) [symmetric] = %v, want %v", tc.b, tc.a, got, tc.want)
		}
	}
}

func TestDetectEmptyAndSingleTask(t *testing.T) {
	if got := Detect(nil); got != nil {
		t.Fatalf("Detect(nil) = %+v, want nil", got)
	}
	if got := Detect([]TaskScope{}); got != nil {
		t.Fatalf("Detect([]) = %+v, want nil", got)
	}
	if got := Detect([]TaskScope{{
		TodoID:        "todo_1",
		RepoKey:       "github.com/nwverse/persona",
		WritePrefixes: []string{"."},
		ChangedFiles:  []string{"README.md"},
	}}); got != nil {
		t.Fatalf("Detect(single) = %+v, want nil", got)
	}
}

func TestDetectCrossRepoIsolationAndEmptyRepoKey(t *testing.T) {
	tasks := []TaskScope{
		{
			TodoID:        "todo_1",
			RepoKey:       "github.com/nwverse/persona",
			WritePrefixes: []string{"."},
			ChangedFiles:  []string{"server/main.go"},
		},
		{
			TodoID:        "todo_2",
			RepoKey:       "example.test/other/repo",
			WritePrefixes: []string{"."},
			ChangedFiles:  []string{"server/main.go"},
		},
		{
			TodoID:        "todo_3",
			RepoKey:       "",
			WritePrefixes: []string{"."},
			ChangedFiles:  []string{"server/main.go"},
		},
		{
			TodoID:        "todo_4",
			RepoKey:       "   ",
			WritePrefixes: []string{"."},
			ChangedFiles:  []string{"server/main.go"},
		},
	}
	if got := Detect(tasks); len(got) != 0 {
		t.Fatalf("cross-repo / empty RepoKey tasks must never conflict, got: %+v", got)
	}
}

func TestDetectMissingStaticDeclarationAndRuntimeCatch(t *testing.T) {
	// Neither task declared static prefixes (or they declared disjoint prefixes),
	// but runtime git diff reveals both edited the same file.
	tasks := []TaskScope{
		{
			TodoID:        "todo_b",
			RepoKey:       "repo/app",
			WritePrefixes: nil,
			ChangedFiles:  []string{"./server/internal/db/todos.go", "server/internal/db/other.go"},
		},
		{
			TodoID:        "todo_a",
			RepoKey:       "repo/app",
			WritePrefixes: []string{"web/src/views/"},
			ChangedFiles:  []string{`server\internal\db\todos.go`, "web/src/views/Home.vue"},
		},
	}
	got := Detect(tasks)
	want := []PairConflict{{
		TodoA:        "todo_a",
		TodoB:        "todo_b",
		RepoKey:      "repo/app",
		Layer:        LayerRuntime,
		Action:       ActionSerialize,
		RuntimeFiles: []string{"server/internal/db/todos.go"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Detect mismatch:\ngot:  %+v\nwant: %+v", got, want)
	}
}

func TestDetectForceParallelOverrideVsRuntimeTruth(t *testing.T) {
	// Case 1: Static-only overlap + ForceParallel on one task -> warn_overridden (parallel allowed).
	staticOnly, ok := DetectPair(
		TaskScope{
			TodoID:        "todo_2",
			RepoKey:       "repo/app",
			WritePrefixes: []string{"web/src/views/"},
			ForceParallel: true,
		},
		TaskScope{
			TodoID:        "todo_1",
			RepoKey:       "repo/app",
			WritePrefixes: []string{"web/src/views/Settings.vue"},
		},
	)
	if !ok {
		t.Fatal("expected static-only conflict")
	}
	wantStatic := PairConflict{
		TodoA:          "todo_1",
		TodoB:          "todo_2",
		RepoKey:        "repo/app",
		Layer:          LayerStatic,
		Action:         ActionWarnOverridden,
		StaticOverlaps: []string{"web/src/views/Settings.vue"},
	}
	if !reflect.DeepEqual(staticOnly, wantStatic) {
		t.Fatalf("static-only override mismatch:\ngot:  %+v\nwant: %+v", staticOnly, wantStatic)
	}

	// Case 2: Both static and runtime overlap + ForceParallel on BOTH tasks ->
	// LayerBoth + ActionSerialize (runtime truth cannot be silently ignored).
	bothLayers, ok := DetectPair(
		TaskScope{
			TodoID:        "todo_2",
			RepoKey:       "repo/app",
			WritePrefixes: []string{"web/src/views/"},
			ChangedFiles:  []string{"web/src/views/Settings.vue", "web/src/views/OnlyA.vue"},
			ForceParallel: true,
		},
		TaskScope{
			TodoID:        "todo_1",
			RepoKey:       "repo/app",
			WritePrefixes: []string{"web/src/views/Settings.vue"},
			ChangedFiles:  []string{"./web/src/views/Settings.vue"},
			ForceParallel: true,
		},
	)
	if !ok {
		t.Fatal("expected both-layer conflict")
	}
	wantBoth := PairConflict{
		TodoA:          "todo_1",
		TodoB:          "todo_2",
		RepoKey:        "repo/app",
		Layer:          LayerBoth,
		Action:         ActionSerialize,
		StaticOverlaps: []string{"web/src/views/Settings.vue"},
		RuntimeFiles:   []string{"web/src/views/Settings.vue"},
	}
	if !reflect.DeepEqual(bothLayers, wantBoth) {
		t.Fatalf("both-layer with ForceParallel mismatch:\ngot:  %+v\nwant: %+v", bothLayers, wantBoth)
	}

	// Case 3: Runtime-only overlap + ForceParallel -> LayerRuntime + ActionSerialize.
	runtimeOnly, ok := DetectPair(
		TaskScope{
			TodoID:        "todo_2",
			RepoKey:       "repo/app",
			WritePrefixes: []string{"server/internal/hub"},
			ChangedFiles:  []string{"server/internal/db/todos.go"},
			ForceParallel: true,
		},
		TaskScope{
			TodoID:        "todo_1",
			RepoKey:       "repo/app",
			WritePrefixes: []string{"server/internal/manager"},
			ChangedFiles:  []string{"server/internal/db/todos.go"},
			ForceParallel: true,
		},
	)
	if !ok {
		t.Fatal("expected runtime-only conflict")
	}
	wantRuntime := PairConflict{
		TodoA:        "todo_1",
		TodoB:        "todo_2",
		RepoKey:      "repo/app",
		Layer:        LayerRuntime,
		Action:       ActionSerialize,
		RuntimeFiles: []string{"server/internal/db/todos.go"},
	}
	if !reflect.DeepEqual(runtimeOnly, wantRuntime) {
		t.Fatalf("runtime-only with ForceParallel mismatch:\ngot:  %+v\nwant: %+v", runtimeOnly, wantRuntime)
	}
}

func TestDetectMultiTaskDeterministicOrdering(t *testing.T) {
	tasks := []TaskScope{
		{
			TodoID:        "todo_z",
			RepoKey:       "repo/beta",
			WritePrefixes: []string{"*"},
			ChangedFiles:  []string{"go.mod"},
		},
		{
			TodoID:        "todo_a",
			RepoKey:       "repo/beta",
			WritePrefixes: []string{"pkg/api/"},
			ChangedFiles:  []string{"go.mod"},
		},
		{
			TodoID:        "todo_3",
			RepoKey:       "repo/alpha",
			WritePrefixes: []string{"web/src/lib2/"},
			ChangedFiles:  []string{"web/src/shared.ts"},
		},
		{
			TodoID:        "todo_2",
			RepoKey:       "repo/alpha",
			WritePrefixes: []string{"web/src/lib"},
			ChangedFiles:  []string{"web/src/shared.ts"},
			ForceParallel: true,
		},
		{
			TodoID:        "todo_1",
			RepoKey:       "repo/alpha",
			WritePrefixes: []string{"web/src/lib/util.ts", "./web/src/lib/util.ts"},
			ForceParallel: true,
		},
	}

	want := []PairConflict{
		{
			TodoA:          "todo_1",
			TodoB:          "todo_2",
			RepoKey:        "repo/alpha",
			Layer:          LayerStatic,
			Action:         ActionWarnOverridden,
			StaticOverlaps: []string{"web/src/lib/util.ts"},
		},
		{
			TodoA:        "todo_2",
			TodoB:        "todo_3",
			RepoKey:      "repo/alpha",
			Layer:        LayerRuntime,
			Action:       ActionSerialize,
			RuntimeFiles: []string{"web/src/shared.ts"},
		},
		{
			TodoA:          "todo_a",
			TodoB:          "todo_z",
			RepoKey:        "repo/beta",
			Layer:          LayerBoth,
			Action:         ActionSerialize,
			StaticOverlaps: []string{"pkg/api"},
			RuntimeFiles:   []string{"go.mod"},
		},
	}

	got := Detect(tasks)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Detect mismatch:\ngot:  %+v\nwant: %+v", got, want)
	}

	// Reversing input order must yield identical output.
	rev := slices.Clone(tasks)
	slices.Reverse(rev)
	if gotRev := Detect(rev); !reflect.DeepEqual(gotRev, want) {
		t.Fatalf("Detect not deterministic on reversed input:\ngot:  %+v\nwant: %+v", gotRev, want)
	}
}
