#!/usr/bin/env python3
"""Write the deployed-commits file a hub reads to tell what it runs without asking GitHub (#3205, #4867;
docs/assistant/todo-status-sop.md "Without GitHub").

    python3 scripts/deploy/deployed_commits.py --out <file or directory> [--commit <rev>] [--repo <owner>/<name>]
        [--max 3000] [--pr <N>[=<head sha>] ...] [--no-open-prs] [--base origin/main]

The file is the header, "# repo: <owner>/<name>", a "# pr: <N> <head sha>" line for each PR whose head the deployed
commit contains, then the deployed commit and its ancestors newest first ("<sha> <subject>", as git log prints them).
The PR lines are for a build of a local branch that runs a PR before it is merged upstream. Open PRs are read with the
GitHub CLI when it is installed and signed in (one call; --no-open-prs skips it); --pr names more, with its head or by
number (its head read with gh). A PR counts when the deployed commit contains its head, or every commit of its own (not
on --base, default origin/main) when the PR branch only merged the base since; its line then names its newest own
commit. Any other PR is left out. The commit a line names is listed too when it is older than the history listed, so the
hub, which believes a PR line only for a commit the file lists, can check it.
The file is written under a temporary name and renamed: run it for the commit the hub is built from, with --out next to
the hub's binary or in its state directory.
"""
import argparse
import datetime
import json
import os
import re
import shutil
import subprocess
import sys

HEADER = "# persona deployed commits v1"
NAME = "deployed-commits.txt"
# A "(#N)" in commits written up to 2026-11-07 is the previous tracker's number (CLAUDE.md, "Old issue numbers"): the
# hub would read it as this repository's PR #N, another PR (#4867). Those commits are left out unless --since says so.
SINCE = "2026-11-08T00:00:00+00:00"


def git(*args, check=True):
    run = subprocess.run(["git", *args], capture_output=True, text=True)
    if check and run.returncode != 0:
        sys.exit(f"deployed-commits: git {' '.join(args)}: {run.stderr.strip()}")
    return run


def repo_of_remote():
    """owner/name of the origin remote on GitHub ("" when it is not one)."""
    url = git("remote", "get-url", "origin", check=False).stdout.strip()
    m = re.search(r"github\.com[:/]([^/\s]+)/([^/\s]+?)(?:\.git)?/?$", url)
    return f"{m.group(1)}/{m.group(2)}".lower() if m else ""


def deployed_by(commit, head, base):
    """The commit that shows the deployed commit runs the PR whose head is head: head itself when the deployed commit
    contains it; else, when the PR branch only merged base since, the PR's newest own commit (not on base) once the
    deployed commit contains every one of them; "" when it runs none or only part of it (or this checkout lacks head)."""
    if git("cat-file", "-e", f"{head}^{{commit}}", check=False).returncode != 0:
        return ""
    if git("merge-base", "--is-ancestor", head, commit, check=False).returncode == 0:
        return head
    if not base:
        return ""
    own = git("rev-list", "--no-merges", head, f"^{base}", check=False).stdout.split()
    missing = git("rev-list", "--no-merges", head, f"^{base}", f"^{commit}", check=False).stdout.split()
    return own[0] if own and not missing else ""


def gh_json(*args):
    """The GitHub CLI's JSON answer, None when it is not installed, not signed in or fails."""
    if not shutil.which("gh"):
        return None
    run = subprocess.run(["gh", *args], capture_output=True, text=True)
    if run.returncode != 0:
        print(f"deployed-commits: gh {args[0]} {args[1]}: {run.stderr.strip()[:200]}", file=sys.stderr)
        return None
    try:
        return json.loads(run.stdout or "null")
    except ValueError:
        return None


def pr_heads(repo, named, open_prs):
    """PR number → head sha: the ones named (by number, read with gh, or with their head) and the open ones."""
    heads = {}
    if open_prs:
        for pr in gh_json("pr", "list", "-R", repo, "--state", "open", "--limit", "1000", "--json", "number,headRefOid") or []:
            if isinstance(pr, dict) and pr.get("number") and pr.get("headRefOid"):
                heads[int(pr["number"])] = str(pr["headRefOid"]).lower()
    for spec in named:
        number, _, sha = spec.partition("=")
        if not number.lstrip("#").isdigit():
            sys.exit(f"deployed-commits: --pr {spec}: give <N> or <N>=<head sha>")
        if not sha:
            view = gh_json("pr", "view", number.lstrip("#"), "-R", repo, "--json", "headRefOid")
            sha = (view or {}).get("headRefOid", "") if isinstance(view, dict) else ""
            if not sha:
                print(f"deployed-commits: PR #{number.lstrip('#')}: its head could not be read, left out", file=sys.stderr)
                continue
        heads[int(number.lstrip("#"))] = sha.strip().lower()
    return heads


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--out", required=True, help=f"the file, or a directory to write {NAME} in")
    ap.add_argument("--commit", default="HEAD", help="the deployed commit (default HEAD)")
    ap.add_argument("--repo", default="", help="owner/name of the PRs (default: the origin remote's)")
    ap.add_argument("--max", type=int, default=3000, help="how many commits of its history to list")
    ap.add_argument("--pr", action="append", default=[], help="a PR the build contains: <N> or <N>=<head sha>")
    ap.add_argument("--no-open-prs", action="store_true", help="do not ask GitHub which open PRs it contains")
    ap.add_argument("--base", default="origin/main", help="the branch PRs go into, to tell a PR's own commits")
    ap.add_argument("--since", default=SINCE, help="leave out commits written before this time (ISO 8601; empty: keep all)")
    a = ap.parse_args()
    commit = git("rev-parse", "--verify", f"{a.commit}^{{commit}}").stdout.strip()
    repo = (a.repo or repo_of_remote()).strip().lower().removesuffix(".git")
    if not re.fullmatch(r"[\w.-]+/[\w.-]+", repo):
        sys.exit("deployed-commits: no GitHub repository: give --repo <owner>/<name>")
    since = datetime.datetime.fromisoformat(a.since).timestamp() if a.since else None
    log = []
    for line in git("log", f"--max-count={max(1, a.max)}", "--format=%H %at %s", commit).stdout.splitlines():
        sha, at, subject = (line.split(" ", 2) + [""])[:3]
        if since is None or int(at) >= since or not log:  # the deployed commit itself always comes first
            log.append(f"{sha} {subject}")
    listed = {line.split(" ", 1)[0] for line in log}
    base = git("rev-parse", "--verify", "--quiet", f"{a.base}^{{commit}}", check=False).stdout.strip()
    heads = {}
    for n, head in sorted(pr_heads(repo, a.pr, not a.no_open_prs).items()):
        if re.fullmatch(r"[0-9a-f]{40}", head) and (sha := deployed_by(commit, head, base)):
            heads[n] = sha
    for n, sha in heads.items():
        if sha not in listed:  # older than the history listed: listed after it (its subject left out, it could name a
            log.append(f"{sha} (head of PR {n})")  # number), so the hub can check the PR line
            listed.add(sha)
    out = os.path.join(a.out, NAME) if os.path.isdir(a.out) else a.out
    text = "\n".join([HEADER, f"# repo: {repo}", *(f"# pr: {n} {sha}" for n, sha in heads.items()), *log]) + "\n"
    with open(out + ".new", "w", encoding="utf-8") as f:
        f.write(text)
    os.replace(out + ".new", out)
    prs = " ".join(f"#{n}" for n in heads) or "none"
    print(f"deployed-commits: {out}: {commit[:9]}, {len(log)} commits, PRs by head: {prs}")


if __name__ == "__main__":
    main()
