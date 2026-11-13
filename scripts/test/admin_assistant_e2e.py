#!/usr/bin/env python3
"""Check the admin UI's "Assistant cards" page (#2023) against the dev server's canned admin API (/admin?mock=app).

Run the web dev server (cd web && npx vite --port <port>), then:
    python3 scripts/test/admin_assistant_e2e.py http://127.0.0.1:<port> [--shots <directory>]

Checks that the page has no switch (the cards are always on, #5509) and calls no hub API, that every example draws,
that a press and a form only change the page (no request leaves it while previewing), that the html example is its
text and no frame; wide and narrow, light and dark. Screenshots contain only the synthetic examples.
"""
import argparse
import os

from playwright.sync_api import expect, sync_playwright

EXAMPLES = ["决策", "比较", "进度", "表单", "图表", "HTML"]


def check(browser, base, width, theme, shots):
    context = browser.new_context(viewport={"width": width, "height": 1000}, color_scheme=theme, locale="zh-CN")
    page = context.new_page()
    page.set_default_timeout(15000)
    errors, requests = [], []
    page.on("pageerror", lambda e: errors.append(str(e)))
    page.on("console", lambda m: m.type == "error" and errors.append(m.text))
    page.on("request", lambda r: requests.append((r.resource_type, r.method, r.url)))
    try:
        page.goto(f"{base}/admin/assistant?mock=app&theme={theme}")
        main = page.locator("main")
        expect(main.get_by_role("heading", name="助手卡片", exact=True)).to_be_visible()
        expect(main.get_by_role("switch")).to_have_count(0)
        expect(main.get_by_text("各端支持", exact=True)).to_be_visible()
        page.wait_for_load_state("networkidle")
        if any("/api/v1/admin/assistant" in url for _, _, url in requests):
            raise AssertionError("the page asked the hub for the retired switch")
        if shots:
            page.wait_for_timeout(300)
            page.screenshot(path=os.path.join(shots, f"assistant-{width}-{theme}.png"), full_page=True)

        # From here on nothing may leave the page: the preview is local.
        start = len(requests)
        picker = main.get_by_role("radiogroup", name="选择示例")
        preview = main.locator("[data-preview]")
        card = main.locator("section").filter(has=page.get_by_role("radiogroup", name="选择示例"))
        for name in EXAMPLES:
            picker.get_by_role("radio", name=name, exact=True).click()
            expect(preview).to_have_count(1)
        expect(main.locator("iframe")).to_have_count(0)
        expect(preview.get_by_text("HTML 卡片不在预览里运行", exact=False)).to_be_visible()
        if shots:
            page.wait_for_timeout(300)
            card.screenshot(path=os.path.join(shots, f"preview-html-{width}-{theme}.png"))

        picker.get_by_role("radio", name="决策", exact=True).click()
        preview.get_by_role("button", name="B 队列去重").click()
        expect(preview.get_by_text("真实对话里会发回「选 B：队列去重」")).to_be_visible()
        if shots:
            page.wait_for_timeout(300)
            card.screenshot(path=os.path.join(shots, f"preview-decision-{width}-{theme}.png"))

        picker.get_by_role("radio", name="比较", exact=True).click()
        preview.get_by_role("button", name="详细依据", exact=True).click()
        expect(preview.get_by_text("真实对话里会打开详情页「详细依据」")).to_be_visible()
        expect(preview.get_by_text("真实对话里会发回「details」")).to_have_count(0)
        if shots:
            page.wait_for_timeout(300)
            card.screenshot(path=os.path.join(shots, f"preview-compare-{width}-{theme}.png"))

        picker.get_by_role("radio", name="表单", exact=True).click()
        preview.get_by_role("textbox").first.fill("整理读书笔记")
        preview.get_by_role("button", name="提交", exact=True).click()
        expect(preview.get_by_text("任务名称：整理读书笔记")).to_be_visible()
        if shots:
            page.wait_for_timeout(300)
            card.screenshot(path=os.path.join(shots, f"preview-form-{width}-{theme}.png"))
        preview.get_by_role("button", name="重来", exact=True).click()
        expect(preview.get_by_text("任务名称：整理读书笔记")).to_have_count(0)

        picker.get_by_role("radio", name="进度", exact=True).click()
        main.get_by_role("radiogroup", name="进度卡片的版本").get_by_role("radio", name="更新后").click()
        expect(main.locator('[data-preview="progress-updated"]')).to_have_count(1)
        picker.get_by_role("radio", name="图表", exact=True).click()
        if shots:
            page.wait_for_timeout(500)
            card.screenshot(path=os.path.join(shots, f"preview-chart-{width}-{theme}.png"))

        # The page's own buttons stay on one line (the card's rows are the shared renderer's, title and subtitle).
        for b in main.get_by_role("button").all() + main.get_by_role("radio").all():
            if not b.is_visible() or b.evaluate("e => !!e.closest('.card-ui')"):
                continue
            box = b.bounding_box()
            assert box and box["height"] <= 40, (b.inner_text(), box)
        leaked = [r for r in requests[start:] if r[0] in ("fetch", "xhr", "websocket", "eventsource", "beacon", "ping", "document") or not r[2].startswith(base)]
        assert not leaked, leaked
        assert not errors, errors
    finally:
        context.close()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("base")
    ap.add_argument("--shots")
    a = ap.parse_args()
    base = a.base.rstrip("/")
    if a.shots:
        os.makedirs(a.shots, exist_ok=True)
    with sync_playwright() as p:
        browser = p.chromium.launch()
        try:
            for width in (1280, 390):
                for theme in ("light", "dark"):
                    check(browser, base, width, theme, a.shots)
                    print(f"ok {width} {theme}")
        finally:
            browser.close()


if __name__ == "__main__":
    main()
