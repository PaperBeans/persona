#!/usr/bin/env python3
"""End-to-end check of the web chat's streaming (#1963): the assistant's reply fades in word by word while its deltas
arrive and is plain Markdown once it is written (no fade spans left); a working Claude's status bar (cindy's, #5312) says
"Thinking…" while the status line does, then the state of the turn, the time in the work summary's format and either the
tokens counting up or, once measured, the latest speed (never both), then says Done, lingers and fades out when the turn ends.

Needs a running web page against the fake hub with MOCK_STREAM=1 MOCK_LIVE_STATUS=1 (the status bar's run starts
when the page subscribes to the widget session) and Playwright for Python (it drives the installed Chrome):

    MOCK_STREAM=1 MOCK_LIVE_STATUS=1 MOCK_LIVE_STATUS_TICK=1.5 PORT=18821 python3 scripts/mock/mock_hub.py &
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:18821 npx vite --port 5321 --strictPort &)
    python3 scripts/test/web_stream_e2e.py http://127.0.0.1:5321 --hub http://127.0.0.1:18821 [--shots <dir>]

Prints one line per check and a verdict; exits 1 on the first failure. Runs wide light, wide dark and phone width
(390) light; --shots saves the reply mid-stream and the status bar thinking / with its speed for each.
"""
import argparse
import os
import re
import sys
import time
import urllib.parse
import urllib.request

from playwright.sync_api import sync_playwright

WIDGET = "/s/macbook/a1f3c2d4-0000-4000-8000-000000000001"
RUNS = [("wide", "light"), ("wide", "dark"), ("narrow", "light")]
SIZES = {"wide": {"width": 1280, "height": 820}, "narrow": {"width": 390, "height": 844}}
REPLY = ("先说结论：小组件的刷新预算够用。**时间线**按小时排，降雨概率的格式化也对上了。\n\n"
         "- 主屏幕小组件每小时刷新一次，耗电在预算之内\n- 锁屏小组件沿用同一条时间线\n\n"
         "```swift\nlet entry = WeatherEntry(date: .now, rain: 0.3)\n```\n\n最后一句：测试全部通过，可以合并了")

# Samples every frame while the reply streams: how many fade spans there were at most, the lowest opacity one of them
# had (a word mid-fade), and whether one ever sat inside a code block.
WATCH = """() => {
  window.__fade = { max: 0, minOpacity: 1, inPre: false };
  const tick = () => {
    const words = document.querySelectorAll("main .prose-j .stream-word, main .prose-j [data-wf-key]");
    const f = window.__fade;
    f.max = Math.max(f.max, words.length);
    for (const w of words) {
      f.minOpacity = Math.min(f.minOpacity, parseFloat(getComputedStyle(w).opacity));
      if (w.closest("pre")) f.inPre = true;
    }
    window.__fadeRaf = requestAnimationFrame(tick);
  };
  tick();
}"""


def ok(msg):
    print("ok  " + msg, flush=True)


def fail(msg):
    print("FAIL " + msg, flush=True)
    sys.exit(1)


def wait(page, js, what, tag, timeout=15):
    end = time.time() + timeout
    while time.time() < end:
        if page.evaluate(js):
            return
        page.wait_for_timeout(50)
    fail(f"{tag}: {what} (timed out after {timeout}s)")


def bar(page):
    return page.evaluate("""() => {
      const b = document.querySelector("[data-running-status]");
      if (!b) return null;
      const t = (s) => b.querySelector(s)?.textContent ?? null;
      return { phase: b.dataset.runningStatus, verb: t("[data-running-verb]"), elapsed: t("[data-running-elapsed]"),
               tokens: t("[data-running-tokens]"), rate: t("[data-running-rate]"), done: !!b.querySelector("[data-running-done]") };
    }""")


def shot(page, shots, name, tag):
    if shots:
        page.screenshot(path=os.path.join(shots, f"stream-{tag}-{name}.png"))


def check_reply(page, base, hub, shots, tag):
    page.goto(base + "/")
    page.wait_for_selector("main textarea", timeout=15000)
    page.wait_for_timeout(800)  # subscribed
    page.evaluate(WATCH)
    last = f"（{tag}）。"  # each run's reply ends its own way: the earlier runs' replies are in the conversation too
    q = urllib.parse.urlencode({"stream": "1", "steps": "50", "ms": "80", "text": REPLY + last})
    urllib.request.urlopen(f"{hub}/mock/say?{q}", timeout=5).read()
    wait(page, "() => document.querySelectorAll('main .prose-j .stream-word').length > 3", "the reply's words fade in while it streams", tag)
    page.wait_for_timeout(1500)
    shot(page, shots, "reply-streaming", tag)
    wait(page, f"() => [...document.querySelectorAll('main .prose-j')].some((p) => p.textContent.includes({last!r}) && !p.querySelector('[data-wf-key]'))",
         "the written reply is plain Markdown", tag, timeout=20)
    page.wait_for_timeout(500)
    f = page.evaluate("() => { cancelAnimationFrame(window.__fadeRaf); return window.__fade; }")
    if f["max"] < 10:
        fail(f"{tag}: too few fading words seen while streaming: {f}")
    if f["minOpacity"] >= 0.95:
        fail(f"{tag}: no word was seen mid-fade: {f}")
    if f["inPre"]:
        fail(f"{tag}: a fade span sat inside a code block: {f}")
    ok(f"{tag}: reply faded in word by word (up to {f['max']} spans, lowest opacity {f['minOpacity']:.2f}), none in code")
    left = page.evaluate("() => document.querySelectorAll('main [data-wf-key], main .stream-word, main [data-stream-marker]').length")
    if left:
        fail(f"{tag}: {left} fade spans left after the reply was written")
    if not page.evaluate("() => !!document.querySelector('main .prose-j pre code .hljs-keyword')"):
        fail(f"{tag}: the swift block is not highlighted")
    ok(f"{tag}: no fade spans left, code highlighted")
    shot(page, shots, "reply-done", tag)


