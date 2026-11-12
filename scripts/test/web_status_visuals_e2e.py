#!/usr/bin/env python3
"""End-to-end check of the status marks, the folded tallies and the sliding segmented thumb (#3559, after Moshi).

Checks, one line each, light / dark x wide / 390 px: the sidebar's marks have a shape per state (the working one an
arc turning in 0.9 s, needs-you and done glowing, idle plain, offline hollow); a folded machine shows a mark and a
monospace count per state, needs you first, where it showed one red badge, and an expanded one keeps the badge; the
/terminal tree and the palette use the same marks (the session header has none: cindy's top bar says no state, #5421); the segmented control's thumb slides (mid-slide
position between the two segments) and lands under the chosen one; under prefers-reduced-motion the arc turns more
slowly instead of stopping and the thumb does not slide.

Needs the fake hub, a web page against it and Playwright for Python (a Chrome / Chromium binary via
PERSONA_E2E_CHROMIUM or --chromium, else Playwright's own):

    PORT=19070 MOCK_AGENT_MARKS=1 MOCK_TERMINAL_STREAM=1 MOCK_TERMINAL_CONTROL=1 MOCK_TERMINAL_PANES=1 python3 scripts/mock/mock_hub.py &
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:19070 npx vite --port 19071 --strictPort &)
    python3 scripts/test/web_status_visuals_e2e.py http://127.0.0.1:19071 --hub http://127.0.0.1:19070 [--shots <dir>]

Prints one line per check and a verdict; exits 1 on the first failure. --shots saves the screenshots.
"""
import argparse
import json
import os
import re
import sys
import urllib.request

from playwright.sync_api import expect, sync_playwright


# Spinners turn this many times per cycle of their keyframes (web/src/lib/busy-motion.ts SPIN_TURNS): the cycle is this
# many times the time one turn takes.
SPIN_TURNS = 100


def per_turn(duration):
    """Seconds per turn of a spinner whose cycle is `duration` ("90s")."""
    return float(duration.rstrip("s")) / SPIN_TURNS


def ok(msg):
    print("ok  " + msg, flush=True)


def px(value):
    return float(value.replace("px", ""))


def style(loc, props):
    """Computed style properties of the first match, by their CSS names."""
    return loc.first.evaluate("(e, props) => Object.fromEntries(props.map((p) => [p, getComputedStyle(e).getPropertyValue(p)]))", props)


def tx(loc):
    """The x translation of an element's computed transform."""
    t = loc.first.evaluate("e => getComputedStyle(e).transform")
    m = re.match(r"matrix\(([^)]*)\)", t)
    return float(m.group(1).split(",")[4]) if m else 0.0


def angle(loc):
    """The rotation of an element's computed transform, in degrees (0-360)."""
    import math
    t = loc.first.evaluate("e => getComputedStyle(e).transform")
    m = re.match(r"matrix\(([^)]*)\)", t)
    if not m:
        return 0.0
    a, b = [float(v) for v in m.group(1).split(",")[:2]]
    return math.degrees(math.atan2(b, a)) % 360


def open_sidebar(page, width):
    if width <= 600:
        page.locator("main").get_by_role("button", name="侧边栏").first.click()
    expect(page.locator("aside .status-mark").first).to_be_visible()


def new_page(browser, base, token, width, height, theme, reduced=False):
    ctx = browser.new_context(viewport={"width": width, "height": height}, device_scale_factor=2, has_touch=width <= 600, locale="zh-CN",
                              reduced_motion="reduce" if reduced else "no-preference")
    page = ctx.new_page()
    page.set_default_timeout(10000)
    page.goto(base + "/overview?token=" + token)
    expect(page.locator("aside")).to_be_attached()
    page.evaluate(f"localStorage.setItem('persona.theme', '{theme}')")
    page.reload()
    page.wait_for_timeout(1500)
    return ctx, page


