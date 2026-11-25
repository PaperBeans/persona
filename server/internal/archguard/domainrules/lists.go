package domainrules

import (
	"os"
	"path/filepath"
	"strings"
)

// Lists is what the rules read besides the code, from archguard/testdata: the contract packages, and the three lists
// that hold the code that predates the rules (docs/operations/testing.md "Count ratchets").
type Lists struct {
	Contracts  []string // contracts.txt: paths under the module, or another module's import paths
	Undeclared Names    // domain_layers/undeclared.txt: the packages frozen without a layer
	Edges      Names    // domain_layers/legacy_edges.txt: imports frozen by name, "<pkg> imports <path>"
	Baseline   Baseline // domain_layers/baseline.txt: shrink-only counts, "<key> <rule> <count> [#PR ...]"
	Problems   []string // what is wrong with the files themselves
}

// The lists' files, under archguard/testdata.
const (
	contractsFile  = "contracts.txt"
	undeclaredFile = "domain_layers/undeclared.txt"
	edgesFile      = "domain_layers/legacy_edges.txt"
	baselineFile   = "domain_layers/baseline.txt"
)

// ReadLists reads the lists from dir, archguard's testdata directory.
func ReadLists(dir string) (Lists, error) {
	var l Lists
	text := map[string]string{}
	for _, name := range []string{contractsFile, undeclaredFile, edgesFile, baselineFile} {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			return l, err
		}
		text[name] = string(b)
	}
	return ParseLists(text[contractsFile], text[undeclaredFile], text[edgesFile], text[baselineFile]), nil
}

// ParseLists reads the lists from their text.
func ParseLists(contracts, undeclaredText, edges, baseline string) Lists {
	var l Lists
	for _, line := range strings.Split(contracts, "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			l.Contracts = append(l.Contracts, line)
		}
	}
	var problems []string
	l.Undeclared, problems = ReadNames(undeclaredText)
	for _, p := range problems {
		l.Problems = append(l.Problems, undeclaredFile+": "+p)
	}
	l.Edges, problems = ReadNames(edges)
	for _, p := range problems {
		l.Problems = append(l.Problems, edgesFile+": "+p)
	}
	l.Baseline, problems = ReadBaseline(baseline)
	for _, p := range problems {
		l.Problems = append(l.Problems, baselineFile+": "+p)
	}
	return l
}

// Names is a freeze list's entries (docs/operations/testing.md "Count ratchets", the third kind): the names a list had
// when it was frozen, then the pending ones, each with the open PRs that add it. scripts/test/name_freezes.py writes
// the pending part, the format archguard's central lists use.
type Names struct{ Entries []NameEntry }

// A NameEntry is one line of a freeze list: its line number, the name and, for a pending one, its PRs.
type NameEntry struct {
	Line int
	Name string
	PRs  []string
}

// Has is the entry for name, and whether the list has one.
func (n Names) Has(name string) (NameEntry, bool) {
	for _, e := range n.Entries {
		if e.Name == name {
			return e, true
		}
	}
	return NameEntry{}, false
}

// ReadNames reads a freeze list: comment lines start with #; an entry is a name, a pending one followed by its PRs
// (NAME #123 #456); names stay sorted and unique within each part, and the pending ones come last.
func ReadNames(text string) (Names, []string) {
	var n Names
	var problems []string
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		k := len(fields)
		for k > 0 && prNumber(fields[k-1]) {
			k--
		}
		if k == 0 {
			problems = append(problems, "line "+itoa(i+1)+" has PRs but no name")
			continue
		}
		name := strings.Join(fields[:k], " ")
		n.Entries = append(n.Entries, NameEntry{i + 1, name, fields[k:]})
	}
	seen := map[string]int{}
	pending := -1
	for i, e := range n.Entries {
		if first, dup := seen[e.Name]; dup {
			problems = append(problems, "line "+itoa(e.Line)+" repeats "+e.Name+" (line "+itoa(first)+")")
		} else {
			seen[e.Name] = e.Line
		}
		switch {
		case len(e.PRs) > 0 && pending < 0:
			pending = i
		case len(e.PRs) == 0 && pending >= 0:
			problems = append(problems, "line "+itoa(e.Line)+" ("+e.Name+") has no PR after the pending entries: those come last")
		}
		if i > 0 && (pending < 0 || i > pending) && e.Name < n.Entries[i-1].Name {
			problems = append(problems, "line "+itoa(e.Line)+" ("+e.Name+") is out of order: it belongs before "+
				n.Entries[i-1].Name+" (keep the names, and the pending ones, sorted)")
		}
	}
	return n, problems
}

