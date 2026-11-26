"""The names of the app's own places stay reserved for Dashboards page titles (#15017).

A Dashboards page sits in the sidebar beside the app's built-in places, so its title may not pass for one of them. The
hub keeps the reserved names in `server/internal/i18n/locales/<lang>/assistant-pages.json` as `reserved_*` keys. It
refuses an Agent's title that, folded (NFKC, case folding, trimmed), CONTAINS any reserved name in any of the three
languages, and the owner's rename only when it EQUALS one.

This suite reads the place names the clients themselves show (rows and tabs that open a built-in page in the web
sidebar, the iPhone / iPad drawer and sidebar, and the Android drawer and rail) from each client's catalogs, and checks
every one of them in English, Simplified Chinese and Japanese is reserved. A client that renames a place's text without
reserving the new words fails here, and so does a client that renames the catalog key the table names.

Left out on purpose, because of the contains rule: the short labels for tight spaces (web `*_short`), group headings
(Recent, Pinned, Pages, Persona chats), actions (New chat, Search), and extension pages, which are not built in.
"""
import json
from pathlib import Path
import re
import unicodedata
import unittest
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[2]
LANGUAGES = ("en", "zh-Hans", "ja")
THIS_FILE = "scripts/test/test_dashboards_reserved_names.py"

HUB_CATALOG = "server/internal/i18n/locales/{lang}/assistant-pages.json"
HUB_CATALOGS = "server/internal/i18n/locales/*/assistant-pages.json"
RESERVED_KEY = re.compile(r"^reserved_[a-z0-9_]+$")
# The hub reserves these with no client catalog behind them yet: the product's name and the Dashboards group itself.
HUB_ONLY_KEYS = ("reserved_persona", "reserved_dashboards")

WEB_LOCALES = "web/src/i18n/locales"
APPLE_CATALOG = "apple/Shared/Localization/Localizable.xcstrings"
ANDROID_ROOT = "android"
# Android resource folders per language; the default `values` is English.
ANDROID_FOLDERS = {
    "en": ("values",),
    "zh-Hans": ("values-zh", "values-zh-rCN", "values-b+zh+Hans"),
    "ja": ("values-ja", "values-ja-rJP"),
}

# Web: `<namespace>.<key>` in web/src/i18n/locales/<lang>/<namespace>.json.
WEB_PLACES = (
    "shellswitch.assistant",  # Assistant: the main conversation's side of the narrow screen's top switch
    "agentspage.title",  # Agents: the Agents page, the top switch's other side
    "sidebar.main_chat",  # Main chat: the sidebar's first row, the main conversation
    "sidebar.nav_home",  # Overview: the start page (Pages)
    "sidebar.nav_inbox",  # Inbox (Pages)
    "sidebar.nav_messages",  # Messages: the Lark module's chats page (Pages)
    "sidebar.nav_health",  # Health: the health module's page (Pages)
    "starred.title",  # Starred messages (Pages)
    "sidebar.nav_board",  # Workflow board, once turned on (Pages)
    "sidebar.nav_todos",  # To-dos (Pages)
    "sidebar.nav_routines",  # Scheduled tasks (Pages)
    "sidebar.nav_kb",  # Knowledge (Pages)
    "sidebar.nav_machines",  # Machines (Pages)
    "sidebar.nav_settings",  # Settings (Pages)
    "work-entry.title",  # History: the Agents' history page, the row under the Agents list
    "lib-state-labels.needs_you",  # Needs you: the Agents list's first group, the agents waiting on the owner
)

