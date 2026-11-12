#!/usr/bin/env python3
"""End-to-end check of reading a chat on a phone (#1996): the buttons that only showed on hover (copy code, Mermaid's
tools) are there to tap, a turn's pictures open in the image viewer where two fingers zoom the picture (not the page)
and Back closes it on the chat, a wide table's last column is not a character a line, your bubble keeps a margin on its
left (also while it is being sent, folded when long), the answer is 16 px, a work group's header is 40 tall, a long
press on an answer leaves its menu open after the finger lifts, an edit's diff takes the whole screen and folds past 40
lines, and formulas are drawn once KaTeX has loaded for them. On a desktop (1280, a mouse) the sizes stay 15 / 14.

Needs a running web page against the fake hub with these conversations, and Playwright for Python (it drives the
installed Chrome):

    MOCK_CHAT_READ=1 MOCK_SEND_DELAY_MS=20000 MOCK_WORK_GROUPS=1 MOCK_TURN_IMAGES_WEB=1 MOCK_RICH=1 MOCK_MERMAID=1 \\
      PORT=18962 python3 scripts/mock/mock_hub.py &
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:18962 npx vite --port 5962 --strictPort &)
    uv run --no-project --with playwright==1.58.0 python3 scripts/test/web_mobile_read_e2e.py http://127.0.0.1:5962 [--shots <dir>]

Prints one line per check and a verdict; exits 1 on the first failure. Runs the checks on a 390 × 844 phone, then the
layout ones on 360 × 740 and 844 × 390 (landscape); --shots saves each state there, light and dark.
"""
import argparse
import os
import sys

from playwright.sync_api import expect, sync_playwright

READ = "/s/macbook/a1f3c2d4-0000-4000-8000-0000000c1ead"  # MOCK_CHAT_READ
GROUPS = "/s/macbook/a1f3c2d4-0000-4000-8000-0000000c1e03"  # MOCK_WORK_GROUPS
REVIEW = "/s/macbook/a1f3c2d4-0000-4000-8000-000000000006"  # MOCK_TURN_IMAGES_WEB: nine pictures
RICH = "/s/macbook/a1f3c2d4-0000-4000-8000-00000000000a"  # MOCK_RICH: the wide tables
WIDGET = "/s/macbook/a1f3c2d4-0000-4000-8000-000000000001"  # MOCK_MERMAID
UA = ("Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 "
      "(KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1")
PHONES = {"390": (390, 844), "360": (360, 740), "land": (844, 390)}
S = {"base": "", "shots": None}


def ok(msg):
    print("ok  " + msg, flush=True)


def fail(msg):
    print("FAIL " + msg, flush=True)
    sys.exit(1)


def shot(page, name):
    if S["shots"]:
        page.screenshot(path=os.path.join(S["shots"], name + ".png"))


def open_page(browser, w, h, theme, phone=True):
    opts = dict(viewport={"width": w, "height": h}, color_scheme=theme)
    if phone:
        opts.update(device_scale_factor=3, is_mobile=True, has_touch=True, user_agent=UA)
    ctx = browser.new_context(**opts)
    ctx.grant_permissions(["clipboard-read", "clipboard-write"], origin=S["base"])
    page = ctx.new_page()
    page.goto(S["base"] + "/overview?token=mock")
    page.wait_for_selector("aside", state="attached", timeout=15000)
    return ctx, page


def same_chat(path, url):
    """Still on the chat `path` names: its /s/ link, or the /t/ one the page replaced it with (#1996)."""
    return path in url or url.rstrip("/").endswith("/" + path.rstrip("/").rsplit("/", 1)[-1])


def go(page, path):
    """In-app navigation (the history the overlays' Back works on), then the chat drawn."""
    page.evaluate("p => { history.pushState(null, '', p); dispatchEvent(new PopStateEvent('popstate')); }", path)
    expect(page.locator("main [data-segment]").first).to_be_visible(timeout=10000)
    page.wait_for_timeout(400)


def touches(page):
    """Sends touch events through one CDP session (a touch has to start and end on the same one)."""
    cdp = page.context.new_cdp_session(page)

    def send(kind, points):
        cdp.send("Input.dispatchTouchEvent", {"type": kind, "touchPoints": [{"x": x, "y": y, "id": i} for i, (x, y) in enumerate(points)]})

    return send


def center(loc):
    b = loc.bounding_box()
    return b["x"] + b["width"] / 2, b["y"] + b["height"] / 2


