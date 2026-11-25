"""Dashboards (#15017): pages the assistant keeps up to date, under the reserved extension id `-pages`, a domain of
scripts/mock/mock_domains.py. Built against the phase 1 contract while the hub side is written; the clients draw and
test against it.

MOCK_DASHBOARDS=<mode> announces the hub feature `assistant_pages` and serves:
  - the switch: top-level boolean `dashboards` in GET / PUT /api/v1/assistant (a read-only device gets 403; turning it
    on or off pushes `assistant_pages`);
  - the places: after the extensions' own, GET /api/v1/extensions/-/ui (and the older /api/v1/plugins/-/ui) lists
    {extension: "-pages", id: "index", title: "Dashboards", placement: "nav_page", icon: "chart"} while a page is kept
    or one waits in Recently removed, and one nav_page per pinned page (at most 3), only with the switch on and for no
    placement, nav_page or a page slot; never a badge;
  - the pages: POST /api/v1/extensions/-pages/-/ui/{index|pg_…} answers a View (403 for a read-only device, 404 with
    the switch off or for an unknown page): the index (Pinned, All, Recently removed with Restore) and each page, whose
    View `title` is the page's title, with a footer (when it was updated, the last tap's state, Pin / Rename / Remove);
  - the server's actions (persona.pin, persona.unpin, persona.rename, persona.remove, persona.restore:<id>,
    persona.delete:<id>) push `assistant_pages` and `extension_ui {extension: "-pages", id, invalidate: true}`; a pin
    also puts the page's sidebar key (and p:-pages/index) in the navigation record's `shown` and pushes `navigation`;
  - a tap on a page's own button (Morning run's Done today / Skip today) is recorded and answers toast "Received"
    and a footer line "Received"; GET /mock/dashboards?advance=1 moves it on to "Delivered to Persona", "Persona is
    working", then the conversation's update (the streak and today's bar), pushing extension_ui {invalidate} each step.
Without MOCK_EXTENSIONS=1 the domain also announces extension_ui_slots and extension_ui_v3 and answers the places list
itself (only `-pages`), as the hub does with no extension installed.

Modes: 1 (switch on; Morning run pinned, Reading list, Weekend trip, Grocery list in Recently removed), off (the same
pages kept before the switch went off: nothing listed, the pages 404), empty (switch on, nothing kept: nothing
listed), removed (switch on, only Recently removed: the index alone), pinned (switch on, all three pinned, Grocery
list in Recently removed).
GET /mock/dashboards answers the state and the taps; ?mode=<mode> sets it up again (pushing assistant_pages);
?advance=1 moves the last tap on. Words in en, zh-Hans and ja by Accept-Language. All names are made up.
scripts/mock/test_dashboards_mock.py drives it.
"""
import os
import re
from urllib.parse import parse_qs, urlparse

HELP = (
    "MOCK_DASHBOARDS=1|off|empty|removed|pinned  Dashboards (#15017): feature assistant_pages, the `dashboards` switch "
    "in /api/v1/assistant, the -pages places in GET /api/v1/extensions/-/ui and their pages (index, a running habit "
    "whose Done today answers Received); off: switch off; empty: on, nothing kept; removed: only Recently removed; "
    "pinned: three pinned pages. GET /mock/dashboards[?mode=…|?advance=1]",
)
MODE = os.environ.get("MOCK_DASHBOARDS", "")
MODES = ("1", "off", "empty", "removed", "pinned")

EXT = "-pages"
INDEX = "index"
PAGE_ID = re.compile(r"pg_[0-9a-f]{16}")
MAX_PINNED = 3
NAV_INDEX = "p:-pages/index"

RUN, READING, TRIP, GROCERY = "pg_3f9a1c0d5e7b2a41", "pg_8c2e4b6d1a0f9e37", "pg_5d7f1e3a9c4b2086", "pg_b1a4c7e2d9f03658"

