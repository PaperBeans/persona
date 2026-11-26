"""Tests for scripts/test/name_freezes.py: the name freezes' list format and rewrite, the open PRs it counts, and the
server's central lists it reads through server/internal/archguard/central_lists_test.go."""
from datetime import date, datetime, timedelta, timezone
import io
from pathlib import Path
import shutil
import sys
import tempfile
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import name_freezes  # noqa: E402

ROOT = name_freezes.ROOT
LISTS = ROOT / "server/internal/archguard/testdata/central_lists"
NOW = datetime(2026, 11, 22, tzinfo=timezone.utc)
LISTED = '# a header\n"GET /api/v1/a"\n"GET /api/v1/b"\n\n# pending\n"PUT /api/v1/p" #4512\n'


def node(number, paths, draft=False, days_old=0, total=None):
    """One of the GraphQL query's PR nodes."""
    committed = (NOW - timedelta(days=days_old)).isoformat().replace("+00:00", "Z")
    return {"number": number, "isDraft": draft, "headRefOid": "head%d" % number,
            "commits": {"nodes": [{"commit": {"committedDate": committed}}]},
            "files": {"totalCount": len(paths) if total is None else total, "nodes": [{"path": p} for p in paths]}}


class FormatTests(unittest.TestCase):
    format = name_freezes.CENTRAL

    def test_a_name_with_spaces_and_a_pending_one(self):
        entries, problems = self.format.read(LISTED)
        self.assertEqual(problems, [])
        self.assertEqual([(e.line, e.name, e.prs) for e in entries],
                         [(2, '"GET /api/v1/a"', []), (3, '"GET /api/v1/b"', []), (6, '"PUT /api/v1/p"', ["#4512"])])
        self.assertEqual([(e.name, e.prs) for e in self.format.read('"GET " + P #1 #22\n')[0]],
                         [('"GET " + P', ["#1", "#22"])])

    def test_the_parts_are_sorted_unique_and_pending_last(self):
        for text, problem in (("B\nA\n", "line 2 (A) is out of order"),
                              ("A\nA\n", "line 2 repeats A (line 1)"),
                              ("X #1\nA\n", "line 2 (A) has no PR after the pending entries"),
                              ("A\nY #2\nX #1\n", "line 3 (X) is out of order")):
            with self.subTest(text=text):
                problems = self.format.read(text)[1]
                self.assertTrue(any(p.startswith(problem) for p in problems), problems)

    def test_a_name_check(self):
        strict = name_freezes.Format(what="a MOCK_* name", example="MOCK_X #123", valid=str.isupper)
        self.assertEqual(strict.read("MOCK_A # note\n")[1],
                         ["line 1 is neither a MOCK_* name nor a pending one with its PRs (MOCK_X #123): "
                          "'MOCK_A # note'"])

    def rewrite(self, today, found=None, still_open=(4512,), text=LISTED):
        return self.format.rewrite(text, set(today), found or {}, set(still_open), "2026-11-22", 14)

    def test_the_land_day_rewrite_has_both_parts_with_a_fresh_pending_header(self):
        today = {'"GET /api/v1/a"', '"GET /api/v1/b"'}
        new = self.rewrite(today, {'"POST /api/v1/c"': [9, 7]})
        header = name_freezes.PENDING_HEADER.format(day="2026-11-22", idle=14)
        self.assertEqual(new, '# a header\n"GET /api/v1/a"\n"GET /api/v1/b"\n\n%s\n"POST /api/v1/c" #7 #9\n'
                         '"PUT /api/v1/p" #4512\n' % header)
        self.assertEqual(self.format.read(new)[1], [])
        self.assertEqual(self.rewrite(today, {'"POST /api/v1/c"': [7, 9]}, still_open=(4512, 7, 9), text=new), new)
        self.assertEqual(self.format.changes(LISTED, new), ['names: 2', 'pending: 2 +"POST /api/v1/c"'])

    def test_a_land_day_rerun_drops_closed_prs_and_keeps_merged_names(self):
        # Nothing found added: #4512 merged, so the base has its route; #11's closed unmerged.
        text = LISTED.replace('"PUT /api/v1/p" #4512', '"PUT /api/v1/gone" #11\n"PUT /api/v1/p" #4512')
        new = self.rewrite({'"GET /api/v1/a"', '"GET /api/v1/b"', '"PUT /api/v1/p"'}, still_open=(), text=text)
        self.assertEqual(new, '# a header\n"GET /api/v1/a"\n"GET /api/v1/b"\n"PUT /api/v1/p"\n')
        self.assertEqual(self.format.changes(text, new),
                         ['names: 3 +"PUT /api/v1/p"', 'pending: 0 -"PUT /api/v1/gone" -"PUT /api/v1/p"'])

    def test_a_prune_of_a_landed_list_adds_no_name(self):
        # On the base: "A", "C" (#5 merged), "X" (on no line: a red main, or a branch as the base); "B" has gone.
        text = '# a header\n"A"\n"B"\n\n# pending, 2026-11-01\n"C" #5\n"D" #8\n"E" #9\n'
        new, unlisted = self.format.prune(text, {'"A"', '"C"', '"X"'}, {8})
        self.assertEqual(new, '# a header\n"A"\n"C"\n\n# pending, 2026-11-01\n"D" #8\n')
        self.assertEqual(unlisted, ['"X"'])
        self.assertEqual(self.format.changes(text, new), ['names: 2 +"C" -"B"', 'pending: 1 -"C" -"E"'])
        self.assertEqual(self.format.prune(new, {'"A"', '"C"', '"D"'}, set()), ('# a header\n"A"\n"C"\n"D"\n', []))

    def test_the_committed_lists_read_cleanly(self):
        paths = sorted(LISTS.glob("*.txt"))
        self.assertEqual([p.name for p in paths], ["assistantwire_routes.txt", "extwire_route_scopes.txt",
                                                   "node_handle_method.txt", "prompts_assistant.txt",
                                                   "proto_features.txt"])
        for path in paths:
            with self.subTest(path=path.name):
                text = path.read_text()
                entries, problems = self.format.read(text)
                self.assertEqual(problems, [])
                self.assertTrue(text.startswith("# "), "a list starts with what it is")
                self.assertGreater(len(entries), 50)


