#!/usr/bin/env python3
"""scripts/new_domain_slice.py writes what the domain rules accept, and keeps writing it.

The golden, server/internal/archguard/testdata/scaffold, is a run with every flag but --shared (a shared kernel holds
no entity). Here it is written again and compared byte for byte; archguard's TestScaffoldGoldenPasses runs the domain
rules over it. Then each flag combination below is written into one temporary module that carries the server's module
path and copies of the domain kit, the rules and their lists, where `go vet` and `go test` run the generated tests and
each package's rules_test.go hook. The module copies what it needs rather than replacing the server: the rules find
the kit, the lists and the packages they guard by the module's own path, and Go lets only code under the server's path
import its internal packages. Last, a getter planted in a generated slice makes its `go test` fail with the rule's
teaching message, as it would for anyone who starts a package with the scaffold.

To write the golden again after changing the scaffold, run this with SCAFFOLD_GOLDEN_UPDATE=1 and read the diff.
"""
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
SERVER = ROOT / "server"
SCRIPT = ROOT / "scripts" / "new_domain_slice.py"
GOLDEN = SERVER / "internal" / "archguard" / "testdata" / "scaffold"
UPDATE = "SCAFFOLD_GOLDEN_UPDATE"

# The golden's command: every flag but --shared.
GOLDEN_ARGS = [
    "go", "internal/assistant/chore",
    "--doc", "decides what happens to one chore the owner keeps: its title, effort, snooze and priority, and whether "
             "it is closed",
    "--value", "Title=string", "--value", "Effort=int", "--value", "Snooze=int", "--zero-is", "no snooze",
    "--value", "Window=opens:instant.Instant,closes:instant.Instant",
    "--value", "HouseID=string", "--value", "RoomID=string", "--parse", "Spot=house:string,room:string",
    "--closed", "Priority=low,normal,urgent", "--closed", "Reminder=silent,ring(instant.Instant)",
    "--entity", "Chore", "--id", "ChoreID", "--builder", "--decider", "--adapter", "--act",
]

# The other combinations, each compiled, vetted and tested in the temporary module.
COMBINATIONS = {
    "shared kernel with an adapter": [
        "go", "internal/seatref", "--doc", "names one seat in one room: the shared kernel the tests use", "--shared",
        "--value", "RoomID=string", "--value", "SeatID=string", "--value", "Row=int",
        "--parse", "Seat=room:string,seat:string,row:int", "--closed", "Side=left,right", "--adapter"],
    "values and closed sets only": [
        "go", "internal/assistant/plainvalues", "--doc", "names what a search keeps",
        "--value", "Query=string", "--zero-is", "no text", "--value", "Limit=int", "--zero-is", "no limit",
        "--value", "Span=length:instant.Span,start:instant.Instant,place:slot.Slot",
        "--closed", "Mode=quick,deep", "--closed", "Pause=none,until(until:instant.Instant),for(instant.Span)"],
    "entity without a builder, its id a --value": [
        "go", "internal/assistant/plainentity", "--doc", "keeps one label", "--value", "LabelID=string",
        "--value", "Text=string", "--value", "Note=string", "--zero-is", "no note", "--closed", "Tone=calm,loud",
        "--entity", "Label", "--id", "LabelID", "--adapter"],
    "decider without a builder": [
        "go", "internal/assistant/plaindecider", "--doc", "decides whether one errand is closed",
        "--value", "Title=string", "--entity", "Errand", "--id", "ErrandID", "--decider", "--adapter", "--act"],
    "a value as the adapter's record": [
        "go", "internal/assistant/plaincount", "--doc", "counts one thing", "--value", "Count=int", "--adapter"],
    # The package's own values, parsed values, closed sets and id as parts and payloads, which its black-box tests
    # name qualified; and values whose zero is a real one, made of parts whose zero is.
    "the package's own types as parts and payloads": [
        "go", "internal/assistant/owntypes", "--doc", "keeps one note: what it says, where it is pinned and why it waits",
        "--value", "Title=string", "--value", "Snooze=int", "--zero-is", "no snooze",
        "--value", "HouseID=string", "--value", "RoomID=string", "--parse", "Spot=house:string,room:string",
        "--value", "Pair=at:Spot,title:Title", "--value", "Pin=note:NoteID,at:instant.Instant",
        "--value", "Gap=snooze:Snooze,place:slot.Slot",
        "--value", "Query=string", "--zero-is", "no text", "--value", "Count=int", "--zero-is", "none",
        "--parse", "Search=query:string,count:int",
        "--closed", "Reason=none,snoozed(Snooze),titled(Title),pinned(Pin)", "--closed", "Wait=plain,nested(Reason)",
        "--value", "Body=string", "--entity", "Note", "--id", "NoteID", "--adapter"],
    # The shared kernel's types as parts and payloads, as a context's asker or card is.
    "the shared kernel's types as parts and payloads": [
        "go", "internal/assistant/askers", "--doc", "says who asked for one card",
        "--value", "Card=machine:agentref.MachineID,card:agentref.InteractionID",
        "--closed", "Asker=nobody,agent(agentref.Ref)", "--value", "Topic=string", "--adapter"],
}

