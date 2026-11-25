#!/usr/bin/env python3
"""The Dashboards mock (scripts/mock/dashboards_mock.py, #15017): the switch, the -pages places, the index and a page,
taps answered "Received", the server actions and the events they push, as the phase 1 contract says.

    python3 scripts/mock/test_dashboards_mock.py

The first classes drive the domain on a made-up hub (its events seen directly); the last starts the mock with
MOCK_EXTENSIONS=1 on a free port the kernel picks and checks the places come after the extensions' own.
"""
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch
from urllib.error import URLError
from urllib.request import ProxyHandler, Request, build_opener

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import mock_domains  # noqa: E402

RUN, READING, TRIP, GROCERY = "pg_3f9a1c0d5e7b2a41", "pg_8c2e4b6d1a0f9e37", "pg_5d7f1e3a9c4b2086", "pg_b1a4c7e2d9f03658"
PAGE = "/api/v1/extensions/-pages/-/ui/"


class Handler:
    """What a route is given: the request's path, headers and method, and what it sent."""

    def __init__(self, method, path, lang="en", role=None):
        self.command, self.path, self.sent = method, path, []
        self.headers = {"Accept-Language": lang, **({"X-Mock-Role": role} if role else {})}

    def send_json(self, obj, status=200, *args, **kwargs):
        self.sent.append((status, obj))


class Domain(unittest.TestCase):
    mode = "1"

    def setUp(self):
        self.events = []
        self.namespace = {
            "PLUGINS_MODE": "",
            "NAVIGATION": {},
            "broadcast": self.events.append,
            "is_read_only": lambda h: h.headers.get("X-Mock-Role") in ("readonly", "answer", "watch"),
            "assistant_state": lambda space="", archived=False: {"manager": "assistant", "conversations": []},
            "assistant_for_manager": lambda h: None if not h.headers.get("X-Persona-Manager") else ({"manager": "m-work", "dashboards": True}, 200),
        }
        with patch.dict(os.environ, {"MOCK_DASHBOARDS": self.mode}):
            self.registry = mock_domains.load(self.namespace, ["dashboards_mock"])

    def call(self, method, path, body=None, lang="en", role=None, headers=None):
        h = Handler(method, path, lang, role)
        h.headers.update(headers or {})
        answered = self.registry.http(h, method, path.partition("?")[0], b"" if body is None else json.dumps(body).encode())
        self.assertTrue(answered, "%s %s was handed on" % (method, path))
        self.assertEqual(len(h.sent), 1)
        return h.sent[0]

    def ok(self, method, path, body=None, lang="en", **kw):
        status, obj = self.call(method, path, body, lang, **kw)
        self.assertEqual(status, 200, obj)
        return obj

    def places(self, lang="en", query=""):
        return [(u["extension"], u["id"], u["title"]) for u in self.ok("GET", "/api/v1/extensions/-/ui" + query, lang=lang)["ui"]]

    def view(self, ui, body=None, lang="en"):
        return self.ok("POST", PAGE + ui, body or {}, lang)

    def types(self):
        return [e["type"] for e in self.events]