class OpenPRTests(unittest.TestCase):
    PATHS = ["server/internal/assistantwire/bus.go", "server/internal/prompts/assistant.go"]

    def test_a_pr_touches_the_paths_its_files_name_or_all_when_they_are_cut_off(self):
        self.assertEqual(name_freezes.read_pr(node(1, ["web/a.ts", self.PATHS[0]]), self.PATHS).touches,
                         {self.PATHS[0]})
        self.assertEqual(name_freezes.read_pr(node(2, ["web/a.ts"]), self.PATHS).touches, set())
        self.assertEqual(name_freezes.read_pr(node(3, ["web/a.ts"], total=101), self.PATHS).touches, set(self.PATHS))

    def test_idle_drafts_are_left_out(self):
        prs = [name_freezes.read_pr(n, self.PATHS) for n in (
            node(1, self.PATHS), node(2, self.PATHS, draft=True, days_old=15),
            node(3, self.PATHS, draft=True, days_old=13), node(4, self.PATHS, days_old=40), node(5, ["web/a.ts"]))]
        log = io.StringIO()
        counted = name_freezes.counted(prs, self.PATHS[1], 14, now=NOW, log=log)
        self.assertEqual([pr.number for pr in counted], [1, 3, 4])
        self.assertEqual(log.getvalue(), "#2: left out, a draft whose head commit is from 2026-11-07\n")


class CentralPendingTests(unittest.TestCase):
    def test_names_a_pr_adds_less_its_merge_base_and_the_base(self):
        lists = [name_freezes.CentralList("routes", "server/bus.go", "server/routes.txt", "route"),
                 name_freezes.CentralList("prompts", "server/prompts.go", "server/prompts.txt", "declaration")]
        files = {("base", "server/bus.go"): "A\nB\n", ("base", "server/prompts.go"): "P\n",
                 ("head1", "server/bus.go"): "A\nB\nC\nM\n", ("mb1", "server/bus.go"): "A\nM\n",
                 ("head2", "server/bus.go"): "A\nC\n", ("mb2", "server/bus.go"): "A\n",
                 ("head2", "server/prompts.go"): "P\nQ\n"}  # mb2 has no prompts.go
        paths = [l.source for l in lists]
        prs = [name_freezes.read_pr(n, paths) for n in (
            node(1, ["server/bus.go"]), node(2, paths), node(3, paths, draft=True, days_old=30))]

        def names(work, sources=None):
            return lists, {key: set(text.splitlines()) for key, text in (sources or {}).items()}

        with mock.patch.object(name_freezes, "show", lambda rev, path: files.get((rev, path))), \
                mock.patch.object(name_freezes, "merge_base", lambda head, base: "mb" + head[4:]), \
                mock.patch.object(name_freezes, "fetch", lambda prs: None), \
                mock.patch.object(name_freezes, "central_names", names):
            today, found = name_freezes.central_pending("base", lists, prs, 14, "unused", log=io.StringIO())
        self.assertEqual(today, {"routes": {"A", "B"}, "prompts": {"P"}})
        self.assertEqual(found, {"routes": {"C": [1, 2]}, "prompts": {"Q": [2]}})


