#!/usr/bin/env python3
"""The Dashboards mock (scripts/mock/dashboards_mock.py, #15017, phase 2 #13020): the switch, the -pages places
(including ?pages_overview=1 Overview cards), the index (with watch glance rows) and pages, the 15 fixture `draws` in
spec/fixtures/dashboards/watch.json, taps answered "Received", the phase 1 and phase 2 footer actions, platform-gated
Show on Overview, localization (en, zh-Hans, ja) and the events they push.

    python3 scripts/mock/test_dashboards_mock.py

The first classes drive the domain on a made-up hub (its events seen directly); the last starts the mock with
MOCK_EXTENSIONS=1 on a free port the kernel picks and checks the places come after the extensions' own.
"""
import copy
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import sys
import tempfile
import time
import unicodedata
import unittest
from unittest.mock import patch
from urllib.error import URLError
from urllib.request import ProxyHandler, Request, build_opener

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
sys.path.insert(0, str(HERE))
import mock_domains  # noqa: E402

RUN, READING, TRIP, GROCERY = "pg_3f9a1c0d5e7b2a41", "pg_8c2e4b6d1a0f9e37", "pg_5d7f1e3a9c4b2086", "pg_b1a4c7e2d9f03658"
WATCH_NUMBER = "pg_1a2b3c4d5e6f7081"
WATCH_STATUS = "pg_2b3c4d5e6f708192"
WATCH_TABLE = "pg_3c4d5e6f708192a3"
WATCH_TREND = "pg_4d5e6f708192a3b4"
WATCH_IDS = {
    "number": WATCH_NUMBER,
    "status": WATCH_STATUS,
    "table": WATCH_TABLE,
    "trend": WATCH_TREND,
}
WATCH_ICONS = {
    "number": "money",
    "status": "wifi",
    "table": "list",
    "trend": "trend_up",
}
PAGE = "/api/v1/extensions/-pages/-/ui/"
FIXTURE = ROOT / "spec" / "fixtures" / "dashboards" / "watch.json"


def _view_strings(view):
    """All user-visible strings drawn inside a View (excluding keys, types, tones, icons, action ids and control names)."""
    out = []
    if view.get("title"):
        out.append(view["title"])
    for sec in view.get("sections") or []:
        if sec.get("title"):
            out.append(sec["title"])
        for item in sec.get("items") or []:
            for field in ("title", "subtitle", "label", "value", "detail", "text"):
                if isinstance(item.get(field), str):
                    out.append(item[field])
            if item.get("type") == "table":
                for col in item.get("columns") or []:
                    if isinstance(col.get("title"), str):
                        out.append(col["title"])
                for row in item.get("rows") or []:
                    out.extend(c for c in row if isinstance(c, str))
            for act in item.get("actions") or []:
                if isinstance(act.get("title"), str):
                    out.append(act["title"])
    return out


def _index_rows(view):
    return {r["key"]: r for s in view["sections"] for r in s["items"] if r.get("type") == "row"}


def _footer_item(view, key):
    footer = view["sections"][-1]
    assert footer["key"] == "persona.footer"
    return next((i for i in footer["items"] if i.get("key") == key), None)


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
                         ["persona.pause", "persona.unpin", "persona.rename", "persona.remove"])
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