# The hub's words (server/internal/i18n/locales/*/assistant-pages.json once it exists), by request language.
WORDS = {
    "en": {
        "dashboards": "Dashboards", "pinned": "Pinned", "all": "All", "removed": "Recently removed",
        "updated_today": "Updated today {t}", "updated_yesterday": "Updated yesterday {t}", "updated_on": "Updated {d}",
        "removed_on": "Removed {d} · restorable for 30 days",
        "pin": "Pin to sidebar", "unpin": "Unpin from sidebar", "rename": "Rename", "remove": "Remove", "restore": "Restore",
        "delete": "Delete now", "save": "Save", "title_field": "Title",
        "remove_confirm": "Remove this page? You can restore it from Dashboards for 30 days.",
        "delete_confirm": "Delete this page now? This can't be undone.",
        "received": "Received", "delivered": "Delivered to Persona", "working": "Persona is working",
        "pinned_toast": "Pinned to the sidebar", "unpinned_toast": "Unpinned", "renamed": "Renamed", "removed_toast": "Removed",
        "restored": "Restored", "deleted": "Deleted", "pin_limit": "You can pin up to 3 pages",
        "bad_title": "A title is 1–40 characters on one line",
        "gone_title": "This page was removed", "gone_detail": "You can restore it from Dashboards for 30 days.",
        "empty_title": "No dashboards yet",
        "empty_detail": "Ask Persona for a page you come back to, like “Make me a page to track my morning runs.”",
        "streak": "Current streak", "days": "{n} days", "best": "Longest {n} days",
        "minutes": "Minutes run, last 30 days", "series": "Minutes", "done": "Done today", "skip": "Skip today",
        "goal": "Goal", "goal_value": "5 runs a week", "this_week": "This week", "this_week_value": "{n} of 5",
        "run": "Morning run", "reading": "Reading list", "trip": "Weekend trip", "grocery": "Grocery list",
        "books": (("The Quiet Orchard", "Chapter 9 of 14"), ("Notes on Tidal Maps", "Not started"), ("A Year of Small Gardens", "Finished")),
        "trip_rows": (("Saturday 9:10", "Train to the coast"), ("Saturday 14:00", "Check in at the guesthouse"), ("Sunday 10:30", "Harbor walk")),
        "now": "8:15",
    },
    "zh-Hans": {
        "dashboards": "仪表盘", "pinned": "已固定", "all": "全部", "removed": "最近移除",
        "updated_today": "今天 {t} 更新", "updated_yesterday": "昨天 {t} 更新", "updated_on": "{d}更新",
        "removed_on": "{d}移除 · 30 天内可恢复",
        "pin": "固定到侧边栏", "unpin": "从侧边栏取消固定", "rename": "改名", "remove": "移除", "restore": "恢复",
        "delete": "立即删除", "save": "保存", "title_field": "标题",
        "remove_confirm": "移除这个页面？30 天内可以在仪表盘里恢复。",
        "delete_confirm": "现在删除这个页面？删除后无法恢复。",
        "received": "已收到", "delivered": "已送到 Persona", "working": "Persona 处理中",
        "pinned_toast": "已固定到侧边栏", "unpinned_toast": "已取消固定", "renamed": "已改名", "removed_toast": "已移除",
        "restored": "已恢复", "deleted": "已删除", "pin_limit": "最多固定 3 个页面",
        "bad_title": "标题 1–40 个字，只能一行",
        "gone_title": "这个页面已移除", "gone_detail": "30 天内可以在仪表盘里恢复。",
        "empty_title": "还没有仪表盘",
        "empty_detail": "让 Persona 做一个你会常回来看的页面，比如「帮我做个晨跑打卡页」。",
        "streak": "连续天数", "days": "{n} 天", "best": "最长 {n} 天",
        "minutes": "最近 30 天跑步分钟数", "series": "分钟", "done": "今天完成", "skip": "今天跳过",
        "goal": "目标", "goal_value": "每周跑 5 次", "this_week": "本周", "this_week_value": "5 次中的 {n} 次",
        "run": "晨跑打卡", "reading": "阅读清单", "trip": "周末出行", "grocery": "购物清单",
        "books": (("安静的果园", "第 9 章，共 14 章"), ("潮汐地图笔记", "还没开始"), ("小花园的一年", "已读完")),
        "trip_rows": (("周六 9:10", "坐火车去海边"), ("周六 14:00", "入住民宿"), ("周日 10:30", "港口散步")),
        "now": "8:15",
    },
    "ja": {
        "dashboards": "ダッシュボード", "pinned": "固定済み", "all": "すべて", "removed": "最近削除した項目",
        "updated_today": "今日 {t} に更新", "updated_yesterday": "昨日 {t} に更新", "updated_on": "{d}に更新",
        "removed_on": "{d}に削除 · 30 日間は復元できます",
        "pin": "サイドバーに固定", "unpin": "サイドバーの固定を解除", "rename": "名前を変更", "remove": "削除", "restore": "復元",
        "delete": "今すぐ完全に削除", "save": "保存", "title_field": "タイトル",
        "remove_confirm": "このページを削除しますか？30 日間はダッシュボードから復元できます。",
        "delete_confirm": "このページを今すぐ完全に削除しますか？元に戻せません。",
        "received": "受け取りました", "delivered": "Persona に届きました", "working": "Persona が対応中",
        "pinned_toast": "サイドバーに固定しました", "unpinned_toast": "固定を解除しました", "renamed": "名前を変更しました",
        "removed_toast": "削除しました", "restored": "復元しました", "deleted": "完全に削除しました",
        "pin_limit": "固定できるのは 3 ページまでです", "bad_title": "タイトルは 1 行で 1〜40 文字です",
        "gone_title": "このページは削除されました", "gone_detail": "30 日間はダッシュボードから復元できます。",
        "empty_title": "ダッシュボードはまだありません",
        "empty_detail": "何度も見返すページを Persona に頼んでみましょう。例：「朝ランの記録ページを作って」",
        "streak": "連続日数", "days": "{n} 日", "best": "最長 {n} 日",
        "minutes": "過去 30 日のラン時間（分）", "series": "分", "done": "今日は完了", "skip": "今日は休む",
        "goal": "目標", "goal_value": "週 5 回走る", "this_week": "今週", "this_week_value": "5 回中 {n} 回",
        "run": "朝ラン", "reading": "読書リスト", "trip": "週末の旅行", "grocery": "買い物リスト",
        "books": (("静かな果樹園", "14 章中 9 章"), ("潮の地図ノート", "未読"), ("小さな庭の一年", "読了")),
        "trip_rows": (("土曜 9:10", "海辺へ電車で移動"), ("土曜 14:00", "ゲストハウスにチェックイン"), ("日曜 10:30", "港を散歩")),
        "now": "8:15",
    },
}

