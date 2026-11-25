"""UI version 12's map on a made-up extension page, overview card, and assistant inline tool_views (#14730).

MOCK_EXTENSION_MAP=1 (with MOCK_EXTENSIONS=1):
  - Registers the `tool_views` hello/REST feature and seeds an assistant conversation (`ASK_MAP`) whose tool result and
    fold carry an out-of-band `ExtensionView` route map between the work summary and final reply.
  - Adds a made-up "Example Maps" nav_page (`route-planner`) and compact overview_card (`route-planner-card`):
      * Two WGS-84 routes (`r-fastest`, `r-alt`) for route pill switching and camera fitting
      * Three markers (`origin`, `waypoint`, `dest`), where `waypoint` pushes a stop detail page
      * One verified `https://` navigation link
  - MOCK_EXTENSION_MAP_DOWNGRADE=1 answers as for a client before UI version 12 (plain text/rows instead of `map`).

apple/PersonaUITests/ExtensionMapUITests.swift drives it.
"""
import copy
import json
import os

HELP = (
    "MOCK_EXTENSION_MAP=1  (with MOCK_EXTENSIONS=1) Example Maps UI v12 nav_page, overview_card, and assistant inline "
    "tool_views (#14730): route switching, markers pushing stop detail pages, Reset View, full screen, and HTTPS links",
    "MOCK_EXTENSION_MAP_DOWNGRADE=1  (with MOCK_EXTENSION_MAP=1) the Example Maps page answers as for an app before "
    "UI version 12: plain fallback text and rows instead of a map component",
)

MODE = os.environ.get("MOCK_EXTENSION_MAP") == "1"
DOWNGRADE = os.environ.get("MOCK_EXTENSION_MAP_DOWNGRADE") == "1"
PAGE = "/api/v1/extensions/example-maps/-/ui/route-planner"
ASK_MAP = "b2e4d6f8-0000-4000-8000-000000000012"

_STOPS = {
    "origin": ("Home", "West Gate · Departure", 39.9087, 116.3975, "origin", None),
    "waypoint": ("Coffee Stop", "Chaoyang Hub · Pickup", 39.9410, 116.4320, "waypoint", "open-stop"),
    "dest": ("Office", "Gate 2 · Arrival", 39.9929, 116.3340, "destination", "open-stop"),
}


def _map_component(key="commute-map", compact=False):
    markers = []
    for stop_id, (label, subtitle, lat, lon, kind, action_id) in _STOPS.items():
        marker = {
            "id": stop_id,
            "lat": lat,
            "lon": lon,
            "kind": kind,
            "label": label,
            "subtitle": subtitle,
        }
        if action_id and not compact:
            marker["action"] = {
                "id": action_id,
                "values": {"id": stop_id},
                "push": {"ui": "route-planner", "state": stop_id},
            }
        markers.append(marker)

    routes = [
        {
            "id": "r-fastest",
            "label": "Fastest",
            "mode": "driving",
            "distance_meters": 12400,
            "duration_seconds": 1380,
            "summary": "Via Ring Rd",
            "selected": True,
            "tone": "positive",
            "points": [
                {"lat": 39.9087, "lon": 116.3975},
                {"lat": 39.9410, "lon": 116.4320},
                {"lat": 39.9929, "lon": 116.3340},
            ],
        },
        {
            "id": "r-alt",
            "label": "Transit",
            "mode": "transit",
            "distance_meters": 14200,
            "duration_seconds": 2100,
            "summary": "Line 10 + Line 15",
            "selected": False,
            "points": [
                {"lat": 39.9087, "lon": 116.3975},
                {"lat": 39.9560, "lon": 116.3890},
                {"lat": 39.9929, "lon": 116.3340},
            ],
        },
    ]

    return {
        "type": "map",
        "key": key,
        "title": "Morning Commute" if not compact else "Commute Preview",
        "alt": "Route map from Home via Coffee Stop to Office showing driving and transit options.",
        "caption": None if compact else "Switch routes, inspect stops, or open full screen.",
        "aspect": "wide" if compact else "landscape",
        "markers": markers,
        "routes": routes,
        "links": [
            {"title": "Open Route Guide", "url": "https://maps.example.com/route?id=commute"},
        ],
    }


