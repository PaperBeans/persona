#!/usr/bin/env python3
"""Verify the test results reported by Hypium in an aa test log."""
import argparse
import re
from pathlib import Path

SUMMARY = re.compile(r'^OHOS_REPORT_RESULT: stream=Tests run: (\d+), Failure: (\d+), Error: (\d+), Pass: (\d+), Ignore: (\d+)$')


def verify(text: str, suite: str, count: int) -> None:
    results = []
    summaries = []
    report_codes = []
    runner_codes = []
    current_suite = ''
    current_test = ''
    for line in text.splitlines():
        line = line.strip()
        if line.startswith('OHOS_REPORT_STATUS: class='):
            current_suite = line.removeprefix('OHOS_REPORT_STATUS: class=')
        elif line.startswith('OHOS_REPORT_STATUS: test='):
            current_test = line.removeprefix('OHOS_REPORT_STATUS: test=')
        elif line.startswith('OHOS_REPORT_STATUS_CODE:'):
            status = int(line.split(':', 1)[1])
            if status != 1:
                results.append((current_suite, current_test, status))
            current_suite = current_test = ''
        elif match := SUMMARY.fullmatch(line):
            summaries.append(tuple(map(int, match.groups())))
        elif line.startswith('OHOS_REPORT_CODE:'):
            report_codes.append(int(line.split(':', 1)[1]))
        elif line.startswith('TestFinished-ResultCode:'):
            runner_codes.append(int(line.split(':', 1)[1]))

    if count < 1 or summaries != [(count, 0, 0, count, 0)] or report_codes != [0] or runner_codes != [0]:
        raise ValueError(f'{suite}: expected {count} passes; Hypium summaries {summaries}, report {report_codes}, runner {runner_codes}')
    if (len(results) != count or len(set(results)) != count or
            any(name != suite or not test or status != 0 for name, test, status in results)):
        raise ValueError(f'{suite}: incorrect test completions {results}')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('log', type=Path)
    parser.add_argument('--suite', required=True)
    parser.add_argument('--count', required=True, type=int)
    args = parser.parse_args()
    try:
        verify(args.log.read_text(), args.suite, args.count)
    except ValueError as error:
        parser.exit(1, f'{error}\n')
    print(f'{args.suite}: {args.count} passed')
