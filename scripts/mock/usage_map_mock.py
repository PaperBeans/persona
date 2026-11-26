"""The disk usage map (#14576, docs/server/machine-usage-map.md): MOCK_USAGE_MAP=1.

The Mac Studio's disk reads 93 % full (so its card shows Disk almost full and its details the guidance banner), and the
hub answers the map's routes for it with made-up folders:

- GET  /api/v1/machines/{mid}/storage/usage            the overview "scanning" on the first ask, then "ready"; folders
                                                       Movies, Downloads, Projects (two levels) and any other as files
- POST /api/v1/machines/{mid}/storage/trash(/undo|/purge)  moves items out of the fixture and back, frees on purge
- POST /api/v1/machines/{mid}/cleanup                  the cache catalog's cleanup (when MOCK_MACHINE_ALERTS is off)
- GET  /api/v1/extensions/-/offload                    one neutral provider, "Example Cloud"
- GET  /api/v1/extensions/example-offload/-/offload/cloud/inventory   what is already offloaded
- POST …/run                                           the first run answers 409 tool_install_required; with install
                                                       (or once installed) 202 {job}; a dry run 200
- GET  …/jobs/{job}, DELETE …/jobs/{job}               install → check → upload → verify → cleanup → done, a step a
                                                       second; done moves the items onto the cloud side

MOCK_USAGE_MAP=old answers the usage route 404 without a code, as a hub from before the map.
GET /mock/usage_map shows the state; ?reset=1 puts the fixture back.
"""
import copy
import os
import time

MODE = os.environ.get("MOCK_USAGE_MAP", "")
GB = 1 << 30
DAY = 86400
HOME = "/Users/me"
MACHINE = "studio"  # the Mac Studio: a Mac every manager's list shows
PROVIDER = {"extension": "example-offload", "id": "cloud", "name": "Example Cloud", "kinds": ["file", "dir", "app"], "restore": True}
TOOL = {"name": "example-offload", "title": "Example Offload Tool", "version": "1.4.0", "installed": False,
        "source": "Downloaded from the extension's release page and installed in ~/.local/bin on this computer."}


def _now():
    return int(time.time())


def _dir(name, size_gb, *, ago_days=5, kids=None, file=False, cache_id=None, category="personal", can_trash=True,
         can_offload=True, refusal=None, base=HOME):
    path = "%s/%s" % (base, name)
    n = {"id": path, "name": name.rsplit("/", 1)[-1], "path": path, "display": path.replace(HOME, "~", 1),
         "kind": "file" if file else "dir", "category": category, "bytes": int(size_gb * GB),
         "used_at": _now() - int(ago_days * DAY), "has_children": bool(kids) or (not file),
         "can_trash": can_trash, "can_offload": can_offload}
    if refusal:
        n["refusal"] = refusal
    if cache_id:
        n["cache_id"], n["cache_in_use"] = cache_id, False
    if kids:
        n["children"] = kids
    return n


def _app(name, size_gb, ago_days):
    path = "/Applications/%s.app" % name
    return {"id": path, "name": name, "path": path, "display": path, "kind": "app", "category": "apps",
            "bytes": int(size_gb * GB), "used_at": _now() - ago_days * DAY, "has_children": False, "can_trash": False,
            "can_offload": True, "refusal": "app_bundle", "app": {"bundle_id": "com.example." + name.lower().replace(" ", ""), "version": "2.1"}}


def _group(key, category, kids, extra_gb=0):
    return {"id": "group:" + key, "name": "", "key": key, "kind": "group", "category": category,
            "bytes": sum(k["bytes"] for k in kids) + int(extra_gb * GB), "has_children": True, "children": kids}


