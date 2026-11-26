"""Read schema/core-paths.txt: which paths are Persona Core (#14916; the file's header describes the format).

Persona Core has no UI (#13430): tier `core` is the server core, the clients' headless code and the foundation
interfaces; tier `panel` is the clients' interface code, the default panel built on Core; module directories are neither.

    is_core(path)        True when a change to this file counts as a change to the core (tier core; a test-only file does not)
    is_panel(path)       True for a file of the default panel (location only: tests of the panel are panel too)
    tier(path)           'core' | 'panel' | 'module' | 'other', by location (tests of a core path are still 'core')
    core_files(paths)    the paths that are core, in order

CLI (the numbers in a PR description):  python3 scripts/test/core_paths.py --table
Used by test_core_paths.py, scripts/hooks/codeowners.py and scripts/hooks/core-change-check.py.
"""
from dataclasses import dataclass
from functools import lru_cache
from pathlib import Path
import re
import subprocess
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))
import module_classification as mc  # noqa: E402

ROOT = mc.ROOT
FILE = ROOT / "schema" / "core-paths.txt"
KINDS = ("go", "web", "gradle", "apple")
TIERS = ("core", "panel")


class FormatError(ValueError):
    pass


@lru_cache(maxsize=None)
def _glob(pattern):
    """A `*` glob as the CODEOWNERS model reads it: `*` stays inside one path segment (never crosses `/`), and a segment
    the glob matches as a whole covers everything below it, like a directory path without `*`."""
    return re.compile("".join("[^/]*" if c == "*" else re.escape(c) for c in pattern) + "(?:/.*)?\\Z")


@dataclass(frozen=True)
class Explicit:
    kind: str  # "core", "headless" (client code with no UI, tier core) or "guard"
    path: str
    line: int

    def matches(self, path):
        if "*" in self.path:
            return _glob(self.path).match(path) is not None
        return path == self.path or path.startswith(self.path.rstrip("/") + "/")


def parse(text):
    """(classified: kind -> (tier, roots), explicit entries); FormatError on a malformed line."""
    classified, explicit = {}, []
    for n, raw in enumerate(text.splitlines(), 1):
        words = raw.split("#", 1)[0].split()
        if not words:
            continue
        if words[0] == "classified":
            if len(words) < 4 or words[1] not in TIERS or words[2] not in KINDS or words[2] in classified:
                raise FormatError(f"line {n}: expected `classified <{'|'.join(TIERS)}> <{'|'.join(KINDS)}> <root>...`, "
                                  f"once per kind: {raw!r}")
            classified[words[2]] = (words[1], tuple(w.rstrip("/") for w in words[3:]))
        elif words[0] in ("core", "headless", "guard"):
            if len(words) != 2 or words[1].startswith("/") or ".." in words[1].split("/"):
                raise FormatError(f"line {n}: expected `{words[0]} <repo-relative path>`: {raw!r}")
            explicit.append(Explicit(words[0], words[1], n))
        else:
            raise FormatError(f"line {n}: unknown directive {words[0]!r}: {raw!r}")
    missing = [k for k in KINDS if k not in classified]
    if missing:
        raise FormatError(f"missing `classified` line for {', '.join(missing)}")
    return classified, explicit


_TEST_FILE = re.compile(r"(_test\.go|\.test\.[cm]?[jt]sx?|\.spec\.[^./]+|(Tests?|Spec)\.(swift|kt))\Z")
_TEST_DIR = re.compile(r"(^|/)(testdata|__tests__)/")
# An Android source set of tests: src/test, src/androidTest, src/testFixtures, src/integrationTest, src/*Test...
_ANDROID_TEST_SET = re.compile(r"(test|testFixtures|[A-Za-z]*Tests?|[A-Za-z]*TestFixtures)\Z")


def is_test_only(path):
    """A file that only tests something: tests may be added to the core freely."""
    parts = path.split("/")
    if _TEST_FILE.search(parts[-1]) or _TEST_DIR.search(path):
        return True
    if parts[0] == "android" and any(_ANDROID_TEST_SET.fullmatch(d) for d in parts[1:-1]):
        return True  # a source set of tests, wherever the project keeps its source sets (not only under src/)
    return parts[0] == "apple" and any(re.search(r"Tests?\Z", d) for d in parts[:-1])


