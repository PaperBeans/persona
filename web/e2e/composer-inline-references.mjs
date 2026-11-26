// Use the synthetic skill hub, with the built Web app served by Vite preview:
//   PORT=20891 MOCK_LANG=en python3 scripts/mock/skill_mentions.py
//   cd web && PERSONA_HUB_URL=http://127.0.0.1:20891 npx vite preview --host 127.0.0.1 --port 20892
//   CHROME_BIN=<installed Chrome> node web/e2e/composer-inline-references.mjs http://127.0.0.1:20892 <shots directory>
import assert from "node:assert/strict";
import { mkdir } from "node:fs/promises";
import { join } from "node:path";
import { chromium } from "playwright-core";

const [target, shots] = process.argv.slice(2);
if (!target || !shots) throw new Error("Pass the loopback Web URL and output directory");
const base = new URL(target);
if (!["127.0.0.1", "localhost"].includes(base.hostname)) throw new Error("Use an isolated loopback mock hub");
await mkdir(shots, { recursive: true });

const session = "/s/macbook/c1111111-0000-4000-8000-000000000001";
const skill = "Skill Creator";
const file = "README.md";
const priorChat = "Example writing chat";
const input = "[data-composer-input]";
const token = "[data-inline-mention]";
const chineseBefore = "介绍下";
const chineseAfter = "相关信息";

const browser = await chromium.launch({ executablePath: process.env.CHROME_BIN, args: ["--no-sandbox"] });

