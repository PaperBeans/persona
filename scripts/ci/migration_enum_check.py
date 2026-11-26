#!/usr/bin/env python3
"""Fail when a migration adds an enum-like text column without a CHECK, or a JSON column without a shape (#15397).

The plan for a strongly typed Core (docs/plans/plan-typed-core.md, sections 1.7 and 3, step P9) wants storage to hold
no illegal values. This replays the migrations of each set (server/internal/db/migrations/hub and .../manager, in the
order the runner applies them) into a model of the final schema, and names every column, as "table.column", that is

- a text (or varchar) column whose name ends in status, kind, state, role, phase or outcome, with no CHECK constraint
  that names the column; or
- a json or jsonb column with no CHECK that names it (a shape check: jsonb_typeof, required keys, a size bound) and
  no SQL comment saying "opaque: <reason>".

A CHECK counts wherever it is declared: inline on the column, as a table constraint, or added by a later migration
(ALTER TABLE ... ADD [CONSTRAINT name] CHECK (...), NOT VALID included). A CHECK a later migration drops (DROP
CONSTRAINT) no longer counts, nor does one whose column was dropped. Renamed tables and columns are followed. Generated
columns are left out: nothing writes them. The opaque note is a comment on the column's own line or on the lines just
before it, inside CREATE TABLE or ALTER TABLE ... ADD COLUMN, or COMMENT ON COLUMN table.column IS 'opaque: <reason>'.
Bodies in dollar quotes (functions, DO blocks) are not read.

The names that predate the check are in scripts/ci/migration_enum_baseline.json, {"hub": [...], "manager": [...]}.
Only a new name fails, with how to fix it; a name that is gone passes with a notice. There is no ignore marker.
--update only lowers: it drops names that are gone and refuses to add one or to create a baseline. See
docs/operations/testing.md ("Count ratchets").
"""
import argparse
import json
import os
from pathlib import Path
import re
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))
import ratchet_base  # noqa: E402


def _source_root():
    configured = os.environ.get("PERSONA_CI_SOURCE_ROOT")
    if configured:
        if not Path(configured).is_absolute():
            raise ValueError("PERSONA_CI_SOURCE_ROOT must be absolute")
        return Path(configured)
    return Path(__file__).resolve().parents[2]


ROOT = _source_root()
SETS = ("hub", "manager")
MIGRATIONS = "server/internal/db/migrations"
BASELINE_REL = "scripts/ci/migration_enum_baseline.json"
ENUM_SUFFIXES = ("status", "kind", "state", "role", "phase", "outcome")
TEXT_TYPES = ("text", "varchar", "character")
JSON_TYPES = ("json", "jsonb")
OPAQUE = re.compile(r"\bopaque:\s*\S", re.IGNORECASE)
IDENT = re.compile(r'"([^"]+)"|([A-Za-z_][A-Za-z0-9_$]*)')
VERSION = re.compile(r"^([0-9]+)_")
# Words that start a table constraint, not a column, inside CREATE TABLE ( ... ).
TABLE_CONSTRAINT_WORDS = ("constraint", "check", "primary", "unique", "foreign", "exclude", "like")
FIX_ENUM = ("add a CHECK ({column} IN (...)) listing every value the code writes, the Go constants cited in a "
            "comment (docs/plans/plan-typed-core.md, step P9)")
FIX_JSON = ("add a shape CHECK (for example CHECK (jsonb_typeof({column}) = 'object')), or say why it has none in a "
            "comment on the column: -- opaque: <reason>")


class MigrationError(Exception):
    """The check could not be run (a broken baseline, or a migration this parser does not follow)."""


# --- Lexing ----------------------------------------------------------------------------------------------------------

