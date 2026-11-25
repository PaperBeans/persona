#!/usr/bin/env python3
"""Name freezes, and the names open PRs add to them (docs/server/architecture.md "Registering a domain's entries").

A name freeze keeps a list that nearly every change used to edit from growing. The names the list may have are kept in
a file: the ones it had when the freeze landed, then the ones the PRs open that day were adding, each with its PRs. The
list's check fails on any other name and reports a listed one that has gone as a notice. This module holds what the
freezes share: the file's format and its rewrite, and the pending names, read from the open PRs' heads.
scripts/mock/mock_hub_switches.py uses it for mock_hub.py's MOCK_* switches. Run as a script, it does the server's
central lists, whose check is server/internal/archguard/central_lists_test.go:

    python3 scripts/test/name_freezes.py pending            # the names open PRs add to each list (gh, git, go)
    python3 scripts/test/name_freezes.py pending --write    # the lists' files rewritten, the day a freeze lands

`pending` reads each open PR's head, fetched from origin by its commit (objects only: no ref or FETCH_HEAD is written),
and reports the names it adds that its merge base with --base did not have, leaving out the ones --base has. A draft
whose head commit is more than --idle-days old is left out. The archguard test itself reads the names
(-central-list-names), so they are written in the form the freeze compares.

One freeze reads its names from the whole server tree instead of one file: the package directories under server/
that declare no layer (server/internal/archguard/testdata/domain_layers/undeclared.txt, which
server/internal/archguard/domain_test.go's TestDomainLayers checks; docs/server/architecture.md "Domain model"). For
it, a PR's names are the undeclared packages among the directories whose .go files it changes, read at its head and at
its merge base by archguard's TestDomainLayerNames. `--only <id>` limits a run to some lists; the day a list lands,
`--base HEAD` (the branch that adds it, committed) gives the names the branch itself has.

One more list holds counts, not names: the domain rules' baseline, baseline.txt beside undeclared.txt
(docs/server/architecture.md "The rules and their checks"). Its allowances name the types open PRs add to the counts
of exported-field records and named primitive types: a PR's are as many of the types its head adds over its
merge base (in the directories it changes, beside the packages this tree declares) as the count rises by, less the ones
the tree has. An allowance adds one to its count's limit only while its type is in the package, so the room is that
PR's alone. `pending` also lists what no allowance covers: a rise in another count, an import or finding that fails at
once, a package this tree declares that the PR changes. The day the baseline lands (--base lacks it), --write sets a
count line to every count the tree has and writes the allowances: the listed ones whose type the tree lacks and whose
PRs are not all closed, and the new ones. Once it has landed, --write only tidies, on a checkout of --base: the counts
take in the allowances whose types landed (TestDomainLayers' DOMAIN_LAYERS_UPDATE, which
scripts/ci/lower_baselines.py runs too), and the allowances whose PRs have all closed leave. It adds no allowance.

Code that moves between packages takes its lines along: `python3 scripts/test/name_freezes.py moves [--base origin/main]`
runs TestDomainLayers with DOMAIN_LAYERS_MOVES, which reads git's renames of .go files under server/ since the merge
base with --base and rewrites the three domain lists along them (domainrules.Carried): undeclared.txt names the
packages a frozen one's files moved to, legacy_edges.txt the frozen imports under their new names, and baseline.txt's
counts of the packages files moved between become their counts now, as long as their total has not risen. It adds no
room: a rise keeps its lines and the test fails on it. With CI_BASE_SHA, which check-fast's diff mode sets,
TestDomainLayers then checks that each line it lacks at the merge base is one a move carried.

Both domain lists read the open PRs' trees, BATCH PRs at a time, so the temporary directory holds a few PRs'
directories, not every one. Once a domain list is on --base, a run leaves it out unless `--only` names it: it takes
nothing new then, and a run for a central list (a seam's new row, on its own branch) needs neither.

`--write` rewrites each list's file. For a list --base does not have yet, the day its freeze lands, that is its
leading comment, every name --base has, then the pending ones: a listed pending name --base now has joins the first
part (its PR merged), one whose PRs have all closed comes off, one whose PR is still open stays, and the names found
now are added. Once a list is on --base, --write only prunes it: it keeps the listed names --base still has (a
pending one whose PR merged among them) and the pending ones whose PRs are still open, under their header, and drops
the rest. It adds no name. A name --base has that the list lacks (a red main, or a branch given as --base) is
reported as an error, exit status 1, and stays off the list: it is not how a later PR gets a name on a list.
"""
import argparse
from datetime import date, datetime, timedelta, timezone
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]

# An entry: the name, then a pending one's PRs (server/internal/archguard/central_lists_test.go reads the same format).
ENTRY = re.compile(r"(?P<name>\S(?:.*?\S)??)(?:[ \t]+(?P<prs>#\d+(?:[ \t]+#\d+)*))?")

MOVED_SHOWN = 20  # names changes() shows for a part; the rest it counts

PENDING_HEADER = """\
# Pending: the names the PRs open when this freeze landed were adding, each with its PRs
# (python3 scripts/test/name_freezes.py pending --write, {day}; drafts idle more than {idle} days left out).
# A name stays when its PR merges; a rerun of --write drops one whose PRs have all closed. No PR opened later gets a
# line here."""


class Entry:
    """A line of a list's file: the name, its line and, for a pending one, the open PRs that added it (["#4512"])."""

    def __init__(self, line, name, prs):
        self.line, self.name, self.prs = line, name, prs


