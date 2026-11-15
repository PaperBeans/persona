// Run against Vite on loopback: node web/e2e/work-timeline-order.mjs <origin> <screenshot directory>
import assert from "node:assert/strict";
import { mkdir } from "node:fs/promises";
import { join } from "node:path";
import { chromium } from "playwright-core";

const [address, output] = process.argv.slice(2);
const base = new URL(address);
assert.ok(["127.0.0.1", "localhost", "[::1]"].includes(base.hostname));
assert.ok(!base.username && !base.password && !base.search && !base.hash && base.pathname === "/");
await mkdir(output, { recursive: true });
const MID = "example-node", SID = "synthetic-timeline";
const machine = { id: MID, name: "Example computer", online: true };
const start = Date.now() - 12000;
const item = (seq, second, role, kind, extra) => ({ id: `item-${seq}#0`, seq, ts: new Date(start + second * 1000).toISOString(), role, kind, ...extra });
const items = [
  item(1, 0, "user", "text", { text: "Check this example." }),
  item(2, 2, "assistant", "text", { text: "A: I’ll inspect the file." }),
  item(3, 3, "assistant", "tool_use", { tool_name: "Bash", tool_use_id: "first", tool_input: { command: "echo first-check" } }),
  item(4, 4, "user", "tool_result", { tool_use_id: "first", text: "ok" }),
  item(5, 6, "assistant", "text", { text: "B: I found the file. Now I’ll verify it." }),
  item(6, 7, "assistant", "tool_use", { tool_name: "Bash", tool_use_id: "later", tool_input: { command: "echo later-check" } }),
  item(7, 9, "user", "tool_result", { tool_use_id: "later", text: "ok" }),
];
const browser = await chromium.launch({ channel: "chrome", headless: true });
try {
  for (const theme of ["light", "dark"]) for (const textSize of ["standard", "large"]) {
    let live = true;
    const session = { machine_id: MID, session_id: SID, title: "Synthetic work timeline", agent: "codex", status: "working", state: "working", capabilities: { read: true, send: true } };
    const context = await browser.newContext({ viewport: { width: 1280, height: 1050 }, colorScheme: theme, serviceWorkers: "block" });
    await context.addInitScript(({ theme, textSize }) => {
      localStorage.setItem("persona.token", "synthetic-only");
      localStorage.setItem("persona.language", "en");
      localStorage.setItem("persona.theme", theme);
      localStorage.setItem("persona.text-size", textSize);
    }, { theme, textSize });
    await context.route("**/*", async route => {
      const request = route.request(), url = new URL(request.url());
      if (url.origin !== base.origin) return route.abort("blockedbyclient");
      if (!["GET", "HEAD"].includes(request.method())) return route.fulfill({ json: { ok: true } });
      if (!url.pathname.startsWith("/api/")) return route.continue();
      const path = url.pathname.replace(/^\/api\/v1/, "");
      if (path.endsWith("/items")) return route.fulfill({ json: {
        items: url.searchParams.has("after_seq") ? [] : items, has_more: false, history: "fixture", to_seq: items.at(-1).seq,
      } });
      const json = {
        "/sessions": { sessions: [session], total: 1, has_more: false }, [`/sessions/${MID}/${SID}/info`]: session,
        "/machines": { machines: [machine] }, "/assistant": { machine_id: MID, main: SID, conversations: [session] },
        "/interactions": { interactions: [] }, "/todos": { todos: [] }, "/mutes": { spaces: [], sessions: [], allowed: [] },
        "/pins": { spaces: [], sessions: [], sections: [] }, "/agents/sessions": { sessions: [] }, "/agents/prefs": {},
        "/managers": { managers: [], default_id: "" }, "/personas": { personas: [] }, "/assistant/personas": { personas: [] },
        "/workspaces": { workspaces: [] }, "/extensions": { extensions: [] },
      }[path] ?? {};
      return route.fulfill({ json });
    });
    await context.routeWebSocket("**/*", socket => {
      assert.equal(new URL(socket.url()).host, base.host);
      socket.send(JSON.stringify({ type: "hello", server_version: "mock", protocol: 1, features: [] }));
      socket.send(JSON.stringify({ type: "machines", machines: [machine] }));
      socket.send(JSON.stringify({ type: "sessions", machine_id: MID, sessions: [session] }));
      socket.onMessage(raw => { const event = JSON.parse(String(raw)); if (event.type === "app_ping") socket.send(JSON.stringify({ type: "pong", id: event.id })); });
    });
    const page = await context.newPage();
    page.setDefaultTimeout(12000);
    const url = new URL(`/s/${MID}/${SID}`, base).href;
    await page.goto(url);
    for (const state of ["live", "reloaded"]) {
      const group = page.locator("[data-work-group]").first();
      const slot = page.locator("[data-latest-text]").last();
      await slot.getByText(/B: I found/).waitFor();
      assert.equal(await group.getAttribute("data-work-group"), live ? "live" : "done");
      const button = group.locator(":scope > button");
      assert.equal(await button.getAttribute("aria-expanded"), "false");
      await button.press("Enter");
      const detail = group.locator(":scope > div").locator("[data-work-detail]").first();
      const expected = ["A: I’ll inspect", "echo first-check", "B: I found", "echo later-check"];
      await detail.getByText(/echo later-check/).waitFor();
      const actual = await detail.innerText();
      let position = -1;
      for (const text of expected) {
        const next = actual.indexOf(text);
        assert.ok(next > position, `${state}: ${text} out of order in ${actual}`);
        position = next;
      }
      assert.match(await button.getAttribute("title"), /start of the exchange/);
      const runs = detail.locator("[data-work-group] > button");
      assert.equal(await runs.count(), 2);
      assert.match(await runs.first().innerText(), /Worked for 4s/);
      assert.match(await runs.last().innerText(), live ? /Working/ : /Worked for 3s/);
      if (!live) assert.match(await button.innerText(), /Worked for 9s/);
      assert.match(await runs.last().getAttribute("title"), /preceding message/);
      await page.locator("[data-segment]").filter({ has: slot }).screenshot({ path: join(output, `${state}-${theme}-${textSize}.png`) });
      if (live) {
        live = false;
        session.status = session.state = "idle";
        await page.reload();
      }
    }
    await context.close();
    console.log(`PASS ${theme}/${textSize}: live and reloaded order, collapsed default and clock boundaries`);
  }
} finally {
  await browser.close();
}