class SwitchOnTests(Domain):
    def test_features_and_the_setting(self):
        features = self.registry.features(["hello"])
        self.assertIn("assistant_pages", features)
        self.assertIn("extension_ui_slots", features)  # without MOCK_EXTENSIONS the hub still has UI slots
        self.assertIs(self.namespace["assistant_state"]()["dashboards"], True)
        self.assertNotIn("dashboards", self.namespace["assistant_state"]("health"))
        other = self.namespace["assistant_for_manager"](Handler("GET", "/api/v1/assistant"))
        self.assertIsNone(other)

    def test_places_are_the_index_then_the_pinned_pages(self):
        self.assertEqual(self.places(), [("-pages", "index", "Dashboards"), ("-pages", RUN, "Morning run")])
        self.assertEqual(self.places("zh-Hans"), [("-pages", "index", "仪表盘"), ("-pages", RUN, "晨跑打卡")])
        self.assertEqual(self.places("ja")[0][2], "ダッシュボード")
        self.assertEqual(self.places(query="?placement=nav_page"), self.places())
        self.assertEqual(self.places(query="?placement=overview_card"), [])
        listed = self.ok("GET", "/api/v1/extensions/-/ui")["ui"]
        self.assertTrue(all(u["placement"] == "nav_page" and u["extension_name"] == "Persona" and "badge" not in u for u in listed))
        self.assertEqual(listed[0]["icon"], "chart")
        old = self.ok("GET", "/api/v1/plugins/-/ui")["ui"][0]
        self.assertEqual((old["plugin"], old["plugin_name"]), ("-pages", "Persona"))

    def test_the_index_lists_pinned_all_and_recently_removed(self):
        view = self.view("index")["view"]
        self.assertEqual(view["title"], "Dashboards")
        sections = {s["key"]: s for s in view["sections"]}
        self.assertEqual([s["title"] for s in view["sections"]], ["Pinned", "All", "Recently removed"])
        self.assertEqual([r["title"] for r in sections["pinned"]["items"]], ["Morning run"])
        self.assertEqual(sections["pinned"]["items"][0]["subtitle"], "Updated today 8:02")
        self.assertEqual(sections["pinned"]["items"][0]["action"]["push"], {"ui": RUN})
        self.assertEqual([r["title"] for r in sections["all"]["items"]], ["Reading list", "Weekend trip"])
        removed = sections["removed"]["items"][0]
        self.assertEqual(removed["title"], "Grocery list")
        self.assertEqual(removed["actions"][0]["action"]["id"], "persona.restore:" + GROCERY)

    def test_a_page_is_titled_by_itself_and_has_the_footer(self):
        view = self.view(RUN)["view"]
        self.assertEqual(view["title"], "Morning run")
        items = [i for s in view["sections"] for i in s["items"]]
        self.assertEqual(next(i for i in items if i["type"] == "metric")["value"], "12 days")
        chart = next(i for i in items if i["type"] == "chart")
        self.assertEqual((chart["kind"], len(chart["series"][0]["points"])), ("bar", 30))
        self.assertEqual([i["title"] for i in items if i["type"] == "button"][:2], ["Done today", "Skip today"])
        footer = view["sections"][-1]
        self.assertEqual(footer["key"], "persona.footer")
        self.assertEqual(footer["items"][0]["text"], "Updated today 8:02")
        self.assertEqual([i["action"]["id"] for i in footer["items"] if i["type"] == "button"],
                         ["persona.unpin", "persona.rename", "persona.remove"])
        self.assertEqual(self.view(READING, lang="ja")["view"]["title"], "読書リスト")

    def test_a_tap_is_received_then_moves_on(self):
        out = self.view(RUN, {"action": "done_today", "values": {}, "context": {"ui_version": 10}})
        self.assertEqual(out["toast"], "Received")
        footer = [i.get("text") for i in out["view"]["sections"][-1]["items"] if i["type"] == "text"]
        self.assertEqual(footer, ["Updated today 8:02", "Received"])
        state = self.ok("GET", "/mock/dashboards")
        self.assertEqual(state["taps"][0]["tag"], '<persona-action view="-pages/%s" action="done_today">' % RUN)
        self.assertEqual(self.view(RUN, lang="zh-Hans")["view"]["sections"][-1]["items"][1]["text"], "已收到")
        steps = []
        for _ in range(3):
            self.events.clear()
            steps.append(self.ok("GET", "/mock/dashboards?advance=1")["pages"][0]["status"])
            self.assertEqual(self.events, [{"type": "extension_ui", "extension": "-pages", "id": RUN, "invalidate": True}])
        self.assertEqual(steps, ["delivered", "working", None])
        view = self.view(RUN)["view"]
        self.assertEqual(view["sections"][0]["items"][0]["value"], "13 days")
        self.assertEqual(len([i for i in view["sections"][-1]["items"] if i["type"] == "text"]), 1)

    def test_pin_adds_a_place_and_its_sidebar_key(self):
        out = self.view("index", {"action": "persona.pin", "values": {"page": READING}})
        self.assertEqual(out["toast"], "Pinned to the sidebar")
        self.assertEqual([r["title"] for r in out["view"]["sections"][0]["items"]], ["Morning run", "Reading list"])
        self.assertIn(("-pages", READING, "Reading list"), self.places())
        self.assertIn("p:-pages/" + READING, self.namespace["NAVIGATION"]["pages"]["shown"])
        self.assertEqual(self.types(), ["navigation", "assistant_pages", "extension_ui", "extension_ui"])
        self.assertEqual(self.events[-1], {"type": "extension_ui", "extension": "-pages", "id": READING, "invalidate": True})
        self.view("index", {"action": "persona.pin", "values": {"page": TRIP}})
        self.assertEqual(len(self.places()), 4)
        self.view("index", {"action": "persona.unpin", "values": {"page": RUN}})
        self.assertEqual([p[1] for p in self.places()], ["index", READING, TRIP])

    def test_rename_remove_restore_delete(self):
        rename = self.view(RUN, {"action": "", "state": "rename"})["view"]
        self.assertEqual(rename["sections"][0]["items"][0]["value"], "Morning run")
        self.assertEqual(self.view(RUN, {"action": "persona.rename", "values": {"title": "Run club"}, "state": "rename"}),
                         {"pop": True, "toast": "Renamed"})
        self.assertEqual(self.places()[1][2], "Run club")
        self.assertIn("toast", self.view(RUN, {"action": "persona.rename", "values": {"title": "x" * 41}}))
        self.events.clear()
        removed = self.view(RUN, {"action": "persona.remove"})
        self.assertEqual(removed["view"]["sections"][0]["items"][0]["type"], "empty")
        self.assertEqual(self.types()[0], "assistant_pages")
        self.assertEqual(self.places(), [("-pages", "index", "Dashboards")])
        index = self.view("index")["view"]
        self.assertEqual([r["title"] for r in index["sections"][-1]["items"]], ["Run club", "Grocery list"])
        self.view("index", {"action": "persona.restore:" + RUN})
        self.assertEqual(self.view(RUN)["view"]["title"], "Run club")
        self.view("index", {"action": "persona.delete:" + GROCERY})
        self.assertEqual(self.call("POST", PAGE + GROCERY, {})[0], 404)

    def test_refusals(self):
        self.assertEqual(self.call("POST", PAGE + "index", {}, role="readonly")[0], 403)
        self.assertEqual(self.call("POST", PAGE + "pg_0000000000000000", {})[0], 404)
        self.assertEqual(self.call("POST", PAGE + "nope", {})[0], 404)
        self.assertEqual(self.call("PUT", "/api/v1/assistant", {"dashboards": "yes"})[0], 400)
        self.assertEqual(self.call("PUT", "/api/v1/assistant", {"dashboards": False}, role="readonly")[0], 403)
        self.assertEqual(self.places()[0][1], "index")

    def test_the_switch(self):
        self.events.clear()
        self.assertIs(self.ok("PUT", "/api/v1/assistant", {"dashboards": False})["dashboards"], False)
        self.assertEqual(self.types(), ["assistant_pages"])
        self.assertEqual(self.places(), [])
        self.assertEqual(self.call("POST", PAGE + RUN, {})[0], 404)
        self.ok("PUT", "/api/v1/assistant", {"dashboards": True})
        self.assertEqual(len(self.places()), 2)
        # another field goes on to the hub's own PUT; another manager's settings have no switch
        h = Handler("PUT", "/api/v1/assistant")
        self.assertFalse(self.registry.http(h, "PUT", "/api/v1/assistant", json.dumps({"dashboards": False, "notes": "x"}).encode()))
        self.assertEqual(self.places(), [])
        h = Handler("PUT", "/api/v1/assistant")
        h.headers["X-Persona-Manager"] = "m-work"
        self.assertFalse(self.registry.http(h, "PUT", "/api/v1/assistant", b'{"dashboards": true}'))
        self.assertEqual(self.places(), [])
        self.assertNotIn("dashboards", self.namespace["assistant_for_manager"](h)[0])

    def test_reset_and_modes_at_run_time(self):
        self.view("index", {"action": "persona.pin", "values": {"page": READING}})
        self.ok("GET", "/mock/dashboards?mode=empty")
        self.assertEqual(self.places(), [])
        self.namespace["NAVIGATION"].clear()
        self.ok("GET", "/mock/dashboards?mode=pinned")
        self.assertEqual(self.namespace["NAVIGATION"]["pages"]["shown"],
                         ["p:-pages/index", "p:-pages/" + RUN, "p:-pages/" + READING, "p:-pages/" + TRIP])
        self.ok("GET", "/mock/dashboards?mode=removed")
        self.assertEqual(self.places(), [("-pages", "index", "Dashboards")])
        self.registry.reset()
        self.assertEqual(len(self.places()), 2)


