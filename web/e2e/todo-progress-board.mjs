// Read-only To-dos progress board (#12713), against synthetic stored data only.
// Start the ordinary mock-backed web server, then:
//   node web/e2e/todos-board.mjs http://127.0.0.1:<web port> [--shots <dir>] [--executable <chromium>]
// Covers grouping, responsibility, progress, owner decisions, exact staleness, persistence, navigation,
// desktop/phone and light/dark layouts. No hub write, model call or Agent start is allowed.
import assert from "node:assert/strict";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const flag = (name) => args.includes(name) ? args[args.indexOf(name) + 1] : undefined;
const base = (args[0] ?? "").replace(/\/$/, "");
const shots = flag("--shots");
const executable = flag("--executable");
if (!base) {
  console.error("usage: node web/e2e/todos-board.mjs <web URL> [--shots <dir>] [--executable <chromium>]");
  process.exit(2);
}
if (shots) mkdirSync(shots, { recursive: true });
const watchdog = setTimeout(() => {
  console.log("VERDICT: timeout after 4 minutes");
  process.exit(1);
}, 240_000);
watchdog.unref();

const NOW = "2026-11-23T12:00:00.000Z";
const at = (hours) => new Date(Date.parse(NOW) - hours * 3_600_000).toISOString();
const link = (id, title, released_at = null) => ({ machine_id: "machine", session_id: id, linked_at: at(120), released_at, title, cwd: "/Users/me/example", agent: "claude", source: "manual", via: "" });
const todo = (id, title, project, created, updated, extra = {}) => ({ id, title, project, notes: "", done_at: null, created_at: created, updated_at: updated, spaces: [], agents: [], ...extra });
const todos = [
  todo("t_supervised", "Release the weather app", "weather", at(140), at(3), {
    responsible: { kind: "person", name: "Release owner" },
    agents: [link("s_supervised", "Weather release worker")],
    supervision: { state: "needs_user", progress: { kind: "progress", text: "Reviewed the release candidate", at: at(1) }, reviews: [{ at: at(0.5), verdict: "needs_user" }] },
    ask: { outcome: "decide", verdict: "Ship this candidate?", asked_at: at(0.5), options: ["Ship", "Wait"], recommend: "Ship" },
  }),
  todo("t_external", "Record the migration", "weather", at(120), at(80), {
    responsible: { kind: "session", name: "External supervisor" },
    supervision: { state: "", progress: { kind: "progress", text: "Recorded the external migration result", at: at(80) } },
  }),
  todo("t_agent", "Build the shop candidate", "shop", at(50), at(20), { agents: [link("s_agent", "Shop release worker")] }),
  todo("t_boundary", "Review the exact boundary", "shop", at(72), at(72), { responsible: { kind: "person", name: "Boundary reviewer" } }),
  todo("t_unassigned", "Plan the next shop errand", "shop", at(1), at(1)),
  todo("t_no_progress", "An old unassigned errand", "", at(100), at(1)),
  todo("t_closed", "Closed work stays away", "weather", at(100), at(2), { done_at: at(2) }),
  todo("t_shelved", "Shelved work stays away", "shop", at(200), at(200), { origin: "conversation", agents: [link("s_shelved", "Old worker", at(100))] }),
];
const session = (id, title, updated, last_text) => ({
  machine_id: "machine", session_id: id, title, cwd: "/Users/me/example", state: "idle", status: "idle", agent: "claude", launcher: "claude",
  started_by_persona: true, assistant: false, updated_at: updated, state_since: updated, last_text, rev: 1,
});
const sessions = [
  session("s_supervised", "Weather release worker", at(3), "Prepared the release candidate"),
  session("s_agent", "Shop release worker", at(4), "Built the shop candidate"),
];
const projects = [{ id: "weather", name: "Weather", extra: "long · active" }, { id: "shop", name: "Shop", extra: "long · active" }];

