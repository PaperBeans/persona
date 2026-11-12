// Screenshots of a model provider's key as a secret provider's reference (#3962 step 4c) in the real web app: a provider
// whose key is a reference (its page, its form with the switch on and a computer's own key), and a preset's key step
// taking one. Light and dark, desktop and phone.
// Usage: node web/e2e/provider-key-references.mjs <web-origin> <artifactDir> [--chromium <executable>]
// Every hub request and the socket are answered in the browser with invented data; nothing reaches a hub.
import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const chromiumAt = args.includes("--chromium") ? args.splice(args.indexOf("--chromium"), 2)[1] : undefined;
const [address, artifacts, ...extra] = args;
assert.ok(address && artifacts && !extra.length, "Supply the web origin and artifact directory");
const base = new URL(address);
assert.ok(["127.0.0.1", "localhost", "[::1]"].includes(base.hostname), "Use a loopback web server");
const output = resolve(artifacts);
await mkdir(output, { recursive: true });

const AT = "2026-01-01T10:00:00Z";
const LAPTOP = { id: "laptop", name: "Laptop", online: true, os: "darwin" };
const DESK = { id: "desk", name: "Desk", online: true, os: "linux" };
const REF = { provider: "example-vault", provider_name: "Example Vault", reference: "op://Example/Gateway/key", title: "Gateway › key" };
const GATEWAY = {
  name: "example_gw", display_name: "Example Gateway", source: "persona", kind: "launcher", base_url: "https://gateway.example.com",
  model: "example-large", has_key: true, auth: "bearer", key_reference: REF, models: [{ id: "example-large", context_window: 200000 }],
  share: "all", delivery: "on_demand", own_service: "skip", key_machines: [LAPTOP.id],
  machine_key_references: { [LAPTOP.id]: { ...REF, reference: "op://Example/Laptop gateway/key", title: "Laptop gateway › key" } },
};
const PRESETS = [{ id: "builtin/example", plugin: "builtin", name: "Example", display_name: "Example Cloud", description: "Pay as you go", kind: "launcher",
  api: "messages", base_url: "https://api.example.com/anthropic", auth: "bearer", model: "example-large", models: [{ id: "example-large" }] }];