class SwitchOffTests(Domain):
    mode = "off"

    def test_nothing_is_listed_and_pages_are_not_found(self):
        self.assertIn("assistant_pages", self.registry.features([]))
        self.assertIs(self.namespace["assistant_state"]()["dashboards"], False)
        self.assertEqual(self.places(), [])
        self.assertEqual(self.call("POST", PAGE + "index", {})[0], 404)


class EmptyTests(Domain):
    mode = "empty"

    def test_on_with_nothing_kept_lists_nothing(self):
        self.assertIs(self.namespace["assistant_state"]()["dashboards"], True)
        self.assertEqual(self.places(), [])
        self.assertEqual(self.namespace["NAVIGATION"], {})
        view = self.view("index")["view"]
        self.assertEqual(view["sections"][0]["items"][0]["type"], "empty")


class RemovedOnlyTests(Domain):
    mode = "removed"

    def test_the_index_alone(self):
        self.assertEqual(self.places(), [("-pages", "index", "Dashboards")])
        keys = [s["key"] for s in self.view("index")["view"]["sections"]]
        self.assertEqual(keys, ["empty", "removed"])


class PinnedTests(Domain):
    mode = "pinned"

    def test_three_pinned_and_no_fourth(self):
        self.assertEqual([p[1] for p in self.places()], ["index", RUN, READING, TRIP])
        self.view("index", {"action": "persona.unpin", "values": {"page": TRIP}})
        self.view("index", {"action": "persona.restore:" + GROCERY})
        self.view("index", {"action": "persona.pin", "values": {"page": TRIP}})
        out = self.view("index", {"action": "persona.pin", "values": {"page": GROCERY}})
        self.assertEqual(out["toast"], "You can pin up to 3 pages")
        self.assertEqual(len(self.places()), 4)


