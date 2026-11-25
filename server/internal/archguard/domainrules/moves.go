package domainrules

import "strings"

// movesHint is how the lists follow code that moved between packages: it carries their lines along git's renames
// (Carried), and adds no room.
const movesHint = "python3 scripts/test/name_freezes.py moves"

// A Move is one .go file git found renamed since the merge base: the package directory it was in there, and the one
// it is in now, both under the root.
type Move struct{ From, To string }

// A Base is what a change is held to besides its own lists, given a merge base whose lists have landed: the counts
// there, its lists, and the files the change moved. A count may not rise above its count at the merge base, and the
// lists take no line the merge base's lack unless a move carried it, so no change grants itself room.
type Base struct {
	Measure Measure // the merge base's tree, measured with its files put back
	Lists   Lists   // the merge base's undeclared.txt, legacy_edges.txt and baseline.txt
	Moves   []Move
}

// groups is the package directories the moves connect: moved code takes its counts and its frozen names along, so the
// directories files moved between are held together. Each directory a move touches maps to its group's name, the
// least directory in it; one no move touches is in no group.
type groups map[string]string

// newGroups is the groups of moves, a file renamed within its directory left out.
func newGroups(moves []Move) groups {
	parent := map[string]string{}
	var find func(d string) string
	find = func(d string) string {
		if p, ok := parent[d]; ok && p != d {
			root := find(p)
			parent[d] = root
			return root
		}
		parent[d] = d
		return d
	}
	for _, m := range moves {
		if m.From == m.To {
			continue
		}
		a, b := find(m.From), find(m.To)
		if b < a {
			a, b = b, a
		}
		parent[b] = a
	}
	g := groups{}
	for d := range parent {
		g[d] = find(d)
	}
	return g
}

// of is the group dir is in: its name, or dir itself when no move touches it.
func (g groups) of(dir string) string {
	if r, ok := g[dir]; ok {
		return r
	}
	return dir
}

// moved says whether a move touches dir.
func (g groups) moved(dir string) bool {
	_, ok := g[dir]
	return ok
}

// edgeParts is a frozen import's package and path, "(test)" kept with the package: "internal/hub (test) imports
// internal/sessionhistory" -> "internal/hub (test)", "internal/sessionhistory".
func edgeParts(name string) (string, string) {
	pkg, path, _ := strings.Cut(name, " imports ")
	return pkg, path
}

// edgeDir is the package directory an edge's package part names, "(test)" left out.
func edgeDir(pkg string) string { return strings.TrimSuffix(pkg, " (test)") }

// edgeGroup is the edge a frozen import is, wherever its package and path moved: the groups of both.
func (g groups) edgeGroup(name string) string {
	pkg, path := edgeParts(name)
	return g.of(edgeDir(pkg)) + strings.TrimPrefix(pkg, edgeDir(pkg)) + " imports " + g.of(path)
}

// edgeMoved says whether a move touches the package or the path of a frozen import.
func (g groups) edgeMoved(name string) bool {
	pkg, path := edgeParts(name)
	return g.moved(edgeDir(pkg)) || g.moved(path)
}

// added is what the lists have that the merge base's lack and no move carried: undeclared and frozen-import names,
// allowances, and room in a count: a count line and the allowances beside it may give no more room than the merge
// base's did, the packages files moved between taken together. A line comes only from the change that adds a list,
// the day it lands; after that it is the merge base's, or it followed code that moved, and a count's room only falls
// (lower_baselines.py taking an allowance into its count keeps it the same).
func (b *Base) added(l Lists, g groups) []string {
	var out []string
	for _, e := range l.Undeclared.Entries {
		if !b.carriesName(b.Lists.Undeclared, e.Name, g.of) {
			out = append(out, undeclaredFile+" line "+itoa(e.Line)+": "+e.Name+" is not on the merge base's list, and no "+
				"package on it moved there: a new package declares its layer from its first line (docs/server/"+
				"architecture.md \"Domain model\"); one that moved carries its line with "+movesHint)
		}
	}
	for _, e := range l.Edges.Entries {
		if !b.carriesName(b.Lists.Edges, e.Name, g.edgeGroup) {
			out = append(out, edgesFile+" line "+itoa(e.Line)+": "+e.Name+" is not on the merge base's list, and no "+
				"import on it moved there: change the import (the line above names its rule); one that moved carries "+
				"its line with "+movesHint)
		}
	}
	for _, e := range l.Baseline.Entries {
		if e.Allowance() && !b.allowsType(e, g) {
			out = append(out, baselineFile+" line "+itoa(e.Line)+": the allowance for "+e.Name+" ("+strings.Join(e.PRs, " ")+
				") is not on the merge base's baseline: allowances were the open PRs' the day the baseline landed, "+
				"and none is added since")
		}
	}
	return append(out, b.roomRises(l, g)...)
}