class MainTests(unittest.TestCase):
    """main()'s pending --write: a list the base has already is only pruned, and one it does not have yet takes every
    name the base has and the names the open PRs add."""
    TEXT = '# a header\n"A"\n"B"\n\n# pending, 2026-11-01\n"C" #5\n"D" #8\n"E" #9\n'
    BUS = "server/bus.go"

    def write(self, landed):
        """(exit status, the list's file after, standard error) of pending --write against a made-up base, on which
        "X" is a route the list lacks and "B" has gone, with #7 open and adding "F", #8 open and #5 and #9 closed."""
        lists = [name_freezes.CentralList("routes", self.BUS, "routes.txt", "route")]
        files = {("base", self.BUS): '"A"\n"C"\n"X"\n', ("head7", self.BUS): '"A"\n"F"\n', ("mb7", self.BUS): '"A"\n'}
        prs = [name_freezes.read_pr(node(7, [self.BUS]), [self.BUS]), name_freezes.read_pr(node(8, ["web/a.ts"]), [])]
        central_pending = name_freezes.central_pending

        def names(work, sources=None):
            return lists, {key: set(text.splitlines()) for key, text in (sources or {}).items()}

        with tempfile.TemporaryDirectory() as root:
            path = Path(root, "routes.txt")
            path.write_text(self.TEXT)
            err = io.StringIO()
            with mock.patch.multiple(name_freezes, ROOT=Path(root), run=lambda args, timeout, **kw: "base\n",
                                     show=lambda rev, p: files.get((rev, p)), merge_base=lambda head, base: "mb7",
                                     fetch=lambda prs: None, central_names=names, open_prs=lambda paths: prs,
                                     on_base=lambda base, p: landed,
                                     central_pending=lambda *args: central_pending(*args, log=io.StringIO())), \
                    mock.patch("sys.stdout", io.StringIO()), mock.patch("sys.stderr", err):
                code = name_freezes.main(["pending", "--write", "--only", "routes"])
            return code, path.read_text(), err.getvalue()

    def test_a_landed_list_is_pruned_and_a_name_it_lacks_fails(self):
        code, text, err = self.write(landed=True)
        self.assertEqual(text, '# a header\n"A"\n"C"\n\n# pending, 2026-11-01\n"D" #8\n')
        self.assertEqual(code, 1)
        self.assertIn('routes.txt: origin/main has "X", which the list does not: --write adds no name', err)
        self.assertIn('routes.txt, names: 2 +"C" -"B" (on origin/main already: pruned, nothing added)', err)

    def test_the_land_day_takes_every_name_the_base_has_and_the_pending_ones(self):
        code, text, err = self.write(landed=False)
        header = name_freezes.PENDING_HEADER.format(day=date.today().isoformat(), idle=14)
        self.assertEqual(text, '# a header\n"A"\n"C"\n"X"\n\n%s\n"D" #8\n"F" #7\n' % header)
        self.assertEqual(code, 0, err)


