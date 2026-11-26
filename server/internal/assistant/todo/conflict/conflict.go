// Package conflict detects write-scope conflicts between concurrent tasks targeting the same repository.
//
// It is pure: no IO, no clock, no network, and no dependencies outside the standard library.
// Detection operates in two layers:
//   - Layer 1 (static): path-prefix overlap between declared write scopes (including whole-repo "." / "*").
//   - Layer 2 (runtime): exact file intersection between actual changed files (e.g. from git diff --name-only).
//
// An explicit ForceParallel override downgrades static-only conflicts to ActionWarnOverridden,
// while runtime file intersections always produce ActionSerialize so callers can downgrade to serial merge/execution.
package conflict

import (
	"cmp"
	"path"
	"slices"
	"strings"
)

const (
	// LayerStatic indicates only declared write prefixes overlapped.
	LayerStatic = "static"
	// LayerRuntime indicates only actual changed files intersected.
	LayerRuntime = "runtime"
	// LayerBoth indicates both declared write prefixes and actual changed files overlapped.
	LayerBoth = "both"

	// ActionSerialize indicates the two tasks should be serialized (or downgraded to serial merge).
	ActionSerialize = "serialize"
	// ActionWarnOverridden indicates a static-only overlap was explicitly overridden via ForceParallel.
	ActionWarnOverridden = "warn_overridden"
)

// TaskScope describes a single task's repository target, declared write prefixes,
// runtime changed files, and explicit parallel override flag.
type TaskScope struct {
	TodoID        string
	RepoKey       string
	WritePrefixes []string
	ChangedFiles  []string
	ForceParallel bool
}

// PairConflict describes a detected write-scope conflict between two tasks in the same repository.
// TodoA is always lexicographically smaller than TodoB.
type PairConflict struct {
	TodoA          string   `json:"todo_a"`
	TodoB          string   `json:"todo_b"`
	RepoKey        string   `json:"repo_key"`
	Layer          string   `json:"layer"`
	Action         string   `json:"action"`
	StaticOverlaps []string `json:"static_overlaps,omitempty"`
	RuntimeFiles   []string `json:"runtime_files,omitempty"`
}

// NormalizePrefix canonicalizes a declared write prefix into a clean repo-relative POSIX path.
// Whole-repo wildcards ("." and "*") normalize to ".".
// Trailing slashes and trailing wildcard markers ("/*" or "/**") are stripped.
// Empty paths, absolute paths, and paths that escape the repository root via ".." are rejected.
func NormalizePrefix(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false
	}
	s = strings.ReplaceAll(s, "\\", "/")
	if strings.HasPrefix(s, "/") {
		return "", false
	}
	for {
		switch {
		case strings.HasSuffix(s, "/**"):
			s = strings.TrimSuffix(s, "/**")
		case strings.HasSuffix(s, "/*"):
			s = strings.TrimSuffix(s, "/*")
		default:
			goto doneWildcard
		}
	}
doneWildcard:
	if s == "" || s == "*" || s == "**" {
		return ".", true
	}
	cleaned := path.Clean(s)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	if cleaned == "." {
		return ".", true
	}
	return cleaned, true
}

// NormalizeFile canonicalizes a runtime changed file path (e.g. from git diff --name-only)
// into a clean repo-relative POSIX path.
// Empty paths, absolute paths, whole-repo markers ("." / "*"), and ".." escapes are rejected.
func NormalizeFile(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false
	}
	s = strings.ReplaceAll(s, "\\", "/")
	if strings.HasPrefix(s, "/") {
		return "", false
	}
	if s == "*" || s == "**" {
		return "", false
	}
	cleaned := path.Clean(s)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	return cleaned, true
}

// PrefixesOverlap reports whether two normalized write prefixes overlap on a strict path-segment boundary.
// For example, "web/src/lib" overlaps "web/src/lib" and "web/src/lib/a.ts",
// but does NOT overlap "web/src/lib2/a.ts".
// "." represents the entire repository and overlaps every valid prefix.
func PrefixesOverlap(a, b string) bool {
	na, okA := NormalizePrefix(a)
	nb, okB := NormalizePrefix(b)
	if !okA || !okB {
		return false
	}
	_, ok := overlapPath(na, nb)
	return ok
}

// overlapPath returns the narrower overlapping path between two already-normalized prefixes.
func overlapPath(a, b string) (string, bool) {
	if a == "." {
		return b, true
	}
	if b == "." {
		return a, true
	}
	if a == b {
		return a, true
	}
	if strings.HasPrefix(b, a+"/") {
		return b, true
	}
	if strings.HasPrefix(a, b+"/") {
		return a, true
	}
	return "", false
}

type normalizedTask struct {
	todoID        string
	repoKey       string
	prefixes      []string
	changedFiles  map[string]struct{}
	forceParallel bool
}