async function scenario({ width, height, theme, touch = false, large = false }) {
  const context = await browser.newContext({ viewport: { width, height }, colorScheme: theme,
    hasTouch: touch, isMobile: touch });
  const page = await context.newPage();
  const errors = [];
  page.setDefaultTimeout(12000);
  page.on("pageerror", (error) => errors.push(error.message));
  await page.addInitScript(({ theme, large }) => {
    localStorage.setItem("persona.token", "mock");
    localStorage.setItem("persona.language", "en");
    localStorage.setItem("persona.theme", theme);
    if (large) localStorage.setItem("persona.text-size", "large");
    window.__composition = [];
    for (const type of ["compositionstart", "compositionupdate", "compositionend"]) {
      document.addEventListener(type, (event) => window.__composition.push([type, event.data]));
    }
  }, { theme, large });
  await page.route("**/api/v1/**/skills", (route) => route.fulfill({ json: { catalog_version: 1, complete: true,
    skills: [{ id: "fixture-creator", name: skill, source: "user · ~/example/skills", description: "Create focused workflow skills" }],
  } }));
  await page.route("**/api/v1/machines/*/fs?**", (route) => route.fulfill({ json: {
    path: "/example/project", entries: [{ name: file, dir: false }, { name: "docs", dir: true }],
  } }));

  const field = page.locator(input);
  const marks = () => field.locator(token).allTextContents();
  const pick = (name) => page.getByRole("option", { name: new RegExp(name) }).first();
  const plus = async (query, name) => {
    await page.locator("[data-composer-plus]").click();
    await page.locator("[data-composer-panel-search]").fill(query);
    await pick(name).click();
  };
  const boundary = (label, after) => field.locator(token).filter({ hasText: label }).evaluate((mark, after) => {
    mark.parentElement.focus();
    const range = document.createRange();
    if (after) range.setStartAfter(mark);
    else range.setStartBefore(mark);
    range.collapse(true);
    const selection = getSelection();
    selection.removeAllRanges();
    selection.addRange(range);
  }, after);

  try {
    await page.goto(new URL(session, base).href);
    await field.waitFor();
    await field.fill(`${chineseBefore}  ${chineseAfter}`);
    await field.evaluate((el, caret) => el.setSelectionRange(caret, caret), chineseBefore.length + 1);
    await page.keyboard.insertText("@Creat");
    await pick(skill).waitFor();
    await page.keyboard.press("Enter");
    await field.locator(token).waitFor();
    assert.equal(await field.textContent(), `${chineseBefore} ${skill} ${chineseAfter}`);
    assert.equal(await field.getAttribute("aria-multiline"), "true");
    assert.equal(await field.evaluate((el) => document.activeElement === el), true);

    // Chromium commits real composition events into the field, including a provisional Latin candidate.
    const cdp = await context.newCDPSession(page);
    await cdp.send("Input.imeSetComposition", { text: "zhongwen", selectionStart: 8, selectionEnd: 8 });
    assert.match(await field.textContent(), /zhongwen/);
    await cdp.send("Input.imeSetComposition", { text: "中文", selectionStart: 2, selectionEnd: 2 });
    await cdp.send("Input.insertText", { text: "中文" });
    await page.waitForFunction(() => window.__composition.some(([type, data]) => type === "compositionend" && data === "中文"));
    assert.equal(await field.textContent(), `${chineseBefore} ${skill} 中文${chineseAfter}`);

    await plus("docs", "docs/");
    await boundary("docs/", false);
    await page.keyboard.press("ArrowRight");
    await page.keyboard.press("Backspace");
    assert.deepEqual(await marks(), [skill]);
    assert.equal(await field.textContent(), `${chineseBefore} ${skill} 中文  ${chineseAfter}`);
    await page.keyboard.press("Delete");

    await plus("README", "README.md");
    await plus("writing", priorChat);
    const composed = `${chineseBefore} ${skill} 中文 ${file} ${priorChat} ${chineseAfter}`;
    assert.equal(await field.textContent(), composed);
    assert.deepEqual(await marks(), [skill, file, priorChat]);
    assert.equal(await field.locator(token).nth(0).getAttribute("title"), "user · ~/example/skills");
    const fileMark = field.locator(token).nth(1);
    assert.equal(await fileMark.getAttribute("title"), "/example/project/README.md");
    if (!touch) {
      await fileMark.hover();
      assert.equal(await fileMark.evaluate((el) => el.matches(":hover")), true);
    }
    await page.waitForFunction((theme) => document.documentElement.classList.contains("dark") === (theme === "dark"), theme);
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
    assert.equal(await fileMark.locator("svg").getAttribute("width"), "14");
    assert.equal(await fileMark.evaluate((el) => getComputedStyle(el).borderWidth), "0px");
    await page.screenshot({ path: join(shots, `${theme}-${width}${large ? "-large" : ""}.png`) });

    // Force the same save the browser performs on navigation, then reload the real route.
    await page.evaluate(() => dispatchEvent(new Event("pagehide")));
    await page.reload();
    await field.locator(token).nth(2).waitFor();
    assert.equal(await field.textContent(), composed);
    await field.evaluate((el) => {
      el.focus();
      getSelection().selectAllChildren(el);
      getSelection().collapseToEnd();
    });
    const newline = touch ? "Enter" : "Shift+Enter";
    await field.press(newline);
    await page.keyboard.insertText("请简述");
    let closing = "请简述";
    if (touch) {
      for (let line = 2; line <= 7; line++) {
        await field.press(newline);
        await page.keyboard.insertText(`Example line ${line}`);
        closing += `\nExample line ${line}`;
      }
      await page.getByTestId("composer-expand").click();
      const expanded = page.getByTestId("composer-expanded").getByRole("textbox");
      await expanded.locator(token).nth(2).waitFor();
      assert.equal(await expanded.textContent(), `${composed}\n${closing}`);
      await page.getByTestId("composer-collapse").click();
    }
    assert.equal(await field.textContent(), `${composed}\n${closing}`);

    const accepted = page.waitForResponse((response) => response.request().method() === "POST" && response.url().endsWith("/messages"));
    if (touch) await page.getByTestId("composer-send").click();
    else await field.press("Enter");
    const response = await accepted;
    assert.equal(response.ok(), true);
    const wire = response.request().postDataJSON().text;
    assert.match(wire, /^<(?:persona|jarvis)-context kind="skill" title="Skill Creator" url="" ref="fixture-creator">\n/);
    assert.match(wire, /kind="context" title="Example writing chat" url="(?:persona|jarvis):\/\/session\//);
    assert.ok(wire.includes(`\n\n${composed.replace(file, `@${file}`)}\n${closing}\n\n<!-- persona-inline-v1 `));
    assert.match(wire, /\n\n<(?:persona|jarvis)-context kind="file" title="README.md" url="" path="README.md" machine="macbook" cwd="\/example\/project">\nREADME.md\n<\/(?:persona|jarvis)-context>$/);
    assert.equal(await field.textContent(), "");
    if (!touch && width === 1440) {
      await context.grantPermissions(["clipboard-read", "clipboard-write"], { origin: base.origin });
      await page.evaluate(() => navigator.clipboard.writeText("请检查\n粘贴内容"));
      await field.focus();
      await page.keyboard.press("Control+v");
      await page.waitForFunction((selector) => document.querySelector(selector).textContent === "请检查\n粘贴内容", input);
      await field.evaluate((el) => {
        const dataTransfer = new DataTransfer();
        dataTransfer.setData("application/x-persona-file", JSON.stringify({ mid: "macbook", path: "/example/project/dropped.md", dir: false }));
        el.dispatchEvent(new DragEvent("drop", { bubbles: true, cancelable: true, dataTransfer }));
      });
      await field.locator(token).waitFor();
      const withDrop = "请检查\n粘贴内容 dropped.md ";
      assert.equal(await field.textContent(), withDrop);
      await page.evaluate(() => navigator.clipboard.writeText("Example text. ".repeat(65)));
      await page.keyboard.press("Control+v");
      await page.getByRole("button", { name: /Pasted text/ }).click();
      await page.getByRole("button", { name: "Expand into input" }).waitFor();
      assert.equal(await field.textContent(), withDrop);
      await page.getByRole("button", { name: "Remove pasted text" }).click();
      await field.press("Control+a");
      await field.press("Backspace");
      assert.equal(await field.textContent(), "");
    }
    assert.deepEqual(errors, []);
    console.log(`PASS ${theme} ${width}×${height}${touch ? " touch, large text, expand" : ""}: IME, @/+, atomic deletion, draft reload, newline and accepted wire payload`);
  } finally {
    await context.close();
  }
}

try {
  for (const theme of ["light", "dark"]) {
    for (const [width, height] of [[1440, 900], [390, 844]]) await scenario({ width, height, theme });
    await scenario({ width: 390, height: 844, theme, touch: true, large: true });
  }
} finally {
  await browser.close();
}
