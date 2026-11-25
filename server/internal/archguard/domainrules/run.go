package domainrules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Stats is what one run inspected: packages per layer and files parsed in full.
type Stats struct {
	Layers  map[string]int // packages per layer, undeclared included
	Parsed  int            // files parsed in full for the rules: the budget the PR that changes it records
	Counted int            // files parsed in full for the application counts only (Ratchets)
}

// Summary is the line the tests print: packages per layer and the files parsed in full.
func (s Stats) Summary() string {
	var parts []string
	for _, l := range []string{layerCore, layerShared, layerKit, layerAdapter, layerApp, layerSeam, layerLibrary,
		layerContract, layerTestkit} {
		parts = append(parts, itoa(s.Layers[l])+" "+l)
	}
	line := "domain layers: " + strings.Join(parts, ", ") + ", " + itoa(s.Layers[undeclared]) + " undeclared (frozen); " +
		itoa(s.Parsed) + " files fully parsed"
	if s.Counted > 0 {
		line += ", and " + itoa(s.Counted) + " more for the application counts"
	}
	return line
}

// Inspected says whether a run looked at anything: packages, and files it parsed in full.
func (s Stats) Inspected() bool {
	n := 0
	for _, c := range s.Layers {
		n += c
	}
	return n > 0 && s.Parsed > 0
}

// fullLayers are the layers whose packages are parsed in full: their own rules read their code.
func fullLayer(l string) bool {
	switch l {
	case layerCore, layerShared, layerKit, layerAdapter, layerApp, layerSeam:
		return true
	}
	return false
}

// Run is every rule over pkgs: it parses the declared domain and application packages in full, and of the other
// files those that import a core, shared kernel, kit or adapter, hold a .Wire selector, or may declare a Visit method
// in a package another file of which imports a core or the kit (a generic type's Visit method names no core). With
// scope set (a package's directory under the root, as the hook runs it), only that package and the files that
// import it, or may declare a Visit method in a package that does, are parsed and checked.
func Run(pkgs []*Pkg, idx Index, scope string) ([]Finding, Stats, error) {
	st := Stats{Layers: map[string]int{}}
	for _, p := range pkgs {
		l := idx.layer(p.Path)
		if l == "" {
			l = undeclared
		}
		st.Layers[l]++
		if fullLayer(l) || scope != "" && p.Rel == scope {
			if err := p.Full(); err != nil {
				return nil, st, err
			}
		}
	}
	scoped := idx.pkg(idx.module + "/" + scope)
	for _, p := range pkgs {
		visits := false // a file of p may declare a Visit method, and one imports what a visitor of it would visit
		for _, f := range p.Files {
			visits = visits || f.visit
		}
		if visits {
			visits = false
			for _, f := range p.Files {
				for _, imp := range f.Imports {
					l := idx.layer(imp.Path)
					visits = visits || scope == "" && (core(l) || l == layerKit) || scoped != nil && imp.Path == scoped.Path
				}
			}
		}
		for _, f := range p.Files {
			if f.ast != nil {
				continue
			}
			parse := f.visit && visits
			if scope == "" {
				parse = parse || f.wire
				for _, imp := range f.Imports {
					if l := idx.layer(imp.Path); core(l) || l == layerKit || l == layerAdapter {
						parse = true
					}
				}
			} else if scoped != nil {
				parse = parse || p == scoped
				for _, imp := range f.Imports {
					parse = parse || imp.Path == scoped.Path
				}
			}
			if parse {
				if err := f.parse(); err != nil {
					return nil, st, err
				}
			}
		}
	}
	var all []Finding
	all = append(all, Layers(pkgs, idx)...)
	for _, p := range pkgs {
		if p.decls != nil {
			all = append(all, Core(p, idx)...)
		}
		fs, err := Outside(p, idx)
		if err != nil {
			return nil, st, err
		}
		all = append(all, fs...)
	}
	st.Parsed = Parsed(pkgs)
	if scope != "" {
		var in []Finding
		for _, f := range all {
			if inScope(f, scope) {
				in = append(in, f)
			}
		}
		all = in
	}
	sortFindings(all)
	return all, st, nil
}

// inScope says whether a finding belongs to the package at scope: it counts against it, or is in its files.
func inScope(f Finding, scope string) bool {
	return f.Key == scope || dirOf(f.Pos.Filename) == scope
}

func dirOf(rel string) string {
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[:i]
	}
	return ""
}

