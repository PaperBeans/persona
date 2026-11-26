"""The Persona Core block of .github/CODEOWNERS is generated from schema/core-paths.txt (#14916): it is current, it owns
every core file, and it leaves every module file without an owner. Run: python3 scripts/test/test_codeowners.py"""
from pathlib import Path
import re
import sys
import unittest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts" / "hooks"))
sys.path.insert(0, str(ROOT / "scripts" / "test"))
import codeowners as co  # noqa: E402
import core_paths as cp  # noqa: E402


def owned(path, patterns):
    """Whether the last matching pattern gives path an owner (CODEOWNERS: `/dir/`, `/dir/*/`, `/file`)."""
    result = False
    for pat, own in patterns:
        pat = pat.lstrip("/")
        if pat.endswith("/"):
            rx = re.escape(pat.rstrip("/")).replace(r"\*", "[^/]+") + "/"
            hit = re.match(rx, path) is not None
        else:
            hit = path == pat
        if hit:
            result = own
    return result


class CodeownersTests(unittest.TestCase):
    def test_generated_block_is_current(self):
        text = (ROOT / ".github" / "CODEOWNERS").read_text(encoding="utf-8")
        self.assertEqual(co.extract(text), co.block(),
                         "the CODEOWNERS core block is out of date: run python3 scripts/hooks/codeowners.py --write")

    def test_super_rules_line_is_kept(self):
        self.assertIn("/docs/SUPER_RULES.md @chyroc", (ROOT / ".github" / "CODEOWNERS").read_text(encoding="utf-8").splitlines())

    def test_owners_are_the_two_named(self):
        for line in co.extract((ROOT / ".github" / "CODEOWNERS").read_text(encoding="utf-8")).splitlines():
            if line and not line.startswith("#") and len(line.split()) > 1:
                self.assertEqual(line.split()[1:], ["@chyroc", "@szupzj18"], line)

    def test_block_owns_core_and_not_modules(self):
        text = (ROOT / ".github" / "CODEOWNERS").read_text(encoding="utf-8")
        patterns = []
        for line in co.extract(text).splitlines():
            if line and not line.startswith("#"):
                patterns.append((line.split()[0], len(line.split()) > 1))
        c = cp.Core()
        for f in cp.tracked():
            cls = c.classification(f)
            if c.is_core(f, tests_count=True):
                self.assertTrue(owned(f, patterns), f"core file {f} has no owner")
            elif cls not in (None, "core"):
                self.assertFalse(owned(f, patterns), f"module file {f} ({cls}) has an owner")
            elif c.is_panel(f):
                self.assertFalse(owned(f, patterns), f"panel file {f} has an owner from the generated block")

    def test_samples(self):
        patterns = [(l.split()[0], len(l.split()) > 1) for l in co.block().splitlines() if l and not l.startswith("#")]
        for f in ("server/internal/hub/hub.go", "android/core/hub/src/main/kotlin/persona/core/hub/Hub.kt",
                  "apple/Packages/PersonaWire/Package.swift", "web/src/core/module-host.ts"):
            self.assertTrue(owned(f, patterns), f)
        for f in ("web/src/views/chat/ChatView.tsx", "android/feature/agents/src/main/kotlin/A.kt", "apple/Persona/Views/Chat/ChatView.swift",
                  "android/app/src/main/kotlin/A.kt", "web/src/modules/lark/module.ts", "docs/architecture/core-and-modules.md"):
            self.assertFalse(owned(f, patterns), f)

    def test_no_wildcard_clears_owners(self):
        # The block names each directory it un-owns; a `dir/*/` catch-all would also strip a sibling nobody listed.
        for line in co.block().splitlines():
            if line and not line.startswith("#"):
                self.assertNotIn("*", line, line)

    def test_unlisted_sibling_under_an_owned_directory_keeps_the_owner(self):
        # Under an owned parent: three non-core directories (a module, the panel, one the tiers do not cover) and a core one.
        tiers = {"p/core1": "core", "p/core2": "core", "p/mod": "module", "p/ui": "panel", "p/misc": "other"}
        files = [f"{d}/f{i}.x" for d in tiers for i in range(40 if tiers[d] == "core" else 4)]  # p/ is 91% core: owned whole
        block = co.block(files, tier=lambda f: tiers[f.rsplit("/", 1)[0]])
        patterns = [(l.split()[0], len(l.split()) > 1) for l in block.splitlines() if l and not l.startswith("#")]
        self.assertTrue(owned("p/core2/f1.x", patterns))
        for d in ("mod", "ui", "misc"):
            self.assertFalse(owned(f"p/{d}/f1.x", patterns), d)  # listed non-core directories lose the generated owner
        self.assertTrue(owned("p/other-new/f.x", patterns))  # a directory the tree never listed is not stripped
        self.assertNotIn("/p/*/", block)

    def test_few_patterns(self):
        self.assertLess(len(co.block().splitlines()), 250)

    def test_replace_keeps_the_rest(self):
        old = "/docs/SUPER_RULES.md @chyroc\n\n" + co.BEGIN + "\n/x/ @a\n" + co.END + "\n# tail\n"
        new = co.replace(old, co.BEGIN + "\n/y/ @a\n" + co.END + "\n")
        self.assertEqual(new, "/docs/SUPER_RULES.md @chyroc\n\n" + co.BEGIN + "\n/y/ @a\n" + co.END + "\n# tail\n")
        self.assertEqual(co.replace("/docs/SUPER_RULES.md @chyroc\n", "B\n"), "/docs/SUPER_RULES.md @chyroc\n\nB\n")


if __name__ == "__main__":
    unittest.main()