class WatchModeTests(Domain):
    """Phase 2 watch pages (#13020): the 15 fixture draws, overview gating, footer actions, events and localization."""

    mode = "watch"

    def test_seed_keeps_four_watch_pages_plus_morning_run_with_valid_titles(self):
        self.assertIs(self.namespace["assistant_state"]()["dashboards"], True)
        self.assertEqual(
            self.places(),
            [("-pages", "index", "Dashboards"), ("-pages", WATCH_NUMBER, "Elm Street rent"), ("-pages", RUN, "Morning run")],
        )
        self.assertEqual(
            self.namespace["NAVIGATION"]["pages"]["shown"],
            ["p:-pages/index", "p:-pages/" + WATCH_NUMBER, "p:-pages/" + RUN],
        )
        # All 5 pages appear on the index, with Morning run still rendered as a phase 1 non-watch page.
        rows = _index_rows(self.view("index")["view"])
        self.assertEqual(
            set(rows),
            {"page:" + pid for pid in (WATCH_NUMBER, WATCH_STATUS, WATCH_TABLE, WATCH_TREND, RUN)},
        )
        self.assertNotIn("value", rows["page:" + RUN])
        self.assertNotIn("spark", rows["page:" + RUN])
        run_view = self.view(RUN, {"context": {"platform": "web"}})["view"]
        self.assertEqual(run_view["sections"][0]["key"], "streak")
        self.assertIsNone(_footer_item(run_view, "persona.refresh"))
        self.assertIsNone(_footer_item(run_view, "persona.alert"))
        self.assertIsNone(_footer_item(run_view, "persona.overview"))

        # Check every watch page title in en, zh-Hans and ja against phase 1's reserved-name rules.
        reserved = []
        for lang in ("en", "zh-Hans", "ja"):
            catalog = json.loads((ROOT / "server/internal/i18n/locales" / lang / "assistant-pages.json").read_text(encoding="utf-8"))
            for key, val in catalog.items():
                if key.startswith("reserved_"):
                    reserved.append(unicodedata.normalize("NFKC", val).casefold().strip())
        for lang in ("en", "zh-Hans", "ja"):
            for pid in WATCH_IDS.values():
                title = self.view(pid, lang=lang)["view"]["title"]
                self.assertTrue(1 <= len(title) <= 40 and "\n" not in title, (lang, title))
                folded = unicodedata.normalize("NFKC", title).casefold()
                for name in reserved:
                    if all(ord(c) < 128 for c in name if c.isalpha()):
                        self.assertIsNone(re.search(r"(?<![A-Za-z0-9])" + re.escape(name) + r"(?![A-Za-z0-9])", folded),
                                          (lang, title, name))
                    else:
                        self.assertNotIn(name, folded, (lang, title, name))

    def test_all_15_fixture_draws_served_byte_for_byte_equal_in_en(self):
        draws = json.loads(FIXTURE.read_text(encoding="utf-8"))["draws"]
        self.assertEqual(len(draws), 15)
        for case in draws:
            name = case["name"]
            display, suffix = name.split("_", 1)
            pid = WATCH_IDS[display]
            with self.subTest(case=name):
                self.ok("GET", "/mock/dashboards?watch=%s&state=%s" % (display, suffix))
                view = self.view(pid, lang="en")["view"]
                # First section is byte-for-byte equal to the fixture's `section`.
                self.assertEqual(view["sections"][0], case["section"])
                self.assertEqual(
                    json.dumps(view["sections"][0], sort_keys=True),
                    json.dumps(case["section"], sort_keys=True),
                )
                # Followed by any own sections (number has a note section) and the footer last.
                self.assertEqual(view["sections"][-1]["key"], "persona.footer")
                if display == "number":
                    self.assertEqual([s["key"] for s in view["sections"]], ["persona.watch", "notes", "persona.footer"])
                else:
                    self.assertEqual([s["key"] for s in view["sections"]], ["persona.watch", "persona.footer"])

                # Index row carries the fixture's glance_row (empty / non-icon keeps the page's own icon).
                row = _index_rows(self.view("index", lang="en")["view"])["page:" + pid]
                glance = case["glance_row"]
                self.assertEqual(row["value"], glance["value"])
                if "spark" in glance:
                    self.assertEqual(row["spark"], glance["spark"])
                else:
                    self.assertNotIn("spark", row)
                self.assertEqual(row["icon"], glance.get("icon", WATCH_ICONS[display]))
                if "icon_color" in glance:
                    self.assertEqual(row["icon_color"], glance["icon_color"])
                else:
                    self.assertNotIn("icon_color", row)
                self.assertNotIn("badge", row)

    def test_runtime_watch_control_all_and_events_and_errors(self):
        # Switching a non-overview watch pushes invalidations for the page and the index.
        self.events.clear()
        self.ok("GET", "/mock/dashboards?watch=status&state=warning")
        self.assertEqual(self.events, [
            {"type": "extension_ui", "extension": "-pages", "id": WATCH_STATUS, "invalidate": True},
            {"type": "extension_ui", "extension": "-pages", "id": "index", "invalidate": True},
        ])

        # Switching an overview watch invalidates the page, its overview card and the index.
        self.events.clear()
        self.ok("GET", "/mock/dashboards?watch=number&state=alert")
        self.assertEqual(self.events, [
            {"type": "extension_ui", "extension": "-pages", "id": WATCH_NUMBER, "invalidate": True},
            {"type": "extension_ui", "extension": "-pages", "id": "g_" + WATCH_NUMBER[3:], "invalidate": True},
            {"type": "extension_ui", "extension": "-pages", "id": "index", "invalidate": True},
        ])

        # watch=all switches all four watch pages together.
        for state in ("empty", "normal", "alert"):
            self.events.clear()
            res = self.ok("GET", "/mock/dashboards?watch=all&state=" + state)
            watch_pages = [p for p in res["pages"] if p.get("watch")]
            self.assertEqual([p["watch_state"] for p in watch_pages], [state] * 4)
            ids = [e["id"] for e in self.events if e["type"] == "extension_ui"]
            for pid in WATCH_IDS.values():
                self.assertIn(pid, ids)
            self.assertIn("index", ids)
            self.assertIn("g_" + WATCH_NUMBER[3:], ids)
            self.assertIn("g_" + WATCH_TREND[3:], ids)

        # Bad queries answer 400.
        self.assertEqual(self.call("GET", "/mock/dashboards?watch=number")[0], 400)
        self.assertEqual(self.call("GET", "/mock/dashboards?state=empty")[0], 400)
        self.assertEqual(self.call("GET", "/mock/dashboards?watch=nope&state=empty")[0], 400)
        self.assertEqual(self.call("GET", "/mock/dashboards?watch=number&state=warning")[0], 400)
        self.assertEqual(self.call("GET", "/mock/dashboards?watch=all&state=warning")[0], 400)

    def test_overview_gating_and_card_views(self):
        gid_num = "g_" + WATCH_NUMBER[3:]
        gid_trend = "g_" + WATCH_TREND[3:]
        gid_status = "g_" + WATCH_STATUS[3:]
        gid_table = "g_" + WATCH_TABLE[3:]

        # Without ?pages_overview=1, no overview_card places are ever listed.
        self.assertEqual(self.ok("GET", "/api/v1/extensions/-/ui?placement=overview_card")["ui"], [])
        self.assertTrue(all(u["placement"] == "nav_page" for u in self.ok("GET", "/api/v1/extensions/-/ui")["ui"]))

        # With ?pages_overview=1, number and trend are listed as overview_card opening their pages.
        cards = self.ok("GET", "/api/v1/extensions/-/ui?placement=overview_card&pages_overview=1")["ui"]
        self.assertEqual(cards, [
            {"extension": "-pages", "extension_name": "Persona", "id": gid_num, "title": "Elm Street rent",
             "placement": "overview_card", "size": "compact", "opens": WATCH_NUMBER},
            {"extension": "-pages", "extension_name": "Persona", "id": gid_trend, "title": "Morning weight",
             "placement": "overview_card", "size": "compact", "opens": WATCH_TREND},
        ])
        # Without placement filter + pages_overview=1 returns nav_pages and overview_cards together.
        all_places = self.ok("GET", "/api/v1/extensions/-/ui?pages_overview=1")["ui"]
        self.assertEqual([u["id"] for u in all_places], ["index", WATCH_NUMBER, RUN, gid_num, gid_trend])
        # placement=nav_page&pages_overview=1 returns only nav_pages.
        nav_only = self.ok("GET", "/api/v1/extensions/-/ui?placement=nav_page&pages_overview=1")["ui"]
        self.assertEqual([u["id"] for u in nav_only], ["index", WATCH_NUMBER, RUN])
        # Legacy /plugins/-/ui uses plugin / plugin_name.
        legacy = self.ok("GET", "/api/v1/plugins/-/ui?placement=overview_card&pages_overview=1")["ui"]
        self.assertEqual((legacy[0]["plugin"], legacy[0]["plugin_name"], legacy[0]["opens"]), ("-pages", "Persona", WATCH_NUMBER))

        # Rendering g_<hex> returns the page title and its watch section alone (no own notes, no footer).
        draws = {d["name"]: d for d in json.loads(FIXTURE.read_text(encoding="utf-8"))["draws"]}
        card_view = self.view(gid_num)["view"]
        self.assertEqual(card_view, {"title": "Elm Street rent", "sections": [draws["number_normal"]["section"]]})
        trend_card = self.view(gid_trend)["view"]
        self.assertEqual(trend_card, {"title": "Morning weight", "sections": [draws["trend_normal"]["section"]]})

        # Pages not on Overview, non-watch pages, and read-only devices are refused.
        self.assertEqual(self.call("POST", PAGE + gid_status, {})[0], 404)
        self.assertEqual(self.call("POST", PAGE + "g_" + RUN[3:], {})[0], 404)
        self.assertEqual(self.call("POST", PAGE + gid_num, {}, role="readonly")[0], 403)
        self.assertEqual(self.call("POST", PAGE + gid_num, {"action": "unknown"})[0], 404)

        # Put table on Overview and verify a table with > 5 rows is capped at its first 5 rows on the Overview card.
        self.view(WATCH_TABLE, {"action": "persona.overview", "values": {"overview": "true"}, "context": {"platform": "web"}})
        self.assertIn(gid_table, [u["id"] for u in self.ok("GET", "/api/v1/extensions/-/ui?placement=overview_card&pages_overview=1")["ui"]])
        import dashboards_mock
        seven_rows = copy.deepcopy(draws["table_alert"])
        seven_rows["section"]["items"][1]["rows"] = [["Listing %d" % i, "%d" % (1800 + i)] for i in range(1, 8)]
        with patch.dict(dashboards_mock._DRAWS, {"table_alert": seven_rows}):
            self.ok("GET", "/mock/dashboards?watch=table&state=alert")
            full_table = self.view(WATCH_TABLE)["view"]["sections"][0]["items"][1]["rows"]
            card_table = self.view(gid_table)["view"]["sections"][0]["items"][1]["rows"]
            self.assertEqual(len(full_table), 7)
            self.assertEqual(len(card_table), 5)
            self.assertEqual(card_table, full_table[:5])

        # Dashboards off hides Overview cards and answers 404.
        self.ok("PUT", "/api/v1/assistant", {"dashboards": False})
        self.assertEqual(self.ok("GET", "/api/v1/extensions/-/ui?placement=overview_card&pages_overview=1")["ui"], [])
        self.assertEqual(self.call("POST", PAGE + gid_num, {})[0], 404)
        self.ok("PUT", "/api/v1/assistant", {"dashboards": True})
        self.assertEqual(len(self.ok("GET", "/api/v1/extensions/-/ui?placement=overview_card&pages_overview=1")["ui"]), 3)

        # Removing a page on Overview drops its Overview card; restoring brings it back.
        self.view(WATCH_NUMBER, {"action": "persona.remove"})
        self.assertNotIn(gid_num, [u["id"] for u in self.ok("GET", "/api/v1/extensions/-/ui?placement=overview_card&pages_overview=1")["ui"]])
        self.assertEqual(self.call("POST", PAGE + gid_num, {})[0], 404)
        self.view("index", {"action": "persona.restore:" + WATCH_NUMBER})
        self.assertIn(gid_num, [u["id"] for u in self.ok("GET", "/api/v1/extensions/-/ui?placement=overview_card&pages_overview=1")["ui"]])

    def test_footer_actions_lines_and_events(self):
        # Routine line: checked vs empty.
        view = self.view(WATCH_NUMBER)["view"]
        self.assertEqual(_footer_item(view, "persona.updated")["text"], "Checked today 08:02 · next check today 20:00")
        self.assertEqual([i["action"]["id"] for i in view["sections"][-1]["items"] if i["type"] == "button"],
                         ["persona.refresh", "persona.pause", "persona.unpin", "persona.rename", "persona.remove"])

        self.ok("GET", "/mock/dashboards?watch=number&state=empty")
        empty_view = self.view(WATCH_NUMBER)["view"]
        self.assertEqual(_footer_item(empty_view, "persona.updated")["text"], "Not checked yet · next check today 20:00")

        # Refresh now: toast & line "Checking now…", second tap "Already checking", advance writes next reading & clears line.
        self.events.clear()
        r1 = self.view(WATCH_NUMBER, {"action": "persona.refresh"})
        self.assertEqual(r1["toast"], "Checking now…")
        self.assertEqual(_footer_item(r1["view"], "persona.status")["text"], "Checking now…")
        self.assertEqual(self.events, [{"type": "extension_ui", "extension": "-pages", "id": WATCH_NUMBER, "invalidate": True}])

        self.events.clear()
        r2 = self.view(WATCH_NUMBER, {"action": "persona.refresh"})
        self.assertEqual(r2["toast"], "Already checking")
        self.assertEqual(_footer_item(r2["view"], "persona.status")["text"], "Checking now…")
        self.assertEqual(self.events, [])

        self.events.clear()
        self.ok("GET", "/mock/dashboards?advance=1")
        self.assertEqual(self.events, [
            {"type": "extension_ui", "extension": "-pages", "id": WATCH_NUMBER, "invalidate": True},
            {"type": "extension_ui", "extension": "-pages", "id": "index", "invalidate": True},
            {"type": "extension_ui", "extension": "-pages", "id": "g_" + WATCH_NUMBER[3:], "invalidate": True},
        ])
        after_first = self.view(WATCH_NUMBER)["view"]
        self.assertIsNone(_footer_item(after_first, "persona.status"))
        self.assertEqual(after_first["sections"][0]["items"][0]["value"], "2,150 USD")  # empty -> normal

        # Refresh again from normal -> alert on advance.
        self.view(WATCH_NUMBER, {"action": "persona.refresh"})
        self.ok("GET", "/mock/dashboards?advance=1")
        after_second = self.view(WATCH_NUMBER)["view"]
        self.assertEqual(after_second["sections"][0]["items"][0]["value"], "1,985.5 USD")  # normal -> alert

        # Pause updates / Resume updates.
        self.events.clear()
        paused = self.view(WATCH_NUMBER, {"action": "persona.pause"})
        self.assertEqual(paused["toast"], "Updates paused")
        self.assertEqual(_footer_item(paused["view"], "persona.paused")["text"], "Updates paused")
        self.assertIsNone(_footer_item(paused["view"], "persona.refresh"))
        self.assertEqual(_footer_item(paused["view"], "persona.pause")["title"], "Resume updates")
        self.assertEqual(_footer_item(paused["view"], "persona.pause")["action"]["id"], "persona.resume")
        self.assertEqual(self.events, [
            {"type": "extension_ui", "extension": "-pages", "id": WATCH_NUMBER, "invalidate": True},
            {"type": "extension_ui", "extension": "-pages", "id": "index", "invalidate": True},
        ])
        self.assertEqual(_index_rows(self.view("index")["view"])["page:" + WATCH_NUMBER]["subtitle"], "Updates paused")
        # Calling persona.refresh while paused refuses without entering checking state.
        blocked = self.view(WATCH_NUMBER, {"action": "persona.refresh"})
        self.assertEqual(blocked["toast"], "Updates paused")
        self.assertIsNone(_footer_item(blocked["view"], "persona.status"))

        self.events.clear()
        resumed = self.view(WATCH_NUMBER, {"action": "persona.resume"})
        self.assertEqual(resumed["toast"], "Updates resumed")
        self.assertIsNone(_footer_item(resumed["view"], "persona.paused"))
        self.assertIsNotNone(_footer_item(resumed["view"], "persona.refresh"))
        self.assertEqual(_footer_item(resumed["view"], "persona.pause")["action"]["id"], "persona.pause")
        self.assertEqual(self.events, [
            {"type": "extension_ui", "extension": "-pages", "id": WATCH_NUMBER, "invalidate": True},
            {"type": "extension_ui", "extension": "-pages", "id": "index", "invalidate": True},
        ])

        # Non-routine Morning run also supports Pause/Resume, and refuses persona.refresh / persona.alert / persona.overview.
        run_paused = self.view(RUN, {"action": "persona.pause"})
        self.assertEqual(_footer_item(run_paused["view"], "persona.paused")["text"], "Updates paused")
        self.view(RUN, {"action": "persona.resume"})
        for bad_act in ("persona.refresh", "persona.alert", "persona.overview"):
            self.assertEqual(self.call("POST", PAGE + RUN, {"action": bad_act})[0], 404)

        # Alert me switch row: rule in words, values on=true/false, invalid value 400.
        expected_rules = {
            WATCH_NUMBER: "Below 2,000 USD",
            WATCH_STATUS: "Problem",
            WATCH_TABLE: "3 rows or more",
            WATCH_TREND: "Above 80 kg",
        }
        for pid, rule in expected_rules.items():
            row = _footer_item(self.view(pid)["view"], "persona.alert")
            self.assertEqual(row["title"], "Alert me")
            self.assertEqual(row["subtitle"], rule)
            self.assertEqual(row["control"], {"kind": "switch", "name": "on", "on": False, "action": {"id": "persona.alert"}})

        self.events.clear()
        on_res = self.view(WATCH_NUMBER, {"action": "persona.alert", "values": {"on": "true"}})
        self.assertEqual(on_res["toast"], "Alerts on")
        self.assertIs(_footer_item(on_res["view"], "persona.alert")["control"]["on"], True)
        self.assertEqual(self.events, [{"type": "extension_ui", "extension": "-pages", "id": WATCH_NUMBER, "invalidate": True}])

        self.events.clear()
        off_res = self.view(WATCH_NUMBER, {"action": "persona.alert", "values": {"on": "false"}})
        self.assertEqual(off_res["toast"], "Alerts off")
        self.assertIs(_footer_item(off_res["view"], "persona.alert")["control"]["on"], False)
        self.assertEqual(self.events, [{"type": "extension_ui", "extension": "-pages", "id": WATCH_NUMBER, "invalidate": True}])
        self.assertEqual(self.call("POST", PAGE + WATCH_NUMBER, {"action": "persona.alert", "values": {"on": "maybe"}})[0], 400)

    def test_show_on_overview_row_is_platform_gated_and_toggles(self):
        # Shown only on web and desktop; omitted on ios, android or missing platform.
        for plat in ("web", "desktop"):
            view = self.view(WATCH_NUMBER, {"context": {"platform": plat}})["view"]
            row = _footer_item(view, "persona.overview")
            self.assertIsNotNone(row, plat)
            self.assertEqual(row["title"], "Show on Overview")
            self.assertEqual(row["control"], {"kind": "switch", "name": "overview", "on": True, "action": {"id": "persona.overview"}})
            # Control names on the same page are distinct (UI v6 CheckView rule).
            alert_row = _footer_item(view, "persona.alert")
            self.assertNotEqual(alert_row["control"]["name"], row["control"]["name"])

        for body in ({}, {"context": {"platform": "ios"}}, {"context": {"platform": "android"}}, {"context": {"platform": "macos"}}):
            self.assertIsNone(_footer_item(self.view(WATCH_NUMBER, body)["view"], "persona.overview"), body)

        # Toggling Show on Overview pushes assistant_pages + invalidations and preserves the row on web.
        self.events.clear()
        off = self.view(WATCH_NUMBER, {"action": "persona.overview", "values": {"overview": "false"}, "context": {"platform": "web"}})
        self.assertEqual(off["toast"], "Removed from Overview")
        self.assertIs(_footer_item(off["view"], "persona.overview")["control"]["on"], False)
        self.assertEqual(self.events, [
            {"type": "assistant_pages"},
            {"type": "extension_ui", "extension": "-pages", "id": "index", "invalidate": True},
            {"type": "extension_ui", "extension": "-pages", "id": WATCH_NUMBER, "invalidate": True},
        ])
        self.assertEqual([u["id"] for u in self.ok("GET", "/api/v1/extensions/-/ui?placement=overview_card&pages_overview=1")["ui"]],
                         ["g_" + WATCH_TREND[3:]])

        self.events.clear()
        on = self.view(WATCH_NUMBER, {"action": "persona.overview", "values": {"overview": "true"}, "context": {"platform": "desktop"}})
        self.assertEqual(on["toast"], "Added to Overview")
        self.assertIs(_footer_item(on["view"], "persona.overview")["control"]["on"], True)
        self.assertEqual(self.types(), ["assistant_pages", "extension_ui", "extension_ui"])
        self.assertEqual(self.call("POST", PAGE + WATCH_NUMBER, {"action": "persona.overview", "values": {"overview": "bad"}})[0], 400)

    def test_zh_hans_and_ja_served_without_english_leftovers(self):
        draws = json.loads(FIXTURE.read_text(encoding="utf-8"))["draws"]
        # Only brand name and metric symbol kg may appear as ASCII words in localized zh-Hans / ja strings.
        allowed_ascii = {"Persona", "kg"}
        ascii_word = re.compile(r"[A-Za-z]{2,}")

        for lang in ("zh-Hans", "ja"):
            strings = []
            for place in self.ok("GET", "/api/v1/extensions/-/ui?pages_overview=1", lang=lang)["ui"]:
                strings.append(place["title"])
            for case in draws:
                display, suffix = case["name"].split("_", 1)
                pid = WATCH_IDS[display]
                self.ok("GET", "/mock/dashboards?watch=%s&state=%s" % (display, suffix))
                view = self.view(pid, {"context": {"platform": "web"}}, lang=lang)["view"]
                strings.extend(_view_strings(view))
                # Structural parity with the fixture section: keys, types, tones, icons, colors, spark.
                sec, ref = view["sections"][0], case["section"]
                self.assertEqual((sec["key"], len(sec["items"])), (ref["key"], len(ref["items"])))
                for got, want in zip(sec["items"], ref["items"]):
                    for attr in ("type", "key", "tone", "icon", "icon_color", "spark"):
                        self.assertEqual(got.get(attr), want.get(attr), (lang, case["name"], attr))
                strings.extend(_view_strings(self.view("index", lang=lang)["view"]))
                if display in ("number", "trend"):
                    strings.extend(_view_strings(self.view("g_" + pid[3:], lang=lang)["view"]))

            # Also check every footer action's toast and paused / checking states in this language.
            for act, vals in (
                ("persona.refresh", {}),
                ("persona.refresh", {}),
                ("persona.pause", {}),
                ("persona.resume", {}),
                ("persona.alert", {"on": "true"}),
                ("persona.alert", {"on": "false"}),
                ("persona.overview", {"overview": "false"}),
                ("persona.overview", {"overview": "true"}),
            ):
                res = self.view(WATCH_NUMBER, {"action": act, "values": vals, "context": {"platform": "web"}}, lang=lang)
                if res.get("toast"):
                    strings.append(res["toast"])
                strings.extend(_view_strings(res["view"]))

            leftovers = [
                (s, w)
                for s in strings
                for w in ascii_word.findall(s)
                if w not in allowed_ascii
            ]
            self.assertEqual(leftovers, [], "English leftovers in %s: %r" % (lang, leftovers))


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
        help_line = next(line for line in self.registry.help_lines if line.startswith("MOCK_DASHBOARDS="))
        self.assertIn("watch", help_line)
        self.assertIn("pages_overview=1", help_line)


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

        # Switch to watch mode live: overview_card places appear after the extensions' own only with ?pages_overview=1.
        self.get("/mock/dashboards?mode=watch")
        no_flag = self.get("/api/v1/extensions/-/ui?placement=overview_card")["ui"]
        self.assertNotIn("-pages", [u["extension"] for u in no_flag])
        with_flag = self.get("/api/v1/extensions/-/ui?placement=overview_card&pages_overview=1")["ui"]
        self.assertEqual([(u["extension"], u["id"], u.get("opens")) for u in with_flag[-2:]], [
            ("-pages", "g_" + WATCH_NUMBER[3:], WATCH_NUMBER),
            ("-pages", "g_" + WATCH_TREND[3:], WATCH_TREND),
        ])
        self.assertGreaterEqual(len([u for u in with_flag if u["extension"] != "-pages"]), 1)
        card = self.get(PAGE + "g_" + WATCH_NUMBER[3:], "POST", {"context": {"platform": "web"}})["view"]
        self.assertEqual((card["title"], card["sections"][0]["key"]), ("Elm Street rent", "persona.watch"))

        self.assertIs(self.get("/api/v1/assistant", "PUT", {"dashboards": False})["dashboards"], False)
        self.assertNotIn("-pages", [u["extension"] for u in self.get("/api/v1/extensions/-/ui?pages_overview=1")["ui"]])


if __name__ == "__main__":
    unittest.main()