def split_statements(text):
    """The statements of a migration, split on top-level semicolons, comments kept and dollar-quoted bodies blanked."""
    statements, current, i, n = [], [], 0, len(text)
    while i < n:
        c = text[i]
        if c == "-" and text.startswith("--", i):
            end = text.find("\n", i)
            end = n if end < 0 else end
            current.append(text[i:end])
            i = end
        elif c == "/" and text.startswith("/*", i):
            end = text.find("*/", i + 2)
            end = n if end < 0 else end + 2
            current.append(text[i:end])
            i = end
        elif c == "'":
            end = i + 1
            while end < n:
                if text[end] == "'":
                    if end + 1 < n and text[end + 1] == "'":
                        end += 2
                        continue
                    break
                end += 1
            current.append(text[i:end + 1])
            i = end + 1
        elif c == "$":
            match = re.match(r"\$([A-Za-z_][A-Za-z0-9_]*)?\$", text[i:])
            if match:
                tag = match.group(0)
                end = text.find(tag, i + len(tag))
                end = n if end < 0 else end + len(tag)
                current.append("$$ $$")
                i = end
            else:
                current.append(c)
                i += 1
        elif c == ";":
            statements.append("".join(current))
            current = []
            i += 1
        else:
            current.append(c)
            i += 1
    if "".join(current).strip():
        statements.append("".join(current))
    return statements


def strip_comments(text):
    """The text without -- and /* */ comments (quotes respected)."""
    out, i, n = [], 0, len(text)
    while i < n:
        c = text[i]
        if text.startswith("--", i):
            end = text.find("\n", i)
            i = n if end < 0 else end
        elif text.startswith("/*", i):
            end = text.find("*/", i + 2)
            i = n if end < 0 else end + 2
            out.append(" ")
        elif c == "'":
            end = i + 1
            while end < n:
                if text[end] == "'":
                    if end + 1 < n and text[end + 1] == "'":
                        end += 2
                        continue
                    break
                end += 1
            out.append(text[i:end + 1])
            i = end + 1
        else:
            out.append(c)
            i += 1
    return "".join(out)


def comments_of(text):
    """The comment texts in text, in order."""
    found = []
    i, n = 0, len(text)
    while i < n:
        if text.startswith("--", i):
            end = text.find("\n", i)
            end = n if end < 0 else end
            found.append(text[i + 2:end])
            i = end
        elif text.startswith("/*", i):
            end = text.find("*/", i + 2)
            end = n if end < 0 else end
            found.append(text[i + 2:end])
            i = end + 2
        elif text[i] == "'":
            end = text.find("'", i + 1)
            i = n if end < 0 else end + 1
        else:
            i += 1
    return found


def split_top(text):
    """text split on commas outside parentheses, quotes and comments; each part keeps its comments."""
    parts, current, depth, i, n = [], [], 0, 0, len(text)
    while i < n:
        c = text[i]
        if text.startswith("--", i):
            end = text.find("\n", i)
            end = n if end < 0 else end
            current.append(text[i:end])
            i = end
            continue
        if text.startswith("/*", i):
            end = text.find("*/", i + 2)
            end = n if end < 0 else end + 2
            current.append(text[i:end])
            i = end
            continue
        if c == "'":
            end = i + 1
            while end < n and not (text[end] == "'" and not text.startswith("''", end)):
                end += 2 if text.startswith("''", end) else 1
            current.append(text[i:end + 1])
            i = end + 1
            continue
        if c == "(":
            depth += 1
        elif c == ")":
            depth -= 1
        if c == "," and depth == 0:
            parts.append("".join(current))
            current = []
        else:
            current.append(c)
        i += 1
    parts.append("".join(current))
    return parts


def attach_comments(parts):
    """[(code, comments)] for comma-separated parts: a comment on the line a part ends (after its comma) is moved from
    the next part to it, and the comment lines before a part stay with it."""
    out = []
    for index, part in enumerate(parts):
        head, sep, rest = part.partition("\n")
        if index > 0 and sep and not strip_comments(head).strip():
            out[-1][1].extend(comments_of(head))
            part = rest
        out.append([strip_comments(part).strip(), comments_of(part)])
    if out and not out[-1][0] and out[-1][1] and len(out) > 1:
        out[-2][1].extend(out.pop()[1])
    return [(code, comments) for code, comments in out if code]


def matching_paren(text, start):
    """The index of the parenthesis closing the one at start (comments already stripped)."""
    depth, i = 0, start
    while i < len(text):
        c = text[i]
        if c == "'":
            end = text.find("'", i + 1)
            i = len(text) if end < 0 else end + 1
            continue
        if c == "(":
            depth += 1
        elif c == ")":
            depth -= 1
            if depth == 0:
                return i
        i += 1
    raise MigrationError("unbalanced parentheses")


