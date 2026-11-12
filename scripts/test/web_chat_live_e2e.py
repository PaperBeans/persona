#!/usr/bin/env python3
"""End-to-end check that an open agent page shows what the agent writes without a reload (#3036): in the chat view,
after the page was switched Terminal ⇄ Chat a few times (the chat stays mounted under the terminal, docs/web/web.md "Terminal ⇄ chat"),
and when a push carries another `history` token (a rewound or rewritten transcript: the page starts over and must come
back with everything, then keep following), and after the socket was dropped and came back.

Needs a running web page against the fake hub and Playwright for Python (it drives the installed Chrome, or the
bundled Chromium when there is none):

    MOCK_TERMINAL_STREAM=1 MOCK_TERMINAL_CONTROL=1 MOCK_TERMINAL_PANES=1 PORT=18798 python3 scripts/mock/mock_hub.py &
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:18798 npx vite --port 5298 --strictPort &)
    python3 scripts/test/web_chat_live_e2e.py http://127.0.0.1:5298 --hub http://127.0.0.1:18798 [--shots <dir>]

The fake hub's /mock/say adds a reply (after a message of the user's, so each reply is the last word of its own turn and
does not fold into a work line) to every conversation a page has subscribed to; &history=<token> also moves the
transcript's history token. Prints one line per check and a verdict; exits 1 on the first failure.
"""
import argparse
import os
import sys
import urllib.parse
import urllib.request

from playwright.sync_api import sync_playwright

AGENT = ("macbook", "a1f3c2d4-0000-4000-8000-000000000001")
# the header's one button to the other view (#5421): its name is the view it goes to
WAIT = 8000
CHAT = "[data-chat-scroll]"  # the conversation itself: the sidebar and header repeat titles
FIRST = "给天气 App 加一个主屏幕小组件"  # the first message of that conversation


def ok(msg):
    print("ok  " + msg, flush=True)


def fail(msg):
    print("FAIL " + msg, flush=True)
    sys.exit(1)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("base")
    ap.add_argument("--hub", required=True)
    ap.add_argument("--token", default="mock")
    ap.add_argument("--shots")
    a = ap.parse_args()

    def hub(path, **q):
        urllib.request.urlopen(a.hub + path + ("?" + urllib.parse.urlencode(q) if q else "")).read()

    def say(user, text, **q):
        hub("/mock/say", user=user, text=text, **q)

    with sync_playwright() as p:
        try:
            browser = p.chromium.launch(channel="chrome")
        except Exception:
            browser = p.chromium.launch()
        page = browser.new_context(viewport={"width": 1280, "height": 820}, locale="zh-CN").new_page()

        def shows(text, what):
            try:
                page.locator(CHAT).get_by_text(text).first.wait_for(timeout=WAIT)
            except Exception:
                fail(f"{what}: {text!r} did not appear without a reload")
            ok(f"{what}: {text!r} appeared without a reload")

        def view(name):
            button = page.locator("main header").get_by_role("button", name=name, exact=True)
            if button.count():
                button.click()
            page.wait_for_timeout(500)

        try:
            hub("/mock/reset")
            page.goto(a.base + "/?token=" + a.token)
            page.wait_for_timeout(800)
            for attempt in range(3):  # a cold dev server optimizes its dependencies and reloads the page once
                page.goto(f"{a.base}/s/{AGENT[0]}/{AGENT[1]}")
                try:
                    page.locator(CHAT).get_by_text(FIRST).first.wait_for(timeout=15000)
                    break
                except Exception:
                    if attempt == 2:
                        fail("the agent page did not open")
            page.wait_for_timeout(1500)  # the page's own subscription is on the socket

            say("第一问", "第一条直播回复")
            shows("第一条直播回复", "chat view")

            for _ in range(3):
                view("终端")
                view("对话")
            say("切换之后的问题", "切换之后的直播回复")
            shows("切换之后的直播回复", "after switching to the terminal and back three times")

            view("终端")
            say("在终端页时的问题", "在终端页时的直播回复")
            view("对话")
            shows("在终端页时的直播回复", "pushed while the terminal was shown")

            # The transcript's history token moves (a branch was abandoned, the file was rewritten): the page starts
            # over from the hub, so the earlier turns must be there again and the push must not be lost.
            say("令牌变了的问题", "令牌变了的直播回复", history="mock-1.r1")
            shows("令牌变了的直播回复", "history token changed")
            shows(FIRST, "history token changed (the first message is back)")
            page.wait_for_timeout(1500)  # the page subscribed again
            say("令牌变了之后的问题", "令牌变了之后的直播回复")
            shows("令牌变了之后的直播回复", "after the page started over")

            # The hub drops a client that cannot keep up (and a network can drop any): the socket closes, and the page
            # subscribes again from what it holds when the new one is up.
            hub("/mock/down", s=2)
            page.wait_for_timeout(6000)
            say("断线重连之后的问题", "断线重连之后的直播回复")
            shows("断线重连之后的直播回复", "after the socket was dropped and came back")

            if a.shots:
                os.makedirs(a.shots, exist_ok=True)
                page.screenshot(path=os.path.join(a.shots, "chat-live.png"))
        finally:
            try:
                hub("/mock/reset")
            finally:
                browser.close()
    print("PASS web_chat_live_e2e", flush=True)


if __name__ == "__main__":
    main()
