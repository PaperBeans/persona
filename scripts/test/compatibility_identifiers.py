"""Exact, file-scoped compatibility identifiers; independent of privacy and repository allowlists."""
import json
from pathlib import Path, PurePosixPath
import re
import subprocess

ROOT = Path(__file__).resolve().parents[2]
REGISTRY = "scripts/compatibility-identifiers.json"
FORMER = "jar" + "vis"
# Tokenize once before testing the brand substring: a greedy prefix regex becomes
# quadratic on long opaque strings such as embedded installer source or fixture payloads.
TOKEN = re.compile(r"[A-Za-z0-9_.-]+")
SHORT_SCHEME = re.compile(r"(?<![A-Za-z0-9_])j://", re.IGNORECASE)
# Most repository documents cannot contain a compatibility identifier. Search the complete document first, then
# preserve exact per-line findings only for candidates instead of tokenizing every line of every file.
CANDIDATE = re.compile(re.escape(FORMER) + r"|(?<![A-Za-z0-9_])j://", re.IGNORECASE)
EXTERNAL_REPOSITORY = "nwverse/" + FORMER


def tokens(text):
    return ({match.group(0) for match in TOKEN.finditer(text) if FORMER in match.group(0).lower()}
            | {match.group(0) for match in SHORT_SCHEME.finditer(text)})


def load_registry(path=None):
    def unique(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise ValueError("duplicate registry key: " + key)
            result[key] = value
        return result

    data = json.loads(Path(path or ROOT / REGISTRY).read_text(), object_pairs_hook=unique)
    if set(data) != {"version", "files"} or data["version"] != 1 or not isinstance(data["files"], dict):
        raise ValueError("invalid compatibility registry schema")
    for name, entry in data["files"].items():
        relative = PurePosixPath(name)
        if relative.is_absolute() or name != relative.as_posix() or ".." in relative.parts or any(character in name for character in "*?[]\\"):
            raise ValueError("registry paths must be exact repository-relative file paths")
        fields = {"reason", "tokens", "path_tokens", "external_refs"}
        if not isinstance(entry, dict) or not fields <= set(entry) or set(entry) - fields - {"symlink_target"}:
            raise ValueError("invalid registry file entry: " + name)
        if "symlink_target" in entry:
            target = entry["symlink_target"]
            if not isinstance(target, str) or not target or PurePosixPath(target).is_absolute() or tokens(target) or any(character in target for character in "*?[]\\"):
                raise ValueError("symlink targets must name an exact canonical entrypoint")
        if not isinstance(entry["reason"], str) or not entry["reason"].strip() or tokens(entry["reason"]):
            raise ValueError("registry reasons must explain the contract without extra legacy identifiers")
        for field in ("tokens", "path_tokens", "external_refs"):
            values = entry[field]
            if not isinstance(values, list) or any(not isinstance(value, str) or not value for value in values) or values != sorted(set(values)):
                raise ValueError("registry identifiers must be sorted unique exact strings")
        for value in entry["tokens"] + entry["path_tokens"]:
            if tokens(value) != {value}:
                raise ValueError("registry tokens must be complete legacy identifiers")
        if set(entry["path_tokens"]) != tokens(name):
            raise ValueError("registry path tokens must match the exact filename")
        if any(value != EXTERNAL_REPOSITORY for value in entry["external_refs"]):
            raise ValueError("compatibility cannot authorize another external repository")
    return data["files"]


def registered_external_ref(path, reference, registry=None):
    registry = load_registry() if registry is None else registry
    if path == REGISTRY:
        # The validated ledger may describe only references explicitly registered to actual carriers.
        return reference == EXTERNAL_REPOSITORY and any(reference in entry["external_refs"] for entry in registry.values())
    return reference in registry.get(path, {}).get("external_refs", ())


def findings(path, text, registry):
    entry = registry.get(path, {})
    found = [(0, value) for value in sorted(tokens(path) - set(entry.get("path_tokens", ())))]
    if not CANDIDATE.search(text):
        return found
    for number, line in enumerate(text.splitlines(), 1):
        found.extend((number, value) for value in sorted(tokens(line) - set(entry.get("tokens", ()))))
    return found


def repository_paths(root=ROOT):
    result = subprocess.run(["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"],
                            cwd=root, check=True, capture_output=True, timeout=30)
    return sorted(set(filter(None, result.stdout.decode().split("\0"))))


def repository_findings(root=ROOT, registry=None, paths=None):
    registry = load_registry(Path(root) / REGISTRY) if registry is None else registry
    paths = repository_paths(root) if paths is None else paths
    present, present_refs, found = {}, {}, []
    for name in paths:
        file = Path(root) / name
        if not file.is_file() and not file.is_symlink():
            continue
        if name == REGISTRY:
            # Schema validation permits legacy text only in exact contract fields, never arbitrary metadata.
            continue
        text = ""
        if file.is_symlink():
            target = file.readlink().as_posix()
            text = target
            if name in registry and registry[name].get("symlink_target") != target:
                found.append(name + ": compatibility symlink target differs from its exact registration")
        else:
            if name in registry and "symlink_target" in registry[name]:
                found.append(name + ": compatibility entrypoint is no longer a symlink")
            content = file.read_bytes()
            if b"\0" not in content[:8192]:
                text = content.decode("utf-8", errors="ignore")
        present[name] = tokens(text) if CANDIDATE.search(text) else set()
        present_refs[name] = set(re.findall(r"\bnwverse/[\w.-]+", text))
        found.extend(f"{name}:{line}: unregistered compatibility identifier {value}" for line, value in findings(name, text, registry))
    for name, entry in registry.items():
        if name not in present:
            found.append(name + ": stale registry path")
        else:
            for value in sorted(set(entry["tokens"]) - present[name]):
                found.append(name + ": stale registered token " + value)
            for value in sorted(set(entry["external_refs"]) - present_refs[name]):
                found.append(name + ": stale registered repository reference " + value)
    return found
