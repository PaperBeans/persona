"""The wire types ratchet: a new weak name fails, a move between packages is neutral, a fall passes, --update only lowers.

The cases give the ratchet a counter of their own, so they need no Go; one case runs the real Go counter over a
throwaway tree when Go is on PATH, and one checks this checkout's committed baseline parses.
"""
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import wire_types_ratchet as w

ROOT = Path(__file__).resolve().parents[2]


def baseline(metrics, exceptions=None, wide=None):
    data = {"metrics": metrics}
    if wide:
        data["wide"] = wide
    if exceptions:
        data["exceptions"] = exceptions
    return json.dumps(data)


class RatchetTests(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.root = Path(self.dir.name)
        (self.root / "scripts/ci").mkdir(parents=True)
        self.addCleanup(self.dir.cleanup)

    def write(self, metrics, exceptions=None, wide=None):
        (self.root / w.BASELINE_REL).write_text(baseline(metrics, exceptions, wide))

    def counter(self, counts, wide=None):
        # The wide counts cover the contract packages too, so by default they are at least the contract counts.
        if wide is None:
            wide = {m: dict(names) for m, names in counts.items() if m in w.FIELD_METRICS}
        return lambda root: {"metrics": counts, "wide": wide}

    def test_a_field_lifted_from_a_core_package_is_neutral(self):
        # #15022: recording types moved from internal/kb (not a contract) into recordingwire, kb kept aliases.
        self.write({"bare-id": {"A.ID": 1}}, wide={"bare-id": {"A.ID": 1, "Recording.MachineID": 1}})
        counter = self.counter({"bare-id": {"A.ID": 1, "Recording.MachineID": 1}},
                               wide={"bare-id": {"A.ID": 1, "Recording.MachineID": 1}})
        self.assertEqual([], w.check(self.root, counter=counter))
        self.assertEqual([], w.update(self.root, counter=counter))
        metrics, _, wide = w.parse_baseline((self.root / w.BASELINE_REL).read_text())
        self.assertEqual({"bare-id": {"A.ID": 1}}, metrics)  # not added; its wide entry keeps letting it through
        self.assertEqual({"bare-id": {"A.ID": 1, "Recording.MachineID": 1}}, wide)

    def test_a_field_moved_back_from_a_module_is_neutral(self):
        self.write({}, wide={"raw-json": {"Photo.Meta": 1}})
        counter = self.counter({"raw-json": {"Photo.Meta": 1}}, wide={"raw-json": {"Photo.Meta": 1}})
        self.assertEqual([], w.check(self.root, counter=counter))

    def test_a_lift_that_also_keeps_a_copy_fails(self):
        # Copying the field instead of moving it raises its wide count: that is a new weak field.
        self.write({}, wide={"bare-id": {"Recording.MachineID": 1}})
        counter = self.counter({"bare-id": {"Recording.MachineID": 1}}, wide={"bare-id": {"Recording.MachineID": 2}})
        self.assertIn("Recording.MachineID (new)", w.check(self.root, counter=counter)[0])

    def test_a_new_field_in_a_core_package_alone_is_not_checked(self):
        # Outside the contract packages the wide count only serves lifts; it is not a limit.
        self.write({"bare-id": {"A.ID": 1}}, wide={"bare-id": {"A.ID": 1}})
        counter = self.counter({"bare-id": {"A.ID": 1}}, wide={"bare-id": {"A.ID": 1, "Helper.OwnerID": 1}})
        self.assertEqual([], w.check(self.root, counter=counter))

    def test_update_lowers_wide_and_never_adds_to_it(self):
        self.write({}, wide={"bare-id": {"A.ID": 2, "B.ID": 1}})
        counter = self.counter({}, wide={"bare-id": {"A.ID": 1, "C.ID": 1}})
        self.assertEqual([], w.update(self.root, counter=counter))
        self.assertEqual({"bare-id": {"A.ID": 1}}, w.parse_baseline((self.root / w.BASELINE_REL).read_text())[2])

    def test_the_wide_counts_may_not_rise_against_the_previous_baseline(self):
        found = w.raised_against({}, {}, "base", {"bare-id": {"A.ID": 2}}, {"bare-id": {"A.ID": 1}})
        self.assertIn("wide bare-id A.ID is 2 in the baseline but 1", found[0])

    def test_the_same_counts_pass(self):
        self.write({"bare-enum-state": {"Session.State": 1}})
        self.assertEqual([], w.check(self.root, counter=self.counter({"bare-enum-state": {"Session.State": 1}})))

    def test_a_new_name_fails_and_says_how_to_fix_it(self):
        self.write({"bare-enum-kind": {"Session.Kind": 1}})
        found = w.check(self.root, counter=self.counter({"bare-enum-kind": {"Session.Kind": 1, "NewThing.Kind": 1}}))
        self.assertEqual(1, len(found))
        self.assertIn("NewThing.Kind (new)", found[0])
        self.assertIn("closed set", found[0])
        self.assertIn("no ignore marker", found[0])

    def test_a_name_that_occurs_more_often_fails(self):
        self.write({"bare-id": {"Item.MachineID": 1}})
        found = w.check(self.root, counter=self.counter({"bare-id": {"Item.MachineID": 2}}))
        self.assertIn("Item.MachineID (1 -> 2)", found[0])

    def test_a_type_moved_between_packages_is_neutral(self):
        # The counter names fields "<Type>.<Field>" with no package, so the same type in another package is the same name.
        self.write({"raw-json": {"Recording.Segments": 1}})
        self.assertEqual([], w.check(self.root, counter=self.counter({"raw-json": {"Recording.Segments": 1}})))

    def test_a_fall_passes_with_a_notice(self):
        self.write({"map-any": {"A.Extra": 1, "B.Extra": 1}})
        notes = []
        found = w.check(self.root, counter=self.counter({"map-any": {"A.Extra": 1}}), notes=notes)
        self.assertEqual([], found)
        self.assertEqual(["map-any B.Extra: 1 -> 0"], notes)

    def test_an_exception_with_a_pr_raises_the_allowance(self):
        self.write({}, {"bare-id": {"Item.OwnerID": {"count": 1, "pr": "#1", "why": "legacy wire"}}})
        self.assertEqual([], w.check(self.root, counter=self.counter({"bare-id": {"Item.OwnerID": 1}})))

    def test_an_exception_that_adds_room_is_refused_against_the_base(self):
        # #15561: new weak fields let in by exceptions alone, nothing removed.
        found = w.exceptions_grow({"bare-id": {"A.ID": 1}}, {"bare-id": {"New.OwnerID": 1}}, {"bare-id": {"A.ID": 1}}, {},
                                  "base")
        self.assertEqual(1, len(found))
        self.assertIn("New.OwnerID add room (1 -> 2", found[0])

    def test_an_exception_for_a_rename_is_allowed(self):
        # --update lowered the old name; the new one is an exception: the allowance does not grow.
        self.assertEqual([], w.exceptions_grow({}, {"bare-id": {"Item.OwnerID": 1}}, {"bare-id": {"Item.HolderID": 1}}, {},
                                               "base"))

    def test_an_existing_exception_is_not_reported_again(self):
        exc = {"bare-id": {"Old.ID": 1}}
        self.assertEqual([], w.exceptions_grow({"bare-id": {"A.ID": 1}}, exc, {"bare-id": {"A.ID": 1}}, exc, "base"))

    def test_an_exception_without_a_pr_is_refused(self):
        self.write({}, {"bare-id": {"Item.OwnerID": {"count": 1, "why": "legacy wire"}}})
        with self.assertRaises(w.WireTypesError):
            w.check(self.root, counter=self.counter({}))

    def test_a_missing_baseline_is_not_invented(self):
        self.assertIn("baseline missing", w.check(self.root, counter=self.counter({}))[0])
        with self.assertRaises(w.WireTypesError):
            w.update(self.root, counter=self.counter({}))

    def test_update_lowers_and_drops_gone_names(self):
        self.write({"bare-id": {"A.ID": 2, "B.ID": 1}, "map-any": {"C.X": 1}})
        self.assertEqual([], w.update(self.root, counter=self.counter({"bare-id": {"A.ID": 1}})))
        metrics = w.parse_baseline((self.root / w.BASELINE_REL).read_text())[0]
        self.assertEqual({"bare-id": {"A.ID": 1}}, metrics)

    def test_update_refuses_to_raise_or_add(self):
        self.write({"bare-id": {"A.ID": 1}})
        before = (self.root / w.BASELINE_REL).read_text()
        refusals = w.update(self.root, counter=self.counter({"bare-id": {"A.ID": 1, "B.ID": 1}}))
        self.assertIn("B.ID (new)", refusals[0])
        self.assertEqual(before, (self.root / w.BASELINE_REL).read_text())

    def test_the_baseline_may_not_rise_against_the_base(self):
        subprocess.run(["git", "init", "-q", str(self.root)], check=True)
        subprocess.run(["git", "-C", str(self.root), "config", "user.email", "t@example.com"], check=True)
        subprocess.run(["git", "-C", str(self.root), "config", "user.name", "t"], check=True)
        self.write({"bare-id": {"A.ID": 1}})
        subprocess.run(["git", "-C", str(self.root), "add", "-A"], check=True)
        subprocess.run(["git", "-C", str(self.root), "commit", "-qm", "base"], check=True)
        base = subprocess.run(["git", "-C", str(self.root), "rev-parse", "HEAD"], capture_output=True, text=True,
                              check=True).stdout.strip()
        self.write({"bare-id": {"A.ID": 1, "B.ID": 1}})
        counter = self.counter({"bare-id": {"A.ID": 1, "B.ID": 1}})
        found = w.check(self.root, base=base, counter=counter)
        self.assertEqual(1, len(found))
        self.assertIn("B.ID is 1 in the baseline but 0", found[0])
        self.assertEqual([], w.check(self.root, counter=counter))  # no base: the baseline alone holds


@unittest.skipUnless(shutil.which("go"), "go is not on PATH")
class CounterTests(unittest.TestCase):
    def test_the_go_counter_names_fields_without_their_package(self):
        with tempfile.TemporaryDirectory() as tmp:
            server = Path(tmp) / "server"
            for name in ("hub", "assistant", "devices", "extmgr"):
                (server / "internal" / name).mkdir(parents=True)
            for name in ("kbwire", "recordingwire"):
                (server / "internal" / name).mkdir(parents=True)
            (server / "internal/archguard/testdata").mkdir(parents=True)
            (server / "internal/archguard/testdata/contracts.txt").write_text("# c\ninternal/kbwire\ninternal/recordingwire\n")
            src = "package {0}\ntype Clip struct {{ State string }}\n"
            (server / "internal/kbwire/a.go").write_text(src.format("kbwire"))
            before = w.run_counter_in(server)
            (server / "internal/kbwire/a.go").unlink()
            (server / "internal/recordingwire/a.go").write_text(src.format("recordingwire"))
            after = w.run_counter_in(server)
        self.assertEqual({"bare-enum-state": {"Clip.State": 1}}, before["metrics"])
        self.assertEqual(before, after)

    def test_the_go_counter_counts_core_packages_and_modules_wide(self):
        with tempfile.TemporaryDirectory() as tmp:
            server = Path(tmp) / "server"
            for name in ("hub", "assistant", "devices", "extmgr", "kb", "recordingwire", "modules/recording"):
                (server / "internal" / name).mkdir(parents=True)
            (server / "internal/archguard/testdata").mkdir(parents=True)
            (server / "internal/archguard/testdata/contracts.txt").write_text("internal/recordingwire\n")
            (server / "internal/kb/a.go").write_text("package kb\ntype Clip struct { MachineID string }\n")
            (server / "internal/modules/recording/a.go").write_text("package recording\ntype Pic struct { ID string }\n")
            (server / "internal/kb/a_test.go").write_text("package kb\ntype T struct { OwnerID string }\n")
            before = w.run_counter_in(server)
            (server / "internal/kb/a.go").write_text("package kb\nimport \"x/recordingwire\"\ntype Clip = recordingwire.Clip\n")
            (server / "internal/recordingwire/a.go").write_text("package recordingwire\ntype Clip struct { MachineID string }\n")
            after = w.run_counter_in(server)
        self.assertEqual({}, before["metrics"])
        self.assertEqual({"bare-id": {"Clip.MachineID": 1, "Pic.ID": 1}}, before["wide"])
        self.assertEqual({"bare-id": {"Clip.MachineID": 1}}, after["metrics"])
        self.assertEqual(before["wide"], after["wide"])  # the alias is not a struct: the lift keeps the wide count


@unittest.skipUnless(shutil.which("go") and shutil.which("git"), "go or git is not on PATH")
class MergeGateTests(unittest.TestCase):
    """scripts/ci/merge_wire_types.py on a real repository: the merge result is counted with the real Go counter."""

    def setUp(self):
        import merge_wire_types
        self.m = merge_wire_types
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        self.root = Path(self.dir.name) / "repo"
        server = self.root / "server/internal"
        for name in ("hub", "assistant", "devices", "extmgr", "proto"):
            (server / name).mkdir(parents=True)
            (server / name / "doc.go").write_text("package " + name + "\n")  # git keeps no empty directory
        (server / "archguard/testdata").mkdir(parents=True)
        (server / "archguard/testdata/contracts.txt").write_text("internal/proto\n")
        (server / "proto/a.go").write_text("package proto\ntype Session struct { MachineID string }\n")
        (self.root / "scripts/ci").mkdir(parents=True)
        (self.root / w.BASELINE_REL).write_text(baseline({"bare-id": {"Session.MachineID": 1}},
                                                         wide={"bare-id": {"Session.MachineID": 1}}))
        (self.root / "README.md").write_text("x\n")
        self.git("init", "-q", "-b", "main")
        self.commit("base")
        self.base = self.git("rev-parse", "HEAD")

    def git(self, *args):
        return subprocess.run(["git", "-C", str(self.root), "-c", "user.name=t", "-c", "user.email=t@example.com", *args],
                              capture_output=True, text=True, check=True).stdout.strip()

    def commit(self, message):
        self.git("add", "-A")
        self.git("commit", "-qm", message)

    def branch(self, name):
        self.git("checkout", "-q", "-b", name, self.base)

    def run_gate(self):
        notes = []
        lines = self.m.check(self.root, self.base, self.git("rev-parse", "HEAD"), out=notes.append)
        self.git("checkout", "-q", "main")
        return lines, notes

    def test_a_new_weak_field_is_refused(self):
        self.branch("weak")
        (self.root / "server/internal/proto/a.go").write_text(
            "package proto\ntype Session struct { MachineID string; OwnerSessionID string }\n")
        self.commit("weak")
        lines, _ = self.run_gate()
        self.assertEqual(1, len(lines))
        self.assertIn("Session.OwnerSessionID (new)", lines[0])

    def test_a_typed_field_merges(self):
        self.branch("typed")
        (self.root / "server/internal/proto/a.go").write_text(
            "package proto\ntype SessionID string\ntype Session struct { MachineID string; Owner SessionID }\n")
        self.commit("typed")
        self.assertEqual([], self.run_gate()[0])

    def test_an_exception_that_adds_room_is_refused(self):
        self.branch("exception")
        (self.root / "server/internal/proto/a.go").write_text(
            "package proto\ntype Session struct { MachineID string; OwnerSessionID string }\n")
        (self.root / w.BASELINE_REL).write_text(baseline(
            {"bare-id": {"Session.MachineID": 1}}, wide={"bare-id": {"Session.MachineID": 1}},
            exceptions={"bare-id": {"Session.OwnerSessionID": {"count": 1, "pr": "#1", "why": "x"}}}))
        self.commit("exception")
        lines, _ = self.run_gate()
        self.assertTrue(any("add room" in line for line in lines), lines)

    def test_a_failure_already_on_the_base_is_not_the_prs(self):
        (self.root / "server/internal/proto/a.go").write_text(
            "package proto\ntype Session struct { MachineID string; StaleID string }\n")
        self.git("checkout", "-q", "main")
        self.commit("main went red")
        self.base = self.git("rev-parse", "HEAD")
        self.branch("other")
        (self.root / "server/internal/proto/b.go").write_text("package proto\ntype Other struct{ N int }\n")
        self.commit("unrelated go change")
        lines, notes = self.run_gate()
        self.assertEqual([], lines)
        self.assertTrue(any("Session.StaleID already fails on the base" in n for n in notes), notes)

    def test_a_pr_without_go_changes_is_not_counted(self):
        self.branch("docs")
        (self.root / "README.md").write_text("y\n")
        self.commit("docs")
        lines, notes = self.run_gate()
        self.assertEqual([], lines)
        self.assertIn("not counted", notes[0])


class CommittedBaselineTests(unittest.TestCase):
    def test_the_committed_baseline_parses(self):
        metrics, _, wide = w.parse_baseline((ROOT / w.BASELINE_REL).read_text(encoding="utf-8"))
        self.assertTrue(metrics["bare-id"])
        for metric, names in metrics.items():
            if metric in w.FIELD_METRICS:
                for name, count in names.items():
                    self.assertLessEqual(count, wide.get(metric, {}).get(name, 0), metric + " " + name)


if __name__ == "__main__":
    unittest.main()