func prNumber(s string) bool {
	_, ok := atoi(strings.TrimPrefix(s, "#"))
	return strings.HasPrefix(s, "#") && ok
}

// Baseline is the shrink-only counts of code that predates the rules, one per package and rule, and the allowances
// made the day the rules landed for the types open PRs were adding.
type Baseline struct{ Entries []BaselineEntry }

// A BaselineEntry is one line: the package a count is kept against and the rule, then either the count (a count
// line) or, for an allowance, the type an open PR adds and the PRs that add it. An allowance adds one to its count's
// limit while the type is in the package, so the room is that PR's and nobody else's; once the type is on main, the
// count takes it in (Lowered), and once its PRs have all closed without it, scripts/test/name_freezes.py pending
// --write drops the line.
type BaselineEntry struct {
	Line  int
	Key   string
	Rule  string
	Count int      // a count line's count
	Name  string   // an allowance's type, "" on a count line
	PRs   []string // an allowance's PRs ("#12508")
}

// ID is the count the line belongs to, "<key> <rule>".
func (e BaselineEntry) ID() string { return e.Key + " " + e.Rule }

// Allowance says whether the line is an allowance rather than a count.
func (e BaselineEntry) Allowance() bool { return e.Name != "" }

// legacyCores are the cores that predate the rules: the only keys a core-*, seam-* or out-* count may have. A core or
// seam declared later starts at zero, so a baseline line for one is no way around its rules; a legacy core that moves
// is renamed here in the change that moves it, and it joins no other.
var legacyCores = map[string]bool{
	"internal/assistant/chatsearch":    true,
	"internal/assistant/todo/timeline": true,
	"internal/conversationlog":         true,
	"internal/sessionhistory":          true,
	"internal/tokenuse":                true,
}

