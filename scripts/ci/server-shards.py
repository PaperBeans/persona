#!/usr/bin/env python3
import argparse
import json
from pathlib import Path
import subprocess
import sys
import threading

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts/test"))
import go_partitions  # noqa: E402  (internal/node's partitions, shared with the release build)
SHARDS = ("node", "managers", "other")


def shard_for(package):
    for name in ("node", "hub", "voice", "assistant"):
        suffix = "/internal/" + name
        if package.endswith(suffix) or suffix + "/" in package:
            return "node" if name == "node" else "managers"
    return "other"


def select_packages(packages, shard):
    if shard not in SHARDS:
        raise ValueError(f"Unknown server shard: {shard}")
    return [package for package in packages if shard_for(package) == shard]


def commands(packages, shard, database_file, node_patterns=()):
    """The go test processes a shard runs at once: one for its packages, and in the node shard one more per
    internal/node partition (go_partitions; the same split as the release build). Each keeps -p and -timeout."""
    workers = {"node": 1, "managers": 3, "other": 2}[shard]
    prefix = [sys.executable, str(ROOT / "scripts/test/server_test_env.py"), "--database-file", database_file,
              "--", "go", "test", "-json", "-p", str(workers), "-count=1", "-timeout=10m"]
    if shard != "node":
        return [prefix + packages]
    rest = [package for package in packages if not go_partitions.is_partitioned(package)]
    if len(rest) == len(packages):
        return [prefix + packages]
    if not node_patterns:
        raise ValueError("internal/node needs its partitions")
    return ([prefix + rest] if rest else []) + [prefix + ["-run", pattern, go_partitions.PACKAGE]
                                                 for pattern in node_patterns]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("shard", choices=SHARDS)
    parser.add_argument("--database-file", required=True)
    args = parser.parse_args()
    listed = subprocess.check_output(["go", "list", "./..."], cwd=ROOT / "server", text=True, timeout=120)
    packages = select_packages(listed.splitlines(), args.shard)
    if not packages:
        parser.error(f"No packages in server shard {args.shard}")
    node_patterns = go_partitions.list_patterns(ROOT / "server") if args.shard == "node" else ()
    runs = commands(packages, args.shard, args.database_file, node_patterns)
    slow, lock = [], threading.Lock()

    def follow(process):
        for line in process.stdout:
            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                with lock:
                    print(line, end="", flush=True)
                continue
            with lock:
                if event.get("Action") == "output":
                    print(event.get("Output", ""), end="", flush=True)
                if event.get("Action") in ("pass", "fail") and event.get("Test"):
                    slow.append((event.get("Elapsed", 0), event["Package"], event["Test"]))

    processes = [subprocess.Popen(command, cwd=ROOT / "server", stdout=subprocess.PIPE, text=True) for command in runs]
    readers = [threading.Thread(target=follow, args=(process,)) for process in processes]
    try:
        for reader in readers:
            reader.start()
        for reader in readers:
            reader.join()
        status = next((code for code in [process.wait() for process in processes] if code), 0)
    finally:
        for process in processes:
            process.stdout.close()
            if process.poll() is None:
                process.terminate()
                process.wait(timeout=10)
    print(f"Server shard {args.shard}: {len(packages)} packages in {len(runs)} go test processes; exit {status}")
    print("Slowest test events (parent/subtest durations may overlap):")
    for elapsed, package, test in sorted(slow, reverse=True)[:20]:
        print(f"SLOW {elapsed:.3f}s {package} {test}")
    return status


if __name__ == "__main__":
    sys.exit(main())
