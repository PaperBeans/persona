#!/usr/bin/env python3
"""Refuse a merge that adds weak types to the contract packages or the hub's handlers (#15397).

scripts/hooks/merge-pr.sh runs this with the merge base and the PR head. It merges the head onto the base in a
throwaway worktree and runs the wire types ratchet (scripts/ci/wire_types_ratchet.py) there, with the merge's own
baseline, so a weak field, an inline event or a literal call the PR adds fails before it reaches main. The ratchet
also holds the merge's baseline to the base's: no count may rise and an exception may only stand in for a name the
change removed.

A name that already fails on the base is not the PR's doing and is left to its own fix (it is reported). A PR that
changes no Go file, no counter and no baseline is not counted. The counter needs Go: without it the merge is refused,
never waved through. Exit codes: 0 pass, 1 refused, 2 could not check.
"""
import argparse
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

sys.path.insert(0, str(Path(__file__).resolve().parent))
import wire_types_ratchet as w  # noqa: E402

GIT_TIMEOUT = 300
RELEVANT = (".go", w.BASELINE_REL, "scripts/ci/wire_types_ratchet.py", "server/internal/archguard/testdata/contracts.txt")


def git(root, *args, check=True):
    result = subprocess.run(["git", "-C", str(root), *args], capture_output=True, text=True, timeout=GIT_TIMEOUT)
    if check and result.returncode != 0:
        raise w.WireTypesError("merge wire types: git {0} failed: {1}".format(" ".join(args), result.stderr.strip()))
    return result


def relevant(paths):
    return [p for p in paths if p.endswith(RELEVANT[0]) or p in RELEVANT[1:] or "/archguard/wirecount/" in p]


def baseline_of(tree):
    return w.parse_baseline((Path(tree) / w.BASELINE_REL).read_text(encoding="utf-8"), str(tree))


def check(root, base, head, counter=w.run_counter_in, out=print):
    """Refusal lines for merging head onto base; empty means the merge may go ahead."""
    changed = git(root, "diff", "--name-only", base + "..." + head).stdout.split()
    if not relevant(changed):
        out("merge wire types: the PR changes no Go file or wire types baseline; not counted")
        return []
    if shutil.which("go") is None:
        return ["merge wire types: go is not on PATH, so the weak types the PR may add cannot be counted; run "
                "merge-pr.sh where Go is installed (source the toolchain first)"]
    with tempfile.TemporaryDirectory(prefix="merge-wire-types-") as temp:
        tree = Path(temp) / "tree"
        git(root, "worktree", "add", "-q", "--detach", str(tree), base)
        try:
            merged = git(tree, "-c", "user.name=merge-pr", "-c", "user.email=merge-pr@example.com", "merge", "-q",
                         "--no-edit", head, check=False)
            if merged.returncode != 0:
                return ["merge wire types: the PR does not merge cleanly onto the base; merge origin/main into it first"]
            if not (tree / w.BASELINE_REL).is_file():
                out("merge wire types: the merge has no {0}; not counted".format(w.BASELINE_REL))
                return []
            metrics, exceptions, wide = baseline_of(tree)
            actual = counter(tree / "server")
            found = w.risen(actual, metrics, exceptions, wide)
            lines = []
            if found:
                git(tree, "checkout", "-q", "--detach", base)
                b_metrics, b_exceptions, b_wide = baseline_of(tree)
                already = w.risen(counter(tree / "server"), b_metrics, b_exceptions, b_wide)
                for metric, name in sorted(found & already):
                    out("merge wire types: {0} {1} already fails on the base; left to its own fix".format(metric, name))
                new = found - already
                if new:
                    keep = {m: {n: c for n, c in actual["metrics"].get(m, {}).items() if (m, n) in new} for m in w.METRICS}
                    lines += w.violations({"metrics": keep, "wide": actual["wide"]}, metrics, exceptions, wide)
            base_baseline = git(root, "show", base + ":" + w.BASELINE_REL, check=False)
            if base_baseline.returncode == 0:
                b_metrics, b_exceptions, b_wide = w.parse_baseline(base_baseline.stdout, base[:12] + ":" + w.BASELINE_REL)
                lines += w.raised_against(metrics, b_metrics, base[:12], wide, b_wide)
                lines += w.exceptions_grow(metrics, exceptions, b_metrics, b_exceptions, base[:12])
            return lines
        finally:
            git(root, "worktree", "remove", "--force", str(tree), check=False)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--root", default=str(Path(__file__).resolve().parents[2]))
    parser.add_argument("--base", required=True)
    parser.add_argument("--head", required=True)
    args = parser.parse_args(argv)
    try:
        lines = check(args.root, args.base, args.head)
    except (w.WireTypesError, subprocess.TimeoutExpired) as error:
        print(error, file=sys.stderr)
        return 2
    if lines:
        print("\n".join(lines), file=sys.stderr)
        return 1
    print("merge wire types: the merge adds no weak type")
    return 0


if __name__ == "__main__":
    sys.exit(main())