class TreeTests(unittest.TestCase):
    """The undeclared packages' freeze reads its names from the whole server tree."""

    def test_the_files_the_domain_rules_read(self):
        for path, read in (("server/internal/a/a.go", True), ("server/cmd/x/main.go", True),
                           ("server/internal/a/a_test.go", True), ("server/internal/a/testdata/b.go", False),
                           ("server/vendor/x/x.go", False), ("server/internal/.cache/x.go", False),
                           ("server/internal/a/_scratch.go", False), ("server/internal/a/README.md", False),
                           ("web/src/a.go", False)):
            with self.subTest(path=path):
                self.assertEqual(name_freezes.server_go(path), read)

    def test_a_file_is_kept_up_to_its_package_clause(self):
        text = "// Code generated by x. DO NOT EDIT.\n\n// Package a is a.\n//\n//persona:layer core\npackage a\n\nimport \"os\"\n"
        self.assertEqual(name_freezes.clause(text), text[:text.index("\n\nimport")] + "\n")
        self.assertEqual(name_freezes.clause("// no clause\n"), "// no clause\n")

    def test_a_pr_touches_the_tree_with_any_go_file_under_it(self):
        paths = [name_freezes.UNDECLARED.source]
        self.assertEqual(name_freezes.read_pr(node(1, ["server/internal/new/new.go"]), paths).touches, {"server/"})
        self.assertEqual(name_freezes.read_pr(node(2, ["server/internal/new/README.md", "web/a.ts"]), paths).touches,
                         set())

    def test_names_a_pr_adds_less_its_merge_base_and_the_base(self):
        # The base has "internal/old" undeclared. #1 adds "internal/new" and a file to "internal/old"; #2 adds
        # "internal/declared", which has its line; #3 is an idle draft.
        trees = {"base": {"internal/old/old.go": "package old\n"},
                 "head1": {"internal/new/new.go": "package new\n", "internal/old/more.go": "package old\n"},
                 "mb1": {"internal/old/old.go": "package old\n"},
                 "head2": {"internal/declared/d.go": "//persona:layer library\npackage declared\n"}, "mb2": {}}
        undeclared = {"base": {"internal/old"}, "head1": {"internal/new", "internal/old"}, "mb1": {"internal/old"},
                      "head2": set(), "mb2": set()}
        paths = [name_freezes.UNDECLARED.source]
        prs = [name_freezes.read_pr(n, paths) for n in (node(1, ["server/internal/new/new.go"]),
                                                         node(2, ["server/internal/declared/d.go"]),
                                                         node(3, ["server/internal/x/x.go"], draft=True, days_old=30))]

        def files(revision, dirs=None):
            return trees[revision]

        # Read in one batch, or a PR's two trees at a time (the base's with the first), each batch removed after.
        for batch, reads in ((8, [["base", "head1", "head2", "mb1", "mb2"]]),
                             (1, [["base", "head1", "mb1"], ["head2", "mb2"]])):
            with self.subTest(batch=batch), tempfile.TemporaryDirectory() as work:
                seen = []

                def names(at, given):
                    seen.append(sorted(given))
                    Path(at).mkdir()
                    return {key: undeclared[key] for key in given}

                with mock.patch.multiple(name_freezes, tree_files=files, merge_base=lambda head, base: "mb" + head[4:],
                                         fetch=lambda prs: None, changed_dirs=lambda old, new: ["dir"],
                                         layer_names=names, BATCH=batch):
                    today, found = name_freezes.tree_pending("base", prs, 14, work, log=io.StringIO())
                self.assertEqual(today, {"internal/old"})
                self.assertEqual(found, {"internal/new": [1]})
                self.assertEqual(seen, reads)
                self.assertEqual(list(Path(work).iterdir()), [], "a batch's trees stay on disk")

    def test_the_land_day_write_takes_the_base_and_the_pending_packages(self):
        with tempfile.TemporaryDirectory() as root:
            path = Path(root, name_freezes.UNDECLARED.file)
            path.parent.mkdir(parents=True)
            path.write_text("# a header\ninternal/gone\ninternal/old\n")
            err = io.StringIO()
            with mock.patch.multiple(name_freezes, ROOT=Path(root), run=lambda args, timeout, **kw: "base\n",
                                     central_names=lambda work, sources=None: ([], {}),
                                     open_prs=lambda paths: [name_freezes.PR(7, False, "head7", NOW, {"server/"})],
                                     tree_pending=lambda base, prs, idle, work: ({"internal/old"}, {"internal/new": [7]}),
                                     on_base=lambda base, p: False), \
                    mock.patch("sys.stdout", io.StringIO()), mock.patch("sys.stderr", err):
                code = name_freezes.main(["pending", "--write", "--only", name_freezes.UNDECLARED.id])
            header = name_freezes.PENDING_HEADER.format(day=date.today().isoformat(), idle=14)
            self.assertEqual(code, 0, err.getvalue())
            self.assertEqual(path.read_text(), "# a header\ninternal/old\n\n%s\ninternal/new #7\n" % header)

    def test_the_committed_list_reads_cleanly(self):
        text = (ROOT / name_freezes.UNDECLARED.file).read_text()
        entries, problems = name_freezes.CENTRAL.read(text)
        self.assertEqual(problems, [])
        self.assertTrue(text.startswith("# "), "a list starts with what it is")
        self.assertGreater(len(entries), 100)