const TYPED = "op://Example/Gateway/key2";
const PRESET_TYPED = "op://Example/Example Cloud/credential";
/** The connection tests and model lists among the page's requests. */
const probes = (posts) => posts.filter((p) => p.path === "/launchers/test" || p.path === "/launchers/discover").map((p) => p.path);
const result = { status: "running", runs: [] };
let step = "start";
const browser = await chromium.launch(chromiumAt ? { executablePath: chromiumAt, args: ["--no-sandbox"] } : { channel: "chrome" });
const deadline = setTimeout(() => { result.status = "timeout"; void browser.close(); }, 300_000);
const settle = (page) => page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
try {
  for (const [width, height] of [[1280, 820], [390, 844]]) for (const theme of ["light", "dark"]) {
    const name = `${width}x${height}-${theme}`;
    const touch = width < 500;
    const context = await browser.newContext({ viewport: { width, height }, locale: "en-US", colorScheme: theme, reducedMotion: "reduce", isMobile: touch, hasTouch: touch });
    await context.addInitScript(`localStorage.setItem('persona.token','example');localStorage.setItem('persona.language','en');localStorage.setItem('persona.theme','${theme}');`);
    await context.route(/^https?:\/\/(?!127\.0\.0\.1|localhost|\[::1\])/, (route) => route.abort());
    const posts = []; // every write and probe the page sends: a typed reference must never reach a test or a model list
    await context.route("**/api/v1/**", (route) => {
      const path = new URL(route.request().url()).pathname.replace("/api/v1", "");
      if (route.request().method() !== "GET") {
        posts.push({ path, body: route.request().postData() ?? "" });
        return route.fulfill({ status: 200, contentType: "application/json", body: "{}" });
      }
      const data = path === "/machines" ? { machines: [LAPTOP, DESK] }
        : path === "/launchers" ? { launchers: [GATEWAY], sync: [] }
        : path === "/launchers/presets" ? { presets: PRESETS }
        : path === `/launchers/${GATEWAY.name}` ? { launcher: GATEWAY }
        : path === `/launchers/${GATEWAY.name}/uses` ? { uses: [] }
        : path === `/launchers/${GATEWAY.name}/quota` ? { unsupported: true }
        : path === "/sessions" ? { sessions: [] }
        : path === "/interactions" ? { interactions: [] }
        : path === "/assistant" ? { machine_id: LAPTOP.id, conversations: [], tasks: [], memory: [] }
        : path === "/todos" ? { todos: [] }
        : null;
      // What this check doesn't need answers as a hub without it would.
      if (!data) return route.fulfill({ status: 404, contentType: "application/json", body: JSON.stringify({ error: "not_found" }) });
      return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(data) });
    });
    await context.routeWebSocket("**/api/v1/ws**", (ws) => {
      ws.send(JSON.stringify({ type: "hello", server_version: "example", hub_name: "Example hub",
        features: ["launcher_secret_references", "launcher_scope", "builtin_presets", "launcher_auto_name", "extension_secret_references"] }));
      ws.onMessage((m) => { if (String(m).includes("app_ping")) ws.send(JSON.stringify({ type: "pong" })); });
    });
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", (e) => errors.push(String(e)));

    // The provider's page: its key says where the value comes from.
    step = `${name} provider`;
    await page.goto(new URL(`/settings?tab=models&launcher=${GATEWAY.name}`, base).href);
    await page.getByText("From Example Vault · Gateway › key").first().waitFor({ timeout: 15_000 });
    await settle(page);
    await page.screenshot({ path: `${output}/${name}-provider.png` });

    // Its form: the switch on, the stored reference named.
    step = `${name} provider form`;
    await page.getByRole("button", { name: "Edit", exact: true }).first().click();
    const ref = page.locator("[data-key-reference]").first();
    await ref.waitFor({ timeout: 15_000 });
    await ref.scrollIntoViewIfNeeded();
    await settle(page);
    await page.screenshot({ path: `${output}/${name}-provider-form.png` });
    // A new reference typed: the test and the model list wait for the save.
    await ref.fill(TYPED);
    await page.waitForTimeout(1500); // past the form's automatic check
    assert.deepEqual(probes(posts), [], `${name}: a typed reference went to a probe`);

    // A computer's own key as a reference.
    step = `${name} computer key`;
    for (let i = 0; i < 2; i++) await page.getByRole("button", { name: "Next", exact: true }).first().click();
    const keys = page.getByRole("button", { name: /Keys for single computers/ }).first();
    await keys.waitFor({ timeout: 10_000 });
    if ((await keys.getAttribute("aria-expanded")) !== "true") await keys.click();
    const laptop = page.getByText("From Example Vault · Laptop gateway › key").first();
    await laptop.waitFor({ timeout: 10_000 });
    await laptop.scrollIntoViewIfNeeded();
    await settle(page);
    await page.screenshot({ path: `${output}/${name}-computer-key.png` });

    // A preset's key step taking a reference: no key test, checked when it is added.
    step = `${name} preset key`;
    await page.goto(new URL("/settings?tab=models&add=1", base).href);
    await page.getByText("Example Cloud").first().click();
    const sw = page.getByRole("switch", { name: "Use a reference" }).first();
    await sw.waitFor({ timeout: 15_000 });
    await sw.click();
    await page.locator("[data-key-reference]").first().fill(PRESET_TYPED);
    await page.getByText("Checked when you save it").first().waitFor({ timeout: 10_000 });
    await page.waitForTimeout(1500); // past the key step's automatic test
    assert.deepEqual(probes(posts), [], `${name}: a preset's typed reference went to its key test`);
    assert.ok(!posts.some((p) => p.body.includes(TYPED) || p.body.includes(PRESET_TYPED)), `${name}: a typed reference was sent`);
    await settle(page);
    await page.screenshot({ path: `${output}/${name}-preset-key.png` });

    const run = { name, errors };
    result.runs.push(run);
    console.log(JSON.stringify(run));
    assert.deepEqual(errors, [], `${name}: page errors`);
    await context.close();
  }
  result.status = "pass";
} catch (error) {
  result.status = result.status === "timeout" ? "timeout" : "fail";
  result.error = `${step}: ${String(error)}`;
} finally {
  clearTimeout(deadline);
  await writeFile(`${output}/results.json`, JSON.stringify(result, null, 2));
  await browser.close().catch(() => {});
  console.log(`${result.status.toUpperCase()} provider key reference screenshots${result.error ? `: ${result.error.split("\n")[0]}` : ""}`);
  process.exit(result.status === "pass" ? 0 : 1);
}