for _lang, _words in WORDS.items():
    _words["lang"] = _lang

# Thirty days of minutes run, the oldest first (0: no run that day); today is the last and still open.
_MINUTES = [32, 0, 28, 35, 30, 0, 41, 33, 29, 0, 36, 38, 0, 31, 34, 30, 27, 0, 40, 35, 33, 29, 31, 36, 30, 34, 32, 37, 35, 0]
_DAYS = [(10, d) for d in range(26, 32)] + [(11, d) for d in range(1, 25)]  # (month, day), the last one today


def _date(lang, month_day):
    """A month and day the way `lang` writes it: Nov 20, 11月20日."""
    month, day = month_day
    return "%s %d" % (("Oct", "Nov")[month - 10], day) if lang == "en" else "%d月%d日" % (month, day)

STATE = {}


def _page(pid, key, icon, updated, pinned=False, removed=None):
    return {"id": pid, "key": key, "title": None, "icon": icon, "updated": updated, "pinned": pinned, "removed": removed,
            "status": None}


def seed(mode):
    """The fixture of `mode` (MODES), afresh."""
    pages = {}
    if mode in ("1", "off", "pinned"):
        pages[RUN] = _page(RUN, "run", "heart", ("today", "8:02"), pinned=True)
        pages[READING] = _page(READING, "reading", "list", ("yesterday", "21:40"), pinned=mode == "pinned")
        pages[TRIP] = _page(TRIP, "trip", "location", ("on", (11, 20)), pinned=mode == "pinned")
    if mode in ("1", "off", "removed", "pinned"):
        pages[GROCERY] = _page(GROCERY, "grocery", "list", ("on", (11, 19)), removed=(11, 21))
    STATE.clear()
    STATE.update(mode=mode, on=mode != "off", pages=pages, minutes=list(_MINUTES), streak=12, week=3, taps=[], renders=[])


def _lang(h):
    first = (h.headers.get("Accept-Language") or "").split(",")[0].strip().lower()
    return "en" if first.startswith("en") else "ja" if first.startswith("ja") else "zh-Hans"


def _title(p, w):
    return p["title"] or w[p["key"]]


def _updated(p, w):
    kind, at = p["updated"]
    return w["updated_" + kind].format(t=at, d=_date(w["lang"], at) if kind == "on" else at)


def _kept():
    return [p for p in STATE["pages"].values() if not p["removed"]]


