"""go_test_partitioned: test-server.sh's go test runs internal/node in go_partitions' partitions (#5594)."""
import io
import sys
import unittest
from unittest import mock

import go_partitions
import go_test_partitioned as gtp

MODULE = ["example/server/cmd/personad", "example/server/internal/assistant", "example/server/internal/node",
          "example/server/internal/node/sub"]
PATTERNS = ["^(TestA|TestC)$", "^(TestB)$"]


def plan(argv):
    return gtp.plan(argv, lambda: list(MODULE), lambda: list(PATTERNS))


class PlanTests(unittest.TestCase):
    def test_the_whole_module_runs_node_in_partitions_beside_the_rest(self):
        self.assertEqual(plan(["go", "test", "-count=1", "-p", "1", "./..."]), [
            ["go", "test", "-count=1", "-p", "1", "example/server/cmd/personad", "example/server/internal/assistant",
             "example/server/internal/node/sub"],
            ["go", "test", "-count=1", "-p", "1", "-run", PATTERNS[0], go_partitions.PACKAGE],
            ["go", "test", "-count=1", "-p", "1", "-run", PATTERNS[1], go_partitions.PACKAGE],
        ])

    def test_every_command_keeps_the_callers_flags(self):
        # A deploy's skip list (scripts/deploy): every partition leaves the same tests out.
        commands = plan(["go", "test", "-count=1", "-p", "1", "-skip", "TestX|TestY", "-v", "./..."])
        for command in commands:
            self.assertEqual(command[2:8], ["-count=1", "-p", "1", "-skip", "TestX|TestY", "-v"])

    def test_node_named_alone_runs_only_its_partitions(self):
        for node in ("./internal/node", "./internal/node/"):
            self.assertEqual(plan(["go", "test", "-count=1", node]), [
                ["go", "test", "-count=1", "-run", pattern, go_partitions.PACKAGE] for pattern in PATTERNS])

    def test_node_beside_other_packages(self):
        self.assertEqual(plan(["go", "test", "./internal/hub/", "./internal/node/"])[0],
                         ["go", "test", "./internal/hub/"])

    def test_run_or_other_packages_or_another_command_run_as_given(self):
        for argv in (["go", "test", "-run", "TestX", "./..."], ["go", "test", "-run=TestX", "./internal/node/"],
                     ["go", "test", "-count=1", "./internal/assistant/"], ["go", "test", "./internal/node/sub"],
                     ["go", "vet", "./..."]):
            self.assertEqual(plan(argv), [argv], argv)

    def test_flag_values_are_not_packages(self):
        flags, packages = gtp.split(["-coverprofile", "/tmp/cover.out", "-skip", "A|B", "-timeout=20m", "./..."])
        self.assertEqual((flags, packages), (["-coverprofile", "/tmp/cover.out", "-skip", "A|B", "-timeout=20m"],
                                             ["./..."]))

    def test_a_partition_is_described_by_its_size_not_its_names(self):
        self.assertEqual(gtp.describe(["go", "test", "-run", "^(TestA|TestB|TestC)$", go_partitions.PACKAGE]),
                         "./internal/node partition (3 tests)")


class RunTests(unittest.TestCase):
    def run_commands(self, commands):
        out = io.StringIO()
        with mock.patch.object(sys, "stdout", out):
            status = gtp.run(commands)
        return status, out.getvalue()

    def test_outputs_never_interleave_and_any_failure_fails(self):
        py = sys.executable
        status, text = self.run_commands([
            [py, "-c", "import time\nfor i in range(3): print('rest', i, flush=True); time.sleep(0.05)"],
            [py, "-c", "print('FAIL\\tpartition one'); raise SystemExit(1)"],
            [py, "-c", "print('ok\\tpartition two')"],
        ])
        self.assertEqual(status, 1)
        lines = text.splitlines()
        for line in ("rest 0", "rest 1", "rest 2", "FAIL\tpartition one", "ok\tpartition two"):
            self.assertIn(line, lines)
        self.assertEqual(lines.index("FAIL\tpartition one") - 1, next(
            i for i, line in enumerate(lines) if line.endswith("exit 1")))

    def test_all_passing_passes(self):
        py = sys.executable
        self.assertEqual(self.run_commands([[py, "-c", "pass"], [py, "-c", "pass"]])[0], 0)


if __name__ == "__main__":
    unittest.main()
