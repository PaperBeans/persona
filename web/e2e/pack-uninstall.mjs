// Actual production app, synthetic loopback hub; no account, registry or real settings are contacted.
// Build web first, then: node web/e2e/pack-uninstall.mjs <absolute-output-directory>
// A pack's page names its members (#4122), and uninstalling it with its members (#4072): the members named as they're
// installed, and a removal that stopped part way listing what went, what didn't and what wasn't tried. Wide and phone width, light and dark.
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
assert.ok(output && isAbsolute(output) && !resolve(output).startsWith(resolve(web, "..") + "/"));
const market = "fixture-market";
const installed = (id, name, extra = {}) => ({ id, name, kind: "ui", source: "installed", status: "running", version: "1.0.0", enabled: true,
  origin: `market:${market}/${id}`, ...extra });
// Two members installed (named), one not (its id only).
const pack = installed("example-pack", "Example Pack", { kind: "extension_pack",
  extension_pack: [{ id: "example-notes", market: market, version: "1.0.0" }, { id: "example-ui" }, { id: "example-mail" }] });
const list = [pack, installed("example-notes", "Example Notes"), installed("example-ui", "Example UI")];
const partial = { ok: false, code: "partial_uninstall", error: "Removal didn’t finish.",
  removed_members: ["example-notes"], failed_members: [{ id: "example-ui", error: "it is still running" }], remaining_members: ["example-mail"] };