class BaselineTests(unittest.TestCase):
    """The domain baseline: counts from the tree, and allowances naming the types open PRs add."""
    ID = "internal/seats app-exported-record"
    TEXT = ("# a header\n\ninternal/a core-any 3\n" + ID + " 2\n" + ID + " Closed #5\n" + ID + " Landed #7\n" +
            ID + " Waiting #8 #9\n")

    def test_the_entries_and_the_text_they_make(self):
        lead, counts, allowances = name_freezes.baseline_entries(self.TEXT)
        self.assertEqual(lead, ["# a header"])
        self.assertEqual(counts, {"internal/a core-any": 3, self.ID: 2})
        self.assertEqual(allowances, {(self.ID, "Closed"): [5], (self.ID, "Landed"): [7], (self.ID, "Waiting"): [8, 9]})
        # Each count's line before its allowances, the allowances by type, whatever order they come in.
        text = name_freezes.compose_baseline(lead, {self.ID: 2, "internal/a core-any": 3},
                                             {(self.ID, "Waiting"): [9, 8], (self.ID, "Closed"): [5],
                                              (self.ID, "Landed"): [7]})
        self.assertEqual(text, self.TEXT.replace("\n\n", "\n"))

    def test_the_land_day_rewrite_takes_the_tree_and_the_open_prs(self):
        # The tree has Landed now (#7 merged) and counts 3; #5 closed; #9 still open, not measured this time (an idle
        # draft now); #12 adds Fresh; #13 was measured and no longer adds Gone.
        pending = name_freezes.Pending(own={"internal/a core-any": (1, set()), self.ID: (3, {"Landed", "Old"})},
                                       found={self.ID: {"Fresh": [12]}, "internal/b app-named-primitive": {"Kind": [12]}},
                                       failing={}, measured={12, 13})
        text = name_freezes.baseline_rewrite(self.TEXT + self.ID + " Gone #13\n", pending, {9, 12, 13})
        self.assertEqual(text, "# a header\ninternal/a core-any 1\ninternal/b app-named-primitive Kind #12\n" + self.ID +
                         " 3\n" + self.ID + " Fresh #12\n" + self.ID + " Waiting #9\n")

    def test_a_landed_baseline_drops_the_allowances_whose_prs_all_closed(self):
        text, dropped = name_freezes.baseline_prune(self.TEXT, {7, 8})
        self.assertEqual(text, "# a header\ninternal/a core-any 3\n" + self.ID + " 2\n" + self.ID + " Landed #7\n" +
                         self.ID + " Waiting #8\n")
        self.assertEqual(dropped, [self.ID + " Closed"])

    def test_what_a_pr_adds_and_what_no_allowance_covers(self):
        # #1 adds two records to internal/seats (one of them the tree has already) and a literal of a legacy core;
        # #2 changes a package this tree declares; #3 adds a frozen import and a new one; #4's head is in the tree.
        count = "internal/legacy out-literal"
        reads = name_freezes.Trees(
            names={}, own={self.ID: (2, {"Seat", "Desk"})},
            counts={"head1": {self.ID: (3, {"Seat", "Desk", "Chair"}), count: (2, set())},
                    "mb1": {self.ID: (1, {"Seat"}), count: (1, set())},
                    "head3": {}, "mb3": {}},
            others={"head1": set(), "mb1": set(),
                    "head3": {"path-no-core\tinternal/hub imports internal/legacy",
                              "core-hook\tRULE core-hook internal/x has no rules_test.go"}, "mb3": set()})
        prs = [name_freezes.PR(n, False, "head%d" % n, NOW, {"server/"}) for n in (1, 2, 3, 4)]
        dirs = {"head1": ["internal/seats"], "head2": ["internal/legacy"], "head3": ["internal/hub"],
                "head4": ["internal/legacy"]}
        with tempfile.TemporaryDirectory() as root:
            edges = Path(root, name_freezes.EDGES)
            edges.parent.mkdir(parents=True)
            edges.write_text("# frozen\ninternal/hub imports internal/legacy\n")
            with mock.patch.multiple(name_freezes, ROOT=Path(root), fetch=lambda prs: None,
                                     merge_base=lambda head, base: "mb" + head[4:],
                                     changed_dirs=lambda old, new: dirs[new], in_tree=lambda head: head == "head4",
                                     declared_dirs=lambda revision: ["internal/legacy"],
                                     tree_files=lambda revision, dirs=None: {},
                                     domain_trees=lambda work, trees, own=False: reads):
                got = name_freezes.baseline_pending("base", prs, 14, "unused", log=io.StringIO())
        self.assertEqual(got.found, {self.ID: {"Chair": [1]}})
        self.assertEqual(got.failing, {
            1: ["adds 1 to internal/legacy out-literal, which takes no allowance"],
            2: ["changes internal/legacy, which this tree declares: check it against the rules by hand"],
            3: ["core-hook: RULE core-hook internal/x has no rules_test.go"]})

    def test_a_landed_baseline_is_tidied_only_on_a_checkout_of_the_base(self):
        with mock.patch.multiple(name_freezes, run=lambda args, timeout, **kw: "elsewhere\n" if args[-1] == "HEAD"
                                 else "base\n", central_names=lambda work, sources=None: ([], {}),
                                 on_base=lambda base, p: True), \
                mock.patch("sys.stdout", io.StringIO()), mock.patch("sys.stderr", io.StringIO()) as err:
            code = name_freezes.main(["pending", "--write", "--only", name_freezes.BASELINE.id])
        self.assertEqual(code, 1)
        self.assertIn("check out origin/main first", err.getvalue())

    def test_landed_domain_lists_are_left_out_of_a_run_that_does_not_name_them(self):
        # A seam's branch writes its new list after merging main, which has both domain lists: the run neither reads the
        # open PRs' trees for them nor asks for a checkout of the base, and says how to run each.
        def baseline_pending(*args, **kwargs):
            raise AssertionError("the open PRs' trees were read for a landed baseline")

        def tree_pending(*args, **kwargs):
            raise AssertionError("the open PRs' trees were read for a landed undeclared list")

        with tempfile.TemporaryDirectory() as root:
            undeclared = Path(root, name_freezes.UNDECLARED.file)
            undeclared.parent.mkdir(parents=True)
            undeclared.write_text("# a header\ninternal/old\n")
            with mock.patch.multiple(name_freezes, ROOT=Path(root), run=lambda args, timeout, **kw: "elsewhere\n"
                                     if args[-1] == "HEAD" else "base\n",
                                     central_names=lambda work, sources=None: ([], {}), on_base=lambda base, p: True,
                                     open_prs=lambda paths: [], tree_pending=tree_pending,
                                     baseline_pending=baseline_pending), \
                    mock.patch("sys.stdout", io.StringIO()), mock.patch("sys.stderr", io.StringIO()) as err:
                for argv in (["pending"], ["pending", "--write"]):
                    self.assertEqual(name_freezes.main(argv), 0, err.getvalue())
            self.assertEqual(undeclared.read_text(), "# a header\ninternal/old\n")
        for l, how in ((name_freezes.UNDECLARED, "prunes it"),
                       (name_freezes.BASELINE, "tidies it on a checkout of origin/main")):
            self.assertEqual(err.getvalue().count("%s: on origin/main already, so left out: `pending --write --only %s` "
                                                  "%s" % (l.id, l.id, how)), 2, err.getvalue())
        self.assertNotIn("check out", err.getvalue())

    def test_the_open_prs_trees_are_read_a_batch_at_a_time(self):
        # Each batch is read and removed before the next is written; the first read also takes the server's counts.
        calls = []

        def domain_trees(work, trees, own=False):
            calls.append((work, sorted(trees), own))
            Path(work).mkdir(parents=True)
            return name_freezes.Trees(names={}, counts={k: {} for k in trees}, others={k: set() for k in trees},
                                      own={self.ID: (2, set())} if own else None)

        prs = [name_freezes.PR(n, False, "head%d" % n, NOW, {"server/"}) for n in (1, 2, 3)]
        with tempfile.TemporaryDirectory() as root:
            edges = Path(root, name_freezes.EDGES)
            edges.parent.mkdir(parents=True)
            edges.write_text("# frozen\n")
            with mock.patch.multiple(name_freezes, ROOT=Path(root), BATCH=1, fetch=lambda prs: None,
                                     merge_base=lambda head, base: "mb" + head[4:],
                                     changed_dirs=lambda old, new: ["internal/seats"], in_tree=lambda head: False,
                                     declared_dirs=lambda revision: [], tree_files=lambda revision, dirs=None: {},
                                     domain_trees=domain_trees):
                got = name_freezes.baseline_pending("base", prs, 14, root, log=io.StringIO())
            left = [work for work, _, _ in calls if Path(work).exists()]
        self.assertEqual([(Path(w).name, keys, own) for w, keys, own in calls],
                         [("batch0", ["head1", "mb1"], True), ("batch1", ["head2", "mb2"], False),
                          ("batch2", ["head3", "mb3"], False)])
        self.assertEqual(left, [], "a batch's trees stay on disk")
        self.assertEqual(got.own, {self.ID: (2, set())})
        self.assertEqual((got.found, got.failing, got.measured), ({}, {}, {1, 2, 3}))

    def test_the_committed_baseline_reads_cleanly(self):
        text = (ROOT / name_freezes.BASELINE.file).read_text()
        lead, counts, allowances = name_freezes.baseline_entries(text)
        self.assertTrue(lead and lead[0].startswith("# "), "the baseline starts with what it is")
        self.assertGreater(len(counts), 100)
        self.assertEqual(name_freezes.compose_baseline(lead, counts, allowances), text,
                         "every line is a count or an allowance, sorted")
        rules = {key.split(" ")[1] for key, _ in allowances}
        self.assertLessEqual(rules, set(name_freezes.ALLOWABLE))


