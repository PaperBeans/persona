#!/usr/bin/env python3
"""End-to-end check of comments (comments on picked words and on lines of code, #2437) in the web chat.

Words in the transcript: selecting some shows a "Comment" pill under the selection (the C key does the same); it puts a
card above the input to write the note in (Enter keeps it, Esc takes a new card back); the words stay lit in the
transcript with the card's number beside them, through a scroll, until the card goes or the message is sent; the
message goes out with `Comment N` / `Selected text:` and the note in the `> ` block in front of it, and the sent bubble
draws a card per comment.

Lines of code: the number in the gutter of a file (and of a Git diff) turns into a + under the pointer, a drag picks
several lines, a box takes the note (Ctrl+Enter adds, Esc cancels); the card above the input carries the location, and
the message carries `Code comment N`, `File: path:lines`, `Diff: …` and the code in a fence; the card goes back to the file
or the diff at its lines.

Needs the fake hub with files (the weather folder), a running web page against it, and Playwright for Python (the
installed Chrome, or the browser PERSONA_E2E_CHROMIUM points at):

    MOCK_FILES=1 MOCK_TERMINAL_STREAM=1 MOCK_TERMINAL_CONTROL=1 MOCK_TERMINAL_PANES=1 PORT=19040 python3 scripts/mock/mock_hub.py &
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:19040 npx vite --port 19041 --strictPort &)
    python3 scripts/test/web_comments_e2e.py http://127.0.0.1:19041 [--shots <dir>]

Prints one line per check and a verdict; exits 1 on the first failure. Runs light and dark, at desktop and phone widths;
--shots saves each.
"""
import argparse
import json
import os
import re
import sys

from playwright.sync_api import expect, sync_playwright

SIZES = {"wide": {"width": 1280, "height": 900}, "narrow": {"width": 390, "height": 844}}
AGENT = "/s/macbook/a1f3c2d4-0000-4000-8000-000000000001"  # a chat in the weather folder
SEND = re.compile(r"^(发送|排队发送)$")  # the round button queues while the agent works (#5324)
WORDS = "接下来把中号布局的降雨条做成和 App 里一样的样式"
PARAGRAPH = "小组件的时间线已经能按小时刷新了"


def ok(msg):
    print("ok  " + msg, flush=True)


def fail(msg):
    print("FAIL " + msg, flush=True)
    sys.exit(1)


def check(cond, msg):
    if not cond:
        fail(msg)


def shot(page, shots, name):
    if shots:
        page.screenshot(path=os.path.join(shots, name + ".png"))


def fresh(browser, base, size, theme):
    ctx = browser.new_context(viewport=SIZES[size], device_scale_factor=2, locale="zh-CN", color_scheme=theme)
    ctx.add_init_script(f"localStorage.setItem('persona.token', 'mock'); localStorage.setItem('persona.theme', '{theme}')")
    page = ctx.new_page()
    page.set_default_timeout(15000)
    page.goto(base + AGENT)
    expect(page.get_by_text(PARAGRAPH).first).to_be_visible()
    return ctx, page


def pick(page, words, within=PARAGRAPH):
    """Selects `words` inside the transcript's paragraph that holds `within`, as a drag would (no key focus moves)."""
    found = page.evaluate(
        """([words, within]) => {
          const sc = document.querySelector('[data-chat-scroll]');
          const walker = document.createTreeWalker(sc, NodeFilter.SHOW_TEXT);
          for (let n = walker.nextNode(); n; n = walker.nextNode()) {
            if (!n.data.includes(within)) continue;
            const at = n.data.indexOf(words);
            if (at < 0) continue;
            const r = document.createRange();
            r.setStart(n, at); r.setEnd(n, at + words.length);
            document.activeElement && document.activeElement.blur(); // a drag over the page takes the focus off the input
            const s = getSelection(); s.removeAllRanges(); s.addRange(r);
            n.parentElement.scrollIntoView({ block: 'center' });
            return true;
          }
          return false;
        }""",
        [words, within],
    )
    check(found, f"could not select {words!r} in the transcript")


