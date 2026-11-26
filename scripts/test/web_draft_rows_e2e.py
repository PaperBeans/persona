#!/usr/bin/env python3
"""End-to-end check of draft marks on sidebar rows (#5568): a conversation with words left unsent in its box shows a red
"Draft:" and the words under its title once you leave it, for agents, Persona conversations and the main conversation;
it survives a reload, goes when the box is cleared or the words are sent, and whitespace alone is no draft.

Needs a running web page against the fake hub and Playwright for Python (it drives the installed Chrome):

    PORT=18793 python3 scripts/mock/mock_hub.py &
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:18793 npx vite --port 5293 --strictPort &)
    python3 scripts/test/web_draft_rows_e2e.py http://127.0.0.1:5293 [--shots <dir>]

Prints one line per check and a verdict; exits 1 on the first failure. --shots saves wide and phone-width (drawer and
the Agents page) screenshots, light and dark.
"""
import argparse
import json
import os
import urllib.request

from playwright.sync_api import expect, sync_playwright

PERSONA = 'a[aria-description="Persona 的主对话"]'


def ok(msg):
    print("ok  " + msg, flush=True)


def get(base, token, path):
    req = urllib.request.Request(base + path, headers={"Authorization": "Bearer " + token})
    return json.load(urllib.request.urlopen(req, timeout=10))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("base")
    ap.add_argument("--shots")
    ap.add_argument("--token", default="mock")
    a = ap.parse_args()
    base = a.base.rstrip("/")
    sessions = get(base, a.token, "/api/v1/sessions")["sessions"]
    assistant = get(base, a.token, "/api/v1/assistant")
    live = [s for s in sessions if not s.get("assistant") and s.get("state") not in ("offline", None)]
    # one that needs you (its line says what it asks), one that finished unseen (its last words), one seen
    asks = next(s for s in live if s["state"] == "needs_you")
    done = next(s for s in live if s["state"] == "unread")
    seen = next(s for s in live if s["state"] == "idle")
    conv = next(c for c in assistant["conversations"] if c["session_id"] != assistant["main"])
    if a.shots:
        os.makedirs(a.shots, exist_ok=True)
    with sync_playwright() as p:
        browser = p.chromium.launch(channel="chrome")
        try:
            run(browser, base, a.token, asks, done, seen, conv, a.shots)
        finally:
            browser.close()
    print("PASS web draft rows e2e")


def agent_row(page, s, scope="aside"):
    # by title: grouped by herdr tab (the default) a row links to its tab, not to the session
    return page.locator(f"{scope} a").filter(has_text=s["title"]).first


def conv_row(page, c):
    return page.locator(f'aside a[href="/persona/{c["session_id"]}"]')


def type_and_leave(page, row_open, text, row_next):
    """Open a conversation from its row, type into its box, then open another one from the sidebar."""
    row_open.click()
    expect(row_open).to_have_attribute("aria-current", "page")
    box = page.locator("main textarea").last
    box.fill(text)
    # the open row says nothing while you type in it
    page.wait_for_timeout(400)
    expect(row_open.locator("[data-draft]")).to_have_count(0)
    row_next.click()
    expect(row_next).to_have_attribute("aria-current", "page")


