"""Observe only the regular visual app-host launch inside its existing 600-second step."""

import argparse
from datetime import datetime, timedelta, timezone
import json
import re
import selectors
import signal
import subprocess
from pathlib import Path

import apple_launch_diagnostics as launch
import apple_readiness_watch as watch

VISUAL_SECONDS = launch.VISUAL_SECONDS
SAMPLE_SECONDS = 15
SYSTEM_AFTER = 60
FINISH_BEFORE = 35
VISUAL_PROCESSES = ("Persona", "debugserver", "launchd_sim")


def scoped_processes(udid, runner=None, owned_debuggers=None, process_identities=None):
    """Return owned host/debugger metadata, never command arguments or environment.

    A debugger can precede its Persona target under this simulator's launchd. Keep
    that validated process for this invocation when its short-lived parent exits.
    The process start time distinguishes PID reuse; neither timestamp nor command
    arguments are exported. Once a successful snapshot observes a debugger absent,
    its continuous-presence retention ends; reappearing with parent 1 or the same
    one-second start timestamp then requires current owned ancestry again.
    Private identities can be retained separately for the immediate thread check.
    """
    runner = runner or watch.subprocess.run
    owned_debuggers = {} if owned_debuggers is None else owned_debuggers
    if (not isinstance(owned_debuggers, dict) or
            not all(isinstance(key, int) and isinstance(value, str)
                    for key, value in owned_debuggers.items())):
        raise ValueError("owned debugger identities must map integer PIDs to start timestamps")
    if process_identities is not None:
        process_identities.clear()
    try:
        result = runner(["ps", "-axo", "pid=,ppid=,lstart=,stat=,command="],
                        capture_output=True, text=True, timeout=5)
    except watch.subprocess.TimeoutExpired:
        return {"query_status": "query-timeout", "processes": []}
    except (OSError, watch.subprocess.SubprocessError):
        return {"query_status": "query-failed", "processes": []}
    if result.returncode:
        return {"query_status": "query-failed", "processes": []}
    process_pattern = re.compile(r"^\s*(\d+)\s+(\d+)\s+(.{24})\s+(\S+)\s+([^\n]*?)\s*$")
    host_pattern = re.compile(r".*/CoreSimulator/Devices/" + re.escape(udid) +
                              r"/[^\n]*/Persona\.app/Persona$")
    records = []
    for line in result.stdout.splitlines():
        match = process_pattern.match(line)
        if not match:
            continue
        pid, ppid, started, state, command = match.groups()
        executable = command.split(None, 1)[0] if command else ""
        name = executable.rsplit("/", 1)[-1]
        # launchd_sim records the simulator data directory as its first command
        # token. Match that directory separately from its executable token.
        launcher = re.search(r"(?:^|\s)(\S*launchd_sim)(?:\s|$)", command)
        if launcher:
            executable = launcher.group(1)
            name = "launchd_sim"
        if name in VISUAL_PROCESSES:
            records.append((int(pid), int(ppid), started, state, name, executable, command))
    hosts = {pid for pid, _ppid, _started, _state, name, path, _command in records
             if name == "Persona" and host_pattern.fullmatch(path)}
    launchers = {pid for pid, _ppid, _started, _state, name, _path, command in records
                 if name == "launchd_sim" and re.match(
                     r"^/Library/Developer/CoreSimulator/Devices/" + re.escape(udid) + r"/(?:\s|$)", command)}
    processes = []
    unowned = set()
    for pid, ppid, started, state, name, _path, _command in records:
        if name == "Persona" and pid in hosts:
            process = {"pid": pid, "ppid": ppid, "state": state, "name": name}
            processes.append(process)
            if process_identities is not None:
                process_identities[(pid, name)] = (started, ppid)
        elif name == "debugserver":
            retained = owned_debuggers.get(pid) == started
            if ppid in hosts or ppid in launchers:
                owned_debuggers[pid] = started
            elif not retained or (ppid != 1 and ppid not in hosts):
                # Equal-second lstart reuse must not inherit ownership. Parent 1
                # is the expected reparenting of a still-live debugger; another
                # non-owned parent requires a fresh owned relationship.
                owned_debuggers.pop(pid, None)
                if process_identities is not None:
                    process_identities[(pid, "debugserver")] = (started, ppid)
                    unowned.add(pid)
                continue
            process = {"pid": pid, "ppid": ppid, "state": state, "name": name}
            processes.append(process)
            if process_identities is not None:
                process_identities[(pid, name)] = (started, ppid)
    present_debuggers = {pid: started for pid, _ppid, started, _state, name, _path, _command in records
                         if name == "debugserver"}
    missing_debuggers = [pid for pid in owned_debuggers
                         if pid not in unowned and present_debuggers.get(pid) != owned_debuggers[pid]]
    processes.extend({"pid": pid, "state": "not-present", "name": "debugserver"}
                     for pid in missing_debuggers)
    for pid in missing_debuggers:
        # This observation ends continuous presence. Neither equal-second lstart
        # nor parent 1 can transfer the old ownership to a later replacement.
        owned_debuggers.pop(pid, None)
    return {"query_status": "completed", "processes": sorted(processes, key=lambda item: (item["name"], item["pid"]))}