def _removed():
    return [p for p in STATE["pages"].values() if p["removed"]]


def places(lang, new_keys=True):
    """The -pages places the default manager appends to the list (contract: index, then up to 3 pinned pages)."""
    if not STATE["on"] or not (_kept() or _removed()):
        return []
    w = WORDS[lang]
    ext, name = ("extension", "extension_name") if new_keys else ("plugin", "plugin_name")
    out = [{ext: EXT, name: "Persona", "id": INDEX, "title": w["dashboards"], "placement": "nav_page", "size": "full",
            "icon": "chart"}]
    for p in [p for p in _kept() if p["pinned"]][:MAX_PINNED]:
        out.append({ext: EXT, name: "Persona", "id": p["id"], "title": _title(p, w), "placement": "nav_page",
                    "size": "full", "icon": p["icon"]})
    return out


# ---- the views ----

def _row(p, w):
    if p["removed"]:
        return {"type": "row", "key": "removed:" + p["id"], "title": _title(p, w), "icon": p["icon"],
                "subtitle": w["removed_on"].format(d=_date(w["lang"], p["removed"])),
                "actions": [{"title": w["restore"], "icon": "refresh", "swipe": True,
                             "action": {"id": "persona.restore:" + p["id"]}},
                            {"title": w["delete"], "icon": "trash", "role": "destructive",
                             "action": {"id": "persona.delete:" + p["id"], "confirm": w["delete_confirm"]}}]}
    pin = {"title": w["unpin"] if p["pinned"] else w["pin"], "icon": "star", "swipe": True,
           "action": {"id": "persona.unpin" if p["pinned"] else "persona.pin", "values": {"page": p["id"]}}}
    remove = {"title": w["remove"], "icon": "trash", "role": "destructive", "swipe": True,
              "action": {"id": "persona.remove", "values": {"page": p["id"]}, "confirm": w["remove_confirm"]}}
    return {"type": "row", "key": "page:" + p["id"], "title": _title(p, w), "subtitle": _updated(p, w), "icon": p["icon"],
            "action": {"id": "open", "push": {"ui": p["id"]}}, "actions": [pin, remove]}


def index_view(lang):
    w = WORDS[lang]
    kept, removed = _kept(), _removed()
    sections = []
    pinned = [p for p in kept if p["pinned"]]
    rest = [p for p in kept if not p["pinned"]]
    if pinned:
        sections.append({"key": "pinned", "title": w["pinned"], "items": [_row(p, w) for p in pinned]})
    if rest:
        sections.append({"key": "all", "title": w["all"], "items": [_row(p, w) for p in rest]})
    if not kept:
        sections.append({"key": "empty", "items": [{"type": "empty", "title": w["empty_title"], "detail": w["empty_detail"],
                                                     "icon": "chart"}]})
    if removed:
        sections.append({"key": "removed", "title": w["removed"], "items": [_row(p, w) for p in removed]})
    return {"title": w["dashboards"], "sections": sections}


def _footer(p, w):
    items = [{"type": "text", "key": "persona.updated", "text": _updated(p, w), "style": "caption"}]
    if p["status"]:
        items.append({"type": "text", "key": "persona.status", "text": w[p["status"]], "style": "caption"})
    items += [
        {"type": "button", "key": "persona.pin", "title": w["unpin"] if p["pinned"] else w["pin"],
         "action": {"id": "persona.unpin" if p["pinned"] else "persona.pin"}},
        {"type": "button", "key": "persona.rename", "title": w["rename"],
         "action": {"id": "persona.rename", "push": {"ui": p["id"], "state": "rename", "title": w["rename"]}}},
        {"type": "button", "key": "persona.remove", "title": w["remove"], "role": "destructive",
         "action": {"id": "persona.remove", "confirm": w["remove_confirm"]}},
    ]
    return {"key": "persona.footer", "items": items}