def payloads(page):
    out = []
    page.on("request", lambda r: out.append(r.post_data or "") if r.method == "POST" else None)
    return out


def sent(posts, needle):
    return next((p for p in posts if needle in p), "")


def text_of(body):
    try:
        return json.loads(body).get("text", body)
    except Exception:
        return body


def transcript_case(browser, base, size, theme, shots):
    tag = f"{size}/{theme} transcript"
    ctx, page = fresh(browser, base, size, theme)
    posts = payloads(page)
    pill = page.locator("[data-comment-pill]")

    pick(page, WORDS)
    expect(pill).to_be_visible()
    check("评论" in pill.inner_text(), f"{tag}: the pill does not say 评论")
    page.wait_for_timeout(250)
    shot(page, shots, f"comments-{size}-{theme}-pill")
    ok(f"{tag}: selecting words shows the 评论 pill")

    # the C key does what the pill does, the note is written in the new card
    page.keyboard.press("c")
    card = page.locator("[data-comment-card='1']")
    expect(card).to_be_visible()
    note = card.locator("textarea")
    expect(note).to_be_focused()
    expect(pill).to_have_count(0)
    note.fill("降雨条的颜色也要和 App 一样")
    page.keyboard.press("Enter")
    expect(note).to_have_count(0)
    expect(card).to_contain_text("降雨条的颜色也要和 App 一样")
    expect(card).to_contain_text(WORDS[:12])
    ok(f"{tag}: C adds the card, Enter keeps its note")

    mark = page.locator("[data-comment-mark='1']")
    expect(mark).to_have_count(1)
    expect(mark.locator("button")).to_contain_text("1")
    page.wait_for_timeout(300)
    shot(page, shots, f"comments-{size}-{theme}-card")

    # the lit words stay on the words through a scroll and a re-render
    def offset():
        return page.evaluate(
            """([words, within]) => {
              const walker = document.createTreeWalker(document.querySelector('[data-chat-scroll]'), NodeFilter.SHOW_TEXT);
              for (let n = walker.nextNode(); n; n = walker.nextNode()) {
                if (!n.data.includes(within) || n.data.indexOf(words) < 0) continue;
                const r = document.createRange(); const at = n.data.indexOf(words);
                r.setStart(n, at); r.setEnd(n, at + words.length);
                const a = r.getClientRects()[0];
                const m = document.querySelector("[data-comment-mark='1'] span").getBoundingClientRect();
                return [Math.round(m.left - a.left), Math.round(m.top - a.top)];
              }
              return null;
            }""",
            [WORDS, PARAGRAPH],
        )

    before = offset()
    check(before is not None and abs(before[0]) <= 2 and abs(before[1]) <= 2, f"{tag}: the lit words are not on the words ({before})")
    page.evaluate("document.querySelector('[data-chat-scroll]').scrollBy(0, -120)")
    page.wait_for_timeout(250)
    after = offset()
    check(after is not None and abs(after[0]) <= 2 and abs(after[1]) <= 2, f"{tag}: the lit words moved off the words after a scroll ({after})")
    ok(f"{tag}: the words stay lit and numbered through a scroll")

    # a second comment: Esc takes a new card back, and its mark with it
    pick(page, "不在这台电脑上", within="不在这台电脑上")
    expect(pill).to_be_visible()
    pill.click()
    expect(page.locator("[data-comment-card='2']")).to_be_visible()
    expect(page.locator("[data-comment-mark='2']")).to_have_count(1)
    page.keyboard.type("先不写了")
    page.keyboard.press("Escape")
    expect(page.locator("[data-comment-card='2']")).to_have_count(0)
    expect(page.locator("[data-comment-mark='2']")).to_have_count(0)
    ok(f"{tag}: Esc cancels a new comment, card and mark")

    # a second one kept, then the first removed: the other becomes 1
    pick(page, "不在这台电脑上", within="不在这台电脑上")
    pill.click()
    page.keyboard.type("这句没有必要")
    page.keyboard.press("Enter")
    expect(page.locator("[data-comment-card]")).to_have_count(2)
    expect(page.locator("[data-comment-mark]")).to_have_count(2)
    page.locator("[data-comment-card='1']").get_by_role("button", name="移除评论").click()
    expect(page.locator("[data-comment-card]")).to_have_count(1)
    expect(page.locator("[data-comment-card='1']")).to_contain_text("这句没有必要")
    expect(page.locator("[data-comment-mark]")).to_have_count(1)
    expect(page.locator("[data-comment-mark='1']")).to_have_count(1)
    ok(f"{tag}: removing a card takes its mark and renumbers the rest")

    # back to the first: add it again, then send both with a message
    pick(page, WORDS)
    pill.click()
    page.keyboard.type("降雨条的颜色也要和 App 一样")
    page.keyboard.press("Enter")
    expect(page.locator("[data-comment-card]")).to_have_count(2)
    box = page.locator("textarea:not([aria-label^='评论'])").last
    box.fill("按这些评论改")
    page.wait_for_timeout(300)
    shot(page, shots, f"comments-{size}-{theme}-two")
    page.get_by_role("button", name=SEND).click()
    page.wait_for_timeout(1200)
    body = text_of(sent(posts, "按这些评论改"))
    for want in ("> Comment 1", "> Selected text:", f"> > {WORDS}", "> 这句没有必要", "> Comment 2", "> 降雨条的颜色也要和 App 一样", "按这些评论改"):
        check(want in body, f"{tag}: the message lacks {want!r}: {body[:400]!r}")
    check(body.index("Comment 1") < body.index("Comment 2") < body.index("按这些评论改"), f"{tag}: the blocks are out of order")
    ok(f"{tag}: sent as numbered blocks in front of the words")
    expect(page.locator("[data-comment-card]")).to_have_count(0)
    expect(page.locator("[data-comment-mark]")).to_have_count(0)
    ok(f"{tag}: sending takes the cards and the lit words away")
    bubble = page.locator("[data-user-bubble]").filter(has_text="按这些评论改").last
    bubble.scroll_into_view_if_needed()
    expect(bubble.locator("[data-comment-block]")).to_have_count(2)
    check("Selected text" not in bubble.inner_text() and ">" not in bubble.inner_text(), f"{tag}: the bubble shows the raw block")
    page.wait_for_timeout(300)
    shot(page, shots, f"comments-{size}-{theme}-sent")
    ok(f"{tag}: the bubble draws a card per comment")
    overflow = page.evaluate("document.documentElement.scrollWidth - document.documentElement.clientWidth")
    check(overflow <= 0, f"{tag}: the page scrolls sideways by {overflow}px")
    ctx.close()