def run(browser, base, token, asks, done, seen, conv, shots):
    ctx = browser.new_context(viewport={"width": 1280, "height": 820}, device_scale_factor=2, locale="zh-CN")
    page = ctx.new_page()
    page.set_default_timeout(10000)
    page.goto(base + "/overview?token=" + token)
    expect(page.locator("aside")).to_be_attached()
    page.evaluate("localStorage.setItem('persona.theme', 'light')")
    page.reload()

    # 1. An agent that needs you: typed, left — "Draft:" and the words in place of what it asks, its count stays.
    type_and_leave(page, agent_row(page, asks), "先把网络层的测试跑一遍\n再看覆盖率", agent_row(page, seen))
    mark = agent_row(page, asks).locator("[data-draft]")
    expect(mark).to_have_text("草稿：先把网络层的测试跑一遍 再看覆盖率")
    expect(mark.locator(".text-red")).to_have_text("草稿：")
    expect(agent_row(page, asks).locator("[data-row-line]")).to_have_count(1)
    ok("an agent that needs you: 草稿：<words> once you leave it, in place of what it asks")

    # 2. A Persona conversation and the main conversation.
    type_and_leave(page, conv_row(page, conv), "把天气和日程放在最前面", agent_row(page, seen))
    expect(conv_row(page, conv).locator("[data-draft]")).to_have_text("草稿：把天气和日程放在最前面")
    type_and_leave(page, page.locator(PERSONA), "明天上午的会挪到下午", agent_row(page, seen))
    expect(page.locator(PERSONA).locator("[data-draft]")).to_have_text("草稿：明天上午的会挪到下午")
    ok("a Persona conversation and the main row: the same mark")

    # 3. Whitespace alone is no draft.
    type_and_leave(page, agent_row(page, done), "   \n  ", agent_row(page, seen))
    expect(agent_row(page, done).locator("[data-draft]")).to_have_count(0)
    ok("whitespace alone: no mark")

    # 4. A reload keeps them; the open row still shows none.
    page.reload()
    expect(agent_row(page, seen)).to_have_attribute("aria-current", "page")
    expect(agent_row(page, asks).locator("[data-draft]")).to_have_text("草稿：先把网络层的测试跑一遍 再看覆盖率")
    expect(page.locator(PERSONA).locator("[data-draft]")).to_have_text("草稿：明天上午的会挪到下午")
    ok("a reload keeps every mark")

    # 5. A draft on the one that finished unseen too, for the pictures: its last words give way.
    type_and_leave(page, agent_row(page, done), "发布说明再加一段英文", agent_row(page, seen))
    expect(agent_row(page, done).locator("[data-draft]")).to_have_text("草稿：发布说明再加一段英文")

    if shots:
        # the whole column at once; the open conversation has words in its box and no mark on its own row
        page.set_viewport_size({"width": 1280, "height": 1560})
        box = page.locator("main textarea").last
        box.fill("顺便看一下 CI 里的重试")
        page.wait_for_timeout(400)
        for theme in ("light", "dark"):
            page.evaluate(f"localStorage.setItem('persona.theme', '{theme}')")
            page.reload()
            expect(agent_row(page, asks).locator("[data-draft]")).to_be_visible()
            expect(page.locator("main textarea").last).to_have_value("顺便看一下 CI 里的重试")
            expect(agent_row(page, seen).locator("[data-draft]")).to_have_count(0)
            page.wait_for_timeout(800)
            page.screenshot(path=os.path.join(shots, f"wide-{theme}.png"))
            page.locator("aside").screenshot(path=os.path.join(shots, f"wide-sidebar-{theme}.png"))
        page.locator("main textarea").last.fill("")
        page.wait_for_timeout(400)
        page.evaluate("localStorage.setItem('persona.theme', 'light')")
        page.set_viewport_size({"width": 1280, "height": 820})
        page.reload()

    # 6. Narrow: the drawer and the phone's Agents page show the same marks without crowding the row.
    for theme in ("light", "dark"):
        nctx = browser.new_context(viewport={"width": 390, "height": 844}, device_scale_factor=2, has_touch=True, locale="zh-CN")
        npage = nctx.new_page()
        npage.set_default_timeout(10000)
        # the drafts typed above, as this browser kept them
        drafts = page.evaluate("Object.fromEntries(Object.keys(localStorage).filter((k) => k.startsWith('draft:')).map((k) => [k, localStorage.getItem(k)]))")
        npage.goto(base + "/overview?token=" + token)
        expect(npage.locator("aside")).to_be_attached()
        npage.evaluate("([drafts, theme]) => { for (const [k, v] of Object.entries(drafts)) localStorage.setItem(k, v); localStorage.setItem('persona.theme', theme); }", [drafts, theme])
        npage.goto(base + "/agents")
        # the phone's Agents page draws the rows itself (the drawer over it leaves them out)
        mark = agent_row(npage, asks, "main").locator("[data-draft]")
        expect(mark).to_have_text("草稿：先把网络层的测试跑一遍 再看覆盖率")
        fits = npage.evaluate("""() => [...document.querySelectorAll('[data-draft]')].every((el) => {
          const a = el.closest('a').getBoundingClientRect(), r = el.getBoundingClientRect();
          return r.right <= a.right + 0.5 && r.height <= 17 && el.scrollWidth >= el.clientWidth;
        })""")
        assert fits, "a draft line runs out of its row or wraps"
        if shots:
            npage.wait_for_timeout(600)
            npage.screenshot(path=os.path.join(shots, f"narrow-agents-{theme}.png"))
        npage.locator("main").get_by_role("button", name="侧边栏").first.click()
        expect(npage.locator(PERSONA).locator("[data-draft]")).to_have_text("草稿：明天上午的会挪到下午")
        if shots:
            npage.wait_for_timeout(600)
            npage.screenshot(path=os.path.join(shots, f"narrow-drawer-{theme}.png"))
        nctx.close()
    ok("narrow: the Agents page and the drawer show the marks on one line each")

    # 7. Clearing the box takes the mark away.
    agent_row(page, done).click()
    box = page.locator("main textarea").last
    expect(box).to_have_value("发布说明再加一段英文")
    box.fill("")
    agent_row(page, seen).click()
    expect(agent_row(page, done).locator("[data-draft]")).to_have_count(0)
    ok("clearing the box: the mark goes")

    # 8. Sending takes it away.
    conv_row(page, conv).click()
    box = page.locator("main textarea").last
    expect(box).to_have_value("把天气和日程放在最前面")
    box.press("Enter")
    expect(box).to_have_value("")
    agent_row(page, seen).click()
    expect(conv_row(page, conv).locator("[data-draft]")).to_have_count(0)
    ok("sending: the mark goes")
    ctx.close()


if __name__ == "__main__":
    main()