class MovesTests(unittest.TestCase):
    """`moves` runs TestDomainLayers with DOMAIN_LAYERS_MOVES and CI_BASE_SHA set to --base's commit, shows the lines
    the lists changed and ends with a verdict; a test still failing (a rise no move carries) exits 1."""

    def carry(self, returncode, output=""):
        with tempfile.TemporaryDirectory() as root:
            for path in (name_freezes.UNDECLARED.file, name_freezes.EDGES, name_freezes.BASELINE.file):
                Path(root, path).parent.mkdir(parents=True, exist_ok=True)
                Path(root, path).write_text("# list\ninternal/old\n")
            seen = {}

            def go(args, cwd, env, **kw):
                seen.update(args=args, cwd=cwd, base=env.get("CI_BASE_SHA"), moves=env.get("DOMAIN_LAYERS_MOVES"))
                Path(root, name_freezes.UNDECLARED.file).write_text("# list\ninternal/modules/old\n")
                return subprocess_result(returncode, output)

            err = io.StringIO()
            with mock.patch.multiple(name_freezes, ROOT=Path(root), run=lambda args, timeout, **kw: "basesha\n"), \
                    mock.patch.object(name_freezes.subprocess, "run", go), mock.patch("sys.stderr", err):
                code = name_freezes.main(["moves", "--base", "origin/main"])
            return code, err.getvalue(), seen

    def test_the_lists_carried_and_the_verdict(self):
        code, err, seen = self.carry(0)
        self.assertEqual(code, 0, err)
        self.assertEqual((seen["base"], seen["moves"]), ("basesha", "1"))
        self.assertIn("^TestDomainLayers$", seen["args"])
        self.assertIn("undeclared.txt: -internal/old", err)
        self.assertIn("undeclared.txt: +internal/modules/old", err)
        self.assertTrue(err.rstrip().endswith("moves: VERDICT ok: 2 list line(s) changed, and TestDomainLayers passes "
                                              "against origin/main"), err)

    def test_a_rise_no_move_carries_fails(self):
        code, err, _ = self.carry(1, "    domain_test.go:80: internal/x app-exported-record: 3 now together\n")
        self.assertEqual(code, 1)
        self.assertIn("internal/x app-exported-record: 3 now together", err)
        self.assertIn("moves: VERDICT failing", err)


