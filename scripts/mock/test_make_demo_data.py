"""Bundled demo data must include useful task samples and their reachable Agent conversations."""
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
from persona_identity import LEGACY_PREFIX


class DemoDataTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with tempfile.TemporaryDirectory(prefix="persona-demo-test-") as home:
            out = Path(home) / "Demo.json"
            env = {k: v for k, v in os.environ.items()
                   if not k.startswith(("PERSONA_", LEGACY_PREFIX, "CLAUDE_", "CODEX_", "ANTHROPIC_", "PG", "XDG_", "MOCK_"))
                   and not re.search(r"token|secret|password|api.?key", k, re.I)
                   and k not in ("BASH_ENV", "ENV", "ZDOTDIR")}
            env.update(HOME=home, PERSONA_DEMO_OUTPUT=str(out))
            result = subprocess.run([sys.executable, "-I", str(ROOT / "scripts/mock/make_demo_data.py")],
                                    cwd=home, env=env, capture_output=True, timeout=60)
            if result.returncode:
                raise RuntimeError("demo fixture generation failed; captured output is not printed")
            cls.data = json.loads(out.read_text())

    def test_health_selection_matches_the_bundled_demo_without_strength_pages(self):
        bundled = json.loads((ROOT / "apple/Persona/Resources/Demo.json").read_text())
        self.assertEqual(self.data["health_items"], bundled["health_items"])
        self.assertEqual(self.data["health_intake"]["items"], bundled["health_intake"]["items"])
        self.assertEqual(self.data["health_intake"]["items"], self.data["health_items"]["items"])

    def test_todo_samples_and_scheduled_routines_are_both_present(self):
        self.assertGreater(len(self.data["todos"]), 0)
        self.assertGreater(len(self.data["tasks"]), 0)
        self.assertTrue(any(todo.get("agents") for todo in self.data["todos"]))
        self.assertTrue(any(todo.get("done_at") for todo in self.data["todos"]))
        self.assertTrue(any(not todo.get("done_at") for todo in self.data["todos"]))

    def test_linked_agents_have_bundled_conversations(self):
        sessions = {(s["machine_id"], s["session_id"]) for s in self.data["sessions"]}
        items = self.data["items"]
        for todo in self.data["todos"]:
            for agent in todo.get("agents", []):
                key = (agent["machine_id"], agent["session_id"])
                self.assertIn(key, sessions)
                self.assertTrue(items.get(agent["session_id"]))

    def test_task_timestamps_are_relative_and_knowledge_entries_are_searchable(self):
        for todo in self.data["todos"]:
            for key in ("created_at", "updated_at", "done_at"):
                if todo.get(key):
                    self.assertTrue(todo[key].startswith("@rel:"))
        entries = [entry for rows in self.data["kb"].values() for entry in rows]
        self.assertGreater(len(entries), 0)
        self.assertTrue(all(entry.get("name") and entry.get("one_line") for entry in entries))


if __name__ == "__main__":
    unittest.main()