// legacyCoreList is legacyCores sorted, for messages.
func legacyCoreList() []string {
	var out []string
	for k := range legacyCores {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

// legacyOnly says whether a rule's count may be kept only for a legacy core: the core, seam and outside rules, whose
// findings are about domain code.
func legacyOnly(rule string) bool {
	return strings.HasPrefix(rule, "core-") || strings.HasPrefix(rule, "seam-") || strings.HasPrefix(rule, "out-")
}

// allowable says whether a rule's count can take an allowance: its findings are named types, which an allowance
// names.
func allowable(rule string) bool {
	return rule == "app-exported-record" || rule == "app-named-primitive"
}

// sortKey orders a baseline's lines: by count, the count line before its allowances, the allowances by type.
func (e BaselineEntry) sortKey() string {
	if e.Allowance() {
		return e.ID() + " 1 " + e.Name
	}
	return e.ID() + " 0"
}

// line is the entry as baseline.txt writes it.
func (e BaselineEntry) line() string {
	if e.Allowance() {
		return e.ID() + " " + e.Name + " " + strings.Join(e.PRs, " ")
	}
	return e.ID() + " " + itoa(e.Count)
}

// ReadBaseline reads baseline.txt: "<key> <rule> <count>" lines and "<key> <rule> <Type> #PR [#PR ...]" allowances,
// sorted by key and rule, each count's line before its allowances and these by type.
func ReadBaseline(text string) (Baseline, []string) {
	var b Baseline
	var problems []string
	seen := map[string]int{}
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		e := BaselineEntry{Line: i + 1}
		ok := len(f) >= 3
		if ok {
			e.Key, e.Rule = f[0], f[1]
			if n, number := atoi(f[2]); number {
				e.Count, ok = n, len(f) == 3
			} else {
				e.Name, e.PRs, ok = f[2], f[3:], identifier(f[2]) && len(f) > 3
				for _, pr := range e.PRs {
					ok = ok && prNumber(pr)
				}
			}
		}
		if !ok {
			problems = append(problems, "line "+itoa(i+1)+" is neither <key> <rule> <count> nor an allowance "+
				"<key> <rule> <Type> #PR [#PR ...]: "+line)
			continue
		}
		if _, known := rules[e.Rule]; !known {
			problems = append(problems, "line "+itoa(i+1)+" names no rule: "+e.Rule)
			continue
		}
		if !e.Allowance() && !legacyCores[e.Key] && legacyOnly(e.Rule) {
			problems = append(problems, "line "+itoa(i+1)+" counts "+e.Rule+" for "+e.Key+": core-*, seam-* and "+
				"out-* counts hold only the cores that predate the rules ("+strings.Join(legacyCoreList(), ", ")+
				"); a core or seam declared since starts at zero, and code outside one holds none of its findings")
			continue
		}
		if e.Allowance() && !allowable(e.Rule) {
			problems = append(problems, "line "+itoa(i+1)+" is an allowance for "+e.Rule+", which takes none: only "+
				"app-exported-record and app-named-primitive count named types")
			continue
		}
		k := e.sortKey()
		if first, dup := seen[k]; dup {
			what := e.ID()
			if e.Allowance() {
				what += " " + e.Name
			}
			problems = append(problems, "line "+itoa(e.Line)+" repeats "+what+" (line "+itoa(first)+")")
		}
		seen[k] = e.Line
		if len(b.Entries) > 0 && k < b.Entries[len(b.Entries)-1].sortKey() {
			problems = append(problems, "line "+itoa(e.Line)+" ("+e.ID()+") is out of order: keep the lines sorted, "+
				"each count before its allowances")
		}
		b.Entries = append(b.Entries, e)
	}
	return b, problems
}

// identifier says whether s is a Go identifier: what an allowance names.
func identifier(s string) bool {
	for i, r := range s {
		letter := r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		if !letter && (i == 0 || r < '0' || r > '9') {
			return false
		}
	}
	return s != ""
}

// Lowered is the baseline's text with the counts brought to the tree as m measures it: an allowance whose type is in
// the package now is taken into its count (its PR landed) and its line removed, every count above what it holds now
// is set to it, and a count gone to zero is removed. It never raises a limit: a count rises only by the allowances it
// takes in, and only up to its count now. The comment the file starts with stays; the lines are written sorted.
func (b Baseline) Lowered(text string, m Measure) string {
	type limit struct {
		count *BaselineEntry
		taken int
	}
	byID := map[string]*limit{}
	var ids []string
	var kept []BaselineEntry
	at := func(id string) *limit {
		if byID[id] == nil {
			byID[id] = &limit{}
			ids = append(ids, id)
		}
		return byID[id]
	}
	for i := range b.Entries {
		e := b.Entries[i]
		l := at(e.ID())
		switch {
		case !e.Allowance():
			l.count = &b.Entries[i]
		case m.Names[e.ID()][e.Name]:
			l.taken++
		default:
			kept = append(kept, e)
		}
	}
	for _, id := range ids {
		l := byID[id]
		n := l.taken
		if l.count != nil {
			n += l.count.Count
		}
		if now := m.Counts[id]; now < n {
			n = now
		}
		if n > 0 {
			key, rule, _ := strings.Cut(id, " ")
			kept = append(kept, BaselineEntry{Key: key, Rule: rule, Count: n})
		}
	}
	return composeBaseline(text, kept)
}

// composeBaseline is baseline.txt with entries for lines: the comment text starts with, then the entries sorted.
func composeBaseline(text string, entries []BaselineEntry) string {
	sortSlice(entries, func(a, b BaselineEntry) bool { return a.sortKey() < b.sortKey() })
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "#") {
			break
		}
		out = append(out, line)
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	for _, e := range entries {
		out = append(out, e.line())
	}
	return strings.Join(out, "\n") + "\n"
}