def _fixture():
    """Every folder the mock knows, by path, plus the overview's groups."""
    movies = _dir("Movies", 160, ago_days=420, kids=[
        _dir("Movies/Trips 2019", 90, ago_days=1500), _dir("Movies/Renders", 70, ago_days=400)])
    downloads = _dir("Downloads", 80, ago_days=30, kids=[
        _dir("Downloads/installers", 45, ago_days=300), _dir("Downloads/archive.zip", 35, ago_days=700, file=True)])
    projects = _dir("Projects", 120, ago_days=1, kids=[
        _dir("Projects/site", 70, ago_days=1, kids=[_dir("Projects/site/node_modules", 40, ago_days=1),
                                                    _dir("Projects/site/build", 30, ago_days=2)]),
        _dir("Projects/app", 50, ago_days=3, kids=[_dir("Projects/app/assets", 35, ago_days=3),
                                                   _dir("Projects/app/src", 15, ago_days=3)])])
    pictures = _dir("Pictures", 60, ago_days=60, can_trash=False, refusal="standard_folder", kids=[
        _dir("Pictures/Photos Library.photoslibrary", 60, ago_days=60, can_trash=False, can_offload=False, refusal="managed_library")])
    home = [movies, projects, downloads, pictures]
    caches = [_dir("Library/Caches/go-build", 22, ago_days=200, category="cache", cache_id="go-build", can_offload=False),
              _dir("Library/Developer/Xcode/DerivedData", 38, ago_days=250, category="cache", cache_id="xcode-derived", can_offload=False)]
    appdata = [_dir("Library/Application Support", 90, ago_days=1, category="appdata", can_trash=False, can_offload=False, refusal="app_managed"),
               _dir("Library/Containers", 60, ago_days=1, category="appdata", can_trash=False, can_offload=False, refusal="app_managed")]
    apps = [_app("Example Studio", 60, 400), _app("Photo Editor", 35, 20), _app("Music Maker", 25, 200)]
    system = [_dir("Library", 50, base="", category="system", can_trash=False, can_offload=False, refusal="outside_home"),
              _dir("usr/local", 30, base="", category="system", can_trash=False, can_offload=False, refusal="outside_home"),
              {"id": "group:unmeasured", "name": "", "key": "unmeasured", "kind": "unmeasured", "category": "other",
               "bytes": 100 * GB, "has_children": False}]
    return {"home": home, "caches": caches, "appdata": appdata, "apps": apps, "system": system}


def _inventory():
    now = _now()
    return [
        {"id": "trips-2018", "name": "Trips 2018", "kind": "dir", "bytes": 48 * GB, "stored_bytes": 44 * GB,
         "origin_path": HOME + "/Movies/Trips 2018", "offloaded_at": now - 90 * DAY, "used_at": now - 900 * DAY, "restorable": True},
        {"id": "old-projects", "name": "Old Projects", "kind": "dir", "bytes": 26 * GB, "origin_path": HOME + "/Projects/archive",
         "offloaded_at": now - 30 * DAY, "used_at": now - 500 * DAY, "restorable": True},
        {"id": "studio-data", "name": "Example Studio data", "kind": "app_data", "bytes": 12 * GB, "group": "Example Studio",
         "origin_path": HOME + "/Library/Application Support/Example Studio", "restorable": True},
        {"id": "sessions", "name": "Session archives", "kind": "session", "bytes": 8 * GB, "restorable": False},
    ]


