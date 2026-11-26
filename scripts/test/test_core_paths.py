"""schema/core-paths.txt (#14916): it parses, every explicit path exists, module directories are never core, and sample
paths classify as expected. Run: python3 scripts/test/test_core_paths.py"""
import re
from pathlib import Path
import sys
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import core_paths as cp  # noqa: E402
import module_classification as mc  # noqa: E402

FILES = cp.tracked()
CORE = cp.Core()

SAMPLES = {
    "core": [
        "server/internal/hub/hub.go", "server/internal/modapi/modapi.go", "server/internal/modules/all/all.go",
        "server/internal/modules/all/node/node.go", "server/go.mod", "sdk/go/ext/ext.go",
        "web/src/core/module-host.ts", "web/src/lib/model-config.ts", "web/src/lib/todo-view-wire.ts",
        "web/src/lib/hub-update.ts", "web/src/lib/session-review-agent.ts",
        "android/core/hub/src/main/kotlin/A.kt", "android/core/data/src/main/kotlin/A.kt",
        "android/core/moduleapi/src/main/kotlin/A.kt", "native/app-core/Cargo.toml",
        "apple/Packages/PersonaWire/Sources/PersonaWire/Wire.swift", "apple/Packages/PersonaModuleAPI/Package.swift",
        "apple/Persona/App/Modules.swift", "apple/modules.yml", "schema/core-paths.txt", "schema/module-classification.txt",
        "scripts/hooks/merge-pr.sh", ".github/CODEOWNERS", "server/internal/proto/proto.go",
        "server/internal/archguard/archguard_test.go",  # a guard: its tests count
        "scripts/test/test_module_flags.py",
    ],
    "panel": [
        "web/src/views/chat/ChatView.tsx", "web/src/main.tsx", "web/src/App.tsx", "web/src/lib/use-hub.ts",
        "web/src/core/ModulePage.tsx", "web/src/ui/Button.tsx",
        # web/src/lib files that serve one page, menu, popover or browser preference are panel (review of #14928)
        "web/src/lib/model-panel.ts", "web/src/lib/session-panel.ts", "web/src/lib/node-palette.ts",
        "web/src/lib/model-id-input.ts", "web/src/lib/machine-alerts.ts", "web/src/lib/machine-capabilities.ts",
        "web/src/lib/todo-slim-row.ts", "web/src/lib/todo-detail-template.ts", "web/src/lib/session-insights.ts",
        "web/src/lib/todo-list-filter.ts", "web/src/lib/handoff.ts", "web/src/lib/agent-open.ts",
        "web/src/lib/todo-details.ts", "web/src/lib/todo-thread.ts", "web/src/lib/todo-stage.ts",
        "web/src/lib/agent-actions.ts", "web/src/lib/node-actions.ts", "web/src/lib/task-board.ts",
        "web/src/lib/taskdir/nested.ts", "web/src/lib/model-config-execution.ts",
        "android/app/src/main/kotlin/A.kt", "android/feature/agents/src/main/kotlin/A.kt",
        "android/core/designsystem/src/main/kotlin/A.kt", "android/core/navigation/src/main/kotlin/A.kt",
        "android/platform/notify/src/main/kotlin/A.kt",
        "apple/Persona/Views/Chat/ChatView.swift", "apple/Shared/Design/Theme.swift", "apple/Vision/V.swift",
        "apple/Packages/PersonaSSH/Sources/A.swift",
    ],
    "module": [
        "server/internal/modules/voice/voice.go", "server/internal/modules/voice/module.json",
        "web/src/modules/lark/module.ts", "android/module/voice/src/main/kotlin/A.kt", "apple/Modules/Health/H.swift",
        "apple/Persona/Views/Mail/MailView.swift", "apple/Persona/Voice/V.swift",
    ],
    "other": ["docs/architecture/core-and-modules.md", "README.md", "apple/project.yml", "web/package.json",
              "android/build.gradle.kts"],
}
# Tests-only changes never count as a core change, whatever the tier.
TESTS_ONLY = [
    "server/internal/hub/hub_test.go", "server/internal/hub/testdata/x.json", "web/src/core/module-host.test.ts",
    "android/core/hub/src/androidTest/kotlin/A.kt", "android/core/hub/src/test/kotlin/A.kt",
    "apple/Packages/PersonaWire/Tests/PersonaWireTests/A.swift", "server/internal/modapi/modapi_test.go",
    "web/src/views/chat/ChatView.test.tsx", "android/app/src/test/kotlin/A.kt", "apple/PersonaTests/ChatTests.swift",
]

