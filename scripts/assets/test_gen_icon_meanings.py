#!/usr/bin/env python3
"""Tests for gen_icon_meanings.py and spec/icons/meanings.json (#4209)."""
import json
import re
import tempfile
import unittest
from pathlib import Path

import gen_icon_meanings as gen
import gen_phosphor_icons as phosphor


def ios_meanings():
    """Theme.Icon's meanings and their SF Symbols, aliases (`static let a = b`) resolved."""
    swift = (gen.ROOT / "apple/Shared/Design/Icons.swift").read_text()
    literal = dict(re.findall(r'static let (\w+) = "([^"]+)"', swift))
    for name, target in re.findall(r'static let (\w+) = (\w+)\s*(?://.*)?$', swift, re.M):
        if target in literal:
            literal.setdefault(name, literal[target])
    return literal


class RepositoryTests(unittest.TestCase):
    def setUp(self):
        self.meanings = gen.load()

    def test_every_meaning_is_an_ios_meaning_with_its_symbol(self):
        ios = ios_meanings()
        self.assertGreater(len(ios), 300)
        wrong = {name: (entry["sf"], ios.get(name)) for name, entry in self.meanings.items() if ios.get(name) != entry["sf"]}
        self.assertEqual({}, wrong, "spec/icons/meanings.json differs from Theme.Icon (meaning: (json, Icons.swift))")

    def test_every_glyph_is_generated_into_phicons(self):
        missing = {name: entry["phosphor"] for name, entry in self.meanings.items() if entry["phosphor"] not in phosphor.NAMES}
        self.assertEqual({}, missing, "a glyph PhIcons doesn't have; add it to gen_phosphor_icons.py NAMES and regenerate")

    def test_committed_kotlin_is_current(self):
        self.assertEqual(gen.KOTLIN.read_text(encoding="utf-8"), gen.render(self.meanings),
                         "PersonaIcons.kt is stale: run python3 scripts/assets/gen_icon_meanings.py")
        self.assertEqual(0, gen.main(["--check"]))


class GeneratorTests(unittest.TestCase):
    def test_names_and_keywords(self):
        self.assertEqual("ArrowSquareOut", gen.pascal("arrow-square-out"))
        text = gen.render({"in": {"sf": "tray", "phosphor": "tray"}, "copy": {"sf": "doc.on.doc", "phosphor": "copy"}})
        self.assertIn("    val `in`: PhIcon get() = PhIcons.Tray", text)
        self.assertLess(text.index("val copy"), text.index("val `in`"))  # sorted by meaning

    def test_an_entry_has_exactly_its_symbol_and_glyph(self):
        with tempfile.TemporaryDirectory() as tmp:
            source = Path(tmp) / "meanings.json"
            source.write_text(json.dumps({"meanings": {"copy": {"sf": "doc.on.doc"}}}))
            with self.assertRaises(ValueError):
                gen.load(source)


if __name__ == "__main__":
    unittest.main()