def ident(token):
    """An identifier, unquoted and folded to lower case unless it was quoted."""
    if token.startswith('"') and token.endswith('"'):
        return token[1:-1]
    return token.lower()


def bare(name):
    """A table name without its schema."""
    return ident(name.split(".")[-1])


def idents_in(expr):
    """The identifiers an expression names, outside string literals."""
    expr = re.sub(r"'(?:[^']|'')*'", " ", expr)
    return {(m.group(1) if m.group(1) is not None else m.group(2).lower()) for m in IDENT.finditer(expr)}


# --- The schema model ------------------------------------------------------------------------------------------------

class Column:
    def __init__(self, name, kind, opaque, generated):
        self.name, self.kind, self.opaque, self.generated = name, kind, opaque, generated


class Table:
    def __init__(self, name):
        self.name = name
        self.columns = {}  # name -> Column, in order
        self.checks = {}  # constraint name -> set of identifiers its expression names

    def default_check_name(self, column):
        base = "{0}_{1}_check".format(self.name, column)
        name, n = base, 1
        while name in self.checks:
            name = "{0}{1}".format(base, n)
            n += 1
        return name

    def add_check(self, name, expr, column=None):
        refs = idents_in(expr)
        if name is None:
            first = column or next((c for c in self.columns if c in refs), "")
            name = self.default_check_name(first)
        if column:
            refs.add(column)
        self.checks[name] = refs

    def checked(self, column):
        return any(column in refs for refs in self.checks.values())


def column_kind(rest):
    """"text", "json" or None from what follows a column's name."""
    first = rest.split(None, 1)[0].lower() if rest.split() else ""
    first = first.split("(")[0]
    if first.endswith("[]"):
        return None
    if first in TEXT_TYPES:
        return "text"
    if first in JSON_TYPES:
        return "json"
    return None


INLINE_CHECK = re.compile(r"\b(?:constraint\s+(\"[^\"]+\"|[A-Za-z_][A-Za-z0-9_]*)\s+)?check\s*\(", re.IGNORECASE)


def add_column(table, code, comments):
    """A column definition: name type [constraints ...]."""
    match = re.match(r'\s*("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)\s+(.*)$', code, re.DOTALL)
    if not match:
        raise MigrationError("cannot read the column definition {0!r}".format(code[:80]))
    name, rest = ident(match.group(1)), match.group(2)
    generated = re.search(r"\bgenerated\s+always\s+as\s*\(", rest, re.IGNORECASE) is not None
    opaque = any(OPAQUE.search(comment) for comment in comments)
    table.columns[name] = Column(name, column_kind(rest), opaque, generated)
    for check in INLINE_CHECK.finditer(rest):
        start = check.end() - 1
        end = matching_paren(rest, start)
        table.add_check(ident(check.group(1)) if check.group(1) else None, rest[start + 1:end], column=name)


def add_table_constraint(table, code):
    match = re.match(r'\s*(?:constraint\s+("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)\s+)?check\s*\(', code, re.IGNORECASE)
    if not match:
        return  # primary key, unique, foreign key, exclude: not a CHECK
    start = match.end() - 1
    end = matching_paren(code, start)
    table.add_check(ident(match.group(1)) if match.group(1) else None, code[start + 1:end])


TABLE_NAME = r'((?:"[^"]+"|[A-Za-z_][A-Za-z0-9_]*)(?:\.(?:"[^"]+"|[A-Za-z_][A-Za-z0-9_]*))?)'
CREATE_TABLE = re.compile(r"^\s*create\s+(?:(?:temporary|temp|unlogged)\s+)?table\s+(if\s+not\s+exists\s+)?"
                          + TABLE_NAME + r"\s*\(", re.IGNORECASE)
ALTER_TABLE = re.compile(r"^\s*alter\s+table\s+(if\s+exists\s+)?(?:only\s+)?" + TABLE_NAME + r"\s+(.*)$",
                         re.IGNORECASE | re.DOTALL)
