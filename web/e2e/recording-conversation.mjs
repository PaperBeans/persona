// A recorded meeting's conversation (#3862), with synthetic fixtures and no live hub: on a hub that lists
// meeting_conversation, the private switch, a running room meeting's one thread (read-only, the room's state, batches
// folded), questions asked in the room that got no answer (why, or still being answered; one that never went in after the
// thread), an ended one's follow-up composer that opens the follow-up, the thread after the divider, and a room that is
// unavailable showing its asks. Run against an already running Vite server.
// BASE_URL=http://127.0.0.1:5791 PERSONA_E2E_CHROMIUM=<chrome> SHOTS_DIR=/tmp/mm/shots node web/e2e/recording-conversation.mjs
import assert from "node:assert/strict";
import { mkdir, readFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { chromium } from "playwright-core";

const base = new URL(process.env.BASE_URL || "http://127.0.0.1:5791");
assert.ok(["127.0.0.1", "localhost", "[::1]"].includes(base.hostname), "Use a loopback Vite server");
assert.ok(["http:", "https:"].includes(base.protocol) && !base.username && !base.password && base.pathname === "/" && !base.search && !base.hash, "Use an origin without credentials or a route");
const executablePath = process.env.PERSONA_E2E_CHROMIUM || process.env.CHROME_BIN;
assert.ok(executablePath, "Set PERSONA_E2E_CHROMIUM or CHROME_BIN");
const output = resolve(process.env.SHOTS_DIR || "/tmp/mm/shots");
await mkdir(output, { recursive: true });
const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
const room = "dddddddd-0000-4000-8000-000000000001";
const follow = "eeeeeeee-0000-4000-8000-000000000002";
const typedAsk = "11111111-1111-4111-8111-111111111111";
const heardAsk = "22222222-2222-4222-8222-222222222222";
const detailPath = `/kb/recordings/${id}`;
const livePath = `/recordings/${id}/live`;
const MACHINE = { id: "server", name: "Hub", online: true, os: "linux" };
const ready = { state: "ready", machine_id: "server", session_id: room };
const ended = { state: "ended", machine_id: "server", session_id: room };
const unavailable = { state: "unavailable", problem: "machine_offline" };
const followup = { machine_id: "server", session_id: follow };

const meta = (over) => ({
  title: "Design review", kind: "meeting", complete: false, started_at: "2026-11-10T14:00:00+08:00", duration: 1860, device: "phone", language: "en",
  revision: 5, minutes_state: "pending", attendance: "participant", updated: new Date().toISOString(), room: ready, ...over,
});
const detail = (over = {}) => ({
  kind: "recordings", id, path: `recordings/${id}`, meta: meta(over), summary: "",
  segments: [
    { start: 5, end: 9, text: "Ada: Today we look at the export flow of the example project." },
    { start: 1790, end: 1806, text: "Bo: Did we decide on the cap last time, or is it still open?" },
  ],
});
const ask = (over) => ({
  id: typedAsk, question: "What did we decide about the export cap last time?", question_source: "typed", at: 1834.2, state: "answered",
  answer: "At the design review on November 3 you kept the 4 MB cap for this release. Bo owns the regression test; it is still open.",
  lang: "en", speak: true, sources: [], asked_at: "2026-11-10T06:30:34Z", answered_at: "2026-11-10T06:30:40Z", via: "room", ...over,
});
const asks = () => [ask(), ask({ id: heardAsk, question: "", question_source: "heard", at: 1810, answer: "Bo owns it, and it is still open.", asked_at: "2026-11-10T06:30:10Z" })];
const live = (over = {}) => ({ next: 57, fed: 57, attendance: "participant", asks: asks(), room: ready, revs: { room: 2, asks: 4 }, ...over });

let seq = 0;
const at = (minute) => `2026-11-10T06:${String(minute).padStart(2, "0")}:00Z`;
const item = (minute, over) => ({ id: `i${++seq}#0`, seq, role: "user", kind: "text", ts: at(minute), ...over });
const source = '<persona-context kind="message-source" title="">\n{"kind":"meeting","title":"Design review"}\n</persona-context>\n\n';
const batch = (minute, lines) => item(minute, {
  source: { kind: "meeting", title: "Design review" },
  text: `${source}New lines from "Design review", a meeting you attend as a participant. Write nothing in reply.\n\n<persona-context kind="context" title="Design review · live" url="">\n${lines.join("\n")}\n</persona-context>`,
});
const roomItems = () => [
  item(0, { client_msg_id: `meeting-${id}`, text: `${source}The meeting is starting. Write nothing now.\n\n<persona-context kind="context" title="Design review" url="">\nTitle: Design review\nStarted: 2026-11-10 14:00 CST\nLanguage: en\nRole: participant: answers may be said aloud\n</persona-context>` }),
  batch(1, ["[0:05] Ada: Today we look at the export flow of the example project.", "[0:12] Bo: The cap is still 4 MB.", "[0:31] Ada: Let's keep it for this release."]),
  batch(29, ["[29:40] Bo: Did we decide on the cap last time, or is it still open?"]),
  item(30, { client_msg_id: `ask-${heardAsk}-1`, text: `${source}Someone in the room just asked a question.\n\n<persona-context kind="context" title="What was just said (30:10)" url="">\n[29:40] Bo: Did we decide on the cap last time, or is it still open?\n</persona-context>` }),
  item(30, { role: "assistant", text: "Bo owns it, and it is still open." }),
  item(30, { client_msg_id: `ask-${typedAsk}-1`, text: "What did we decide about the export cap last time?" }),
  item(30, { role: "assistant", text: ask().answer }),
];
const followItems = () => [
  item(40, { client_msg_id: "followup-1", text: '<persona-context kind="context" title="Meeting: Design review" url="">\nThis is the material of a meeting I recorded.\nMinutes:\n- Keep the 4 MB cap for this release.\n- Bo owns the regression test.\n</persona-context>\n\nDraft the email to the vendor about the cap.' }),
  item(41, { role: "assistant", text: "Here is a **draft**:\n\n> We are keeping the 4 MB export cap for this release and will revisit it next quarter." }),
];

const browser = await chromium.launch({ executablePath, headless: true, args: ["--no-sandbox", "--disable-dev-shm-usage"] });
const deadline = setTimeout(() => { console.error("FAIL recording conversation timed out"); process.exit(1); }, 240_000);
let screenshots = 0, runs = 0;
const fill = (text, values) => text.replace(/\{(\w+)\}/g, (_, k) => String(values[k]));

async function context(theme, lang, width, scene) {
  const ctx = await browser.newContext({ viewport: { width, height: 1400 }, colorScheme: theme, timezoneId: "Asia/Shanghai", deviceScaleFactor: 1, reducedMotion: "reduce" });
  await ctx.addInitScript(({ theme, lang }) => {
    localStorage.setItem("persona.token", "fixture-token");
    localStorage.setItem("persona.language", lang);
    localStorage.setItem("persona.theme", theme);
  }, { theme, lang });
  const state = { live: [], private: [], followup: [], items: [], made: false, asksRev: 0 };
  await ctx.route("**/*", (route) => new URL(route.request().url()).origin === base.origin ? route.continue() : route.abort());
  await ctx.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname.replace(/^\/api\/v1/, "");
    if (path === detailPath) return route.fulfill({ json: { detail: detail({ ...scene.meta, ...(state.made ? { followup } : {}) }) } });
    if (path === livePath) {
      assert.equal(request.method(), "GET");
      assert.equal(request.headers()["x-persona-manager"], "assistant");
      state.live.push(url.search);
      const revs = { ...(scene.live.revs ?? live().revs), ...(state.asksRev ? { asks: state.asksRev } : {}), ...(state.made ? { followup: 1 } : {}) };
      return route.fulfill({ json: live({ ...scene.live, ...(state.made ? { followup } : {}), revs }) });
    }
    if (path === `/recordings/${id}/private`) {
      assert.equal(request.method(), "PUT");
      const body = request.postDataJSON(); state.private.push(body);
      return route.fulfill({ json: { private: body.private } });
    }
    if (path === `/recordings/${id}/followup`) {
      assert.equal(request.method(), "POST");
      const body = request.postDataJSON(); state.followup.push(body);
      state.made = true;
      return route.fulfill({ json: { followup, created: true, client_msg_id: `followup-${body.id}` } });
    }
    if (path === `/sessions/server/${room}/items` || path === `/sessions/server/${follow}/items`) {
      assert.equal(request.method(), "GET");
      // The detail's own reads; the chat page the follow-up opens reads it its own way.
      if (url.searchParams.get("turns") === "400") state.items.push(path);
      return route.fulfill({ json: { items: path.includes(room) ? (scene.roomItems ?? roomItems)() : followItems(), has_more: false } });
    }
    assert.ok(["GET", "HEAD"].includes(request.method()), `Unexpected write ${request.method()} ${path}`);
    const assistant = { manager: "assistant", machine_id: "server", main: "main-1", conversations: [], meeting_attendance_default: "participant", notes_effective: "", main_tasks_quiet: true };
    const data = {
      "/kb/recordings": { items: [{ kind: "recordings", id, name: "Design review", relevance: 0 }], tags: [] },
      "/sessions": { sessions: [], total: 0, has_more: false, revision: "fixture" },
      "/machines": { machines: [MACHINE] }, "/interactions": { interactions: [] },
      "/assistant": assistant, "/assistant/tasks": { tasks: [] }, "/assistant/memory": { files: [] }, "/todos": { todos: [] },
      "/mutes": { spaces: [], sessions: [], allowed: [] }, "/pins": { spaces: [], sessions: [], sections: [] },
      "/agents/sessions": { sessions: [] }, "/agents/prefs": {}, "/skills": { skills: [] },
      "/managers": { managers: [{ id: "assistant", name: "Persona", builtin: true, online: true }], default: "assistant" },
      "/personas": { personas: [] }, "/assistant/personas": { personas: [] }, "/workspaces": { workspaces: [] }, "/extensions": { extensions: [] },
    };
    return route.fulfill({ json: data[path] ?? {} });
  });
  const sockets = [];
  await ctx.routeWebSocket(/\/api\/v1\/ws(?:\?|$)/, (ws) => {
    sockets.push(ws);
    ws.send(JSON.stringify({ type: "hello", server_version: "mock", protocol: 1, features: ["managers", "meeting_attendance", "meeting_conversation"], hub_name: "Example hub" }));
    ws.send(JSON.stringify({ type: "machines", machines: [MACHINE] }));
    ws.onMessage((message) => { const event = JSON.parse(String(message)); if (event.type === "app_ping") ws.send(JSON.stringify({ type: "pong", id: event.id })); });
  });
  return { ctx, state, push: (msg) => sockets.forEach((ws) => ws.send(JSON.stringify(msg))) };
}

