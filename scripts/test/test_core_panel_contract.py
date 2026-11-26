"""Keep the Core-to-panel contract's routes, events, and citations tied to source.

The contract is a Markdown document, not a second registry. A route or WebSocket
name in its contract tables must occur in one of that row's cited source files.
The negative tests mutate the real document, so an invented name cannot pass
merely because the parser or repository fixture is empty.

CI discovers this file through scripts/ci/python-tests.py.
"""

from pathlib import Path
import re
import unittest


ROOT = Path(__file__).resolve().parents[2]
DOC = "docs/architecture/core-panel-contract.md"
CONTRACT = ROOT / DOC

FENCE = re.compile(r"^[ \t]*(`{3,}|~{3,})(.*)$")
HEADING = re.compile(r"^##\s+(.+?)\s*#*\s*$")
ROUTE = re.compile(r"^(GET|POST|PUT|PATCH|DELETE)\s+(/api/v1/[^\s`]+)$")
EVENT = re.compile(r"^[a-z][a-z0-9_.-]*$")
REPOSITORY_PATH = re.compile(r"`((?:server|web|docs|scripts)/[^`\s]+)`")
LINE_SUFFIX = re.compile(r":\d+$")

ROUTE_HEADERS = {"http route", "http request"}
EVENT_HEADERS = {"ws event", "ws frame", "websocket event", "websocket frame"}
EVIDENCE_HEADERS = {"source evidence", "evidence source"}
REQUIRED_SECTIONS = {
    "Sessions": "route",
    "Pending requests / asks": "route",
    "Tasks": "route",
    "To-dos": "route",
    "Actions": "route",
    "Realtime events": "event",
    "Existing data when a module is off": "route",
}


def normalized(text):
    return re.sub(r"\s+", " ", text.replace("`", "").strip().casefold())


def visible_lines(text):
    """Return numbered lines outside fenced code blocks."""
    fence = ""
    for number, line in enumerate(text.splitlines(), 1):
        mark = FENCE.match(line)
        if not fence:
            if mark and not (mark[1][0] == "`" and "`" in mark[2]):
                fence = mark[1]
            else:
                yield number, line
        elif mark and mark[1][0] == fence[0] and len(mark[1]) >= len(fence) and not mark[2].strip():
            fence = ""


def table_cells(line):
    line = line.strip()
    if line.startswith("|"):
        line = line[1:]
    if line.endswith("|") and not line.endswith(r"\|"):
        line = line[:-1]
    return [cell.strip().replace(r"\|", "|") for cell in re.split(r"(?<!\\)\|", line)]


def delimiter_row(cells):
    return bool(cells) and all(re.fullmatch(r":?-{3,}:?", cell.replace(" ", "")) for cell in cells)


def column(headers, names):
    for index, header in enumerate(headers):
        if normalized(header) in names:
            return index
    return None


def markdown_tables(text):
    """Parse pipe tables outside fences, retaining each row's source line."""
    lines = text.splitlines()
    visible = dict(visible_lines(text))
    found = []
    for number, line in visible.items():
        if not line.strip().startswith("|") or number + 1 not in visible:
            continue
        headers = table_cells(line)
        delimiter = table_cells(visible[number + 1])
        if len(headers) != len(delimiter) or not delimiter_row(delimiter):
            continue
        rows = []
        following = number + 2
        while following in visible and visible[following].strip().startswith("|"):
            rows.append({"line": following, "cells": table_cells(visible[following])})
            following += 1
        found.append({"headers": headers, "rows": rows})
    return found


def backticked_paths(text):
    """Return repository-relative paths, allowing an optional trailing line number."""
    for token in REPOSITORY_PATH.findall(text):
        yield LINE_SUFFIX.sub("", token).rstrip(".,;")


def contract_rows(text):
    """Return route/event rows and structural errors from the contract tables."""
    rows, errors = [], []
    for table in markdown_tables(text):
        headers = table["headers"]
        route_column = column(headers, ROUTE_HEADERS)
        event_column = column(headers, EVENT_HEADERS)
        if route_column is None and event_column is None:
            continue
        evidence_column = column(headers, EVIDENCE_HEADERS)
        if evidence_column is None:
            errors.append(f"{DOC}: a route/event table needs a Source evidence column")
            continue
        for row in table["rows"]:
            number, cells = row["line"], row["cells"]
            if len(cells) != len(headers):
                errors.append(f"{DOC}:{number}: expected {len(headers)} columns, found {len(cells)}")
                continue
            evidence = list(backticked_paths(cells[evidence_column]))
            if not evidence:
                errors.append(f"{DOC}:{number}: Source evidence names no repository file")
            if route_column is not None:
                raw = cells[route_column].strip()
                match = ROUTE.fullmatch(raw.strip("`"))
                if not match:
                    errors.append(f"{DOC}:{number}: invalid HTTP route cell {raw!r}")
                    continue
                rows.append({"kind": "route", "name": match.group(1) + " " + match.group(2),
                             "line": number, "evidence": evidence})
            if event_column is not None:
                raw = cells[event_column].strip()
                name = raw.strip("`")
                if raw != f"`{name}`" or not EVENT.fullmatch(name):
                    errors.append(f"{DOC}:{number}: invalid WebSocket event cell {raw!r}")
                    continue
                rows.append({"kind": "event", "name": name, "line": number, "evidence": evidence})
    return rows, errors