def subprocess_result(returncode, stdout):
    """A finished process as subprocess.run returns it."""
    return name_freezes.subprocess.CompletedProcess([], returncode, stdout=stdout, stderr="")


@unittest.skipUnless(shutil.which("go"), "needs go")
class GoLayerNamesTests(unittest.TestCase):
    """The undeclared packages come from the archguard test itself, read as TestDomainLayers reads them."""

    def test_the_names_of_each_tree(self):
        with tempfile.TemporaryDirectory() as work:
            names = name_freezes.layer_names(work, {
                "one": {"internal/a/a.go": "// Package a is plain.\npackage a\n",
                        "internal/b/b.go": "// Package b is a library.\n//\n//persona:layer library\npackage b\n",
                        "internal/c/c.go": "// Package c has a typo.\n//\n//persona:layer cor\npackage c\n",
                        "internal/a/testdata/x.go": "package x\n"},
                "empty": {}})
        self.assertEqual(names, {"one": {"internal/a"}, "empty": set()})

    def test_the_counts_of_each_tree_and_of_the_server(self):
        with tempfile.TemporaryDirectory() as work:
            read = name_freezes.domain_trees(work, {
                "one": {"internal/a/a.go": "// Package a is plain.\npackage a\n\n// Seat is a record.\ntype Seat struct{ Name string }\n\n"
                                           "// Kind is a name.\ntype Kind string\n"}}, own=True)
        self.assertEqual(read.counts["one"], {"internal/a app-exported-record": (1, {"Seat"}),
                                              "internal/a app-named-primitive": (1, {"Kind"})})
        self.assertEqual(read.others["one"], set())
        self.assertGreater(len(read.own), 100)

    def test_the_files_of_a_directory_at_a_commit(self):
        files = name_freezes.tree_files("HEAD", ["internal/lazyre"])
        self.assertTrue(files)
        self.assertTrue(all(rel.startswith("internal/lazyre/") and rel.count("/") == 2 for rel in files), files)
        self.assertTrue(all(text.lstrip().startswith(("//", "package")) for text in files.values()))