class State:
    def __init__(self):
        self.reset()

    def reset(self):
        self.tree = _fixture()
        self.asked = 0
        self.trash = {}  # trash_id -> (path, node, where)
        self.trash_n = 0
        self.installed = False
        self.inventory = _inventory()
        self.jobs = {}
        self.runs = []

    def free(self):
        total, used = 1000 * GB, 930 * GB
        gone = sum(n["bytes"] for _, n, _ in self.trash.values() if n.get("purged"))
        offloaded = sum(i["bytes"] for i in self.inventory if i.get("moved"))
        return total - used + gone + offloaded

    def overview(self):
        t = self.tree
        free = self.free()
        groups = [_group("apps", "apps", t["apps"]), _group("home", "personal", t["home"]), _group("appdata", "appdata", t["appdata"])]
        groups += t["caches"]
        groups.append(_group("system", "system", t["system"]))
        groups.append({"id": "group:free", "name": "", "key": "free", "kind": "free", "category": "free", "bytes": free, "has_children": False})
        scanning = self.asked == 0
        self.asked += 1
        if scanning:  # the first answer has what the walk measured so far
            groups = [g for g in groups if g.get("key") != "system"]
        total = 1000 * GB
        return {"machine_id": MACHINE, "at": _now(), "os": "darwin", "path": "", "state": "scanning" if scanning else "ready",
                "partial": False, "cached": not scanning, "scanned_at": _now(), "duration_ms": 1480, "home": HOME,
                "volume": {"mount": "/", "total_bytes": total, "used_bytes": total - free, "free_bytes": free},
                "node": {"id": "group:root", "name": "", "kind": "root", "category": "other", "bytes": total, "has_children": True,
                         "children": groups}}

    def find(self, path, nodes=None):
        """(node, the list holding it) for a path anywhere in the fixture."""
        lists = [nodes] if nodes is not None else list(self.tree.values())
        for lst in lists:
            for n in lst:
                if n.get("path") == path:
                    return n, lst
                if n.get("children"):
                    hit = self.find(path, n["children"])
                    if hit:
                        return hit
        return None

    def folder(self, path):
        hit = self.find(path)
        if hit:
            node = copy.deepcopy(hit[0])
            if not node.get("children") and node["kind"] == "dir":  # a folder never opened: a few made-up files
                node["children"] = [_dir("%s/file-%d.bin" % (path.replace(HOME + "/", "", 1), i), node["bytes"] / GB / 4,
                                         file=True, ago_days=40 * i) for i in range(1, 4)]
                for c in node["children"]:
                    c["id"] = c["path"] = path + "/" + c["name"]
        else:
            node = {"id": path, "name": path.rsplit("/", 1)[-1], "path": path, "kind": "dir", "category": "personal",
                    "bytes": 0, "has_children": False, "can_trash": False, "can_offload": False, "refusal": "not_found"}
        return {"machine_id": MACHINE, "at": _now(), "os": "darwin", "path": path, "state": "ready", "partial": False,
                "cached": True, "scanned_at": _now(), "duration_ms": 120, "home": HOME, "node": node}


STATE = State()


def _usage(h, req):
    if MODE == "old":
        return h.send_json({"error": "not found"}, 404)
    path = (req.query.get("path") or [""])[0]
    if path in ("~", "~/"):
        path = HOME
    elif path.startswith("~/"):
        path = HOME + path[1:]
    time.sleep(0.2)
    h.send_json(STATE.overview() if not path else STATE.folder(path))


def _refusal(node):
    return None if node.get("can_trash") else node.get("refusal", "not_found")


def _trash(h, req):
    body = req.json()
    paths = body.get("paths") or []
    if not body.get("dry_run") and not body.get("confirm"):
        return h.send_json({"error": "Confirm first.", "code": "confirmation_required"}, 400)
    items = []
    for p in paths:
        hit = STATE.find(p)
        if not hit:
            items.append({"path": p, "display": p, "bytes": 0, "status": "missing"})
            continue
        node, where = hit
        why = _refusal(node)
        item = {"path": p, "display": node.get("display", p), "bytes": node["bytes"]}
        if why:
            items.append(dict(item, status="refused", refusal=why))
        elif body.get("dry_run"):
            items.append(dict(item, status="preview"))
        else:
            STATE.trash_n += 1
            tid = "t-%d" % STATE.trash_n
            where.remove(node)
            STATE.trash[tid] = (p, node, where)
            items.append(dict(item, status="trashed", trash_id=tid))
    free = STATE.free()
    h.send_json({"machine_id": req.match["mid"], "dry_run": bool(body.get("dry_run")), "freed_bytes": 0,
                 "pending_bytes": sum(i["bytes"] for i in items if i["status"] == "trashed"),
                 "available_before": free, "available_after": free, "items": items})


