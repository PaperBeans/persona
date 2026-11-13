#!/usr/bin/env python3
"""Run resident reloads against an isolated real hub/node and disposable Postgres.

This driver accepts a binary, never an existing hub URL or credentials. Raw logs
stay in its private temporary directory; only synthetic aggregate readings are printed.
"""
import argparse
import concurrent.futures
import importlib.util
import json
import os
from pathlib import Path
import secrets
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import threading
import time
import urllib.request


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def run(args, timeout=30, **kwargs):
    result = subprocess.run(args, capture_output=True, text=True, timeout=timeout, **kwargs)
    if result.returncode:
        raise RuntimeError("command failed: " + args[0] + " (exit " + str(result.returncode) + ")")
    return result.stdout


def wait(check, seconds, label):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        try:
            if check():
                return
        except (OSError, ValueError, urllib.error.URLError):
            pass
        time.sleep(0.02)
    raise TimeoutError(label)


def relay_pids(root_pid):
    # Walk only the isolated tree, including children spawned by non-leader Go threads.
    # Scanning every host process adds unrelated observer latency on a shared machine.
    found, pending, visited = [], [root_pid], set()
    while pending:
        pid = pending.pop()
        if pid in visited:
            continue
        visited.add(pid)
        path = Path("/proc") / str(pid)
        try:
            args = (path / "cmdline").read_bytes().decode(errors="replace").split("\0")
            if args[1:3] == ["node", "--stdio"]:
                found.append(pid)
            for thread in (path / "task").iterdir():
                try:
                    pending.extend(int(child) for child in (thread / "children").read_text().split())
                except (OSError, ValueError):
                    continue
        except (OSError, ValueError):
            continue
    return sorted(found)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True)
    parser.add_argument("--iterations", type=int, default=5)
    parser.add_argument("--max-gap", type=float, default=1.0,
                        help="maximum observed node RPC wait in seconds, including safe retries (default: 1)")
    args = parser.parse_args()
    if sys.platform != "linux":
        parser.error("the process-tree observer requires Linux /proc")
    if not 1 <= args.iterations <= 100 or not 0 < args.max_gap < 5:
        parser.error("iterations must be 1..100 and max-gap must be below the existing five-second grace")
    root = Path(tempfile.mkdtemp(prefix="nr5277-"))
    home, state = root / "home", root / "home/.persona"
    state.mkdir(parents=True, mode=0o700)
    binary = home / "bin/personad"
    binary.parent.mkdir()
    shutil.copyfile(args.binary, binary)
    binary.chmod(0o755)
    port, pgport = free_port(), free_port()
    token, password = secrets.token_hex(24), secrets.token_hex(24)
    container = "persona-reload-lab-" + secrets.token_hex(6)
    config = {"listen": "127.0.0.1:" + str(port), "token": token,
              "database_url": "postgres://postgres:" + password + "@127.0.0.1:" + str(pgport) + "/persona?sslmode=disable",
              "auto_update_nodes": False, "update": {"url": "off"}, "node_packages": {"url": "off"},
              "machines": [{"id": "example-machine", "name": "Test computer", "kind": "local",
                            "remote_bin": str(binary), "herdr_autostart": False, "botmux": False,
                            "usage_windows": False, "codex_discovery": False}]}
    (state / "hub.json").write_text(json.dumps(config))
    (state / "hub.json").chmod(0o600)
    spec = importlib.util.spec_from_file_location("persona_lab_environment", Path(__file__).with_name("server_test_env.py"))
    environment = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(environment)
    env = environment.clean_environment(os.environ)
    for key in ("INVOCATION_ID", "XPC_SERVICE_NAME"):
        env.pop(key, None)
    env.update(HOME=str(home), PERSONA_HOME=str(state), PERSONA_HUB_EDGE="1")
    hub = "http://127.0.0.1:" + str(port)
    counts = {"state_samples": 0, "offline_samples": 0, "state_errors": 0, "node_requests": 0, "node_errors": 0}
    samples, gaps, readiness, processes, logs = [], [], [], [], []
    stop, lock = threading.Event(), threading.Lock()
    created, verdict, completed = False, "FAIL", 0

    def api(path):
        req = urllib.request.Request(hub + path, headers={"Authorization": "Bearer " + token})
        with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(req, timeout=25) as response:
            return json.load(response)

    def online():
        data = api("/api/v1/machines")
        rows = data.get("machines", []) if isinstance(data, dict) else data
        return len(rows) == 1 and bool(rows[0].get("online"))

    def probe(kind):
        while not stop.is_set():
            began, error, status, code = time.monotonic(), None, None, None
            try:
                if kind == "state":
                    if not online():
                        error = "offline_samples"
                else:
                    api("/api/v1/machines/example-machine/claude-settings")
            except urllib.error.HTTPError as exc:
                error = "state_errors" if kind == "state" else "node_errors"
                status = exc.code
                try:
                    code = json.loads(exc.read(4096)).get("code")
                except (ValueError, AttributeError):
                    code = "unclassified"
            except Exception:
                error = "state_errors" if kind == "state" else "node_errors"
            with lock:
                counts["state_samples" if kind == "state" else "node_requests"] += 1
                if error:
                    counts[error] += 1
                samples.append({"probe": kind, "error": error, "status": status, "code": code,
                                "duration_ms": round((time.monotonic() - began) * 1000)})
            stop.wait(0.02)

    try:
        run(["docker", "run", "-d", "--name", container, "-e", "POSTGRES_PASSWORD=" + password,
             "-e", "POSTGRES_DB=persona", "-p", "127.0.0.1:" + str(pgport) + ":5432", "postgres:18-alpine"], timeout=60)
        created = True
        wait(lambda: subprocess.run(["docker", "exec", container, "pg_isready", "-U", "postgres", "-d", "persona"],
                                    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=5).returncode == 0,
             45, "isolated database readiness")
        for label, command in [("resident", [str(binary), "node", "--attach", "--supervised"]), ("hub", [str(binary), "hub"])]:
            log = open(root / (label + ".log"), "wb")
            logs.append(log)
            processes.append(subprocess.Popen(command, cwd=home, env=env, stdin=subprocess.DEVNULL,
                                              stdout=log, stderr=log, start_new_session=True))
        wait(online, 100, "isolated machine readiness")
        api("/api/v1/machines/example-machine/claude-settings")
        wait(lambda: len(relay_pids(processes[1].pid)) == 1, 5, "isolated relay readiness")
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            futures = [pool.submit(probe, kind) for kind in ("state", "node")]
            try:
                for iteration in range(args.iterations):
                    before = relay_pids(processes[1].pid)
                    inode = (state / "node-attach.sock").stat().st_ino
                    with lock:
                        first_sample = len(samples)
                    began = time.monotonic()
                    os.kill(processes[0].pid, signal.SIGHUP)
                    def relay_changed():
                        current = relay_pids(processes[1].pid)
                        return len(current) == 1 and current != before
                    wait(relay_changed, 15, "resident executable handover")
                    request_began = time.monotonic()
                    api("/api/v1/machines/example-machine/claude-settings")
                    request_wait = time.monotonic() - request_began
                    handover = time.monotonic() - began
                    same_socket = (state / "node-attach.sock").stat().st_ino == inode
                    # Signal-to-readiness includes preflight and idle-process drain, during which the old node still
                    # answers requests. Bound actual RPC waiting separately; neither number proves zero disconnects.
                    with lock:
                        observed = [r["duration_ms"] / 1000 for r in samples[first_sample:] if r["probe"] == "node"]
                        reading = dict(counts)
                    gap = max([request_wait, *observed])
                    gaps.append(round(gap, 4))
                    readiness.append(round(handover, 4))
                    completed += 1
                    print(json.dumps({"reload": iteration + 1, "max_node_rpc_wait_seconds": round(gap, 4),
                                      "signal_to_readiness_seconds": round(handover, 4), "same_attach_socket": same_socket,
                                      "online": online(), "counts": reading}, sort_keys=True), flush=True)
                    if not same_socket or gap >= args.max_gap:
                        raise RuntimeError("resident handover did not meet its socket/RPC-wait contract")
                    stop.wait(0.5)
            finally:
                stop.set()
                for future in futures:
                    future.result(timeout=30)
        rpc_waits = [r["duration_ms"] / 1000 for r in samples if r["probe"] == "node"]
        verdict = "PASS" if (not any(counts[k] for k in ("offline_samples", "state_errors", "node_errors"))
                             and max(rpc_waits, default=0) < args.max_gap) else "FAIL"
    except TimeoutError as exc:
        verdict = "TIMEOUT"
        print(str(exc), flush=True)
    except Exception as exc:
        print(type(exc).__name__ + ": " + str(exc), flush=True)
    finally:
        stop.set()
        for process in reversed(processes):
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGTERM)
                try:
                    process.wait(timeout=20)
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait(timeout=5)
        for log in logs:
            log.close()
        if created:
            try:
                diagnostic = subprocess.run(["docker", "logs", "--tail", "100", container], capture_output=True, text=True, timeout=10)
                (root / "database.log").write_text(diagnostic.stdout + diagnostic.stderr)
                (root / "database-inspect.json").write_text(run(["docker", "inspect", container], timeout=10))
            except (OSError, RuntimeError, subprocess.TimeoutExpired):
                print("isolated database diagnostic collection failed", flush=True)
            try:
                run(["docker", "rm", "-fv", container], timeout=30)
            except (OSError, RuntimeError, subprocess.TimeoutExpired):
                verdict = "FAIL"
                print("isolated database cleanup failed", flush=True)
        max_rpc_wait = max([0, *gaps, *(r["duration_ms"] / 1000 for r in samples if r["probe"] == "node")])
        report = {"verdict": verdict, "completed_reloads": completed, "max_node_rpc_wait_seconds": round(max_rpc_wait, 4),
                  "node_rpc_waits_seconds": gaps, "signal_to_readiness_seconds": readiness, "counts": counts}
        (root / "summary.json").write_text(json.dumps(report, indent=2))
        (root / "samples.json").write_text(json.dumps(samples, indent=2))
        print("private evidence: " + str(root), flush=True)
        print(verdict + ": " + json.dumps(report, sort_keys=True), flush=True)
    return 0 if verdict == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