# The web/src/lib modules that only the panel uses: a headless web file may not import them.
PANEL_ONLY_WEB_IMPORT = re.compile(
    r"""(?:from\s*|import\s*\(?\s*|require\(\s*)['"](?:\.\./(?:ui|views)/[^'"]*|\./(?:router|store|palette)(?:\.\w+)?)['"]""")

# What a tier-core client directory must not contain (non-test files only): a UI framework import or a .tsx file.
UI_IMPORT = re.compile(
    r"^\s*import\s+(androidx\.compose\b|SwiftUI|UIKit|AppKit)|\bfrom\s+[\"']react(-dom)?(/[^\"']*)?[\"']"
    r"|\brequire\([\"']react", re.M)


class CorePathsTests(unittest.TestCase):
    def test_parses(self):
        classified, explicit = cp.parse(cp.FILE.read_text(encoding="utf-8"))
        self.assertEqual(set(classified), set(cp.KINDS))
        self.assertTrue(explicit)

    def test_bad_lines_are_refused(self):
        base = ("classified core go server\nclassified panel web web/src\nclassified panel gradle android\n"
                "classified panel apple apple\n")
        for bad in ("nonsense x\n", "core\n", "core /abs\n", "headless a/../b\n", "classified core go server\n",
                    "classified middle go server\n"):
            with self.assertRaises(cp.FormatError, msg=bad):
                cp.parse(base + bad)
        with self.assertRaises(cp.FormatError):
            cp.parse("classified core go server\n")

    def test_every_explicit_path_exists(self):
        for e in CORE.explicit:
            hit = any(e.matches(f) for f in FILES)
            self.assertTrue(hit, f"core-paths.txt line {e.line}: `{e.kind} {e.path}` matches no tracked file (stale: "
                                 "delete the line or point it at the new place)")

    def test_explicit_paths_are_never_module_files(self):
        for e in CORE.explicit:
            for f in FILES:
                if e.matches(f):
                    self.assertIn(CORE.classification(f), (mc.CORE, None),
                                  f"line {e.line}: {f} belongs to module {CORE.classification(f)!r}")

    def test_module_directories_are_never_core_or_panel(self):
        for f in FILES:
            cls = CORE.classification(f)
            if cls not in (mc.CORE, None):
                self.assertFalse(CORE.is_core(f, tests_count=True), f"{f} is in module {cls} but counts as core")
                self.assertFalse(CORE.is_panel(f), f"{f} is in module {cls} but counts as panel")
                self.assertEqual(CORE.tier(f), "module", f)

    def test_modules_dirs_are_modules(self):
        mods = [f for f in FILES if f.startswith(("server/internal/modules/", "web/src/modules/", "android/module/"))]
        self.assertTrue(mods)
        for f in mods:
            if f.startswith("server/internal/modules/all/") or f.count("/") < 4 and f.startswith("web/src/modules/"):
                continue  # the composition roots and the module list, which are core
            self.assertFalse(cp.is_core(f), f)
            self.assertFalse(cp.is_panel(f), f)

    def test_samples(self):
        for want, paths in SAMPLES.items():
            for p in paths:
                self.assertEqual(cp.tier(p), want, p)
                self.assertEqual(cp.is_core(p), want == "core", p)
                self.assertEqual(cp.is_panel(p), want == "panel", p)

    def test_tests_only_changes_do_not_count(self):
        for p in TESTS_ONLY:
            self.assertFalse(cp.is_core(p), p)
        self.assertEqual(cp.tier("apple/Packages/PersonaWire/Tests/PersonaWireTests/A.swift"), "core")
        self.assertEqual(cp.tier("web/src/views/chat/ChatView.test.tsx"), "panel")

    def test_headless_client_directories_have_no_ui_import(self):
        """Core has no UI: a file under a `headless` path (or a client file the tier puts in core, outside the
        foundation lines) must not import a UI framework or be a .tsx file. A failure names the file."""
        checked = 0
        for e in CORE.explicit:
            if e.kind != "headless":
                continue
            hits = [f for f in FILES if e.matches(f) and not cp.is_test_only(f)]
            self.assertTrue(hits, f"line {e.line}: `headless {e.path}` matches no non-test file")
            for f in hits:
                self.assertFalse(f.endswith(".tsx"), f"{f}: a .tsx file cannot be tier-core headless code (line {e.line})")
                if not f.endswith((".kt", ".swift", ".ts", ".js", ".mjs", ".rs")):
                    continue
                text = (cp.ROOT / f).read_text(encoding="utf-8", errors="replace")
                m = UI_IMPORT.search(text)
                self.assertIsNone(m, f"{f}: imports a UI framework ({m and m.group(0).strip()}); it cannot be headless "
                                     f"(schema/core-paths.txt line {e.line}); move it to the panel or split the UI out")
                if f.startswith("web/"):
                    m = PANEL_ONLY_WEB_IMPORT.search(text)
                    self.assertIsNone(m, f"{f}: imports a panel-only module ({m and m.group(0).strip()}); it cannot be "
                                         f"headless (schema/core-paths.txt line {e.line})")
                checked += 1
        self.assertGreater(checked, 500)

    def test_the_headless_import_rule_catches_the_review_cases(self):
        for text in ('import { toast } from "../ui/atoms";', "import { navigate } from './router';",
                     'import { getState } from "./store";', 'import { reg } from "./palette";',
                     "const m = await import('../ui/menu-model');"):
            self.assertTrue(PANEL_ONLY_WEB_IMPORT.search(text), text)
        for text in ('import { api } from "./api";', 'import type { Item } from "./types";', 'import { t } from "../i18n";',
                     'import { storeRows } from "./store-rows";'):
            self.assertIsNone(PANEL_ONLY_WEB_IMPORT.search(text), text)

    def test_every_web_lib_file_in_core_is_listed_by_name(self):
        # No prefix glob puts a page's helpers in core: each web/src/lib core entry is one .ts file (or `configuration/`).
        for e in CORE.explicit:
            if e.kind == "headless" and e.path.startswith("web/src/lib/"):
                self.assertNotIn("*", e.path, f"line {e.line}: list the web/src/lib file by name")

    def test_a_glob_star_stays_inside_one_path_segment(self):
        glob = cp.Explicit("core", "web/src/core/module-*", 1)
        self.assertTrue(glob.matches("web/src/core/module-host.ts"))
        self.assertTrue(glob.matches("web/src/core/module-foo/bar/baz.ts"))  # a matched directory covers what is below it
        self.assertFalse(glob.matches("web/src/core/x/module-host.ts"))
        ts = cp.Explicit("headless", "web/src/lib/task*.ts", 1)
        self.assertTrue(ts.matches("web/src/lib/task-board.ts"))
        self.assertFalse(ts.matches("web/src/lib/taskdir/nested.ts"))
        self.assertFalse(ts.matches("web/src/lib/task/nested.ts"))
        self.assertFalse(ts.matches("web/src/lib/task.tsx"))

    def test_headless_paths_are_not_foundation_ui(self):
        # The UI-import guard above covers `headless`; the explicit foundation `core` lines may name a file that
        # declares a UI-typed contract (the module API), so they are not scanned.
        self.assertTrue(any(e.kind == "headless" for e in CORE.explicit))

    def test_core_files_keeps_order(self):
        self.assertEqual(cp.core_files(["docs/a.md", "server/go.mod", "web/src/modules/lark/a.ts", "apple/modules.yml",
                                        "web/src/views/chat/ChatView.tsx"]),
                         ["server/go.mod", "apple/modules.yml"])

    def test_test_only_rule(self):
        for p in ("a/b_test.go", "x/testdata/f.json", "web/src/a.test.tsx", "android/app/src/test/A.kt",
                  "android/app/src/debugUnitTest/A.kt", "apple/PersonaUITests/A.swift", "apple/Shared/FooTests.swift"):
            self.assertTrue(cp.is_test_only(p), p)
        for p in ("server/internal/hub/hub.go", "android/app/src/main/A.kt", "web/src/test-utils.ts"):
            self.assertFalse(cp.is_test_only(p), p)

    def test_the_core_is_a_minority_of_the_clients(self):
        counts = cp.tier_counts(FILES)
        for area in ("web", "apple"):  # the interface is most of each of these clients
            self.assertGreater(counts.get(("panel", area), 0), 3 * counts.get(("core", area), 0), area)
        self.assertGreater(counts.get(("panel", "android"), 0), counts.get(("core", "android"), 0))
        self.assertGreater(counts[("core", "server")], 1000)


if __name__ == "__main__":
    unittest.main()