def _body(p, w):
    if p["key"] == "run":
        minutes = STATE["minutes"]
        best = 21
        return [{"key": "streak", "items": [
            {"type": "metric", "key": "streak", "label": w["streak"], "value": w["days"].format(n=STATE["streak"]),
             "detail": w["best"].format(n=best), "tone": "positive", "icon": "heart"},
            {"type": "stat_grid", "key": "week", "stats": [
                {"label": w["this_week"], "value": w["this_week_value"].format(n=STATE["week"])},
                {"label": w["goal"], "value": w["goal_value"]}]},
            {"type": "chart", "key": "minutes", "kind": "bar", "title": w["minutes"], "unit": w["series"],
             "series": [{"name": w["series"], "tone": "positive",
                         "points": [{"label": d, "value": m} for d, m in zip([_date(w["lang"], x) for x in _DAYS], minutes)]}]},
            {"type": "button", "key": "done", "title": w["done"], "action": {"id": "done_today"}},
            {"type": "button", "key": "skip", "title": w["skip"], "action": {"id": "skip_today"}},
        ]}]
    if p["key"] == "reading":
        return [{"key": "books", "items": [{"type": "row", "key": "book:%d" % i, "title": t, "subtitle": s, "icon": "doc"}
                                           for i, (t, s) in enumerate(w["books"])]}]
    if p["key"] == "trip":
        return [{"key": "plan", "items": [{"type": "row", "key": "stop:%d" % i, "title": s, "subtitle": t, "icon": "location"}
                                          for i, (t, s) in enumerate(w["trip_rows"])]}]
    return [{"key": "items", "items": [{"type": "text", "text": "—"}]}]


def page_view(p, lang, state=""):
    w = WORDS[lang]
    if p["removed"]:
        return {"title": _title(p, w), "sections": [{"key": "gone", "items": [
            {"type": "empty", "title": w["gone_title"], "detail": w["gone_detail"], "icon": p["icon"],
             "button": {"title": w["restore"], "action": {"id": "persona.restore:" + p["id"]}}}]}]}
    if state == "rename":
        return {"title": w["rename"], "sections": [{"key": "rename", "items": [
            {"type": "field", "key": "title", "name": "title", "label": w["title_field"], "kind": "text", "value": _title(p, w)},
            {"type": "button", "key": "save", "title": w["save"], "action": {"id": "persona.rename", "submit": True}}]}]}
    return {"title": _title(p, w), "sections": _body(p, w) + [_footer(p, w)]}


# ---- what changes, and the events it sends ----

def _send(hub, msg):
    hub.broadcast(msg)


def _changed(hub, *ids):
    """The places changed (the list) and these pages' content."""
    _send(hub, {"type": "assistant_pages"})
    for ui in (INDEX,) + ids:
        _send(hub, {"type": "extension_ui", "extension": EXT, "id": ui, "invalidate": True})


def _shown(hub, *keys):
    """The hub puts the sidebar keys in the navigation record's `shown` (first Keep, a pin), and says so."""
    nav = hub.NAVIGATION
    pages = dict(nav.get("pages") or {})
    shown = list(pages.get("shown") or [])
    added = [k for k in keys if k not in shown]
    if not added:
        return
    pages.update(shown=shown + added, order=list(pages.get("order") or []), hidden=[k for k in pages.get("hidden") or [] if k not in added])
    nav["pages"] = pages
    _send(hub, {"type": "navigation", "pages": pages})


def _act(hub, ui, action, values, lang):
    """A server action (persona.*) on page `ui` (or, from the index, values.page): (result, status)."""
    w = WORDS[lang]
    target = values.get("page") or ui
    if action.startswith(("persona.restore:", "persona.delete:")):
        target = action.split(":", 1)[1]
        action = action.split(":", 1)[0]
    p = STATE["pages"].get(target)
    if p is None:
        return {"error": "no such page", "code": "not_found"}, 404
    toast = None
    if action == "persona.pin":
        if not p["pinned"]:
            if sum(1 for x in _kept() if x["pinned"]) >= MAX_PINNED:
                toast = w["pin_limit"]
            else:
                p["pinned"] = True
                toast = w["pinned_toast"]
                _shown(hub, NAV_INDEX, "p:%s/%s" % (EXT, p["id"]))
                _changed(hub, p["id"])
    elif action == "persona.unpin":
        if p["pinned"]:
            p["pinned"] = False
            _changed(hub, p["id"])
        toast = w["unpinned_toast"]
    elif action == "persona.rename":
        title = (values.get("title") or "").strip()
        if not 1 <= len(title) <= 40 or any(c in title for c in "\r\n\t"):
            return {"toast": w["bad_title"]}, 200
        p["title"] = title
        _changed(hub, p["id"])
        return {"pop": True, "toast": w["renamed"]}, 200
    elif action == "persona.remove":
        p.update(removed=_DAYS[-1], pinned=False, status=None)
        _changed(hub, p["id"])
        toast = w["removed_toast"]
    elif action == "persona.restore":
        if p["removed"]:
            p["removed"] = None
            _changed(hub, p["id"])
        toast = w["restored"]
    elif action == "persona.delete":
        STATE["pages"].pop(p["id"])
        _changed(hub, p["id"])
        return {"view": index_view(lang), "toast": w["deleted"]}, 200
    else:
        return {"error": "no such action", "code": "not_found"}, 404
    view = index_view(lang) if ui == INDEX else page_view(p, lang)
    return {"view": view, **({"toast": toast} if toast else {})}, 200