def _untrash(h, req):
    restored = []
    for tid in req.json().get("trash_ids") or []:
        if tid in STATE.trash and not STATE.trash[tid][1].get("purged"):
            path, node, where = STATE.trash.pop(tid)
            where.append(node)
            restored.append({"path": path})
    h.send_json({"restored": restored})


def _purge(h, req):
    body = req.json()
    if not body.get("confirm"):
        return h.send_json({"error": "Confirm first.", "code": "confirmation_required"}, 400)
    before = STATE.free()
    items, freed = [], 0
    for tid in body.get("trash_ids") or []:
        entry = STATE.trash.get(tid)
        if not entry:
            items.append({"trash_id": tid, "status": "missing"})
            continue
        entry[1]["purged"] = True
        freed += entry[1]["bytes"]
        items.append({"trash_id": tid, "status": "purged"})
    h.send_json({"freed_bytes": freed, "available_before": before, "available_after": STATE.free(), "items": items})


def _cleanup(h, req):
    ids = req.json().get("ids") or []
    freed, items = 0, []
    for c in list(STATE.tree["caches"]):
        if c.get("cache_id") in ids:
            freed += c["bytes"]
            items.append({"id": c["cache_id"], "bytes": c["bytes"], "status": "cleaned"})
            STATE.tree["caches"].remove(c)
    h.send_json({"freed_bytes": freed, "available_before": STATE.free(), "available_after": STATE.free() + freed, "items": items})


def _providers(h, req):
    h.send_json({"providers": [PROVIDER]})


def _inventory_route(h, req):
    items = [i for i in STATE.inventory if not i.get("pending")]
    h.send_json({"state": "ready", "at": _now(), "tool": dict(TOOL, installed=STATE.installed),
                 "total_bytes": sum(i["bytes"] for i in items), "items": [{k: v for k, v in i.items() if k != "moved"} for i in items]})


def _run(h, req):
    body = req.json()
    STATE.runs.append(body)
    if not body.get("dry_run") and not body.get("confirm"):
        return h.send_json({"error": "Confirm first.", "code": "confirmation_required"}, 400)
    paths, ids = body.get("paths") or [], body.get("items") or []
    if len(paths) + len(ids) > 50:
        return h.send_json({"error": "At most 50 items a run.", "code": "bad_request"}, 400)
    if body.get("action") == "offload":
        refused = [{"path": p, "reason": _refusal(STATE.find(p)[0]) if STATE.find(p) and not STATE.find(p)[0].get("can_offload") else ""}
                   for p in paths]
        refused = [r for r in refused if r["reason"] or not STATE.find(r["path"])]
        if refused:
            return h.send_json({"error": "Some items can't be offloaded.", "code": "offload_refused",
                                "items": [dict(r, reason=r["reason"] or "not_found") for r in refused]}, 422)
    if body.get("dry_run"):
        return h.send_json({"status": "ok", "tool": dict(TOOL, installed=STATE.installed),
                            "items": [{"path": p, "ok": True, "bytes": STATE.find(p)[0]["bytes"] if STATE.find(p) else 0} for p in paths]})
    if not STATE.installed and not body.get("install") and body.get("action") == "offload":
        return h.send_json({"error": "Example Offload Tool isn't on this computer yet.", "code": "tool_install_required",
                            "tool": TOOL}, 409)
    job = "job-%d" % (len(STATE.jobs) + 1)
    phases = (["install"] if body.get("install") and not STATE.installed else []) + \
        (["check", "upload", "verify", "cleanup"] if body.get("action") == "offload" else ["check", "download", "verify"])
    total = sum(STATE.find(p)[0]["bytes"] for p in paths if STATE.find(p)) + \
        sum(i["bytes"] for i in STATE.inventory if i["id"] in ids)
    STATE.jobs[job] = {"started": time.time(), "phases": phases, "total": total, "action": body.get("action"),
                       "paths": paths, "ids": ids, "canceled": False, "done": False}
    if body.get("install"):
        STATE.installed = True
    h.send_json({"status": "started", "job": job}, 202)


