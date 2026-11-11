// Real web UI, invented chat/quotes; all hub HTTP and WebSocket traffic is intercepted.
// node web/e2e/composer-quote-overflow.mjs <origin> <artifacts> [before|after]
// A deployed origin is allowed for read-only asset verification; no live hub data or profile is used.
import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { chromium } from "playwright-core";

const [address, artifacts, phase = "after"] = process.argv.slice(2);
assert.ok(address && artifacts && ["before", "after"].includes(phase));
const base = new URL(address);
const output = resolve(artifacts);
await mkdir(output, { recursive: true });
const AT = "2026-01-01T10:00:00Z";
const MACHINE = { id: "example", name: "Computer A", online: true, os: "linux" };
const SESSION = { machine_id: MACHINE.id, session_id: "example-main", title: "Persona", state: "idle", status: "idle", cwd: "/example", updated_at: AT, assistant: true, started_by_persona: true, capabilities: { send: true, interrupt: true } };
const KEY = `${MACHINE.id}/${SESSION.session_id}`;
const ITEMS = [
  { id: "u1", seq: 1, role: "user", kind: "text", text: "Please review this example plan.", ts: AT },
  { id: "a1", seq: 2, role: "assistant", kind: "text", text: "The example plan has three steps: prepare, check and deliver.", ts: AT },
  { id: "u2", seq: 3, role: "user", kind: "text", text: "What happens after the first check?", ts: AT },
  { id: "a2", seq: 4, role: "assistant", kind: "text", text: "Messages after the quoted passage should remain visible while you write your comments.", ts: AT },
];
const quote = (i, lines = 4) => ({ id: `quote-${i}`, text: `Example passage ${i}: prepare, check and deliver.`, comment: { body: Array.from({ length: lines }, (_, n) => `Note ${i}, line ${n + 1}: keep this detail available.`).join("\n"), anchor: { item: "a1", start: 0, end: 23, exact: "The example plan has three steps" } } });
const scenarios = { stacked: Array.from({ length: 6 }, (_, i) => quote(i + 1)), long: [quote(1, 35)], short: [quote(1, 1)] };
const result = { status: "running", phase, runs: [] };
const browser = await chromium.launch({ channel: "chrome" });
const deadline = setTimeout(() => { result.status = "timeout"; void browser.close(); }, 180_000);
try {
  for (const [width, height] of [[1280, 720], [1024, 520], [390, 664]]) for (const theme of ["light", "dark"]) for (const scenario of Object.keys(scenarios)) {
    const name = `${width}x${height}-${theme}-${scenario}`;
    const context = await browser.newContext({ viewport: { width, height }, locale: "en-US", colorScheme: theme, reducedMotion: "reduce", serviceWorkers: "block", isMobile: width < 500, hasTouch: width < 500 });
    await context.addInitScript(({ theme, key, quotes }) => {
      localStorage.setItem("persona.token", "example");
      localStorage.setItem("persona.language", "en");
      localStorage.setItem("persona.theme", theme);
      localStorage.setItem(`quote:${key}`, JSON.stringify(quotes));
    }, { theme, key: KEY, quotes: scenarios[scenario] });
    await context.route("**/*", (route) => {
      if (new URL(route.request().url()).origin !== base.origin) return route.abort();
      return route.continue();
    });
    const writes = [];
    await context.route("**/api/v1/**", (route) => {
      const method = route.request().method();
      if (method !== "GET") { writes.push(method); return route.fulfill({ status: 200, contentType: "application/json", body: "{}" }); }
      const path = new URL(route.request().url()).pathname.replace("/api/v1", "");
      const data = path === "/machines" ? { machines: [MACHINE] }
        : path === "/sessions" ? { sessions: [SESSION] }
        : path === "/assistant" ? { machine_id: MACHINE.id, main: SESSION.session_id, conversations: [SESSION], tasks: [], memory: [] }
        : path === "/interactions" ? { interactions: [] }
        : path.endsWith("/items") ? { items: ITEMS, has_more: false }
        : path.endsWith("/feed") ? { items: [], plans: {}, task_cards: [] }
        : path === "/todos" ? { todos: [] }
        : /^\/sessions\/[^/]+\/[^/]+$/.test(path) ? { session: SESSION }
        : {};
      return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(data) });
    });
    await context.routeWebSocket("**/api/v1/ws**", (ws) => {
      ws.send(JSON.stringify({ type: "hello", server_version: "example", hub_name: "Example hub", features: [] }));
      ws.onMessage((m) => { if (String(m).includes("app_ping")) ws.send(JSON.stringify({ type: "pong" })); });
    });
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", (e) => errors.push(String(e)));
    await page.goto(base.href);
    await page.locator("[data-quotes]").waitFor({ timeout: 20_000 });
    await page.getByText(ITEMS[3].text, { exact: true }).waitFor();
    await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
    const measure = () => page.evaluate(() => {
      const list = document.querySelector("[data-quotes]");
      const composer = list.parentElement;
      const input = composer.querySelector("textarea");
      const rect = (el) => { const r = el.getBoundingClientRect(); return { top: r.top, bottom: r.bottom, height: r.height }; };
      const own = (el) => { const r = el.getBoundingClientRect(); const top = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2); return Boolean(top && el.contains(top)); };
      return { quotes: rect(list), input: rect(input), inputVisible: own(input), scrollHeight: list.scrollHeight, clientHeight: list.clientHeight, scrollTop: list.scrollTop, cards: list.querySelectorAll("[data-comment-card]").length, overflow: getComputedStyle(list).overflowY };
    });
    const initial = await measure();
    const run = { name, initial, errors, interceptedWrites: writes };
    result.runs.push(run);
    await page.screenshot({ path: `${output}/${name}-${phase}.png` });
    if (phase === "after") {
      assert.ok(initial.quotes.height <= height / 4 + 1, `${name}: quote stack exceeded one quarter of the viewport`);
      assert.ok(initial.quotes.top > 0 && initial.input.bottom <= height && initial.inputVisible, `${name}: composer input is off screen or covered`);
      run.laterMessageVisible = await page.getByText(ITEMS[3].text, { exact: true }).evaluate((el) => {
        const r = el.getBoundingClientRect(); const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
        return r.top >= 0 && Boolean(hit && el.contains(hit));
      });
      assert.ok(run.laterMessageVisible, `${name}: the message after the quoted passage is covered`);
      assert.equal(initial.cards, scenarios[scenario].length);
      if (scenario !== "short") {
        assert.ok(initial.scrollHeight > initial.clientHeight, `${name}: content was clipped instead of scrollable`);
        assert.equal(initial.overflow, "auto");
        await page.locator("[data-quotes]").hover();
        await page.mouse.wheel(0, 400);
        await page.waitForFunction(() => document.querySelector("[data-quotes]").scrollTop > 0);
        await page.locator("[data-quotes]").evaluate((el) => { el.scrollTop = el.scrollHeight; });
        const last = page.locator("[data-comment-card]").last();
        // The complete bottom note remains reachable without scrolling the whole chat.
        const lastNote = last.locator("button").nth(1);
        const noteBox = await lastNote.boundingBox();
        await lastNote.click({ position: { x: 20, y: noteBox.height - 10 } });
        const note = page.locator("[data-quotes] textarea");
        await note.fill("Edited final comment\nThe main input stays visible.");
        await page.keyboard.press("Enter");
        assert.equal(await page.locator("[data-quotes] textarea").count(), 0);
        assert.ok(await last.innerText().then((text) => text.includes("Edited final comment")));
        const mainInput = page.locator("textarea").last();
        await mainInput.fill("Draft stays here");
        // Add a comment through the same store operation the selection menu calls (local dev only).
        if (["127.0.0.1", "localhost"].includes(base.hostname)) {
          await page.evaluate(async ({ key, q }) => {
            const { addQuote } = await import("/src/lib/drafts.ts");
            addQuote(key, q);
          }, { key: KEY, q: quote(7, 2) });
          const active = page.locator("[data-quotes] textarea");
          await active.waitFor();
          run.activeNoteVisible = await active.evaluate((el) => {
            const r = el.getBoundingClientRect(); const p = el.closest("[data-quotes]").getBoundingClientRect();
            return { visible: r.top >= p.top - 1 && r.bottom <= p.bottom + 1, top: r.top, bottom: r.bottom, bounds: { top: p.top, bottom: p.bottom } };
          });
          assert.ok(run.activeNoteVisible.visible, `${name}: newly added note is outside the quote scroller ${JSON.stringify(run.activeNoteVisible)}`);
          await page.keyboard.press("Escape");
        }
        await last.getByRole("button", { name: "Remove comment", exact: true }).click();
        assert.equal(await page.locator("[data-comment-card]").count(), scenarios[scenario].length - 1);
        assert.equal(await mainInput.inputValue(), "Draft stays here");
        run.afterEditing = await measure().catch(() => null); // the lone long comment removes the whole list
        await page.screenshot({ path: `${output}/${name}-edited.png` });
      }
      // Largest supported text preference also preserves the bounded region and main input.
      if (scenario === "stacked") {
        await page.evaluate(() => { document.documentElement.dataset.textSize = "large"; });
        run.largeText = await measure();
        assert.ok(run.largeText.quotes.height <= height / 4 + 1 && run.largeText.inputVisible);
        await page.screenshot({ path: `${output}/${name}-large-text.png` });
      }
    }
    assert.deepEqual(errors, [], `${name}: browser errors`);
    await context.close();
    console.log(JSON.stringify(run));
  }
  result.status = "pass";
} catch (error) {
  result.status = result.status === "timeout" ? "timeout" : "fail";
  result.error = String(error);
} finally {
  clearTimeout(deadline);
  await writeFile(`${output}/results.json`, JSON.stringify(result, null, 2));
  await browser.close().catch(() => {});
  console.log(`${result.status.toUpperCase()} composer quote overflow ${phase}${result.error ? `: ${result.error}` : ""}`);
  process.exit(result.status === "pass" ? 0 : 1);
}