async function settle(page) {
  await page.evaluate(() => document.fonts.ready);
  await page.waitForTimeout(350);
  assert.equal(await page.locator("vite-error-overlay").count(), 0);
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), "No page overflow");
}

async function until(check, what, ms = 6000) {
  const started = Date.now();
  while (!(await check())) {
    assert.ok(Date.now() - started < ms, `Timed out waiting for ${what}`);
    await new Promise((r) => setTimeout(r, 100));
  }
}

/** The private switch and the Conversation section from the top of the facts down, as one picture. */
async function shoot(page, name) {
  const section = page.locator("[data-meeting-conversation]");
  assert.ok(await section.evaluate((el) => el.scrollWidth <= el.clientWidth + 1), "No section overflow");
  await page.screenshot({ path: join(output, name), fullPage: true });
  screenshots++;
}

const kinds = (page) => page.locator("[data-meeting-conversation] li[data-thread]").evaluateAll((els) => els.map((el) => el.getAttribute("data-thread")));

async function running(theme, lang = "en", width = 1440) {
  const s = JSON.parse(await readFile(new URL(`../src/i18n/locales/${lang}/knowledge-kbdetail.json`, import.meta.url), "utf8"));
  const { ctx, state, push } = await context(theme, lang, width, { meta: {}, live: {} });
  const errors = [];
  const page = await ctx.newPage();
  page.setDefaultTimeout(15_000);
  page.on("pageerror", (err) => errors.push(err.message));
  await page.goto(new URL(detailPath, base).href);
  const section = page.locator("[data-meeting-conversation]");
  await section.getByText(ask().answer, { exact: true }).waitFor();
  await settle(page);
  assert.deepEqual(await kinds(page), ["joined", "lines", "lines", "heard", "answer", "question", "answer"]);
  assert.equal(await section.getByRole("status").innerText(), s.room_ready_participant);
  assert.ok(await section.getByText(fill(s.thread_lines_other, { count: 3 }), { exact: true }).isVisible());
  assert.ok(await section.getByText(fill(s.thread_heard, { clock: "30:10" }), { exact: true }).isVisible());
  assert.ok(await section.getByText(s.conversation_read_only, { exact: true }).isVisible());
  assert.equal(await section.locator("textarea").count(), 0);
  assert.equal(await page.locator("[data-recording-asks]").count(), 0, "A room meeting's asks are in its thread");
  assert.equal(await page.getByText(s.recording_attending, { exact: true }).count(), 0, "No attending session row");
  // The private switch: no mark yet reads as private.
  const box = page.locator("[data-meeting-private]");
  const toggle = box.getByRole("switch");
  assert.equal(await toggle.getAttribute("aria-checked"), "true");
  assert.ok(await box.getByText(s.private_unset, { exact: true }).isVisible());
  // The first batch opens onto its lines.
  await section.locator('li[data-thread="lines"] button').first().click();
  await section.getByText("Bo: The cap is still 4 MB.", { exact: true }).waitFor();
  await settle(page);
  await shoot(page, width === 1440 ? `conversation-running-${theme}.png` : `conversation-running-${lang}-${width}.png`);
  if (runs === 0) {
    await toggle.click();
    await until(async () => state.private.length === 1, "the private mark's PUT");
    assert.deepEqual(state.private, [{ private: false }]);
    await box.getByText(s.private_off, { exact: true }).waitFor();
    assert.equal(await toggle.getAttribute("aria-checked"), "false");
    // A meeting_live event reads only the newer parts, then the room's thread again.
    const reads = state.live.length, threads = state.items.length;
    state.asksRev = 5;
    push({ type: "meeting_live", recording_id: id, changed: ["room", "asks"], revs: { room: 2, asks: 5 } });
    await until(async () => state.live.length > reads && state.items.length > threads, "the newer part and the thread");
    assert.equal(state.live[reads], "?parts=asks");
  }
  assert.deepEqual(errors, []);
  await ctx.close();
  runs++;
}

