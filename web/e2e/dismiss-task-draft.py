#!/usr/bin/env python3
"""Drive the real built web client with synthetic task/feed data; keep CDP pages open."""
import argparse
import copy
import importlib.util
import json
from pathlib import Path
from urllib.parse import urlparse

from playwright.sync_api import expect, sync_playwright

fixture_path = Path(__file__).with_name("assistant-task-trays.py")
spec = importlib.util.spec_from_file_location("task_tray_fixtures", fixture_path)
fixtures = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixtures)


def run(args):
    shots = Path(args.shots)
    shots.mkdir(parents=True, exist_ok=True)
    with sync_playwright() as p:
        browser = p.chromium.connect_over_cdp(args.cdp) if args.cdp else p.chromium.launch(executable_path=args.chromium, args=["--no-sandbox"])
        for language, width in (("zh-Hans", 1280), ("en", 390), ("ja", 390)):
            for theme in ("light", "dark"):
                labels = {"zh-Hans": ("不处理", "撤销"), "en": ("Don't handle", "Undo"), "ja": ("対応しない", "元に戻す")}[language]
                context = browser.new_context(viewport={"width": width, "height": 900}, locale=language, color_scheme=theme, reduced_motion="reduce")
                context.add_init_script(f"localStorage.setItem('persona.token','demo');localStorage.setItem('persona.language','{language}');localStorage.setItem('persona.theme','{theme}');")
                context.add_init_script("const RealDate=Date;globalThis.Date=class extends RealDate{constructor(...args){super(...(args.length?args:['2026-11-10T08:10:00Z']));}static now(){return new RealDate('2026-11-10T08:10:00Z').getTime();}};")
                main = fixtures.MAIN
                task = copy.deepcopy(fixtures.TODOS[0])
                task["title"] = "Synthetic reply task"
                task["supervision"]["progress"]["text"] = "Checking the example change"
                task.pop("pr_url", None)
                sessions = copy.deepcopy(fixtures.SESSIONS[:2])
                sessions[1]["title"] = task["title"]
                sessions[1]["todo"]["title"] = task["title"]
                draft = {"id": "demo-draft", "conversation": main, "at": fixtures.AT, "kind": "message", "ref": "demo-draft-ref", "body": {"todo_id": task["id"], "source": "github", "sender": "Example reviewer", "chat": "example-org/example-repo", "text": "Are the checks ready?", "draft": "The synthetic checks are ready for review."}}
                feed = [{"id": "demo-task-event", "conversation": main, "at": fixtures.AT, "kind": "task", "ref": task["id"]}, draft]
                writes, sockets, errors = [], [], []

                def api(route):
                    path = urlparse(route.request.url).path.removeprefix("/api/v1")
                    method = route.request.method
                    body = json.loads(route.request.post_data or "{}") if method == "POST" else {}
                    data, status = {}, 200
                    if method == "POST" and not path.endswith("/today"):
                        writes.append({"path": path, "body": body})
                    if path == "/machines":
                        data = {"machines": [fixtures.MACHINE]}
                    elif path == "/sessions":
                        data = {"sessions": sessions}
                    elif path == "/assistant":
                        data = {"machine_id": "demo", "main": main, "conversations": [sessions[0]], "tasks": [], "memory": []}
                    elif path == f"/assistant/conversations/{main}/feed":
                        data = {"items": feed, "plans": {}, "task_cards": []}
                    elif path == "/todos":
                        data = {"todos": [task]}
                    elif path.endswith("/items"):
                        data = {"items": fixtures.ITEMS, "has_more": False}
                    elif path.startswith("/sessions/demo/") and method == "GET":
                        data = {"session": next((s for s in sessions if s["session_id"] == path.rsplit("/", 1)[-1]), sessions[0])}
                    elif path == "/assistant/feed/demo-draft/dismiss":
                        assert body == {"draft": "The synthetic checks are ready for review."}, body
                        draft["body"].update(dismissed_at="2026-11-10T08:11:00Z", dismissal_id="demo-dismissal")
                        data = {"item": draft, "dismissal_id": "demo-dismissal"}
                    elif path == "/assistant/feed/demo-draft/undo-dismiss":
                        assert body == {"dismissal_id": "demo-dismissal"}, body
                        draft["body"].pop("dismissed_at", None)
                        draft["body"].pop("dismissal_id", None)
                        data = {"item": draft}
                    elif path in ("/extensions", "/managers", "/interactions"):
                        data = {path[1:]: []}
                    route.fulfill(status=status, content_type="application/json", body=json.dumps(data))

                def socket(ws):
                    sockets.append(ws)
                    ws.send(json.dumps({"type": "hello", "server_version": "synthetic", "features": [], "hub_name": "Example hub"}))
                    ws.on_message(lambda message: ws.send(json.dumps({"type": "pong"})) if "app_ping" in message else None)

                context.route("**/api/v1/**", api)
                context.route_web_socket("**/api/v1/ws**", socket)
                page = context.new_page()
                page.on("pageerror", lambda error: errors.append(str(error)))
                page.goto(args.url.rstrip("/") + "/")
                bar = page.locator("[data-assistant-actionbar]")
                needs = bar.locator('[data-tray-entry="needs"]')
                running = bar.locator('[data-tray-entry="running"]')
                expect(needs).to_be_visible()
                needs.click()
                card = bar.locator('[data-testid="message-card"]')
                expect(card).to_be_visible()

                def capture(name):
                    assert not errors, errors
                    page.evaluate("() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r)))")
                    card_buttons = card.locator("button")
                    if card.count():
                        assert card_buttons.evaluate_all("es=>es.every(e=>{const b=e.getBoundingClientRect();return b.right<=innerWidth&&b.x>=0;})"), "Buttons overflow"
                    page.screenshot(path=str(shots / f"{language}-{width}-{theme}-{name}.png"))

                card.get_by_role("button", name=labels[0], exact=True).scroll_into_view_if_needed()
                capture("three-actions")
                page.evaluate("document.documentElement.style.setProperty('--text-scale', '2')")
                card.get_by_role("button", name=labels[0], exact=True).scroll_into_view_if_needed()
                capture("three-actions-accessibility")
                page.evaluate("document.documentElement.style.removeProperty('--text-scale')")
                original_task = copy.deepcopy(task)
                card.get_by_role("button", name=labels[0], exact=True).click()
                expect(card).to_have_count(0)
                undo = page.get_by_role("button", name=labels[1], exact=True)
                expect(undo).to_be_visible()
                expect(running).to_contain_text("1")
                assert task == original_task
                capture("dismissed-undo")
                undo.click()
                if needs.get_attribute("aria-expanded") != "true":
                    needs.click()
                expect(card).to_be_visible()
                card.get_by_role("button", name=labels[0], exact=True).click()
                expect(card).to_have_count(0)
                page.reload()
                expect(needs).to_be_visible()
                needs.click()
                expect(card).to_have_count(0)
                next_draft = copy.deepcopy(draft)
                next_draft["id"] = "demo-new-progress"
                next_draft["at"] = "2026-11-10T08:12:00Z"
                next_draft["body"].pop("dismissed_at", None)
                next_draft["body"].pop("dismissal_id", None)
                next_draft["body"]["draft"] = "A new progress update is ready."
                feed.append(next_draft)
                for ws in sockets[-1:]:
                    ws.send(json.dumps({"type": "assistant_feed", "item": next_draft}))
                expect(card).to_have_count(1)
                expect(card.locator("textarea")).to_have_value("A new progress update is ready.")
                assert task == original_task
                assert writes == [
                    {"path": "/assistant/feed/demo-draft/dismiss", "body": {"draft": "The synthetic checks are ready for review."}},
                    {"path": "/assistant/feed/demo-draft/undo-dismiss", "body": {"dismissal_id": "demo-dismissal"}},
                    {"path": "/assistant/feed/demo-draft/dismiss", "body": {"draft": "The synthetic checks are ready for review."}},
                ], writes
                print(f"PASS {language} {width} {theme}: dismiss=2 undo=1 reply=0 task_mutations=0 reload_pending=0 new_progress_pending=1", flush=True)
                if not args.cdp:
                    context.close()
        if not args.cdp:
            browser.close()
    print("PASS: web task draft dismissal, Undo, refresh persistence, new progress, and light/dark screenshots", flush=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("url")
    parser.add_argument("--shots", required=True)
    parser.add_argument("--cdp")
    parser.add_argument("--chromium")
    run(parser.parse_args())
