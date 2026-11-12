#!/usr/bin/env python3
"""End-to-end check of the Files tree and the live Files / Git panels (#2439): on a wide screen the panel is an
expandable tree (folders first, what Git ignores dimmed, open folders kept per session), a narrow one keeps the
folder-by-folder list, the menu switches; while a panel shows, the hub is told which folders it has open (and told to
stop when it closes), and a change pushed by the hub shows up without a click: a new file in an open folder, the Git
count and the ↑N ↓N badge of the Git panel.

Needs the fake hub with MOCK_FILES_LIVE=1, a running web page against it, and Playwright for Python:

    MOCK_FILES_LIVE=1 PORT=18950 python3 scripts/mock/mock_hub.py &
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:18950 npx vite --port 18951 --strictPort &)
    python3 scripts/test/web_files_live_e2e.py http://127.0.0.1:18951 --hub http://127.0.0.1:18950 [--shots <dir>]

Prints one line per check and a verdict; exits 1 on the first failure. --shots saves light / dark / narrow screenshots.
"""
import argparse
import json
import os
import time
import urllib.request

from playwright.sync_api import expect, sync_playwright

MID, SID = "macbook", "a1f3c2d4-0000-4000-8000-000000000001"  # a chat whose folder is /Users/me/code/weather
CWD = "/Users/me/code/weather"


def ok(msg):
    print("ok  ", msg, flush=True)


def hub_json(hub, path):
    with urllib.request.urlopen(hub + path) as r:
        return json.load(r)


def watched(hub):
    """The folders (and Git) the fake hub was last told to watch, for this session's folder."""
    sets = [w for w in hub_json(hub, "/mock/fs-watch")["watches"] if w.get("cwd") == CWD]
    paths = sorted({p for w in sets for p in w.get("paths", [])})
    return paths, any(w.get("git") for w in sets)


def sync_state(hub, token):
    """(ahead, behind) of the session folder's Git status as the fake hub answers it now."""
    req = urllib.request.Request(f"{hub}/api/v1/machines/{MID}/git/status?cwd={CWD}", headers={"Authorization": f"Bearer {token}"})
    with urllib.request.urlopen(req) as r:
        st = json.load(r)
    return st["ahead"], st["behind"]


def wait_for(fn, what, seconds=5):
    end = time.time() + seconds
    while time.time() < end:
        got = fn()
        if got:
            return got
        time.sleep(0.1)
    raise AssertionError("timed out waiting for " + what)


def fresh(browser, token, viewport, scheme="light"):
    ctx = browser.new_context(viewport=viewport, color_scheme=scheme, device_scale_factor=2, locale="zh-CN")
    ctx.add_init_script(f"localStorage.setItem('persona.token', {token!r}); localStorage.setItem('persona.theme', {scheme!r})")
    page = ctx.new_page()
    page.set_default_timeout(10000)
    return ctx, page


def item(page, name):
    return page.locator("aside[aria-label=文件] [role=treeitem]", has=page.get_by_text(name, exact=True))


def open_files(page, base):
    page.goto(f"{base}/s/{MID}/{SID}")
    page.get_by_role("button", name="文件", exact=True).click()
    expect(page.locator("aside[aria-label=文件]")).to_be_visible()


def open_files_phone(page, base):
    """On a phone the session header keeps Files in its ⋯ menu."""
    page.goto(f"{base}/s/{MID}/{SID}")
    page.locator("main header").get_by_role("button", name="更多操作", exact=True).click()
    page.get_by_role("menuitem", name="文件").or_(page.get_by_role("menuitemcheckbox", name="文件")).click()
    expect(page.locator("aside[aria-label=文件]")).to_be_visible()


