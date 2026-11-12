// Actual production app in owned shared-Chrome contexts, using ego-linux's native CDP pattern.
// Build first; node web/e2e/extension-logos.mjs <absolute-output-directory> [--cdp http://127.0.0.1:9222] [--icons-dir <directory>] [--bundled]
// Optional brand snapshots read github.png, notion.png and telegram.png from --icons-dir; no PNG enters the repository.
// No discover/auto-attach, default context, existing target navigation, Browser.close or target disposal.
// Fixture servers stop; all owned contexts/pages and the shared Chrome remain open.
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, isAbsolute, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { preview } from "vite";
const web = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const output = process.argv[2], cdpIndex = process.argv.indexOf("--cdp");
const cdp = cdpIndex >= 0 ? process.argv[cdpIndex + 1] : "http://127.0.0.1:9222";
const bundled = process.argv.includes("--bundled");
const iconsIndex = process.argv.indexOf("--icons-dir"), iconsDir = iconsIndex >= 0 ? process.argv[iconsIndex + 1] : null;
if (iconsDir) assert.ok(isAbsolute(iconsDir), "--icons-dir must be absolute");
assert.ok(output && isAbsolute(output) && !resolve(output).startsWith(resolve(web, "..") + "/"));
const market = { id: "official", official: true, name: "Official extensions", spec: "https://example.com/extensions/", has_token: false, extensions: 5, signature: { state: "verified" }, downloads_updated_at: "2026-11-11T00:00:00Z" };
const artwork = "https://example.com/persona-fixtures/extension-logos/";
// The API returns this descending-download order, deliberately not alphabetical. The client must keep it.
const items = (iconsDir ? [
  { id: "github", name: "GitHub", description: "Connect repository activity and review your work.", downloads: 18420, icon_url: artwork + "github.png", tags: ["Developer tools"] },
  { id: "notion", name: "Notion", description: "Read and search your notes and workspace pages.", downloads: 6300, icon_url: artwork + "notion.png", tags: ["Knowledge"] },
  { id: "telegram", name: "Telegram", description: "Send and receive messages through a connected channel.", downloads: 210, icon_url: artwork + "telegram.png", tags: ["Messaging"] },
] : [
  { id: "zebra-notes", name: "Zebra Notes", description: "Capture notes and find them when you need them.", downloads: 18420, icon_url: artwork + "notes.svg", tags: ["Notes"] },
  { id: "calm-calendar", name: "Calm Calendar", description: "Keep your daily plans together.", downloads: 6300, icon_url: artwork + "calendar.svg", tags: ["Calendar"] },
  { id: "broken-artwork", name: "Broken Artwork", description: "This image fails to load; the original extension icon stays.", downloads: 210, icon_url: artwork + "missing.svg" },
  { id: "zero-downloads", name: "Zero Downloads", description: "A new package with an explicit zero download count.", downloads: 0, icon_url: artwork + "zero.svg" },
  { id: "legacy-package", name: "Legacy Package", description: "An older API response without artwork or download statistics." },
]).map((item) => ({ ...item, market: market.id, market_name: market.name, version: "1.2.0", publisher: "Example publisher", permissions: [], downloads_updated_at: market.downloads_updated_at }));
if (bundled) for (const item of items) {
  if (item.icon_url && item.id !== "calm-calendar") item.icon_url = `/api/v1/extensions/-/catalog/official/${item.id}/icon?sha256=${"a".repeat(64)}`;
}
market.extensions = items.length;
const extensions = items.map((item) => ({ ...item,
  ...(bundled && item.icon_url?.startsWith("/") ? { icon_url: `/api/v1/extensions/${item.id}/-/icon?v=${item.version}` } : {}),
  kind: "process", source: "installed", status: "running", enabled: true, origin: `market:official/${item.id}`, config: {}, form: { fields: [] } }));
