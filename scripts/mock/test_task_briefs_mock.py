"""Tests mock_hub's task briefs (#3822, MOCK_TASKS=1 MOCK_BRIEFS=1): the open plan's brief, a brief taken over twice
with a scope change and an overdue mark, a reconstructed one, and a result on a finished task, its review and receipt."""
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import unittest
from urllib.error import URLError
from urllib.request import Request, urlopen


class MockHub:
    def __init__(self, **env):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        self.base = f"http://127.0.0.1:{port}"
        self.log = tempfile.TemporaryFile()
        self.process = subprocess.Popen(
            ["python3", "-I", str(Path(__file__).with_name("mock_hub.py"))],
            env=dict(os.environ, PORT=str(port), **env),
            stdout=self.log,
            stderr=self.log,
        )
        for _ in range(60):
            if self.process.poll() is not None:
                raise RuntimeError("the mock hub exited")
            try:
                with urlopen(self.base + "/api/v1/health", timeout=1):
                    return
            except (URLError, TimeoutError):
                time.sleep(0.1)
        raise RuntimeError("the mock hub did not start")

    def stop(self):
        self.process.terminate()
        try:
            self.process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            self.process.kill()
            self.process.wait(timeout=5)
        self.log.close()

    def request(self, path):
        headers = {"Content-Type": "application/json", "Authorization": "Bearer mock"}
        with urlopen(Request(self.base + path, headers=headers), timeout=3) as response:
            return json.load(response)


class TaskBriefsMockTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.hub = MockHub(MOCK_TASKS="1", MOCK_BRIEFS="1")
        cls.addClassCleanup(cls.hub.stop)
        cls.todos = {t["id"]: t for t in cls.hub.request("/api/v1/todos")["todos"]}
        main = cls.hub.request("/api/v1/assistant")["main"]
        cls.feed = cls.hub.request(f"/api/v1/assistant/conversations/{main}/feed")

    def test_the_open_plan_carries_a_real_brief(self):
        plan = next(p for p in self.feed["plans"].values() if p["state"] == "open")
        brief = plan["tasks"][0]["brief"]
        self.assertEqual(brief["started_by"], "owner")
        self.assertIn("PR #42", brief["request"]["text"])
        self.assertTrue(brief["done_when"] and brief["expect_minutes"] and brief["out_of_bounds"])

    def test_a_brief_taken_over_twice_with_a_scope_change_and_an_overdue_mark(self):
        todo = self.todos["todo_task_widget"]
        brief = todo["brief"]
        self.assertFalse(brief.get("reconstructed"))
        self.assertTrue(brief["request"]["text"])
        self.assertEqual([e["kind"] for e in brief["log"]], ["scope", "overdue"])
        self.assertTrue(brief["overdue_at"])
        self.assertEqual(brief["criteria"], todo["supervision"]["done_when"])
        self.assertEqual(len(todo["lineage"]), 3)
        self.assertEqual([bool(s.get("why")) for s in todo["lineage"]], [True, True, False])
        self.assertTrue(todo["lineage"][2]["current"])

    def test_a_reconstructed_brief_has_no_request(self):
        brief = self.todos["todo_task_blog"]["brief"]
        self.assertTrue(brief["reconstructed"])
        self.assertNotIn("request", brief)
        self.assertTrue(brief["handover"])

    def test_a_finished_task_has_each_criterion_s_result(self):
        todo = self.todos["todo_task_release"]
        result = todo["brief"]["result"]
        self.assertTrue(todo["done_at"])
        self.assertEqual([c["state"] for c in result["criteria"]], ["met", "unchecked", "dropped"])
        self.assertTrue(result["criteria"][2]["change"] and result["also_done"])
        items = self.feed["items"]
        review = next(i for i in items if i["kind"] == "review" and i["ref"] == todo["id"])
        self.assertEqual(review["body"]["result"], result)
        receipt = next(i for i in items if i["kind"] == "receipt" and i["ref"] == todo["id"])
        self.assertEqual([c["state"] for c in receipt["body"]["criteria"]], ["unchecked", "unchecked"])


if __name__ == "__main__":
    unittest.main()