class OffTests(Domain):
    mode = ""

    def test_without_the_switch_the_hub_answers_as_before(self):
        self.assertEqual(self.registry.features(["a"]), ["a"])
        h = Handler("GET", "/api/v1/extensions/-/ui")
        self.assertFalse(self.registry.http(h, "GET", "/api/v1/extensions/-/ui"))
        self.assertTrue(any(line.startswith("MOCK_DASHBOARDS=") for line in self.registry.help_lines))


class RunningMockTests(unittest.TestCase):
    """The real mock: the places come after the extensions' own (MOCK_EXTENSIONS=1 with three nav pages)."""

    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        cls.base = "http://127.0.0.1:%d" % port
        cls.open = build_opener(ProxyHandler({})).open
        cls.log = tempfile.TemporaryFile()
        env = dict(os.environ, PORT=str(port), MOCK_DASHBOARDS="1", MOCK_EXTENSIONS="1", MOCK_EXTENSION_COLUMNS="1",
                   MOCK_EXTENSION_GAUGE="1")
        cls.process = subprocess.Popen([sys.executable, "-I", str(HERE / "mock_hub.py")], env=env, stdout=cls.log, stderr=cls.log)
        cls.addClassCleanup(cls.stop)
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            if cls.process.poll() is not None:
                raise RuntimeError("mock hub exited before startup")
            try:
                with cls.open(cls.base + "/api/v1/health", timeout=1):
                    return
            except (URLError, OSError):
                time.sleep(0.1)
        raise RuntimeError("mock hub startup timed out")

    @classmethod
    def stop(cls):
        cls.process.terminate()
        try:
            cls.process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            cls.process.kill()
            cls.process.wait(timeout=5)
        cls.log.close()

    def get(self, path, method="GET", body=None):
        req = Request(self.base + path, method=method, data=None if body is None else json.dumps(body).encode(),
                      headers={"Accept-Language": "en", "Content-Type": "application/json"})
        with self.open(req, timeout=5) as response:
            return json.load(response)

    def test_after_the_extensions_places(self):
        features = self.get("/api/v1/health")["features"]
        self.assertIn("assistant_pages", features)
        ui = [(u["extension"], u["id"]) for u in self.get("/api/v1/extensions/-/ui?placement=nav_page")["ui"]]
        self.assertEqual(ui[-2:], [("-pages", "index"), ("-pages", RUN)])
        self.assertGreaterEqual(len([u for u in ui if u[0] != "-pages"]), 3)
        self.assertNotIn("-pages", [u["extension"] for u in self.get("/api/v1/extensions/-/ui?placement=overview_card")["ui"]])
        self.assertIs(self.get("/api/v1/assistant")["dashboards"], True)
        self.assertIn("p:-pages/index", self.get("/api/v1/navigation")["pages"]["shown"])
        tap = self.get(PAGE + RUN, "POST", {"action": "done_today", "context": {"ui_version": 10}})
        self.assertEqual(tap["toast"], "Received")
        self.assertIs(self.get("/api/v1/assistant", "PUT", {"dashboards": False})["dashboards"], False)
        self.assertNotIn("-pages", [u["extension"] for u in self.get("/api/v1/extensions/-/ui")["ui"]])


if __name__ == "__main__":
    unittest.main()