def _tap(hub, p, action, values, lang):
    """A tap on the page's own button: recorded for its conversation, answered "Received" at once."""
    STATE["taps"].append({"page": p["id"], "action": action, "values": dict(values),
                          "tag": '<persona-action view="%s/%s" action="%s">' % (EXT, p["id"], action)})
    p["status"] = "received"
    return {"view": page_view(p, lang), "toast": WORDS[lang]["received"]}, 200


def advance(hub):
    """The last tap, a step further: delivered → working → the conversation updated the page (its status cleared)."""
    tap = STATE["taps"][-1] if STATE["taps"] else None
    p = STATE["pages"].get(tap["page"]) if tap else None
    if p is None or not p["status"]:
        return None
    p["status"] = {"received": "delivered", "delivered": "working"}.get(p["status"])
    if p["status"] is None:
        if p["id"] == RUN and tap["action"] == "done_today" and not STATE["minutes"][-1]:
            STATE["minutes"][-1] = 34
            STATE["streak"] += 1
            STATE["week"] += 1
        p["updated"] = ("today", WORDS["en"]["now"])
    _send(hub, {"type": "extension_ui", "extension": EXT, "id": p["id"], "invalidate": True})
    return p["status"]


# ---- the routes ----

def _new_keys(path):
    return path.startswith("/api/v1/extensions")


def list_places(d, h, req):
    hub = d.hub
    placement = (req.query.get("placement") or [""])[0]
    new = _new_keys(req.path)
    mine = places(_lang(h), new) if placement in ("", "nav_page", "extension_page", "plugin_page") else []
    if not hub.PLUGINS_MODE:
        return h.send_json({"ui": mine})

    class Capture:  # the built-in list, with ours appended after it
        sent = None

        def __getattr__(self, name):
            return getattr(h, name)

        def send_json(self, obj, status=200, *args, **kwargs):
            Capture.sent = (obj, status)

    if not hub.plugins_route(Capture(), req.path, None) or Capture.sent is None:
        return False
    obj, status = Capture.sent
    if status == 200 and isinstance(obj, dict) and isinstance(obj.get("ui"), list):
        obj = dict(obj, ui=obj["ui"] + mine)
    return h.send_json(obj, status)


def render(d, h, req):
    hub = d.hub
    ui = req.match["ui"]
    if hub.is_read_only(h):
        return h.send_json({"error": "only a device with full access can open this page", "code": "forbidden"}, 403)
    if not STATE["on"]:
        return h.send_json({"error": "Dashboards are off", "code": "not_found"}, 404)
    try:
        body = req.json()
    except ValueError:
        return h.send_json({"error": "not JSON", "code": "bad_request"}, 400)
    lang = _lang(h)
    action = body.get("action") or ""
    values = body.get("values") if isinstance(body.get("values"), dict) else {}
    state = body.get("state") if isinstance(body.get("state"), str) else ""
    STATE["renders"].append({"ui": ui, "action": action, "ui_version": (body.get("context") or {}).get("ui_version")})
    if ui == INDEX:
        if not action:
            return h.send_json({"view": index_view(lang)})
        out, status = _act(hub, ui, action, values, lang)
        return h.send_json(out, status)
    p = STATE["pages"].get(ui)
    if p is None:
        return h.send_json({"error": "no such page", "code": "not_found"}, 404)
    if action == "open":
        return h.send_json({"view": page_view(p, lang, state)})
    if action.startswith("persona."):
        out, status = _act(hub, ui, action, values, lang)
        return h.send_json(out, status)
    if action and not p["removed"]:
        out, status = _tap(hub, p, action, values, lang)
        return h.send_json(out, status)
    return h.send_json({"view": page_view(p, lang, state)})