# Apple: keys of apple/Shared/Localization/Localizable.xcstrings.
APPLE_PLACES = (
    "Persona",  # Persona: the tab bar's assistant tab
    "Agents",  # Agents: the Agents tab, the top switch's Agents side and the Agents list's heading
    "Main Chat",  # Main chat: the sidebar's first row, the main conversation
    "Meetings",  # Meetings: the recordings page, first among the drawer's places (iPhone)
    "Inbox",  # Inbox: a drawer place and a tab
    "Starred",  # Starred messages: a drawer place (iPhone)
    "To-dos",  # To-dos: a drawer place
    "Scheduled Tasks",  # Scheduled tasks: a drawer place
    "Knowledge",  # Knowledge: a drawer place
    "Health",  # Health: a drawer place
    "Machines",  # Machines: a drawer place
    "Messages [Quote Views 4da199]",  # Messages: the Lark chats, a drawer place, a tab and a switch side
    "Settings",  # Settings: the sidebar foot's button and a tab
    "Needs You [State Names]",  # Needs you: the Agents list's section the sidebar's "N need you" jumps to
)

# Android: string resource names under android/**/src/main/res/values*/.
ANDROID_PLACES = (
    "app_tab_assistant",  # Assistant: the rail's and bottom bar's assistant destination
    "app_tab_agents",  # Agents: the rail's and bottom bar's Agents destination
    "agents_sidebar_heading",  # Agents: the Agents list's heading in the merged sidebar
    "assistant_main_conversation",  # Main conversation: the drawer's first row
    "assistant_origin_inbox",  # Inbox: a drawer place
    "assistant_todos",  # To-dos: a drawer place
    "assistant_info_schedule",  # Scheduled tasks: a drawer place
    "assistant_info_knowledge",  # Knowledge: a drawer place
    "assistant_health",  # Health: a drawer place
    "assistant_starred_title",  # Starred messages: a drawer place
    "inbox_lark_messages_title",  # Messages: the Lark chats, a drawer place
    "app_machines",  # Computers: a drawer place
    "assistant_open_settings",  # Settings: the drawer foot's button
    "designsystem_status_needs_you",  # Needs you: the Agents list's group and the sidebar's jump chip
)


def fold(text):
    """The hub's comparison form: NFKC, case folded, trimmed, NFKC again."""
    return unicodedata.normalize("NFKC", unicodedata.normalize("NFKC", text.strip()).casefold())


def read_json(path):
    with path.open(encoding="utf-8") as f:
        return json.load(f)


_hub = None


def hub_reserved():
    """{lang: {key: value}} of the hub catalogs' `reserved_*` entries."""
    global _hub
    if _hub is None:
        catalogs = {}
        for lang in LANGUAGES:
            path = ROOT / HUB_CATALOG.format(lang=lang)
            if not path.is_file():
                raise AssertionError(f"{path.relative_to(ROOT)} is missing: the hub's reserved names live there")
            catalogs[lang] = {k: v for k, v in read_json(path).items() if k.startswith("reserved_")}
        _hub = catalogs
    return _hub


def reserved_set():
    """Every reserved name of every language, folded: a title is checked against all of them."""
    return {fold(v) for names in hub_reserved().values() for v in names.values() if isinstance(v, str)}


def web_text(lang, key):
    namespace, name = key.split(".", 1)
    path = ROOT / WEB_LOCALES / lang / f"{namespace}.json"
    if not path.is_file():
        return []
    value = read_json(path).get(name)
    return [value] if isinstance(value, str) else []


_apple = None


def apple_text(lang, key):
    global _apple
    if _apple is None:
        _apple = read_json(ROOT / APPLE_CATALOG)
    entry = _apple.get("strings", {}).get(key)
    if entry is None:
        return []
    if entry.get("shouldTranslate") is False:
        return [key]
    unit = entry.get("localizations", {}).get(lang, {}).get("stringUnit")
    if unit and isinstance(unit.get("value"), str):
        return [unit["value"]]
    # English is the source language: a key with no English localization is its own English text.
    if lang == _apple.get("sourceLanguage", "en") and not unit:
        return [key]
    return []


ANDROID_ESCAPE = re.compile(r"\\(u[0-9a-fA-F]{4}|.)")


def android_unescape(raw):
    text = raw.strip()
    if len(text) >= 2 and text[0] == text[-1] == '"':
        text = text[1:-1]

    def one(m):
        c = m.group(1)
        if c.startswith("u") and len(c) == 5:
            return chr(int(c[1:], 16))
        return {"n": "\n", "t": "\t"}.get(c, c)

    return ANDROID_ESCAPE.sub(one, text)


