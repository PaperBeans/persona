"""New enum-like text columns need a CHECK and new JSON columns a shape or an opaque note; the old ones only go (#15397).

The cases build throwaway trees of migrations; one case checks this checkout's committed baseline.
"""
import contextlib
import io
import json
from pathlib import Path
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))
import migration_enum_check as check

ROOT = Path(__file__).resolve().parents[2]


def tree(manager=None, hub=None, baseline=None):
    """A repository with the given migrations ({file name: SQL}) and baseline ({set: [names]}, None for none)."""
    root = Path(tempfile.mkdtemp(prefix="migration-enum-check-"))
    for part, files in (("manager", manager or {}), ("hub", hub or {})):
        folder = root / check.MIGRATIONS / part
        folder.mkdir(parents=True, exist_ok=True)
        for name, text in files.items():
            (folder / name).write_text(text, encoding="utf-8")
    if baseline is not None:
        path = root / check.BASELINE_REL
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(baseline), encoding="utf-8")
    return root


def found(sql, *later):
    """The manager set's findings for a first migration sql and later ones."""
    files = {"0001_init.sql": sql}
    for i, text in enumerate(later):
        files["2026112{0}000000_next.sql".format(i)] = text
    return check.scan(tree(manager=files))["manager"]


class FindingTests(unittest.TestCase):
    def test_an_enum_like_text_column_needs_a_check(self):
        sql = """CREATE TABLE jobs (
            id text PRIMARY KEY,
            status text NOT NULL DEFAULT 'new', -- new | done
            kind text NOT NULL,
            delivery_state varchar(20),
            role text NOT NULL CHECK (role IN ('a', 'b')),
            phase text NOT NULL,
            outcome text,
            title text NOT NULL,
            attempt_state integer NOT NULL,
            tags_kind text[] NOT NULL DEFAULT '{}',
            CONSTRAINT jobs_phase_valid CHECK (phase IN ('x', 'y'))
        );"""
        self.assertEqual(found(sql), {"jobs.status": "enum", "jobs.kind": "enum", "jobs.delivery_state": "enum",
                                      "jobs.outcome": "enum"})

    def test_a_check_added_later_counts_and_one_dropped_later_does_not(self):
        create = "CREATE TABLE jobs (id text PRIMARY KEY, state text NOT NULL, kind text NOT NULL);"
        add = "ALTER TABLE jobs ADD CONSTRAINT jobs_state_check CHECK (state IN ('a', 'b')) NOT VALID;"
        self.assertEqual(found(create, add), {"jobs.kind": "enum"})
        self.assertEqual(found(create, add, "ALTER TABLE jobs DROP CONSTRAINT jobs_state_check;"),
                         {"jobs.kind": "enum", "jobs.state": "enum"})
        unnamed = "ALTER TABLE jobs ADD CHECK (kind <> ''), ADD CONSTRAINT k CHECK (state IN ('a'));"
        self.assertEqual(found(create, unnamed), {})
        self.assertEqual(found(create, unnamed, "ALTER TABLE jobs DROP CONSTRAINT IF EXISTS jobs_kind_check;"),
                         {"jobs.kind": "enum"})

    def test_added_renamed_and_dropped_columns_and_tables_are_followed(self):
        create = "CREATE TABLE jobs (id text PRIMARY KEY, kind text NOT NULL CHECK (kind IN ('a')));"
        self.assertEqual(found(create, "ALTER TABLE jobs ADD COLUMN IF NOT EXISTS status text NOT NULL DEFAULT '';"),
                         {"jobs.status": "enum"})
        self.assertEqual(found(create, "ALTER TABLE jobs RENAME COLUMN kind TO job_kind;"), {})
        self.assertEqual(found(create, "ALTER TABLE jobs ADD status text;", "ALTER TABLE jobs RENAME TO tasks;"),
                         {"tasks.status": "enum"})
        self.assertEqual(found(create, "ALTER TABLE jobs ADD status text;", "ALTER TABLE jobs DROP COLUMN status;"), {})
        self.assertEqual(found(create, "ALTER TABLE jobs ADD status text;", "DROP TABLE IF EXISTS jobs;"), {})
        self.assertEqual(found(create, "ALTER TABLE jobs ADD COLUMN status text GENERATED ALWAYS AS (kind) STORED;"), {})
        self.assertEqual(found("CREATE TABLE jobs (id text, state jsonb CHECK (jsonb_typeof(state) = 'object'));",
                               "ALTER TABLE jobs ALTER COLUMN state TYPE text;"), {})

    def test_a_json_column_needs_a_shape_check_or_an_opaque_note(self):
        sql = """CREATE TABLE docs (
            id text PRIMARY KEY,
            body jsonb NOT NULL,
            raw json,
            shaped jsonb NOT NULL CHECK (jsonb_typeof(shaped) = 'object'),
            -- opaque: what the extension sent, read back only by it
            blob jsonb NOT NULL,
            meta jsonb, -- opaque: a client's own settings
            bare jsonb -- opaque:
        );"""
        self.assertEqual(found(sql), {"docs.body": "json", "docs.raw": "json", "docs.bare": "json"})
        self.assertEqual(found(sql, "COMMENT ON COLUMN docs.body IS 'opaque: the model''s own reply';",
                               "ALTER TABLE docs ADD COLUMN extra jsonb, -- opaque: kept as sent\n"
                               "    ADD CONSTRAINT docs_raw_shape CHECK (json_typeof(raw) = 'array');"),
                         {"docs.bare": "json"})

    def test_function_bodies_and_comments_are_not_read(self):
        sql = """-- CREATE TABLE ghosts (kind text);
        CREATE TABLE jobs (id text PRIMARY KEY, kind text CHECK (kind IN ('a')));
        CREATE FUNCTION f() RETURNS trigger LANGUAGE plpgsql AS $$
        BEGIN ALTER TABLE jobs ADD COLUMN state text; RETURN NEW; END $$;
        /* ALTER TABLE jobs DROP CONSTRAINT jobs_kind_check; */"""
        self.assertEqual(found(sql), {})

    def test_the_sets_are_apart_and_run_in_version_order(self):
        root = tree(manager={"20261120000000_b.sql": "ALTER TABLE jobs ADD status text;",
                             "0002_a.sql": "CREATE TABLE jobs (id text);"},
                    hub={"0001_init.sql": "CREATE TABLE jobs (id text, role text);"})
        self.assertEqual(check.scan(root), {"hub": {"jobs.role": "enum"}, "manager": {"jobs.status": "enum"}})

    def test_an_alter_of_an_unknown_table_is_an_error(self):
        with self.assertRaises(check.MigrationError):
            found("ALTER TABLE nowhere ADD COLUMN kind text;")
        self.assertEqual(found("ALTER TABLE IF EXISTS nowhere ADD COLUMN kind text;"), {})


