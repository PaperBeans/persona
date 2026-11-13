#!/usr/bin/env python3
"""End-to-end check of the chat's composer area on a phone (#1996): with three cards waiting the conversation still
shows and the composer stays on screen (the first card open, "N more" for the others, one line while typing); a short
viewport (a keyboard on Android) and a twenty-line draft keep the send button on screen; the iOS keyboard moves the
side panel with it (--kb-top / --kb-h); Back closes a full-screen side panel and stays on the conversation; switching
panes writes no history; the header leaves the title room at 360; on a phone on its side the run's status and the
queue share a line.

Needs a running web page against the fake hub with the crowded conversation, and Playwright for Python (it drives the
installed Chrome):

    MOCK_SPLIT_TABS=1 MOCK_CHAT_CARDS=1 MOCK_FILES=1 PORT=18961 python3 scripts/mock/mock_hub.py &
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:18961 npx vite --port 5961 --strictPort &)
    uv run --no-project --with playwright==1.58.0 python3 scripts/test/web_mobile_input_e2e.py http://127.0.0.1:5961 [--shots <dir>]

Prints one line per check and a verdict; exits 1 on the first failure. --shots saves 390, 360 and 844×390 screenshots,
light and dark.
"""
import argparse
import json
import os
import sys
from urllib.parse import quote

from playwright.sync_api import expect, sync_playwright

CLAUDE = "5b1170b5-0000-4000-8000-000000000001"  # working, three cards, two queued (MOCK_CHAT_CARDS)
CODEX = "5b1170b5-0000-4000-8000-000000000002"  # the other pane of its tab
PANE = "/t/server/w1/" + quote("w1:t1", safe="") + "/" + CLAUDE
UA = ("Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 "
      "(KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1")

# iOS: the keyboard shrinks the visual viewport only; a stand-in that the page's listeners follow.
FAKE_VV = """(() => {
  const et = new EventTarget();
  const vv = { height: innerHeight, width: innerWidth, offsetTop: 0, offsetLeft: 0, scale: 1, pageTop: 0,
    addEventListener: et.addEventListener.bind(et), removeEventListener: et.removeEventListener.bind(et) };
  Object.defineProperty(window, 'visualViewport', { get: () => vv });
  window.__kb = (h, top = 0) => { vv.height = h; vv.offsetTop = top; et.dispatchEvent(new Event('resize')); };
})();"""

BOX = """(sel) => { const e = document.querySelector(sel); if (!e) return null; const r = e.getBoundingClientRect();
  return { top: r.top, bottom: r.bottom, left: r.left, right: r.right, width: r.width, height: r.height, inner: innerHeight }; }"""

ERRORS = []  # uncaught errors on any page: the run fails on them
COMPOSER = "main textarea[aria-label=消息]"
SEND = "main [aria-label=发送], main [aria-label=中断]"


def ok(msg):
    print("ok  " + msg, flush=True)


def fail(msg):
    print("FAIL " + msg, flush=True)
    sys.exit(1)


def box(page, sel):
    b = page.evaluate(BOX, sel)
    if not b:
        fail(f"no {sel}")
    return b


def on_screen(page, sel, what):
    b = box(page, sel)
    if b["top"] < 0 or b["bottom"] > b["inner"] + 0.5:
        fail(f"{what}: {sel} is off screen ({b})")
    return b


def phone(browser, w, h, theme="light", ios=False):
    ctx = browser.new_context(viewport={"width": w, "height": h}, device_scale_factor=3, is_mobile=True, has_touch=True,
                              user_agent=UA, color_scheme=theme)
    if ios:
        ctx.add_init_script(FAKE_VV)
    page = ctx.new_page()
    page.on("pageerror", lambda e: ERRORS.append(str(e)))
    return ctx, page


def login(page, base):
    """Signs in; the theme follows the context's color scheme (the page's own setting is left at "Match system")."""
    page.goto(base + "/overview?token=mock")
    page.wait_for_selector("aside", state="attached", timeout=15000)


def go(page, path):
    """In-page navigation (a pushState and its popstate), as a link in the app would."""
    page.evaluate(f"history.pushState(null, '', {json.dumps(path)}); dispatchEvent(new PopStateEvent('popstate'))")


