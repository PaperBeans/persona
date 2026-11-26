// Actual-app Recovery evidence with synthetic loopback fixtures only.
// Build web first, then: node web/e2e/recovery.mjs <absolute-output-directory>
import assert from "node:assert/strict";
import { createServer as httpServer } from "node:http";
import { mkdir, writeFile } from "node:fs/promises";
import { dirname, isAbsolute, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright-core";
import { preview } from "vite";
import { ownedCleanup } from "./extension-mirrors-cleanup.mjs";

const web = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const output = process.argv[2];
assert.ok(output && isAbsolute(output), "Pass an absolute output directory outside the repository");
assert.ok(!resolve(output).startsWith(resolve(web, "..") + "/"), "Screenshots must stay outside the repository");
const id = "a".repeat(64);
const bundle = { id, revision: "synthetic-revision-1", name: "synthetic.tar", archive_bytes: 8192, modified_at: "2026-01-02T03:04:05Z" };
const overview = {
  capabilities: {
    inspect: { state: "supported" }, verify: { state: "supported" },
    create: { state: "guided", reason: "writes_quiesced" },
    restore: { state: "unsupported", reason: "terminal_only" }, download: { state: "unsupported", reason: "private_keys" },
  },
  discovery: { state: "supported" }, history: { state: "unavailable", reason: "no_authoritative_source" }, bundles: [bundle],
};
const inspection = {
  kind: "offline_integrity", state: "passed", checked_at: "2026-01-03T03:04:05Z", bundle,
  report: {
    check: "offline_integrity", format: 1, created_at: "2026-01-02T03:04:05Z", archive_bytes: 8192, database_restore_verified: false,
    hub_dump: { files: 1, bytes: 100 }, manager_dump: { files: 1, bytes: 100 }, runtime: { files: 4, bytes: 200 },
    knowledge_base: { state: "included", location: "internal", files: 1, bytes: 50 },
  },
};
const calls = [];
const screenshots = [];
let browser;
let vite;
let mock;
let timedOut = false;
let verdict = "";
const cleanup = ownedCleanup(() => ({ browser, vite, mock }));
const deadline = setTimeout(() => {
  timedOut = true;
  console.error("WEB_RECOVERY TIMEOUT");
  const exit = setTimeout(() => process.exit(1), 5_000);
  void cleanup().then(() => { clearTimeout(exit); process.exit(1); });
}, 180_000);
try {
  await mkdir(output, { recursive: true });
  mock = httpServer(async (req, res) => {
    const url = new URL(req.url, "http://127.0.0.1");
    const json = (status, data) => { res.writeHead(status, { "Content-Type": "application/json" }); res.end(JSON.stringify(data)); };
    if (url.pathname === "/api/v1/managers") return json(200, { default: "assistant", managers: [{ id: "assistant", name: "Synthetic assistant", online: true, builtin: true }] });
    if (url.pathname === "/api/v1/hub/update") return json(404, { error: "Synthetic older updater" });
    if (url.pathname === "/api/v1/hub/recovery" && req.method === "GET") { calls.push("overview"); return json(200, overview); }
    const check = url.pathname.match(/^\/api\/v1\/hub\/recovery\/bundles\/([a-f0-9]{64})\/(inspect|verify)$/);
    if (check && req.method === "POST") {
      assert.equal(check[1], id);
      calls.push(check[2]);
      return json(200, check[2] === "inspect" ? inspection : { kind: "database_restore", state: "passed", checked_at: "2026-01-03T03:04:05Z", bundle });
    }
    return json(200, { machines: [], sessions: [], agents: [], groups: [], items: [], interactions: [], todos: [], workflows: [], extensions: [], conversations: [], providers: [], channels: [], personas: [], count: 0, total: 0 });
  });
  await new Promise((done, reject) => { mock.once("error", reject); mock.listen(0, "127.0.0.1", done); });
  const mockURL = `http://127.0.0.1:${mock.address().port}`;
  process.env.PERSONA_HUB_URL = mockURL;
  const reservation = httpServer();
  await new Promise((done) => reservation.listen(0, "127.0.0.1", done));
  const port = reservation.address().port;
  await new Promise((done) => reservation.close(done));
  vite = await preview({ root: web, preview: { host: "127.0.0.1", port, strictPort: true, proxy: { "/api": { target: mockURL, changeOrigin: true, ws: true } } } });
  const appURL = `http://127.0.0.1:${vite.httpServer.address().port}`;
  browser = await chromium.launch({ ...(process.env.CHROMIUM_EXECUTABLE ? { executablePath: process.env.CHROMIUM_EXECUTABLE } : { channel: "chrome" }), headless: true, args: ["--no-sandbox"] });
  for (const width of [1440, 390]) for (const scheme of ["light", "dark"]) {
    const context = await browser.newContext({ viewport: { width, height: width === 390 ? 1000 : 1100 }, locale: "en-US", timezoneId: "UTC", colorScheme: scheme });
    await context.addInitScript(({ scheme }) => {
      localStorage.setItem("persona.token", "synthetic-browser-token");
      localStorage.setItem("persona.language", "en");
      localStorage.setItem("persona.theme", scheme);
    }, { scheme });
    await context.route("**/*", (route) => new URL(route.request().url()).origin === appURL ? route.continue() : route.abort());
    await context.routeWebSocket("**/api/v1/ws?*", (ws) => ws.onMessage((raw) => {
      const message = JSON.parse(String(raw));
      if (message.type === "client_hello") {
        ws.send(JSON.stringify({ type: "hello", server_version: "fixture", hub_name: "Synthetic hub", features: ["managers"], resumed: false }));
        ws.send(JSON.stringify({ type: "machines", machines: [] }));
        ws.send(JSON.stringify({ type: "sessions", sessions: [] }));
      } else if (message.type === "app_ping") ws.send(JSON.stringify({ type: "pong" }));
    }));
    const page = await context.newPage();
    const errors = [];
    const beforeInspect = calls.filter((call) => call === "inspect").length;
    const beforeVerify = calls.filter((call) => call === "verify").length;
    page.on("pageerror", (error) => errors.push(error.message));
    await page.goto(`${appURL}/settings?tab=hub`);
    await page.locator('[data-settings-group="hub-recovery"] button').click();
    await page.locator("[data-recovery-page]").waitFor();
    await page.locator(`[data-recovery-bundle="${id}"]`).waitFor();
    assert.equal(new URL(page.url()).searchParams.get("page"), "recovery");
    assert.equal(calls.filter((call) => call === "inspect").length, beforeInspect, "Opening Recovery never inspects");
    assert.equal(calls.filter((call) => call === "verify").length, beforeVerify, "Opening Recovery never verifies");
    const tag = `${width}-${scheme}`;
    const shot = async (name) => {
      await page.evaluate(() => document.fonts.ready);
      assert.equal(await page.locator("html").evaluate((element) => element.classList.contains("dark")), scheme === "dark");
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, "No horizontal page overflow");
      await page.screenshot({ path: join(output, `${name}-${tag}.png`), fullPage: true, animations: "disabled" });
      screenshots.push(`${name}-${tag}.png`);
    };
    await shot("recovery-overview");
    await page.getByRole("button", { name: "Check integrity", exact: true }).click();
    await page.getByText("Offline integrity passed", { exact: true }).waitFor();
    await page.getByText("Database restore not verified", { exact: true }).waitFor();
    assert.equal(calls.filter((call) => call === "verify").length, beforeVerify);
    await page.getByRole("button", { name: "Verify databases", exact: true }).click();
    await page.getByText("Database verification passed", { exact: true }).waitFor();
    await page.getByText("This is not a restore drill", { exact: false }).waitFor();
    for (const label of ["Create backup", "Restore", "Download", "Export keys"]) assert.equal(await page.getByRole("button", { name: label, exact: true }).count(), 0);
    await shot("recovery-checked");
    await page.getByText("Create a bundle in a terminal", { exact: true }).scrollIntoViewIfNeeded();
    await shot("recovery-guidance");
    const buttons = await page.getByRole("button", { name: /^(Refresh|Check integrity|Verify databases)$/ }).evaluateAll((elements) => elements.map((element) => ({ width: element.clientWidth, scroll: element.scrollWidth, whitespace: getComputedStyle(element).whiteSpace })));
    assert.ok(buttons.every((button) => button.scroll <= button.width && button.whitespace === "nowrap"), "Recovery button labels stay on one line");
    assert.deepEqual(errors, []);
    await context.close();
  }
  assert.equal(calls.filter((call) => call === "inspect").length, 4);
  assert.equal(calls.filter((call) => call === "verify").length, 4);
  await writeFile(join(output, "run.json"), JSON.stringify({ fixture: "synthetic-only", screenshots, calls }, null, 2) + "\n");
  verdict = `WEB_RECOVERY PASS screenshots=${screenshots.length} output=${output}`;
} catch (error) {
  verdict = `WEB_RECOVERY FAIL ${String(error.message ?? error).replace(/\s+/g, " ")}`;
  process.exitCode = 1;
} finally {
  const errors = await cleanup();
  if (errors.length) {
    process.exitCode = 1;
    verdict = `WEB_RECOVERY FAIL cleanup ${errors.map((error) => String(error.message ?? error).replace(/\s+/g, " ")).join("; ")}`;
  } else clearTimeout(deadline);
  if (!timedOut) (process.exitCode ? console.error : console.log)(verdict);
}