def same_process_identity(actual, identity, process_name):
    """Compare current PID/name/start/ancestry without accepting missing evidence.

    Reparenting a retained debugger to 1 preserves the identity when its
    short-lived launcher exits. A host observed as that debugger's parent
    does not become interchangeable with PID 1 through this exception.
    """
    return actual is not None and actual[0] == identity[0] and (
        actual[1] == identity[1] or
        (process_name == "debugserver" and actual[1] == 1 and identity[1] != 1))


def current_identity(udid, runner, owned_debuggers, process_identities, key, identity):
    """Require the exact validated process identity after refreshing state.

    A missing identity is a failed check, not a reason to retain the process as
    current owned evidence or publish a thread sample captured across its exit.
    """
    current = scoped_processes(udid, runner, owned_debuggers, process_identities)
    if current["query_status"] == "completed" and same_process_identity(
            process_identities.get(key), identity, key[1]):
        return True
    owned_debuggers.pop(key[0], None)
    return False


def sample_processes(udid, destination, index, runner=watch.subprocess.run, owned_debuggers=None,
                     process_identities=None):
    if owned_debuggers is None:
        owned_debuggers = {}
    if process_identities is None:
        process_identities = {}
    observation = scoped_processes(udid, runner, owned_debuggers, process_identities)
    # Revalidation clears the shared current map. Keep this sample's identities
    # immutable so another process's capture cannot erase the next one's baseline.
    snapshot_identities = dict(process_identities)
    label = f"process-{index:03d}"
    launch.publish(destination / (label + ".json"), json.dumps({
        "captured_at": datetime.now(timezone.utc).isoformat(), **observation,
        "note": "Process presence is not a launch, test-bundle or readiness verdict."}, indent=2) + "\n")
    for process in observation["processes"]:
        if "pid" not in process or "ppid" not in process:
            continue
        key = (process["pid"], process["name"])
        # A successful snapshot must carry the exact private identity of every
        # process it exposes for capture. Never infer one from a public row.
        if key not in snapshot_identities:
            raise RuntimeError("validated process is missing its private identity")
        identity = snapshot_identities[key]
        path = destination / (label + f"-{process['name']}-{process['pid']}-threads.txt")
        status = path.with_suffix(path.suffix + ".status.txt")
        # Revalidate immediately before ps -M rather than relying on the earlier
        # metadata snapshot. Reject a mismatch after temporary capture, before its
        # result is published as current evidence. Portable ps offers no atomic
        # identity-and-thread snapshot; this narrows the remaining kernel race.
        if not current_identity(udid, runner, owned_debuggers, process_identities, key, identity):
            launch.publish(path, "unavailable: process identity changed before thread capture\n")
            launch.publish_status(status, "identity-mismatch-before-capture\n")
            continue
        code = watch.bounded_file(["ps", "-M", str(process["pid"])], path, timeout=5)
        if code:
            continue
        if not current_identity(udid, runner, owned_debuggers, process_identities, key, identity):
            launch.publish(path, "unavailable: process identity changed during thread capture\n")
            launch.publish_status(status, "identity-mismatch-after-capture\n")
    return observation


def launch_system_lines(text, bundle):
    kept = []
    selected = False
    for line in text.splitlines():
        if re.fullmatch(r"[A-Za-z0-9_.-]+", line):
            selected = line == bundle
            if selected:
                kept.append(bundle + "\n")
        elif selected and (match := re.fullmatch(
                r"\s*(state|pid|runs|last exit code)\s*=\s*([A-Za-z0-9_.:-]+)\s*", line, re.I)):
            kept.append(f"{match.group(1).lower()} = {match.group(2)}\n")
    return kept


def launch_system_projection(raw, bundle):
    """Project only fixed state fields from a matching service block.

    Never return arbitrary launchctl lines: program, path, arguments and environment
    values can carry credentials without using a secret-like keyword.
    """
    try:
        text = raw.decode()
    except UnicodeDecodeError:
        return []
    return launch_system_lines(text, bundle)


