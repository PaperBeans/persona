#!/usr/bin/env python3
"""Fail when the contract packages or the hub's handlers gain weak types: bare enums and ids, untyped maps, raw JSON.

server/internal/archguard/wirecount counts, with go/parser, the weak shapes in every package of
server/internal/archguard/testdata/contracts.txt (a string field named *State, *Status, *Phase, *Kind, *Type or *Role; a
string or []string *ID field; map[string]any; json.RawMessage; *time.Time, *bool, *string *Error fields) and in the
handler layer (server/internal/{hub,assistant,devices,extmgr}: map[string]any{"type": ...} events, writeJSON(map...)
calls, .Call(ctx, "method", ...) with a literal method). The counts are global and kept as names, never per package:
a field is "<Type>.<Field>", an event is its type string, a writeJSON map is its sorted keys, a call is its method.
A type that moves from one contract package to another (the module slimming does this) keeps its names and so is
neutral; only a name that is new, or occurs more often than the baseline allows, is a rise.

Moves into the contract packages are neutral too. The counter also counts the field metrics "wide", over every
non-test package under server/internal (modules included) and the SDK's wire package, and the baseline keeps those
counts in "wide". A contract field name above its allowance is let through ("lifted") when its wide count is at or
under the baseline's: the field already existed in a Core package or a module and was lifted into a contract package
(slimming's first step for each module, #15022) or moved back from a module (#15297). A field written anew raises its
wide count too and fails. Lifted names are not added to "metrics"; their wide entry keeps letting them through. A
rename is a new name: lower the old one and add an exception for the new one in the same PR.

The baseline is scripts/ci/wire_types_baseline.json: {"metrics": {metric: {name: count}}, "wide": {field metric:
{name: count}}, "exceptions": ...}. A rise
fails with the new names and how to fix them. There is no ignore marker. A weak type that has to stay goes in the
baseline's "exceptions": {metric: {name: {"count": n, "pr": "#N", "why": "..."}}}, which raises that name's allowance
for review to see. --update only lowers: it drops names that are gone and lowers counts, and it refuses to raise or
add a name (in "metrics" or "wide") or to create a baseline. With a base (--base, else CI_BASE_SHA;
scripts/ci/ratchet_base.py) the baseline's own metrics and wide counts may not rise or gain a name against the merge
base. Needs Go. See docs/operations/testing.md ("Count
ratchets") and docs/lessons/testing.md.
"""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

sys.path.insert(0, str(Path(__file__).resolve().parent))
import ratchet_base  # noqa: E402

BASELINE_REL = "scripts/ci/wire_types_baseline.json"
COUNTER_PKG = "./internal/archguard/wirecount/"
GO_TIMEOUT = 300
METRICS = ("bare-enum-state", "bare-enum-kind", "bare-enum-type", "bare-enum-role", "bare-id", "bare-error",
           "map-any", "raw-json", "ptr-time", "ptr-bool", "ptr-error-string", "inline-event", "write-json-map",
           "literal-call")
FIELD_METRICS = METRICS[:11]  # the metrics the "wide" counts cover (wirecount.FieldMetrics)
FIX = {
    "bare-enum-state": "use a named type with a closed set of values (a Go enum with Valid(), a schema enum) instead of string",
    "bare-enum-kind": "use a named type with a closed set of values instead of string",
    "bare-enum-type": "use a named type with a closed set of values instead of string",
    "bare-enum-role": "use a named type with a closed set of values instead of string",
    "bare-id": "use an ID type (agentref.MachineID, SessionID, ... or a new one) instead of string",
    "bare-error": "use a typed failure (a closed code plus message) instead of a bare string",
    "map-any": "use a struct or a named type; decode into it instead of map[string]any",
    "raw-json": "use the typed payload, or a named type that says what the JSON is",
    "ptr-time": "model the states as one closed enum with its own timestamps instead of optional fields",
    "ptr-bool": "model the states as one closed enum instead of an optional flag",
    "ptr-error-string": "use a typed failure instead of an optional error string",
    "inline-event": "define the event as a typed struct in the wire package and send that",
    "write-json-map": "reply with a named response struct from the wire package",
    "literal-call": "call a method descriptor constant from the wire package, not a string literal",
}


class WireTypesError(Exception):
    """The check could not be run (no Go, a broken baseline, or git failed)."""