_android = {}


def android_strings(lang):
    """{name: [text, ...]} of every module's string resources in `lang` (a name some modules share has several)."""
    if lang not in _android:
        found = {}
        for folder in ANDROID_FOLDERS[lang]:
            for path in sorted((ROOT / ANDROID_ROOT).glob(f"**/src/main/res/{folder}/*.xml")):
                if "build" in path.relative_to(ROOT).parts:
                    continue
                for el in ET.parse(path).getroot():
                    if el.tag == "string" and el.get("name"):
                        found.setdefault(el.get("name"), []).append(android_unescape("".join(el.itertext())))
        _android[lang] = found
    return _android[lang]


def android_text(lang, key):
    return android_strings(lang).get(key, [])


CLIENTS = (
    ("web", WEB_PLACES, web_text, f"{WEB_LOCALES}/<lang>/<namespace>.json"),
    ("Apple", APPLE_PLACES, apple_text, APPLE_CATALOG),
    ("Android", ANDROID_PLACES, android_text, f"{ANDROID_ROOT}/**/src/main/res/values*/"),
)


class HubCatalogTests(unittest.TestCase):
    longMessage = False  # the messages below say what to fix; the default appends whole sets

    def test_languages_share_the_reserved_keys(self):
        catalogs = hub_reserved()
        english = set(catalogs["en"])
        self.assertTrue(english, f"{HUB_CATALOGS} reserves no names")
        for lang in LANGUAGES[1:]:
            with self.subTest(lang=lang):
                self.assertEqual(set(catalogs[lang]), english,
                                 f"the {lang} reserved_* keys differ from English: added "
                                 f"{sorted(set(catalogs[lang]) - english)}, missing {sorted(english - set(catalogs[lang]))}")

    def test_reserved_entries_are_names(self):
        for lang, names in hub_reserved().items():
            for key, value in names.items():
                with self.subTest(lang=lang, key=key):
                    self.assertRegex(key, RESERVED_KEY, "a reserved key is reserved_<slug> (a-z, 0-9, underscore)")
                    self.assertIsInstance(value, str, f"{lang} {key} is not text")
                    self.assertTrue(fold(value), f"{lang} {key} is empty: it would reserve every title")
                    self.assertNotIn("{", value, f"{lang} {key} has a placeholder; a reserved name is plain text")

    def test_names_without_a_client_catalog(self):
        for key in HUB_ONLY_KEYS:
            for lang in LANGUAGES:
                with self.subTest(key=key, lang=lang):
                    self.assertIn(key, hub_reserved()[lang], f"{HUB_CATALOG.format(lang=lang)} lost {key}")
        for lang in LANGUAGES:
            with self.subTest(product_name=lang):
                self.assertEqual(hub_reserved()[lang].get("reserved_persona"), "Persona",
                                 "the product's name is Persona in every language")


class ClientPlaceTests(unittest.TestCase):
    longMessage = False

    def test_client_keys_exist(self):
        for client, keys, read, where in CLIENTS:
            for key in keys:
                for lang in LANGUAGES:
                    with self.subTest(client=client, key=key, lang=lang):
                        self.assertTrue(read(lang, key),
                                        f"{client} has no {lang} text for {key!r} in {where}: if the place's key was "
                                        f"renamed, update the table in {THIS_FILE}")

    def test_client_place_names_are_reserved(self):
        reserved = reserved_set()
        for client, keys, read, _ in CLIENTS:
            for key in keys:
                for lang in LANGUAGES:
                    for text in read(lang, key):
                        with self.subTest(client=client, key=key, lang=lang, text=text):
                            self.assertIn(fold(text), reserved,
                                          f"{client} names a place {text!r} ({key}, {lang}) that is not reserved: "
                                          f"add it to {HUB_CATALOGS} as a reserved_* key in all three languages")


if __name__ == "__main__":
    unittest.main()
