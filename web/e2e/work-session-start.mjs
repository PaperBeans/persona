// node web/e2e/work-session-start.mjs <mock web origin> <screenshot directory>
import assert from "node:assert/strict";
import { mkdir } from "node:fs/promises";
import { resolve } from "node:path";
import { chromium } from "playwright-core";

const [address, artifacts] = process.argv.slice(2);
assert.ok(address && artifacts);
const origin = new URL(address).origin;
assert.ok(["127.0.0.1", "localhost"].includes(new URL(origin).hostname));
assert.equal((await (await fetch(origin + "/api/v1/health")).json()).version, "mock");
await mkdir(artifacts, { recursive: true });
const browser = await chromium.launch({ channel: "chrome", headless: true });
const machine = { id: "desk", name: "开发机", online: true, engine: "herdr" };
const cwd = "/projects/sample";
const main = "persona-main";
const work = { machine_id: machine.id, session_id: "work-created", agent: "codex", title: "样例项目", cwd, launcher: "codex-team", status: "idle" };
const title = "新建工作会话";
try {
  for (const [theme, width, height, hasMain] of [["light", 1260, 880, false], ["dark", 390, 844, true]]) {
    const context = await browser.newContext({ colorScheme: theme, viewport: { width, height }, serviceWorkers: "block" });
    await context.addInitScript(({ theme }) => {
      localStorage.setItem("persona.token", "fixture-token");
      localStorage.setItem("persona.language", "zh-Hans");
      localStorage.setItem("persona.theme", theme);
    }, { theme });
    const writes = [];
    const assistant = { machine_id: machine.id, main: hasMain ? main : undefined, conversations: [] };
    await context.route("**/*", async (route) => {
      const req = route.request();
      const url = new URL(req.url());
      if (url.origin !== origin) return route.abort();
      if (!url.pathname.startsWith("/api/v1/")) return route.continue();
      const path = url.pathname.slice("/api/v1".length);
      if (!["GET", "HEAD"].includes(req.method())) {
        writes.push({ path, body: req.postDataJSON() });
        return route.fulfill({ json: path === "/sessions" ? { session: work } : { ok: true } });
      }
      const data = {
        "/assistant": assistant,
        "/sessions": { sessions: [], total: 0, has_more: false, revision: "fixture" },
        "/machines": { machines: [machine] }, "/interactions": { interactions: [] },
        "/todos": { todos: [] }, "/mutes": { spaces: [], sessions: [], allowed: [] },
        "/pins": { spaces: [], sessions: [], sections: [] }, "/managers": { managers: [] },
        "/personas": { personas: [] }, "/assistant/personas": { personas: [] },
        "/machines/desk/launchers": { launchers: [{ name: "claude", agent_type: "claude" }, { name: "codex-team", agent_type: "codex" }, { name: "codex-local", agent_type: "codex" }] },
        "/machines/desk/launchers/codex-team/start-options": {
          agent_type: "codex", model_mode: "selectable", prompt_required: true, inline_prompt: true, attachments: false,
          models: [{ id: "model-fast", name: "Fast" }, { id: "model-large", name: "Large" }],
        },
      };
      if (path === "/machines/desk/dirs") {
        return route.fulfill({ json: { path: url.searchParams.get("path") || "/projects", entries: url.searchParams.get("path") ? [] : [{ name: "sample", path: cwd, is_git: true }] } });
      }
      return route.fulfill({ json: data[path] ?? { items: [] } });
    });
    await context.routeWebSocket(/\/api\/v1\/ws(?:\?|$)/, (socket) => {
      socket.send(JSON.stringify({ type: "hello", server_version: "mock", protocol: 1, features: [], hub_name: "示例 Hub" }));
      socket.send(JSON.stringify({ type: "machines", machines: [machine] }));
      socket.onMessage((raw) => {
        const event = JSON.parse(String(raw));
        if (event.type === "app_ping") socket.send(JSON.stringify({ type: "pong", id: event.id }));
      });
    });
    const page = await context.newPage();
    page.setDefaultTimeout(12_000);
    await page.goto(origin + "/j");
    await page.getByRole("button", { name: title, exact: true }).filter({ visible: true }).click();
    const dialog = page.getByRole("dialog", { name: title });
    const selects = dialog.getByRole("combobox");
    await selects.nth(0).selectOption(machine.id);
    await selects.nth(1).selectOption(cwd);
    await selects.nth(2).selectOption("codex");
    await selects.nth(3).selectOption("codex-team");
    await selects.nth(4).selectOption("model-large");
    const start = dialog.getByRole("button", { name: "启动", exact: true });
    assert.ok(await start.isDisabled(), "Codex needs a first message");
    await dialog.locator("textarea").fill("请读一下项目说明。");
    assert.ok(await start.isEnabled());
    await page.evaluate(() => document.fonts.ready);
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "The form must fit the viewport");
    await page.screenshot({ path: resolve(artifacts, `work-session-${theme}.png`), animations: "disabled" });
    await start.click();
    await page.waitForURL("**/s/desk/work-created");
    assert.deepEqual(writes.filter(({ path }) => path === "/sessions" || path === "/assistant/conversations"), [{
      path: "/sessions",
      body: { work_session: true, machine_id: "desk", cwd, agent_type: "codex", launcher: "codex-team", model: "model-large", prompt: "请读一下项目说明。" },
    }]);
    await page.goto(origin + "/j");
    await page.getByRole("button", { name: title, exact: true }).filter({ visible: true }).waitFor();
    assert.equal(new URL(page.url()).pathname, "/j");
    await context.close();
    console.log(`${theme}: ${hasMain ? "active main" : "welcome"} → model selection → ordinary session → main route`);
  }
} finally {
  await browser.close();
}