def check_marks(page, where, theme):
    """One shape per state, read from the computed styles of the sidebar's real marks."""
    side = page.locator("aside")
    arc = side.locator('svg.status-mark[data-state="working"]')
    assert arc.count() >= 1, "a working mark"
    s = style(arc, ["animation-name", "animation-duration", "animation-iteration-count", "animation-timing-function"])
    assert s["animation-name"] == "spin-long" and abs(per_turn(s["animation-duration"]) - 0.9) < 1e-6 and s["animation-iteration-count"] == "infinite" and s["animation-timing-function"] == "linear", s
    a1 = angle(arc)
    page.wait_for_timeout(200)
    a2 = angle(arc)
    assert abs(a1 - a2) > 5, ("the working mark turns", a1, a2)
    assert arc.first.locator("circle").get_attribute("stroke-dasharray") == "28 72"
    needs = side.locator('span.status-mark[data-state="needs_you"]').first
    n = style(needs, ["box-shadow", "outline-style", "outline-width"])
    assert n["box-shadow"] != "none" and "6px" in n["box-shadow"] and n["outline-style"] == "solid", n
    done = side.locator('span.status-mark[data-state="unread"]').first
    d = style(done, ["box-shadow", "outline-style"])
    assert d["box-shadow"] != "none" and "6px" in d["box-shadow"] and d["outline-style"] == "none", d
    idle = side.locator('span.status-mark[data-state="idle"]').first
    i = style(idle, ["box-shadow", "background-color"])
    assert i["box-shadow"] == "none" and i["background-color"] != "rgba(0, 0, 0, 0)", i
    shapes = {st: side.locator(f'.status-mark[data-state="{st}"]').first.get_attribute("data-shape") for st in ("working", "needs_you", "unread", "idle")}
    assert len(set(shapes.values())) == 4, shapes
    off = side.locator('.status-mark[data-state="offline"]')
    if off.count():
        o = style(off, ["background-color", "box-shadow"])
        assert o["background-color"] == "rgba(0, 0, 0, 0)" and "inset" in o["box-shadow"], o
    # sizes come from the tokens: a dot is 7 px (6 in dense rows), the arc 9 px (8), whichever state it stands for
    for size, dot, spin in (("md", 7, 9), ("sm", 6, 8)):
        for shape, want in (("dot", dot), ("glow", dot), ("alert", dot), ("arc", spin)):
            loc = side.locator(f'.status-mark[data-size="{size}"][data-shape="{shape}"]')
            if loc.count():
                box = style(loc, ["width", "height"])  # computed, not the bounding box: the arc is turning
                assert abs(px(box["width"]) - want) < 0.6 and abs(px(box["height"]) - want) < 0.6, (size, shape, box)
    ok(f"{where} {theme}: sidebar marks: arc turning in 0.9 s (28/72), needs-you glow + ring, done glow, idle plain, four distinct shapes")


def check_folded(page, where, theme, shots):
    """A machine row: expanded it keeps the red badge; folded it shows a mark and a count per state, needs you first."""
    head = page.locator("aside button[aria-expanded]", has_text="服务器").first
    row = head.locator("xpath=..")
    expect(row.locator("[data-testid=state-tally]")).to_have_count(0)
    badge = row.locator("span.bg-red-tint")
    expect(badge).to_have_count(1)
    ok(f"{where} {theme}: an expanded machine keeps today's red needs badge")
    if shots:
        page.locator("aside").first.screenshot(path=os.path.join(shots, f"sidebar-expanded-{where}-{theme}.png"))
    head.click()
    expect(head).to_have_attribute("aria-expanded", "false")
    tally = row.locator("[data-testid=state-tally]")
    expect(tally).to_have_count(1)
    expect(row.locator("span.bg-red-tint")).to_have_count(0)
    states = tally.locator("[data-state]").evaluate_all("els => els.filter((e) => e.tagName === 'SPAN' && e.parentElement.dataset.testid === 'state-tally').map((e) => e.dataset.state)")
    assert states and states[0] == "needs_you", states
    order = ["needs_you", "working", "unread"]
    assert states == [s for s in order if s in states], states
    count = tally.locator("span[data-state]").first
    assert "mono" in style(count, ["font-family"])["font-family"].lower() or "monospace" in style(count, ["font-family"])["font-family"].lower(), style(count, ["font-family"])
    assert re.fullmatch(r"\d+", count.inner_text().strip()), count.inner_text()
    assert tally.locator(".status-mark").count() == len(states)
    ok(f"{where} {theme}: a folded machine tallies {states} (monospace counts), the lone badge is gone")
    if shots:
        page.wait_for_timeout(300)
        page.locator("aside").first.screenshot(path=os.path.join(shots, f"sidebar-folded-{where}-{theme}.png"))
    head.click()
    expect(head).to_have_attribute("aria-expanded", "true")