const calls = [], screenshots = [], errors = [], targets = [], sockets = [];
let browser, vite, mock, page, fixtureInit, verdict = "FAIL";
const cleanup = async () => {
  sockets.forEach((socket) => socket.close());
  if (vite) await new Promise((done) => vite.httpServer.close(done));
  if (mock) { mock.closeAllConnections(); await new Promise((done) => mock.close(done)); }
};
const deadline = setTimeout(() => { console.error("WEB_EXTENSION_LOGOS TIMEOUT"); void cleanup().finally(() => process.exit(124)); }, 240_000);
// A custom 256px square PNG for the authenticated fixture, not a publisher's official logo.
const bundledPNG = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAQAAAAEAAQMAAABmvDolAAAABlBMVEXUaD////8tfBWfAAAAi0lEQVR42u3YsQ2AIBRFUYyFpSM4iqPpaI7iCJYUxk/LD8TXCLG4t+U0JErxQiAiIurbbFmxArYcWAW4c9uL88GDowCjB2cBJg+u90vUrrF4cDcAqwcPAADoBWL2nwJ+DvhoAbwPAN4HAO8DgPcBAAB8AjpMPXJukoOVnLzkaCZnNz3cyemPiIioZQk6QdpJ++i31AAAAABJRU5ErkJggg==", "base64");
const svg = (kind) => {
  const color = kind === "calendar" ? "#27747f" : kind === "zero" ? "#7a5af8" : "#d4683f";
  return `<svg xmlns="http://www.w3.org/2000/svg" width="96" height="96" viewBox="0 0 96 96"><rect width="96" height="96" rx="20" fill="${color}"/><rect x="25" y="19" width="46" height="58" rx="5" fill="white"/><path d="M35 35h26M35 47h26M35 59h18" fill="none" stroke="${color}" stroke-width="5" stroke-linecap="round"/></svg>`;
};
async function connect(url) {
  const ws = new WebSocket(url); sockets.push(ws);
  await new Promise((done, reject) => { ws.addEventListener("open", done, { once: true }); ws.addEventListener("error", reject, { once: true }); });
  let id = 0; const pending = new Map(), listeners = new Map();
  ws.addEventListener("message", (event) => {
    const m = JSON.parse(event.data);
    if (m.id && pending.has(m.id)) { const p = pending.get(m.id); pending.delete(m.id); m.error ? p.reject(new Error(m.error.message)) : p.resolve(m.result); }
    else if (m.method) for (const fn of listeners.get(m.method) || []) void Promise.resolve(fn(m.params)).catch((e) => errors.push(String(e)));
  });
  return { send: (method, params = {}) => new Promise((resolve, reject) => { const n = ++id; pending.set(n, { resolve, reject }); ws.send(JSON.stringify({ id: n, method, params })); }), on: (method, fn) => { if (!listeners.has(method)) listeners.set(method, []); listeners.get(method).push(fn); } };
}
async function evaluate(code) {
  const result = await page.send("Runtime.evaluate", { expression: code, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) throw new Error(result.exceptionDetails.text + " " + result.exceptionDetails.exception?.description);
  return result.result.value;
}
async function wait(code, timeout = 20000) {
  const start = Date.now();
  while (Date.now() - start < timeout) {
    if (await evaluate(code)) return;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error("Timed out waiting for " + code);
}
async function capture(filename) {
  const result = await page.send("Page.captureScreenshot", { format: "png", captureBeyondViewport: true });
  await writeFile(join(output, filename), Buffer.from(result.data, "base64")); screenshots.push(filename);
}
try {
  await mkdir(output, { recursive: true });
  const brandImages = new Map();
  if (iconsDir) for (const name of ["github", "notion", "telegram"]) {
    const data = await readFile(join(iconsDir, `${name}.png`));
    assert.equal(data.subarray(0, 8).toString("hex"), "89504e470d0a1a0a", "Brand artwork must be PNG");
    brandImages.set(name + ".png", data);
  }
  mock = createServer(async (req, res) => {
    const path = new URL(req.url, "http://127.0.0.1").pathname; calls.push({ method: req.method, path });
    const json = (data) => { res.writeHead(200, { "Content-Type": "application/json" }); res.end(JSON.stringify(data)); };
    if (bundled && path.endsWith("/icon")) {
      assert.equal(req.headers.authorization, "Bearer synthetic-browser-token");
      assert.equal(req.headers["x-persona-client"], "web/1");
      assert.equal(req.headers.cookie, "fixture-hub-cookie=synthetic");
      assert.equal(req.headers.referer, undefined);
      const missing = path.includes("/broken-artwork/");
      res.writeHead(missing ? 404 : 200, { "Content-Type": missing ? "application/json" : "image/png" });
      return res.end(missing ? JSON.stringify({ error: "Not found" }) : bundledPNG);
    }
    if (path === "/api/v1/managers") return json({ default: "assistant", managers: [{ id: "assistant", name: "Persona", online: true, builtin: true }] });
    if (path.endsWith("/-/catalog")) {
      assert.equal(req.headers["x-persona-client"], "web/1");
      return json({ markets: [market], extensions: items });
    }
    if (path.endsWith("/-/markets")) return json({ markets: [market] });
    if (path.endsWith("/-/upgrades")) return json({ auto: false, upgrades: [] });
    if (path.endsWith("/extensions") || path.endsWith("/plugins")) return json({ extensions, can_install: true, markets: true });
    const extension = extensions.find((item) => path === `/api/v1/extensions/${item.id}/-/` || path === `/api/v1/plugins/${item.id}/-/`);
    if (extension) return json(extension);
    return json({ machines: [], sessions: [], agents: [], groups: [], items: [], interactions: [], todos: [], workflows: [], extensions: [], conversations: [], providers: [], channels: [], personas: [], count: 0, total: 0 });
  });
  await new Promise((done, reject) => { mock.once("error", reject); mock.listen(0, "127.0.0.1", done); });
  const reserve = createServer(); await new Promise((done) => reserve.listen(0, "127.0.0.1", done)); const port = reserve.address().port; await new Promise((done) => reserve.close(done));
  vite = await preview({ root: web, preview: { host: "127.0.0.1", port, strictPort: true, proxy: { "/api": { target: `http://127.0.0.1:${mock.address().port}`, changeOrigin: true, ws: true } } } });
  const appURL = `http://127.0.0.1:${vite.httpServer.address().port}`;
  const version = await (await fetch(cdp + "/json/version")).json(); browser = await connect(version.webSocketDebuggerUrl);
  for (const language of iconsDir ? ["en"] : ["en", "zh-Hans", "ja"]) for (const scheme of ["light", "dark"]) for (const size of language === "en" ? ["standard", "large"] : ["large"]) for (const width of !iconsDir && language === "en" ? [1440, 390] : [1440]) {
    const { browserContextId } = await browser.send("Target.createBrowserContext");
    await browser.send("Storage.setCookies", { browserContextId, cookies: [
      { name: "fixture-logo-cookie", value: "synthetic", domain: "example.com", path: "/", secure: true, sameSite: "None" },
      { name: "fixture-hub-cookie", value: "synthetic", domain: "127.0.0.1", path: "/", sameSite: "Lax" },
    ] });
    const seeded = await browser.send("Storage.getCookies", { browserContextId });
    assert.ok(seeded.cookies.some((cookie) => cookie.name === "fixture-logo-cookie"), "The no-Cookie assertion must run with a real cookie on the logo host");
    for (const [surface, sub] of [["official-list", "-market"], ["market-detail", `-market/official/${items[0].id}`], ["installed-list", ""], ["installed-detail", items[0].id]]) {
      const { targetId } = await browser.send("Target.createTarget", { url: "about:blank", browserContextId }); targets.push({ targetId, browserContextId, surface });
      const list = await (await fetch(cdp + "/json/list")).json(); const target = list.find((t) => t.id === targetId); assert.ok(target);
      page = await connect(target.webSocketDebuggerUrl);
      await page.send("Page.enable"); await page.send("Runtime.enable"); await page.send("Network.enable");
      page.on("Runtime.exceptionThrown", (e) => errors.push(e.exceptionDetails.exception?.description || e.exceptionDetails.text));
      await page.send("Emulation.setDeviceMetricsOverride", { width, height: 1100, deviceScaleFactor: 1, mobile: false });
      await page.send("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: scheme }, { name: "prefers-reduced-motion", value: "reduce" }] });
      const init = `localStorage.setItem('persona.token','synthetic-browser-token');localStorage.setItem('persona.language',${JSON.stringify(language)});localStorage.setItem('persona.theme',${JSON.stringify(scheme)});localStorage.setItem('persona.text-size',${JSON.stringify(size)});
        class FixtureSocket extends EventTarget { static CONNECTING=0;static OPEN=1;static CLOSING=2;static CLOSED=3;readyState=0; constructor(){super();setTimeout(()=>{this.readyState=1;this.dispatchEvent(new Event('open'));this.onopen?.(new Event('open'));},0);} send(raw){const m=JSON.parse(raw);if(m.type==='client_hello'){for(const data of [{type:'hello',server_version:'fixture',hub_name:'Fixture hub',features:['extensions','managers'],resumed:false},{type:'machines',machines:[]},{type:'sessions',sessions:[]}])setTimeout(()=>{const e=new MessageEvent('message',{data:JSON.stringify(data)});this.dispatchEvent(e);this.onmessage?.(e);},0);}else if(m.type==='app_ping')setTimeout(()=>{const e=new MessageEvent('message',{data:JSON.stringify({type:'pong'})});this.dispatchEvent(e);this.onmessage?.(e);},0);}close(){this.readyState=3;} } window.WebSocket=FixtureSocket;`;
      fixtureInit = init;
      await page.send("Page.addScriptToEvaluateOnNewDocument", { source: init });
      await page.send("Fetch.enable", { patterns: [{ urlPattern: "*", requestStage: "Request" }] });
      const ownedPage = page;
      ownedPage.on("Fetch.requestPaused", async ({ requestId, request }) => {
        const url = request.url;
        if (url.startsWith(artwork)) {
          const headers = Object.fromEntries(Object.entries(request.headers).map(([k, v]) => [k.toLowerCase(), v]));
          assert.equal(headers.authorization, undefined); assert.equal(headers.cookie, undefined); assert.equal(headers.referer, undefined); assert.equal(new URL(url).search, "");
          const brand = brandImages.get(new URL(url).pathname.split("/").pop());
          return ownedPage.send("Fetch.fulfillRequest", { requestId, responseCode: url.endsWith("missing.svg") ? 404 : 200, responseHeaders: [{ name: "Content-Type", value: brand ? "image/png" : "image/svg+xml" }, { name: "Access-Control-Allow-Origin", value: "*" }], body: (brand || Buffer.from(url.endsWith("missing.svg") ? "Not found" : svg(url.includes("calendar") ? "calendar" : url.includes("zero") ? "zero" : "notes"))).toString("base64") });
        }
        if (new URL(url).origin === appURL) return ownedPage.send("Fetch.continueRequest", { requestId });
        return ownedPage.send("Fetch.failRequest", { requestId, errorReason: "BlockedByClient" });
      });
      await page.send("Page.navigate", { url: `${appURL}/settings?tab=extensions${sub ? `&extension=${sub}` : ""}` });
      await wait(`document.querySelector('[data-extension-logo] img')?.naturalWidth > 0 && [...document.querySelectorAll('[data-extension-logo] img')].every(img=>img.complete&&img.naturalWidth>0&&getComputedStyle(img).opacity==='1')`);
      await evaluate("document.fonts.ready.then(()=>true)");
      await evaluate("Promise.all(document.getAnimations().filter(a=>Number.isFinite(a.effect?.getComputedTiming().iterations)).map(a=>a.finished.catch(()=>{}))).then(()=>true)");
      assert.equal(await evaluate("document.querySelector('vite-error-overlay')!==null"), false);
      assert.equal(await evaluate("document.documentElement.scrollWidth>innerWidth+1"), false);
      assert.equal(await evaluate("document.documentElement.classList.contains('dark')"), scheme === "dark");
      assert.equal(await evaluate("document.documentElement.dataset.textSize||'standard'"), size);
      const logos = await evaluate("[...document.querySelectorAll('[data-extension-logo]')].map(tile=>({image:!!tile.querySelector('img'),fallback:!!tile.querySelector('svg')}))");
      assert.equal(logos[0].image, true);
      if (surface.endsWith("list")) {
        const labels = await evaluate("[...document.querySelectorAll('button:has([data-extension-logo])')].map(n=>n.textContent)");
        assert.equal(labels.length, items.length); items.forEach((item, i) => assert.ok(labels[i].includes(item.name)));
        assert.equal(logos.filter((logo) => logo.fallback).length, iconsDir ? 0 : 2);
        const counts = await evaluate("[...document.querySelectorAll('[data-extension-downloads]')].map(n=>n.textContent)");
        assert.equal(counts.length, iconsDir ? 3 : 4); if (!iconsDir) assert.ok(counts[3].includes("0"));
      } else {
        const scope = surface === "installed-detail" ? "document.querySelector('[data-extension-settings]').parentElement" : "document";
        await wait(`${scope}.querySelector('[data-extension-logo] img')?.naturalWidth > 0`);
        assert.ok((await evaluate(`${scope}.querySelector('[data-extension-downloads]').textContent`)).includes("18,420"));
        assert.equal(await evaluate(`${scope}.querySelector('[data-extension-logo] img').naturalWidth>0`), true);
      }
      const attrs = await evaluate("[...document.querySelectorAll('[data-extension-logo] img')].map(n=>({loading:n.loading,decoding:n.decoding,policy:n.referrerPolicy,crossOrigin:n.crossOrigin}))");
      attrs.forEach((a) => assert.deepEqual(a, { loading: "lazy", decoding: "async", policy: "no-referrer", crossOrigin: "anonymous" }));
      if (iconsDir) {
        await wait("[...document.querySelectorAll('[data-extension-logo] img')].every(img=>img.naturalWidth===512&&getComputedStyle(img).backgroundColor==='rgb(255, 255, 255)')");
        assert.equal(await evaluate("[...document.querySelectorAll('[data-extension-logo] img')].every(img=>img.naturalWidth===512&&getComputedStyle(img).backgroundColor==='rgb(255, 255, 255)')"), true, "Transparent black brand marks need the same light token backing in both schemes");
      }
      await capture(`${language}-${surface}-${width}-${size}-${scheme}.png`);
    }
  }
  if (bundled) {
    // Real viewport acceptance: mounting a long catalog must not download every package before it is seen.
    const prototype = items[0];
    items.splice(0, items.length, ...Array.from({ length: 102 }, (_, i) => ({ ...prototype,
      id: `lazy_${i}`, name: `Example Notes ${i}`, downloads: 102 - i,
      icon_url: `/api/v1/extensions/-/catalog/official/lazy_${i}/icon?sha256=${"b".repeat(64)}`,
    })));
    market.extensions = items.length;
    const { browserContextId } = await browser.send("Target.createBrowserContext");
    const { targetId } = await browser.send("Target.createTarget", { url: "about:blank", browserContextId });
    targets.push({ targetId, browserContextId, surface: "lazy-catalog-102" });
    await browser.send("Storage.setCookies", { browserContextId, cookies: [{ name: "fixture-hub-cookie", value: "synthetic", domain: "127.0.0.1", path: "/", sameSite: "Lax" }] });
    const target = (await (await fetch(cdp + "/json/list")).json()).find(t => t.id === targetId);
    page = await connect(target.webSocketDebuggerUrl);
    await page.send("Page.enable"); await page.send("Runtime.enable");
    page.on("Runtime.exceptionThrown", e => errors.push(e.exceptionDetails.exception?.description || e.exceptionDetails.text));
    await page.send("Emulation.setDeviceMetricsOverride", { width: 1440, height: 1100, deviceScaleFactor: 1, mobile: false });
    await page.send("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: "light" }, { name: "prefers-reduced-motion", value: "reduce" }] });
    await page.send("Page.addScriptToEvaluateOnNewDocument", { source: fixtureInit + ";localStorage.setItem('persona.language','en');localStorage.setItem('persona.theme','light');localStorage.setItem('persona.text-size','standard');" });
    await page.send("Page.navigate", { url: `${appURL}/settings?tab=extensions&extension=-market` });
    await wait("document.querySelectorAll('[data-extension-logo]').length===102 && document.querySelector('[data-extension-logo] img')?.naturalWidth>0");
    await evaluate("document.fonts.ready.then(()=>new Promise(r=>setTimeout(r,250)))");
    const lazyCalls = () => calls.filter(call => call.path.startsWith("/api/v1/extensions/-/catalog/official/lazy_"));
    assert.ok(lazyCalls().length > 0 && lazyCalls().length < 20, "Only viewport rows may request package artwork");
    assert.ok(!lazyCalls().some(call => call.path.endsWith("/lazy_101/icon")), "The unseen final row must not fetch");
    await capture("en-lazy-catalog-102-top-light.png");
    await evaluate("document.querySelectorAll('[data-extension-logo]')[101].scrollIntoView({block:'center'})");
    await wait("document.querySelectorAll('[data-extension-logo]')[101].querySelector('img')?.naturalWidth>0");
    assert.ok(lazyCalls().some(call => call.path.endsWith("/lazy_101/icon")), "Scrolling the final row into view loads it");
    assert.ok(lazyCalls().length < 35, "Jumping to the end must not fetch the intervening rows");
    await capture("en-lazy-catalog-102-bottom-light.png");
  }
  assert.deepEqual(errors, []); assert.ok(calls.every((call) => call.method === "GET")); verdict = "PASS";
} catch (error) {
  console.error(error); process.exitCode = 1;
  if (page) { await capture("failure.png"); await writeFile(join(output, "failure.txt"), await evaluate("document.body.innerText")); }
} finally {
  try { await cleanup(); } catch (error) { console.error(error); verdict = "FAIL"; process.exitCode = 1; }
  clearTimeout(deadline);
  await writeFile(join(output, "result.json"), JSON.stringify({ verdict, fixture: bundled ? "authenticated bundled PNG and anonymous legacy artwork simulation" : iconsDir ? "brand-package snapshot simulation" : "generic compatibility snapshot simulation", online_hub: false, download_counts: "synthetic snapshot values, not live registry statistics", screenshots, calls, errors, targets, shared_browser_kept_open: true }, null, 2));
  console.log(`WEB_EXTENSION_LOGOS ${verdict} screenshots=${screenshots.length} output=${output}`);
}
