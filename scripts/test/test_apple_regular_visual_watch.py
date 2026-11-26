"""The regular visual observer stays scoped, read-only and inside the existing deadline."""

import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts/ci"))
import apple_regular_visual_watch as visual

UDID = "11111111-1111-1111-1111-111111111111"
OTHER = "22222222-2222-2222-1111-111111111111"
START = "Wed Nov 25 12:00:00 2026"
REUSED = "Wed Nov 25 12:01:00 2026"


class ProcessScopeTests(unittest.TestCase):
    def test_only_owned_host_and_its_debugger_have_public_metadata(self):
        owned = f"/Library/Developer/CoreSimulator/Devices/{UDID}/Application/X/Persona.app/Persona"
        foreign = owned.replace(UDID, OTHER)
        def line(pid, ppid, command, started=START, state="S"):
            return f" {pid} {ppid} {started} {state} {command}"
        result = subprocess.CompletedProcess([], 0, "\n".join([
            line(41, 1, owned, state="Ss"), line(42, 41, "/usr/libexec/debugserver"),
            line(43, 999, "/usr/libexec/debugserver"), line(44, 1, foreign),
            line(45, 1, "/private/path/Persona --private-argument"),
            line(46, 1, f"/Library/Developer/CoreSimulator/Devices/{UDID}/ --some-launchd-argument launchd_sim"),
        ]), "")
        observation = visual.scoped_processes(UDID, lambda *args, **kwargs: result)
        self.assertEqual(observation, {"query_status": "completed", "processes": [
            {"pid": 41, "ppid": 1, "state": "Ss", "name": "Persona"},
            {"pid": 42, "ppid": 41, "state": "S", "name": "debugserver"},
        ]})
        self.assertNotIn("/private", str(observation))
        self.assertNotIn(OTHER, str(observation))

    def test_owned_launchd_debugger_is_retained_when_its_short_lived_parent_exits(self):
        owned = f"/Library/Developer/CoreSimulator/Devices/{UDID}/Application/X/Persona.app/Persona"
        launcher = f"/Library/Developer/CoreSimulator/Devices/{UDID}/ --launchd /usr/sbin/launchd_sim"
        foreign_launcher = launcher.replace(UDID, OTHER)
        def line(pid, ppid, command, started=START, state="S"):
            return f" {pid} {ppid} {started} {state} {command}"
        snapshots = iter([
            "\n".join([line(46, 1, launcher), line(42, 46, "/usr/libexec/debugserver"),
                       line(47, 1, foreign_launcher), line(43, 47, "/usr/libexec/debugserver")]),
            line(41, 46, owned, state="Ss") + "\n" + line(42, 1, "/usr/libexec/debugserver"),
            line(41, 1, owned, state="Ss") + "\n" + line(42, 1, "/usr/libexec/debugserver"),
            line(41, 1, owned, state="Ss") + "\n" + line(42, 1, "/usr/libexec/debugserver"),
        ])
        def runner(*args, **kwargs):
            return subprocess.CompletedProcess([], 0, next(snapshots), "")
        owned_debuggers = {}
        first = visual.scoped_processes(UDID, runner, owned_debuggers)
        second = visual.scoped_processes(UDID, runner, owned_debuggers)
        third = visual.scoped_processes(UDID, runner, owned_debuggers)
        self.assertEqual(first["processes"], [{"pid": 42, "ppid": 46, "state": "S", "name": "debugserver"}])
        self.assertEqual(second["processes"], [{"pid": 41, "ppid": 46, "state": "Ss", "name": "Persona"},
                                               {"pid": 42, "ppid": 1, "state": "S", "name": "debugserver"}])
        self.assertIn({"pid": 42, "ppid": 1, "state": "S", "name": "debugserver"}, third["processes"])
        self.assertEqual(owned_debuggers, {42: START})
        self.assertNotIn("43", str(first) + str(second) + str(third))

    def test_equal_second_reappeared_debugger_pid_requires_owned_launcher_again(self):
        launcher = f"/Library/Developer/CoreSimulator/Devices/{UDID}/ --launchd /usr/sbin/launchd_sim"
        foreign_launcher = launcher.replace(UDID, OTHER)
        def line(pid, ppid, command, started=START, state="S"):
            return f" {pid} {ppid} {started} {state} {command}"
        snapshots = iter([
            line(46, 1, launcher, state="Ss") + "\n" + line(42, 46, "/usr/libexec/debugserver"),
            line(46, 1, launcher, state="Ss"),
            line(47, 1, foreign_launcher, state="Ss") + "\n" + line(42, 47, "/usr/libexec/debugserver"),
        ])
        def runner(*args, **kwargs):
            return subprocess.CompletedProcess([], 0, next(snapshots), "")
        owned_debuggers = {}
        first = visual.scoped_processes(UDID, runner, owned_debuggers)
        second = visual.scoped_processes(UDID, runner, owned_debuggers)
        reused = visual.scoped_processes(UDID, runner, owned_debuggers)
        self.assertEqual(first["processes"], [{"pid": 42, "ppid": 46, "state": "S", "name": "debugserver"}])
        self.assertEqual(second["processes"], [{"pid": 42, "state": "not-present", "name": "debugserver"}])
        self.assertEqual(reused["processes"], [],
                         "a foreign launcher must not revive an absent owned PID")
        self.assertNotIn(OTHER, str(first) + str(second) + str(reused))
        self.assertEqual(owned_debuggers, {}, "the rejected replacement is not retained as owned evidence")

    def test_absent_equal_second_parent_one_replacement_cannot_publish_foreign_threads(self):
        launcher = f"/Library/Developer/CoreSimulator/Devices/{UDID}/ --launchd /usr/sbin/launchd_sim"
        original = (f" 46 1 {START} Ss {launcher}\n"
                    f" 42 46 {START} S /usr/libexec/debugserver")
        absent = f" 46 1 {START} Ss {launcher}"
        replacement = f" 42 1 {START} S /usr/libexec/debugserver"
        owned_debuggers = {}
        visual.scoped_processes(UDID, lambda command, **kwargs:
            subprocess.CompletedProcess(command, 0, original, ""), owned_debuggers)
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory)
            real_popen = subprocess.Popen
            captures = []
            def capture(command, **kwargs):
                captures.append(command)
                self.assertEqual(command, ["ps", "-M", "42"])
                return real_popen([sys.executable, "-c", "print('foreign-thread-evidence')"], **kwargs)
            with mock.patch.object(visual.watch.subprocess, "Popen", side_effect=capture):
                missing = visual.sample_processes(UDID, output, 0, runner=lambda command, **kwargs:
                    subprocess.CompletedProcess(command, 0, absent, ""), owned_debuggers=owned_debuggers)
                replaced = visual.sample_processes(UDID, output, 1, runner=lambda command, **kwargs:
                    subprocess.CompletedProcess(command, 0, replacement, ""), owned_debuggers=owned_debuggers)
            self.assertEqual(missing["processes"], [
                {"pid": 42, "state": "not-present", "name": "debugserver"}])
            self.assertEqual(replaced["processes"], [],
                             "an observed absence must end the parent-1 continuity exception")
            self.assertEqual(captures, [])
            self.assertEqual(owned_debuggers, {})
            self.assertFalse((output / "process-001-debugserver-42-threads.txt").exists())
            self.assertNotIn("foreign-thread-evidence", "".join(
                path.read_text() for path in output.iterdir() if path.suffix != ".command"))

    def test_equal_second_reappeared_debugger_with_an_owned_launcher_is_new_evidence(self):
        launcher = f"/Library/Developer/CoreSimulator/Devices/{UDID}/ --launchd /usr/sbin/launchd_sim"
        snapshot = (f" 46 1 {START} Ss {launcher}\n"
                    f" 42 46 {START} S /usr/libexec/debugserver")
        owned_debuggers = {42: START}
        observation = visual.scoped_processes(UDID, lambda command, **kwargs:
            subprocess.CompletedProcess(command, 0, snapshot, ""), owned_debuggers)
        self.assertEqual(observation["processes"], [
            {"pid": 42, "ppid": 46, "state": "S", "name": "debugserver"}])
        self.assertEqual(owned_debuggers, {42: START})

    def test_legitimate_retained_debugger_can_reparent_to_init(self):
        launcher = f"/Library/Developer/CoreSimulator/Devices/{UDID}/ --launchd /usr/sbin/launchd_sim"
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory)
            owned_debuggers = {}
            original = (f" 46 1 {START} Ss {launcher}\n"
                        f" 42 46 {START} S /usr/libexec/debugserver")
            reparented = f" 42 1 {START} S /usr/libexec/debugserver"
            snapshots = iter([reparented, reparented, reparented])
            real_popen = subprocess.Popen
            captures = []
            def capture(command, **kwargs):
                captures.append(command)
                self.assertEqual(command, ["ps", "-M", "42"])
                return real_popen([sys.executable, "-c", "print('owned-thread-evidence')"], **kwargs)
            def runner(command, **kwargs):
                return subprocess.CompletedProcess(command, 0, next(snapshots), "")
            visual.scoped_processes(UDID, runner=lambda command, **kwargs:
                subprocess.CompletedProcess(command, 0, original, ""), owned_debuggers=owned_debuggers)
            with mock.patch.object(visual.watch.subprocess, "Popen", side_effect=capture):
                observation = visual.sample_processes(UDID, output, 0, runner=runner,
                                                     owned_debuggers=owned_debuggers)
            self.assertEqual(observation["processes"], [
                {"pid": 42, "ppid": 1, "state": "S", "name": "debugserver"}])
            path = output / "process-000-debugserver-42-threads.txt"
            self.assertEqual(path.read_text(), "owned-thread-evidence\n")
            self.assertEqual(path.with_suffix(path.suffix + ".status.txt").read_text(), "0\n")
            self.assertFalse(path.with_suffix(path.suffix + ".command").exists())
            self.assertEqual(captures, [["ps", "-M", "42"]])
            self.assertEqual(owned_debuggers, {42: START})

    def test_reappeared_debugger_with_an_owned_launcher_is_new_evidence(self):
        launcher = f"/Library/Developer/CoreSimulator/Devices/{UDID}/ --launchd /usr/sbin/launchd_sim"
        def line(pid, ppid, command, started=START, state="S"):
            return f" {pid} {ppid} {started} {state} {command}"
        snapshots = iter([
            line(46, 1, launcher, state="Ss") + "\n" + line(42, 46, "/usr/libexec/debugserver"),
            line(46, 1, launcher, state="Ss"),
            line(46, 1, launcher, state="Ss") + "\n" + line(42, 46, "/usr/libexec/debugserver", started=REUSED),
        ])
        def runner(*args, **kwargs):
            return subprocess.CompletedProcess([], 0, next(snapshots), "")
        owned_debuggers = {}
        visual.scoped_processes(UDID, runner, owned_debuggers)
        missing = visual.scoped_processes(UDID, runner, owned_debuggers)
        restarted = visual.scoped_processes(UDID, runner, owned_debuggers)
        self.assertEqual(missing["processes"], [{"pid": 42, "state": "not-present", "name": "debugserver"}])
        self.assertEqual(restarted["processes"], [{"pid": 42, "ppid": 46, "state": "S", "name": "debugserver"}])
        self.assertEqual(owned_debuggers, {42: REUSED})

    def test_replacement_before_capture_cannot_publish_foreign_thread_evidence(self):
        launcher = f"/Library/Developer/CoreSimulator/Devices/{UDID}/ --launchd /usr/sbin/launchd_sim"
        foreign_launcher = launcher.replace(UDID, OTHER)
        def line(pid, ppid, command, started=START, state="S"):
            return f" {pid} {ppid} {started} {state} {command}"
        first = line(46, 1, launcher, state="Ss") + "\n" + line(42, 46, "/usr/libexec/debugserver")
        replaced = line(47, 1, foreign_launcher, state="Ss") + "\n" + line(42, 47, "/usr/libexec/debugserver")
        snapshots = iter([first, replaced])
        def runner(command, **kwargs):
            if command[-1] == "42":
                return subprocess.CompletedProcess(command, 0, replaced, "")
            return subprocess.CompletedProcess(command, 0, next(snapshots), "")
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory)
            with mock.patch.object(visual.watch, "bounded_file", side_effect=lambda command, path, **kwargs: (
                    path.write_text("foreign-thread-evidence\n"),
                    path.with_suffix(path.suffix + ".status.txt").write_text("0\n"), 0)[-1]):
                visual.sample_processes(UDID, output, 0, runner=runner)
            threads = output / "process-000-debugserver-42-threads.txt"
            self.assertNotIn("foreign-thread-evidence", threads.read_text())
            self.assertEqual((output / "process-000-debugserver-42-threads.txt.status.txt").read_text(),
                             "identity-mismatch-before-capture\n")
            self.assertNotIn("foreign-thread-evidence", "".join(
                path.read_text() for path in output.iterdir() if path.suffix != ".command"))
            observation = json.loads((output / "process-000.json").read_text())
            self.assertNotIn(START, str(observation))

    def test_replacement_during_capture_is_not_a_successful_thread_sample(self):
        launcher = f"/Library/Developer/CoreSimulator/Devices/{UDID}/ --launchd /usr/sbin/launchd_sim"
        foreign_launcher = launcher.replace(UDID, OTHER)
        original = (f" 46 1 {START} Ss {launcher}\n"
                    f" 42 46 {START} S /usr/libexec/debugserver")
        replacement = (f" 47 1 {START} Ss {foreign_launcher}\n"
                       f" 42 47 {START} S /usr/libexec/debugserver")
        snapshots = iter([original, original, replacement])
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory)
            real_popen = subprocess.Popen
            def capture(command, **kwargs):
                self.assertEqual(command, ["ps", "-M", "42"])
                return real_popen([sys.executable, "-c", "print('foreign-thread-evidence')"], **kwargs)
            with mock.patch.object(visual.watch.subprocess, "Popen", side_effect=capture):
                visual.sample_processes(UDID, output, 0, runner=lambda command, **kwargs:
                    subprocess.CompletedProcess(command, 0, next(snapshots), ""))
            path = output / "process-000-debugserver-42-threads.txt"
            self.assertEqual(path.with_suffix(path.suffix + ".status.txt").read_text(),
                             "identity-mismatch-after-capture\n")
            self.assertNotIn("foreign-thread-evidence", path.read_text())
            self.assertFalse(path.with_suffix(path.suffix + ".command").exists())
            self.assertNotIn(START, (output / "process-000.json").read_text())

    def test_debugger_exit_after_successful_capture_is_explicitly_unavailable(self):
        launcher = f"/Library/Developer/CoreSimulator/Devices/{UDID}/ --launchd /usr/sbin/launchd_sim"
        original = (f" 46 1 {START} Ss {launcher}\n"
                    f" 42 46 {START} S /usr/libexec/debugserver")
        exited = f" 46 1 {START} Ss {launcher}"
        snapshots = iter([original, original, exited])
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory)
            def capture(command, path, **kwargs):
                self.assertEqual(command, ["ps", "-M", "42"])
                path.write_text("exit-race-thread-evidence\n")
                path.with_suffix(path.suffix + ".status.txt").write_text("0\n")
                return 0
            with mock.patch.object(visual.watch, "bounded_file", side_effect=capture):
                visual.sample_processes(UDID, output, 0, runner=lambda command, **kwargs:
                    subprocess.CompletedProcess(command, 0, next(snapshots), ""))
            path = output / "process-000-debugserver-42-threads.txt"
            self.assertEqual(path.read_text(), "unavailable: process identity changed during thread capture\n")
            self.assertEqual(path.with_suffix(path.suffix + ".status.txt").read_text(),
                             "identity-mismatch-after-capture\n")
            self.assertFalse(path.with_suffix(path.suffix + ".command").exists())

    def test_host_replacement_before_capture_cannot_publish_foreign_thread_evidence(self):
        owned = f"/Library/Developer/CoreSimulator/Devices/{UDID}/Application/X/Persona.app/Persona"
        original = f" 41 1 {START} Ss {owned}"
        replaced = f" 41 1 {REUSED} Ss {owned}"
        snapshots = iter([original, replaced])
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory)
            with mock.patch.object(visual.watch, "bounded_file") as bounded:
                visual.sample_processes(UDID, output, 0, runner=lambda command, **kwargs:
                    subprocess.CompletedProcess(command, 0, next(snapshots), ""))
            bounded.assert_not_called()
            path = output / "process-000-Persona-41-threads.txt"
            self.assertEqual(path.read_text(), "unavailable: process identity changed before thread capture\n")
            self.assertEqual(path.with_suffix(path.suffix + ".status.txt").read_text(),
                             "identity-mismatch-before-capture\n")

    def test_host_parent_change_does_not_use_the_debugger_reparenting_exception(self):
        self.assertFalse(visual.same_process_identity((START, 1), (START, 46), "Persona"))

    def test_host_exit_before_capture_is_unavailable_without_calling_ps_m(self):
        owned = f"/Library/Developer/CoreSimulator/Devices/{UDID}/Application/X/Persona.app/Persona"
        original = f" 41 1 {START} Ss {owned}"
        snapshots = iter([original, ""])
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory)
            with mock.patch.object(visual.watch, "bounded_file") as bounded:
                visual.sample_processes(UDID, output, 0, runner=lambda command, **kwargs:
                    subprocess.CompletedProcess(command, 0, next(snapshots), ""))
            bounded.assert_not_called()
            path = output / "process-000-Persona-41-threads.txt"
            self.assertEqual(path.read_text(), "unavailable: process identity changed before thread capture\n")
            self.assertEqual(path.with_suffix(path.suffix + ".status.txt").read_text(),
                             "identity-mismatch-before-capture\n")

    def test_identity_query_failure_after_capture_is_not_a_successful_sample(self):
        launcher = f"/Library/Developer/CoreSimulator/Devices/{UDID}/ --launchd /usr/sbin/launchd_sim"
        snapshot = (f" 46 1 {START} Ss {launcher}\n"
                    f" 42 46 {START} S /usr/libexec/debugserver")
        snapshots = iter([snapshot, snapshot])
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory)
            def capture(command, path, **kwargs):
                path.write_text("uncertain-thread-evidence\n")
                path.with_suffix(path.suffix + ".status.txt").write_text("0\n")
                return 0
            def runner(command, **kwargs):
                try:
                    return subprocess.CompletedProcess(command, 0, next(snapshots), "")
                except StopIteration:
                    return subprocess.CompletedProcess(command, 1, "", "")
            with mock.patch.object(visual.watch, "bounded_file", side_effect=capture):
                visual.sample_processes(UDID, output, 0, runner=runner)
            path = output / "process-000-debugserver-42-threads.txt"
            self.assertEqual(path.read_text(), "unavailable: process identity changed during thread capture\n")
            self.assertEqual(path.with_suffix(path.suffix + ".status.txt").read_text(),
                             "identity-mismatch-after-capture\n")

    def test_legitimate_same_process_capture_preserves_its_exit_status(self):
        launcher = f"/Library/Developer/CoreSimulator/Devices/{UDID}/ --launchd /usr/sbin/launchd_sim"
        def line(pid, ppid, command, started=START, state="S"):
            return f" {pid} {ppid} {started} {state} {command}"
        snapshot = line(46, 1, launcher, state="Ss") + "\n" + line(42, 46, "/usr/libexec/debugserver")
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory)
            def capture(command, path, **kwargs):
                path.write_text("owned-thread-evidence\n")
                path.with_suffix(path.suffix + ".status.txt").write_text("0\n")
                return 0
            with mock.patch.object(visual.watch, "bounded_file", side_effect=capture) as bounded:
                visual.sample_processes(UDID, output, 0, runner=lambda command, **kwargs:
                    subprocess.CompletedProcess(command, 0, snapshot, ""))
            bounded.assert_called_once()
            self.assertEqual(bounded.call_args.args[0], ["ps", "-M", "42"])
            path = output / "process-000-debugserver-42-threads.txt"
            self.assertEqual(path.read_text(), "owned-thread-evidence\n")
            self.assertEqual(path.with_suffix(path.suffix + ".status.txt").read_text(), "0\n")

    def test_legitimate_capture_preserves_a_nonzero_thread_command_status(self):
        launcher = f"/Library/Developer/CoreSimulator/Devices/{UDID}/ --launchd /usr/sbin/launchd_sim"
        snapshot = (f" 46 1 {START} Ss {launcher}\n"
                    f" 42 46 {START} S /usr/libexec/debugserver")
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory)
            with mock.patch.object(visual.watch, "bounded_file", return_value=42) as bounded:
                visual.sample_processes(UDID, output, 0, runner=lambda command, **kwargs:
                    subprocess.CompletedProcess(command, 0, snapshot, ""))
            bounded.assert_called_once()

    def test_thread_samples_follow_only_the_validated_process_identity(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory)
            snapshot = (f" 46 1 {START} Ss /Library/Developer/CoreSimulator/Devices/{UDID}/ launchd_sim\n"
                        f" 42 46 {START} S /usr/libexec/debugserver")
            with mock.patch.object(visual.watch, "bounded_file") as bounded:
                visual.sample_processes(UDID, output, 0, runner=lambda command, **kwargs:
                    subprocess.CompletedProcess(command, 0, snapshot, ""), owned_debuggers={43: START})
            self.assertEqual(bounded.call_args.args[0], ["ps", "-M", "42"])
            self.assertNotIn(START, (output / "process-000.json").read_text())
        with self.assertRaises(ValueError):
            visual.scoped_processes(UDID, owned_debuggers={42})
        self.assertRaises(ValueError, visual.sample_processes, UDID, directory, 1, owned_debuggers={42})

    def test_process_query_failures_remain_distinct(self):
        for error, status in [(subprocess.TimeoutExpired("ps", 5), "query-timeout"),
                              (OSError("private"), "query-failed")]:
            with self.subTest(status=status):
                with mock.patch.object(visual.watch.subprocess, "run", side_effect=error):
                    self.assertEqual(visual.scoped_processes(UDID),
                                     {"query_status": status, "processes": []})


class VisualObserverTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.output = Path(self.tmp.name)
        self.destination = self.output / "diagnostics/live-regular-visual"
        self.live = mock.Mock()

    def test_successful_host_capture_then_debugger_exit_keeps_observer_collecting(self):
        owned = f"/Library/Developer/CoreSimulator/Devices/{UDID}/Application/X/Persona.app/Persona"
        launcher = f"/Library/Developer/CoreSimulator/Devices/{UDID}/ --launchd /usr/sbin/launchd_sim"
        def line(pid, ppid, command, started=START, state="S"):
            return f" {pid} {ppid} {started} {state} {command}"
        original = "\n".join([line(41, 1, owned, state="Ss"), line(42, 41, "/usr/libexec/debugserver")])
        host_only = line(41, 1, owned, state="Ss")
        snapshots = iter([original, original, host_only, host_only])
        with mock.patch.object(visual.watch, "start_live_log", return_value=self.live), \
                mock.patch.object(visual.watch, "stop_live_log") as stop, \
                mock.patch.object(visual.launch, "collect") as collect:
            real_popen = subprocess.Popen
            def capture(command, **kwargs):
                if command[1] == "-axo":
                    return real_popen([sys.executable, "-c",
                        "import sys; sys.stdout.write(next(iter(sys.argv[1:])))", next(snapshots)], **kwargs)
                self.assertEqual(command, ["ps", "-M", "41"])
                return real_popen([sys.executable, "-c", "print('owned-host-thread-evidence')"], **kwargs)
            with mock.patch.object(visual.watch.subprocess, "Popen", side_effect=capture):
                visual.observe(UDID, visual.launch.BUNDLE, self.destination, seconds=36,
                               scope_root=self.output, monotonic=lambda: 0,
                               sleep=lambda _: (self.destination / "observer.stop").write_text(""))
        host = self.destination / "process-000-Persona-41-threads.txt"
        debugger = self.destination / "process-000-debugserver-42-threads.txt"
        self.assertEqual(host.read_text(), "owned-host-thread-evidence\n")
        self.assertEqual(host.with_suffix(host.suffix + ".status.txt").read_text(), "0\n")
        self.assertEqual(debugger.read_text(), "unavailable: process identity changed before thread capture\n")
        self.assertEqual(debugger.with_suffix(debugger.suffix + ".status.txt").read_text(),
                         "identity-mismatch-before-capture\n")
        self.assertFalse(debugger.with_suffix(debugger.suffix + ".command").exists())
        collect.assert_called_once()
        stop.assert_called_once_with(self.live)
        status = (self.destination / "observer.status.txt").read_text()
        self.assertIn("xcodebuild-finished", status)
        self.assertIn("observation only; no launch cause established", status)
        self.assertNotIn("collection incomplete", status)

    def test_stream_starts_before_first_sample_and_stop_publishes_no_cause(self):
        self.destination.mkdir(parents=True)
        (self.destination / "observer.stop").write_text("")
        with mock.patch.object(visual.watch, "start_live_log") as start, \
                mock.patch.object(visual, "sample_processes") as sample, \
                mock.patch.object(visual, "launch_system") as system, \
                mock.patch.object(visual.watch, "stop_live_log") as stop, \
                mock.patch.object(visual.launch, "collect") as collect:
            visual.observe(UDID, visual.launch.BUNDLE, self.destination, scope_root=self.output)
        start.assert_not_called()
        sample.assert_not_called()
        system.assert_not_called()
        collect.assert_called_once()
        stop.assert_called_once_with(None)
        self.assertIn("xcodebuild-finished", (self.destination / "observer.status.txt").read_text())
        self.assertIn("no launch cause established", (self.destination / "observer.status.txt").read_text())

    def test_samples_at_zero_and_fifteen_seconds_and_queries_system_once_at_sixty(self):
        events = []
        clock = mock.Mock(side_effect=[0, 0, 0, 14, 15, 15, 59, 60, 60, 1000, 1000, 1000])
        def record_sample(_udid, _destination, index, **_kwargs):
            events.append(("sample", index))
            return {"query_status": "completed", "processes": []}
        with mock.patch.object(visual.watch, "start_live_log", return_value=self.live), \
                mock.patch.object(visual, "sample_processes", side_effect=record_sample), \
                mock.patch.object(visual, "launch_system", side_effect=lambda *args: events.append(("system",))) as system, \
                mock.patch.object(visual.watch, "stop_live_log"), \
                mock.patch.object(visual.launch, "collect"):
            visual.observe(UDID, visual.launch.BUNDLE, self.destination, scope_root=self.output,
                           monotonic=clock, sleep=lambda _: None)
        self.assertEqual(events, [("sample", 0), ("sample", 1), ("sample", 2), ("system",)])
        system.assert_called_once_with(UDID, self.destination)
        self.assertIn("visual-deadline", (self.destination / "observer.status.txt").read_text())

    def test_observer_rejects_a_lifetime_beyond_the_visual_step(self):
        with self.assertRaises(ValueError):
            visual.observe(UDID, visual.launch.BUNDLE, self.destination, visual.VISUAL_SECONDS + 1)

    def test_visual_live_scope_has_its_own_invocation_start_and_six_hundred_second_horizon(self):
        from datetime import datetime, timedelta, timezone
        current = datetime(2026, 1, 1, 0, 10, tzinfo=timezone.utc)
        (self.output / "regular-visual-context.json").write_text(json.dumps({
            "udid": UDID, "bundle_id": visual.launch.BUNDLE,
            "started_at": "2026-01-01T00:10:00+00:00"}))
        (self.output / "regular-readiness-context.json").write_text(json.dumps({
            "udid": UDID, "bundle_id": visual.launch.BUNDLE,
            "started_at": "2026-01-01T00:00:00+00:00"}))
        (self.output / "regular-simulator.log").write_text(UDID + "\n")
        context, start, _ = visual.launch.observation_scope(
            self.output, current, visual=True, seconds=600, allow_current_start=True)
        self.assertEqual(context["udid"], UDID)
        self.assertEqual(start, current)
        self.assertEqual(start + timedelta(seconds=600), current + timedelta(seconds=600))
        with mock.patch.object(visual.launch, "LiveLog") as live_type, \
                mock.patch.object(visual.launch.os, "getpgid", side_effect=[999, 1234]), \
                mock.patch.object(visual.launch.subprocess, "Popen") as popen:
            live_type.return_value.command = ["log", "stream"]
            live_type.return_value.process.pid = 1234
            popen.return_value.pid = 5678
            visual.launch.start_live_log(self.output, self.destination, now=lambda: current)
        self.assertEqual(live_type.call_args.args[2], current + timedelta(seconds=600))

    def test_launchctl_exports_only_scoped_service_identity(self):
        self.destination.mkdir(parents=True)
        real_popen = subprocess.Popen
        def fake_popen(command, **kwargs):
            self.assertEqual(command[:4], ["xcrun", "simctl", "spawn", UDID])
            return real_popen([
                sys.executable, "-c",
                "print('system'); "
                "print('  program = /unrelated/tool --private-system-argument'); "
                "print('unrelated.example'); print('  state = running'); "
                "print('org.example.persona.ci'); print('  state = waiting'); print('  pid = 41'); "
                "print('  runs = 1'); print('  last exit code = 0'); "
                "print('  path = /private/container/Persona.app'); "
                "print('  program = /path/tool org.example.persona.ci --unsafe-private-argument'); "
                "print('  arguments = {'); print('    --credential-without-keyword'); print('  }'); "
                "print('  environment = {'); print('    ORG_EXAMPLE_PERSONA_CI=unsafe-private-value'); print('  }'); "
                "print('other.example'); print('  state = running'); "
                "print('  program = other.example --other-private-argument')"
            ], stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        with mock.patch.object(visual.subprocess, "Popen", side_effect=fake_popen):
            self.assertEqual(visual.launch_system(UDID, self.destination), 0)
        exported = (self.destination / "launch-system-after-60s.txt").read_text()
        self.assertEqual(exported, "org.example.persona.ci\nstate = waiting\npid = 41\nruns = 1\nlast exit code = 0\n")
        for private in ("unsafe-private", "credential-without-keyword", "/private/container", "other-private", "environment"):
            self.assertNotIn(private, exported)

    def test_launchctl_rejects_arbitrary_values_in_allowlisted_fields(self):
        for raw in (f"{visual.launch.BUNDLE}\n state = running --private-argument\n",
                    f"{visual.launch.BUNDLE}\n pid = 41;unsafe-private-value\n",
                    f"{visual.launch.BUNDLE}\n program = {visual.launch.BUNDLE} --unsafe-private-argument\n"):
            self.assertEqual(visual.launch_system_projection(raw.encode(), visual.launch.BUNDLE),
                             [visual.launch.BUNDLE + "\n"])
        raw = f"{visual.launch.BUNDLE}\n state = running\nother.example\n state = running\n"
        self.assertEqual(visual.launch_system_projection(raw.encode(), visual.launch.BUNDLE),
                         [visual.launch.BUNDLE + "\n", "state = running\n"])
        self.assertEqual(visual.launch_system_projection(b"\xff", visual.launch.BUNDLE), [])

    def test_cli_converts_the_output_to_a_path_before_observing(self):
        with mock.patch.object(sys, "argv", ["apple_regular_visual_watch.py", "--udid", UDID,
                                             "--bundle-id", visual.launch.BUNDLE, "--scope-root", str(self.output),
                                             "--output", str(self.destination), "--seconds", "600"]), \
                mock.patch.object(visual, "observe") as observe:
            visual.main()
        self.assertEqual(observe.call_args.args[2], self.destination)

    def test_silent_launchctl_is_bounded_without_changing_shared_readiness_reader(self):
        self.destination.mkdir(parents=True)
        real_popen = subprocess.Popen
        def fake_popen(command, **kwargs):
            return real_popen([sys.executable, "-c", "import time; time.sleep(30)"],
                                    stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                    stderr=subprocess.DEVNULL)
        with mock.patch.object(visual.subprocess, "Popen", side_effect=fake_popen), \
                mock.patch.object(visual.watch.time, "monotonic", side_effect=[0, 0, 11]):
            self.assertEqual(visual.launch_system(UDID, self.destination), 124)
        self.assertIn("timed out", (self.destination / "launch-system-after-60s.txt").read_text())


if __name__ == "__main__":
    unittest.main()