DROP_TABLE = re.compile(r"^\s*drop\s+table\s+(if\s+exists\s+)?(.*?)(?:\s+(?:cascade|restrict))?\s*$",
                        re.IGNORECASE | re.DOTALL)
COMMENT_ON = re.compile(r"^\s*comment\s+on\s+column\s+" + TABLE_NAME + r"\.(\"[^\"]+\"|[A-Za-z_][A-Za-z0-9_]*)"
                        r"\s+is\s+'((?:[^']|'')*)'\s*$", re.IGNORECASE | re.DOTALL)


class Schema:
    def __init__(self):
        self.tables = {}

    def apply(self, statement):
        code = strip_comments(statement).strip()
        if not code:
            return
        create = CREATE_TABLE.match(code)
        if create:
            name = bare(create.group(2))
            if name in self.tables:
                if create.group(1):
                    return
                raise MigrationError("table {0} is created twice".format(name))
            self.create(name, statement)
            return
        alter = ALTER_TABLE.match(code)
        if alter:
            name = bare(alter.group(2))
            if name not in self.tables:
                if alter.group(1):
                    return
                raise MigrationError("ALTER TABLE {0}: no such table".format(name))
            self.alter(name, statement)
            return
        drop = DROP_TABLE.match(code)
        if drop:
            for name in drop.group(2).split(","):
                self.tables.pop(bare(name.strip()), None)
            return
        comment = COMMENT_ON.match(code)
        if comment:
            table = self.tables.get(bare(comment.group(1)))
            column = table and table.columns.get(ident(comment.group(2)))
            if column and OPAQUE.search(comment.group(3)):
                column.opaque = True

    def create(self, name, statement):
        # Find the body in the statement with comments kept: the parenthesis after the name.
        open_at = self._body_start(statement)
        close_at = self._body_end(statement, open_at)
        table = Table(name)
        for code, comments in attach_comments(split_top(statement[open_at + 1:close_at])):
            first = re.match(r"[A-Za-z_]*", code).group(0).lower()
            if first in TABLE_CONSTRAINT_WORDS:
                add_table_constraint(table, code)
            else:
                add_column(table, code, comments)
        self.tables[name] = table

    @staticmethod
    def _body_start(statement):
        # The first "(" outside comments.
        lowered = statement
        i, n = 0, len(lowered)
        while i < n:
            if lowered.startswith("--", i):
                end = lowered.find("\n", i)
                i = n if end < 0 else end
                continue
            if lowered.startswith("/*", i):
                end = lowered.find("*/", i + 2)
                i = n if end < 0 else end + 2
                continue
            if lowered[i] == "(":
                return i
            i += 1
        raise MigrationError("CREATE TABLE without a body")

    @staticmethod
    def _body_end(statement, start):
        depth, i, n = 0, start, len(statement)
        while i < n:
            if statement.startswith("--", i):
                end = statement.find("\n", i)
                i = n if end < 0 else end
                continue
            if statement.startswith("/*", i):
                end = statement.find("*/", i + 2)
                i = n if end < 0 else end + 2
                continue
            c = statement[i]
            if c == "'":
                end = i + 1
                while end < n and not (statement[end] == "'" and not statement.startswith("''", end)):
                    end += 2 if statement.startswith("''", end) else 1
                i = end + 1
                continue
            if c == "(":
                depth += 1
            elif c == ")":
                depth -= 1
                if depth == 0:
                    return i
            i += 1
        raise MigrationError("CREATE TABLE body is not closed")

    def alter(self, name, statement):
        table = self.tables[name]
        # Drop the "ALTER TABLE [IF EXISTS] [ONLY] name" head, keeping comments in the actions.
        head = re.match(r"(?:\s|--[^\n]*\n|/\*.*?\*/)*alter\s+table\s+(?:if\s+exists\s+)?(?:only\s+)?"
                        + TABLE_NAME, statement, re.IGNORECASE | re.DOTALL)
        if not head:
            raise MigrationError("cannot read ALTER TABLE {0}".format(name))
        for code, comments in attach_comments(split_top(statement[head.end():])):
            self.action(table, code, comments)

    def action(self, table, code, comments):
        lowered = code.lower()
        match = re.match(r"rename\s+to\s+(\S+)$", lowered)
        if match:
            new = bare(re.match(r"rename\s+to\s+(\S+)$", code, re.IGNORECASE).group(1))
            self.tables.pop(table.name)
            table.name = new
            self.tables[new] = table
            return
        match = re.match(r'rename\s+(?:column\s+)?("[^"]+"|\S+)\s+to\s+("[^"]+"|\S+)$', code, re.IGNORECASE)
        if match and match.group(1).lower() != "constraint":
            old, new = ident(match.group(1)), ident(match.group(2))
            column = table.columns.pop(old, None)
            if column is None:
                raise MigrationError("{0}: RENAME of no such column {1}".format(table.name, old))
            column.name = new
            table.columns[new] = column
            for refs in table.checks.values():
                if old in refs:
                    refs.discard(old)
                    refs.add(new)
            return
        match = re.match(r'rename\s+constraint\s+("[^"]+"|\S+)\s+to\s+("[^"]+"|\S+)$', code, re.IGNORECASE)
        if match:
            old, new = ident(match.group(1)), ident(match.group(2))
            if old in table.checks:
                table.checks[new] = table.checks.pop(old)
            return
        match = re.match(r"add\s+(constraint\s+\S+\s+)?check\s*\(", lowered)
        if match:
            add_table_constraint(table, code[len("add"):].strip())
            return
        match = re.match(r"add\s+(constraint|primary|unique|foreign|exclude)\b", lowered)
        if match:
            return
        match = re.match(r"add\s+(?:column\s+)?(?:if\s+not\s+exists\s+)?(.*)$", code, re.IGNORECASE | re.DOTALL)
        if match:
            definition = match.group(1)
            column = re.match(r'\s*("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)', definition)
            if column and ident(column.group(1)) in table.columns and re.search(r"if\s+not\s+exists", lowered):
                return
            add_column(table, definition, comments)
            return
        match = re.match(r'drop\s+constraint\s+(?:if\s+exists\s+)?("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)', code, re.IGNORECASE)
        if match:
            table.checks.pop(ident(match.group(1)), None)
            return
        match = re.match(r'drop\s+(?:column\s+)?(?:if\s+exists\s+)?("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)', code, re.IGNORECASE)
        if match and match.group(1).lower() not in ("constraint",):
            column = ident(match.group(1))
            table.columns.pop(column, None)
            table.checks = {n: refs for n, refs in table.checks.items() if column not in refs}
            return
        match = re.match(r'alter\s+(?:column\s+)?("[^"]+"|[A-Za-z_][A-Za-z0-9_]*)\s+(?:set\s+data\s+)?type\s+(.*)$',
                         code, re.IGNORECASE | re.DOTALL)
        if match:
            column = table.columns.get(ident(match.group(1)))
            if column is not None:
                column.kind = column_kind(match.group(2))
            return
        # SET DEFAULT, SET / DROP NOT NULL, SET COMPRESSION, REPLICA IDENTITY, ...: no bearing here.


