// Drive the real app against an intercepted, synthetic mock hub; never reads live conversation data.
// node web/e2e/composer-growth-scroll.mjs <origin> <artifacts> [before|after]
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
const SESSION = { machine_id: MACHINE.id, session_id: "example-main", title: "Composer test", state: "idle", status: "idle", cwd: "/example", updated_at: AT, assistant: true, started_by_persona: true, capabilities: { send: true, interrupt: true } };
const ITEMS = [{ id: "u1", seq: 1, role: "user", kind: "text", text: "This is a synthetic composer test conversation.", ts: AT }, { id: "a1", seq: 2, role: "assistant", kind: "text", text: "Write a draft below without sending it.", ts: AT }];
const lines = (n) => Array.from({ length: n }, (_, i) => `Draft line ${i + 1}: example text.`).join("\n");
const result = { status: "running", phase, runs: [] };
const remote = process.env.BROWSER_CDP;
const browser = remote ? await chromium.connectOverCDP(remote) : await chromium.launch({ channel: "chrome" });
const deadline = setTimeout(() => {
  result.status = "timeout";
  console.log(`TIMEOUT composer growth/scroll ${phase}`);
  void writeFile(`${output}/results.json`, JSON.stringify(result, null, 2)).finally(() => process.exit(1));
}, 180_000);
try {
  for (const [width, height] of [[390, 844], [1280, 900]]) for (const theme of ["light", "dark"]) {
    const name = `${width}x${height}-${theme}`;
    const context = await browser.newContext({ viewport: { width, height }, locale: "en-US", colorScheme: theme, reducedMotion: "reduce", serviceWorkers: "block", isMobile: width < 500, hasTouch: width < 500 });
    await context.addInitScript(({ theme }) => {
      localStorage.setItem("persona.token", "example");
      localStorage.setItem("persona.language", "en");
      localStorage.setItem("persona.theme", theme);
    }, { theme });
    await context.route("**/*", (route) => new URL(route.request().url()).origin === base.origin ? route.continue() : route.abort());
    const writes = [];
    await context.route("**/api/v1/**", (route) => {
      const method = route.request().method();
      if (method !== "GET") { writes.push({ method, path: new URL(route.request().url()).pathname }); return route.fulfill({ status: 200, contentType: "application/json", body: "{}" }); }
      const path = new URL(route.request().url()).pathname.replace("/api/v1", "");
      const data = path === "/machines" ? { machines: [MACHINE] }
        : path === "/sessions" ? { sessions: [SESSION] }
        : path === "/assistant" ? { machine_id: MACHINE.id, main: SESSION.session_id, conversations: [SESSION], tasks: [], memory: [] }
        : path === "/interactions" ? { interactions: [] }
        : path.endsWith("/items") ? { items: ITEMS, has_more: false }
        : path.endsWith("/feed") ? { items: [], plans: {}, task_cards: [] }
        : path === "/todos" ? { todos: [] }
        : /^\/sessions\/[^/]+\/[^/]+$/.test(path) ? { session: SESSION } : {};
      return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(data) });
    });
    let socket;
    await context.routeWebSocket("**/api/v1/ws**", (ws) => {
      socket = ws;
      ws.send(JSON.stringify({ type: "hello", server_version: "example", hub_name: "Example hub", features: [] }));
      ws.onMessage((m) => { if (String(m).includes("app_ping")) ws.send(JSON.stringify({ type: "pong" })); });
    });
    const page = await context.newPage();
    page.setDefaultTimeout(20_000);
    const errors = [];
    page.on("pageerror", (e) => errors.push(String(e)));
    await page.goto(base.href);
    await page.getByText(ITEMS[1].text, { exact: true }).waitFor();
    const input = page.locator("textarea").last();
    await input.waitFor();
    const measure = async () => {
      await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
      return input.evaluate((el) => {
      const css = getComputedStyle(el), r = el.getBoundingClientRect();
      const line = parseFloat(css.lineHeight), pad = parseFloat(css.paddingTop) + parseFloat(css.paddingBottom);
      return { height: r.height, line, pad, rows: (el.clientHeight - pad) / line, scrollTop: el.scrollTop, scrollHeight: el.scrollHeight, clientHeight: el.clientHeight, top: r.top, bottom: r.bottom, selection: [el.selectionStart, el.selectionEnd] };
      });
    };
    const run = { name, growth: [], manual: [], errors, interceptedWrites: writes };
    result.runs.push(run);
    for (const n of [1, 3, 4, 5, 6, 7, 12]) {
      await input.fill(lines(n));
      await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
      run.growth.push({ lines: n, ...await measure() });
      if ([1, 6, 12].includes(n)) await page.screenshot({ path: `${output}/${name}-${n}-lines-${phase}.png` });
    }
    if (width < 500) {
      await page.setViewportSize({ width, height: 500 });
      await input.fill(lines(6));
      run.keyboard = await measure();
      await page.screenshot({ path: `${output}/${name}-keyboard-${phase}.png` });
      await page.setViewportSize({ width, height });
    }
    await input.fill(lines(30));
    for (const fraction of [0, 0.4]) {
      // Use an actual wheel gesture to read earlier text without moving the caret.
      await input.evaluate((el) => { el.scrollTop = el.scrollHeight; });
      await input.hover();
      await page.mouse.wheel(0, -10000);
      await page.waitForFunction(() => document.querySelector("textarea").scrollTop < 1);
      if (fraction) { await page.mouse.wheel(0, 280); await page.waitForFunction(() => document.querySelector("textarea").scrollTop > 0); }
      const start = await measure();
      // More than the reported one or two seconds, with an unrelated hub update.
      socket?.send(JSON.stringify({ type: "sessions", machine_id: MACHINE.id, sessions: [SESSION] }));
      await page.waitForTimeout(2200);
      const idle = await measure();
      await page.setViewportSize({ width, height: height - 1 });
      await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
      const resized = await measure();
      run.manual.push({ fraction, start, idle, resized });
      await page.screenshot({ path: `${output}/${name}-read-${fraction ? "middle" : "first"}-${phase}.png` });
      if (phase === "after") {
        assert.ok(Math.abs(start.scrollTop - idle.scrollTop) < 2, `${name}: an idle update moved the draft`);
        assert.ok(Math.abs(start.scrollTop - resized.scrollTop) < 2, `${name}: a viewport update moved the draft`);
      }
      await page.setViewportSize({ width, height });
    }
    // Native cursor navigation and typing must still reveal the caret after manual scrolling.
    await input.focus();
    await page.keyboard.press("Control+End");
    await page.keyboard.insertText(" Typed after reading.");
    run.typing = await measure();
    await page.keyboard.press("Control+Home");
    run.caretAtStart = await measure();
    if (phase === "after") {
      const growth = run.growth;
      assert.ok(growth.find((g) => g.lines === 4).height > growth.find((g) => g.lines === 3).height, `${name}: stuck at three lines`);
      assert.ok(Math.abs(growth.find((g) => g.lines === 6).rows - 6) < 0.1, `${name}: six lines are not visible`);
      assert.ok(Math.abs(growth.find((g) => g.lines === 7).height - growth.find((g) => g.lines === 6).height) < 1, `${name}: grew past six lines`);
      assert.ok(run.typing.scrollTop + run.typing.clientHeight >= run.typing.scrollHeight - 3, `${name}: typing did not reveal the end caret`);
      assert.ok(run.caretAtStart.scrollTop < run.caretAtStart.line, `${name}: cursor navigation did not reveal the first line`);
      if (run.keyboard) assert.ok(Math.abs(run.keyboard.rows - 6) < 0.1, `${name}: keyboard-sized viewport stuck below six lines`);
    }
    await input.fill("A wrapped draft without explicit newlines. ".repeat(40));
    run.wrapped = await measure();
    if (phase === "after") assert.ok(Math.abs(run.wrapped.rows - 6) < 0.1 && run.wrapped.scrollHeight > run.wrapped.clientHeight, `${name}: wrapped text did not cap and scroll at six lines`);
    // Use the real persisted preference on a fresh page, not a transient attribute overwritten by look effects.
    await page.evaluate(() => { localStorage.setItem("persona.text-size", "large"); });
    await page.reload();
    await input.waitFor();
    await input.fill(lines(7));
    run.largeText = await measure();
    await page.screenshot({ path: `${output}/${name}-large-text-${phase}.png` });
    if (phase === "after") assert.ok(Math.abs(run.largeText.rows - 6) < 0.1, `${name}: largest text size did not retain six lines`);
    await page.evaluate(() => { localStorage.removeItem("persona.text-size"); });
    await page.reload();
    await input.waitFor();
    await input.fill(lines(30));
    if (width < 500) {
      await page.getByTestId("composer-expand").click();
      const panel = page.getByTestId("composer-expanded");
      await panel.waitFor();
      run.expanded = await panel.evaluate((el) => {
        const button = el.querySelector('[data-testid="composer-collapse"]'), field = el.querySelector("textarea");
        const b = button.getBoundingClientRect(), f = field.getBoundingClientRect();
        return { color: getComputedStyle(button).color, buttonBottom: b.bottom, textTop: f.top, overlaps: f.top < b.bottom, hasHideKeyboard: Array.from(el.querySelectorAll("button")).some((b) => /keyboard/i.test(b.getAttribute("aria-label") || "")) };
      });
      await page.keyboard.press("Control+Home");
      await page.screenshot({ path: `${output}/${name}-expanded-${phase}.png` });
    } else run.expanded = { available: await page.getByTestId("composer-expand").count() > 0 };
    assert.deepEqual(errors, [], `${name}: browser errors`);
    assert.ok(writes.every((w) => /\/(seen|today)$/.test(w.path)), `${name}: unexpected writes ${JSON.stringify(writes)}`);
    console.log(JSON.stringify(run));
    if (!remote) await context.close(); // Remote pages stay open for the user to inspect.
  }
  result.status = "pass";
} catch (error) {
  result.status = result.status === "timeout" ? "timeout" : "fail";
  result.error = String(error);
} finally {
  clearTimeout(deadline);
  await writeFile(`${output}/results.json`, JSON.stringify(result, null, 2));
  if (!remote) await browser.close().catch(() => {});
  console.log(`${result.status.toUpperCase()} composer growth/scroll ${phase}${result.error ? `: ${result.error}` : ""}`);
  process.exit(result.status === "pass" ? 0 : 1);
}
