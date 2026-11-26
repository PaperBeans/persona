"""Persona Core does not reference a feature module, checked with no toolchain on every platform (#14916, part of #13430;
docs/architecture/core-and-modules.md "Guards"). The platform guards stay the precise ones (server: archguard
TestCoreImportsNoModule; web: the ESLint boundary; Android: ./gradlew :app:moduleGuard; Apple: test_apple_module_refs.py).
This is one cheap check that runs wherever Python does (scripts/ci/scripts.sh, scripts/hooks/merge-pr.sh's checkout):

  1. each guard's baseline of known violations is still empty;
  2. no tier-core or tier-panel Go, web or Kotlin file has an import line that names a module (the Apple ratchet's own
     scan covers Swift). Tier panel is the clients' interface code: it is scanned like the core under the same rule.

Run: python3 scripts/test/test_core_references_no_module.py   (--list prints each violation)
"""
import json
from pathlib import Path
import re
import sys
import unittest

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
sys.path.insert(0, str(HERE))
import core_paths as cp  # noqa: E402
import test_apple_module_refs as apple  # noqa: E402

ROOTS_FILE = "server/internal/archguard/testdata/module_roots.txt"
BASELINES = {
    "server": ("server/internal/archguard/testdata/module_imports_baseline.txt", "lines"),
    "web": ("scripts/ci/web_module_boundary_baseline.json", "json"),
    "android": ("android/module-guard-baseline.txt", "lines"),
    "apple": ("scripts/test/apple_module_refs_baseline.json", "json"),
}
GO_IMPORT = re.compile(r'"github\.com/[^"/]+/[^"/]+/server/internal/modules/([^"]+)"')
# The module list (src/modules/index.ts, standalone.ts) is the way in; this is web/eslint.modules.config.mjs' INTO_A_MODULE.
WEB_IMPORT = re.compile(r"""(?:from\s*|import\s*\(?\s*|require\(\s*)(['"`])((?:[^'"`]*/)?modules/(?!(?:index|standalone)\1)[^/'"`]+[^'"`]*)\1""")
KOTLIN_IMPORT = re.compile(r"^\s*import\s+persona\.module\.", re.M)
KOTLIN_ALLOWED = {"android/app/src/main/java/persona/android/Modules.kt"}  # the app's module list


def baseline_problems(root=ROOT):
    """Each guard baseline that is not empty."""
    out = []
    for platform, (rel, kind) in BASELINES.items():
        text = (root / rel).read_text(encoding="utf-8")
        if kind == "json":
            known = json.loads(text)
        else:
            known = [l for l in text.splitlines() if l.strip() and not l.lstrip().startswith("#")]
        if known:
            out.append(f"{platform}: {rel} is not empty ({len(known)} known violation(s)); core references no module, "
                       "so the baseline stays empty: fix the reference instead of listing it")
    return out


def composition_roots(root=ROOT):
    text = (root / ROOTS_FILE).read_text(encoding="utf-8")
    return {l.strip() for l in text.splitlines() if l.strip() and not l.lstrip().startswith("#")}


def go_violations(path, text, roots):
    """Import lines of a core Go file (path from the repository root) that name a module."""
    if not path.endswith(".go") or path.startswith("server/internal/modules/"):
        return []
    pkg = path[len("server/"):].rsplit("/", 1)[0] if path.startswith("server/") else ""
    out = []
    for n, line in enumerate(text.splitlines(), 1):
        m = GO_IMPORT.search(line)
        if not m:
            continue
        target = m.group(1)
        if pkg in roots and target in ("all", "all/node"):
            continue  # a composition root imports the list of modules
        if path.endswith("_test.go") and (target in ("all", "all/node") or re.fullmatch(r"([a-z0-9-]+)/\1test", target)):
            continue  # a core test runs as the manager does, or against a module's stand-in
        out.append(f"{path}:{n}: imports internal/modules/{target}")
    return out