def parse_baseline(text, label="baseline"):
    """(metrics, exceptions, wide): metrics {metric: {name: count}}, exceptions {metric: {name: count}}, wide
    {field metric: {name: count}} (empty when the baseline has none)."""
    try:
        data = json.loads(text)
    except ValueError as error:
        raise WireTypesError("wire types ratchet: {0} is not JSON: {1}".format(label, error))
    metrics = data.get("metrics") if isinstance(data, dict) else None
    if not isinstance(metrics, dict):
        raise WireTypesError("wire types ratchet: {0} has no \"metrics\" object".format(label))
    exceptions = {}
    for metric, names in (data.get("exceptions") or {}).items():
        for name, entry in names.items():
            if not (isinstance(entry, dict) and isinstance(entry.get("count"), int) and entry["count"] > 0
                    and str(entry.get("pr", "")).startswith("#") and entry.get("why")):
                raise WireTypesError("wire types ratchet: {0}: exception {1} {2} needs count, pr (#N) and why".format(
                    label, metric, name))
            exceptions.setdefault(metric, {})[name] = entry["count"]
    for metric, names in metrics.items():
        if metric not in METRICS:
            raise WireTypesError("wire types ratchet: {0}: unknown metric {1}".format(label, metric))
        for name, count in names.items():
            if not isinstance(count, int) or count < 1:
                raise WireTypesError("wire types ratchet: {0}: {1} {2} has count {3!r}".format(label, metric, name, count))
    wide = data.get("wide") or {}
    if not isinstance(wide, dict):
        raise WireTypesError("wire types ratchet: {0}: \"wide\" is not an object".format(label))
    for metric, names in wide.items():
        if metric not in FIELD_METRICS:
            raise WireTypesError("wire types ratchet: {0}: wide has non-field metric {1}".format(label, metric))
        for name, count in names.items():
            if not isinstance(count, int) or count < 1:
                raise WireTypesError("wire types ratchet: {0}: wide {1} {2} has count {3!r}".format(label, metric, name, count))
    return metrics, exceptions, wide


def run_counter(root):
    """The counts as {"metrics": {metric: {name: count}}, "wide": {field metric: {name: count}}} from the Go counter,
    run over the repository's server/."""
    return run_counter_in(Path(root) / "server")


def run_counter_in(server):
    """The same, over a server/ directory; the counter's own source is always this checkout's."""
    server = Path(server).resolve()
    with tempfile.TemporaryDirectory(prefix="wire-types-") as temp:
        out = Path(temp) / "counts.json"
        env = dict(os.environ, WIRE_COUNTS_OUT=str(out), WIRE_COUNTS_ROOT=str(server))
        try:
            result = subprocess.run(["go", "test", "-count=1", "-run", "^TestEmit$", COUNTER_PKG],
                                    cwd=str(Path(__file__).resolve().parents[2] / "server"), capture_output=True,
                                    timeout=GO_TIMEOUT, env=env)
        except FileNotFoundError:
            raise WireTypesError("wire types ratchet: go is not on PATH")
        except subprocess.TimeoutExpired:
            raise WireTypesError("wire types ratchet: the counter timed out after {0} s".format(GO_TIMEOUT))
        if result.returncode != 0 or not out.is_file():
            raise WireTypesError("wire types ratchet: the counter failed: "
                                 + (result.stdout + result.stderr).decode("utf-8", "replace").strip())
        data = json.loads(out.read_text(encoding="utf-8"))
    return {key: {metric: names for metric, names in (data.get(key) or {}).items() if names}
            for key in ("metrics", "wide")}


def lifted_names(actual, metrics, exceptions, wide):
    """{metric: [name]} of contract names above their allowance whose wide count is at or under the baseline's wide
    count: fields that existed outside the contract packages and moved into one."""
    lifted = {}
    for metric in FIELD_METRICS:
        allowed, extra = metrics.get(metric, {}), exceptions.get(metric, {})
        now_wide, base_wide = actual["wide"].get(metric, {}), wide.get(metric, {})
        for name, count in sorted(actual["metrics"].get(metric, {}).items()):
            if count > allowed.get(name, 0) + extra.get(name, 0) and \
                    count <= now_wide.get(name, 0) <= base_wide.get(name, 0) + extra.get(name, 0):
                lifted.setdefault(metric, []).append(name)
    return lifted