def check_sizes(page, tag, body, secondary, group_min=None):
    """The answer's size, a tool row's and, on a touch screen, the work group header's height."""
    go(page, READ)
    fs = page.evaluate("() => getComputedStyle(document.querySelector('main [data-answer] .prose-j')).fontSize")
    if fs != body:
        fail(f"{tag}: the answer is {fs}, not {body}")
    # the row's words ("run tests"), in the open once the group is open
    open_group(page)
    row = page.locator('main [data-action-row="Bash"] button span', has_text="跑测试").first.evaluate("e => getComputedStyle(e).fontSize")
    if row != secondary:
        fail(f"{tag}: a tool row is {row}, not {secondary}")
    # a table and a code block keep their size
    pre = page.evaluate("() => getComputedStyle(document.querySelector('main [data-answer] pre')).fontSize")
    if pre != "12.5px":
        fail(f"{tag}: a code block grew with the text ({pre})")
    ok(f"{tag}: the answer is {body}, a tool row {secondary}, code stays 12.5px")
    if group_min is not None:
        h = main_box(page, "[data-work-group] > button")["height"]
        if h < group_min:
            fail(f"{tag}: the work group header is {h:.0f} tall, under {group_min}")
        ok(f"{tag}: the work group header is {h:.0f} tall")


def open_group(page):
    """Opens the exchange's work group (it remembers being open across visits)."""
    head = page.locator("main [data-work-group] > button").first
    if head.get_attribute("aria-expanded") != "true":
        head.click()
    expect(page.locator("main [data-work-detail] [data-action-row]").first).to_be_visible()
    page.wait_for_timeout(350)  # the fold's opening


def main_box(page, sel):
    return page.locator("main " + sel).first.bounding_box()


def check_bubble(page, tag):
    """Your bubble leaves 12% on its left."""
    go(page, READ)
    sec = main_box(page, "[data-segment]")
    inner = page.locator("main [data-user-bubble] .bg-user-bubble").first.bounding_box()
    gap = inner["x"] - sec["x"]
    if gap < sec["width"] * 0.12 - 1:
        fail(f"{tag}: your bubble starts {gap:.0f}px in, under 12% of {sec['width']:.0f}")
    ok(f"{tag}: your bubble keeps {gap:.0f}px on its left")


def check_pending(page, tag):
    go(page, READ)
    long = "\n".join(f"第 {i} 行：这是一条很长的消息，发出去以后还没送到的时候也要收起来" for i in range(20))
    box = page.locator("main textarea").first
    box.fill(long)
    page.locator('main button[aria-label="发送"], main button[aria-label="排队发送"]').first.click()
    pend = page.locator("main [data-user-bubble]").filter(has_text="发送中").last
    expect(pend).to_be_visible()
    folded = pend.locator("[data-folded]")
    expect(folded).to_have_count(1)
    expect(pend.get_by_role("button", name="展开")).to_be_visible()
    # the column the bubble sits in
    sec = pend.evaluate("e => { const r = e.parentElement.getBoundingClientRect(); return { x: r.x, width: r.width }; }")
    gap = folded.bounding_box()["x"] - sec["x"]
    if gap < sec["width"] * 0.12 - 1:
        fail(f"{tag}: the bubble being sent starts {gap:.0f}px in")
    ok(f"{tag}: a long message being sent is folded to ten lines with 展开, {gap:.0f}px in")
    shot(page, f"{tag}-pending")


def check_copy(page, tag):
    go(page, READ)
    btn = page.locator('main [data-answer] button[aria-label="拷贝代码"]').first
    expect(btn).to_be_visible()
    if float(btn.evaluate("e => getComputedStyle(e).opacity")) < 0.99:
        fail(f"{tag}: copy code is invisible on a touch screen")
    btn.tap()
    clip = page.evaluate("() => navigator.clipboard.readText()")
    if "forecast.hours.prefix(6)" not in clip:
        fail(f"{tag}: tapping copy code put {clip[:40]!r} on the clipboard")
    ok(f"{tag}: copy code shows without hover and a tap copies the block")


def check_math(page, tag):
    go(page, READ)
    expect(page.locator("main [data-answer] .katex").first).to_be_visible(timeout=8000)
    if page.locator("main [data-math-pending]").count():
        fail(f"{tag}: a formula is still TeX after KaTeX loaded")
    if page.locator("main [data-answer] .katex-display").count() != 1:
        fail(f"{tag}: the display formula is not drawn")
    ok(f"{tag}: KaTeX loads for the formulas and draws them")