async function open(browser, theme, width, textSize) {
  const context = await browser.newContext({ viewport: { width, height: width < 600 ? 844 : 1000 }, deviceScaleFactor: 1, colorScheme: theme, locale: "en-US", hasTouch: width < 600, reducedMotion: "reduce" });
  await context.addInitScript(({ theme, textSize }) => {
    localStorage.setItem("persona.token", "mock");
    localStorage.setItem("persona.theme", theme);
    localStorage.setItem("persona.language", "en");
    localStorage.setItem("persona.text-size", textSize);
    localStorage.setItem("persona.todos.view", "list");
  }, { theme, textSize });
  const writes = [];
  const socketCommands = [];
  await context.route(/\/api\/v1\/todos(?:\?.*)?$/, async (route) => {
    await route.fulfill({ json: { todos } });
  });
  await context.route("**/api/v1/todos/*", async (route) => {
    const path = new URL(route.request().url()).pathname;
    const id = path.split("/").at(-1);
    const found = todos.find((t) => t.id === id);
    if (found) return route.fulfill({ json: { todo: found } });
    return route.fulfill({ json: {} });
  });
  await context.route(/\/api\/v1\/sessions(?:\?.*)?$/, (route) => route.fulfill({ json: { sessions } }));
  await context.route(/\/api\/v1\/kb\/projects(?:\?.*)?$/, (route) => route.fulfill({ json: { items: projects } }));
  await context.routeWebSocket(/\/api\/v1\/ws/, (socket) => {
    const server = socket.connectToServer();
    socket.onMessage((message) => {
      socketCommands.push(String(message));
      server.send(message);
    });
    server.onMessage((message) => {
      let body;
      try { body = JSON.parse(String(message)); } catch { return socket.send(message); }
      if (body.type === "sessions_delta") return;
      if (body.type === "sessions") body.sessions = sessions.filter((s) => s.machine_id === body.machine_id);
      if (body.type === "todos") body.todos = todos;
      socket.send(JSON.stringify(body));
    });
  });
  const page = await context.newPage();
  page.setDefaultTimeout(15000);
  page.on("request", (request) => {
    if (!["GET", "OPTIONS"].includes(request.method())) writes.push(`${request.method()} ${request.url()}`);
  });
  await page.clock.setFixedTime(new Date(NOW));
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto(`${base}/todos`);
  await page.getByRole("radio", { name: "List", exact: true }).waitFor();
  return { context, page, writes, socketCommands, errors };
}

const group = (page, slug) => page.locator(`[data-testid="todo-board-group"][data-project="${slug}"]`);
const counts = async (page, slug) => {
  const g = group(page, slug);
  return {
    open: await g.getAttribute("data-open-count"),
    waiting: await g.getAttribute("data-waiting-count"),
    stale: await g.getAttribute("data-stale-count"),
  };
};
const check = (condition, message) => {
  assert.ok(condition, message);
  console.log(`ok   ${message}`);
};