// roomRises is the counts whose lines give more room than the merge base's: a count line's count plus one for each
// allowance beside it that the merge base lists (one it does not list fails on its own), summed over the packages
// files moved between. Without it, a raised or new count line would pass wherever a base holds the count and give
// room wherever none does.
func (b *Base) roomRises(l Lists, g groups) []string {
	room := func(bl Baseline, listed func(BaselineEntry) bool) (map[string]int, map[string][]BaselineEntry, []string) {
		total, lines := map[string]int{}, map[string][]BaselineEntry{}
		var order []string
		for _, e := range bl.Entries {
			n := e.Count
			if e.Allowance() {
				if !listed(e) {
					continue
				}
				n = 1
			}
			gid := g.of(e.Key) + " " + e.Rule
			if _, ok := lines[gid]; !ok {
				order = append(order, gid)
			}
			total[gid] += n
			lines[gid] = append(lines[gid], e)
		}
		return total, lines, order
	}
	now, lines, order := room(l.Baseline, func(e BaselineEntry) bool { return b.allowsType(e, g) })
	then, _, _ := room(b.Lists.Baseline, func(BaselineEntry) bool { return true })
	var out []string
	for _, gid := range order {
		if now[gid] <= then[gid] {
			continue
		}
		var at []string
		for _, e := range lines[gid] {
			at = append(at, itoa(e.Line))
		}
		_, rule, _ := strings.Cut(gid, " ")
		var keys []string
		for _, e := range lines[gid] {
			if !contains(keys, e.Key) {
				keys = append(keys, e.Key)
			}
		}
		out = append(out, baselineFile+" line "+strings.Join(at, ", ")+": "+strings.Join(keys, ", ")+" "+rule+
			" gives room for "+itoa(now[gid])+", more than the "+itoa(then[gid])+" the merge base's baseline gives: "+
			"a count's line only falls, so put it back and fix the code its count names; code that only moved here "+
			"carries its lines with "+movesHint)
	}
	return out
}

// carriesName says whether the merge base's list has name, or a name in the same group (as key puts it).
func (b *Base) carriesName(list Names, name string, key func(string) string) bool {
	for _, o := range list.Entries {
		if o.Name == name || key(o.Name) == key(name) {
			return true
		}
	}
	return false
}

// allowsType says whether the merge base's baseline has allowance e, in its package or one it moved from.
func (b *Base) allowsType(e BaselineEntry, g groups) bool {
	for _, o := range b.Lists.Baseline.Entries {
		if o.Allowance() && o.Rule == e.Rule && o.Name == e.Name && g.of(o.Key) == g.of(e.Key) {
			return true
		}
	}
	return false
}

// fresh is how many of allowed (allowances whose types are in the tree, at key and rule) the merge base's baseline
// lists while the merge base's tree lacks their types: the room the PR they name takes when it lands. An allowance the
// change wrote itself gives none.
func (b *Base) fresh(allowed []BaselineEntry, g groups, keys []string) int {
	n := 0
	for _, e := range allowed {
		if !b.allowsType(e, g) {
			continue
		}
		had := false
		for _, k := range keys {
			had = had || b.Measure.Names[k+" "+e.Rule][e.Name]
		}
		if !had {
			n++
		}
	}
	return n
}