def check_mermaid(page, tag):
    go(page, WIDGET)
    m = page.locator("main [data-mermaid]").first
    m.scroll_into_view_if_needed()
    diagram = m.locator("[data-mermaid-diagram]")
    expect(diagram.locator("svg").first).to_be_visible(timeout=10000)
    tools = m.locator('button[aria-label="放大"]')
    if float(tools.evaluate("e => getComputedStyle(e.parentElement).opacity")) < 0.99:
        fail(f"{tag}: Mermaid's tools are invisible on a touch screen")
    diagram.tap()
    expect(page.get_by_role("dialog", name="图")).to_be_visible()
    page.go_back()
    expect(page.get_by_role("dialog", name="图")).to_have_count(0)
    if not same_chat(WIDGET, page.url):
        fail(f"{tag}: Back left the chat ({page.url})")
    ok(f"{tag}: Mermaid's tools show, a tap on the diagram opens it big and Back closes it")


def check_viewer(page, tag):
    go(page, REVIEW)
    strip = page.locator("main [data-turn-images] button").first
    strip.scroll_into_view_if_needed()
    strip.tap()
    dlg = page.locator('[role=dialog][aria-modal="true"]')
    expect(dlg).to_be_visible()
    img = dlg.locator("img").first
    expect(img).to_be_visible()
    page.wait_for_timeout(400)
    # the bottom bar ("Download", "Copy path", "Open in Files" ...) fits the screen
    out = dlg.evaluate("""d => { const w = document.documentElement.clientWidth;
      return [...d.querySelectorAll(':scope > div:last-of-type button')].filter(b => { const r = b.getBoundingClientRect(); return r.left < 0 || r.right > w; }).length; }""")
    if out:
        fail(f"{tag}: {out} of the viewer's buttons run off the screen")
    cx, cy = center(img)
    cdp_touch = touches(page)
    cdp_touch("touchStart", [(cx - 30, cy), (cx + 30, cy)])
    for k in range(1, 9):
        cdp_touch("touchMove", [(cx - 30 - k * 12, cy), (cx + 30 + k * 12, cy)])
        page.wait_for_timeout(16)
    cdp_touch("touchEnd", [])
    page.wait_for_timeout(300)
    scale = float(img.evaluate("e => parseFloat(e.style.scale || '1')"))
    vv = page.evaluate("() => visualViewport.scale")
    if scale < 1.5:
        fail(f"{tag}: a pinch zoomed the picture only to {scale:.2f}")
    if abs(vv - 1) > 0.01:
        fail(f"{tag}: a pinch zoomed the page ({vv})")
    ok(f"{tag}: two fingers zoom the picture ({scale:.1f}×), the page stays at 1×")
    shot(page, f"{tag}-viewer-pinch")
    page.go_back()
    expect(dlg).to_have_count(0)
    page.wait_for_timeout(300)
    if not same_chat(REVIEW, page.url) or not page.locator("main [data-turn-images]").count():
        fail(f"{tag}: Back left the chat ({page.url})")
    ok(f"{tag}: Back closes the viewer and stays on the chat")


def check_table(page, tag):
    """Every table's last column holds a few characters a line (it was one a line): the narrow table and the wide one
    that scrolls sideways alike, measured as its cell's text width over the font size, and its height in lines."""
    go(page, RICH)
    expect(page.locator("main .prose-j table").first).to_be_visible()
    cols = page.evaluate("""() => [...document.querySelectorAll('main .prose-j table')].map(t => {
      const td = t.querySelector('tbody tr:last-child td:last-child'); const cs = getComputedStyle(td);
      const inner = td.clientWidth - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight);
      const high = td.clientHeight - parseFloat(cs.paddingTop) - parseFloat(cs.paddingBottom);
      return { chars: inner / parseFloat(cs.fontSize), lines: high / parseFloat(cs.lineHeight) }; })""")
    if len(cols) < 2:
        fail(f"{tag}: expected the two tables, found {len(cols)}")
    for n, c in enumerate(cols):
        if c["chars"] < 3:
            fail(f"{tag}: table {n + 1}'s last column holds {c['chars']:.1f} characters a line ({c['lines']:.0f} lines)")
    ok(f"{tag}: the tables' last columns hold " + ", ".join(f"{c['chars']:.0f} characters a line ({c['lines']:.0f} lines)" for c in cols))
    shot(page, f"{tag}-table")


def check_long_press(page, tag):
    go(page, READ)
    a = page.locator("main [data-answer]").first
    a.scroll_into_view_if_needed()
    b = a.bounding_box()
    x, y = b["x"] + 40, b["y"] + 10
    cdp_touch = touches(page)
    cdp_touch("touchStart", [(x, y)])
    page.wait_for_timeout(650)
    cdp_touch("touchEnd", [])
    page.wait_for_timeout(400)
    menu = page.get_by_role("menu")
    if not menu.count() or not menu.first.is_visible():
        fail(f"{tag}: the long press menu closed when the finger lifted")
    ok(f"{tag}: a long press opens the menu and it stays after the finger lifts")
    shot(page, f"{tag}-menu")
    page.keyboard.press("Escape")
    expect(menu).to_have_count(0)