const browser = await chromium.launch(executable ? { executablePath: executable } : { channel: "chrome" });
try {
  for (const [theme, width, textSize] of [["light", 1280, "standard"], ["dark", 1280, "large"], ["light", 390, "large"], ["dark", 390, "standard"]]) {
    const label = `${theme}/${width}px/${textSize}`;
    const { context, page, writes, socketCommands, errors } = await open(browser, theme, width, textSize);
    try {
      writes.length = 0;
      await page.getByRole("radio", { name: "Board", exact: true }).click();
      const board = page.locator('[data-testid="todos-board"]');
      await board.waitFor();
      check(page.url().includes("view=board"), `${label}: Board has its own restorable address`);
      const groupNames = await page.locator('[data-testid="todo-board-group"]').evaluateAll((elements) => elements.map((element) => element.getAttribute("data-project")));
      assert.deepEqual(groupNames, ["shop", "weather", ""], `${label}: stored projects and No project form three groups`);
      console.log(`ok   ${label}: stored projects and No project form three groups`);
      assert.deepEqual(await counts(page, "weather"), { open: "2", waiting: "1", stale: "1" });
      assert.deepEqual(await counts(page, "shop"), { open: "3", waiting: "0", stale: "0" });
      assert.deepEqual(await counts(page, ""), { open: "1", waiting: "0", stale: "1" });
      console.log(`ok   ${label}: per-project open / waiting / stale counts`);

      const supervised = board.locator('[data-todo="t_supervised"]');
      check((await supervised.locator('[data-testid="board-responsible"]').innerText()) === "Release owner", `${label}: stored responsibility leads the row`);
      check((await supervised.locator('[data-testid="board-progress"]').innerText()).includes("Reviewed the release candidate"), `${label}: latest supervised summary is shown`);
      check(await supervised.locator("time").getAttribute("datetime") === at(1), `${label}: summary and timestamp come from the same report`);
      check(await supervised.locator('[data-testid="board-owner"]').count() === 1, `${label}: an open owner decision is marked`);
      const agent = board.locator('[data-todo="t_agent"]');
      check((await agent.locator('[data-testid="board-responsible"]').innerText()) === "Shop release worker", `${label}: a linked worker is the fallback responsible party`);
      check((await agent.locator('[data-testid="board-progress"]').innerText()).includes("Built the shop candidate"), `${label}: linked-worker progress is shown`);
      check((await board.locator('[data-todo="t_unassigned"] [data-testid="board-responsible"]').innerText()) === "Unassigned", `${label}: missing responsibility is explicit`);
      const empty = board.locator('[data-todo="t_no_progress"]');
      check((await empty.locator('[data-testid="board-progress"]').innerText()) === "No progress yet", `${label}: missing progress is not invented from an edit`);
      check(await empty.locator('[data-testid="board-stale"]').count() === 1, `${label}: no-progress age starts at creation, not the last edit`);
      check(await board.locator('[data-todo="t_boundary"] [data-testid="board-stale"]').count() === 0, `${label}: exactly 72 hours is not longer than 72 hours`);
      check(await board.locator('[data-todo="t_external"] [data-testid="board-stale"]').count() === 1, `${label}: 80 hours is stale at the default`);
      check(await board.locator('[data-todo="t_closed"], [data-todo="t_shelved"]').count() === 0, `${label}: closed and shelved work is absent`);
      check(await board.getByRole("button", { name: /Complete|Hand to agent|Delete|Follow up/ }).count() === 0, `${label}: the board has no work-changing controls`);

      const threshold = page.getByRole("spinbutton", { name: "Stale after (hours)" });
      check(await threshold.inputValue() === "72", `${label}: fresh storage defaults to 72 hours`);
      if (width < 600) {
        const box = await threshold.boundingBox();
        check(Boolean(box && box.height >= 44), `${label}: the threshold control is at least 44 px tall on touch`);
      }
      await threshold.fill("96");
      await threshold.press("Tab");
      await page.waitForFunction(() => localStorage.getItem("persona.todos.board.staleHours") === "96");
      await page.reload();
      await page.locator('[data-testid="todos-board"]').waitFor();
      check(await page.getByRole("spinbutton", { name: "Stale after (hours)" }).inputValue() === "96", `${label}: a valid threshold survives reload`);
      await page.getByRole("spinbutton", { name: "Stale after (hours)" }).fill("0");
      await page.getByRole("spinbutton", { name: "Stale after (hours)" }).press("Tab");
      await page.getByText("Enter a positive number of hours", { exact: true }).waitFor();
      check(await page.evaluate(() => localStorage.getItem("persona.todos.board.staleHours")) === "96", `${label}: an invalid threshold does not replace the saved value`);
      await page.getByRole("spinbutton", { name: "Stale after (hours)" }).fill("48");
      await page.getByRole("spinbutton", { name: "Stale after (hours)" }).press("Tab");
      await page.waitForFunction(() => document.querySelector('[data-project="shop"]')?.closest('[data-testid="todo-board-group"]')?.getAttribute("data-stale-count") === "1");
      check(true, `${label}: changing the threshold recomputes existing rows locally`);

      await page.locator('[data-testid="todos-board"] [data-todo="t_agent"]').click();
      await page.locator('[data-testid="todos-detail-column"], [data-testid="todo-calendar-detail"]').first().waitFor();
      check(page.url().includes("view=board") && page.url().includes("id=t_agent"), `${label}: a row opens the existing detail without leaving Board`);
      await page.goBack();
      await page.locator('[data-testid="todos-board"]').waitFor();
      check(!page.url().includes("id="), `${label}: browser Back returns to the board`);
      check(await page.locator('[data-testid="todos-detail-column"]').isHidden(), `${label}: browser Back closes the detail beside the board`);
      check(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), `${label}: the layout fits the viewport`);
      if (textSize === "large") check(await page.locator("html").getAttribute("data-text-size") === "large", `${label}: the real Large text setting is active`);
      if (shots) await page.screenshot({ path: join(shots, `todos-board-${width}-${theme}-${textSize}.png`), fullPage: true, animations: "disabled" });
      assert.deepEqual(writes, [], `${label}: board reads and navigation issued no writes`);
      for (const command of socketCommands) {
        const body = JSON.parse(command);
        assert.equal(body.type, "client_hello", `${label}: only the read-only socket hello is allowed, got ${body.type}`);
      }
      assert.deepEqual(errors, [], `${label}: no browser errors`);
      console.log(`ok   ${label}: zero writes / model calls / Agent starts and no browser errors`);
    } finally {
      await context.close();
    }
  }
  console.log("VERDICT: pass");
} finally {
  await browser.close();
  clearTimeout(watchdog);
}