@unittest.skipUnless(shutil.which("go"), "needs go")
class GoNamesTests(unittest.TestCase):
    """The names come from the archguard test itself, so they match what the freeze compares."""

    def test_the_freeze_reads_the_names_of_the_given_files(self):
        bus = (ROOT / "server/internal/assistantwire/bus.go").read_text()
        planted = bus.replace("var Routes = []string{\n", 'var Routes = []string{\n\t"DELETE /api/v1/planted",\n', 1)
        node = (ROOT / "server/internal/node/node.go").read_text()
        dispatch = "\tswitch method {\n"
        self.assertIn(dispatch, node)
        planted_node = node.replace(dispatch, dispatch + '\tcase "machine.planted", "session.planted":\n\t\treturn nil, nil\n', 1)
        with tempfile.TemporaryDirectory() as work:
            lists, names = name_freezes.central_names(work, {
                ("assistantwire_routes", "now"): bus, ("assistantwire_routes", "planted"): planted,
                ("assistantwire_routes", "broken"): "package x\nvar Routes = []string{\n",
                ("node_handle_method", "now"): node, ("node_handle_method", "planted"): planted_node})
        self.assertEqual([(l.id, l.source, l.file) for l in lists], [
            ("assistantwire_routes", "server/internal/assistantwire/bus.go",
             "server/internal/archguard/testdata/central_lists/assistantwire_routes.txt"),
            ("prompts_assistant", "server/internal/prompts/assistant.go",
             "server/internal/archguard/testdata/central_lists/prompts_assistant.txt"),
            ("extwire_route_scopes", "server/internal/extwire/scopes.go",
             "server/internal/archguard/testdata/central_lists/extwire_route_scopes.txt"),
            ("node_handle_method", "server/internal/node/node.go",
             "server/internal/archguard/testdata/central_lists/node_handle_method.txt"),
            ("proto_features", "server/internal/proto/proto.go",
             "server/internal/archguard/testdata/central_lists/proto_features.txt")])
        # A session-bound label stays in the switch, so only the other one is a name.
        self.assertEqual(names[("node_handle_method", "planted")] - names[("node_handle_method", "now")],
                         {'"machine.planted"'})
        listed = {e.name for e in name_freezes.CENTRAL.read((ROOT / lists[0].file).read_text())[0]}
        now = names[("assistantwire_routes", "now")]
        self.assertGreater(len(now), 100)
        self.assertLessEqual(now, listed)
        self.assertEqual(names[("assistantwire_routes", "planted")] - now, {'"DELETE /api/v1/planted"'})
        self.assertIsNone(names[("assistantwire_routes", "broken")])


if __name__ == "__main__":
    unittest.main()
