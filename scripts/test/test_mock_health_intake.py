"""The screenshot hub's intake routes mirror the client contract, including idempotent writes and tombstones."""
import json
from pathlib import Path
import sys
import unittest
import uuid
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "mock"))
import mock_hub as hub
import health_intake_mock as intake


class Handler:
    def __init__(self, headers=None):
        self.headers = headers or {}
        self.result = None
        self.status = None

    def send_json(self, result, status=200):
        self.result, self.status = result, status


class HealthIntakeMockTests(unittest.TestCase):
    path = "/api/v1/assistant/health/intake"

    def setUp(self):
        intake.HEALTH_INTAKE_ENABLED = True
        intake.HEALTH_INTAKE_ENTRIES.clear()
        intake.HEALTH_INTAKE_ENTRIES.update(intake.health_intake_seed())
        self.events = []
        self.broadcast = patch.object(hub, "broadcast", self.events.append)
        self.broadcast.start()
        self.addCleanup(self.broadcast.stop)

    def request(self, method="GET", suffix="", body=None, query=None, headers=None):
        handler = Handler(headers)
        self.assertTrue(intake.health_intake_route(handler, method, self.path + suffix,
                                              {"space": ["health"]} if query is None else query,
                                              json.dumps(body).encode() if body is not None else b""))
        return handler

    def test_snapshot_has_goals_records_and_zero_filled_days(self):
        response = self.request(query={"space": ["health"], "days": ["30"]})
        self.assertEqual(response.status, 200)
        self.assertEqual(response.result["water"]["today"], 750)
        self.assertEqual(response.result["protein"]["today"], 36)
        self.assertEqual(response.result["water"]["goal"], 2000)
        self.assertEqual(len(response.result["days"]), 30)
        self.assertEqual(response.result["days"][0]["water"], 0)
        self.assertEqual(response.result["days"][-1]["water"], 750)

    def test_detail_pages_use_exported_gauges_settings_and_action_toasts(self):
        for kind, color in [("water", "blue"), ("protein", "amber")]:
            body = {"context": {"locale": "en", "ui_version": 11}, "state": json.dumps({"kind": kind})}
            initial = self.request("POST", "/ui", body).result
            self.assertEqual(initial["view"]["title"], kind.title())
            self.assertEqual(initial["view"]["sections"][0]["items"][0]["tiles"][0]["gauge"]["color"], color)
            body["state"] = initial["view"]["state"]
            body["action"] = "drink" if kind == "water" else "log"
            acted = self.request("POST", "/ui", body).result
            self.assertEqual(acted["view"], initial["view"])
            self.assertTrue(acted["toast"])
            body["action"] = "settings"
            settings = self.request("POST", "/ui", body).result["view"]
            self.assertTrue(json.loads(settings["state"])["edit"])
            self.assertEqual(settings["sections"][0]["items"][0]["name"], "goal_ml" if kind == "water" else "goal_g")
        legacy = self.request("POST", "/ui", {"context": {"locale": "zh-Hans", "ui_version": 4}}).result
        self.assertEqual(legacy["view"]["title"], "喝水")
        self.assertEqual(legacy["view"]["sections"][0]["items"][0]["type"], "row")

    def test_default_cup_retry_and_tombstone_are_idempotent(self):
        draft = {"id": str(uuid.uuid4()), "kind": "water"}
        first = self.request("POST", body=draft).result
        self.assertEqual(first["entry"]["amount"], 250)
        self.assertEqual(first["today"]["water"], 1000)
        retry = self.request("POST", body=dict(draft, amount=500)).result
        self.assertEqual(first, retry)
        deleted = self.request("DELETE", "/" + draft["id"]).result
        self.assertEqual(deleted["today"]["water"], 750)
        sequence = intake.HEALTH_INTAKE_ENTRIES[draft["id"]]["seq"]
        self.assertEqual(self.request("DELETE", "/" + draft["id"]).result, deleted)
        self.assertEqual(intake.HEALTH_INTAKE_ENTRIES[draft["id"]]["seq"], sequence)
        tombstone = self.request("POST", body=draft).result
        self.assertIn("deleted_at", tombstone["entry"])
        self.assertEqual(tombstone["today"]["water"], 750)
        self.assertTrue(self.events)
        self.assertTrue(all(event == {"type": "health_intake"} for event in self.events))

    def test_protein_and_water_accept_one_decimal(self):
        for kind, amount, total in [("protein", 12.3, 48.3), ("water", 125.5, 875.5)]:
            result = self.request("POST", body={"id": str(uuid.uuid4()), "kind": kind, "amount": amount})
            self.assertEqual(result.status, 200)
            self.assertEqual(result.result["today"][kind], total)

    def test_wrong_space_manager_and_old_hub_are_refused(self):
        self.assertEqual(self.request(query={}).status, 400)
        self.assertEqual(self.request(headers={"X-Persona-Manager": "another-manager"}).status, 400)
        intake.HEALTH_INTAKE_ENABLED = False
        self.assertNotIn("health_intake", hub.features())
        self.assertEqual(self.request().status, 403)

    def test_invalid_values_and_missing_delete_have_explicit_errors(self):
        for kind, amount in [("water", 0), ("water", 3000.1), ("water", True), ("protein", 200.1), ("protein", 0.01)]:
            result = self.request("POST", body={"id": str(uuid.uuid4()), "kind": kind, "amount": amount})
            self.assertEqual(result.status, 400)
            self.assertTrue(result.result["error"])
        self.assertEqual(self.request("DELETE", "/" + str(uuid.uuid4())).status, 404)


if __name__ == "__main__":
    unittest.main()
