"""go_partitions: internal/node's tests split into exact, disjoint -run patterns (CI node shard and release build)."""
import re
import unittest

import go_partitions as gp

LISTING = """TestAlpha
TestBeta
BenchmarkGamma
ExampleDelta
FuzzEpsilon
TestZeta
ok  \texample/server/internal/node\t0.123s
"""


class PartitionTests(unittest.TestCase):
    def test_names_keep_runnable_tests_and_drop_benchmarks_and_the_summary(self):
        self.assertEqual(gp.names(LISTING), ["TestAlpha", "TestBeta", "ExampleDelta", "FuzzEpsilon", "TestZeta"])

    def test_every_test_runs_in_exactly_one_partition(self):
        names = ["Test%03d" % i for i in range(101)]
        for partitions in (1, 2, 3):
            patterns = gp.patterns(names, partitions)
            self.assertEqual(len(patterns), partitions)
            matched = [[n for n in names + ["Test0010x", "XTest001"] if re.fullmatch(p, n)] for p in patterns]
            self.assertEqual(sorted(sum(matched, [])), names)  # disjoint, complete, nothing else
            self.assertLessEqual(max(map(len, matched)) - min(map(len, matched)), 1)  # round-robin

    def test_a_pattern_never_matches_a_longer_name_or_a_prefix(self):
        (pattern,) = gp.patterns(["TestA"], 1)
        self.assertEqual(pattern, "^(TestA)$")
        self.assertFalse(re.search(pattern, "TestAB"))
        self.assertFalse(re.search(pattern, "Test"))

    def test_an_empty_listing_never_becomes_an_empty_run(self):
        with self.assertRaisesRegex(ValueError, "no tests"):
            gp.patterns(gp.names("ok  \texample/server/internal/node\t0.1s\n"))

    def test_repeated_names_and_oversized_patterns_fail(self):
        with self.assertRaisesRegex(ValueError, "repeated"):
            gp.patterns(["TestA", "TestA"])
        with self.assertRaisesRegex(ValueError, "too long"):
            gp.patterns(["Test%06d%s" % (i, "x" * 40) for i in range(6000)], 2)

    def test_only_the_package_itself_is_partitioned(self):
        self.assertTrue(gp.is_partitioned("./internal/node"))
        self.assertTrue(gp.is_partitioned("github.com/example/persona/server/internal/node"))
        self.assertFalse(gp.is_partitioned("github.com/example/persona/server/internal/node/hostinfo"))
        self.assertFalse(gp.is_partitioned("github.com/example/persona/server/internal/nodeupdate"))


if __name__ == "__main__":
    unittest.main()