let browser, vite, mock, currentPage, verdict = "FAIL";
const cleanup = ownedCleanup(() => ({ browser, vite, mock }));
const deletes = [], screenshots = [];
const deadline = setTimeout(() => {
  console.error("WEB_PACK_UNINSTALL TIMEOUT");
  const force = setTimeout(() => process.exit(1), 5_000);
  void cleanup().then(() => { clearTimeout(force); process.exit(1); });
}, 180_000);
try {
  await mkdir(output, { recursive: true });
  mock = httpServer(async (req, res) => {
    try {
      const url = new URL(req.url, "http://127.0.0.1"), path = url.pathname;
      const json = (data, status = 200) => { res.writeHead(status, { "Content-Type": "application/json" }); res.end(JSON.stringify(data)); };
      if (path === "/api/v1/managers") return json({ default: "assistant", managers: [{ id: "assistant", name: "Hub manager", online: true, builtin: true }] });
      if (path === `/api/v1/extensions/${pack.id}/-/` && req.method === "DELETE") { deletes.push(url.search); return json(partial, 409); }
      if (path === `/api/v1/extensions/${pack.id}/-/`) return json(pack);
      if (path.endsWith("/-/upgrades")) return json({ auto: true, upgrades: [] });
      if (path === "/api/v1/extensions") return json({ extensions: list, can_install: true, markets: true });
      return json({ machines: [], sessions: [], agents: [], groups: [], items: [], interactions: [], todos: [], workflows: [],
        extensions: [], conversations: [], providers: [], channels: [], personas: [], count: 0, total: 0 });
    } catch (error) {
      res.writeHead(500, { "Content-Type": "application/json" }); res.end(JSON.stringify({ error: String(error) }));
    }
  });
  await new Promise((done, reject) => { mock.once("error", reject); mock.listen(0, "127.0.0.1", done); });
  const mockURL = `http://127.0.0.1:${mock.address().port}`;
  const reserve = httpServer();
  await new Promise((done) => reserve.listen(0, "127.0.0.1", done));
  const port = reserve.address().port;
  await new Promise((done) => reserve.close(done));
  vite = await preview({ root: web, preview: { host: "127.0.0.1", port, strictPort: true, proxy: { "/api": { target: mockURL, changeOrigin: true, ws: true } } } });
  const appURL = `http://127.0.0.1:${vite.httpServer.address().port}`;
  browser = await chromium.launch({ channel: process.env.PERSONA_E2E_CHANNEL || "chrome", headless: true, args: ["--no-sandbox"] });
  for (const width of [1440, 390]) for (const scheme of ["light", "dark"]) {
    const context = await browser.newContext({ viewport: { width, height: 1000 }, locale: "en-US", colorScheme: scheme });
    await context.addInitScript(({ scheme }) => {
      localStorage.setItem("persona.token", "synthetic-browser-token");
      localStorage.setItem("persona.language", "en"); localStorage.setItem("persona.theme", scheme);
    }, { scheme });
    await context.route("**/*", (route) => new URL(route.request().url()).origin === appURL ? route.continue() : route.abort());
    await context.routeWebSocket("**/api/v1/ws?*", (ws) => ws.onMessage((raw) => {
      const message = JSON.parse(String(raw));
      if (message.type === "client_hello") {
        ws.send(JSON.stringify({ type: "hello", server_version: "fixture", hub_name: "Fixture hub", features: ["extensions", "managers"], resumed: false }));
        ws.send(JSON.stringify({ type: "machines", machines: [] })); ws.send(JSON.stringify({ type: "sessions", sessions: [] }));
      } else if (message.type === "app_ping") ws.send(JSON.stringify({ type: "pong" }));
    }));
    const page = await context.newPage(), errors = [];
    currentPage = page;
    page.on("pageerror", (error) => errors.push(error.message));
    await page.goto(`${appURL}/settings?tab=extensions&extension=${pack.id}`);
    await page.evaluate((dark) => document.documentElement.classList.toggle("dark", dark), scheme === "dark");
    // The pack's page names its members, the id beside (#4122); the one not installed keeps its id.
    const members = page.locator('[data-pack-members]');
    await members.getByText("Example Notes", { exact: true }).waitFor();
    const listed = (await members.innerText()).replace(/\s+/g, " ");
    for (const part of ["Example Notes", "example-notes · fixture-market", "Example UI", "example-ui", "example-mail"]) assert.ok(listed.includes(part), `${part} in ${listed}`);
    await members.scrollIntoViewIfNeeded();
    { const file = `pack-members-${width}-${scheme}.png`; await members.screenshot({ path: join(output, file) }); screenshots.push(file); }
    await page.getByRole("button", { name: "Uninstall", exact: true }).first().click();
    const dialog = page.getByRole("dialog");
    // By name; the member that isn't installed keeps its id.
    await dialog.getByText("Example Notes, Example UI, example-mail. Left unchecked, they stay installed.", { exact: true }).waitFor();
    const choice = dialog.getByText("Also uninstall pack members", { exact: true });
    await choice.click();
    // The two choices look alike: rows of one box.
    const rows = await dialog.locator("label:has(input[type=checkbox])").evaluateAll((labels) => labels.map((l) => [getComputedStyle(l).padding, l.parentElement === labels[0].parentElement]));
    assert.equal(rows.length, 2); assert.equal(rows[0][0], rows[1][0]); assert.ok(rows.every(([, same]) => same));
    const settle = () => page.evaluate(async () => {
      await document.fonts.ready;
      await Promise.all(document.getAnimations().filter((a) => Number.isFinite(a.effect?.getComputedTiming().iterations)).map((a) => a.finished.catch(() => {})));
    });
    await settle();
    const shot = async (name) => { const file = `pack-uninstall-${name}-${width}-${scheme}.png`; await page.screenshot({ path: join(output, file), fullPage: true }); screenshots.push(file); };
    await shot("choice");
    await dialog.getByRole("button", { name: "Uninstall", exact: true }).click();
    const part = dialog.locator("[data-partial-uninstall]");
    await part.waitFor();
    await settle();
    assert.deepEqual(await part.locator("li").allTextContents(),
      ["Uninstalled: Example Notes", "Not uninstalled: Example UI (it is still running)", "Not tried, still installed: example-mail"]);
    assert.ok((await part.textContent()).includes("\"Example Pack\" is still installed."));
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false);
    await shot("partial");
    assert.deepEqual(errors, []); await context.close();
  }
  assert.ok(deletes.length === 4 && deletes.every((q) => q === "?keep_data=1&members=1"), `deletes: ${deletes}`);
  verdict = "PASS";
} catch (error) {
  console.error(error);
  if (currentPage && !currentPage.isClosed()) {
    await currentPage.screenshot({ path: join(output, "failure.png"), fullPage: true });
    await writeFile(join(output, "failure.txt"), await currentPage.locator("body").innerText());
  }
  process.exitCode = 1;
} finally {
  const errors = await cleanup();
  clearTimeout(deadline);
  if (errors.length) { console.error(errors); verdict = "FAIL"; process.exitCode = 1; }
  console.log(`WEB_PACK_UNINSTALL ${verdict} ${screenshots.length} screenshots`);
}
