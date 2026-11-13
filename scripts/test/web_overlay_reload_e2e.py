#!/usr/bin/env python3
"""End-to-end check of Back after a reload with an overlay open (#5134): at phone width, open a chat's Files panel
from ⋯, reload, and the first Back leaves the chat (the panel's leftover history entry is gone), instead of doing
nothing until a second Back.

Needs the fake hub, a running web page against it, and Playwright for Python:

    PORT=18884 python3 scripts/mock/mock_hub.py &
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:18884 npx vite --port 18885 --strictPort &)
    uv run --no-project --with playwright==1.58.0 python3 scripts/test/web_overlay_reload_e2e.py http://127.0.0.1:18885

Prints one line per check and a verdict; exits 1 on the first failure.
"""
import argparse
import os
import sys

from playwright.sync_api import expect, sync_playwright

MID, SID = "macbook", "a1f3c2d4-0000-4000-8000-000000000001"  # a chat with a folder


def ok(msg):
    print("ok  " + msg, flush=True)


def run(browser, base, token):
    ctx = browser.new_context(viewport={"width": 390, "height": 844}, device_scale_factor=2, locale="zh-CN", is_mobile=True, has_touch=True)
    ctx.add_init_script(f"localStorage.setItem('persona.token', {token!r})")
    page = ctx.new_page()
    page.set_default_timeout(15000)
    chat = f"{base}/s/{MID}/{SID}"
    page.goto(base + "/overview")
    page.goto(chat)
    page.locator("main header").get_by_role("button", name="更多操作", exact=True).click()
    page.get_by_role("menuitem", name="文件").or_(page.get_by_role("menuitemcheckbox", name="文件")).click()
    panel = page.locator("aside[aria-label=文件]")
    expect(panel).to_be_visible()
    here = page.url  # an old /s/ link has become its /t/ by now
    ok("Files opens from ⋯ at 390 wide")

    page.reload()
    page.locator("main header").get_by_role("button", name="更多操作", exact=True).wait_for()
    expect(panel).to_have_count(0)
    ok("the reload closed the panel")
    page.go_back()
    try:
        page.wait_for_url(lambda u: u != here, timeout=3000)
    except Exception:
        marked = page.evaluate("() => Boolean(history.state && history.state.personaOverlay)")
        raise AssertionError(f"the first Back stayed on the chat ({page.url.removeprefix(base)}, overlay marker still there: {marked})")
    ok("the first Back leaves the chat: " + page.url.removeprefix(base))
    ctx.close()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("base")
    ap.add_argument("--token", default="mock")
    a = ap.parse_args()
    with sync_playwright() as p:
        browser = p.chromium.launch(executable_path=os.environ.get("PERSONA_E2E_CHROMIUM") or None)
        try:
            run(browser, a.base.rstrip("/"), a.token)
        except Exception as e:  # one verdict line, whatever failed
            print(f"FAIL {e}", flush=True)
            sys.exit(1)
        finally:
            browser.close()
    print("PASS web_overlay_reload_e2e", flush=True)


if __name__ == "__main__":
    main()