# --- The check -------------------------------------------------------------------------------------------------------

def migration_files(root, part):
    """The set's migrations in the order the runner applies them (by version)."""
    folder = Path(root) / MIGRATIONS / part
    if not folder.is_dir():
        return []
    files = [path for path in folder.iterdir() if path.suffix == ".sql" and VERSION.match(path.name)]
    return sorted(files, key=lambda path: (int(VERSION.match(path.name).group(1)), path.name))


def schema_of(root, part):
    schema = Schema()
    for path in migration_files(root, part):
        for statement in split_statements(path.read_text(encoding="utf-8")):
            try:
                schema.apply(statement)
            except MigrationError as error:
                raise MigrationError("migration enum check: {0}/{1}: {2}".format(part, path.name, error))
    return schema


def enum_like(name):
    return name.endswith(ENUM_SUFFIXES)


def findings_in(schema):
    """{"table.column": "enum" | "json"} for the columns the check names."""
    found = {}
    for table in schema.tables.values():
        for column in table.columns.values():
            if column.generated or table.checked(column.name):
                continue
            key = "{0}.{1}".format(table.name, column.name)
            if column.kind == "text" and enum_like(column.name):
                found[key] = "enum"
            elif column.kind == "json" and not column.opaque:
                found[key] = "json"
    return found


