"""Synthetic Health intake records, detail pages, health items and strength training for clients and bundled demos
(#13176, #14716, #14713)."""
from copy import deepcopy
from datetime import datetime, timedelta, timezone
import json
import math
import os
import threading
import uuid

HUB = None

HEALTH_INTAKE_ENABLED = True
HEALTH_INTAKE_LOCK = threading.RLock()
HEALTH_WATER_EXTENSION_MODE = "pending"
HEALTH_WATER_EXTENSION_PENDING = True
HEALTH_INTAKE_SETTINGS = {}
# What Health tracks (docs/protocol/protocol.md "Health items", #14716): the catalog, then the list as the hub keeps it.
# MOCK_HEALTH_ITEMS picks the owner: existing (default, records on file: the hub's defaults add water and protein), fresh
# (Activity rings and trends only, so the web asks once) or off (a hub before #14711: no feature, no route).
HEALTH_ITEMS_CATALOG = (("water", "persona"), ("protein", "persona"), ("strength", "persona"), ("activity", "apple_health"),
                        ("trends", "apple_health"))
HEALTH_ITEMS_MODE = "existing"
HEALTH_ITEMS = {}
# Strength training (docs/protocol/protocol.md, strength training, #14713): MOCK_HEALTH_STRENGTH=on (default: the feature,
# the catalog's `strength` and its pages, exported from the hub's code: health_strength_ui.py), off (a hub before it) or
# card (also a Health conversation holding a draft's card, whose Adjust opens the edit page).
HEALTH_STRENGTH_MODE = "on"
# The conversation the card is in, and the hub's public address its Adjust links to (a documentation address, listed in
# the hello's endpoints in card mode so the web opens it in the app).
STRENGTH_CHAT = "hlth0005-0000-4000-8000-000000000005"
STRENGTH_PUBLIC = "https://hub.example.com"


def health_intake_reset():
    """Restore the seeded records, settings and initial migration state together."""
    global HEALTH_WATER_EXTENSION_PENDING
    with HEALTH_INTAKE_LOCK:
        HEALTH_INTAKE_ENTRIES.clear()
        HEALTH_INTAKE_ENTRIES.update(health_intake_seed())
        HEALTH_WATER_EXTENSION_PENDING = HEALTH_WATER_EXTENSION_MODE == "pending"
        HEALTH_INTAKE_SETTINGS.clear()
        HEALTH_INTAKE_SETTINGS.update({
            "water": {"goal_ml": 2000, "cup_ml": 250, "remind": False, "start": "09:00", "end": "21:00",
                      "interval_minutes": 60, "weekends": True, "stop_when_done": True},
            "protein": {"goal_g": 60},
        })


def health_intake_seed():
    """Fictional records for the hub and bundled demo, always on today's date."""
    at = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
    return {str(uuid.UUID(int=i)): {"id": str(uuid.UUID(int=i)), "kind": kind, "amount": amount,
            "at": at, "note": "", "seq": i, "created_at": at}
            for i, (kind, amount) in enumerate([("water", 500), ("water", 250), ("protein", 36)], 1)}


HEALTH_INTAKE_ENTRIES = {}
health_intake_reset()


def health_items_reset():
    """The never-saved list the hub reads for the owner MOCK_HEALTH_ITEMS names."""
    with HEALTH_INTAKE_LOCK:
        HEALTH_ITEMS.clear()
        HEALTH_ITEMS.update(items=([] if HEALTH_ITEMS_MODE == "fresh" else ["water", "protein"]) + ["activity", "trends"],
                            revision=0, saved=False)


def health_items_catalog():
    """The catalog the hub lists: strength training only on a hub that has it."""
    return [(i, s) for i, s in HEALTH_ITEMS_CATALOG if i != "strength" or HEALTH_STRENGTH_MODE != "off"]


def health_items_state():
    with HEALTH_INTAKE_LOCK:
        return {"catalog": [{"id": i, "source": s} for i, s in health_items_catalog()], "items": list(HEALTH_ITEMS["items"]),
                "revision": HEALTH_ITEMS["revision"], "saved": HEALTH_ITEMS["saved"]}


health_items_reset()