def open_git(page, size):
    """The Git panel: from the Files panel's button on a wide screen, from the header's menu on a phone."""
    if size == "wide":
        page.get_by_role("button", name="文件").first.click()
        page.locator("aside[aria-label=文件]").get_by_role("button", name="Git").click()
    else:
        page.get_by_role("button", name="更多").first.click()
        page.get_by_role("menuitem", name="Git").click()
    expect(page.locator("aside[aria-label=Git], aside[aria-label=文件]").first).to_be_visible()


def code_case(browser, base, size, theme, shots):
    tag = f"{size}/{theme} code"
    ctx, page = fresh(browser, base, size, theme)
    posts = payloads(page)
    # a file named in the chat opens in the viewer panel
    page.get_by_role("link", name="Sources/App.swift").first.click()
    gutter = page.locator("[data-testid=file-code] [data-gutter]")
    expect(gutter.first).to_be_visible()
    row = page.locator("[data-testid=file-code] [data-line='2']")
    row.hover()
    plus = row.locator("[data-gutter] svg")
    expect(plus).to_be_visible()
    ok(f"{tag}: a + under the pointer in the gutter")
    # a drag over the numbers picks several lines
    a, b = row.locator("[data-gutter]").bounding_box(), page.locator("[data-testid=file-code] [data-line='4'] [data-gutter]").bounding_box()
    page.mouse.move(a["x"] + a["width"] / 2, a["y"] + a["height"] / 2)
    page.mouse.down()
    page.mouse.move(b["x"] + b["width"] / 2, b["y"] + b["height"] / 2, steps=4)
    page.mouse.up()
    box = page.locator("[data-line-box]")
    expect(box).to_be_visible()
    expect(box).to_contain_text("Sources/App.swift:2-4")
    page.wait_for_timeout(250)
    shot(page, shots, f"comments-{size}-{theme}-filebox")
    box.locator("textarea").fill("这几行要抽成函数")
    page.keyboard.press("Control+Enter")
    expect(box).to_have_count(0)
    ok(f"{tag}: a drag picks lines, Ctrl+Enter adds the comment")
    if size == "narrow":
        page.get_by_role("button", name="关闭面板").first.click()
    card = page.locator("[data-comment-card='1']")
    expect(card).to_contain_text("Sources/App.swift:2-4")
    expect(card).to_contain_text("这几行要抽成函数")
    page.wait_for_timeout(300)
    shot(page, shots, f"comments-{size}-{theme}-filecard")
    ok(f"{tag}: the card carries the location, the code and the note")

    # the card goes back to the lines
    if size == "wide":
        page.get_by_role("button", name="关闭面板").first.click()
        expect(page.locator("[data-testid=file-code]")).to_have_count(0)
    card.get_by_title("回到评论的位置").click()
    lit = page.locator("[data-testid=file-code] [data-line='2']")
    expect(lit).to_be_visible()
    expect(page.locator("[data-testid=file-code] [data-line='2'].bg-accent-tint, [data-testid=file-code] [data-line='3'].bg-accent-tint").first).to_be_visible()
    ok(f"{tag}: the card opens the file at its lines")

    # the file's own numbers show the card
    expect(page.locator("[data-testid=file-code] [data-line='2'] [data-gutter]")).to_have_attribute("aria-label", "这一行有评论 1")
    if size == "narrow":
        page.get_by_role("button", name="关闭面板").first.click()
    box = page.locator("textarea:not([aria-label^='评论'])").last
    box.fill("请看这条评论")
    page.get_by_role("button", name=SEND).click()
    page.wait_for_timeout(1200)
    body = text_of(sent(posts, "请看这条评论"))
    for want in ("> Code comment 1", "> File: Sources/App.swift:2-4", "> ```swift", "> 这几行要抽成函数", "请看这条评论"):
        check(want in body, f"{tag}: the message lacks {want!r}: {body[:400]!r}")
    ok(f"{tag}: sent with the file, the lines, the code and the note")
    bubble = page.locator("[data-user-bubble]").filter(has_text="请看这条评论").last
    expect(bubble.locator("[data-comment-block]")).to_contain_text("Sources/App.swift:2-4")
    # the sent card goes back to the file too
    page.locator("[data-testid=file-code]").count() == 0 or page.get_by_role("button", name="关闭面板").first.click()
    bubble.get_by_title("回到评论的位置").click()
    expect(page.locator("[data-testid=file-code] [data-line='2']")).to_be_visible()
    ok(f"{tag}: the card in the sent bubble opens the file again")
    ctx.close()