// movedRises is the failures of the groups whose counts rose: with a base, the packages files moved between are held
// together to their total at the merge base, plus the room their allowances give, since moved code carries its count.
// byID and found are the counts and the findings by "<key> <rule>", for the lines that name what was counted.
func (b *Base) movedRises(now Measure, lists Lists, g groups, byID map[string]Count, found map[string][]Finding) []string {
	type total struct {
		now, base int
		keys      []string
		allowed   []BaselineEntry
	}
	totals := map[string]*total{}
	var order []string
	at := func(id string) (*total, string) {
		key, rule, _ := strings.Cut(id, " ")
		if !g.moved(key) {
			return nil, ""
		}
		gid := g.of(key) + " " + rule
		t := totals[gid]
		if t == nil {
			t = &total{}
			totals[gid] = t
			order = append(order, gid)
		}
		if !contains(t.keys, key) {
			t.keys = append(t.keys, key)
		}
		return t, rule
	}
	for id, n := range now.Counts {
		if t, rule := at(id); t != nil && !strict(rule) {
			t.now += n
		}
	}
	for id, n := range b.Measure.Counts {
		if t, rule := at(id); t != nil && !strict(rule) {
			t.base += n
		}
	}
	for _, e := range lists.Baseline.Entries {
		if t, _ := at(e.ID()); t != nil && e.Allowance() && now.Names[e.ID()][e.Name] {
			t.allowed = append(t.allowed, e)
		}
	}
	sortStrings(order)
	var out []string
	for _, gid := range order {
		t := totals[gid]
		_, rule, _ := strings.Cut(gid, " ")
		top := t.base + b.fresh(t.allowed, g, t.keys)
		if t.now <= top {
			continue
		}
		sortStrings(t.keys)
		var counted []string
		var pos string
		for _, k := range t.keys {
			id := k + " " + rule
			if c, ok := byID[id]; ok {
				if pos == "" {
					pos = c.Pos.Filename
				}
				counted = append(counted, c.Names...)
			}
			for _, f := range found[id] {
				out = append(out, f.String())
			}
		}
		keys := strings.Join(t.keys, ", ")
		if pos != "" {
			out = append(out, pos+":1: "+message(rule, keys+" add "+countWhat(rule)+" (count "+itoa(t.now)+" > "+
				itoa(top)+" together)")+". Counted: "+strings.Join(counted, ", ")+".")
		}
		out = append(out, keys+" "+rule+": "+itoa(t.now)+" now together, above their "+itoa(top)+" at the merge base: "+
			"code that moves carries its count, and the packages it moved between are held together, so the count "+
			"only falls; fix the code the lines above name")
	}
	return out
}

// ListTexts is the text of the three lists under archguard/testdata/domain_layers.
type ListTexts struct{ Undeclared, Edges, Baseline string }

// Carried is the lists' texts with what moved carried along (python3 scripts/test/name_freezes.py moves, from git's
// renames since the merge base). In each group of directories the moves connect: undeclared.txt lists the group's
// packages that declare no layer, if it listed one of the group's before; legacy_edges.txt holds the group's frozen
// imports under their names now, if it held them before; and baseline.txt's count lines become the group's counts now,
// if its lines held them all before, the allowances whose types are in the tree taken in, and a waiting allowance of
// a package that is gone moves to the package most of its files went to. It adds no room: a group whose counts rose
// keeps its lines, and the check fails on it.
func Carried(t ListTexts, l Lists, findings []Finding, counts []Count, pkgs []*Pkg, moves []Move) ListTexts {
	g := newGroups(moves)
	exists, bare := map[string]bool{}, map[string]bool{}
	for _, p := range pkgs {
		exists[p.Rel] = true
		if p.Layer == "" && len(p.declared) == 0 {
			bare[p.Rel] = true
		}
	}

	// undeclared.txt: the packages of a group it froze that declare no layer now.
	frozen := map[string]bool{}
	listed := map[string]bool{}
	for _, e := range l.Undeclared.Entries {
		listed[e.Name] = true
		if len(e.PRs) == 0 && g.moved(e.Name) {
			frozen[g.of(e.Name)] = true
		}
	}
	drop, add := map[string]bool{}, map[string]bool{}
	for _, e := range l.Undeclared.Entries {
		if len(e.PRs) == 0 && frozen[g.of(e.Name)] && !bare[e.Name] {
			drop[e.Name] = true
		}
	}
	for d := range bare {
		if g.moved(d) && frozen[g.of(d)] && !listed[d] {
			add[d] = true
		}
	}
	out := t
	out.Undeclared = renamed(t.Undeclared, l.Undeclared, drop, add)

	// legacy_edges.txt: the imports that happen now in place of a frozen one that moved.
	happening := map[string]bool{}
	for _, f := range findings {
		if f.Edge != "" && f.Rule != "layer-declared" {
			happening[f.Edge] = true
		}
	}
	carried := map[string]bool{}
	listed = map[string]bool{}
	for _, e := range l.Edges.Entries {
		listed[e.Name] = true
		if g.edgeMoved(e.Name) {
			carried[g.edgeGroup(e.Name)] = true
		}
	}
	drop, add = map[string]bool{}, map[string]bool{}
	for _, e := range l.Edges.Entries {
		if g.edgeMoved(e.Name) && !happening[e.Name] {
			drop[e.Name] = true
		}
	}
	for name := range happening {
		if !listed[name] && carried[g.edgeGroup(name)] {
			add[name] = true
		}
	}
	out.Edges = renamed(t.Edges, l.Edges, drop, add)

	out.Baseline = carriedBaseline(t.Baseline, l.Baseline, Measured(findings, counts), g, exists, moves)
	return out
}