def pick_session(hub, token):
    req = urllib.request.Request(hub + "/api/v1/sessions", headers={"Authorization": "Bearer " + token})
    with urllib.request.urlopen(req) as r:
        d = json.load(r)
    ss = d if isinstance(d, list) else d.get("sessions", [])
    for want in ("working", "needs_you"):
        for s in ss:
            if s.get("state") == want and not s.get("assistant"):
                return want, s["machine_id"], s["session_id"]
    raise AssertionError("no working session on the fake hub")


def check_header(page, base, where, theme, shots, sess):
    state, mid, sid = sess
    page.goto(f"{base}/s/{mid}/{sid}")
    header = page.locator("main header").first
    # cindy's top bar (#5421): the Agent's mark with its "on another computer" arc before the title, and no state mark
    expect(header.locator("[data-agent-device] [data-agent-remote]").first).to_be_attached()
    assert header.locator(".status-mark").count() == 0, f"{where} {theme}: the session header draws a state mark"
    page.wait_for_timeout(600)
    ok(f"{where} {theme}: the session header shows the Agent's mark and no {state} mark")
    if shots:
        page.screenshot(path=os.path.join(shots, f"header-{where}-{theme}.png"), clip={"x": 0, "y": 0, "width": page.viewport_size["width"], "height": 70})


def check_terminal(page, base, where, theme, shots):
    page.goto(base + "/terminal")
    page.wait_for_timeout(1500)
    tree = page.locator("main")
    got = tree.locator(".status-mark").count()
    assert got >= 3, got
    assert tree.locator('svg.status-mark[data-state="working"]').count() >= 1
    ok(f"{where} {theme}: the /terminal tree draws {got} marks, including a turning arc")
    if shots:
        page.screenshot(path=os.path.join(shots, f"terminal-{where}-{theme}.png"))


def check_palette(page, base, where, theme, shots):
    page.goto(base + "/overview")
    page.wait_for_timeout(800)
    page.keyboard.press("ControlOrMeta+k")
    page.keyboard.type("agent-")
    page.wait_for_timeout(700)
    dialog = page.locator('[role="dialog"]').first
    got = dialog.locator(".status-mark").count()
    assert got >= 3, got
    ok(f"{where} {theme}: the palette's results draw {got} marks")
    if shots:
        dialog.screenshot(path=os.path.join(shots, f"palette-{where}-{theme}.png"))
    page.keyboard.press("Escape")