def _finish(j):
    if j["done"]:
        return
    j["done"] = True
    if j["action"] == "offload":
        for p in j["paths"]:
            hit = STATE.find(p)
            if not hit:
                continue
            node, where = hit
            where.remove(node)
            STATE.inventory.insert(0, {"id": "off-%d" % len(STATE.inventory), "name": node["name"], "kind": node["kind"] if node["kind"] in ("file", "dir") else "app",
                                       "bytes": node["bytes"], "origin_path": p, "offloaded_at": _now(), "used_at": node.get("used_at"),
                                       "restorable": True, "moved": True})
    else:
        STATE.inventory = [i for i in STATE.inventory if i["id"] not in j["ids"]]


def _job(h, req):
    j = STATE.jobs.get(req.match["job"])
    if not j:
        return h.send_json({"error": "No such job.", "code": "not_found"}, 404)
    if j["canceled"]:
        return h.send_json({"state": "canceled", "done_bytes": 0, "total_bytes": j["total"], "items": []})
    step = int(time.time() - j["started"])
    if step >= len(j["phases"]):
        _finish(j)
        return h.send_json({"state": "done", "phase": j["phases"][-1], "done_bytes": j["total"], "total_bytes": j["total"],
                            "freed_bytes": j["total"] if j["action"] == "offload" else 0, "items": []})
    done = int(j["total"] * step / len(j["phases"]))
    h.send_json({"state": "running", "phase": j["phases"][step], "done_bytes": done, "total_bytes": j["total"], "items": []})


def _cancel(h, req):
    j = STATE.jobs.get(req.match["job"])
    if j and not j["done"]:
        j["canceled"] = True
    h.send_json({})


def _show(h, req):
    if req.query.get("reset"):
        STATE.reset()
    h.send_json({"asked": STATE.asked, "trash": sorted(STATE.trash), "installed": STATE.installed, "runs": STATE.runs,
                 "inventory": [i["id"] for i in STATE.inventory]})


def register(d):
    d.help("MOCK_USAGE_MAP=1|old  the Mac Studio's disk 93 % full and its disk usage map: scan, trash, cleanup, an offload "
           "provider with an install step (#14576); old: a hub without the map")
    if not MODE:
        return
    # The Mac Studio reads 93 % full: Disk almost full on its card, the guidance banner in its details.
    alerts = d.hub.MACHINE_ALERT_METRICS
    if MACHINE not in alerts:
        alerts[MACHINE] = {"cpu": 18.0, "mem": 0.55, "disk": 0.93}
    mid = r"/api/v1/machines/(?P<mid>[^/]+)"
    d.route("GET", mid + r"/storage/usage", _usage)
    if MODE == "old":
        return
    d.route("POST", mid + r"/storage/trash", _trash)
    d.route("POST", mid + r"/storage/trash/undo", _untrash)
    d.route("POST", mid + r"/storage/trash/purge", _purge)
    if os.environ.get("MOCK_MACHINE_ALERTS") != "1":  # machine_alerts.py has its own cleanup
        d.route("POST", mid + r"/cleanup", _cleanup)
    d.route("GET", r"/api/v1/extensions/-/offload", _providers)
    base = r"/api/v1/extensions/example-offload/-/offload/cloud"
    d.route("GET", base + r"/inventory", _inventory_route)
    d.route("POST", base + r"/run", _run)
    d.route("GET", base + r"/jobs/(?P<job>[^/]+)", _job)
    d.route("DELETE", base + r"/jobs/(?P<job>[^/]+)", _cancel)
    d.route("GET", r"/mock/usage_map", _show)
    d.on_reset(STATE.reset)