def check_bar(page, base, shots, tag):
    page.goto(base + WIDGET)
    seen = {"thinking": False, "words": set(), "tokens": [], "rates": [], "both": [], "phases": [], "elapsed": set(), "done": False}
    end = time.time() + 40
    shot_thinking = shot_rate = False
    while time.time() < end:
        b = bar(page)
        if b is None:
            if "fading" in seen["phases"]:
                break
            page.wait_for_timeout(50)
            continue
        if not seen["phases"] or seen["phases"][-1] != b["phase"]:
            seen["phases"].append(b["phase"])
        if b["done"]:
            seen["done"] = seen["done"] or b["verb"] == "已完成"
        elif b["verb"] == "正在思考…":
            seen["thinking"] = True
            if not shot_thinking:
                page.wait_for_timeout(200)
                shot(page, shots, "bar-thinking", tag)
                shot_thinking = True
        elif b["verb"]:
            seen["words"].add(b["verb"])
        if b["tokens"] and b["rate"]:
            seen["both"].append((b["tokens"], b["rate"]))
        if b["tokens"] and (not seen["tokens"] or seen["tokens"][-1] != b["tokens"]):
            seen["tokens"].append(b["tokens"])
        if b["rate"] and (not seen["rates"] or seen["rates"][-1] != b["rate"]):
            seen["rates"].append(b["rate"])
            if not shot_rate:
                shot(page, shots, "bar-rate", tag)
                shot_rate = True
        if b["elapsed"]:
            seen["elapsed"].add(b["elapsed"])
        page.wait_for_timeout(50)
    if not seen["thinking"]:
        fail(f"{tag}: the bar never said 正在思考…: {seen}")
    if any("Frosting" in w or "Moonwalking" in w for w in seen["words"]):
        fail(f"{tag}: the bar showed the spinner's verb instead of a state: {seen['words']}")
    if seen["both"]:
        fail(f"{tag}: the bar showed the tokens and the speed at once: {seen['both'][:3]}")
    if not seen["tokens"] or not seen["rates"] or not all(r.endswith(" tok/s") for r in seen["rates"]):
        fail(f"{tag}: the tokens before a measurement and the speed after it should both show: {seen['tokens']} / {seen['rates']}")
    # the tween shows numbers between the pushed ones
    if not set(seen["tokens"]) - {"400 tok", "900 tok", "1.2k tok", "2.3k tok", "3.4k tok", "4.8k tok", "22.8k tok"}:
        fail(f"{tag}: the token count jumped instead of easing: {seen['tokens']}")
    if len(seen["elapsed"]) < 4 or not all(re.fullmatch(r"\d+s|\d+m \d+s", e) for e in seen["elapsed"]):
        fail(f"{tag}: the time did not tick in the work summary's format: {sorted(seen['elapsed'])}")
    ok(f"{tag}: bar said 正在思考…, then {sorted(seen['words'])}, tokens eased to {seen['tokens'][-1]}, then {seen['rates']}, never both; time ticked")
    if not seen["done"] or seen["phases"][-2:] != ["linger", "fading"] or bar(page) is not None:
        fail(f"{tag}: the bar did not say Done, linger, fade and go: {seen['phases']} done={seen['done']}")
    ok(f"{tag}: after the turn the bar said 已完成, lingered, faded out and went ({' → '.join(seen['phases'])})")


def run(browser, base, hub, token, size, theme, shots):
    tag = f"{size}-{theme}"
    ctx = browser.new_context(viewport=SIZES[size], device_scale_factor=2, has_touch=size == "narrow")
    page = ctx.new_page()
    page.goto(base + "/?token=" + token)
    page.wait_for_selector("main textarea", timeout=15000)  # paired
    page.evaluate(f"localStorage.setItem('persona.theme', '{theme}'); localStorage.removeItem('persona.replyMotion')")
    check_reply(page, base, hub, shots, tag)
    check_bar(page, base, shots, tag)
    ctx.close()


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("base", help="the web page, e.g. http://127.0.0.1:5321")
    ap.add_argument("--hub", required=True, help="the fake hub, e.g. http://127.0.0.1:18821")
    ap.add_argument("--token", default="mock")
    ap.add_argument("--shots", help="directory for screenshots")
    a = ap.parse_args()
    if a.shots:
        os.makedirs(a.shots, exist_ok=True)
    with sync_playwright() as p:
        browser = p.chromium.launch(channel="chrome")
        try:
            for size, theme in RUNS:
                run(browser, a.base.rstrip("/"), a.hub.rstrip("/"), a.token, size, theme, a.shots)
        finally:
            browser.close()
    print("PASS web_stream_e2e", flush=True)


if __name__ == "__main__":
    main()