class Core:
    def __init__(self, root=ROOT):
        self.root = root
        self.classified, self.explicit = parse((root / "schema" / "core-paths.txt").read_text(encoding="utf-8"))
        _, self.rules = mc.parse((root / "schema" / "module-classification.txt").read_text(encoding="utf-8"))
        self.projects = sorted(
            mc.gradle_projects((root / "android" / "settings.gradle.kts").read_text(encoding="utf-8")),
            key=lambda p: -len(p))
        self._class = lru_cache(maxsize=None)(self._class_of_dir)

    def _class_of_dir(self, kind, d):
        return mc.classify(d, kind, self.rules)[0]

    def _under(self, kind, path):
        return any(path == r or path.startswith(r + "/") for r in self.classified[kind][1])

    def _located_tier(self, path):
        """The tier of the classified root a path sits under ('core' or 'panel'), None outside every root."""
        for kind, (tier, _) in self.classified.items():
            if self._under(kind, path):
                return tier
        return None

    def classification(self, path):
        """"core", a module id, or None for a file the classification does not cover."""
        for kind in ("go", "web"):
            if self._under(kind, path):
                d = path.rsplit("/", 1)[0] if "/" in path else ""
                while d:
                    c = self._class(kind, d)
                    if c is not None:
                        return c
                    d = d.rsplit("/", 1)[0] if "/" in d else ""
                return mc.CORE  # a file no classified directory sits above: server/go.mod, migrations
        if self._under("gradle", path):
            for p in self.projects:
                d = "android/" + p.strip(":").replace(":", "/")
                if path.startswith(d + "/"):
                    return self._class("gradle", p)
            return None
        if self._under("apple", path):
            d = path.rsplit("/", 1)[0]
            while "/" in d:
                c = self._class("apple", d)
                if c is not None:
                    return c
                d = d.rsplit("/", 1)[0]
        return None

    def tier(self, path):
        """'core' | 'panel' | 'module' | 'other', by location; whether a test file counts is is_core's business."""
        cls = self.classification(path)
        if cls is not None and cls != mc.CORE:
            return "module"  # a module's file is never core and never panel
        if any(e.matches(path) for e in self.explicit):
            return "core"
        if cls == mc.CORE:
            return self._located_tier(path) or "other"
        return "other"

    def is_panel(self, path):
        return self.tier(path) == "panel"

    def is_core(self, path, tests_count=False):
        if self.tier(path) != "core":
            return False
        for e in self.explicit:
            if e.kind == "guard" and e.matches(path):
                return True
        return tests_count or not is_test_only(path)


@lru_cache(maxsize=None)
def _default():
    return Core()


def is_core(path):
    return _default().is_core(path)


def is_panel(path):
    return _default().is_panel(path)


def tier(path):
    return _default().tier(path)


def core_files(paths):
    return [p for p in paths if is_core(p)]


def tracked(root=ROOT):
    """Tracked files, and new files not ignored (a file added in this change counts before it is committed)."""
    out = subprocess.run(["git", "-C", str(root), "ls-files", "-z", "--cached", "--others", "--exclude-standard"], check=True, capture_output=True).stdout.decode()
    return [p for p in out.split("\0") if p]


def table(files):
    """(area, pattern, count) rows: the core files by area, at directory level."""
    c, rows = _default(), {}
    for f in files:
        if not c.is_core(f, tests_count=True):
            continue
        parts = f.split("/")
        if f.startswith("server/internal/") and len(parts) > 3:
            key = ("Server", "server/internal/" + parts[2] + "/")
        elif parts[0] in ("server", "sdk"):
            key = ("Server", "/".join(parts[:2 if len(parts) > 2 else 1]) + ("/" if len(parts) > 2 else ""))
        elif parts[0] == "web":
            key = ("Web", "/".join(parts[:3]) + "/" if len(parts) > 3 else f)
        elif parts[0] == "android":
            key = ("Android", "/".join(parts[:3]) + "/" if len(parts) > 3 else f)
        elif parts[0] == "apple":
            key = ("Apple", "/".join(parts[:3]) + "/" if len(parts) > 3 else f)
        else:
            key = ("Foundation", f)
        rows[key] = rows.get(key, 0) + 1
    return sorted((a, p, n) for (a, p), n in rows.items())


def tier_counts(files):
    """{(tier, area): file count} over all tracked files (tests included), area = server | web | android | apple | other."""
    c, out = _default(), {}
    for f in files:
        t = c.tier(f)
        if t in ("core", "panel"):
            area = {"server": "server", "sdk": "server", "web": "web", "android": "android", "apple": "apple"}.get(f.split("/")[0], "other")
            out[(t, area)] = out.get((t, area), 0) + 1
    return out


if __name__ == "__main__":
    if "--table" in sys.argv:
        files = tracked()
        total = {}
        for area, pat, n in table(files):
            total[area] = total.get(area, 0) + n
        for area, n in sorted(total.items()):
            print(f"{area}\t{n}")
        print("total core files (tests included)\t%d of %d tracked" % (sum(total.values()), len(files)))
    elif "--tiers" in sys.argv:
        files = tracked()
        for (t, area), n in sorted(tier_counts(files).items()):
            print(f"{t}\t{area}\t{n}")
    elif "--dirs" in sys.argv:  # tier core files by directory, depth 3 (the core path list as a reader expands it)
        files = tracked()
        c, rows = _default(), {}
        for f in files:
            if c.tier(f) == "core":
                k = "/".join(f.split("/")[:3]) if f.count("/") > 2 else f
                rows[k] = rows.get(k, 0) + 1
        for k, n in sorted(rows.items()):
            print(f"{n}\t{k}")
    else:
        print("usage: core_paths.py --table | --tiers | --dirs")