def health_items_route(h, method, path, query, body=b""):
    """GET and PUT /api/v1/assistant/health/items as the hub answers them: the whole list at a revision, 409 with the
    current list for a stale one, one `health_items` message per save. /mock/health-items?mode=existing|fresh|off starts
    the owner over (reload the page for the hello's features)."""
    global HEALTH_ITEMS_MODE
    if path == "/mock/health-items":
        if query.get("mode", [""])[0] in ("existing", "fresh", "off"):
            HEALTH_ITEMS_MODE = query["mode"][0]
            health_items_reset()
        h.send_json({"mode": HEALTH_ITEMS_MODE, **health_items_state()})
        return True
    if HEALTH_ITEMS_MODE == "off" or not HEALTH_INTAKE_ENABLED:
        h.send_json({"error": "Not found"}, 404)
        return True
    if h.headers.get("X-Persona-Manager"):
        h.send_json({"error": "Health items belong to the default manager"}, 400)
        return True
    if method == "GET":
        h.send_json(health_items_state())
        return True
    try:
        req = json.loads(body or b"null")
    except ValueError:
        req = None
    known = [i for i, _ in health_items_catalog()]
    items = req.get("items") if isinstance(req, dict) else None
    revision = req.get("revision") if isinstance(req, dict) else None
    if (not isinstance(req, dict) or set(req) != {"items", "revision"} or not isinstance(items, list)
            or any(not isinstance(i, str) or i not in known for i in items) or len(set(items)) != len(items)
            or isinstance(revision, bool) or not isinstance(revision, int) or revision < 0):
        h.send_json({"error": "Invalid health items", "code": "invalid_health_items"}, 400)
        return True
    with HEALTH_INTAKE_LOCK:
        if revision != HEALTH_ITEMS["revision"]:
            h.send_json(health_items_state(), 409)
            return True
        HEALTH_ITEMS.update(items=list(items), revision=HEALTH_ITEMS["revision"] + 1, saved=True)
        state = health_items_state()
    HUB.broadcast({"type": "health_items", "revision": state["revision"]})
    h.send_json(state)
    return True


def health_intake_snapshot(days=7):
    with HEALTH_INTAKE_LOCK:
        today = datetime.now(timezone.utc).date()
        active = [e for e in HEALTH_INTAKE_ENTRIES.values() if "deleted_at" not in e]
        def day(e):
            return datetime.fromisoformat(e["at"].replace("Z", "+00:00")).astimezone(timezone.utc).date()
        def total(kind, date):
            return round(sum(e["amount"] for e in active if e["kind"] == kind and day(e) == date), 1)
        def portion(kind, goal):
            entries = [dict(e) for e in active if e["kind"] == kind and day(e) == today]
            entries.sort(key=lambda e: (-datetime.fromisoformat(e["at"].replace("Z", "+00:00")).timestamp(), e["id"]))
            return {"today": total(kind, today), "goal": goal, "entries": entries}
        result = {"water": portion("water", HEALTH_INTAKE_SETTINGS["water"]["goal_ml"]),
                  "protein": portion("protein", HEALTH_INTAKE_SETTINGS["protein"]["goal_g"]), "zone": "UTC",
                  "days": [{"date": str(d), "water": total("water", d), "protein": total("protein", d)}
                           for d in [today - timedelta(days=i) for i in reversed(range(days))]]}
        if HEALTH_WATER_EXTENSION_PENDING:
            result["water_extension"] = "pending"
        return result