def diff_case(browser, base, size, theme, shots):
    tag = f"{size}/{theme} diff"
    ctx, page = fresh(browser, base, size, theme)
    posts = payloads(page)
    open_git(page, size)
    page.get_by_role("button", name="Provider.swift").first.click()
    diff = page.locator("[data-testid=diff]")
    expect(diff).to_be_visible()
    rows = diff.locator("[data-row] [data-gutter]")
    expect(rows.first).to_be_visible()
    # a changed line (the first green row)
    add = diff.locator("[data-row].bg-green-tint").first
    add.hover()
    n = add.locator("[data-gutter]").first
    n.click()
    box = page.locator("[data-line-box]")
    expect(box).to_be_visible()
    expect(box).to_contain_text("未暂存")
    box.locator("textarea").fill("这里漏了错误处理")
    page.wait_for_timeout(250)
    shot(page, shots, f"comments-{size}-{theme}-diffbox")
    box.get_by_role("button", name="添加").click()
    ok(f"{tag}: the gutter of a diff takes a comment")
    toast_gone = page.locator("[data-line-box]")
    expect(toast_gone).to_have_count(0)
    if size == "narrow":
        page.get_by_role("button", name="关闭面板").first.click()
    card = page.locator("[data-comment-card='1']")
    expect(card).to_contain_text("未暂存")
    expect(card).to_contain_text("这里漏了错误处理")
    page.wait_for_timeout(300)
    shot(page, shots, f"comments-{size}-{theme}-diffcard")
    ok(f"{tag}: the card names the diff")

    if size == "wide":
        page.get_by_role("button", name="关闭面板").first.click()
    card.get_by_title("回到评论的位置").click()
    expect(page.locator("[data-testid=diff] [data-row][class*='outline']").first).to_be_visible()
    ok(f"{tag}: the card opens the diff at its line")
    if size == "narrow":
        page.get_by_role("button", name="关闭面板").first.click()
    field = page.locator("textarea:not([aria-label^='评论'])").last
    field.fill("diff 的评论")
    page.get_by_role("button", name=SEND).click()
    page.wait_for_timeout(1200)
    body = text_of(sent(posts, "diff 的评论"))
    for want in ("> Code comment 1", "> Diff: unstaged", "> ```diff", "> +", "> 这里漏了错误处理", "diff 的评论"):
        check(want in body, f"{tag}: the message lacks {want!r}: {body[:400]!r}")
    ok(f"{tag}: sent with the diff, the changed lines and the note")
    ctx.close()


