#!/usr/bin/env python3
"""Run internal/node's tests in concurrent `go test -run` partitions (docs/operations/testing.md, "Hosted CI").

internal/node runs about 2,400 top-level tests one after another (almost none call t.Parallel), so its serial run
outgrew Go's ten-minute -timeout and CI's ten-minute step. CI's node shard (scripts/ci/server-shards.py) and the release
build (scripts/release/build_hub_release.py) both split it here, so the two cannot drift. Each partition is a whole
`go test` process with an exact `-run '^(A|B|…)$'` list: every listed test runs in exactly one of them, and
subtests follow their parent.
"""
import re
import subprocess

# The package and how many processes share its tests. Two keeps CI's four-CPU runners, which also drive a real browser
# in this package, from contending much; each partition keeps -p 1 and its own -timeout.
PACKAGE = "./internal/node"
IMPORT_SUFFIX = "/internal/node"
PARTITIONS = 2
# What `go test -run` selects: tests, examples and fuzz seeds. Benchmarks need -bench and are not listed here.
TEST_NAME = re.compile(r"(?:Test|Example|Fuzz)\w*")
# One argv string must stay well below Linux's 128 KiB limit for a single argument (MAX_ARG_STRLEN).
MAX_PATTERN = 100_000


def names(listing):
    """The runnable names in `go test -list .*` output; the trailing `ok <package>` line and benchmarks are not."""
    return [line.strip() for line in listing.splitlines() if TEST_NAME.fullmatch(line.strip())]


def patterns(test_names, partitions=PARTITIONS):
    """Exact -run patterns, round-robin so the slow tests (listed together by file) spread across partitions."""
    if not test_names:
        raise ValueError("go test -list found no tests in %s: refusing to run an empty partition" % PACKAGE)
    if len(set(test_names)) != len(test_names):
        raise ValueError("go test -list repeated a test name")
    groups = [test_names[i::partitions] for i in range(partitions)]
    result = ["^(" + "|".join(group) + ")$" for group in groups if group]
    if max(map(len, result)) > MAX_PATTERN:
        raise ValueError("a -run pattern for %s is too long for one argument: raise PARTITIONS" % PACKAGE)
    return result


def list_patterns(cwd, env=None, partitions=PARTITIONS):
    """List PACKAGE's tests in the server module at cwd and split them."""
    listing = subprocess.check_output(["go", "test", "-list", ".*", PACKAGE], cwd=cwd, env=env, text=True,
                                      timeout=600)
    return patterns(names(listing), partitions)


def is_partitioned(package):
    """Whether an import path or ./ path is the partitioned package itself (not one of its subpackages)."""
    return package == PACKAGE or package.endswith(IMPORT_SUFFIX)