def health_intake_route(h, method, path, query, body=b""):
    global HEALTH_INTAKE_ENABLED, HEALTH_WATER_EXTENSION_PENDING
    base = "/api/v1/assistant/health/intake"
    if path == "/mock/health-intake":
        with HEALTH_INTAKE_LOCK:
            if "enabled" in query: HEALTH_INTAKE_ENABLED = query["enabled"][0] != "0"
            if query.get("reset") == ["1"]:
                health_intake_reset()
        h.send_json({"enabled": HEALTH_INTAKE_ENABLED})
        return True
    if path != base and not path.startswith(base + "/"): return False
    if query.get("space") != ["health"] or h.headers.get("X-Persona-Manager"):
        h.send_json({"error": "Intake must use the default manager's health space"}, 400)
        return True
    if not HEALTH_INTAKE_ENABLED:
        h.send_json({"error": "Update your hub first"}, 403)
        return True
    try:
        with HEALTH_INTAKE_LOCK:
            if method == "POST" and path == base + "/import":
                if HEALTH_WATER_EXTENSION_MODE == "none":
                    h.send_json({"error": "Water extension is not installed", "code": "water_import_failed"}, 404)
                    return True
                imported = 0
                if HEALTH_WATER_EXTENSION_PENDING:
                    now = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
                    for index, amount in enumerate((200, 250), 1):
                        identity = "00000000-0000-5000-8000-%012d" % index
                        if identity not in HEALTH_INTAKE_ENTRIES:
                            HEALTH_INTAKE_ENTRIES[identity] = {
                                "id": identity, "kind": "water", "amount": amount, "at": now, "note": "",
                                "seq": max((e["seq"] for e in HEALTH_INTAKE_ENTRIES.values()), default=0) + 1,
                                "created_at": now,
                            }
                            imported += 1
                    HEALTH_WATER_EXTENSION_PENDING = False
                    HEALTH_INTAKE_SETTINGS["water"].update(goal_ml=2400, interval_minutes=90, remind=True)
                    HUB.broadcast({"type": "health_intake"})
                h.send_json({"imported": imported, "extension_disabled": True,
                             "settings": deepcopy(HEALTH_INTAKE_SETTINGS)})
                return True
            if method == "GET" and path == base + "/changes":
                after = int(query.get("after", ["0"])[0])
                limit = int(query.get("limit", ["100"])[0])
                if after < 0 or not 1 <= limit <= 1000: raise ValueError("Invalid changes cursor or limit")
                changes = sorted((dict(e) for e in HEALTH_INTAKE_ENTRIES.values() if e["seq"] > after), key=lambda e: e["seq"])
                page = changes[:limit]
                h.send_json({"changes": page, "next": page[-1]["seq"] if page else after, "has_more": len(changes) > limit})
                return True
            if method == "GET" and path == base:
                days = int(query.get("days", ["7"])[0])
                if not 1 <= days <= 366: raise ValueError("days must be 1–366")
                snapshot = health_intake_snapshot(days)
                if HEALTH_ITEMS_MODE != "off":
                    snapshot["items"] = list(HEALTH_ITEMS["items"])
                h.send_json(snapshot)
                return True
            if method == "POST" and path == base + "/ui":
                req = json.loads(body or b"{}")
                if str(json.loads(req.get("state") or "{}").get("page", "")).startswith("strength."):
                    import health_strength_ui
                    if HEALTH_STRENGTH_MODE == "off":
                        raise ValueError("Invalid page")
                    try:
                        h.send_json(health_strength_ui.answer(req))
                    except KeyError:
                        h.send_json({"error": "That strength training page could not be found", "code": "bad_request"}, 400)
                    return True
                import health_intake_ui
                h.send_json(health_intake_ui.answer(req))
                return True
            if method == "POST" and path == base:
                draft = json.loads(body or b"{}")
                identity = str(uuid.UUID(draft["id"]))
                kind = draft["kind"]
                amount = draft.get("amount", 250 if kind == "water" else None)
                if kind not in ("water", "protein") or isinstance(amount, bool) or not isinstance(amount, (int, float)):
                    raise ValueError("Choose water or protein and enter an amount")
                if not math.isfinite(amount) or not (1 if kind == "water" else 0.1) <= amount <= (3000 if kind == "water" else 200) or abs(amount * 10 - round(amount * 10)) > 0.000001:
                    raise ValueError("Amount is outside the allowed range or precision")
                now = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
                at = datetime.fromisoformat(draft.get("at", now).replace("Z", "+00:00"))
                if at.tzinfo is None: raise ValueError("Timestamp needs a timezone")
                note = draft.get("note", "")
                if not isinstance(note, str) or "\0" in note or len(note.encode()) > 4096: raise ValueError("Invalid note")
                entry = HEALTH_INTAKE_ENTRIES.setdefault(identity, {"id": identity, "kind": kind, "amount": amount,
                    "at": at.astimezone(timezone.utc).isoformat().replace("+00:00", "Z"), "note": note,
                    "seq": max((e["seq"] for e in HEALTH_INTAKE_ENTRIES.values()), default=0) + 1, "created_at": now})
            elif method == "DELETE" and path.startswith(base + "/"):
                identity = str(uuid.UUID(path[len(base) + 1:]))
                entry = HEALTH_INTAKE_ENTRIES.get(identity)
                if entry is None:
                    h.send_json({"error": "Intake entry not found"}, 404)
                    return True
                if "deleted_at" not in entry:
                    entry["deleted_at"] = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
                    entry["seq"] = max(e["seq"] for e in HEALTH_INTAKE_ENTRIES.values()) + 1
            else:
                h.send_json({"error": "Unknown intake route"}, 404)
                return True
            snapshot = health_intake_snapshot()
            response = {"today": {k: snapshot[k]["today"] for k in ("water", "protein")}, "zone": snapshot["zone"]}
            if method == "POST": response["entry"] = dict(entry)
        HUB.broadcast({"type": "health_intake"})
        h.send_json(response)
    except (ValueError, KeyError, TypeError) as error:
        h.send_json({"error": str(error)}, 400)
    return True


def strength_chat_feed(h, req):
    """GET the strength conversation's feed: the draft's card (assistant view), in the language the page asks in."""
    import health_strength_ui
    lang = h.headers.get("Accept-Language", "") or ""
    c = health_strength_ui.card(lang)
    now = STRENGTH_CARD["at"]
    view = {"id": c["key"], "conversation": STRENGTH_CHAT, "version": c["version"],
            "view": c["view"], "created_at": now, "updated_at": now}
    item = {"id": "feed_strength_draft", "conversation": STRENGTH_CHAT, "at": now, "kind": "view", "ref": c["key"],
            "body": {"view": view}}
    h.send_json({"items": [item], "plans": {}, "views": {c["key"]: view}})