def wide_checks(browser, base, hub, token, shots):
    ctx, page = fresh(browser, token, {"width": 1440, "height": 900})
    open_files(page, base)
    tree = page.locator("aside[aria-label=文件] [role=tree]")
    expect(tree).to_be_visible()
    names = [t.strip() for t in tree.locator("[role=treeitem] button > span.truncate").all_text_contents()]
    folders = names[: names.index("Package.swift")]
    assert names[0] == "DerivedData" and "Sources" in folders and "README.md" in names[len(folders):], names
    ok("a wide screen opens the Files panel as a tree: folders first, then files")

    # what Git ignores is dimmed (the fake node marks DerivedData)
    dimmed = item(page, "DerivedData").locator("span.truncate")
    assert "text-ink-3" in dimmed.get_attribute("class"), dimmed.get_attribute("class")
    assert "text-ink-3" not in item(page, "Sources").locator("span.truncate").get_attribute("class")
    ok("a folder Git ignores is dimmed, the others are not")

    # open folders in place, nested
    item(page, "Sources").click()
    item(page, "App").click()
    item(page, "Models").click()
    expect(item(page, "Forecast.swift")).to_be_visible()
    levels = [int(item(page, n).get_attribute("aria-level")) for n in ("Sources", "App", "Models", "Forecast.swift")]
    assert levels == [1, 2, 3, 4], levels
    expect(item(page, "Sources")).to_have_attribute("aria-expanded", "true")
    ok("folders open in place under their parent (levels 1-4)")
    if shots:
        page.wait_for_timeout(300)
        page.screenshot(path=os.path.join(shots, "tree-wide-light.png"))

    # the hub is told the folders that show, and Git
    expected = [f"{CWD}", f"{CWD}/Sources", f"{CWD}/Sources/App", f"{CWD}/Sources/App/Models"]
    paths, git = wait_for(lambda: (lambda p: p if p[0] == expected else None)(watched(hub)), "the hub to be told the open folders")
    assert git, "Git is watched too"
    ok("the hub is told the root and every open folder (and Git)")

    # a file pushed into an open folder appears with no click
    hub_json(hub, f"/mock/fs-change?machine={MID}&dir={CWD}/Sources/App/Models&add=Daily.swift")
    expect(item(page, "Daily.swift")).to_be_visible(timeout=3000)
    ok("a new file in an open folder shows up on its own")

    # a folder that is not open is not watched: a change there reaches nobody
    sent = hub_json(hub, f"/mock/fs-change?machine={MID}&dir={CWD}/docs&add=notes.md")["sent"]
    assert sent == 0, sent
    ok("a closed folder is not watched (the hub sent nothing for it)")

    # closing a folder closes the ones in it and shrinks the watch set
    item(page, "Sources").click()
    wait_for(lambda: watched(hub)[0] == [CWD], "the watch set to shrink to the root")
    ok("closing a folder shrinks what the hub watches")
    item(page, "Sources").click()  # reopen: the nested ones were closed with it
    expect(item(page, "Models")).to_have_count(0)
    # reopen them again for the persistence check
    item(page, "App").click()
    item(page, "Models").click()
    expect(item(page, "Forecast.swift")).to_be_visible()

    # expansion is kept per session: a reload brings the same folders back
    page.reload()
    page.get_by_role("button", name="文件", exact=True).click()
    expect(item(page, "Forecast.swift")).to_be_visible()
    ok("the open folders come back after a reload")
    page.goto(f"{base}/s/{MID}/a1f3c2d4-0000-4000-8000-000000000002")
    page.get_by_role("button", name="文件", exact=True).click()
    expect(item(page, "Sources")).to_have_attribute("aria-expanded", "false")
    ok("another session of the same folder starts closed")
    page.goto(f"{base}/s/{MID}/{SID}")
    page.get_by_role("button", name="文件", exact=True).click()
    expect(item(page, "Forecast.swift")).to_be_visible()

    # the Git button: ahead / behind and the change count move when the status does
    git_button = page.locator("aside[aria-label=文件] header button[title=Git]")
    ahead, behind = sync_state(hub, token)
    expect(git_button).to_contain_text(f"↑{ahead}")
    before = git_button.inner_text()
    hub_json(hub, f"/mock/fs-change?machine={MID}&dir={CWD}&git=1")
    expect(git_button).not_to_have_text(before, timeout=3000)
    expect(git_button).to_contain_text(f"↑{ahead + 1}")
    expect(git_button).to_contain_text(f"↓{behind + 1}")
    ok("the Git button's ↑N ↓N and count follow the status")
    if shots:
        page.screenshot(path=os.path.join(shots, "tree-wide-live-light.png"))

    # dragging a tree row works as before: the same payload type, relative to nothing here (checked in files_drag_e2e)
    row = item(page, "main.swift")
    expect(row).to_have_attribute("draggable", "true")
    ok("tree rows are drag sources")

    # the Git panel: the badge in the header, and a refresh with no click
    page.locator("aside[aria-label=文件] header button[title=Git]").click()
    git_panel = page.locator("aside[aria-label=Git]")
    expect(git_panel).to_be_visible()
    expect(git_panel).to_contain_text(f"↑{ahead + 1} ↓{behind + 1}")
    hub_json(hub, f"/mock/fs-change?machine={MID}&dir={CWD}&git=1")
    expect(git_panel).to_contain_text(f"↑{ahead + 2} ↓{behind + 2}", timeout=3000)
    ok("the Git panel header shows ↑N ↓N and refreshes in place")
    if shots:
        page.screenshot(path=os.path.join(shots, "git-wide-light.png"))
    ctx.close()

    # leaving the panel releases the watch
    ctx, page = fresh(browser, token, {"width": 1440, "height": 900})
    open_files(page, base)
    wait_for(lambda: watched(hub)[0], "a watch to start")
    page.locator("aside[aria-label=文件] header button[aria-label=关闭面板]").click()
    wait_for(lambda: watched(hub) == ([], False), "the watch to be released on close")
    ok("closing the panel releases the watch")
    open_files(page, base)
    wait_for(lambda: watched(hub)[0], "a watch to start")
    page.goto(base + "/overview")
    wait_for(lambda: watched(hub) == ([], False), "the watch to be released on leaving the page")
    ok("leaving the page releases the watch")
    open_files(page, base)
    wait_for(lambda: watched(hub)[0], "a watch to start")
    ctx.close()
    wait_for(lambda: watched(hub) == ([], False), "the watch to be released when the browser goes")
    ok("closing the browser releases the watch")