def check_revisit(page, tag):
    """A work group left open shows its steps when the chat comes back (it stayed at "Fetching N")."""
    go(page, READ)
    open_group(page)
    go(page, GROUPS)
    go(page, READ)
    expect(page.locator('main [data-work-detail] [data-action-row="Edit"]')).to_be_visible(timeout=5000)
    ok(f"{tag}: a work group left open has its steps when the chat comes back")


def check_diff(page, tag):
    go(page, READ)
    open_group(page)
    page.locator('main [data-action-row="Edit"] button').first.tap()
    scr = page.locator("[data-diff-screen]")
    expect(scr).to_be_visible()
    page.wait_for_timeout(350)  # its slide-up
    b = scr.bounding_box()
    cw = page.evaluate("document.documentElement.clientWidth")
    if abs(b["width"] - cw) > 1 or b["x"] > 1:
        fail(f"{tag}: the diff is {b['width']:.0f} wide at {b['x']:.0f}, not the screen ({cw})")
    more = scr.locator("[data-diff-more]")
    expect(more).to_contain_text("展开其余")
    n = scr.locator("[data-diff-line]").count()
    if n != 40:
        fail(f"{tag}: the folded diff shows {n} lines, not 40")
    # a fade at the right edge while the lines go on past it, none when they fit
    over, fade = scr.locator("[data-diff]").evaluate("e => [e.scrollWidth > e.clientWidth + 2, getComputedStyle(e).maskImage]")
    if over != ("gradient" in fade):
        fail(f"{tag}: lines {'overflow' if over else 'fit'} but the fade is {fade!r}")
    shot(page, f"{tag}-diff")
    more.tap()
    expect(more).to_have_count(0)
    if scr.locator("[data-diff-line]").count() <= 40:
        fail(f"{tag}: 展开 did not show the rest")
    ok(f"{tag}: the diff takes the screen, fades at the right, folds at 40 lines and opens to all")
    page.go_back()
    expect(scr).to_have_count(0)
    if not same_chat(READ, page.url):
        fail(f"{tag}: Back left the chat")
    ok(f"{tag}: Back closes the diff on the chat")


def phone(browser, name, theme, full):
    w, h = PHONES[name]
    tag = f"{name}-{theme}"
    ctx, page = open_page(browser, w, h, theme)
    body = "16px" if w < 640 else "15px"
    check_sizes(page, tag, body, "15px" if w < 640 else "14px", group_min=40)
    shot(page, f"{tag}-read")
    check_bubble(page, tag)
    go(page, GROUPS)
    shot(page, f"{tag}-groups")
    check_table(page, tag)
    if full:
        check_copy(page, tag)
        check_math(page, tag)
        check_mermaid(page, tag)
        check_viewer(page, tag)
        check_long_press(page, tag)
        check_revisit(page, tag)
        check_diff(page, tag)
        check_pending(page, tag)
    ctx.close()


def desktop(browser, theme):
    """A mouse at 1280: the sizes and the header as before."""
    tag = f"desktop-{theme}"
    ctx, page = open_page(browser, 1280, 820, theme, phone=False)
    check_sizes(page, tag, "15px", "14px")
    h = main_box(page, "[data-work-group] > button")
    if h and h["height"] >= 32:
        fail(f"{tag}: the work group header grew on a desktop ({h['height']:.0f})")
    btn = page.locator('main [data-answer] button[aria-label="拷贝代码"]').first
    if float(btn.evaluate("e => getComputedStyle(e).opacity")) > 0.01:
        fail(f"{tag}: copy code shows without hover on a desktop")
    ok(f"{tag}: header {h['height']:.0f} tall, copy code waits for hover")
    shot(page, f"{tag}-read")
    ctx.close()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("base")
    ap.add_argument("--shots")
    a = ap.parse_args()
    S["base"] = a.base.rstrip("/")
    S["shots"] = a.shots
    if a.shots:
        os.makedirs(a.shots, exist_ok=True)
    with sync_playwright() as p:
        browser = p.chromium.launch(channel="chrome")
        try:
            for theme in ("light", "dark"):
                phone(browser, "390", theme, full=True)
                phone(browser, "360", theme, full=False)
                phone(browser, "land", theme, full=theme == "dark")
                desktop(browser, theme)
        finally:
            browser.close()
    print("PASS", flush=True)


if __name__ == "__main__":
    main()