async function after(theme, lang = "en", width = 1440) {
  const s = JSON.parse(await readFile(new URL(`../src/i18n/locales/${lang}/knowledge-kbdetail.json`, import.meta.url), "utf8"));
  const scene = { meta: { complete: true, ended_at: "2026-11-10T14:31:00+08:00", minutes_state: "ready", room: ended, private: false }, live: { room: ended, private: false, revs: { room: 3, asks: 4, private: 1 } } };
  const { ctx, state } = await context(theme, lang, width, scene);
  const errors = [];
  const page = await ctx.newPage();
  page.setDefaultTimeout(15_000);
  page.on("pageerror", (err) => errors.push(err.message));
  await page.goto(new URL(detailPath, base).href);
  const section = page.locator("[data-meeting-conversation]");
  const field = section.locator("textarea");
  await field.waitFor();
  await section.getByText(ask().answer, { exact: true }).waitFor();
  await settle(page);
  assert.equal(await section.getByRole("status").innerText(), s.room_ended);
  assert.ok(await page.locator("[data-meeting-private]").getByText(s.private_off, { exact: true }).isVisible());
  await field.fill("Draft the email to the vendor about the cap.");
  await shoot(page, width === 1440 ? `conversation-composer-${theme}.png` : `conversation-composer-${lang}-${width}.png`);
  await section.getByRole("button", { name: s.followup_send, exact: true }).click();
  await page.waitForURL((u) => u.pathname === `/persona/${follow}`);
  assert.equal(state.followup.length, 1);
  assert.match(state.followup[0].id, /^[0-9a-f-]{36}$/);
  assert.equal(state.followup[0].text, "Draft the email to the vendor about the cap.");
  // Back on the recording, the thread goes on after the divider. (The chat page in between is fed nothing real.)
  assert.deepEqual(errors, []);
  await page.goto(new URL(detailPath, base).href);
  errors.length = 0;
  await section.getByRole("separator").waitFor();
  await section.getByText("draft", { exact: true }).waitFor();
  await settle(page);
  assert.deepEqual(await kinds(page), ["joined", "lines", "lines", "heard", "answer", "question", "answer", "material", "question", "answer"]);
  const seed = section.locator('li[data-thread="material"] button');
  assert.equal(await seed.innerText(), s.thread_material);
  assert.equal(await seed.getAttribute("aria-expanded"), "false");
  assert.equal(await section.getByRole("link", { name: s.conversation_continue, exact: true }).getAttribute("href"), `/persona/${follow}`);
  assert.equal(await field.count(), 0);
  await section.evaluate((el) => el.scrollIntoView({ block: "end" }));
  await shoot(page, width === 1440 ? `conversation-followup-${theme}.png` : `conversation-followup-${lang}-${width}.png`);
  assert.deepEqual(errors, []);
  await ctx.close();
  runs++;
}

