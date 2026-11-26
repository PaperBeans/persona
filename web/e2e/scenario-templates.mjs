// Scenario templates with synthetic HTTP/WS only. Never connects to a hub, GitHub, mail, or a model.
// node web/e2e/scenario-templates.mjs <loopback web URL> --shots <directory> --chromium <executable>
import assert from "node:assert/strict";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const flag = (name) => args[args.indexOf(name) + 1];
const base = args[0];
const shots = flag("--shots");
assert(["localhost", "127.0.0.1"].includes(new URL(base).hostname));
mkdirSync(shots, { recursive: true });
const watchdog = setTimeout(() => { console.error("TIMEOUT scenario template verification"); process.exit(124); }, 300_000);
const browser = await chromium.launch({ executablePath: flag("--chromium"), headless: true, args: ["--no-sandbox"] });

const source = (connector, names) => ({
  connector,
  status: "ok",
  sources: [{ id: connector, name: connector, tools: names.map((name) => ({ name, subscribable: true })) }],
});
const capable = [
  source("github", ["search", "read", "diff", "checks"]),
  source("mail", ["accounts", "search", "read"]),
];
const scenarios = [
  { key: "daily-digest", title: "Daily digest", repeat: "daily", time: "08:30", every_minutes: 0 },
  { key: "inbox-triage", title: "Inbox triage", repeat: "interval", time: "", every_minutes: 30 },
  { key: "pr-review", title: "PR review & follow-up", repeat: "interval", time: "", every_minutes: 120 },
];

