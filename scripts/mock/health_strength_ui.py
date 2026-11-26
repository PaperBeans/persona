"""Synthetic strength training pages and card (#14713) for the mock hub, as the hub's own code drew them.

The fixtures (fixtures/health-strength-ui/) are exported from the Health module's page code with synthetic data
(docs/web/web.md "Health Today"): re-export them when those pages change. Exercise and draft ids come from the export.
"""
from copy import deepcopy
import json
from pathlib import Path

FIXTURES = Path(__file__).with_name("fixtures") / "health-strength-ui"
# Drafts settled from their card meanwhile: their edit page is read-only and the card says they were saved.
SETTLED = set()


def fixtures():
    """Every exported page and card, by "<locale>.<name>" (en and zh-Hans; other languages read the English ones)."""
    out = {}
    for path in sorted(FIXTURES.glob("*.json")):
        _, locale, _, name, _, _ = path.name.split(".")
        out[f"{locale}.{name}"] = json.loads(path.read_text())
    return out


def state_of(result):
    return json.loads(result["view"].get("state") or "{}")


def ids():
    """The exported exercises' and drafts' ids: exercise id -> page name, and the pending draft's and the plan's ids."""
    pages = fixtures()
    exercises = {state_of(v)["exercise"]: k.split(".", 1)[1] for k, v in pages.items() if k.startswith("en.exercise-")}
    return exercises, state_of(pages["en.edit"])["draft"], state_of(pages["en.edit-plan"])["draft"]


def card_key():
    return fixtures()["en.card"]["key"]


def card(locale):
    """The draft's card in the chat: open until its Save was pressed, then saved."""
    pages = fixtures()
    _, pending, _ = ids()
    lang = "zh-Hans" if locale.startswith("zh") else "en"
    return deepcopy(pages[f"{lang}.{'card-saved' if pending in SETTLED else 'card'}"])


def answer(body):
    """POST /api/v1/assistant/health/intake/ui for a strength state: the exported page it names. Manage and Done switch
    the overview; a draft's Save and Cancel answer with a pop, as the hub's do; other presses keep the page with a toast.
    A KeyError is a state the export has no page for (400)."""
    context = body.get("context") or {}
    lang = "zh-Hans" if context.get("locale", "").startswith("zh") else "en"
    state = json.loads(body.get("state") or "{}")
    page, action = state.get("page"), body.get("action") or ""
    exercises, pending, plan = ids()
    if page == "strength.home":
        name = "home"
    elif page == "strength.overview":
        manage = (state.get("manage", False) or action == "manage") and action != "done"
        name = "overview-manage" if manage else "overview"
    elif page == "strength.exercise":
        name = exercises[state["exercise"]]
    elif page == "strength.edit":
        draft = state["draft"]
        if draft == pending:
            name = "edit-saved" if pending in SETTLED else "edit"
        elif draft == plan:
            name = "edit-plan"
        else:
            raise KeyError(draft)
    else:
        raise KeyError(page)
    result = deepcopy(fixtures()[f"{lang}.{name}"])
    if action in ("save", "cancel") and page == "strength.edit":
        result["pop"] = True
    elif action and action not in ("manage", "done"):
        result["toast"] = "Sample action: " + action
    return result