class RatchetTests(unittest.TestCase):
    def run_main(self, root, *args):
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = check.main(["--root", str(root), *args])
        return code, out.getvalue(), err.getvalue()

    MIGRATION = {"0001_init.sql": "CREATE TABLE jobs (id text, status text, body jsonb);"}

    def test_a_new_name_fails_with_how_to_fix_it(self):
        code, _, err = self.run_main(tree(manager=self.MIGRATION, baseline={"hub": [], "manager": ["jobs.body"]}))
        self.assertEqual(code, 1)
        self.assertIn("manager jobs.status: an enum-like text column without a CHECK; add a CHECK (status IN (...))", err)
        self.assertNotIn("jobs.body", err)
        code, _, err = self.run_main(tree(manager=self.MIGRATION, baseline={"manager": ["jobs.status"]}))
        self.assertEqual(code, 1)
        self.assertIn("manager jobs.body: a JSON column without a shape CHECK or an opaque note", err)
        self.assertIn("-- opaque: <reason>", err)

    def test_a_name_in_another_set_does_not_cover_it(self):
        code, _, err = self.run_main(tree(manager=self.MIGRATION, baseline={"hub": ["jobs.status", "jobs.body"]}))
        self.assertEqual(code, 1)
        self.assertIn("manager jobs.status", err)

    def test_the_baseline_passes_and_a_fixed_name_passes_with_a_notice(self):
        baseline = {"hub": [], "manager": ["jobs.body", "jobs.status", "old.kind"]}
        code, out, _ = self.run_main(tree(manager=self.MIGRATION, baseline=baseline))
        self.assertEqual(code, 0)
        self.assertIn("1 file(s) under the baseline", out)
        self.assertIn("manager old.kind: no longer found", out)
        self.assertIn("3 column(s) in the baseline, none new", out)

    def test_update_only_lowers(self):
        root = tree(manager=self.MIGRATION, baseline={"hub": [], "manager": ["jobs.body", "jobs.status", "old.kind"]})
        code, out, _ = self.run_main(root, "--update")
        self.assertEqual(code, 0)
        self.assertIn("lowered the baseline to 2 column(s)", out)
        self.assertEqual(json.loads((root / check.BASELINE_REL).read_text()), {"hub": [], "manager": ["jobs.body", "jobs.status"]})
        self.assertIn("already matches", self.run_main(root, "--update")[1])
        root = tree(manager=self.MIGRATION, baseline={"manager": ["jobs.body"]})
        code, _, err = self.run_main(root, "--update")
        self.assertEqual(code, 1)
        self.assertIn("refusing to add manager jobs.status", err)
        self.assertEqual(json.loads((root / check.BASELINE_REL).read_text()), {"manager": ["jobs.body"]})

    def test_no_baseline_fails_and_update_will_not_create_one(self):
        root = tree(manager=self.MIGRATION)
        self.assertEqual(self.run_main(root)[0], 1)
        self.assertEqual(self.run_main(root, "--update")[0], 2)
        self.assertFalse((root / check.BASELINE_REL).exists())

    def test_a_broken_baseline_is_an_error(self):
        for bad in ({"other": []}, {"manager": "jobs.status"}, {"manager": ["status"]}):
            self.assertEqual(self.run_main(tree(manager=self.MIGRATION, baseline=bad))[0], 2, bad)

    def test_there_is_no_ignore_marker(self):
        sql = "CREATE TABLE jobs (id text, status text); -- migration-enum-ok: legacy\n"
        self.assertEqual(found(sql), {"jobs.status": "enum"})


class CheckoutTests(unittest.TestCase):
    def test_this_checkout_has_no_name_above_its_baseline(self):
        actual = check.scan(ROOT)
        baseline = check.parse_baseline((ROOT / check.BASELINE_REL).read_text(encoding="utf-8"), check.BASELINE_REL)
        self.assertEqual(check.violations(actual, baseline), [])


if __name__ == "__main__":
    unittest.main()