def term_case(browser, base, size, theme, shots):
    """A comment made while the page shows the terminal waits in the chat's box, and the toast offers the chat."""
    tag = f"{size}/{theme} terminal"
    ctx, page = fresh(browser, base, size, theme)
    page.get_by_role("radio", name="终端", exact=True).click()
    expect(page.get_by_role("radio", name="终端", exact=True)).to_have_attribute("aria-checked", "true")
    if size == "wide":
        page.get_by_role("button", name="文件").first.click()
    else:
        page.get_by_role("button", name="更多").first.click()
        page.get_by_role("menuitem", name="文件").click()
    aside = page.locator("aside[aria-label=文件]")
    expect(aside).to_be_visible()
    aside.get_by_text("Package.swift", exact=True).first.click()
    gutter = page.locator("[data-testid=file-code] [data-line='3'] [data-gutter]")
    expect(gutter).to_be_visible()
    gutter.click()
    box = page.locator("[data-line-box]")
    box.locator("textarea").fill("从终端里加的评论")
    page.keyboard.press("Control+Enter")
    toast = page.get_by_text("评论 1 已添加，切到「对话」发送")
    expect(toast).to_be_visible()
    page.wait_for_timeout(250)
    shot(page, shots, f"comments-{size}-{theme}-terminal-toast")
    page.get_by_role("button", name="查看对话").click()
    expect(page.get_by_role("radio", name="对话", exact=True)).to_have_attribute("aria-checked", "true")
    expect(page.locator("[data-comment-card='1']")).to_contain_text("从终端里加的评论")
    ok(f"{tag}: the comment waits in the chat's box and the toast goes to the chat")
    ctx.close()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("base")
    ap.add_argument("--shots")
    ap.add_argument("--only", choices=["transcript", "code", "diff", "terminal"])
    a = ap.parse_args()
    if a.shots:
        os.makedirs(a.shots, exist_ok=True)
    base = a.base.rstrip("/")
    cases = {"transcript": transcript_case, "code": code_case, "diff": diff_case, "terminal": term_case}
    with sync_playwright() as pw:
        exe = os.environ.get("PERSONA_E2E_CHROMIUM")
        browser = pw.chromium.launch(executable_path=exe) if exe else pw.chromium.launch(channel="chrome")
        try:
            for name, run in cases.items():
                if a.only and a.only != name:
                    continue
                for size in SIZES:
                    for theme in ("light", "dark"):
                        run(browser, base, size, theme, a.shots)
        finally:
            browser.close()
    print("PASS")


if __name__ == "__main__":
    main()