def drill_checks(browser, base, hub, token, shots):
    ctx, page = fresh(browser, token, {"width": 1440, "height": 900})
    open_files(page, base)
    expect(page.locator("aside[aria-label=文件] [role=tree]")).to_be_visible()
    page.locator("aside[aria-label=文件] header button[aria-label=更多]").click()
    page.get_by_role("menuitemcheckbox", name="树状视图").or_(page.get_by_role("menuitem", name="树状视图")).click()
    expect(page.locator("aside[aria-label=文件] [role=tree]")).to_have_count(0)
    expect(page.locator("aside[aria-label=文件]").get_by_role("button", name="上一级")).to_be_visible()
    ok("the menu switches a wide screen to the folder-by-folder list (with its breadcrumb)")
    wait_for(lambda: watched(hub)[0] == [CWD], "the list to watch its folder only")
    hub_json(hub, f"/mock/fs-change?machine={MID}&dir={CWD}&add=Fresh.swift")
    expect(page.locator("aside[aria-label=文件]").get_by_text("Fresh.swift", exact=True)).to_be_visible(timeout=3000)
    ok("the folder-by-folder list refreshes by itself too")
    if shots:
        page.screenshot(path=os.path.join(shots, "drill-wide-light.png"))
    page.reload()
    page.get_by_role("button", name="文件", exact=True).click()
    expect(page.locator("aside[aria-label=文件] [role=tree]")).to_have_count(0)
    ok("the choice is remembered")
    ctx.close()


def narrow_checks(browser, base, hub, token, shots):
    ctx, page = fresh(browser, token, {"width": 390, "height": 844})
    open_files_phone(page, base)
    panel = page.locator("aside[aria-label=文件]")
    expect(panel.get_by_role("button", name="上一级")).to_be_visible()
    expect(panel.locator("[role=tree]")).to_have_count(0)
    ok("a narrow screen keeps the folder-by-folder list")
    if shots:
        page.wait_for_timeout(300)
        page.screenshot(path=os.path.join(shots, "drill-narrow-light.png"))
    ctx.close()


def dark_shots(browser, base, hub, token, shots):
    ctx, page = fresh(browser, token, {"width": 1440, "height": 900}, "dark")
    open_files(page, base)
    item(page, "Sources").click()
    item(page, "App").click()
    expect(item(page, "main.swift")).to_be_visible()
    page.wait_for_timeout(300)
    page.screenshot(path=os.path.join(shots, "tree-wide-dark.png"))
    ctx.close()
    ctx, page = fresh(browser, token, {"width": 390, "height": 844}, "dark")
    open_files_phone(page, base)
    page.wait_for_timeout(500)
    page.screenshot(path=os.path.join(shots, "drill-narrow-dark.png"))
    ctx.close()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("base")
    ap.add_argument("--hub", required=True)
    ap.add_argument("--token", default="mock")
    ap.add_argument("--shots")
    a = ap.parse_args()
    if a.shots:
        os.makedirs(a.shots, exist_ok=True)
    with sync_playwright() as p:
        browser = p.chromium.launch(executable_path=os.environ.get("PERSONA_E2E_CHROMIUM") or None)
        try:
            wide_checks(browser, a.base, a.hub, a.token, a.shots)
            drill_checks(browser, a.base, a.hub, a.token, a.shots)
            narrow_checks(browser, a.base, a.hub, a.token, a.shots)
            if a.shots:
                dark_shots(browser, a.base, a.hub, a.token, a.shots)
        finally:
            browser.close()
    print("PASS")


if __name__ == "__main__":
    main()