def web_violations(path, text):
    """Import specifiers of a core web file that reach into a module (tests may; the module list is the way in)."""
    if not path.endswith((".ts", ".tsx", ".mts", ".js", ".jsx", ".mjs")) or re.search(r"\.(test|spec)\.", path):
        return []
    return [f"{path}:{text.count(chr(10), 0, m.start()) + 1}: imports {m.group(2)}" for m in WEB_IMPORT.finditer(text)]


def kotlin_violations(path, text):
    if not path.endswith((".kt", ".java")) or path in KOTLIN_ALLOWED:
        return []
    return [f"{path}:{text.count(chr(10), 0, m.start()) + 1}: imports persona.module.*" for m in KOTLIN_IMPORT.finditer(text)]


def in_scope(core, f):
    """Whether the rule covers this file: tier core AND tier panel (the clients' interface code is the default panel, built
    on Core, and references no module either). A module's own files and everything outside both tiers are not scanned.
    Selected by tier (schema/core-paths.txt), not by schema/module-classification.txt."""
    return core.tier(f) in ("core", "panel")


def scan(root=ROOT, texts=None):
    """Every core or panel file that references a module, as readable lines. `texts` ({path: source}) replaces the
    checkout's files (the tests use it to show what a given file would report)."""
    core = cp.Core(root)
    roots = composition_roots(root)
    out = []
    for f in (texts if texts is not None else cp.tracked(root)):
        if not f.endswith((".go", ".ts", ".tsx", ".mts", ".js", ".jsx", ".mjs", ".kt", ".java")):
            continue
        if not in_scope(core, f):
            continue
        # The module list and the host are core files that name modules by design (web/src/modules/*.ts, the web
        # host only through the list); the lint config's own rule covers the rest of web.
        if f.startswith("web/src/modules/") and f.count("/") == 3:
            continue
        text = texts[f] if texts is not None else (root / f).read_text(encoding="utf-8", errors="replace")
        if f.startswith(("server/", "sdk/go/")):
            out += go_violations(f, text, roots)
        elif f.startswith("web/src/"):
            out += web_violations(f, text)
        elif f.startswith("android/"):
            out += kotlin_violations(f, text)
    return out


