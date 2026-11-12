"""Check the one-second UI budget from synthesized input, excluding pre-input XCTest lookup/idle waits.

python3 -I apple/verify/send-feedback/check_timing.py <xcodebuild UI log>
Fails closed on missing/failed observations or unrecognized input timestamps.
"""
import json
import re
import sys
from pathlib import Path

STEP = re.compile(r"t\s*=\s*([0-9.]+)s\s+(.*)")
RESULT = re.compile(r"SEND_FEEDBACK (agent|assistant) processing_visible=(true|false) seconds=([0-9.]+)")


def measurements(text):
    tap = event = None
    rows = []
    for line in text.splitlines():
        step = STEP.search(line)
        if step:
            at, action = float(step[1]), step[2]
            if 'Tap "shell-composer-send" Button' in action:
                tap, event = at, None
            elif tap is not None and event is None and "Synthesize event" in action:
                event = at
        result = RESULT.search(line)
        if not result:
            continue
        if result[2] != "true" or tap is None or event is None:
            raise ValueError("Missing successful visible-state observation or synthesized-input timestamp")
        command = float(result[3])
        elapsed = tap + command - event
        # Step logs round to 10 ms. Include 20 ms rather than hiding the uncertainty.
        upper = elapsed + 0.02
        if elapsed < 0 or upper >= 1:
            raise ValueError(f"{result[1]} input-to-visible budget failed: {elapsed:.3f}s, upper bound {upper:.3f}s")
        rows.append({"chat": result[1], "input_to_visible_seconds": round(elapsed, 3),
                     "conservative_upper_seconds": round(upper, 3), "automation_command_seconds": command,
                     "pre_input_automation_seconds": round(event - tap, 3)})
        tap = event = None
    if sorted(r["chat"] for r in rows) != ["agent", "assistant"]:
        raise ValueError("Expected exactly one successful main and Agent observation")
    return rows


if __name__ == "__main__":
    try:
        rows = measurements(Path(sys.argv[1]).read_text())
    except (OSError, ValueError, IndexError) as error:
        print(f"send-feedback timing: FAIL {error}", file=sys.stderr)
        sys.exit(1)
    print(json.dumps({"verdict": "PASS", "measurements": rows}, indent=2))