def check_segmented(page, base, where, theme, shots, reduced=False):
    """The settings page's text-size control: choose another segment and watch the thumb."""
    page.goto(base + "/settings")
    if page.viewport_size["width"] <= 600:  # on a phone the settings list comes first
        page.locator("main").get_by_role("button", name="通用").first.click()
    group = page.locator('[role="radiogroup"]', has=page.locator("[data-testid=seg-thumb]")).first  # not the sidebar's quick filter
    expect(group).to_be_visible()
    page.wait_for_timeout(600)
    thumb = group.locator("[data-testid=seg-thumb]")
    expect(thumb).to_have_count(1)
    radios = group.locator('[role="radio"]')
    n = radios.count()
    assert n >= 2
    cur = next(i for i in range(n) if radios.nth(i).get_attribute("aria-checked") == "true")
    target = n - 1 if cur != n - 1 else 0
    start = tx(thumb)
    assert abs(start - radios.nth(cur).evaluate("e => e.offsetLeft")) < 1.5, ("the thumb sits under the chosen segment", start)
    want = radios.nth(target).evaluate("e => e.offsetLeft")
    if reduced:
        assert style(thumb, ["transition-duration"])["transition-duration"] in ("0s", "0s, 0s, 0s"), style(thumb, ["transition-duration"])
        radios.nth(target).click()
        page.wait_for_timeout(30)
        landed = radios.nth(target).evaluate("e => e.offsetLeft")
        assert abs(tx(thumb) - landed) < 1.5, ("no slide under reduced motion", tx(thumb), landed)
        ok(f"{where} {theme}: reduced motion: the thumb lands at once, no slide")
        return
    s = style(thumb, ["transition-duration", "transition-property", "transition-timing-function"])
    assert s["transition-duration"].startswith("0.2s") and "transform" in s["transition-property"], s
    page.evaluate("document.documentElement.style.setProperty('--seg-dur', '1200ms')")  # slow enough to look at mid-slide
    radios.nth(target).click()
    page.wait_for_timeout(250)
    mid = tx(thumb)
    lo, hi = sorted((start, want))
    assert lo + 1 < mid < hi - 1, ("mid-slide the thumb is between the two segments", start, mid, want)
    if shots:
        group.locator("xpath=..").screenshot(path=os.path.join(shots, f"segmented-midslide-{where}-{theme}.png"))
    page.wait_for_timeout(1400)
    landed = radios.nth(target).evaluate("e => e.offsetLeft")  # the chosen label turns medium weight, so it may have moved a little
    assert abs(tx(thumb) - landed) < 1.5, ("it lands under the chosen segment", tx(thumb), landed)
    assert radios.nth(target).get_attribute("aria-checked") == "true"
    page.evaluate("document.documentElement.style.removeProperty('--seg-dur')")
    # keyboard and ARIA unchanged: the group is still a radiogroup of buttons that can be focused and pressed
    radios.nth(cur).focus()
    page.keyboard.press("Enter")
    expect(radios.nth(cur)).to_have_attribute("aria-checked", "true")
    ok(f"{where} {theme}: the segmented thumb slides ({start:.0f} -> {mid:.0f} -> {want:.0f} px), lands under the choice, ARIA and keys unchanged")


def check_reduced(browser, base, token, width, height, theme, shots):
    ctx, page = new_page(browser, base, token, width, height, theme, reduced=True)
    where = "wide" if width > 600 else "390"
    open_sidebar(page, width)
    arc = page.locator('aside svg.status-mark[data-state="working"]')
    s = style(arc, ["animation-duration", "animation-iteration-count"])
    assert abs(per_turn(s["animation-duration"]) - 2.4) < 1e-6 and s["animation-iteration-count"] == "infinite", s
    a1 = angle(arc)
    page.wait_for_timeout(400)
    assert abs(angle(arc) - a1) > 5, "the arc still turns under reduced motion"
    ok(f"{where} {theme}: reduced motion: the arc turns in 2.4 s instead of freezing")
    check_segmented(page, base, where, theme, shots, reduced=True)
    if shots:
        page.goto(base + "/overview")
        open_sidebar(page, width)
        page.locator("aside").first.screenshot(path=os.path.join(shots, f"sidebar-reduced-motion-{where}-{theme}.png"))
    ctx.close()


def run(browser, base, hub, token, shots):
    sess = pick_session(hub, token)
    for width, height in ((1280, 900), (390, 844)):
        where = "wide" if width > 600 else "390"
        for theme in ("light", "dark"):
            ctx, page = new_page(browser, base, token, width, height, theme)
            open_sidebar(page, width)
            check_marks(page, where, theme)
            check_folded(page, where, theme, shots)
            check_header(page, base, where, theme, shots, sess)
            check_terminal(page, base, where, theme, shots)
            if width > 600:
                check_palette(page, base, where, theme, shots)
            check_segmented(page, base, where, theme, shots)
            ctx.close()
    for width, height in ((1280, 900), (390, 844)):
        check_reduced(browser, base, token, width, height, "light", shots)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("base")
    ap.add_argument("--hub", required=True)
    ap.add_argument("--shots")
    ap.add_argument("--token", default="mock")
    ap.add_argument("--chromium", default=os.environ.get("PERSONA_E2E_CHROMIUM"))
    a = ap.parse_args()
    if a.shots:
        os.makedirs(a.shots, exist_ok=True)
    with sync_playwright() as p:
        browser = p.chromium.launch(executable_path=a.chromium) if a.chromium else p.chromium.launch()
        try:
            run(browser, a.base.rstrip("/"), a.hub.rstrip("/"), a.token, a.shots)
        finally:
            browser.close()
    print("PASS web status visuals e2e")


if __name__ == "__main__":
    try:
        main()
    except AssertionError as e:
        print("FAIL", e)
        sys.exit(1)
