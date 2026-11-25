"""The fake hub's disk usage map (#14576, scripts/mock/usage_map_mock.py, docs/server/machine-usage-map.md)."""
import unittest

from test_machine_alerts_mock import call, start, stop

MID = "/api/v1/machines/studio"
CLOUD = "/api/v1/extensions/example-offload/-/offload/cloud"


class UsageMapMockTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.process, cls.log, cls.base = start({"MOCK_USAGE_MAP": "1"})
        cls.addClassCleanup(stop, cls.process, cls.log)

    def setUp(self):
        call(self.base, "/mock/usage_map?reset=1")

    def test_overview_scans_first_then_is_ready_and_adds_up(self):
        status, first = call(self.base, MID + "/storage/usage")
        self.assertEqual(status, 200)
        self.assertEqual(first["state"], "scanning")
        _, ready = call(self.base, MID + "/storage/usage")
        self.assertEqual(ready["state"], "ready")
        kids = ready["node"]["children"]
        self.assertEqual(sum(k["bytes"] for k in kids), ready["volume"]["total_bytes"])
        self.assertIn("free", [k.get("key") for k in kids])

    def test_two_folder_levels(self):
        _, projects = call(self.base, MID + "/storage/usage?path=~/Projects")
        site = next(c for c in projects["node"]["children"] if c["name"] == "site")
        self.assertTrue(site["children"])
        _, deeper = call(self.base, MID + "/storage/usage?path=/Users/me/Projects/site")
        self.assertEqual({c["name"] for c in deeper["node"]["children"]}, {"node_modules", "build"})

    def test_trash_needs_confirm_then_undo_and_purge(self):
        path = "/Users/me/Downloads/archive.zip"
        status, _ = call(self.base, MID + "/storage/trash", "POST", {"paths": [path]})
        self.assertEqual(status, 400)
        _, done = call(self.base, MID + "/storage/trash", "POST", {"paths": [path], "confirm": True})
        tid = done["items"][0]["trash_id"]
        _, back = call(self.base, MID + "/storage/trash/undo", "POST", {"trash_ids": [tid]})
        self.assertEqual(back["restored"], [{"path": path}])
        _, again = call(self.base, MID + "/storage/trash", "POST", {"paths": [path], "confirm": True})
        _, purged = call(self.base, MID + "/storage/trash/purge", "POST", {"trash_ids": [again["items"][0]["trash_id"]], "confirm": True})
        self.assertGreater(purged["freed_bytes"], 0)
        self.assertGreater(purged["available_after"], purged["available_before"])

    def test_refused_items_say_why(self):
        _, res = call(self.base, MID + "/storage/trash", "POST", {"paths": ["/Users/me/Pictures"], "confirm": True})
        self.assertEqual(res["items"][0]["status"], "refused")
        self.assertEqual(res["items"][0]["refusal"], "standard_folder")

    def test_offload_asks_to_install_first_then_runs_a_job(self):
        _, providers = call(self.base, "/api/v1/extensions/-/offload")
        self.assertEqual([p["name"] for p in providers["providers"]], ["Example Cloud"])
        body = {"action": "offload", "paths": ["/Users/me/Movies/Trips 2019"], "confirm": True}
        status, refused = call(self.base, CLOUD + "/run", "POST", body)
        self.assertEqual(status, 409)
        self.assertEqual(refused["code"], "tool_install_required")
        status, started = call(self.base, CLOUD + "/run", "POST", dict(body, install=True))
        self.assertEqual(status, 202)
        _, job = call(self.base, CLOUD + "/jobs/" + started["job"])
        self.assertEqual(job["state"], "running")
        self.assertEqual(job["phase"], "install")
        _, inventory = call(self.base, CLOUD + "/inventory")
        self.assertTrue(inventory["tool"]["installed"])


if __name__ == "__main__":
    unittest.main()