// sortFindings orders findings by file, line, rule and message.
func sortFindings(fs []Finding) {
	sortSlice(fs, func(a, b Finding) bool {
		switch {
		case a.Pos.Filename != b.Pos.Filename:
			return a.Pos.Filename < b.Pos.Filename
		case a.Pos.Line != b.Pos.Line:
			return a.Pos.Line < b.Pos.Line
		case a.Rule != b.Rule:
			return a.Rule < b.Rule
		}
		return a.Msg < b.Msg
	})
}

// Strict says whether a rule takes no count: what it reports fails at once, frozen lists aside.
func Strict(rule string) bool { return strict(rule) }

// strict are the rules that take no count: what they report fails at once, frozen lists aside.
func strict(rule string) bool {
	switch rule {
	case "core-hook", "core-importers", "out-rules-import", "adapter-no-application", "adapter-import":
		return true
	}
	return strings.HasPrefix(rule, "layer-")
}

// Tally is the counts by "<key> <rule>" that a baseline holds: findings of counted rules (edge rules and strict rules
// left out) and the app-* counts.
func Tally(findings []Finding, counts []Count) map[string]int {
	out := map[string]int{}
	for _, f := range findings {
		if f.Edge != "" || strict(f.Rule) {
			continue
		}
		out[f.Key+" "+f.Rule]++
	}
	for _, c := range counts {
		out[c.Key+" "+c.Rule] += c.N
	}
	return out
}

// A Measure is one tree's counts by "<key> <rule>" (Tally), with the type names behind the counts an allowance can
// name.
type Measure struct {
	Counts map[string]int
	Names  map[string]map[string]bool
}

// Measured is what findings and counts come to in one tree.
func Measured(findings []Finding, counts []Count) Measure {
	m := Measure{Counts: Tally(findings, counts), Names: map[string]map[string]bool{}}
	for _, c := range counts {
		if !allowable(c.Rule) {
			continue
		}
		id := c.Key + " " + c.Rule
		if m.Names[id] == nil {
			m.Names[id] = map[string]bool{}
		}
		for _, n := range c.Names {
			m.Names[id][n] = true
		}
	}
	return m
}

// Lines is the measure as name_freezes.py reads it: "<key> <rule>\t<count>\t<type> <type> ..." per count, sorted.
func (m Measure) Lines() []string {
	var out []string
	for id, n := range m.Counts {
		var names []string
		for name := range m.Names[id] {
			names = append(names, name)
		}
		sortStrings(names)
		out = append(out, id+"\t"+itoa(n)+"\t"+strings.Join(names, " "))
	}
	sortStrings(out)
	return out
}

// A Verdict is what a run's findings come to against the lists: the failures, and the notices that say what can be
// tidied (a count under its baseline, an allowance whose type landed, a frozen name that left).
type Verdict struct {
	Failures []string
	Notices  []string
}

// lowerHint is how to lower the domain baseline, and how its allowances whose types landed are taken in.
const lowerHint = "python3 scripts/ci/lower_baselines.py --only domain-rules"

// dropHint is how an allowance whose PRs closed without its type leaves.
const dropHint = "python3 scripts/test/name_freezes.py pending --write --only domain_layers_baseline"

// A limit is what one count may reach: its count line, and the allowances whose types are in the tree.
type limit struct {
	count   BaselineEntry // the count line; Line is 0 when there is none
	allowed []BaselineEntry
	waiting []BaselineEntry // allowances whose types are not in the tree
}