def sections(text):
    """Return H2 bodies with their one-based start and end lines."""
    lines = text.splitlines()
    visible = dict(visible_lines(text))
    headings = [(number, match.group(1)) for number, line in visible.items()
                for match in [HEADING.match(line)] if match]
    found = {}
    for index, (start, title) in enumerate(headings):
        end = headings[index + 1][0] if index + 1 < len(headings) else len(lines) + 1
        found[title] = (start, end, "\n".join(lines[start - 1:end - 1]))
    return found


def source_literal(name):
    """The exact string literal in either Go/TypeScript quotes or a Go raw string."""
    return (f'"{name}"', f"`{name}`")


def validate(root, text):
    rows, errors = contract_rows(text)
    if not rows:
        errors.append(f"{DOC}: no route or WebSocket contract rows found")

    checked_paths = set()
    for path in backticked_paths(text):
        if path in checked_paths:
            continue
        checked_paths.add(path)
        candidate = root / path
        if Path(path).is_absolute() or ".." in Path(path).parts:
            errors.append(f"{DOC}: {path} is not repository-relative")
        elif not candidate.exists():
            errors.append(f"{DOC}: missing referenced path {path}")

    source_cache = {}

    def read_source(path):
        if path not in source_cache:
            source_cache[path] = (root / path).read_text(encoding="utf-8", errors="replace")
        return source_cache[path]

    for row in rows:
        where = f"{DOC}:{row['line']}"
        if not row["evidence"]:
            continue
        evidence_texts = []
        for path in row["evidence"]:
            source = root / path
            if not source.is_file():
                errors.append(f"{where}: evidence is not a file: {path}")
            else:
                evidence_texts.append(read_source(path))
        if not evidence_texts:
            continue
        literals = source_literal(row["name"])
        if not any(literal in source for source in evidence_texts for literal in literals):
            errors.append(f"{where}: {row['kind']} {row['name']!r} has no exact string literal "
                          f"in its cited source evidence")

    document_sections = sections(text)
    for title, kind in REQUIRED_SECTIONS.items():
        if title not in document_sections:
            errors.append(f"{DOC}: missing required H2 {title!r}")
            continue
        start, end, body = document_sections[title]
        if not any(row["kind"] == kind and start < row["line"] < end for row in rows):
            errors.append(f"{DOC}:{start}: {title!r} has no {kind} contract row")
        if title == "Existing data when a module is off":
            for concept in ("read", "delete", "rewrite"):
                if not re.search(rf"\b{concept}\w*\b", body, re.I):
                    errors.append(f"{DOC}:{start}: the module-off boundary does not state {concept!r} semantics")
    return errors


def replace_row_value(text, row, value):
    lines = text.splitlines()
    index = row["line"] - 1
    old = f"`{row['name']}`"
    if old not in lines[index]:
        raise AssertionError(f"cannot mutate {old!r} on line {row['line']}")
    lines[index] = lines[index].replace(old, f"`{value}`", 1)
    return "\n".join(lines) + "\n"


class CorePanelContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.text = CONTRACT.read_text(encoding="utf-8")
        cls.rows, _ = contract_rows(cls.text)

    def test_repository_contract_matches_cited_source(self):
        self.assertEqual([], validate(ROOT, self.text))

    def test_invented_route_is_rejected(self):
        row = next(row for row in self.rows if row["kind"] == "route")
        invented = "GET /api/v1/core-panel-contract-invented"
        changed = replace_row_value(self.text, row, invented)
        errors = validate(ROOT, changed)
        self.assertTrue(any(invented in error and "no exact string literal" in error for error in errors), errors)

    def test_invented_websocket_event_is_rejected(self):
        row = next(row for row in self.rows if row["kind"] == "event")
        invented = "core_panel_contract_invented"
        changed = replace_row_value(self.text, row, invented)
        errors = validate(ROOT, changed)
        self.assertTrue(any(invented in error and "no exact string literal" in error for error in errors), errors)

    def test_invented_source_path_is_rejected(self):
        row = next(row for row in self.rows if row["evidence"])
        old = row["evidence"][0]
        invented = "server/internal/hub/core_panel_contract_invented.go"
        lines = self.text.splitlines()
        index = row["line"] - 1
        self.assertIn(f"`{old}`", lines[index])
        lines[index] = lines[index].replace(f"`{old}`", f"`{invented}`", 1)
        changed = "\n".join(lines) + "\n"
        errors = validate(ROOT, changed)
        self.assertTrue(any(invented in error for error in errors), errors)


if __name__ == "__main__":
    unittest.main()