class Format:
    """A freeze list's file. Comment lines start with #; an entry is one name, and a pending one is followed by its
    PRs (NAME #4512). The names the list had when its freeze landed come first, then the pending ones, and names stay
    sorted and unique within each part.

    what and example name an entry in messages ("a MOCK_* name", "MOCK_X #123"), valid says whether a name is one,
    header is the pending part's comment ({day} and {idle} filled in), and label names the first part in changes()."""

    def __init__(self, what="a name", example="NAME #123", valid=None, header=PENDING_HEADER, label="names"):
        self.what, self.example, self.valid, self.header, self.label = what, example, valid, header, label

    def read(self, text):
        """(entries, problems) of a list's text."""
        entries, problems = [], []
        for number, raw in enumerate(text.splitlines(), 1):
            line = raw.strip()
            if not line or line.startswith("#"):
                continue
            match = ENTRY.fullmatch(line)
            if match is None or self.valid is not None and not self.valid(match["name"]):
                problems.append("line %d is neither %s nor a pending one with its PRs (%s): %r"
                                % (number, self.what, self.example, raw))
                continue
            entries.append(Entry(number, match["name"], (match["prs"] or "").split()))
        seen = {}
        for entry in entries:
            if entry.name in seen:
                problems.append("line %d repeats %s (line %d)" % (entry.line, entry.name, seen[entry.name]))
            seen.setdefault(entry.name, entry.line)
        first_pending = next((i for i, e in enumerate(entries) if e.prs), len(entries))
        problems += ["line %d (%s) has no PR after the pending entries: those come last" % (e.line, e.name)
                     for e in entries[first_pending:] if not e.prs]
        for part in (entries[:first_pending], entries[first_pending:]):
            problems += ["line %d (%s) is out of order: it belongs before %s (keep the names, and the pending ones, "
                         "sorted)" % (e.line, e.name, before.name) for before, e in zip(part, part[1:])
                         if e.name < before.name]
        return entries, problems

    def rewrite(self, text, today, found, still_open, day, idle_days):
        """The list's text rewritten the day its freeze lands: its leading comment, every name the base has
        (`today`), then the pending entries under a fresh header. Those are the listed pending names the base does not
        have whose PRs are not all closed, with the PRs still open (`still_open`, a set of numbers), and `found`'s
        ({name: [PR numbers]}). A listed pending name the base has is one of its names now, since its PR merged, so no
        name the base has is dropped. Only for a list the base does not have yet: every name the base has goes in."""
        lines = text.splitlines()
        header = self.header.format(day=day, idle=idle_days).splitlines()
        return self._compose(self._lead(lines), today, self._waiting(text, today, still_open, found), header)

    def prune(self, text, today, still_open):
        """(text, unlisted) for a list the base has already: its text pruned, and the names the base has that it
        lacks. The first part keeps the listed names, of either part, that the base still has, so a pending one whose
        PR merged joins it and one that has left the base comes off. The pending part keeps its header and the listed
        pending names the base does not have yet whose PRs are still open (`still_open`). No name is added: a name
        the base has that the list lacks is one the freeze fails on, and its entry belongs in its domain's seam."""
        lines, entries = text.splitlines(), self.read(text)[0]
        listed = {e.name for e in entries}
        names = [e.line for e in entries if not e.prs]
        pending = [e.line for e in entries if e.prs]
        header = []
        if names and pending:  # what sits between the last name and the first pending entry
            header = lines[names[-1]:pending[0] - 1]
            while header and not header[0].strip():
                header.pop(0)
            while header and not header[-1].strip():
                header.pop()
        return (self._compose(self._lead(lines), today & listed, self._waiting(text, today, still_open), header),
                sorted(today - listed))

    @staticmethod
    def _lead(lines):
        """The comment a list's file starts with, less the blank lines that end it."""
        lead = []
        for line in lines:
            if line.strip() and not line.lstrip().startswith("#"):
                break
            lead.append(line)
        while lead and not lead[-1].strip():
            lead.pop()
        return lead

    def _waiting(self, text, today, still_open, found=None):
        """{name: open PR numbers}: the listed pending names the base does not have whose PRs are not all closed,
        and found's names the base does not have."""
        waiting = {}
        for entry in self.read(text)[0]:
            open_ones = {int(pr[1:]) for pr in entry.prs} & still_open
            if entry.prs and entry.name not in today and open_ones:
                waiting[entry.name] = open_ones
        for name, numbers in (found or {}).items():
            if name not in today:
                waiting.setdefault(name, set()).update(numbers)
        return waiting

    @staticmethod
    def _compose(lead, names, waiting, header):
        """A list's text: the leading comment, the names, then the pending entries, when there are any, after a
        blank line and header (when there is one)."""
        lines = lead + sorted(names)
        if waiting:
            lines += ([""] + header) if header else []
            lines += ["%s %s" % (name, " ".join("#%d" % n for n in sorted(waiting[name]))) for name in sorted(waiting)]
        return "\n".join(lines) + "\n"

    def changes(self, old, new):
        """What a rewrite changed, for the person landing the freeze: one line for each part of the list."""
        def parts(text):
            entries = self.read(text)[0]
            return {e.name for e in entries if not e.prs}, {e.name for e in entries if e.prs}

        (old_names, old_waiting), (new_names, new_waiting) = parts(old), parts(new)

        def line(label, before, after):
            moved = ["+" + name for name in sorted(after - before)] + ["-" + name for name in sorted(before - after)]
            if len(moved) > MOVED_SHOWN:  # a list's first write adds every name
                moved = moved[:MOVED_SHOWN] + ["(%d more)" % (len(moved) - MOVED_SHOWN)]
            return "%s: %d" % (label, len(after)) + "".join(" " + name for name in moved)

        return [line(self.label, old_names, new_names), line("pending", old_waiting, new_waiting)]