// Judge holds findings and counts to the lists: frozen names suppress what they name, and a count may not rise above
// its count line plus its allowances whose types are in the tree. Given base (a merge base whose lists have landed),
// a count may not rise above its count there either, plus the allowances the merge base's baseline lists whose types
// it lacks; the packages files moved between are held together (Base); and a list line the merge base's lists lack
// fails unless a move carried it. Every other finding fails. pkgs names what exists, for the notices. scope limits it
// to one package, as the hook runs.
func Judge(findings []Finding, counts []Count, pkgs []*Pkg, lists Lists, base *Base, scope string) Verdict {
	var v Verdict
	in := func(key string) bool { return scope == "" || key == scope }
	var g groups
	if base != nil {
		g = newGroups(base.Moves)
	}
	if scope == "" {
		v.Failures = append(v.Failures, lists.Problems...)
		if base != nil {
			v.Failures = append(v.Failures, base.added(lists, g)...)
		}
	}
	// Frozen names: the undeclared packages and the legacy edges.
	usedEdges := map[string]bool{}
	groupsByID := map[string][]Finding{}
	var order []string
	for _, f := range findings {
		switch {
		case f.Rule == "layer-declared":
			// A package with no //persona:layer line at all may be frozen by name; a malformed one may not.
			if _, ok := lists.Undeclared.Has(f.Edge); ok && f.Edge != "" {
				continue
			}
			line := f.String()
			if f.Edge != "" {
				line += ". If the package only moved here and undeclared.txt lists its old path, " + movesHint +
					" carries its line."
			}
			v.Failures = append(v.Failures, line)
		case f.Edge != "":
			if _, ok := lists.Edges.Has(f.Edge); ok {
				usedEdges[f.Edge] = true
				continue
			}
			v.Failures = append(v.Failures, f.String()+". If the import only moved here with its package and "+
				"legacy_edges.txt lists it under the old path, "+movesHint+" carries its line.")
		case strict(f.Rule):
			v.Failures = append(v.Failures, f.String())
		default:
			id := f.Key + " " + f.Rule
			if _, ok := groupsByID[id]; !ok {
				order = append(order, id)
			}
			groupsByID[id] = append(groupsByID[id], f)
		}
	}
	now := Measured(findings, counts)
	byID := map[string]Count{}
	for _, c := range counts {
		id := c.Key + " " + c.Rule
		byID[id] = c
		if _, ok := groupsByID[id]; !ok {
			order = append(order, id)
			groupsByID[id] = nil
		}
	}
	limits := map[string]*limit{}
	var ids []string
	for _, e := range lists.Baseline.Entries {
		if strict(e.Rule) {
			v.Failures = append(v.Failures, baselineFile+" line "+itoa(e.Line)+": "+e.Rule+" takes no count: fix what it reports")
		}
		l := limits[e.ID()]
		if l == nil {
			l = &limit{}
			limits[e.ID()] = l
			ids = append(ids, e.ID())
		}
		switch {
		case !e.Allowance():
			l.count = e
		case now.Names[e.ID()][e.Name]:
			l.allowed = append(l.allowed, e)
		default:
			l.waiting = append(l.waiting, e)
		}
	}
	sortStrings(order)
	for _, id := range order {
		key, rule, _ := strings.Cut(id, " ")
		if !in(key) {
			continue
		}
		n, l := now.Counts[id], limits[id]
		if l == nil {
			l = &limit{}
		}
		top, why := l.count.Count+len(l.allowed), "baseline "+itoa(l.count.Count)+allowedText(l.allowed)
		if base != nil && !g.moved(key) {
			// What the merge base has already holds the allowed types it has; the allowances it lists add the others.
			b := base.Measure.Counts[id]
			if held := b + base.fresh(l.allowed, g, []string{key}); held < top {
				top, why = held, "merge base "+itoa(b)+", baseline "+itoa(l.count.Count)+allowedText(l.allowed)
			}
		}
		if n <= top {
			continue
		}
		if c, ok := byID[id]; ok {
			what := c.Key + " adds " + countWhat(rule) + " (count " + itoa(n) + " > " + itoa(top) + ")"
			v.Failures = append(v.Failures, c.Pos.Filename+":1: "+message(rule, what)+". Counted: "+
				strings.Join(c.Names, ", ")+". If its code only moved here, "+movesHint+" carries its lines.")
			continue
		}
		for _, f := range groupsByID[id] {
			v.Failures = append(v.Failures, f.String())
		}
		v.Failures = append(v.Failures, key+" "+rule+": "+itoa(n)+" now, above its limit ("+why+"): the count only "+
			"falls; fix the code the lines above name (if it only moved here, "+movesHint+" carries its lines)")
	}
	if base != nil && scope == "" {
		v.Failures = append(v.Failures, base.movedRises(now, lists, g, byID, groupsByID)...)
	}
	// What can be tidied: a count under its line, an allowance whose type landed, one still waiting for its PRs.
	for _, id := range ids {
		l := limits[id]
		key, rule, _ := strings.Cut(id, " ")
		if !in(key) || strict(rule) {
			continue
		}
		for _, e := range l.allowed {
			v.Notices = append(v.Notices, baselineFile+" line "+itoa(e.Line)+": "+e.Name+" ("+strings.Join(e.PRs, " ")+") is in "+
				key+" now: take it into the count with "+lowerHint)
		}
		if n, top := now.Counts[id], l.count.Count+len(l.allowed); l.count.Line > 0 && n < top {
			v.Notices = append(v.Notices, baselineFile+" line "+itoa(l.count.Line)+": "+id+" is "+itoa(n)+", under its baseline "+
				itoa(l.count.Count)+allowedText(l.allowed)+": lower it with "+lowerHint)
		}
		if len(l.waiting) > 0 {
			var waiting []string
			for _, e := range l.waiting {
				waiting = append(waiting, e.Name+" ("+strings.Join(e.PRs, " ")+")")
			}
			v.Notices = append(v.Notices, baselineFile+" line "+itoa(l.waiting[0].Line)+": "+id+" allows "+
				strings.Join(waiting, ", ")+", not here yet; once a type's PRs have all closed, "+dropHint+" drops its line")
		}
	}
	if scope == "" {
		declared := map[string]string{}
		for _, p := range pkgs {
			declared[display(p)] = p.Layer
		}
		for _, e := range lists.Undeclared.Entries {
			layer, exists := declared[e.Name]
			switch {
			case exists && layer != "":
				v.Notices = append(v.Notices, e.Name+" declares "+layer+" now: delete line "+itoa(e.Line)+" of "+undeclaredFile)
			case !exists && len(e.PRs) == 0:
				v.Notices = append(v.Notices, e.Name+" is gone: delete line "+itoa(e.Line)+" of "+undeclaredFile)
			case !exists:
				v.Notices = append(v.Notices, e.Name+" is pending ("+strings.Join(e.PRs, " ")+"); delete line "+itoa(e.Line)+
					" of "+undeclaredFile+" once its PRs have all closed")
			}
		}
	}
	for _, e := range lists.Edges.Entries {
		pkg, _, _ := strings.Cut(e.Name, " ")
		if !in(pkg) || usedEdges[e.Name] {
			continue
		}
		v.Notices = append(v.Notices, e.Name+" no longer happens: delete line "+itoa(e.Line)+" of "+edgesFile)
	}
	return v
}

