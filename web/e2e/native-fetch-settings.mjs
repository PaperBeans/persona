// Real Chrome, the production web bundle, and an isolated HTTP hub fixture. No account or live hub is used.
// Usage: node web/e2e/native-fetch-settings.mjs <artifact-directory>
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { extname, join, resolve, sep } from "node:path";
import { chromium } from "playwright-core";

const [directory, ...extra] = process.argv.slice(2);
assert.ok(directory && !extra.length, "Supply an artifact directory outside the checkout");
const output = resolve(directory);
await mkdir(output, { recursive: true });
const root = resolve(new URL("../dist", import.meta.url).pathname);
const name = "Receiver smoke assistant";
const reads = [], writes = [];
const fixtures = {
  "/health": { version: "fixture" },
  "/managers": { managers: [{ id: "assistant", name, builtin: true, online: true, default: true }], default: "assistant" },
  "/machines": { machines: [] }, "/sessions": { sessions: [], total: 0, has_more: false },
  "/assistant": { conversations: [] }, "/todos": { todos: [] }, "/interactions": { interactions: [] },
  "/agents/sessions": { sessions: [] }, "/mutes": { spaces: [], sessions: [], allowed: [] },
  "/pins": { spaces: [], sessions: [], sections: [] }, "/workspaces": { workspaces: [] },
  "/extensions": { extensions: [] }, "/personas": { personas: [] }, "/assistant/personas": { personas: [] },
};
const mime = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".json": "application/json", ".svg": "image/svg+xml", ".woff2": "font/woff2", ".png": "image/png", ".ico": "image/x-icon" };
const server = createServer(async (request, response) => {
  try {
    const url = new URL(request.url, "http://127.0.0.1");
    if (url.pathname.startsWith("/api/v1/")) {
      const path = url.pathname.slice("/api/v1".length);
      if (!["GET", "HEAD"].includes(request.method)) {
        writes.push(path);
        response.writeHead(405, { "Content-Type": "application/json" });
        return response.end(JSON.stringify({ error: "Read-only fixture" }));
      }
      reads.push(path);
      response.writeHead(200, { "Content-Type": "application/json" });
      return response.end(JSON.stringify(fixtures[path] ?? {}));
    }
    const file = resolve(root, `.${decodeURIComponent(url.pathname)}`);
    assert.ok(file === root || file.startsWith(root + sep));
    const asset = extname(file) ? file : join(root, "index.html");
    const data = await readFile(asset);
    response.writeHead(200, { "Content-Type": mime[extname(asset)] ?? "application/octet-stream" });
    response.end(data);
  } catch {
    response.writeHead(404);
    response.end();
  }
});
await new Promise((done) => server.listen(0, "127.0.0.1", done));
const origin = `http://127.0.0.1:${server.address().port}`;
let browser;
const result = { status: "running", browser: "", nativeControls: null, runs: [] };
try {
  browser = await chromium.launch({ channel: "chrome", headless: true, args: ["--no-sandbox", "--disable-dev-shm-usage"] });
  result.browser = browser.version();
  for (const theme of ["light", "dark"]) {
    const context = await browser.newContext({ colorScheme: theme, viewport: { width: 1280, height: 900 }, serviceWorkers: "block" });
    await context.addInitScript(({ theme }) => {
      localStorage.setItem("persona.token", "fixture-token");
      localStorage.setItem("persona.language", "en");
      localStorage.setItem("persona.theme", theme);
    }, { theme });
    await context.route("**/*", (route) => new URL(route.request().url()).origin === origin ? route.continue() : route.abort());
    await context.routeWebSocket(/\/api\/v1\/ws(?:\?|$)/, (socket) => {
      socket.onMessage((message) => {
        const event = JSON.parse(String(message));
        if (event.type === "client_hello") socket.send(JSON.stringify({ type: "hello", protocol: 1, features: ["managers"], server_version: "fixture" }));
        if (event.type === "app_ping") socket.send(JSON.stringify({ type: "pong", id: event.id }));
      });
    });
    const page = await context.newPage();
    page.setDefaultTimeout(20_000);
    const errors = [];
    page.on("pageerror", (error) => errors.push(error.message));
    const run = { theme, settingsLoaded: false, health: false, errors };
    result.runs.push(run);
    await page.goto(`${origin}/settings?tab=managers`);
    const controls = await page.evaluate(async () => {
      const direct = (await fetch("/api/v1/health")).ok;
      const entry = { fetcher: fetch };
      let memberError = "";
      try { await entry.fetcher("/api/v1/health"); } catch (error) { memberError = error.name + ": " + error.message; }
      const bound = (await entry.fetcher.call(globalThis, "/api/v1/health")).ok;
      return { direct, bound, memberError };
    });
    result.nativeControls = controls;
    assert.ok(controls.direct && controls.bound);
    assert.match(controls.memberError, /Illegal invocation/);
    run.health = controls.direct;
    // This row is rendered by ManagersSettings only after the shared GET's JSON reaches the component.
    await page.getByRole("region", { name: "Assistants", exact: true }).getByRole("button", { name: new RegExp(name) }).waitFor();
    run.settingsLoaded = true;
    assert.deepEqual(errors, []);
    assert.ok(reads.includes("/managers"));
    assert.deepEqual(writes, []);
    await page.evaluate(() => document.fonts.ready);
    await page.screenshot({ path: join(output, `settings-${theme}.png`), fullPage: true });
    await context.close();
  }
  result.status = "passed";
} catch (error) {
  result.status = "failed";
  result.error = error.message;
  throw error;
} finally {
  await browser?.close();
  await new Promise((done) => server.close(done));
  await writeFile(join(output, "result.json"), JSON.stringify(result, null, 2) + "\n");
  console.log("NATIVE_FETCH_SETTINGS " + JSON.stringify(result));
}