func normalizeTask(t TaskScope) (normalizedTask, bool) {
	todoID := strings.TrimSpace(t.TodoID)
	repoKey := strings.TrimSpace(t.RepoKey)
	if todoID == "" || repoKey == "" {
		return normalizedTask{}, false
	}
	var prefixes []string
	if len(t.WritePrefixes) > 0 {
		seen := make(map[string]struct{}, len(t.WritePrefixes))
		for _, raw := range t.WritePrefixes {
			norm, ok := NormalizePrefix(raw)
			if !ok {
				continue
			}
			if _, dup := seen[norm]; dup {
				continue
			}
			seen[norm] = struct{}{}
			prefixes = append(prefixes, norm)
		}
		slices.Sort(prefixes)
	}
	var changed map[string]struct{}
	if len(t.ChangedFiles) > 0 {
		changed = make(map[string]struct{}, len(t.ChangedFiles))
		for _, raw := range t.ChangedFiles {
			norm, ok := NormalizeFile(raw)
			if !ok {
				continue
			}
			changed[norm] = struct{}{}
		}
	}
	return normalizedTask{
		todoID:        todoID,
		repoKey:       repoKey,
		prefixes:      prefixes,
		changedFiles:  changed,
		forceParallel: t.ForceParallel,
	}, true
}

// DetectPair evaluates a single pair of tasks for static and runtime write-scope conflicts.
// Tasks targeting different repositories, missing a RepoKey or TodoID, or sharing the same TodoID
// never conflict and return (PairConflict{}, false).
func DetectPair(a, b TaskScope) (PairConflict, bool) {
	na, okA := normalizeTask(a)
	nb, okB := normalizeTask(b)
	if !okA || !okB {
		return PairConflict{}, false
	}
	return detectNormalizedPair(na, nb)
}

func detectNormalizedPair(a, b normalizedTask) (PairConflict, bool) {
	if a.repoKey != b.repoKey || a.todoID == b.todoID {
		return PairConflict{}, false
	}

	var staticOverlaps []string
	if len(a.prefixes) > 0 && len(b.prefixes) > 0 {
		seen := make(map[string]struct{})
		for _, pa := range a.prefixes {
			for _, pb := range b.prefixes {
				if overlap, ok := overlapPath(pa, pb); ok {
					if _, dup := seen[overlap]; !dup {
						seen[overlap] = struct{}{}
						staticOverlaps = append(staticOverlaps, overlap)
					}
				}
			}
		}
		slices.Sort(staticOverlaps)
	}

	var runtimeFiles []string
	if len(a.changedFiles) > 0 && len(b.changedFiles) > 0 {
		small, large := a.changedFiles, b.changedFiles
		if len(small) > len(large) {
			small, large = large, small
		}
		for f := range small {
			if _, ok := large[f]; ok {
				runtimeFiles = append(runtimeFiles, f)
			}
		}
		slices.Sort(runtimeFiles)
	}

	hasStatic := len(staticOverlaps) > 0
	hasRuntime := len(runtimeFiles) > 0
	if !hasStatic && !hasRuntime {
		return PairConflict{}, false
	}

	todoA, todoB := a.todoID, b.todoID
	if todoA > todoB {
		todoA, todoB = todoB, todoA
	}

	var layer string
	switch {
	case hasStatic && hasRuntime:
		layer = LayerBoth
	case hasRuntime:
		layer = LayerRuntime
	default:
		layer = LayerStatic
	}

	action := ActionSerialize
	if !hasRuntime && (a.forceParallel || b.forceParallel) {
		action = ActionWarnOverridden
	}

	return PairConflict{
		TodoA:          todoA,
		TodoB:          todoB,
		RepoKey:        a.repoKey,
		Layer:          layer,
		Action:         action,
		StaticOverlaps: staticOverlaps,
		RuntimeFiles:   runtimeFiles,
	}, true
}

// Detect evaluates all pairs of tasks and returns all detected conflicts in deterministic order
// (sorted by RepoKey ascending, then TodoA ascending, then TodoB ascending).
// Tasks with an empty RepoKey or empty TodoID are ignored.
// Duplicate pairs with identical (RepoKey, TodoA, TodoB) are not emitted more than once.
func Detect(tasks []TaskScope) []PairConflict {
	if len(tasks) < 2 {
		return nil
	}
	norm := make([]normalizedTask, 0, len(tasks))
	for _, t := range tasks {
		if nt, ok := normalizeTask(t); ok {
			norm = append(norm, nt)
		}
	}
	if len(norm) < 2 {
		return nil
	}

	// Sort normalized tasks by TodoID before pairwise evaluation so duplicate-ID handling is order-independent.
	slices.SortFunc(norm, func(a, b normalizedTask) int {
		return cmp.Or(strings.Compare(a.repoKey, b.repoKey), strings.Compare(a.todoID, b.todoID))
	})

	type pairKey struct {
		repo  string
		todoA string
		todoB string
	}
	seenPairs := make(map[pairKey]struct{})
	var out []PairConflict
	for i := 0; i < len(norm); i++ {
		for j := i + 1; j < len(norm); j++ {
			if norm[i].repoKey != norm[j].repoKey {
				break
			}
			c, ok := detectNormalizedPair(norm[i], norm[j])
			if !ok {
				continue
			}
			pk := pairKey{repo: c.RepoKey, todoA: c.TodoA, todoB: c.TodoB}
			if _, dup := seenPairs[pk]; dup {
				continue
			}
			seenPairs[pk] = struct{}{}
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return nil
	}
	slices.SortFunc(out, func(a, b PairConflict) int {
		return cmp.Or(
			strings.Compare(a.RepoKey, b.RepoKey),
			strings.Compare(a.TodoA, b.TodoA),
			strings.Compare(a.TodoB, b.TodoB),
		)
	})
	return out
}