async function down(theme, lang = "en", width = 1440) {
  const s = JSON.parse(await readFile(new URL(`../src/i18n/locales/${lang}/knowledge-kbdetail.json`, import.meta.url), "utf8"));
  const scene = { meta: { room: unavailable }, live: { room: unavailable, asks: [ask({ via: "material" })] } };
  const { ctx, state } = await context(theme, lang, width, scene);
  const errors = [];
  const page = await ctx.newPage();
  page.setDefaultTimeout(15_000);
  page.on("pageerror", (err) => errors.push(err.message));
  await page.goto(new URL(detailPath, base).href);
  const section = page.locator("[data-meeting-conversation]");
  await section.locator("li[data-ask]").first().waitFor();
  await settle(page);
  assert.equal(await section.getByRole("status").innerText(), s.room_problem_machine_offline);
  assert.equal(await section.locator("li[data-ask]").count(), 1);
  assert.deepEqual(state.items, [], "A room that never came up has no thread to read");
  await shoot(page, width === 1440 ? `conversation-unavailable-${theme}.png` : `conversation-unavailable-${lang}-${width}.png`);
  assert.deepEqual(errors, []);
  await ctx.close();
  runs++;
}

/** Questions in the room that got no answer: one in the thread says why, one still being answered says so, and one that
 *  never went into the room (spoken, failed) shows after the thread. */