def _map_downgrade_items(component):
    items = [{"type": "text", "text": component["alt"], "style": "secondary"}]
    for route in component["routes"]:
        mins = max(1, round(route["duration_seconds"] / 60))
        km = route["distance_meters"] / 1000.0
        items.append({
            "type": "row",
            "key": f"fallback-route:{route['id']}",
            "title": route["label"],
            "subtitle": route.get("summary"),
            "value": f"{mins} min · {km:.1f} km",
            "icon": "car" if route["mode"] == "driving" else "map",
        })
    for marker in component["markers"]:
        row = {
            "type": "row",
            "key": f"fallback-stop:{marker['id']}",
            "title": marker["label"],
            "subtitle": marker.get("subtitle"),
            "icon": "location",
        }
        if marker.get("action"):
            row["action"] = marker["action"]
        items.append(row)
    return items


def _stop_detail_view(stop_id):
    info = _STOPS.get(stop_id) or _STOPS["waypoint"]
    label, subtitle, lat, lon, kind, _ = info
    return {
        "title": label,
        "sections": [
            {
                "key": f"stop-{stop_id}",
                "items": [
                    {
                        "type": "header",
                        "title": label,
                        "subtitle": subtitle,
                        "badges": [{"text": kind.title(), "tone": "positive"}],
                    },
                    {
                        "type": "text",
                        "text": f"Stop details for {label} along the planned commute route.",
                    },
                    {
                        "type": "metric",
                        "label": "WGS-84 Coordinates",
                        "value": f"{lat:.4f}, {lon:.4f}",
                    },
                ],
            }
        ],
    }


def _route_planner_view(card=False, ui_version=12, unsupported=()):
    use_map = ui_version >= 12 and "map" not in unsupported
    component = _map_component(key="commute-card-map" if card else "commute-map", compact=card)
    if card:
        items = [component] if use_map else _map_downgrade_items(component)[:3]
        return {"title": "Commute Map", "sections": [{"key": "commute-card", "items": items}]}

    body_items = [component] if use_map else _map_downgrade_items(component)
    return {
        "title": "Commute Map",
        "refresh_after": 30,
        "sections": [
            {
                "key": "commute",
                "items": [
                    {
                        "type": "header",
                        "title": "Home to Office",
                        "subtitle": "Fastest drive 23 min · 12.4 km",
                        "badges": [{"text": "2 Routes", "tone": "positive"}],
                    },
                    *body_items,
                ],
            }
        ],
    }


def mock_route_planner(handler, body, card=False):
    request = json.loads(body) if body else {}
    ctx = request.get("context") or {}
    ui_version = ctx.get("ui_version", 12)
    unsupported = set(ctx.get("unsupported") or [])
    if DOWNGRADE:
        ui_version = 11
    state = request.get("state")
    stop_id = state if isinstance(state, str) and state in _STOPS else None
    if request.get("action") == "open-stop":
        stop_id = (request.get("values") or {}).get("id") or "waypoint"
    if stop_id:
        handler.send_json({"view": _stop_detail_view(stop_id)})
        return True
    handler.send_json({"view": _route_planner_view(card=card, ui_version=ui_version, unsupported=unsupported)})
    return True


def _chat_route_view():
    comp = _map_component(key="chat-route-map", compact=True)
    comp["aspect"] = "wide"
    return {
        "title": "Commute Options",
        "fallback": "Fastest drive: 23 min (12.4 km). Transit: 35 min (14.2 km).",
        "sections": [{"key": "chat-route", "items": [comp]}],
    }


