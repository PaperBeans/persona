"""Positive and negative checks for exact compatibility registration, never directory exemptions."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

import compatibility_identifiers as guard
from test_external_service_refs import hits

ROOT = Path(__file__).resolve().parents[2]
FORMER = "jar" + "vis"


def entry(*values, path_values=(), refs=()):
    return {"reason": "Versioned compatibility fixture.", "tokens": sorted(values),
            "path_tokens": sorted(path_values), "external_refs": sorted(refs)}


class CompatibilityIdentifierTests(unittest.TestCase):
    maxDiff = None

    def test_exact_registered_token_passes_only_in_its_registered_file(self):
        path = "server/internal/compat/fixture.go"
        token = "X-" + FORMER.capitalize() + "-Client"
        registry = {path: entry(token)}
        self.assertEqual(guard.findings(path, 'header = "' + token + '"', registry), [])
        self.assertEqual(guard.findings("server/internal/other.go", token, registry), [(1, token)])

    def test_new_identifier_in_a_registered_adapter_fails(self):
        path = "scripts/fixture.py"
        old = FORMER.upper() + "_HOME"
        unexpected = FORMER.upper() + "_UNREGISTERED"
        registry = {path: entry(old)}
        self.assertEqual(guard.findings(path, old + "\n" + unexpected, registry), [(2, unexpected)])
        self.assertEqual(guard.findings(path, "prefix_" + old, registry), [(1, "prefix_" + old)])
        self.assertEqual(guard.findings(path, old.lower(), registry), [(1, old.lower())])

    def test_unregistered_source_document_fixture_and_generated_names_fail(self):
        name = FORMER + "-unregistered"
        for path in ("server/internal/new.go", "docs/new.md", "spec/fixtures/new.json", "sdk/generated/new.ts"):
            with self.subTest(path=path):
                self.assertEqual(guard.findings(path, name, {}), [(1, name)])

    def test_only_the_registered_filename_is_allowed(self):
        path = "scripts/" + FORMER + "_config.py"
        token = FORMER + "_config.py"
        registry = {path: entry(path_values=(token,))}
        self.assertEqual(guard.findings(path, "", registry), [])
        self.assertEqual(guard.findings("other/" + token, "", registry), [(0, token)])

    def test_short_legacy_scheme_is_also_registered_exactly(self):
        scheme = "j" + "://"
        registry = {"web/compat.ts": entry(scheme)}
        self.assertEqual(guard.findings("web/compat.ts", scheme, registry), [])
        self.assertEqual(guard.findings("web/new.ts", scheme, registry), [(1, scheme)])

    def test_own_previous_repository_needs_a_separate_exact_reference_registration(self):
        path = "sdk/compat.go"
        reference = "nwverse/" + FORMER
        registry = {path: entry(FORMER, refs=(reference,))}
        self.assertEqual(hits("github.com/" + reference + "/server", path, registry), [])
        self.assertEqual(len(hits(reference, "sdk/other.go", registry)), 1)
        self.assertEqual(len(hits("nwverse" + "/example-service", path, registry)), 1)
        self.assertEqual(len(hits(reference + "-private", path, registry)), 1)
        self.assertEqual(len(hits("<service>" + "/integrations/private", path, registry)), 1)
        self.assertEqual(hits(reference, guard.REGISTRY, registry), [])
        self.assertEqual(len(hits("nwverse" + "/example-service", guard.REGISTRY, registry)), 1)
        self.assertEqual(len(hits(reference, guard.REGISTRY, {})), 1)

    def test_registry_schema_rejects_wildcards_metadata_and_other_repository_permissions(self):
        examples = [
            {"scripts/*": entry(FORMER)},
            {"../outside.py": entry(FORMER)},
            {"/absolute.py": entry(FORMER)},
            {"scripts/fixture.py": dict(entry(FORMER), note=FORMER + "New")},
            {"scripts/fixture.py": dict(entry(FORMER), reason=FORMER + "New")},
            {"scripts/fixture.py": entry(FORMER, refs=("nwverse" + "/example-service",))},
            {"scripts/fixture.py": entry(FORMER + "*")},
        ]
        with tempfile.TemporaryDirectory() as directory:
            file = Path(directory) / "registry.json"
            for files in examples:
                with self.subTest(files=files):
                    file.write_text(json.dumps({"version": 1, "files": files}))
                    with self.assertRaises(ValueError):
                        guard.load_registry(file)
            file.write_text('{"version":1,"version":1,"files":{}}')
            with self.assertRaises(ValueError):
                guard.load_registry(file)

    def test_registry_itself_does_not_allow_arbitrary_legacy_metadata(self):
        with tempfile.TemporaryDirectory() as directory:
            file = Path(directory) / "registry.json"
            file.write_text(json.dumps({"version": 1, "files": {}, "note": FORMER}))
            with self.assertRaises(ValueError):
                guard.load_registry(file)

    def test_registered_compatibility_does_not_disable_privacy_checks(self):
        spec = importlib.util.spec_from_file_location("compat_privacy", ROOT / "scripts/privacy_check.py")
        privacy = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(privacy)
        word = "synthetic-private-marker"
        private_address = "10." + "20.30.40"
        vendor = "open" + "-" + "lark"
        for text in (word, private_address, vendor):
            with self.subTest(text=text):
                self.assertTrue(privacy.problems(text, [word]))
        registered = FORMER + '-' + vendor
        self.assertEqual(guard.findings('fixture.py', registered, {'fixture.py': entry(registered)}), [])
        self.assertTrue(privacy.problems(registered, []))
        self.assertEqual(privacy.SKIP_FILES, {"scripts/privacy_check.py"})

    def test_stale_registration_is_rejected_instead_of_growing_forever(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            file = root / "fixture.txt"
            file.write_text("canonical")
            registry = {"fixture.txt": entry(FORMER)}
            self.assertEqual(guard.repository_findings(root, registry, ["fixture.txt"]),
                             ["fixture.txt: stale registered token " + FORMER])
            self.assertEqual(guard.repository_findings(root, registry, []), ["fixture.txt: stale registry path"])

    def test_document_prefilter_keeps_exact_registered_and_unregistered_lines(self):
        token = FORMER.upper() + "_TOKEN"
        scheme = "j" + "://"
        path = "scripts/fixture.py"
        registry = {path: entry(token)}
        ordinary = "canonical only\n"
        self.assertEqual(guard.findings(path, ordinary, registry), [])
        self.assertEqual(guard.findings(path, ordinary + token + "\n", registry), [])
        self.assertEqual(guard.findings(path, ordinary + scheme + "\n", registry), [(2, scheme)])

    def test_opaque_payloads_do_not_hide_an_unregistered_identifier(self):
        token = FORMER.upper() + '_UNREGISTERED'
        self.assertEqual(guard.tokens('opaque' * 20000 + ' ' + token), {token})

    def test_registered_symlink_cannot_be_replaced_with_another_target(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            name = FORMER + '_config.py'
            file = root / name
            file.symlink_to('other.py')
            registry = {name: dict(entry(path_values=(name,)), symlink_target='persona_config.py')}
            self.assertEqual(guard.repository_findings(root, registry, [name]),
                             [name + ': compatibility symlink target differs from its exact registration'])
            file.unlink()
            file.write_text('canonical')
            self.assertEqual(guard.repository_findings(root, registry, [name]),
                             [name + ': compatibility entrypoint is no longer a symlink'])

    def test_repository_uses_only_exact_registered_compatibility_identifiers(self):
        registry = guard.load_registry(ROOT / guard.REGISTRY)
        found = guard.repository_findings(ROOT, registry)
        self.assertEqual(found, [], "register only a required exact token in its actual compatibility carrier")


if __name__ == "__main__":
    unittest.main()