def risen(actual, metrics, exceptions, wide=None):
    """{(metric, name)} of the names violations reports."""
    lifted = lifted_names(actual, metrics, exceptions, wide or {})
    return {(metric, name) for metric in METRICS for name, count in actual["metrics"].get(metric, {}).items()
            if count > metrics.get(metric, {}).get(name, 0) + exceptions.get(metric, {}).get(name, 0)
            and name not in lifted.get(metric, ())}


def violations(actual, metrics, exceptions, wide=None):
    """Lines for each name that is new or occurs more often than the baseline (plus its exception) allows, and was
    not lifted from outside the contract packages."""
    lines = []
    lifted = lifted_names(actual, metrics, exceptions, wide or {})
    for metric in METRICS:
        allowed = metrics.get(metric, {})
        extra = exceptions.get(metric, {})
        risen = []
        for name, count in sorted(actual["metrics"].get(metric, {}).items()):
            limit = allowed.get(name, 0) + extra.get(name, 0)
            if count > limit and name not in lifted.get(metric, ()):
                risen.append("{0} ({1} -> {2})".format(name, limit, count) if limit else name + " (new)")
        if risen:
            lines.append("wire types ratchet: {0}: {1} new weak name(s): {2}\n  fix: {3}. There is no ignore marker; "
                         "a weak type that must stay goes in {4} \"exceptions\" with a PR reference.".format(
                             metric, len(risen), ", ".join(risen), FIX[metric], BASELINE_REL))
    return lines


def lowerable(actual, metrics, wide=None):
    lines = ["{0} {1}: {2} -> {3}".format(metric, name, count, actual["metrics"].get(metric, {}).get(name, 0))
             for metric in METRICS for name, count in sorted(metrics.get(metric, {}).items())
             if actual["metrics"].get(metric, {}).get(name, 0) < count]
    lines += ["wide {0} {1}: {2} -> {3}".format(metric, name, count, actual["wide"].get(metric, {}).get(name, 0))
              for metric in FIELD_METRICS for name, count in sorted((wide or {}).get(metric, {}).items())
              if actual["wide"].get(metric, {}).get(name, 0) < count]
    return lines


def raised_against(candidate, previous, label, candidate_wide=None, previous_wide=None):
    """Lines for baseline entries ("metrics", and "wide" when the previous baseline has it) above the same entry of a
    previous baseline, or absent from it."""
    lines = []
    sections = [("", candidate, previous)]
    if previous_wide:
        sections.append(("wide ", candidate_wide or {}, previous_wide))
    for prefix, now, before in sections:
        for metric in METRICS:
            for name, count in sorted(now.get(metric, {}).items()):
                if count > before.get(metric, {}).get(name, 0):
                    lines.append("wire types ratchet: {0}{1} {2} is {3} in the baseline but {4} at {5}; a baseline "
                                 "never rises (an exception needs a PR reference)".format(
                                     prefix, metric, name, count, before.get(metric, {}).get(name, 0), label))
    return lines


def exceptions_grow(metrics, exceptions, previous_metrics, previous_exceptions, label):
    """Lines for each metric whose allowance (baseline counts plus exceptions) is above a previous baseline's. An
    exception may stand in for a name the same change removed (a rename: --update lowers the old name, the new one goes
    in "exceptions"), but never add room: #15561 let three new weak fields in through exceptions alone."""
    lines = []
    for metric in METRICS:
        def total(m, e):
            return sum(m.get(metric, {}).values()) + sum(e.get(metric, {}).values())
        now, before = total(metrics, exceptions), total(previous_metrics, previous_exceptions)
        added = sorted(n for n, c in exceptions.get(metric, {}).items() if c > previous_exceptions.get(metric, {}).get(n, 0))
        if added and now > before:
            lines.append("wire types ratchet: {0}: exceptions {1} add room ({2} -> {3} allowed at {4}); an exception "
                         "may only replace a name this change removes (lower the old one with --update). Type the new "
                         "field instead: {5}".format(metric, ", ".join(added), before, now, label, FIX[metric]))
    return lines


def render(metrics, exceptions_text, wide=None):
    data = {"metrics": {metric: dict(sorted(metrics[metric].items())) for metric in METRICS if metrics.get(metric)}}
    if wide:
        data["wide"] = {metric: dict(sorted(wide[metric].items())) for metric in FIELD_METRICS if wide.get(metric)}
    if exceptions_text:
        data["exceptions"] = exceptions_text
    return json.dumps(data, indent=1, sort_keys=False) + "\n"