# The slice a getter is planted in: closed sets, a builder, a decider and an adapter, as a new context starts.
GETTER_ARGS = [
    "go", "internal/assistant/getter", "--doc", "decides what happens to one task",
    "--value", "Title=string", "--closed", "Size=small,large", "--entity", "Task", "--id", "TaskID",
    "--builder", "--decider", "--adapter"]
GETTER = '''package getter

// Title is the task's title: the getter a caller reaches for first.
func (x Task) Title() Title { return x.title }
'''


# The packages generated code imports, whose rules_test.go hooks also judge that code.
HOOKED = ["./internal/agentref/...", "./internal/domainkit/..."]


def scaffold(*args, root):
    return subprocess.run([sys.executable, "-B", str(SCRIPT), *args, "--root", str(root)], capture_output=True,
                          text=True, timeout=120)


def files_under(root):
    return {p.relative_to(root).as_posix(): p.read_bytes() for p in sorted(root.rglob("*")) if p.is_file()}


class GenerateTests(unittest.TestCase):
    @unittest.skipUnless(shutil.which("gofmt") or shutil.which("go"), "needs gofmt")
    def test_the_golden_is_what_the_scaffold_writes(self):
        with tempfile.TemporaryDirectory() as work:
            done = scaffold(*GOLDEN_ARGS, root=work)
            self.assertEqual(done.returncode, 0, done.stderr)
            got = files_under(Path(work))
            if os.environ.get(UPDATE):
                shutil.rmtree(GOLDEN, ignore_errors=True)
                for rel, data in got.items():
                    (GOLDEN / rel).parent.mkdir(parents=True, exist_ok=True)
                    (GOLDEN / rel).write_bytes(data)
            want = files_under(GOLDEN)
        self.assertEqual(sorted(got), sorted(want), f"the golden's files differ; rerun with {UPDATE}=1")
        for rel in want:
            self.assertEqual(got[rel].decode(), want[rel].decode(), f"{rel} differs; rerun with {UPDATE}=1")

    @unittest.skipUnless(shutil.which("gofmt") or shutil.which("go"), "needs gofmt")
    def test_it_refuses_a_directory_that_exists(self):
        with tempfile.TemporaryDirectory() as work:
            first = scaffold("go", "internal/assistant/once", "--doc", "is written once", "--value", "Name=string",
                             root=work)
            self.assertEqual(first.returncode, 0, first.stderr)
            before = files_under(Path(work))
            second = scaffold("go", "internal/assistant/once", "--doc", "is written again", "--value", "Kind=string",
                              root=work)
            self.assertEqual(second.returncode, 2)
            self.assertIn("exists: the scaffold starts a package, it never writes over one", second.stderr)
            self.assertEqual(files_under(Path(work)), before)

    def test_it_refuses_what_the_rules_would(self):
        cases = {
            ("--shared", "--entity", "Seat", "--id", "SeatID"): "a shared kernel holds values and closed sets only",
            ("--value", "Name=string", "--act"): "--act acts on a decider's decisions",
            ("--value", "Name=string", "--builder"): "--builder and --decider build an entity",
            ("--value", "Name=string", "--zero-is", "x", "--parse", "Pair=name:string,other:string"):
                "needs a --value of string whose field is other",
            ("--value", "A=string", "--value", "B=string", "--value", "C=string", "--value", "D=string",
             "--entity", "Box", "--id", "BoxID"): "takes four at most: add --builder",
            ("--closed", "Fail=a,b"): "the scaffold declares that name itself",
            ("--value", "NoteID=string", "--zero-is", "x", "--entity", "Note", "--id", "NoteID"): "takes no --zero-is",
            ("--value", "Pair=left:Pair"): "its parts hold Pair again",
            ("--value", "A=left:B", "--value", "B=right:A"): "its parts hold",
            ("--value", "Known=string", "--entity", "Box", "--id", "BoxID"): "the scaffold declares that name itself",
            ("--value", "Tag=string", "--value", "Pair=tag:Tag", "--entity", "Box", "--id", "BoxID"):
                "holds only its id, which makes it a value",
        }
        with tempfile.TemporaryDirectory() as work:
            for flags, says in cases.items():
                dir_ = "internal/refused" if "--shared" in flags else "internal/assistant/refused"
                done = scaffold("go", dir_, "--doc", "is refused", *flags, root=work)
                self.assertEqual(done.returncode, 2, (flags, done.stdout))
                self.assertIn(says, done.stderr, flags)
            self.assertEqual(list(Path(work).iterdir()), [])

    def test_the_other_platforms_say_when_they_arrive(self):
        for platform, item in (("swift", "6.5"), ("kotlin", "6.11"), ("web", "6.1")):
            done = subprocess.run([sys.executable, "-B", str(SCRIPT), platform, "Inbox"], capture_output=True,
                                  text=True, timeout=60)
            self.assertEqual(done.returncode, 2)
            self.assertIn(f"arrives with the first {platform} domain module (#10613, item {item})", done.stderr)
        helped = subprocess.run([sys.executable, "-B", str(SCRIPT), "--help"], capture_output=True, text=True,
                                timeout=60)
        self.assertEqual(helped.returncode, 0)
        self.assertIn("The Swift, Kotlin and web flavours", helped.stdout)


