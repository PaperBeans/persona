// A task its supervisor parked (#3804): the real web app with synthetic HTTP/WS only; it never connects to a user's hub
// or conversation. One task is parked, one waits for the owner's decision: the parked one is In progress, saying until
// when and why, and Needs you lists only the decision.
// node web/e2e/parked-task.mjs <web URL> --shots <directory> [--chromium <executable>]
import assert from "node:assert/strict";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const flag = (name) => { const i = args.indexOf(name); return i < 0 ? undefined : args[i + 1]; };
const base = args[0]?.replace(/\/$/, "");
const shots = flag("--shots");
if (!base || !shots) {
  console.error("usage: node web/e2e/parked-task.mjs <web URL> --shots <directory> [--chromium <executable>]");
  process.exit(2);
}
const origin = new URL(base);
assert(["127.0.0.1", "localhost", "[::1]"].includes(origin.hostname) && origin.protocol === "http:", "only an owned loopback HTTP web server is allowed");
assert(!origin.username && !origin.password && !origin.search && !origin.hash, "the web URL must not contain credentials");
mkdirSync(shots, { recursive: true });
const watchdog = setTimeout(() => { console.error("TIMEOUT parked-task browser verification (180s)"); process.exit(124); }, 180_000);
const sid = "parked-task-demo";
const REASON = "The design team has to pick the icon set first";
const words = { en: /^Parked until .+ · /, "zh-Hans": /^搁置到 .+ · /, ja: /^.+ まで保留 · / };
const chip = { en: "Parked", "zh-Hans": "已搁置", ja: "保留中" };

function todos() {
  // parked a week ago, so an unparked task this still would be folded under shelved; due in three days
  const at = new Date(Date.now() - 7 * 24 * 3600 * 1000).toISOString();
  const until = new Date(Date.now() + 3 * 24 * 3600 * 1000).toISOString();
  const task = (id, title, supervision, more = {}) => ({ id, title, origin: sid, created_at: at, updated_at: at,
    agents: [{ machine_id: "demo", session_id: `${id}-worker`, linked_at: at }], supervision: { supervisor: sid, done_when: ["The icons are merged"], ...supervision }, ...more });
  return [
    task("demo-icons", "Example toolbar icons", { state: "parked", parked: { reason: REASON, until, waiting_on: "someone_else", count: 1 },
      reviews: [{ at, verdict: "parked", note: REASON }] }),
    task("demo-rollout", "Example rollout", { state: "needs_user" },
      { ask: { outcome: "decide", asked_at: at, verdict: "Needs your decision: roll out to everyone now, or to 10% first", recommend: "10% first" } }),
  ];
}

async function run(browser, size, theme, language) {
  const context = await browser.newContext({ viewport: size === "wide" ? { width: 1280, height: 900 } : { width: 390, height: 844 }, colorScheme: theme, reducedMotion: "reduce" });
  await context.addInitScript(({ theme, language }) => {
    localStorage.setItem("persona.token", "synthetic-demo-token");
    localStorage.setItem("persona.language", language);
    localStorage.setItem("persona.theme", theme);
  }, { theme, language });
  const at = new Date(Date.now() - 7 * 24 * 3600 * 1000).toISOString();
  const session = { machine_id: "demo", session_id: sid, title: "Example conversation", assistant: true, state: "idle", status: "idle", updated_at: at };
  const list = todos();
  const agents = list.map((t) => ({ machine_id: "demo", session_id: `${t.id}-worker`, title: `${t.title} Agent`, state: "idle", status: "idle", updated_at: at, state_since: at }));
  await context.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "");
    if (request.method() !== "GET") { // the app's own bookkeeping (read marks); a parked task has nothing to press
      assert(!path.startsWith("/todos"), `nothing is written to a to-do: ${request.method()} ${path}`);
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ ok: true }) });
      return;
    }
    let body = {};
    if (path === "/machines") body = { machines: [{ id: "demo", name: "Example computer", online: true, os: "linux" }] };
    else if (path === "/sessions") body = { sessions: [session, ...agents] };
    else if (path === "/interactions") body = { interactions: [] };
    else if (path === "/assistant") body = { machine_id: "demo", main: sid, conversations: [session], tasks: [], memory: [] };
    else if (path === "/todos") body = { todos: list };
    else if (path === `/sessions/demo/${sid}`) body = { session };
    else if (path.endsWith("/feed")) body = { items: [] };
    else if (path.endsWith("/items")) body = { items: [], has_more: false };
    else if (path === "/extensions/ui") body = { slots: [] };
    else if (path === "/extensions") body = { extensions: [] };
    else if (path === "/managers") body = { managers: [] };
    await route.fulfill({ contentType: "application/json", body: JSON.stringify(body) });
  });
  await context.routeWebSocket("**/api/v1/ws**", (ws) => {
    ws.send(JSON.stringify({ type: "hello", server_version: "synthetic", hub_name: "Example hub", features: [] }));
    ws.onMessage((raw) => {
      const message = JSON.parse(String(raw));
      if (message.type === "app_ping") ws.send(JSON.stringify({ type: "pong" }));
    });
  });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(String(error)));
  page.setDefaultTimeout(12_000);
  const name = (step) => join(shots, `parked-${size}-${theme}-${language}-${step}.png`);
  try {
    await page.goto(`${base}/`);
    // Needs you: the decision only
    await page.locator('[data-tray-entry="needs"]').click();
    await page.locator('[data-needs-row="task:demo-rollout"]').waitFor();
    assert.equal(await page.locator("[data-needs-row]").count(), 1, "Needs you lists the decision and not the parked task");
    assert.equal(await page.locator('[data-needs-row="task:demo-icons"]').count(), 0);
    // In progress: the parked task, counted, not folded under shelved, saying until when and why
    await page.locator('[data-tray-entry="running"]').click();
    const row = page.locator("[data-task-slot]");
    await row.first().waitFor();
    assert.equal(await row.count(), 1, "In progress lists the parked task");
    assert.equal(await page.locator("[data-progress-shelved]").count(), 0, "a parked task is not folded under shelved");
    const text = (await row.first().innerText()).split("\n").map((line) => line.trim()).filter(Boolean);
    assert(text.includes(chip[language]), `the row's state word is ${chip[language]}: ${JSON.stringify(text)}`);
    const line = text.find((part) => part.includes(REASON));
    assert(line && words[language].test(line), `the row says until when and why: ${JSON.stringify(text)}`);
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth), false, "no horizontal page overflow");
    await page.waitForTimeout(300); // the entry has turned to its selected colors
    await page.screenshot({ path: name("tray") });
    assert.deepEqual(errors, []);
    console.log(`PASS ${size} ${theme} ${language}: needs_you=1, in_progress=1, line=${JSON.stringify(line)}`);
  } catch (error) {
    await page.screenshot({ path: name("failed") }).catch(() => {});
    throw error;
  } finally {
    await context.close();
  }
}

let browser;
try {
  browser = await chromium.launch({ executablePath: flag("--chromium") || "/usr/bin/google-chrome", headless: true });
  for (const theme of ["light", "dark"]) {
    await run(browser, "wide", theme, "en");
    await run(browser, "narrow", theme, "en");
  }
  for (const language of ["zh-Hans", "ja"]) await run(browser, "narrow", "light", language);
  console.log("PASS parked-task browser verification");
} catch (error) {
  console.error("FAIL parked-task browser verification", error);
  process.exitCode = 1;
} finally {
  clearTimeout(watchdog);
  await browser?.close();
}
