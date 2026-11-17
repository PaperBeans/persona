#!/usr/bin/env python3
"""Publish only the pointer for verified immutable beta artifacts.

The beta-channel prerelease contains latest.json and optionally android.json,
not replacement binaries. Those index files belong to the immutable beta tag
whose bytes have already been uploaded and verified. The pointer never becomes
GitHub's stable releases/latest.
"""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys

from tag_release import ConfigError, is_beta_tag, read_receipt, resolve_tag, verify_remote_assets

POINTER = "beta-channel"
BETA = re.compile(r"0|[1-9][0-9]*")


def get_json(gh, path):
    return json.loads(subprocess.check_output([gh, "api", path], text=True, timeout=60))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", required=True, type=Path)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--receipt", required=True, type=Path)
    parser.add_argument("--gh", default="gh")
    args = parser.parse_args()
    if not is_beta_tag(args.tag) or not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", args.repository):
        raise ConfigError("a versioned vX.Y.Z-beta.N tag and owner/repository are required")
    commit = resolve_tag(args.repo, args.repository, args.tag)
    assets = read_receipt(args.receipt, commit)
    index = {item["name"]: Path(item["path"]) for item in assets}
    if "latest.json" not in index:
        raise ConfigError("beta pointer needs the verified tag's latest.json")
    manifest = json.loads(index["latest.json"].read_text())
    if manifest.get("channel") != "beta" or manifest.get("version") != args.tag[1:]:
        raise ConfigError("latest.json is not this beta version's beta-channel manifest")
    # The pointer may copy only the indexes, never binaries from another tag.
    for name in ("latest.json", "android.json"):
        if name not in index:
            continue
        remote = json.loads(subprocess.check_output(
            [args.gh, "api", f"repos/{args.repository}/releases/tags/{args.tag}"], text=True, timeout=60))
        if remote.get("tag_name") != args.tag or remote.get("draft") is not False or remote.get("prerelease") is not True:
            raise ConfigError("immutable beta tag must be a published prerelease")
        verify_remote_assets(args.repository, args.tag, [item for item in assets if item["name"] == name],
                             remote.get("assets", []), args.gh, missing=True)
    pointer = None
    try:
        pointer = get_json(args.gh, f"repos/{args.repository}/releases/tags/{POINTER}")
        if pointer.get("draft") or not pointer.get("prerelease") or pointer.get("tag_name") != POINTER:
            raise ConfigError("beta-channel must be a prerelease, never a stable Release")
    except subprocess.CalledProcessError:
        subprocess.run([args.gh, "release", "create", POINTER, "--repo", args.repository,
                        "--prerelease", "--title", "Persona beta updates",
                        "--notes", "The beta-channel pointer only. Binaries remain on immutable versioned prereleases."], check=True)
    if pointer is not None and pointer.get("target_commitish") not in ("", commit):
        raise ConfigError("beta pointer is owned by another immutable source; reconcile it explicitly")
    remote = get_json(args.gh, f"repos/{args.repository}/releases/tags/{POINTER}")
    existing = {asset["name"]: asset for asset in remote.get("assets", [])}
    pointers = []
    for name in ("latest.json", "android.json"):
        if name not in index:
            continue
        item = {"path": str(index[name]), "name": name}
        if name in existing:
            verify_remote_assets(args.repository, POINTER, [item], remote.get("assets", []), args.gh)
            print(f"Beta pointer {name} already has the verified digest; keeping it")
        else:
            pointers.append(item)
    if not pointers:
        print(f"Beta pointer {POINTER} already describes {args.tag} ({commit})")
        return
    subprocess.run([args.gh, "release", "edit", POINTER, "--repo", args.repository,
                    "--target", commit, "--prerelease"], check=True, timeout=60)
    # One small request, then independent remote SHA-256 verification.
    subprocess.run([args.gh, "release", "upload", POINTER, "--repo", args.repository, "--clobber",
                    *(item["path"] for item in pointers)], check=True, timeout=120)
    remote = get_json(args.gh, f"repos/{args.repository}/releases/tags/{POINTER}")
    verify_remote_assets(args.repository, POINTER, pointers, remote.get("assets", []), args.gh, missing=True)
    print(f"Verified beta pointer {POINTER} -> {args.tag} ({commit})")


if __name__ == "__main__":
    try:
        main()
    except (ConfigError, OSError, ValueError, subprocess.CalledProcessError) as error:
        print("Beta pointer publication failed: " + str(error), file=sys.stderr)
        raise SystemExit(1)