def scan(root):
    """{set: {"table.column": "enum" | "json"}}."""
    return {part: findings_in(schema_of(root, part)) for part in SETS}


def parse_baseline(text, label):
    try:
        data = json.loads(text)
    except json.JSONDecodeError as error:
        raise MigrationError("migration enum check: {0} is not valid JSON ({1})".format(label, error.msg))
    if not isinstance(data, dict) or set(data) - set(SETS):
        raise MigrationError("migration enum check: {0} must be an object with the keys {1}".format(label, ", ".join(SETS)))
    names = {}
    for part in SETS:
        listed = data.get(part, [])
        if not isinstance(listed, list) or not all(isinstance(n, str) and n.count(".") == 1 for n in listed):
            raise MigrationError("migration enum check: {0}: {1} must be a list of \"table.column\"".format(label, part))
        names[part] = set(listed)
    return names


def render(found):
    return json.dumps({part: sorted(found.get(part, ())) for part in SETS}, indent=1) + "\n"


def violations(actual, baseline):
    problems = []
    for part in SETS:
        for key, what in sorted(actual[part].items()):
            if key in baseline.get(part, set()):
                continue
            column = key.split(".", 1)[1]
            fix = (FIX_ENUM if what == "enum" else FIX_JSON).format(column=column)
            label = "an enum-like text column without a CHECK" if what == "enum" else "a JSON column without a shape CHECK or an opaque note"
            problems.append("{0} {1}: {2}; {3}".format(part, key, label, fix))
    return problems


def lowerable(actual, baseline):
    return ["{0} {1}: no longer found".format(part, key)
            for part in SETS for key in sorted(baseline.get(part, set()) - set(actual[part]))]


def check(root, notes=None):
    path = Path(root) / BASELINE_REL
    if not path.is_file():
        return ["migration enum check: baseline missing ({0})".format(BASELINE_REL)]
    actual, baseline = scan(root), parse_baseline(path.read_text(encoding="utf-8"), BASELINE_REL)
    if notes is not None:
        notes.extend(lowerable(actual, baseline))
    return violations(actual, baseline)


def update(root):
    path = Path(root) / BASELINE_REL
    if not path.is_file():
        raise MigrationError("migration enum check: no baseline at {0}; refusing to create one".format(BASELINE_REL))
    old, actual = parse_baseline(path.read_text(encoding="utf-8"), BASELINE_REL), scan(root)
    refusals = ["migration enum check: refusing to add {0} {1}".format(part, key)
                for part in SETS for key in sorted(set(actual[part]) - old.get(part, set()))]
    if refusals:
        return refusals
    rendered = render(actual)
    total = sum(len(names) for names in actual.values())
    if path.read_text(encoding="utf-8") != rendered:
        path.write_text(rendered, encoding="utf-8")
        print("migration enum check: lowered the baseline to {0} column(s)".format(total))
    else:
        print("migration enum check: baseline already matches ({0} column(s))".format(total))
    return []


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--root", default=str(ROOT), help="repository root (default: this checkout)")
    parser.add_argument("--update", action="store_true", help="lower the baseline; refuses to add a name")
    args = parser.parse_args(argv)
    try:
        notes = []
        problems = update(args.root) if args.update else check(args.root, notes=notes)
    except MigrationError as error:
        print(error, file=sys.stderr)
        return 2
    if problems:
        print("\n".join(problems), file=sys.stderr)
        return 1
    if not args.update:
        ratchet_base.print_notice("migration enum check", notes, "python3 scripts/ci/migration_enum_check.py --update")
        baseline = parse_baseline((Path(args.root) / BASELINE_REL).read_text(encoding="utf-8"), BASELINE_REL)
        print("migration enum check: {0} column(s) in the baseline, none new".format(
            sum(len(names) for names in baseline.values())))
    return 0


if __name__ == "__main__":
    sys.exit(main())