async function stuck(theme, lang = "en", width = 1440) {
  const s = JSON.parse(await readFile(new URL(`../src/i18n/locales/${lang}/knowledge-kbdetail.json`, import.meta.url), "utf8"));
  const spokenAsk = "33333333-3333-4333-8333-333333333333";
  const waitingAsk = "44444444-4444-4444-8444-444444444444";
  const stuckAsks = [
    ask({ id: heardAsk, question: "", question_source: "heard", at: 1810, answer: "Bo owns it, and it is still open.", asked_at: "2026-11-10T06:30:10Z" }),
    ask({ state: "failed", error: "no_answer", answer: "", answered_at: undefined }),
    ask({ id: waitingAsk, question: "And when is the next review?", state: "pending", answer: "", answered_at: undefined, asked_at: "2026-11-10T06:31:00Z" }),
    ask({ id: spokenAsk, question: "Who presents next week?", question_source: "spoken", state: "failed", error: "model_error", answer: "", answered_at: undefined, asked_at: "2026-11-10T06:31:20Z" }),
  ];
  const items = () => [...roomItems().slice(0, -1), item(31, { client_msg_id: `ask-${waitingAsk}-1`, text: "And when is the next review?" })];
  const { ctx } = await context(theme, lang, width, { meta: {}, live: { asks: stuckAsks }, roomItems: items });
  const errors = [];
  const page = await ctx.newPage();
  page.setDefaultTimeout(15_000);
  page.on("pageerror", (err) => errors.push(err.message));
  await page.goto(new URL(detailPath, base).href);
  const section = page.locator("[data-meeting-conversation]");
  await section.locator(`li[data-ask="${spokenAsk}"]`).waitFor();
  await settle(page);
  assert.deepEqual(await kinds(page), ["joined", "lines", "lines", "heard", "answer", "question", "question"]);
  const questions = section.locator('li[data-thread="question"]');
  assert.ok(await questions.nth(0).getByText(s.ask_failed_no_answer, { exact: true }).isVisible());
  assert.ok(await questions.nth(1).getByText(s.ask_pending, { exact: true }).isVisible());
  assert.equal(await section.locator("li[data-ask]").count(), 1, "Only the ask its thread does not hold");
  const row = section.locator(`li[data-ask="${spokenAsk}"]`);
  assert.equal(await row.locator(`[title="${s.ask_spoken}"]`).count(), 1);
  assert.ok(await row.getByText(s.ask_failed_model_error, { exact: true }).isVisible());
  await shoot(page, width === 1440 ? `conversation-stuck-${theme}.png` : `conversation-stuck-${lang}-${width}.png`);
  assert.deepEqual(errors, []);
  await ctx.close();
  runs++;
}

try {
  for (const theme of ["light", "dark"]) await running(theme);
  for (const theme of ["light", "dark"]) await stuck(theme);
  await stuck("dark", "ja", 390);
  for (const lang of ["en", "zh-Hans", "ja"]) await running("dark", lang, 390);
  for (const theme of ["light", "dark"]) await after(theme);
  for (const lang of ["zh-Hans", "ja"]) await after("light", lang, 390);
  for (const theme of ["light", "dark"]) await down(theme);
  console.log(`PASS recording conversation: ${runs} browser runs, ${screenshots} screenshots; private switch, room thread, questions without an answer, meeting_live parts, follow-up composer, follow-up thread, unavailable room and overflow checked`);
} catch (err) {
  console.error(`FAIL recording conversation: ${err?.stack || err}`);
  process.exitCode = 1;
} finally {
  clearTimeout(deadline);
  await browser.close();
}
