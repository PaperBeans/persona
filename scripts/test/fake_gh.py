"""A fake `gh` for the tests of scripts/hooks/merge-pr.sh and scripts/hooks/core-change-check.py (#14916).

The fake is a script text (FAKE_GH): a test writes it to `<bin>/gh` behind a `#!<python>` line and puts the directory first
on PATH, so the real gh never runs. State comes from the JSON file named by FAKE_GH_STATE, every call is appended to the
file named by FAKE_GH_LOG:

    pr          {title, body, labels: [{name}], ...}  what `pr view --json a,b` prints (only the asked fields)
    at_merge    the title and body GitHub would record when the merge runs (an edit saved after the last view)
    files       [{"filename": ..., "previous_filename": ...}, ...]  the rows of GET repos/<repo>/pulls/<n>/files

Like the real CLI, `gh api` of the files endpoint serves ONE page of `per_page` rows (the `page` query, default 1) and
walks every page only with --paginate; `--jq` runs once per page, on that page's JSON array. The fake knows the jq programs
the scripts use (JQ below) and refuses any other, so a changed expression fails the test instead of passing silently.
"""

FAKE_GH = r'''
import json, os, re, sys
args = sys.argv[1:]
state_path, log_path = os.environ["FAKE_GH_STATE"], os.environ["FAKE_GH_LOG"]
state = json.load(open(state_path))
entry = {"argv": args, "cwd": os.getcwd()}
out = ""


def tsv(row):
    return "\t".join(row)


# jq program -> what it prints for one page (a list of objects), the way gojq would.
JQ = {
    '.[] | [.filename, (.previous_filename // "")] | @tsv':
        lambda page: "\n".join(tsv([r["filename"], r.get("previous_filename") or ""]) for r in page),
}


def api(argv):
    options = {"--jq": None, "-q": None, "-X": None}
    path, i, paginate = None, 0, False
    while i < len(argv):
        a = argv[i]
        if a == "--paginate":
            paginate = True
        elif a in options:
            options[a] = argv[i + 1]
            i += 1
        elif not a.startswith("-"):
            path = a
        i += 1
    jq = options["--jq"] or options["-q"]
    m = re.fullmatch(r"repos/[^/]+/[^/]+/pulls/(\d+)/files(?:\?(.*))?", path or "")
    if not m:
        return ""  # the other api calls (branch refs) only need to succeed
    query = dict(p.split("=", 1) for p in (m.group(2) or "").split("&") if "=" in p)
    per_page, rows = int(query.get("per_page", 30)), state.get("files", [])
    pages = [rows[i:i + per_page] for i in range(0, len(rows), per_page)] or [[]]
    if not paginate:
        pages = [pages[int(query.get("page", 1)) - 1] if int(query.get("page", 1)) <= len(pages) else []]
    if jq is None:
        return "".join(json.dumps(p) + "\n" for p in pages)
    if jq not in JQ:
        sys.stderr.write("fake gh: unsupported --jq program: %r\n" % jq)
        sys.exit(1)
    return "\n".join(t for t in (JQ[jq](p) for p in pages) if t)


if args[:2] == ["pr", "view"]:
    out = json.dumps({k: state["pr"].get(k, [] if k == "labels" else None) for k in args[args.index("--json") + 1].split(",")})
elif args[:1] == ["api"]:
    out = api(args[1:])
elif args[:2] == ["repo", "view"]:
    out = "example/persona"
elif args[:2] == ["pr", "merge"]:
    now = state.get("at_merge") or state["pr"]
    entry["subject"] = args[args.index("--subject") + 1] if "--subject" in args else now["title"] + " (#7)"
    body_file = args[args.index("--body-file") + 1] if "--body-file" in args else None
    entry["body"] = open(body_file, newline="").read() if body_file else now["body"]
with open(log_path, "a") as log:
    log.write(json.dumps(entry) + "\n")
print(out)
'''


def rows(*names):
    """files state rows: a name, or `old -> new` for a rename."""
    out = []
    for n in names:
        old, _, new = n.partition(" -> ")
        out.append({"filename": new, "previous_filename": old} if new else {"filename": n})
    return out