// allowedText is the allowances a limit holds, for a message: "" for none, " and 2 allowed (A, B)".
func allowedText(allowed []BaselineEntry) string {
	if len(allowed) == 0 {
		return ""
	}
	var names []string
	for _, e := range allowed {
		names = append(names, e.Name)
	}
	return " and " + itoa(len(allowed)) + " allowed (" + strings.Join(names, ", ") + ")"
}

// countWhat is what an app-* count holds, for its message.
func countWhat(rule string) string {
	switch rule {
	case "app-exported-record":
		return "an exported-field record"
	case "app-named-primitive":
		return "a named primitive type"
	case "app-bridge-call":
		return "a use of an adapter's Wire, which unwraps a domain value in application code"
	}
	return "a direct write of DoneAt or DoneBy"
}

// Check is the hook in every declared package's rules_test.go: the same rules as archguard's TestDomainLayers, with
// the same lists, for the package the test runs in and the files that import it.
//
//	func TestDomainRules(t *testing.T) { domainrules.Check(t) }
func Check(t *testing.T) {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, module, err := moduleRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	scope := filepath.ToSlash(rel)
	lists, err := ReadLists(filepath.Join(root, "internal", "archguard", "testdata"))
	if err != nil {
		t.Fatal(err)
	}
	pkgs, err := Load(root, module)
	if err != nil {
		t.Fatal(err)
	}
	idx := NewIndex(module, pkgs, lists.Contracts)
	if p := idx.pkg(module + "/" + scope); p == nil || p.Layer == "" {
		t.Fatalf("%s declares no layer: the hook runs in a package whose package comment has a //persona:layer line", scope)
	}
	findings, _, err := Run(pkgs, idx, scope)
	if err != nil {
		t.Fatal(err)
	}
	v := Judge(findings, nil, pkgs, lists, nil, scope)
	for _, n := range v.Notices {
		t.Log("notice: " + n)
	}
	for _, f := range v.Failures {
		t.Error(f)
	}
}

// moduleRoot is the directory of the go.mod above dir and its module path.
func moduleRoot(dir string) (string, string, error) {
	for d := dir; ; d = filepath.Dir(d) {
		b, err := os.ReadFile(filepath.Join(d, "go.mod"))
		if err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				if m, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
					return d, strings.Trim(strings.TrimSpace(m), `"`), nil
				}
			}
			return "", "", os.ErrNotExist
		}
		if filepath.Dir(d) == d {
			return "", "", err
		}
	}
}
