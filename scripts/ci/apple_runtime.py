"""Select an installed stable Apple runtime for the minimum compiler or the iOS deployment floor."""

import argparse
import json
from pathlib import Path
import re
import subprocess
import sys
import time


def select(runtimes, platform="iOS", version="26.0"):
    pattern = re.escape(version) + (r"(?:\.[0-9]+)*" if version == "26.0" else "")
    compatible = []
    for runtime in runtimes:
        installed = runtime.get("version", "")
        identifier = runtime.get("identifier", "")
        name = runtime.get("name", "").lower()
        if (runtime.get("isAvailable") is True
                and identifier.startswith("com.apple.CoreSimulator.SimRuntime." + platform + "-")
                and re.fullmatch(pattern, installed)
                and "beta" not in name and "release candidate" not in name):
            compatible.append(runtime)
    return max(compatible, key=lambda runtime: tuple(int(part) for part in runtime["version"].split(".")))["identifier"] if compatible else None


def download(platform, version="26.0", attempts=3, pause=10, runner=subprocess.run, sleeper=time.sleep, clock=time.monotonic):
    """Download the pinned platform, retrying failed downloads within the existing 1200-second total budget."""
    command = ["xcodebuild", "-downloadPlatform", platform, "-architectureVariant", "universal"]
    if version != "26.0":
        command = ["xcodebuild", "-downloadPlatform", platform, "-buildVersion", version]
    deadline = clock() + 1200
    for attempt in range(attempts):
        remaining = deadline - clock()
        if remaining <= 0:
            return 124
        try:
            result = runner(command, timeout=max(1, remaining))
        except subprocess.TimeoutExpired:
            return 124
        if result.returncode == 0:
            return 0
        if attempt + 1 < attempts:
            print(f"apple-ci: {platform} runtime download failed; retrying after {pause}s", file=sys.stderr, flush=True)
            sleeper(min(pause, max(0, deadline - clock())))
    return result.returncode or 1


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("runtimes_json", nargs="?")
    parser.add_argument("--download", action="store_true")
    parser.add_argument("--platform", choices=("iOS", "watchOS"), default="iOS")
    parser.add_argument("--version", choices=("26.0", "18.6"), default="26.0")
    args = parser.parse_args()
    if args.download:
        if args.runtimes_json is not None:
            parser.error("runtimes_json is not used with --download")
        sys.exit(download(args.platform, args.version))
    if args.runtimes_json is None:
        parser.error("runtimes_json is required unless --download is used")
    selected = select(json.loads(Path(args.runtimes_json).read_text())["runtimes"], args.platform, args.version)
    if not selected:
        sys.exit("apple-ci: FAIL no available stable " + args.platform + " " + args.version + " runtime")
    print(selected)


if __name__ == "__main__":
    main()