def open_pane(page, base, cards=True):
    """The crowded conversation, settled (its cards' box only exists on a narrow screen)."""
    login(page, base)
    go(page, PANE)
    page.wait_for_selector(COMPOSER)
    if cards:
        page.wait_for_selector("[data-chat-cards]", state="attached")
    page.wait_for_timeout(700)


def chat_height(page):
    return page.evaluate("document.querySelector('[data-chat-scroll]').clientHeight")


def crowded(page, what, min_chat=0.25, fold="还有 2 件"):
    """Three cards: the conversation still shows, the composer is on screen, the dock within about half."""
    expect(page.locator("[data-chat-cards-fold]").get_by_text(fold)).to_be_visible()
    # not scrolled out of a box or covered: a finger on its middle lands on it
    if not page.evaluate("""(() => { const e = document.querySelector('[data-chat-cards-fold]'); const r = e.getBoundingClientRect();
        return e.contains(document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2)); })()"""):
        fail(f"{what}: {fold} is out of sight")
    inner = page.evaluate("innerHeight")
    dock = box(page, "[data-chat-dock]")
    if dock["height"] > inner * 0.55 + 1:
        fail(f"{what}: the dock is {dock['height']:.0f} of {inner} px")
    chat = chat_height(page)
    if chat < inner * min_chat:
        fail(f"{what}: the conversation has {chat} of {inner} px")
    on_screen(page, COMPOSER, what)
    on_screen(page, SEND, what)
    ok(f"{what}: three cards, {fold}; the chat keeps {chat} px, the composer on screen")