try {
  for (const width of [1280, 390]) for (const theme of ["light", "dark"]) {
    const context = await browser.newContext({ viewport: { width, height: 900 }, colorScheme: theme, reducedMotion: "reduce" });
    await context.addInitScript((theme) => {
      localStorage.setItem("persona.token", "synthetic-scenario-token");
      localStorage.setItem("persona.language", "en");
      localStorage.setItem("persona.theme", theme);
    }, theme);
    let catalogs = capable;
    let tasks = [];
    const writes = [];
    await context.route("**/api/v1/**", async (route) => {
      const req = route.request();
      const path = new URL(req.url()).pathname.replace(/^\/api\/v1/, "");
      let body = {};
      if (path === "/machines") body = { machines: [] };
      else if (path === "/sessions") body = { sessions: [] };
      else if (path === "/assistant") body = { machine_id: "box", main: "", conversations: [], tasks: [], memory: [], timezone: "UTC" };
      else if (path === "/assistant/runs") body = { runs: [] };
      else if (path === "/subscriptions/catalog") body = { catalogs };
      else if (path === "/assistant/tasks" && req.method() === "GET") body = { tasks };
      else if (path === "/assistant/tasks" && req.method() === "POST") {
        const patch = req.postDataJSON();
        writes.push(patch);
        const task = { ...patch, id: `created-${writes.length}` };
        tasks = [task];
        body = { task };
      } else if (path === "/model-config") body = { providers: [] };
      else if (path === "/extensions/ui") body = { slots: [] };
      else if (path === "/extensions") body = { extensions: [] };
      else if (path === "/managers") body = { default: "assistant", managers: [{ id: "assistant", name: "Default assistant", default: true, builtin: true, online: true }] };
      if (!["GET", "HEAD"].includes(req.method()) && path !== "/assistant/tasks") throw new Error(`unexpected write ${req.method()} ${path}`);
      await route.fulfill({ contentType: "application/json", body: JSON.stringify(body) });
    });
    await context.routeWebSocket("**/api/v1/ws**", (ws) => {
      ws.send(JSON.stringify({ type: "hello", server_version: "synthetic", features: [] }));
      ws.onMessage((raw) => { if (JSON.parse(String(raw)).type === "app_ping") ws.send(JSON.stringify({ type: "pong" })); });
    });
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", (error) => errors.push(String(error)));
    page.setDefaultTimeout(10_000);
    await page.goto(`${base}/routines`);
    await page.getByRole("button", { name: "New", exact: true }).waitFor();

    const capture = async (name, scale = 1) => {
      await page.evaluate((scale) => document.documentElement.style.setProperty("--text-scale", String(scale)), scale);
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth), false, `${name}: page has horizontal overflow`);
      const dialog = page.getByRole("dialog");
      const overflow = await dialog.evaluate((element) => {
        const box = element.getBoundingClientRect();
        return [...element.querySelectorAll("input,select,textarea,button")].filter((node) => {
          const rect = node.getBoundingClientRect();
          return rect.width > 0 && (rect.left < box.left - 1 || rect.right > box.right + 1);
        }).map((node) => node.outerHTML);
      });
      assert.deepEqual(overflow, [], `${name}: controls stay inside the dialog`);
      await page.screenshot({ path: join(shots, `${name}-${width}-${theme}${scale === 2 ? "-large" : ""}.png`) });
    };
    const open = async (title) => {
      await page.getByRole("button", { name: "New", exact: true }).click();
      await page.getByRole("menuitem", { name: new RegExp(title.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")) }).click();
      await page.getByRole("dialog").waitFor();
    };
    const closeToasts = async () => {
      const close = page.locator('[aria-live="polite"]').getByRole("button", { name: "Close", exact: true });
      while (await close.count()) await close.first().click();
    };

    for (const scenario of scenarios) {
      await open(scenario.title);
      await page.getByText("Starts off in Plan mode.", { exact: false }).waitFor();
      assert.equal(await page.getByLabel("Specific model", { exact: false }).count(), 0, "model IDs stay out of initial configuration");
      if (scenario.key === "inbox-triage") {
        await page.getByLabel("Scope", { exact: false }).fill("Unread support messages");
        await page.getByRole("spinbutton", { name: "Interval (minutes)", exact: false }).fill("30");
      }
      if (scenario.key === "pr-review") {
        await page.getByRole("combobox", { name: "Data source", exact: true }).selectOption("github");
        assert(await page.getByRole("button", { name: "Review routine", exact: true }).isDisabled(), "repository is required");
        await page.getByLabel("Scope", { exact: false }).fill("example/project");
      }
      await capture(`scenario-${scenario.key}-configure`);
      await capture(`scenario-${scenario.key}-configure`, 2);
      await capture(`scenario-${scenario.key}-configure`);
      await page.getByRole("button", { name: "Review routine", exact: true }).click();
      const prompt = page.getByLabel("What should Persona do?", { exact: true });
      assert((await prompt.inputValue()).includes("This is a read-only reporting routine."));
      if (scenario.key === "inbox-triage") assert((await prompt.inputValue()).includes("Unread support messages"));
      if (scenario.key === "pr-review") assert((await prompt.inputValue()).includes("example/project"));
      await prompt.scrollIntoViewIfNeeded();
      await capture(`scenario-${scenario.key}-review`);
      await capture(`scenario-${scenario.key}-review`, 2);
      await capture(`scenario-${scenario.key}-review`);
      assert.equal(writes.length, scenarios.indexOf(scenario), "review creates nothing");
      await page.getByRole("button", { name: "Add", exact: true }).click();
      await page.getByRole("dialog").waitFor({ state: "hidden" });
      await closeToasts();
      const body = writes.at(-1);
      assert.deepEqual({ repeat: body.repeat, time: body.time, every_minutes: body.every_minutes }, { repeat: scenario.repeat, time: scenario.time, every_minutes: scenario.every_minutes });
      assert.equal(body.enabled, false);
      assert.equal(body.permission_mode, "plan");
      assert.equal(body.push, "asked");
      for (const key of ["launcher", "model", "effort", "cwd", "execution_machine_id"]) assert.equal(key in body, false, `${scenario.key}: ${key} stays inherited`);
    }

    catalogs = [];
    await open("PR review & follow-up");
    await page.getByRole("combobox", { name: "Data source", exact: true }).selectOption("github");
    await page.getByRole("alert").filter({ hasText: "GitHub does not currently expose" }).waitFor();
    assert(await page.getByRole("button", { name: "Review routine", exact: true }).isDisabled());
    await capture("scenario-pr-missing-capability");
    await page.getByRole("button", { name: "Use the available source instead", exact: true }).click();
    assert.equal(await page.getByRole("combobox", { name: "Data source", exact: true }).inputValue(), "conversation");
    await page.getByRole("button", { name: "Review routine", exact: true }).click();
    const fallbackPrompt = await page.getByLabel("What should Persona do?", { exact: true }).inputValue();
    assert(fallbackPrompt.includes("No live GitHub access is configured"));
    assert.equal(fallbackPrompt.includes("github_search"), false);
    await page.getByRole("button", { name: "Cancel", exact: true }).click();
    assert.equal(writes.length, scenarios.length, "missing capabilities and cancel create nothing");
    assert.deepEqual(errors, []);
    console.log(`PASS ${width} ${theme}: all three reviewed safe creations, capability fallback, light/dark and enlarged-text layout`);
    await context.close();
  }
} finally {
  await browser.close();
  clearTimeout(watchdog);
}