# ---- the open PRs' heads ----

QUERY = """
query($owner: String!, $repo: String!, $endCursor: String) {
  repository(owner: $owner, name: $repo) {
    pullRequests(states: OPEN, first: 40, after: $endCursor) {
      pageInfo { hasNextPage endCursor }
      nodes {
        number isDraft headRefOid
        commits(last: 1) { nodes { commit { committedDate } } }
        files(first: 100) { totalCount nodes { path } }
      }
    }
  }
}
"""


def run(args, timeout, tries=1, cwd=ROOT):
    """args' standard output; the last error after `tries` attempts (GitHub drops connections now and then)."""
    for attempt in range(tries):
        try:
            return subprocess.run(args, cwd=cwd, check=True, capture_output=True, text=True, timeout=timeout).stdout
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired) as error:
            if attempt == tries - 1:
                raise RuntimeError("%s failed: %s" % (" ".join(args[:3]), getattr(error, "stderr", "") or error))


class PR:
    """An open PR: its number, whether it is a draft, its head commit and that commit's date, and which of the paths
    open_prs() was asked about it may touch (all of them when it has more files than one page lists)."""

    def __init__(self, number, draft, head, committed, touches):
        self.number, self.draft, self.head, self.committed, self.touches = number, draft, head, committed, touches


def open_prs(paths):
    """Every open PR (PR), with the paths of `paths` it may touch."""
    out = run(["gh", "api", "graphql", "--paginate", "-F", "owner={owner}", "-F", "repo={repo}", "-f",
               "query=" + QUERY, "--jq", ".data.repository.pullRequests.nodes[]"], 300, tries=3)
    return [read_pr(json.loads(line), paths) for line in out.splitlines()]


def read_pr(node, paths):
    """A PR from one of QUERY's nodes. A path that ends in "/" stands for every .go file under it."""
    files = node["files"]
    if files["totalCount"] > len(files["nodes"]):
        touches = set(paths)  # more files than one page lists: read its head to see
    else:
        names = [f["path"] for f in files["nodes"]]
        touches = {p for p in paths if any(n == p or p.endswith("/") and n.startswith(p) and n.endswith(".go")
                                           for n in names)}
    committed = datetime.fromisoformat(node["commits"]["nodes"][0]["commit"]["committedDate"].replace("Z", "+00:00"))
    return PR(node["number"], node["isDraft"], node["headRefOid"], committed, touches)


def counted(prs, path, idle_days, now=None, log=sys.stderr):
    """The PRs of `prs` that may touch path, less the drafts whose head commit is more than idle_days old."""
    now = now or datetime.now(timezone.utc)
    out = []
    for pr in prs:
        if path not in pr.touches:
            continue
        if pr.draft and now - pr.committed > timedelta(days=idle_days):
            print("#%d: left out, a draft whose head commit is from %s" % (pr.number, pr.committed.date()), file=log)
        else:
            out.append(pr)
    return out