def run(base, shots):
    with sync_playwright() as p:
        browser = p.chromium.launch(channel="chrome")

        # 1. Three cards at 390×844: one open, the others in a line; tapping it opens them in their own scroll box.
        ctx, page = phone(browser, 390, 844)
        open_pane(page, base)
        if page.evaluate("matchMedia('(any-pointer: fine)').matches"):
            fail("the phone context reports a precise pointer: Enter would send")
        crowded(page, "390×844")
        # the run's time and the live group's "Working..." count from the same start (the turn's message)
        times = page.evaluate(r"""(() => {
          const secs = (t) => { const m = /(?:(\d+)\s*(?:分|m))?\s*(\d+)\s*(?:秒|s)/.exec(t); return m ? (+(m[1] || 0)) * 60 + +m[2] : null; };
          const bar = document.querySelector('[data-running-elapsed]')?.textContent ?? '';
          const live = [...document.querySelectorAll('[data-chat-scroll] *')].filter((e) => !e.children.length && /^\d+m \d+s$|^\d+s$/.test(e.textContent.trim())).pop()?.textContent ?? '';
          return [bar, live, secs(bar), secs(live)]; })()""")
        if times[2] is None or times[3] is None or abs(times[2] - times[3]) > 2:
            fail(f"the run's time {times[0]!r} and 正在工作… {times[1]!r} do not agree")
        ok(f"the run's time ({times[0]}) agrees with 正在工作… ({times[1]})")
        page.locator("[data-chat-cards-fold]").click()
        expect(page.get_by_text("刷新失败以后怎么处理？")).to_be_visible()
        cards = page.evaluate("(() => { const e = document.querySelector('[data-chat-cards]'); return [e.scrollHeight, e.clientHeight]; })()")
        if cards[0] <= cards[1]:
            fail(f"opened, the three cards should scroll in their own box ({cards})")
        dock = box(page, "[data-chat-dock]")
        if dock["height"] > 844 * 0.55 + 1:
            fail(f"opened, the dock is {dock['height']:.0f} px")
        on_screen(page, SEND, "opened")
        ok(f"还有 2 件 opens the others in a box of their own (scrolls {cards[0]} in {cards[1]} px), the dock still capped")
        page.get_by_role("button", name="收起其余").click()
        expect(page.locator("[data-chat-cards-fold]").get_by_text("还有 2 件")).to_be_visible()

        # 2. Typing (the keyboard up): the cards fold into one line; tapping it puts the keyboard away.
        page.locator(COMPOSER).tap()
        expect(page.locator("[data-chat-cards-fold]").get_by_text("3 件待处理")).to_be_visible()
        try:
            page.wait_for_function("[...document.querySelectorAll('[data-chat-cards] .grid')].every((g) => g.inert)", timeout=2000)
        except Exception:
            fail("while typing, a card is still open")
        ok("while typing, the three cards are one line: 3 件待处理")
        page.locator("[data-chat-cards-fold]").click()
        expect(page.locator("[data-chat-cards-fold]").get_by_text("还有 2 件")).to_be_visible()
        if page.evaluate("document.activeElement?.getAttribute('aria-label')") == "消息":
            fail("tapping 3 件待处理 should put the keyboard away")
        ok("tapping it puts the keyboard away and the first card is back")

        # 3. A short viewport (Android's keyboard) and a twenty-line draft: the composer and send stay on screen.
        page.set_viewport_size({"width": 390, "height": 450})
        page.wait_for_timeout(400)
        crowded(page, "390×450 (short: all in one line)", min_chat=0.15, fold="3 件待处理")
        page.locator(COMPOSER).fill("\n".join(f"第 {i} 行" for i in range(1, 21)))
        page.wait_for_timeout(300)
        send = on_screen(page, "main [aria-label=发送]", "twenty lines at 450")
        ta = box(page, COMPOSER)
        if ta["height"] > 450 * 0.35 + 1:
            fail(f"twenty lines: the text box is {ta['height']:.0f} px, over 35% of 450")
        if page.evaluate(f"(() => {{ const t = document.querySelector({json.dumps(COMPOSER)}); return t.scrollHeight <= t.clientHeight; }})()"):
            fail("twenty lines: the text box should scroll")
        ok(f"twenty lines at 450: the box stops at {ta['height']:.0f} px and scrolls, send at {send['bottom']:.0f}")
        page.set_viewport_size({"width": 390, "height": 844})
        page.wait_for_timeout(300)
        page.locator(COMPOSER).fill("")
        page.locator(COMPOSER).blur()
        page.wait_for_timeout(400)  # the draft is saved 250 ms after the last change

        # Scrolled up: the plan's Step N / M and "Jump to bottom" side by side, not over each other.
        # a finger dragging the chat down (a programmatic scroll is not the reader leaving the end)
        cdp = page.context.new_cdp_session(page)
        y0 = box(page, "[data-chat-scroll]")["top"] + 20
        for _ in range(3):
            cdp.send("Input.dispatchTouchEvent", {"type": "touchStart", "touchPoints": [{"x": 200, "y": y0}]})
            for i in range(1, 13):
                cdp.send("Input.dispatchTouchEvent", {"type": "touchMove", "touchPoints": [{"x": 200, "y": y0 + 22 * i}]})
            cdp.send("Input.dispatchTouchEvent", {"type": "touchEnd", "touchPoints": []})
        cdp.detach()
        expect(page.locator("[data-plan-pill]")).to_be_visible()
        expect(page.locator("[data-jump-pill]")).to_be_visible()
        pill, jump = box(page, "[data-plan-pill]"), box(page, "[data-jump-pill]")
        if not (pill["right"] <= jump["left"] or jump["right"] <= pill["left"]) and not (pill["bottom"] <= jump["top"] or jump["bottom"] <= pill["top"]):
            fail(f"the plan's pill {pill} and 跳到底部 {jump} overlap")
        if abs(pill["top"] + pill["height"] / 2 - (jump["top"] + jump["height"] / 2)) > 2:
            fail(f"the plan's pill {pill} and 跳到底部 {jump} are not side by side")
        if shots:
            page.screenshot(path=os.path.join(shots, "390-pills.png"))
        page.locator("[data-jump-pill]").click()
        expect(page.locator("[data-jump-pill]")).to_have_count(0)
        ok("scrolled up: Step N / M and 跳到底部 side by side; 跳到底部 still goes to the end")
        if shots:
            for theme in ("dark", "light"):  # light last: the checks below go on in it
                page.evaluate(f"localStorage.setItem('persona.theme', '{theme}')")
                page.reload()
                page.wait_for_selector("[data-chat-cards]", state="attached")
                page.wait_for_timeout(900)
                page.screenshot(path=os.path.join(shots, f"390-cards-{theme}.png"))

        # 4. Back closes the full-screen file panel and stays on the conversation.
        url = page.url
        page.get_by_role("button", name="更多").click()
        page.locator("[role=menu] button", has_text="文件").first.click()
        expect(page.locator("aside[aria-label=文件]")).to_be_visible()
        page.go_back()
        expect(page.locator("aside[aria-label=文件]")).to_have_count(0)
        if page.url != url:
            fail(f"Back with the file panel open left the conversation ({page.url})")
        expect(page.locator(COMPOSER)).to_be_visible()
        ok("Back closes the full-screen file panel and stays on the conversation")
        # closed from the page, its entry goes too: Back then leaves the conversation
        page.get_by_role("button", name="更多").click()
        page.locator("[role=menu] button", has_text="文件").first.click()
        expect(page.locator("aside[aria-label=文件]")).to_be_visible()
        page.get_by_role("button", name="关闭面板").click()
        expect(page.locator("aside[aria-label=文件]")).to_have_count(0)
        page.wait_for_timeout(300)
        if page.url != url:
            fail(f"closing the panel moved the page ({page.url})")
        page.go_back()
        page.wait_for_function("!location.pathname.startsWith('/t/')")
        ok("closed with ×, the panel takes its history entry with it")

        # 5. Switching panes replaces the entry: Back returns to where the chat was opened from.
        go(page, "/overview")
        page.wait_for_timeout(300)
        go(page, PANE)
        page.wait_for_selector(COMPOSER)
        tabs = page.locator("main [role=tablist] [role=tab]")
        expect(tabs).to_have_count(2)
        page.locator("main [role=tab][aria-selected=false]").first.click()
        page.wait_for_function(f"location.pathname.endsWith({CODEX!r})")
        page.locator("main [role=tab][aria-selected=false]").first.click()
        page.wait_for_function(f"location.pathname.endsWith({CLAUDE!r})")
        page.go_back()
        try:
            page.wait_for_function("location.pathname === '/overview'", timeout=5000)
        except Exception:
            fail(f"after switching panes, Back went to {page.url}, not the page before the chat")
        ok("switching panes twice writes no history: Back goes to the page before the chat")
        ctx.close()

        # 6. iOS: the keyboard shrinks the visual viewport only; the side panel (terminal key bar) sits on what shows.
        ctx, page = phone(browser, 390, 844, ios=True)
        open_pane(page, base)
        page.evaluate("__kb(450, 0)")
        page.wait_for_timeout(200)
        kb = page.evaluate("[getComputedStyle(document.documentElement).getPropertyValue('--kb-top'), getComputedStyle(document.documentElement).getPropertyValue('--kb-h')]")
        if kb != ["0px", "450px"]:
            fail(f"--kb-top / --kb-h with the keyboard up: {kb}")
        page.get_by_role("button", name="更多").click()
        page.locator("[role=menu] button", has_text="文件").first.click()
        aside = box(page, "aside[aria-label=文件]")
        if abs(aside["top"]) > 1 or abs(aside["height"] - 450) > 1:
            fail(f"the side panel with the iOS keyboard up: {aside}")
        page.evaluate("__kb(450, 300)")
        page.wait_for_timeout(200)
        aside = box(page, "aside[aria-label=文件]")
        if abs(aside["top"] - 300) > 1:
            fail(f"the side panel follows the scrolled visual viewport: {aside}")
        page.evaluate("__kb(844, 0)")
        page.wait_for_timeout(200)
        aside = box(page, "aside[aria-label=文件]")
        if abs(aside["height"] - 844) > 1 or page.evaluate("document.documentElement.style.getPropertyValue('--kb-h')"):
            fail(f"keyboard down: the panel and the variables should be back ({aside})")
        ok("iOS keyboard: --kb-top / --kb-h follow the visual viewport and the side panel sits in it")
        ctx.close()

        # 7. 360 wide: the header leaves the title some ten characters.
        for theme in ("light", "dark"):
            ctx, page = phone(browser, 360, 740, theme)
            open_pane(page, base)
            h1 = box(page, "main header h1")
            size = page.evaluate("parseFloat(getComputedStyle(document.querySelector('main header h1')).fontSize)")
            if h1["width"] < size * 9.5:
                fail(f"360: the title gets {h1['width']:.0f} px ({h1['width'] / size:.1f} characters)")
            if page.locator("main header").get_by_text("需要你").count() or page.locator("main header").get_by_text("工作中").count():
                fail("360: the header says no state (#5421, cindy's: the running bar and the sidebar say it)")
            if page.locator("main header").get_by_text("同空间").count():
                fail("360: 同空间 is a row of ⋯, not a header button (#5421)")
            crowded(page, f"360×740 {theme}")
            if theme == "light":
                ok(f"360: the title gets {h1['width']:.0f} px ({h1['width'] / size:.1f} characters)")
            if shots:
                page.screenshot(path=os.path.join(shots, f"360-{theme}.png"))
                page.locator("main header").screenshot(path=os.path.join(shots, f"360-header-{theme}.png"))
            ctx.close()

        # 8. On its side (844×390): the run's status and the queue share a line; the chat keeps some room.
        for theme in ("light", "dark"):
            ctx, page = phone(browser, 844, 390, theme)
            open_pane(page, base)
            run_ = box(page, "[data-running-status]")
            queue = box(page, "[data-queue-strip]")
            if abs(run_["top"] + run_["height"] / 2 - (queue["top"] + queue["height"] / 2)) > 4:
                fail(f"844×390: the status ({run_}) and the queue ({queue}) are not on one line")
            if queue["height"] > 44:
                fail(f"844×390: the queue takes {queue['height']:.0f} px, should be one line")
            chat = chat_height(page)
            if chat < 390 * 0.22:
                fail(f"844×390: the conversation has {chat} px")
            on_screen(page, COMPOSER, "844×390")
            expect(page.locator("[data-chat-cards-fold]").get_by_text("3 件待处理")).to_be_visible()
            if shots:
                page.screenshot(path=os.path.join(shots, f"land-{theme}.png"))
            if theme == "light":
                ok(f"844×390: status and queue on one line, the cards in one more, the chat keeps {chat} px")
                page.locator("[data-chat-cards-fold]").click()
                expect(page.get_by_text("刷新失败以后怎么处理？")).to_be_visible()
                if box(page, "[data-chat-dock]")["height"] > 390 * 0.55 + 1:
                    fail("844×390: opened, the dock is over its cap")
                on_screen(page, SEND, "844×390 opened")
                page.get_by_role("button", name="收起").click()
                expect(page.locator("[data-chat-cards-fold]").get_by_text("3 件待处理")).to_be_visible()
                ok("844×390: tapping the line opens the cards in the capped box, 收起 folds them again")
            ctx.close()

        # 9. Wide (1280): the cards show as before, no box, no fold.
        ctx = browser.new_context(viewport={"width": 1280, "height": 900})
        page = ctx.new_page()
        page.on("pageerror", lambda e: ERRORS.append(str(e)))
        open_pane(page, base, cards=False)
        if page.locator("[data-chat-cards]").count() or page.locator("[data-chat-cards-fold]").count():
            fail("1280: the cards should show as before")
        expect(page.get_by_text("刷新失败以后怎么处理？")).to_be_visible()
        expect(page.locator("main header").get_by_text("工作中")).to_be_visible()
        ok("1280: every card in full and the header as before")
        if shots:
            page.screenshot(path=os.path.join(shots, "1280.png"))
        ctx.close()
        browser.close()
    if ERRORS:
        fail(f"uncaught page errors: {ERRORS[:3]}")
    print("PASS web_mobile_input_e2e", flush=True)


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("base")
    ap.add_argument("--shots")
    a = ap.parse_args()
    if a.shots:
        os.makedirs(a.shots, exist_ok=True)
    run(a.base.rstrip("/"), a.shots)
