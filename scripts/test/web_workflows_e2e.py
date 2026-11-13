#!/usr/bin/env python3
"""End-to-end check of how the web shows a background workflow (#2935): the agent's row in the sidebar says which run
it waits for and how far it got, the top bar's Background tasks button opens its side panel (#5421, cindy's tab): a row per run
under Running / Completed, each opening the run's detail (#3434: counts, a progress bar, one block
per phase, the stopped one's error), the running phase's block lists its agents, another phase opens on a click, and the
agent rows only read (#5030: a workflow agent opens no conversation of its own); the task card says the same in a line. These runs carry no phase_stats (an older
hub): the blocks come from the agents listed. With MOCK_WORKFLOWS=legacy (a hub without the workflows field) the row only
says "Running in the background". The cards in the chat itself: scripts/test/web_workflow_cards_e2e.py.

    MOCK_WORKFLOWS=1 MOCK_TASKS=1 PORT=18835 python3 scripts/mock/mock_hub.py &
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:18835 npx vite --port 5335 --strictPort &)
    python3 scripts/test/web_workflows_e2e.py http://127.0.0.1:5335 [--legacy] [--shots <dir>]

Prints one line per check and a verdict; exits 1 on the first failure. Desktop (1280) and phone (390), light and dark.
"""
import argparse
import os
import sys

from playwright.sync_api import expect, sync_playwright

WIDGET = "/s/macbook/a1f3c2d4-0000-4000-8000-000000000001"
TASK = "修小组件横屏崩溃"
SIZES = {"wide": {"width": 1280, "height": 820}, "narrow": {"width": 390, "height": 844}}
OVERFLOW = "() => document.documentElement.scrollWidth - document.documentElement.clientWidth"


def ok(msg):
    print("ok  " + msg, flush=True)


def fail(msg):
    print("FAIL " + msg, flush=True)
    sys.exit(1)