def put_assistant(d, h, req):
    hub = d.hub
    if hub.assistant_for_manager(h) is not None:
        return False  # another manager's settings: it has no dashboards
    try:
        body = req.json()
    except ValueError:
        return False
    if not isinstance(body, dict) or "dashboards" not in body:
        return False
    if type(body["dashboards"]) is not bool:
        return h.send_json({"error": "dashboards must be true or false", "code": "bad_request"}, 400)
    if hub.is_read_only(h):
        return h.send_json({"error": "only a device with full access can change this", "code": "forbidden"}, 403)
    if STATE["on"] != body["dashboards"]:
        STATE["on"] = body["dashboards"]
        _send(hub, {"type": "assistant_pages"})
    if set(body) - {"dashboards"}:
        return False  # the other fields as the hub's own PUT answers them (with ours in its answer)
    return h.send_json(hub.assistant_state())


def control(d, h, req):
    q = parse_qs(urlparse(h.path).query)
    mode = (q.get("mode") or [""])[0]
    if mode:
        if mode not in MODES:
            return h.send_json({"error": "mode is one of " + ", ".join(MODES)}, 400)
        seed(mode)
        _keep_sidebar_keys(d.hub)
        _changed(d.hub)
    if (q.get("advance") or [""])[0] == "1":
        advance(d.hub)
    return h.send_json({
        "mode": STATE["mode"], "dashboards": STATE["on"],
        "pages": [{"id": p["id"], "title": _title(p, WORDS["en"]), "pinned": p["pinned"], "removed": bool(p["removed"]),
                   "status": p["status"]}
                  for p in STATE["pages"].values()],
        "places": places("en"), "taps": STATE["taps"], "renders": STATE["renders"][-20:],
    })


def _wrap_assistant(hub):
    state, for_manager = hub.assistant_state, hub.assistant_for_manager
    if getattr(state, "_dashboards", False):
        return

    def assistant_state(space="", archived=False):
        out = state(space, archived)
        if not space:
            out["dashboards"] = STATE["on"]
        return out

    def assistant_for_manager(h):
        out = for_manager(h)
        if out is not None and isinstance(out[0], dict):
            out = ({k: v for k, v in out[0].items() if k != "dashboards"}, out[1])
        return out

    assistant_state._dashboards = True
    hub.assistant_state = assistant_state
    hub.assistant_for_manager = assistant_for_manager


def _keep_sidebar_keys(hub):
    """Pages kept (and pinned) before: the hub put their sidebar keys in the navigation record's `shown` then."""
    if STATE["mode"] not in ("1", "off", "pinned"):
        return
    nav = hub.NAVIGATION
    pages = dict(nav.get("pages") or {})
    shown = list(pages.get("shown") or [])
    keys = [NAV_INDEX] + ["p:%s/%s" % (EXT, p["id"]) for p in _kept() if p["pinned"]]
    pages.update(shown=shown + [k for k in keys if k not in shown], order=list(pages.get("order") or []),
                 hidden=list(pages.get("hidden") or []))
    nav["pages"] = pages


def register(d):
    d.help(*HELP)
    if not MODE:
        return
    if MODE not in MODES:
        raise SystemExit("MOCK_DASHBOARDS is one of " + ", ".join(MODES))
    seed(MODE)
    hub = d.hub
    d.feature("assistant_pages")
    if not hub.PLUGINS_MODE:
        # the hub always has UI slots; without MOCK_EXTENSIONS the mock says so only for the -pages places
        d.feature("extension_ui_slots", "extension_ui_v3")
    _keep_sidebar_keys(hub)
    _wrap_assistant(hub)
    d.route("GET", r"/api/v1/(?:extensions|plugins)/-/ui", lambda h, req: list_places(d, h, req))
    d.route("POST", r"/api/v1/(?:extensions|plugins)/-pages/-/ui/(?P<ui>index|pg_[0-9a-f]{16})",
            lambda h, req: render(d, h, req))
    d.route("POST", r"/api/v1/(?:extensions|plugins)/-pages/-/ui/(?P<ui>[^/]+)",
            lambda h, req: h.send_json({"error": "no such page", "code": "not_found"}, 404))
    d.route("PUT", r"/api/v1/assistant", lambda h, req: put_assistant(d, h, req))
    d.route("GET", r"/mock/dashboards", lambda h, req: control(d, h, req))
    d.on_reset(lambda: (seed(MODE), _keep_sidebar_keys(hub)))
