#!/usr/bin/env python3
"""Bind each Android lane to a run, then publish one exact-source aggregate result."""
import argparse
import json
from pathlib import Path
import sys

LANES = ("lint-build-test-0", "test-1")
SCHEMA = 1


def _json(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(path.name + ".tmp")
    temporary.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")
    temporary.replace(path)


def bind_receipt(commit, lane, checks_outcome, evidence_outcome, job_status, run_id, run_attempt):
    if lane not in LANES:
        raise ValueError("unknown Android lane: " + lane)
    if checks_outcome == evidence_outcome == job_status == "success":
        outcome, verified = "success", True
        reason = "lane checks and exact-source evidence completed"
    elif checks_outcome == "failure":
        outcome, verified = "failure", False
        reason = "Android checks failed"
    elif checks_outcome == "success" and evidence_outcome != "success":
        outcome, verified = "unavailable", False
        reason = "checks completed but exact-source evidence was not uploaded"
    else:
        outcome, verified = "unavailable", False
        reason = "lane did not complete checks and result publication"
    return {
        "schema": SCHEMA,
        "commit": commit,
        "lane": lane,
        "run_id": run_id,
        "run_attempt": run_attempt,
        "checks_outcome": checks_outcome,
        "evidence_outcome": evidence_outcome,
        "job_status": job_status,
        "outcome": outcome,
        "verified": verified,
        "reason": reason,
    }


def _lane_result(path, lane, commit, run_id, run_attempt):
    try:
        receipt = json.loads(path.read_text())
    except (OSError, ValueError) as error:
        return {"status": "invalid", "reason": "unreadable receipt: " + str(error)}
    if not isinstance(receipt, dict):
        return {"status": "invalid", "reason": "receipt is not an object"}
    expected = {
        "schema": SCHEMA,
        "commit": commit,
        "lane": lane,
        "run_id": run_id,
        "run_attempt": run_attempt,
    }
    for field, value in expected.items():
        if receipt.get(field) != value:
            return {
                "status": "invalid",
                "reason": f"receipt {field} is {receipt.get(field)!r}, expected {value!r}",
            }
    outcome = receipt.get("outcome")
    verified = receipt.get("verified")
    completed = (
        receipt.get("checks_outcome")
        == receipt.get("evidence_outcome")
        == receipt.get("job_status")
        == "success"
    )
    if outcome == "success" and verified is True and completed:
        return {"status": "verified", "reason": receipt.get("reason", "lane checks completed")}
    if outcome == "failure" and verified is False and receipt.get("checks_outcome") == "failure":
        return {"status": "failed", "reason": receipt.get("reason", "Android checks failed")}
    if outcome == "unavailable" and verified is False and receipt.get("checks_outcome") != "failure":
        return {"status": "unavailable", "reason": receipt.get("reason", "lane result is unavailable")}
    return {"status": "invalid", "reason": "receipt outcome and verified fields disagree"}


def verify(commit, lanes_dir, run_id, run_attempt):
    lanes = {}
    for lane in LANES:
        paths = sorted(lanes_dir.rglob(f"receipt-{lane}.json")) if lanes_dir.is_dir() else []
        if not paths:
            lanes[lane] = {
                "status": "unavailable",
                "reason": "no run-bound receipt was published; the runner or result publication ended first",
            }
        elif len(paths) > 1:
            lanes[lane] = {"status": "invalid", "reason": "more than one receipt was published for this lane"}
        else:
            lanes[lane] = _lane_result(paths[0], lane, commit, run_id, run_attempt)
    statuses = [value["status"] for value in lanes.values()]
    if "invalid" in statuses:
        status = "invalid"
    elif "unavailable" in statuses and any(value != "unavailable" for value in statuses):
        status = "incomplete"
    elif "unavailable" in statuses:
        status = "unavailable"
    elif "failed" in statuses:
        status = "failed"
    else:
        status = "verified"
    return {
        "schema": SCHEMA,
        "commit": commit,
        "run_id": run_id,
        "run_attempt": run_attempt,
        "status": status,
        "verified": status == "verified",
        "lanes": lanes,
    }


def bind(args):
    receipt = bind_receipt(
        args.commit,
        args.lane,
        args.checks_outcome,
        args.evidence_outcome,
        args.job_status,
        args.run_id,
        args.run_attempt,
    )
    _json(args.output, receipt)
    print(f"android-result: bound {args.lane} {receipt['outcome']} for {args.commit}")
    if not receipt["verified"]:
        print(f"android-result: FAILED ({args.lane} did not pass; receipt is diagnostic)", file=sys.stderr)
        return 1
    return 0


def publish(args):
    result = verify(args.commit, args.lanes_dir, args.run_id, args.run_attempt)
    _json(args.output, result)
    print(f"android-result: {result['status']} for {args.commit}")
    for lane in LANES:
        value = result["lanes"][lane]
        print(f"android-result: {lane}: {value['status']}: {value['reason']}")
    if not result["verified"]:
        print(f"android-result: FAILED (aggregate status {result['status']})", file=sys.stderr)
        return 1
    return 0


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)

    bind_parser = subparsers.add_parser("bind", help="write one lane's run-bound receipt")
    bind_parser.add_argument("--commit", required=True)
    bind_parser.add_argument("--lane", required=True, choices=LANES)
    bind_parser.add_argument("--checks-outcome", required=True)
    bind_parser.add_argument("--evidence-outcome", required=True)
    bind_parser.add_argument("--job-status", required=True)
    bind_parser.add_argument("--run-id", required=True)
    bind_parser.add_argument("--run-attempt", required=True)
    bind_parser.add_argument("--output", required=True, type=Path)
    bind_parser.set_defaults(handler=bind)

    publish_parser = subparsers.add_parser("verify", help="verify every lane and write the aggregate result")
    publish_parser.add_argument("--commit", required=True)
    publish_parser.add_argument("--lanes-dir", required=True, type=Path)
    publish_parser.add_argument("--run-id", required=True)
    publish_parser.add_argument("--run-attempt", required=True)
    publish_parser.add_argument("--output", required=True, type=Path)
    publish_parser.set_defaults(handler=publish)

    args = parser.parse_args(argv)
    try:
        return args.handler(args)
    except (OSError, ValueError) as error:
        print(f"android-result: FAILED ({error})", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