def _fold_tool_views(run):
    """Collect out-of-band tool_views from tool_result items in `run`, mirroring transcript.FoldToolViews."""
    uses = {x.get("tool_use_id"): x for x in run if x.get("kind") == "tool_use" and x.get("tool_use_id")}
    out = []
    seen = set()
    for it in run:
        if it.get("kind") != "tool_result":
            continue
        views = it.get("tool_views") or []
        if not views:
            continue
        call_id = it.get("tool_use_id") or ""
        item_id = it.get("id") or ""
        if item_id:
            base_key = "i:" + item_id
        elif call_id:
            base_key = "t:" + call_id
        else:
            continue
        tool_name = (uses.get(call_id) or {}).get("tool_name") or it.get("tool_name") or ""
        for idx, view in enumerate(views):
            dedup_key = f"{base_key}#{idx}"
            if dedup_key in seen:
                continue
            seen.add(dedup_key)
            entry = {"view": copy.deepcopy(view)}
            if item_id:
                entry["item_id"] = item_id
            if call_id:
                entry["tool_use_id"] = call_id
            if tool_name:
                entry["tool"] = tool_name
            out.append(entry)
            if len(out) >= 8:
                return out
    return out


def _seed_chat(hub):
    it, tool = hub.it, hub.tool
    rows = [
        it("user", "text", 5.0, text="Compare driving and transit to Office."),
        it("assistant", "thinking", 4.9, text="Comparing driving and subway commute options."),
    ]
    call_items = tool(
        "mcp__amap__maps_direction_driving",
        {"origin": "Home", "destination": "Office"},
        '{"fastest_seconds":1380,"distance_meters":12400}',
        4.8,
    )
    call_items[1]["tool_views"] = [_chat_route_view()]
    rows += call_items
    rows.append(it(
        "assistant",
        "text",
        4.6,
        text="Fastest drive takes 23 min (12.4 km) via Ring Rd, or take Transit in 35 min.",
    ))
    for seq, row in enumerate(rows, 1):
        row["seq"] = seq
    hub.ITEMS[ASK_MAP] = rows
    hub.TASK_FEED[ASK_MAP] = []
    if not any(s.get("session_id") == ASK_MAP for s in hub.ASSISTANT):
        hub.ASSISTANT.append(
            hub.assistant_sess(
                ASK_MAP,
                "Commute route plan",
                4,
                "Fastest drive takes 23 min (12.4 km) via Ring Rd, or take Transit in 35 min.",
            )
        )


def _wrap_fold_work(hub):
    handler_cls = getattr(hub, "H", None)
    do_get = getattr(handler_cls, "do_GET", None) if handler_cls is not None else None
    get_globals = getattr(do_get, "__globals__", None)
    original_fold = (get_globals.get("fold_work") if get_globals else None) or hub.fold_work
    if getattr(original_fold, "_wraps_tool_views", False):
        return

    def fold_work_with_tool_views(items):
        folded = original_fold(items)
        for row in folded:
            if row.get("kind") != "fold":
                continue
            through = (row.get("fold") or {}).get("through_seq")
            if through is None:
                continue
            run = [x for x in items if row["seq"] <= x.get("seq", -1) <= through]
            tv = _fold_tool_views(run)
            if tv:
                row["fold"]["tool_views"] = tv
        return folded

    fold_work_with_tool_views._wraps_tool_views = True
    if get_globals is not None:
        get_globals["fold_work"] = fold_work_with_tool_views
    hub.fold_work = fold_work_with_tool_views


def register(d):
    d.help(*HELP)
    if not MODE:
        return
    d.feature("tool_views")
    _seed_chat(d.hub)
    _wrap_fold_work(d.hub)
    d.on_reset(lambda: _seed_chat(d.hub))

    if not d.hub.PLUGINS_MODE:
        return
    d.hub.EXTENSION_UI_EXTRA.extend([
        {
            "plugin": "example-maps",
            "plugin_name": "Example Maps",
            "id": "route-planner",
            "title": "Commute Map",
            "placement": "nav_page",
            "size": "full",
            "icon": "map",
        },
        {
            "plugin": "example-maps",
            "plugin_name": "Example Maps",
            "id": "route-planner-card",
            "title": "Commute Map",
            "placement": "overview_card",
            "size": "compact",
            "opens": "route-planner",
        },
    ])
    d.route(
        ["GET", "POST"],
        PAGE + "(?P<card>-card)?",
        lambda h, req: mock_route_planner(h, req.body, card=bool(req.match["card"])),
    )