// carriedBaseline is Carried on baseline.txt: see there.
func carriedBaseline(text string, b Baseline, now Measure, g groups, exists map[string]bool, moves []Move) string {
	type total struct {
		lines, now int
		allowed    []BaselineEntry
	}
	totals := map[string]*total{}
	for _, e := range b.Entries {
		if !g.moved(e.Key) {
			continue
		}
		gid := g.of(e.Key) + " " + e.Rule
		if totals[gid] == nil {
			totals[gid] = &total{}
		}
		switch {
		case !e.Allowance():
			totals[gid].lines += e.Count
		case inGroup(now, g, e):
			totals[gid].allowed = append(totals[gid].allowed, e)
		}
	}
	for id, n := range now.Counts {
		key, rule, _ := strings.Cut(id, " ")
		if t := totals[g.of(key)+" "+rule]; t != nil && g.moved(key) {
			t.now += n
		}
	}
	// where[d] is the directory most of d's moved files went to.
	went := map[string]map[string]int{}
	for _, m := range moves {
		if m.From != m.To {
			if went[m.From] == nil {
				went[m.From] = map[string]int{}
			}
			went[m.From][m.To]++
		}
	}
	where := func(d string) string {
		best, most := d, 0
		for to, n := range went[d] {
			if n > most || n == most && to < best {
				best, most = to, n
			}
		}
		return best
	}
	carries := func(e BaselineEntry) bool {
		t := totals[g.of(e.Key)+" "+e.Rule]
		return t != nil && g.moved(e.Key) && t.now <= t.lines+len(t.allowed)
	}
	var kept []BaselineEntry
	for _, e := range b.Entries {
		switch {
		case !carries(e):
			kept = append(kept, e)
		case e.Allowance() && !inGroup(now, g, e):
			if !exists[e.Key] {
				e.Key = where(e.Key)
			}
			kept = append(kept, e)
		}
	}
	for id, n := range now.Counts {
		key, rule, _ := strings.Cut(id, " ")
		if n > 0 && g.moved(key) && carries(BaselineEntry{Key: key, Rule: rule}) {
			kept = append(kept, BaselineEntry{Key: key, Rule: rule, Count: n})
		}
	}
	return composeBaseline(text, kept)
}

// inGroup says whether an allowance's type is in any package of its group now.
func inGroup(now Measure, g groups, e BaselineEntry) bool {
	for id, names := range now.Names {
		key, rule, _ := strings.Cut(id, " ")
		if rule == e.Rule && g.of(key) == g.of(e.Key) && names[e.Name] {
			return true
		}
	}
	return false
}

// renamed is a freeze list's text with the names in drop taken out and those in add put among the names it had when
// it was frozen, sorted; its comments and pending entries stay where they are.
func renamed(text string, n Names, drop, add map[string]bool) string {
	if len(drop) == 0 && len(add) == 0 {
		return text
	}
	var names []string
	frozen := map[int]bool{} // the lines of the names it had when it was frozen
	for _, e := range n.Entries {
		if len(e.PRs) > 0 {
			continue
		}
		frozen[e.Line] = true
		if !drop[e.Name] {
			names = append(names, e.Name)
		}
	}
	for name := range add {
		names = append(names, name)
	}
	sortStrings(names)
	lines := strings.Split(text, "\n")
	// The names go where the first frozen one was; with none, before the first entry or at the end.
	at := -1
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if frozen[i+1] || at < 0 && t != "" && !strings.HasPrefix(t, "#") {
			at = i
			break
		}
	}
	if at < 0 {
		at = len(lines)
		if at > 0 && lines[at-1] == "" {
			at-- // the newline the text ends with
		}
	}
	var out []string
	for i, line := range lines {
		if i == at {
			out = append(out, names...)
		}
		if !frozen[i+1] {
			out = append(out, line)
		}
	}
	if at == len(lines) {
		out = append(out, names...)
	}
	return strings.Join(out, "\n")
}

// Moves is the package moves among git's renames, each {from, to} as paths under the root: the .go files Load reads,
// as the directory each left and the one it is in now.
func Moves(renames [][2]string) []Move {
	var out []Move
	for _, r := range renames {
		from, to := r[0], r[1]
		if strings.HasSuffix(from, ".go") && strings.HasSuffix(to, ".go") && !skipped(from) && !skipped(to) {
			out = append(out, Move{From: dirOf(from), To: dirOf(to)})
		}
	}
	return out
}