def fetch(prs):
    """Fetches the PRs' heads from origin, by commit: objects only, no ref and no FETCH_HEAD."""
    heads = sorted({pr.head for pr in prs})
    for start in range(0, len(heads), 20):
        run(["git", "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "origin"] + heads[start:start + 20],
            600, tries=3)


def merge_base(head, base):
    """The commit head and base last shared."""
    return run(["git", "merge-base", head, base], 60).strip()


def show(revision, path):
    """path's text at revision, or None when it has no such file there."""
    try:
        return run(["git", "show", "%s:%s" % (revision, path)], 60)
    except RuntimeError:
        return None


# ---- the server's central lists ----


class CentralList:
    """One row of central_lists_test.go's table: its id, its source file and its list file (from the repository's
    root), and what one name is."""

    def __init__(self, id, source, file, what):
        self.id, self.source, self.file, self.what = id, source, file, what


GO_NAMES = ["go", "test", "-count=1", "-run", "^TestCentralListNames$", "./internal/archguard", "-args"]


def central_names(work, sources=None):
    """(lists, names): central_lists_test.go's table of central lists (CentralList), and the names each reads in the
    given sources ({(list id, key): text}), {(list id, key): names, or None for a file that does not parse}. work is a
    directory to use, empty or missing. The archguard test reads the files there, so the names are the ones the freeze
    compares."""
    os.makedirs(work, exist_ok=True)
    for (list_id, key), text in (sources or {}).items():
        os.makedirs(os.path.join(work, list_id), exist_ok=True)
        Path(work, list_id, key + ".go").write_text(text)
    run(GO_NAMES + ["-central-list-names=" + os.path.abspath(work)], 900, cwd=ROOT / "server")
    lists = [CentralList(*line.split("\t")) for line in Path(work, "lists.tsv").read_text().splitlines()]
    names = {}
    for list_id, key in sources or {}:
        path = Path(work, list_id, key + ".names")
        names[(list_id, key)] = set(path.read_text().splitlines()) if path.exists() else None
    return lists, names


CENTRAL = Format()


def central_pending(base, lists, prs, idle_days, work, log=sys.stderr):
    """(today, found) for the central lists: the names each has at base ({id: names}), and the names the counted PRs
    of prs (open_prs()) add to it ({id: {name: [PR numbers]}})."""
    per_list = {l.id: counted(prs, l.source, idle_days, log=log) for l in lists}
    fetch([pr for each in per_list.values() for pr in each])
    sources, bases = {}, {}
    for l in lists:
        text = show(base, l.source)
        if text is not None:
            sources[(l.id, "base")] = text
        for pr in per_list[l.id]:
            if pr.head not in bases:
                bases[pr.head] = merge_base(pr.head, base)
            for revision in (pr.head, bases[pr.head]):
                text = show(revision, l.source)
                if text is not None:
                    sources[(l.id, revision)] = text
    _, names = central_names(work, sources)
    today, found = {}, {}
    for l in lists:
        today[l.id] = names.get((l.id, "base")) or set()
        found[l.id] = {}
        for pr in per_list[l.id]:
            added = (names.get((l.id, pr.head)) or set()) - (names.get((l.id, bases[pr.head])) or set()) - today[l.id]
            for name in added:
                found[l.id].setdefault(name, []).append(pr.number)
            print("%s #%d: %s" % (l.id, pr.number, " ".join(sorted(added)) or "no new name"), file=log)
    return today, {i: {n: sorted(p) for n, p in sorted(f.items())} for i, f in found.items()}


# ---- the server's undeclared packages: names read from the whole tree ----


class TreeList:
    """A freeze whose names come from the server's whole tree: its id, its list file (from the repository's root) and
    what one name is."""

    def __init__(self, id, file, what):
        self.id, self.file, self.what, self.source = id, file, what, "server/"


UNDECLARED = TreeList("domain_layers_undeclared", "server/internal/archguard/testdata/domain_layers/undeclared.txt",
                      "a package directory under server/ with no //persona:layer line")

GO_LAYERS = ["go", "test", "-count=1", "-run", "^TestDomainLayerNames$", "./internal/archguard", "-args"]

SKIPPED_DIRS = ("testdata", "vendor")

# How many PRs' trees are on disk at once while the domain lists' pending names and counts are read: each is two
# copies (its head and its merge base) of the directories it changes, and package assistant alone is 1,700 files and
# over 20 MB, so writing every open PR's trees at once would take gigabytes of the temporary directory.
BATCH = 8


def server_go(path):
    """Whether path is a .go file under server/ that the domain rules read: none under testdata, vendor or a hidden
    directory, and no hidden file (server/internal/archguard/domainrules' Load)."""
    parts = path.split("/")
    if parts[0] != "server" or not path.endswith(".go"):
        return False
    return not any(p in SKIPPED_DIRS or p[:1] in "._" for p in parts[1:-1]) and parts[-1][:1] not in "._"


def tree_files(revision, dirs=None):
    """{path under server/: text} of the .go files under server/ at revision that the domain rules read; with dirs
    (directories under server/), only the files directly in them."""
    if dirs is None:
        listed = run(["git", "ls-tree", "-r", "--name-only", revision, "--", "server/"], 120).splitlines()
    else:
        listed = []
        for start in range(0, len(dirs), 200):
            listed += run(["git", "ls-tree", "--name-only", revision, "--"] +
                          ["server/%s/" % d if d else "server/" for d in dirs[start:start + 200]], 120).splitlines()
    paths = [p for p in listed if server_go(p)]
    out = {}
    for start in range(0, len(paths), 500):
        chunk = paths[start:start + 500]
        result = subprocess.run(["git", "cat-file", "--batch"], cwd=ROOT, check=True, capture_output=True, timeout=300,
                                input="".join("%s:%s\n" % (revision, p) for p in chunk).encode())
        data, at = result.stdout, 0
        for path in chunk:
            header_end = data.index(b"\n", at)
            header = data[at:header_end].split()
            if header[-1] == b"missing":
                at = header_end + 1
                continue
            size = int(header[2])
            out[path[len("server/"):]] = data[header_end + 1:header_end + 1 + size].decode("utf-8", "replace")
            at = header_end + 1 + size + 1
    return out


def clause(text):
    """A Go file's text up to and including its package clause: all a layer is read from (the package comment, a
    generated-code marker and the clause), so a tree written for layer_names stays small."""
    lines = text.splitlines(True)
    for i, line in enumerate(lines):
        if re.match(r"package\s+\w", line):
            return "".join(lines[:i + 1])
    return text


def layer_names(work, trees):
    """{key: names}: the undeclared package directories in each tree ({key: {path under server/: text}}), as archguard's
    TestDomainLayerNames reads them; a tree it could not read has no entry. work is a directory to use, empty or
    missing."""
    return domain_trees(work, trees).names


class Trees:
    """What archguard's TestDomainLayerNames read in some trees, by key: the undeclared package directories (names,
    {key: set}), the counts ({key: {"<key> <rule>": (count, types)}}, the types for the counts an allowance can name),
    the findings no count holds ({key: set of "<rule>\t<frozen name or message>"}), and the server's own counts (own,
    or None when not asked for). A tree it could not read has no entry."""

    def __init__(self, names, counts, others, own):
        self.names, self.counts, self.others, self.own = names, counts, others, own


def read_counts(path):
    """{"<key> <rule>": (count, types)} of a .counts file TestDomainLayerNames wrote."""
    out = {}
    for line in Path(path).read_text().splitlines():
        if line:
            id_, count, types = line.split("\t")
            out[id_] = (int(count), set(types.split()))
    return out


def domain_trees(work, trees, own=False):
    """Trees: each tree ({key: {path under server/: text}}) read by archguard's TestDomainLayerNames, and with own the
    server's own counts. work is a directory to use, empty or missing."""
    os.makedirs(work, exist_ok=True)
    for key, files in trees.items():
        for rel, text in files.items():
            path = Path(work, key, rel)
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(text)
        Path(work, key).mkdir(exist_ok=True)
    run(GO_LAYERS + ["-domain-layer-names=" + os.path.abspath(work)] + (["-domain-layer-self"] if own else []), 900,
        cwd=ROOT / "server")
    names, counts, others = {}, {}, {}
    for key in trees:
        path = Path(work, key + ".names")
        if path.exists():
            names[key] = set(path.read_text().splitlines())
        if Path(work, key + ".counts").exists() and Path(work, key + ".others").exists():
            counts[key] = read_counts(Path(work, key + ".counts"))
            others[key] = set(Path(work, key + ".others").read_text().splitlines())
    return Trees(names, counts, others, read_counts(Path(work, "self.counts")) if own else None)


def changed_dirs(old, new):
    """The directories under server/ of the .go files the domain rules read that differ between two commits."""
    paths = run(["git", "diff", "--name-only", "--no-renames", old, new, "--", "server/"], 120).splitlines()
    return sorted({p[len("server/"):].rpartition("/")[0] for p in paths if server_go(p)})


def tree_pending(base, prs, idle_days, work, log=sys.stderr):
    """(today, found) for the undeclared packages: the ones base has, and the ones each counted PR of prs adds
    ({name: [PR numbers]}): undeclared at its head among the directories it changes, not at its merge base, not at
    base."""
    counted_prs = counted(prs, UNDECLARED.source, idle_days, log=log)
    fetch(counted_prs)

    def heads(revision, dirs=None):
        return {rel: clause(text) for rel, text in tree_files(revision, dirs).items()}

    trees, keys, names, batches = {"base": heads(base)}, {}, {}, []

    def flush():
        """Reads the trees gathered since the last flush, then removes them: at most BATCH PRs' trees are on disk at
        once (each a file per .go file of the directories it changes, so thousands of files for package assistant)."""
        batch = os.path.join(work, "batch%d" % len(batches))
        batches.append(batch)
        names.update(layer_names(batch, trees))
        shutil.rmtree(batch, ignore_errors=True)
        trees.clear()

    for pr in counted_prs:
        mb = merge_base(pr.head, base)
        dirs = changed_dirs(mb, pr.head)
        if dirs:
            keys[pr.number] = ("head%d" % pr.number, "mb%d" % pr.number)
            trees[keys[pr.number][0]] = heads(pr.head, dirs)
            trees[keys[pr.number][1]] = heads(mb, dirs)
            if len(trees) >= 2 * BATCH:
                flush()
    if trees:
        flush()
    today, found = names.get("base", set()), {}
    for pr in counted_prs:
        head, mb = keys.get(pr.number, (None, None))
        added = names.get(head, set()) - names.get(mb, set()) - today
        for name in added:
            found.setdefault(name, []).append(pr.number)
        print("%s #%d: %s" % (UNDECLARED.id, pr.number, " ".join(sorted(added)) or "no new name"), file=log)
    return today, {n: sorted(p) for n, p in sorted(found.items())}


# ---- the domain rules' baseline: counts read from the whole tree, allowances for the types open PRs add ----


BASELINE = TreeList("domain_layers_baseline", "server/internal/archguard/testdata/domain_layers/baseline.txt",
                    "a type an open PR adds to an exported-field record or named primitive type count")

ALLOWABLE = ("app-exported-record", "app-named-primitive")  # the counts of named types, which an allowance names

EDGES = "server/internal/archguard/testdata/domain_layers/legacy_edges.txt"

GO_UPDATE = ["go", "test", "-count=1", "-run", "^TestDomainLayers$", "./internal/archguard/"]


class Pending:
    """What the open PRs add to the domain baseline: the server's own counts now (own, {id: (count, types)}), the types
    each counted PR adds to a count an allowance can name and the tree lacks (found, {id: {type: [PR numbers]}}), what
    no allowance covers (failing, {PR number: [reasons]}): an addition to another count, a new import or rule finding
    that fails at once, a package this tree declares that the PR changes, a tree that could not be read; and the PRs
    measured (measured, a set of numbers), whose found types replace what the list had for them."""

    def __init__(self, own, found, failing, measured=frozenset()):
        self.own, self.found, self.failing, self.measured = own, found, failing, set(measured)


def declared_dirs(revision):
    """The directories under server/ whose files at revision declare a layer before their package clause: the domain
    packages a PR's tree needs beside it, so its counts read the layers it imports."""
    dirs = set()
    for rel, text in tree_files(revision).items():
        if re.search(r"(?m)^//persona:layer[ \t]+\S", clause(text)):
            dirs.add(rel.rpartition("/")[0])
    return sorted(dirs)


def baseline_pending(base, prs, idle_days, work, log=sys.stderr):
    """Pending for the domain baseline the day it lands. A counted PR of prs (open_prs()) adds what its head has and its
    merge base with base does not, read over the directories it changes, beside the packages this tree (HEAD)
    declares. For a count an allowance can name, that is as many of the types its head adds as the count rises by,
    less the ones the tree has already; for any other count, or a finding that fails at once, nothing covers it."""
    counted_prs = counted(prs, BASELINE.source, idle_days, log=log)
    fetch(counted_prs)
    context = declared_dirs("HEAD")
    shared = tree_files("HEAD", context)
    trees, keys, failing, batches = {}, {}, {}, []
    read = Trees({}, {}, {}, None)

    def flush():
        """Reads the trees gathered since the last flush, then removes them: at most BATCH PRs' trees are on disk at
        once. The first read also takes the server's own counts."""
        batch = os.path.join(work, "batch%d" % len(batches))
        batches.append(batch)
        got = domain_trees(batch, trees, own=read.own is None)
        read.counts.update(got.counts)
        read.others.update(got.others)
        read.own = read.own if read.own is not None else got.own
        shutil.rmtree(batch, ignore_errors=True)
        trees.clear()

    for pr in counted_prs:
        if in_tree(pr.head):
            print("%s #%d: its head is in this tree (the branch that lands the baseline)" % (BASELINE.id, pr.number),
                  file=log)
            continue
        mb = merge_base(pr.head, base)
        dirs = changed_dirs(mb, pr.head)
        declared = sorted(set(dirs) & set(context))
        if declared:
            failing.setdefault(pr.number, []).append(
                "changes %s, which this tree declares: check it against the rules by hand" % ", ".join(declared))
        dirs = [d for d in dirs if d not in context]
        if not dirs:
            continue
        keys[pr.number] = ("head%d" % pr.number, "mb%d" % pr.number)
        for key, revision in zip(keys[pr.number], (pr.head, mb)):
            files = dict(shared)
            files.update(tree_files(revision, dirs))
            trees[key] = files
        if len(trees) >= 2 * BATCH:
            flush()
    if trees or read.own is None:
        flush()
    frozen = {e.name for e in CENTRAL.read((ROOT / EDGES).read_text())[0]}
    found = {}
    for pr in counted_prs:
        if pr.number not in keys:
            continue
        head, mb = keys[pr.number]
        if head not in read.counts or mb not in read.counts:
            failing.setdefault(pr.number, []).append("its tree could not be read (a file that does not parse?)")
            continue
        added_counts = []
        for id_, (count, types) in sorted(read.counts[head].items()):
            before, before_types = read.counts[mb].get(id_, (0, set()))
            if count <= before:
                continue
            if id_.split(" ")[1] in ALLOWABLE:
                new = sorted(types - before_types - read.own.get(id_, (0, set()))[1])[:count - before]
                for name in new:
                    found.setdefault(id_, {}).setdefault(name, []).append(pr.number)
                added_counts.append("%s +%d (%s)" % (id_, count - before, " ".join(new) or "the tree has them"))
            else:
                failing.setdefault(pr.number, []).append("adds %d to %s, which takes no allowance" % (count - before, id_))
        for other in sorted(read.others[head] - read.others[mb]):
            rule, what = other.split("\t", 1)
            if what not in frozen:
                failing.setdefault(pr.number, []).append("%s: %s" % (rule, what))
        print("%s #%d: %s" % (BASELINE.id, pr.number, "; ".join(added_counts) or "no count rises"), file=log)
    return Pending(read.own, {i: {n: sorted(p) for n, p in sorted(t.items())} for i, t in sorted(found.items())},
                   failing, {pr.number for pr in counted_prs})


def in_tree(head):
    """Whether a PR's head commit is in HEAD's history, so the tree has all it adds."""
    return subprocess.run(["git", "merge-base", "--is-ancestor", head, "HEAD"], cwd=ROOT,
                          capture_output=True).returncode == 0


def baseline_entries(text):
    """(lead, counts, allowances) of baseline.txt: its leading comment's lines, {id: count} and {(id, type): [PR
    numbers]}. Other lines are left out: TestDomainLayers reports them."""
    lead, counts, allowances, started = [], {}, {}, False
    for raw in text.splitlines():
        line = raw.strip()
        if not started and (not line or line.startswith("#")):
            lead.append(raw)
            continue
        started = True
        fields = line.split()
        if len(fields) == 3 and fields[2].isdigit():
            counts["%s %s" % tuple(fields[:2])] = int(fields[2])
        elif len(fields) > 3 and not fields[2].isdigit() and all(re.fullmatch(r"#\d+", f) for f in fields[3:]):
            allowances[("%s %s" % tuple(fields[:2]), fields[2])] = [int(f[1:]) for f in fields[3:]]
    while lead and not lead[-1].strip():
        lead.pop()
    return lead, counts, allowances


def compose_baseline(lead, counts, allowances):
    """baseline.txt's text: the leading comment, then each count's line before its allowances, sorted."""
    lines = [(id_, 0, "", "%s %d" % (id_, n)) for id_, n in counts.items() if n > 0]
    lines += [(id_, 1, name, "%s %s %s" % (id_, name, " ".join("#%d" % n for n in sorted(prs))))
              for (id_, name), prs in allowances.items()]
    return "\n".join(lead + [line for *_, line in sorted(lines)]) + "\n"


def baseline_rewrite(text, pending, still_open):
    """The baseline's text the day it lands: its leading comment, a count line for every count the tree has now, then
    the allowances: the ones the open PRs add now, and the listed ones whose type the tree lacks for PRs still open
    that this run did not measure (a PR it measured has only what it adds now)."""
    lead, _, listed = baseline_entries(text)
    counts = {id_: n for id_, (n, _) in pending.own.items()}
    allowances = {}
    for (id_, name), prs in listed.items():
        waiting = (set(prs) & still_open) - pending.measured
        if name not in pending.own.get(id_, (0, set()))[1] and waiting:
            allowances[(id_, name)] = sorted(waiting)
    for id_, types in pending.found.items():
        for name, prs in types.items():
            allowances[(id_, name)] = sorted(set(allowances.get((id_, name), [])) | set(prs))
    return compose_baseline(lead, counts, allowances)


def baseline_prune(text, still_open):
    """(text, dropped): the baseline with the allowances left whose PRs have all closed, after the counts took in the
    ones whose types landed (lower_baselines.py's update, run first), and the lines dropped."""
    lead, counts, allowances = baseline_entries(text)
    kept = {k: prs for k, prs in allowances.items() if set(prs) & still_open}
    dropped = ["%s %s" % key for key in sorted(set(allowances) - set(kept))]
    return compose_baseline(lead, counts, {k: sorted(set(prs) & still_open) for k, prs in kept.items()}), dropped


def on_base(base, path):
    return subprocess.run(["git", "cat-file", "-e", "%s:%s" % (base, path)], cwd=ROOT,
                          capture_output=True).returncode == 0


def carry_moves(base, log=sys.stderr):
    """`moves`: the domain lists rewritten along the packages moved since the merge base of HEAD with base
    (TestDomainLayers with DOMAIN_LAYERS_MOVES and CI_BASE_SHA), the lines it changed and a verdict. 0 when
    TestDomainLayers passes afterwards."""
    sha = run(["git", "rev-parse", "--verify", base + "^{commit}"], 60).strip()
    paths = [UNDECLARED.file, EDGES, BASELINE.file]
    before = {p: (ROOT / p).read_text() for p in paths}
    try:
        result = subprocess.run(GO_UPDATE + ["-v"], cwd=ROOT / "server", capture_output=True, text=True, timeout=1200,
                                env=dict(os.environ, CI_BASE_SHA=sha, DOMAIN_LAYERS_MOVES="1"))
    except subprocess.TimeoutExpired:
        print("moves: VERDICT timeout: TestDomainLayers ran over 1200 s; the lists may be half rewritten, see git diff",
              file=log)
        return 1
    changed = 0
    for p in paths:
        old, new = before[p].splitlines(), (ROOT / p).read_text().splitlines()
        gone, added = sorted(set(old) - set(new)), sorted(set(new) - set(old))
        changed += len(gone) + len(added)
        for line in gone:
            print("%s: -%s" % (p, line), file=log)
        for line in added:
            print("%s: +%s" % (p, line), file=log)
    if result.returncode != 0:
        failures = [l.strip() for l in (result.stdout + result.stderr).splitlines() if "domain_test.go" in l
                    and "notice:" not in l and "domain layers:" not in l and "carried " not in l]
        print("\n".join(failures[-20:] or (result.stdout + result.stderr).splitlines()[-20:]), file=log)
        print("moves: VERDICT failing: %d list line(s) changed, and TestDomainLayers still fails on what no move "
              "carries (above)" % changed, file=log)
        return 1
    print("moves: VERDICT ok: %d list line(s) changed, and TestDomainLayers passes against %s" % (changed, base),
          file=log)
    return 0


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    sub = parser.add_subparsers(dest="command", required=True)
    moves = sub.add_parser("moves", help="carry the domain lists' lines along the packages moved since --base")
    moves.add_argument("--base", default="origin/main", help="the moves are read since HEAD's merge base with it "
                       "(default origin/main)")
    cmd = sub.add_parser("pending", help="the names open PRs add to the server's central lists")
    cmd.add_argument("--base", default="origin/main", help="what the names are counted against (default origin/main)")
    cmd.add_argument("--idle-days", type=int, default=14, help="a draft whose head commit is older is left out")
    cmd.add_argument("--write", action="store_true", help="rewrite the lists' files (a list --base has: pruned only)")
    cmd.add_argument("--only", help="comma-separated list ids (default every list)")
    args = parser.parse_args(argv)
    if args.command == "moves":
        return carry_moves(args.base, log=sys.stderr)
    base = run(["git", "rev-parse", "--verify", args.base + "^{commit}"], 60).strip()
    wanted = set(args.only.split(",")) if args.only else None
    with tempfile.TemporaryDirectory(prefix="name-freezes-") as work:
        lists, _ = central_names(os.path.join(work, "table"))
        lists = [l for l in lists + [UNDECLARED, BASELINE] if wanted is None or l.id in wanted]
        if wanted is not None and wanted - {l.id for l in lists}:
            print("unknown list id(s): %s" % ", ".join(sorted(wanted - {l.id for l in lists})), file=sys.stderr)
            return 2
        if wanted is None:
            # The domain lists read every open PR's tree, and once on --base they take nothing new: a run for the
            # central lists (a seam's new row, on its own branch) leaves them out unless --only names them.
            for l, how in ((UNDECLARED, "prunes it"), (BASELINE, "tidies it on a checkout of %s" % args.base)):
                if l in lists and on_base(base, l.file):
                    print("%s: on %s already, so left out: `pending --write --only %s` %s" % (
                        l.id, args.base, l.id, how), file=sys.stderr)
                    lists.remove(l)
        baseline = BASELINE in lists
        lists = [l for l in lists if l is not BASELINE]
        landed = baseline and on_base(base, BASELINE.file)
        if landed and args.write and run(["git", "rev-parse", "HEAD"], 60).strip() != base:
            print("%s is on %s already: --write takes the allowances whose types landed into their counts from the "
                  "working tree, so check out %s first" % (BASELINE.file, args.base, args.base), file=sys.stderr)
            return 1
        prs = open_prs([l.source for l in lists] + ([BASELINE.source] if baseline else []))
        central = [l for l in lists if l is not UNDECLARED]
        today, found = central_pending(base, central, prs, args.idle_days, os.path.join(work, "names")) if central \
            else ({}, {})
        if UNDECLARED in lists:
            today[UNDECLARED.id], found[UNDECLARED.id] = tree_pending(base, prs, args.idle_days,
                                                                      os.path.join(work, "trees"))
        pending = None
        if baseline and not (landed and args.write):
            pending = baseline_pending(base, prs, args.idle_days, os.path.join(work, "counts"))
    for l in lists:
        print("== %s (%s): %d names at %s, %d pending from %d PRs" % (
            l.id, l.source, len(today[l.id]), args.base, len(found[l.id]),
            len({n for numbers in found[l.id].values() for n in numbers})))
        for name, numbers in found[l.id].items():
            print("%s %s" % (name, " ".join("#%d" % n for n in numbers)))
    if pending is not None:
        allowed = [(id_, name, numbers) for id_, types in pending.found.items() for name, numbers in types.items()]
        print("== %s (%s): %d counts now, %d allowances from %d PRs, %d PRs no allowance covers" % (
            BASELINE.id, BASELINE.source, len(pending.own), len(allowed),
            len({n for *_, numbers in allowed for n in numbers}), len(pending.failing)))
        for id_, name, numbers in allowed:
            print("%s %s %s" % (id_, name, " ".join("#%d" % n for n in numbers)))
        for number, reasons in sorted(pending.failing.items()):
            for reason in reasons:
                print("#%d would fail: %s" % (number, reason))
    if not args.write:
        return 0
    failed, still_open = False, {pr.number for pr in prs}
    for l in lists:
        path = ROOT / l.file
        text = path.read_text() if path.exists() else (
            "# The names %s may have (each %s), frozen: server/internal/archguard/central_lists_test.go\n"
            "# fails on any other (docs/server/architecture.md \"Registering a domain's entries\"). Delete a line\n"
            "# once the test's notice names it; add none.\n" % (l.source, l.what))
        problems = CENTRAL.read(text)[1]
        if problems:
            print("fix %s first:\n%s" % (l.file, "\n".join(problems)), file=sys.stderr)
            return 1
        if on_base(base, l.file):
            new, unlisted = CENTRAL.prune(text, today[l.id], still_open)
            note = " (on %s already: pruned, nothing added)" % args.base
        else:
            new, unlisted = CENTRAL.rewrite(text, today[l.id], found[l.id], still_open, date.today().isoformat(),
                                            args.idle_days), []
            note = ""
        path.write_text(new)
        for line in CENTRAL.changes(text, new):
            print("%s, %s%s" % (l.file, line, note), file=sys.stderr)
        for name in unlisted:
            failed = True
            fix = ("Give it a //persona:layer line (docs/server/architecture.md \"Domain model\")" if l is UNDECLARED
                   else "Move its entry to its domain's own file (docs/server/architecture.md \"Registering a domain's "
                   "entries\")")
            print("%s: %s has %s, which the list does not: --write adds no name to a list that has landed, so the "
                  "freeze still fails on it. %s." % (l.file, args.base, name, fix), file=sys.stderr)
    if baseline:
        failed = write_baseline(landed, pending, still_open, args.base) or failed
    return 1 if failed else 0


def write_baseline(landed, pending, still_open, base_name):
    """Rewrites baseline.txt: the day it lands, to the tree's counts and the open PRs' allowances; once it has landed,
    the counts take in the allowances whose types landed (TestDomainLayers' update, the one lower_baselines.py runs)
    and the allowances whose PRs have all closed leave. Whether it failed."""
    path = ROOT / BASELINE.file
    old = path.read_text()
    if landed:
        update = subprocess.run(GO_UPDATE, cwd=ROOT / "server", capture_output=True, text=True, timeout=1200,
                                env=dict(os.environ, DOMAIN_LAYERS_UPDATE="1"))
        new, dropped = baseline_prune(path.read_text(), still_open)
        path.write_text(new)
        print("%s: %d allowance(s) whose PRs have all closed dropped%s (on %s already: nothing added)" % (
            BASELINE.file, len(dropped), "".join("\n  -" + d for d in dropped), base_name), file=sys.stderr)
        if update.returncode != 0:
            print("%s: TestDomainLayers failed after lowering, so a count is above its limit:\n%s" % (
                BASELINE.file, "\n".join((update.stdout + update.stderr).splitlines()[-20:])), file=sys.stderr)
            return True
        return False
    new = baseline_rewrite(old, pending, still_open)
    path.write_text(new)
    _, old_counts, old_allowed = baseline_entries(old)
    _, new_counts, new_allowed = baseline_entries(new)
    print("%s: %d counts (%d changed), %d allowances (+%d -%d)" % (
        BASELINE.file, len(new_counts), len({k for k in set(old_counts) | set(new_counts)
                                             if old_counts.get(k) != new_counts.get(k)}),
        len(new_allowed), len(set(new_allowed) - set(old_allowed)), len(set(old_allowed) - set(new_allowed))),
        file=sys.stderr)
    return False


if __name__ == "__main__":
    sys.exit(main())