def launch_system(udid, destination, bundle=launch.BUNDLE):
    """Filter while reading; even a silent or noisy launchctl cannot outlive its bound."""
    path = destination / "launch-system-after-60s.txt"
    status = path.with_suffix(path.suffix + ".status.txt")
    launch.publish(status, "incomplete\n")
    process = None
    try:
        with path.with_suffix(path.suffix + ".command").open("wb") as output:
            process = subprocess.Popen(
                ["xcrun", "simctl", "spawn", udid, "launchctl", "print", "system"],
                stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
            deadline = watch.time.monotonic() + 10
            kept = 0
            partial = bytearray()

            def export(raw):
                nonlocal kept
                for line in launch_system_lines(raw, bundle):
                    piece = line.encode()
                    if kept + len(piece) > watch.MAX_SAMPLE_BYTES:
                        raise launch.OutputLimitError("launch system output exceeded byte limit")
                    output.write(piece)
                    kept += len(piece)

            with selectors.DefaultSelector() as selector:
                selector.register(process.stdout, selectors.EVENT_READ)
                while selector.get_map():
                    remaining = deadline - watch.time.monotonic()
                    if remaining <= 0:
                        raise subprocess.TimeoutExpired(process.args, 10)
                    for key, _ in selector.select(remaining):
                        chunk = launch.os.read(key.fd, 4096)
                        if not chunk:
                            selector.unregister(key.fileobj)
                            continue
                        partial.extend(chunk)
                        if len(partial) > watch.MAX_SAMPLE_BYTES:
                            raise launch.OutputLimitError("launch system output exceeded byte limit")
            if process.wait(timeout=max(0, deadline - watch.time.monotonic())):
                raise subprocess.CalledProcessError(process.returncode, process.args)
            export(partial.decode())
        if not path.with_suffix(path.suffix + ".command").stat().st_size:
            raise launch.OutputLimitError("no matching bounded launch service state")
        path.with_suffix(path.suffix + ".command").replace(path)
        launch.publish(status, "0\n")
        return 0
    except subprocess.TimeoutExpired:
        launch.publish(status, "124\n")
        launch.publish(path, "unavailable: command timed out\n")
        return 124
    except launch.OutputLimitError:
        path.unlink(missing_ok=True)
        launch.publish_status(status, "output-limit\n")
        return 125
    except (OSError, subprocess.SubprocessError):
        launch.publish_status(status, "unavailable: command-failure\n")
        return 127
    finally:
        if process is not None:
            if process.poll() is None:
                process.kill()
                process.wait()
            process.stdout.close()


def observe(udid, bundle, destination, seconds=VISUAL_SECONDS, scope_root=None,
            monotonic=watch.time.monotonic, sleep=watch.time.sleep):
    launch.identity(udid, bundle)
    if seconds > VISUAL_SECONDS:
        raise ValueError("visual observation cannot exceed the existing step deadline")
    destination.mkdir(parents=True, exist_ok=True)
    launch.publish(destination / "observer.status.txt",
                   "collection incomplete; observation only; no launch cause established\n")
    launch.publish(destination / "launch-system-after-60s.txt", "unavailable: observation incomplete\n")
    launch.publish(destination / "launch-system-after-60s.txt.status.txt", "incomplete\n")
    deadline = monotonic() + seconds
    started = monotonic()
    stopped = (destination / "observer.stop").exists()
    live = None
    index = 0
    system_captured = False
    owned_debuggers = {}
    process_identities = {}
    try:
        try:
            if not stopped:
                live = watch.start_live_log(scope_root, destination,
                                            deadline=datetime.now(timezone.utc) + timedelta(seconds=seconds))
            while not stopped and monotonic() < deadline - FINISH_BEFORE:
                if (destination / "observer.stop").exists():
                    stopped = True
                    break
                now = monotonic()
                if now - started >= index * SAMPLE_SECONDS:
                    observation = sample_processes(udid, destination, index, owned_debuggers=owned_debuggers,
                                                   process_identities=process_identities)
                    watch.correlate_live(live, {"host_pids": [p["pid"] for p in observation["processes"]
                                                               if p["name"] == "Persona"]})
                    index += 1
                if not system_captured and now - started >= SYSTEM_AFTER:
                    launch_system(udid, destination)
                    system_captured = True
                sleep(min(1, max(0, deadline - FINISH_BEFORE - monotonic())))
        except KeyboardInterrupt:
            stopped = True
        if not system_captured and not stopped:
            launch_system(udid, destination)
        launch.collect(destination.parent, destination, scope_root=scope_root)
        launch.publish(destination / "observer.status.txt",
                       ("xcodebuild-finished" if stopped else "visual-deadline") +
                       "; observation only; no launch cause established\n")
    finally:
        watch.stop_live_log(live)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--udid", required=True)
    parser.add_argument("--bundle-id", required=True)
    parser.add_argument("--scope-root", required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--seconds", type=float, default=VISUAL_SECONDS)
    args = parser.parse_args()
    if not 0 < args.seconds <= VISUAL_SECONDS:
        parser.error("observation cannot exceed the visual step deadline")
    for sig in (signal.SIGINT, signal.SIGTERM):
        signal.signal(sig, watch.interrupted)
    observe(args.udid, args.bundle_id, args.output, args.seconds, scope_root=args.scope_root)


if __name__ == "__main__":
    main()