def go_env():
    env = dict(os.environ, GOWORK="off")
    flags = env.get("GOFLAGS", "").split()
    if "-trimpath" not in flags:
        flags.append("-trimpath")
    env["GOFLAGS"] = " ".join(flags)
    return env


def go(module, *args, timeout=600):
    return subprocess.run(["go", *args], cwd=module, env=go_env(), capture_output=True, text=True, timeout=timeout)


def copy_package(src, dst, tests=False):
    """A package's Go files, without its tests unless asked (its rules_test.go hook always comes along)."""
    dst.mkdir(parents=True, exist_ok=True)
    for f in sorted(src.glob("*.go")):
        if tests or not f.name.endswith("_test.go") or f.name == "rules_test.go":
            shutil.copy2(f, dst / f.name)


@unittest.skipUnless(shutil.which("go"), "needs go")
class CompileTests(unittest.TestCase):
    """Every combination in one temporary module: compiled, vetted, tested, and held to the rules by its hooks."""

    @classmethod
    def setUpClass(cls):
        cls.work = tempfile.TemporaryDirectory(prefix="domain-slice-")
        module = cls.module = Path(cls.work.name)
        version = next(line for line in (SERVER / "go.mod").read_text().splitlines() if line.startswith("go "))
        (module / "go.mod").write_text(f"module github.com/nwverse/persona/server\n\n{version}\n")
        for pkg in sorted({f.parent for f in (SERVER / "internal" / "domainkit").rglob("*.go")}):
            copy_package(pkg, module / pkg.relative_to(SERVER))
        copy_package(SERVER / "internal" / "agentref", module / "internal" / "agentref")
        rules = SERVER / "internal" / "archguard"
        copy_package(rules / "domainrules", module / "internal" / "archguard" / "domainrules")
        lists = module / "internal" / "archguard" / "testdata"
        (lists / "domain_layers").mkdir(parents=True)
        for name in ("contracts.txt", "domain_layers/undeclared.txt", "domain_layers/legacy_edges.txt",
                     "domain_layers/baseline.txt"):
            shutil.copy2(rules / "testdata" / name, lists / name)
        cls.dirs = []
        for name, args in [("golden", GOLDEN_ARGS), *COMBINATIONS.items(), ("getter", GETTER_ARGS)]:
            done = scaffold(*args, root=module)
            if done.returncode != 0:
                raise AssertionError(f"{name}: {done.stderr}")
            if name != "getter":
                cls.dirs.append("./" + args[1] + "/...")
        (module / "internal" / "assistant" / "getter" / "getter.go").write_text(GETTER)

    @classmethod
    def tearDownClass(cls):
        cls.work.cleanup()

    def test_every_combination_is_formatted(self):
        listed = subprocess.run(["gofmt", "-l", "internal"], cwd=self.module, capture_output=True, text=True,
                                timeout=120)
        self.assertEqual(listed.returncode, 0, listed.stderr)
        self.assertEqual(listed.stdout.split(), [])

    def test_every_combination_vets_and_passes_its_tests_and_rules(self):
        vet = go(self.module, "vet", *self.dirs)
        self.assertEqual(vet.returncode, 0, vet.stdout + vet.stderr)
        # The kit's and agentref's hooks run too: a finding in generated code counts against the package whose type
        # it uses (an agentref.Ref payload's test visitor against agentref), and only that package's hook reports it.
        tested = go(self.module, "test", "-p", "4", "-count=1", *self.dirs, *HOOKED)
        self.assertEqual(tested.returncode, 0, tested.stdout + tested.stderr)
        for d in self.dirs + HOOKED:
            self.assertIn(d[2:-4], tested.stdout)

    def test_a_planted_getter_fails_its_package_test_with_the_rules_message(self):
        tested = go(self.module, "test", "-count=1", "-run", "TestDomainRules", "./internal/assistant/getter/")
        self.assertNotEqual(tested.returncode, 0, tested.stdout)
        self.assertIn("RULE core-accessor Task.Title() hands state out", tested.stdout)
        self.assertIn('see docs/server/architecture.md "Domain model"', tested.stdout)


if __name__ == "__main__":
    unittest.main()