def strength_card_action(h, req):
    """The card's Save / Don't save (POST …/views/{key}/actions): settles the draft once, then its card and edit page
    are read-only; the hub says so with `assistant_view` and `health_strength`, as it does."""
    import health_strength_ui
    key = req.match["key"]
    if key != health_strength_ui.card_key():
        return False
    body = json.loads(req.body or b"{}")
    if body.get("action") not in ("save", "discard"):
        h.send_json({"error": "Invalid action", "code": "bad_request"}, 400)
        return True
    _, pending, _ = health_strength_ui.ids()
    at = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
    receipt = {"key": key, "action": body["action"], "values": {}, "client_msg_id": body.get("client_msg_id", ""),
               "status": "sent", "revision": 1, "at": at}
    if body["action"] == "save":
        health_strength_ui.SETTLED.add(pending)
    HUB.broadcast({"type": "health_strength"})
    h.send_json({"action": receipt})
    return True


STRENGTH_CARD = {"at": ""}


def strength_reset():
    import health_strength_ui
    health_strength_ui.SETTLED.clear()
    STRENGTH_CARD.update(at=datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"))


def route(h, req):
    return health_intake_route(h, req.method, req.path, req.query, req.body)


def items_route(h, req):
    return health_items_route(h, req.method, req.path, req.query, req.body)


def register(d):
    global HUB, HEALTH_INTAKE_ENABLED, HEALTH_WATER_EXTENSION_MODE, HEALTH_ITEMS_MODE, HEALTH_STRENGTH_MODE
    HUB = d.hub
    HEALTH_INTAKE_ENABLED = os.environ.get("MOCK_HEALTH_INTAKE") != "off"
    HEALTH_WATER_EXTENSION_MODE = os.environ.get("MOCK_WATER_EXTENSION", "pending")
    HEALTH_ITEMS_MODE = os.environ.get("MOCK_HEALTH_ITEMS", "existing")
    HEALTH_STRENGTH_MODE = os.environ.get("MOCK_HEALTH_STRENGTH", "on")
    health_intake_reset()
    health_items_reset()
    d.help("MOCK_HEALTH_INTAKE=off  omit Health intake and refuse its routes (#13176)")
    d.help("MOCK_WATER_EXTENSION=none|migrated  omit the water migration card; default pending (#13636)")
    d.help("MOCK_HEALTH_ITEMS=fresh|off  an owner who never chose what Health tracks (asked once), or a hub without the list; default existing (#14716)")
    d.on_reset(health_intake_reset)
    d.on_reset(health_items_reset)
    d.feature("health_intake", when=lambda: HEALTH_INTAKE_ENABLED)
    d.feature("health_items", when=lambda: HEALTH_INTAKE_ENABLED and HEALTH_ITEMS_MODE != "off")
    d.help("MOCK_HEALTH_STRENGTH=off|card  a hub without strength training, or one more Health conversation with a draft's card (Adjust opens its page; lists https://hub.example.com in the hello's endpoints); default on (#14713)")
    d.feature("health_strength", when=lambda: HEALTH_INTAKE_ENABLED and HEALTH_STRENGTH_MODE != "off")
    if HEALTH_INTAKE_ENABLED and HEALTH_STRENGTH_MODE == "card":
        strength_reset()
        d.on_reset(strength_reset)
        d.feature("assistant_views")
        HUB.ASSISTANT.append(HUB.health_sess(STRENGTH_CHAT, "今天的训练", 5, "记好了，这四组等你确认。"))
        HUB.ITEMS[STRENGTH_CHAT] = [
            HUB.it("user", "text", 7, text="刚练完：卧推热身 20kg 12 个，60kg 做了 8 个和 7 个，坐姿推胸 50kg 10 个"),
            HUB.it("assistant", "text", 6, text="记好了，这四组等你确认：点「记下」保存，「调整」可以逐组改。"),
        ]
        HUB.ENDPOINTS.append({"url": STRENGTH_PUBLIC, "kind": "name", "port": 443, "scheme": "https",
                              "seen_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")})
        d.route("GET", "/api/v1/assistant/conversations/%s/feed" % STRENGTH_CHAT, strength_chat_feed)
        d.route("POST", r"/api/v1/assistant/conversations/%s/views/(?P<key>[^/]+)/actions" % STRENGTH_CHAT, strength_card_action)
    for method in ("GET", "POST", "DELETE"):
        d.route(method, r"/api/v1/assistant/health/intake(?:/.*)?", route)
        d.route(method, "/mock/health-intake", route)
    for method in ("GET", "PUT"):
        d.route(method, r"/api/v1/assistant/health/items", items_route)
    d.route("GET", "/mock/health-items", items_route)