class CoreReferencesNoModuleTests(unittest.TestCase):
    def test_baselines_are_empty(self):
        self.assertEqual(baseline_problems(), [])

    def test_core_files_import_no_module(self):
        self.assertEqual(scan(), [], "a core file imports a module: reach it through the kernel / the module host / "
                         "Modules.registry (docs/architecture/core-and-modules.md, \"Guards\")")

    def test_apple_core_names_no_module_type(self):
        counts, sites = apple.scan()
        self.assertEqual(counts, {}, "a core Apple file names a module's type:\n" + "\n".join(sites))

    def test_scope_is_both_tiers_selected_by_tier(self):
        c = cp.Core()
        for f in ("server/internal/hub/hub.go", "web/src/lib/model-config.ts", "web/src/views/chat/ChatView.tsx",
                  "web/src/App.tsx", "android/core/hub/src/main/kotlin/A.kt", "android/feature/agents/src/main/kotlin/A.kt",
                  "android/app/src/main/kotlin/A.kt"):
            self.assertTrue(in_scope(c, f), f)
        for f in ("server/internal/modules/voice/voice.go", "web/src/modules/lark/page.ts", "android/module/voice/src/A.kt",
                  "docs/a.md"):
            self.assertFalse(in_scope(c, f), f)

    def test_a_panel_file_that_imports_a_module_is_reported(self):
        # Without selecting by tier these would be green: a panel file in web, in Android, and a headless web file.
        bad_web = 'import { Page } from "../../modules/health/page";\n'
        found = scan(texts={"web/src/views/chat/ChatView.tsx": bad_web,
                            "web/src/lib/model-config.ts": bad_web,
                            "android/feature/agents/src/main/kotlin/A.kt": "import persona.module.voice.VoiceModule\n",
                            "android/app/src/main/java/persona/android/Modules.kt": "import persona.module.voice.VoiceModule\n",
                            "web/src/modules/lark/page.ts": bad_web,  # a module may reach its own files
                            "web/src/views/chat/ok.tsx": 'import { x } from "../../lib/model-config";\n'})
        self.assertEqual(sorted(l.split(":")[0] for l in found),
                         ["android/feature/agents/src/main/kotlin/A.kt", "web/src/lib/model-config.ts", "web/src/views/chat/ChatView.tsx"])

    # The rules themselves, on sample text.
    def test_go_rule(self):
        imp = '\t"github.com/nwverse/persona/server/internal/modules/%s"\n'
        roots = {"internal/manager"}
        self.assertEqual(go_violations("server/internal/hub/a.go", imp % "voice", roots),
                         ["server/internal/hub/a.go:1: imports internal/modules/voice"])
        self.assertEqual(go_violations("server/internal/hub/a.go", imp % "all", roots),
                         ["server/internal/hub/a.go:1: imports internal/modules/all"])
        self.assertEqual(go_violations("server/internal/manager/a.go", imp % "all", roots), [])
        self.assertEqual(go_violations("server/internal/manager/a.go", imp % "voice", roots),
                         ["server/internal/manager/a.go:1: imports internal/modules/voice"])
        self.assertEqual(go_violations("server/internal/hub/a_test.go", imp % "all", roots), [])
        self.assertEqual(go_violations("server/internal/hub/a_test.go", imp % "volc/volctest", roots), [])
        self.assertEqual(go_violations("server/internal/hub/a_test.go", imp % "volc/voicetest", roots),
                         ["server/internal/hub/a_test.go:1: imports internal/modules/volc/voicetest"])
        self.assertEqual(go_violations("server/internal/modules/voice/a.go", imp % "voice", roots), [])
        self.assertEqual(go_violations("server/internal/hub/a.go", '\t"github.com/nwverse/persona/server/internal/moduleswitch"\n', roots), [])

    def test_web_rule(self):
        bad = ['import { A } from "../modules/health/page";', "const m = await import('../../modules/voice/x');",
               'export * from "./modules/lark/y";', 'import x from "../../src/modules/mail/z"']
        for line in bad:
            self.assertEqual(len(web_violations("web/src/views/a.ts", line + "\n")), 1, line)
        for line in ['import { m } from "../modules";', 'import { m } from "./modules/index";',
                     'import { s } from "../modules/standalone";', 'import x from "./modulesfoo/y";']:
            self.assertEqual(web_violations("web/src/views/a.ts", line + "\n"), [], line)
        self.assertEqual(web_violations("web/src/views/a.test.tsx", bad[0]), [])  # tests may reach a module's files

    def test_kotlin_rule(self):
        line = "import persona.module.voice.VoiceModule\n"
        self.assertEqual(len(kotlin_violations("android/core/hub/src/main/A.kt", line)), 1)
        self.assertEqual(kotlin_violations("android/app/src/main/java/persona/android/Modules.kt", line), [])
        self.assertEqual(kotlin_violations("android/core/hub/src/main/A.kt", "import persona.android.Foo\n"), [])

    def test_baseline_rule(self):
        import tempfile
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            for rel, kind in BASELINES.values():
                (root / rel).parent.mkdir(parents=True, exist_ok=True)
                (root / rel).write_text("{}\n" if kind == "json" else "# none\n")
            self.assertEqual(baseline_problems(root), [])
            (root / BASELINES["android"][0]).write_text("# none\n:core:x -> :module:voice\n")
            (root / BASELINES["web"][0]).write_text('{"web/src/a.ts": 1}\n')
            self.assertEqual(len(baseline_problems(root)), 2)


if __name__ == "__main__":
    if "--list" in sys.argv:
        print("\n".join(scan()) or "no core file references a module")
    else:
        unittest.main()