def _base_metrics(root, base, env):
    try:
        merge_base = ratchet_base.resolve(root, base, env)
        if merge_base is None:
            return None
        text = merge_base.texts([BASELINE_REL]).get(BASELINE_REL)
    except ratchet_base.BaseError as error:
        raise WireTypesError("wire types ratchet: " + str(error))
    return (parse_baseline(text, merge_base.label + ":" + BASELINE_REL), merge_base.label) if text else None


def check(root, base=None, env=None, counter=run_counter, notes=None):
    """Violation lines. Empty means pass; notes receives the lowerable lines."""
    env = os.environ if env is None else env
    path = Path(root) / BASELINE_REL
    if not path.is_file():
        return ["wire types ratchet: baseline missing ({0}); refusing to invent counts".format(BASELINE_REL)]
    metrics, exceptions, wide = parse_baseline(path.read_text(encoding="utf-8"), BASELINE_REL)
    actual = counter(root)
    found = violations(actual, metrics, exceptions, wide)
    if notes is not None:
        notes.extend(lowerable(actual, metrics, wide))
        for metric, names in sorted(lifted_names(actual, metrics, exceptions, wide).items()):
            print("wire types ratchet: {0}: {1} name(s) moved into the contract packages from Core or a module, "
                  "neutral: {2}".format(metric, len(names), ", ".join(names)))
    previous = _base_metrics(root, base, env)
    if previous:
        (base_metrics, base_exceptions, base_wide), label = previous
        found.extend(raised_against(metrics, base_metrics, label, wide, base_wide))
        found.extend(exceptions_grow(metrics, exceptions, base_metrics, base_exceptions, label))
    return found


def update(root, base=None, env=None, counter=run_counter):
    """Lower the baseline to the current counts. Refuses to raise, to add a name, or to create the baseline."""
    env = os.environ if env is None else env
    path = Path(root) / BASELINE_REL
    if not path.is_file():
        raise WireTypesError("wire types ratchet: no baseline at {0}; refusing to create one".format(BASELINE_REL))
    metrics, exceptions, wide = parse_baseline(path.read_text(encoding="utf-8"), BASELINE_REL)
    actual = counter(root)
    refusals = violations(actual, metrics, exceptions, wide)
    if refusals:
        return refusals

    def lowered(limits, counts, metrics_list):
        planned = {}
        for metric in metrics_list:
            for name, count in limits.get(metric, {}).items():
                now = min(count, counts.get(metric, {}).get(name, 0))
                if now:
                    planned.setdefault(metric, {})[name] = now
        return planned

    planned = lowered(metrics, actual["metrics"], METRICS)
    planned_wide = lowered(wide, actual["wide"], FIELD_METRICS)
    raw = json.loads(path.read_text(encoding="utf-8")).get("exceptions")
    rendered = render(planned, raw, planned_wide)
    if path.read_text(encoding="utf-8") != rendered:
        path.write_text(rendered, encoding="utf-8")
        print("wire types ratchet: lowered the baseline to {0} name(s), {1} wide".format(
            sum(len(v) for v in planned.values()), sum(len(v) for v in planned_wide.values())))
    else:
        print("wire types ratchet: baseline already matches")
    return []


def summary(metrics):
    return ", ".join("{0} {1}".format(metric, sum(metrics.get(metric, {}).values())) for metric in METRICS)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--base", help="base ref (default: CI_BASE_SHA); the baseline may not rise against its merge base")
    parser.add_argument("--root", default=str(Path(__file__).resolve().parents[2]), help="repository root")
    parser.add_argument("--update", action="store_true", help="lower the baseline; refuses to raise or add a name")
    parser.add_argument("--print", action="store_true", help="print the current counts per metric and exit")
    args = parser.parse_args(argv)
    notes = []
    try:
        if args.print:
            print(summary(run_counter(args.root)["metrics"]))
            return 0
        problems = update(args.root, args.base) if args.update else check(args.root, base=args.base, notes=notes)
    except WireTypesError as error:
        print(error, file=sys.stderr)
        return 2
    if problems:
        print("\n".join(problems), file=sys.stderr)
        return 1
    if not args.update:
        ratchet_base.print_notice("wire types ratchet", notes, "python3 scripts/ci/wire_types_ratchet.py --update")
        metrics = parse_baseline((Path(args.root) / BASELINE_REL).read_text(encoding="utf-8"))[0]
        print("wire types ratchet: none above the baseline ({0})".format(summary(metrics)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