def run(browser, base, token, size, theme, shots, legacy):
    tag = f"{size}/{theme}"
    ctx = browser.new_context(viewport=SIZES[size], device_scale_factor=2, has_touch=size == "narrow", color_scheme=theme, locale="zh-CN")
    page = ctx.new_page()
    page.set_default_timeout(15000)
    errors = []
    page.on("pageerror", lambda e: errors.append(str(e)))
    page.goto(base + "/overview?token=" + token)
    page.wait_for_load_state("networkidle")
    page.evaluate(f"localStorage.setItem('persona.theme', '{theme}')")

    def shot(name):
        if shots:
            page.wait_for_timeout(350)
            page.screenshot(path=os.path.join(shots, f"workflows-{'legacy-' if legacy else ''}{size}-{theme}-{name}.png"))

    # the task card on the home page
    page.goto(base + "/")
    card = page.locator("div[role=button]", has_text=TASK).first
    expect(card).to_be_visible()
    line = card.locator("[data-workflow-line]")
    if legacy:
        expect(line).to_contain_text("后台运行中")
    else:
        expect(line).to_contain_text("widget-audit")
        expect(line).to_contain_text("Verify 2/2")
        expect(line).to_contain_text("4/6+")
    ok(f"{tag}: the task card says the run")
    card.scroll_into_view_if_needed()
    shot("task")
    if shots:
        card.screenshot(path=os.path.join(shots, f"workflows-{'legacy-' if legacy else ''}{size}-{theme}-task-card.png"))

    page.goto(base + WIDGET)
    if size == "wide":
        row = page.locator("nav a[aria-current=page], aside a[aria-current=page]").first
        expect(row.locator("[data-workflow-line]")).to_contain_text("后台运行中" if legacy else "4/6+ · widget-audit")
        ok(f"{tag}: the sidebar row says it waits for the run")
    button = page.locator("main header").get_by_role("button", name="后台任务", exact=False).first
    expect(button).to_be_visible()
    shot("chat")
    button.click()
    panel = page.locator('aside[aria-label="后台任务"]')
    expect(panel).to_be_visible()
    if legacy:
        expect(panel).to_contain_text("widget-audit")  # a background task like the others
        expect(panel.locator("[data-bg-run]")).to_have_count(0)
        ok(f"{tag}: an older hub's run is a background task")
        shot("panel")
    else:
        # the loop first, then the running run and the shell task under Running; the stopped run under Completed
        running_section = panel.locator('[data-background-section="running"]')
        expect(running_section.locator("[data-bg-loop]")).to_have_count(1)
        expect(running_section.locator('[data-bg-run="wdemo1"]')).to_contain_text("widget-audit")
        expect(running_section.locator('[data-bg-run="wdemo1"] [data-bg-meta]')).to_contain_text("4/6")
        expect(running_section).to_contain_text("跑小组件快照测试")  # the shell task still listed
        expect(panel.locator('[data-background-section="completed"] [data-bg-run="wdemo0"]')).to_have_count(1)
        ok(f"{tag}: the panel lists the loop, the running run and the task under Running, the stopped run under Completed")
        shot("panel")
        panel.locator('[data-bg-run="wdemo0"] [data-bg-row-button]').click()
        expect(panel.locator('[data-workflow-card="killed"]')).to_contain_text("Error: Workflow aborted")
        panel.get_by_role("button", name="返回").click()
        panel.locator('[data-bg-run="wdemo1"] [data-bg-row-button]').click()
        running = panel.locator('[data-workflow-card="running"]')
        expect(running).to_contain_text("widget-audit")
        expect(running.locator("[data-workflow-counts]")).to_have_text("4/6+")
        expect(running.locator('[data-tally="running"]')).to_have_text("2 在跑")
        ok(f"{tag}: a run's row opens its detail, the stopped one with its error")
        shot("detail")
        # the phase it is in is open, in start order; the one before it folded until clicked
        verify = running.locator('[data-phase="Verify"]')
        expect(verify).to_have_attribute("data-phase-current", "true")
        expect(verify.locator("[data-agent]")).to_have_count(3)
        expect(verify.locator("[data-agent]").first).to_contain_text("verify:rain#0")
        audit = running.locator('[data-phase="Audit"]')
        expect(audit.locator("[data-agent]")).to_have_count(0)
        audit.get_by_role("button").first.click()
        expect(audit.locator("[data-agent]")).to_have_count(3)
        expect(audit.locator('[data-agent="wa2"]')).to_have_attribute("title", "降雨概率在 0.5% 以下时显示成 0%，和 App 里不一致。")
        if page.evaluate(OVERFLOW) > 0:
            fail(f"{tag}: the page scrolls sideways (agents open)")
        ok(f"{tag}: the detail opens its agents with their last words")
        shot("agents")
        if running.locator("[data-agent] button, [data-agent] a, [data-agent][role=button]").count():
            fail(f"{tag}: the agent rows should open nothing")
        audit.locator('[data-agent="wa2"]').click()
        expect(panel).to_have_count(1)  # nothing opened in its place
        ok(f"{tag}: the agent rows only read")
    if errors:
        fail(f"{tag}: page errors: {errors[:3]}")
    ctx.close()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("base")
    ap.add_argument("--shots")
    ap.add_argument("--token", default="mock")
    ap.add_argument("--legacy", action="store_true", help="the fake hub runs with MOCK_WORKFLOWS=legacy")
    a = ap.parse_args()
    if a.shots:
        os.makedirs(a.shots, exist_ok=True)
    with sync_playwright() as p:
        browser = p.chromium.launch(channel="chrome")
        try:
            for size in SIZES:
                for theme in ("light", "dark"):
                    run(browser, a.base.rstrip("/"), a.token, size, theme, a.shots, a.legacy)
        finally:
            browser.close()
    print("PASS web workflows e2e")


if __name__ == "__main__":
    main()
