// A task's brief (#3822): on the plan card before Start, on the task's page above its criteria with the scope changes
// and who worked on it under them, and under a finished task's criteria the dropped ones and what else was done.
// The real web app with synthetic HTTP/WS only; it never connects to a user's hub or conversation.
// node web/e2e/task-brief.mjs <web URL> --shots <directory> [--chromium <executable>]
import assert from "node:assert/strict";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const flag = (name) => { const i = args.indexOf(name); return i < 0 ? undefined : args[i + 1]; };
const base = args[0]?.replace(/\/$/, "");
const shots = flag("--shots");
if (!base || !shots) {
  console.error("usage: node web/e2e/task-brief.mjs <web URL> --shots <directory> [--chromium <executable>]");
  process.exit(2);
}
const origin = new URL(base);
assert(["127.0.0.1", "localhost", "[::1]"].includes(origin.hostname) && origin.protocol === "http:", "only an owned loopback HTTP web server is allowed");
assert(!origin.username && !origin.password && !origin.search && !origin.hash, "the web URL must not contain credentials");
mkdirSync(shots, { recursive: true });
const watchdog = setTimeout(() => { console.error("TIMEOUT task-brief browser verification (240s)"); process.exit(124); }, 240_000);

const sid = "task-brief-demo";
const day = (n, hour = 9) => new Date(Date.UTC(2026, 10, n, hour, 30)).toISOString();
const AT = day(10);
const words = {
  en: { summary: "2 criteria · about 45 min · 2 out of bounds", start: "Start 2 tasks", asked: "You asked on", started: "Started by the assistant on", reconstructed: "Reconstructed: the original request was not recorded", handover: "Hand-over prompt", overdue: "Overdue", showAll: "Show all", lineage: "Who worked on it", scope: "Scope changes", also: "Also done", states: ["Met", "Not met", "Not checked", "Dropped"] },
  "zh-Hans": { summary: "2 条验收标准 · 约 45 分钟 · 2 条不能做", start: "开始 2 个任务", asked: "你在", started: "由助理发起", reconstructed: "事后补建：当初的原话没有记录", handover: "交接提示词", overdue: "已超时", showAll: "展开全部", lineage: "谁做过", scope: "范围变更", also: "另外做了", states: ["已达成", "未达成", "未核对", "已去掉"] },
  ja: { summary: "完了条件 2 件 · 約 45 分 · 禁止事項 2 件", start: "2件を開始", asked: "のあなたの依頼", started: "にアシスタントが開始", reconstructed: "後から再構成：元の依頼は記録されていません", handover: "引き継ぎ時のプロンプト", overdue: "期限超過", showAll: "すべて表示", lineage: "担当の履歴", scope: "範囲の変更", also: "ほかに実施", states: ["達成", "未達成", "未確認", "対象外に変更"] },
};

const request = (at, text) => ({ text, at, conversation: sid, message_id: "demo-message" });
const brief = (id, at, goal, doneWhen, expect, more = {}) => ({ id, created_at: at, started_by: "owner", goal, done_when: doneWhen, criteria: doneWhen, expect_minutes: expect, expect_minutes_now: expect, log: [], clock_at: at, due_at: day(12), ...more });

const planBrief = brief("brief-plan", AT, "Every review comment on the example change is answered or fixed", ["Each review comment has a reply or a commit", "The checks on the example change pass"], 45, {
  request: request(AT, "The reviewers left a pile of comments on the example change yesterday. Go through all of them: fix what is clearly right, and for anything you disagree with write a short reply saying why instead of changing the code. Do not merge it, I want to read the replies first. If a comment needs a bigger refactor, leave it and list it for me at the end, with how long you think each would take."),
  out_of_bounds: ["Do not merge the change", "No refactors beyond what a comment asks for"],
});
const ownBrief = brief("brief-own", AT, "Remove the logs the example deploy left behind", ["The old logs are deleted"], 150, { started_by: "assistant", trigger: "supervision", reason: "Left over from the example deploy" });
const widgetCriteria = ["The fix is merged", "A snapshot test covers landscape"];
const widgetBrief = brief("brief-widget", day(7), "The example widget no longer crashes in landscape", ["The fix is merged", "Landscape screenshots are in the change"], 60, {
  request: request(day(7), "The example widget crashes when I rotate the phone. Fix it and add a test so it stays fixed."),
  out_of_bounds: ["Do not change the widget's layout"],
  criteria: widgetCriteria,
  expect_minutes_now: 90,
  due_at: day(9),
  overdue_at: day(9, 12),
  log: [
    { at: day(8), kind: "scope", change: "A snapshot test replaces the screenshots", why: "A test keeps it fixed; screenshots go stale", done_when: widgetCriteria, expect_minutes: 90, added: ["A snapshot test covers landscape"], dropped: ["Landscape screenshots are in the change"] },
    { at: day(9, 12), kind: "overdue" },
  ],
});
const lineage = [
  { todo_id: "demo-widget-1", title: "Fix the example widget's landscape crash", agent: "claude", machine: "demo", started: day(7), ended: day(8), why: "Its computer went offline", superseded_by: "demo-widget-2" },
  { todo_id: "demo-widget-2", title: "Take over the example widget fix", agent: "codex", machine: "demo", started: day(8), ended: day(9), why: "The model service kept failing", continues: "demo-widget-1", superseded_by: "demo-widget" },
  { todo_id: "demo-widget", title: "Retry the example widget fix", agent: "claude", machine: "demo", started: day(9), continues: "demo-widget-2", current: true },
];
const releaseCriteria = ["Every change merged this week is listed", "Grouped as features, fixes and other"];
const result = { at: day(10, 10), todo_id: "demo-release", verdict: "accepted", also_done: ["Fixed two typos in last week's notes"], criteria: [
  { criterion: releaseCriteria[0], state: "met", evidence: "7 listed, the same as the repository shows" },
  { criterion: releaseCriteria[1], state: "not_met", evidence: "Two fixes are under features" },
  { criterion: "The notes name who to thank", state: "unchecked" },
  { criterion: "Each entry links its issue", state: "dropped", change: "Links are added by the release script", changed_at: day(10, 9) },
] };

function todos() {
  const task = (id, title, more) => ({ id, title, origin: sid, created_at: day(9), updated_at: AT, agents: [{ machine_id: "demo", session_id: `${id}-worker`, linked_at: day(9), agent: "claude", title }], ...more });
  return [
    task("demo-widget", "Retry the example widget fix", { brief: widgetBrief, lineage, supervision: { supervisor: sid, done_when: widgetCriteria, expect_minutes: 90, state: "", reviews: [] } }),
    task("demo-blog", "Move the example blog", { notes: "You are taking over a session that stopped half way.",
      brief: brief("brief-blog", day(6), "Move the example blog", [], 0, { started_by: "unknown", reconstructed: true, handover: "You are taking over a session that stopped half way: the posts are converted, the theme is not. Carry on from the branch it left, and keep the old addresses working." }),
      lineage: [{ todo_id: "demo-blog", title: "Move the example blog", agent: "claude", machine: "demo", started: day(6), current: true }] }),
    task("demo-release", "Write the example release notes", { done_at: day(10, 10), done_by: "review", done_reason: "The notes are written",
      brief: brief("brief-release", day(10, 8), "This week's example release notes are ready to send", [...releaseCriteria, "The notes name who to thank", "Each entry links its issue"], 20, {
        request: request(day(10, 8), "Write up this week's release notes from what was merged."), criteria: [...releaseCriteria, "The notes name who to thank"], result,
        log: [{ at: day(10, 9), kind: "scope", change: "Links are added by the release script", why: "The script already adds them when it publishes", dropped: ["Each entry links its issue"] }] }),
      supervision: { supervisor: sid, done_when: [...releaseCriteria, "The notes name who to thank"], expect_minutes: 20, state: "accepted", reviews: [{ at: day(10, 10), verdict: "accepted", note: "Written, with one gap", checks: [{ criterion: releaseCriteria[0], met: true }, { criterion: releaseCriteria[1], met: false }] }],
        criteria: [
          { criterion: releaseCriteria[0], state: "met", evidence: "7 listed, the same as the repository shows", at: day(10, 10) },
          { criterion: releaseCriteria[1], state: "not_met", evidence: "Two fixes are under features", at: day(10, 10) },
          { criterion: "The notes name who to thank", state: "pending" },
        ] } }),
  ];
}

const plan = { id: "demo-plan", conversation: sid, state: "open", created_at: AT, tasks: [
  { title: "Answer the example change's review", prompt: "Demo", machine: "demo", cwd: "/demo/example", permission: "acceptEdits", why: "The example repository is on this computer.", brief: planBrief },
  { title: "Remove the logs the example deploy left behind", prompt: "Demo", machine: "demo", cwd: "/demo/example", readonly: true, brief: ownBrief },
] };
const feed = [{ id: "demo-plan-item", conversation: sid, at: AT, kind: "plan", ref: plan.id }];

async function open(browser, size, theme, language) {
  const context = await browser.newContext({ viewport: size === "wide" ? { width: 1280, height: 900 } : { width: 390, height: 844 }, colorScheme: theme, reducedMotion: "reduce" });
  await context.addInitScript(({ theme, language }) => {
    localStorage.setItem("persona.token", "synthetic-demo-token");
    localStorage.setItem("persona.language", language);
    localStorage.setItem("persona.theme", theme);
  }, { theme, language });
  const session = { machine_id: "demo", session_id: sid, title: "Example conversation", assistant: true, state: "idle", status: "idle", updated_at: AT };
  const list = todos();
  const agents = list.map((t) => ({ machine_id: "demo", session_id: `${t.id}-worker`, title: `${t.title} Agent`, state: "idle", status: "idle", updated_at: AT, state_since: AT }));
  const writes = [];
  await context.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "");
    // the page asks for its Today card on its own; nothing else is written (checked at the end)
    if (request.method() !== "GET" && path !== `/assistant/conversations/${sid}/today`) writes.push(`${request.method()} ${path}`);
    let body = {};
    if (path === "/machines") body = { machines: [{ id: "demo", name: "Example computer", online: true, os: "linux" }] };
    else if (path === "/sessions") body = { sessions: [session, ...agents] };
    else if (path === "/interactions") body = { interactions: [] };
    else if (path === "/assistant") body = { machine_id: "demo", main: sid, conversations: [session], tasks: [], memory: [] };
    else if (path === "/todos") body = { todos: list };
    else if (path === `/sessions/demo/${sid}`) body = { session };
    else if (path.endsWith("/feed")) body = { items: feed, plans: { [plan.id]: plan } };
    else if (path.endsWith("/items")) body = { items: [], has_more: false };
    else if (path.endsWith("/decisions")) body = { decisions: [] };
    else if (path === "/extensions/ui") body = { slots: [] };
    else if (path === "/extensions") body = { extensions: [] };
    else if (path === "/managers") body = { managers: [] };
    await route.fulfill({ contentType: "application/json", body: JSON.stringify(body) });
  });
  await context.routeWebSocket("**/api/v1/ws**", (ws) => {
    ws.send(JSON.stringify({ type: "hello", server_version: "synthetic", hub_name: "Example hub", features: [] }));
    ws.onMessage((raw) => {
      const message = JSON.parse(String(raw));
      if (message.type === "app_ping") ws.send(JSON.stringify({ type: "pong" }));
    });
  });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(String(error)));
  page.setDefaultTimeout(12_000);
  return { context, page, errors, writes };
}

const noOverflow = async (page, what) => assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth), false, `${what}: no horizontal page overflow`);
/** No element's text is clipped sideways inside the brief boxes (a long word wraps, a button stays one line). */
const oneLine = async (locator, what) => {
  for (const g of await locator.evaluateAll((els) => els.map((el) => ({ h: el.clientHeight, s: el.scrollHeight, text: el.textContent })))) assert(g.s <= g.h + 1, `${what}: "${g.text}" wraps (${g.s} > ${g.h})`);
};

async function run(browser, size, theme, language) {
  const { context, page, errors, writes } = await open(browser, size, theme, language);
  const w = words[language];
  const name = (step) => join(shots, `brief-${size}-${theme}-${language}-${step}.png`);
  try {
    // 1. the plan card: each task's brief before Start
    await page.goto(`${base}/`);
    await page.locator('[data-tray-entry="needs"]').click(); // an open plan waits in the Needs you tray
    const briefs = page.locator("[data-plan-brief]");
    await briefs.first().waitFor();
    const card = briefs.first().locator("xpath=ancestor::div[contains(@class,'shadow-card')][1]");
    assert.equal(await card.locator("[data-plan-brief]").count(), 2, "each task of the plan shows its brief");
    const mine = card.locator("[data-plan-brief]").nth(0);
    const own = card.locator("[data-plan-brief]").nth(1);
    assert(new RegExp(w.asked).test(await mine.locator('[data-brief-asked="owner"]').innerText()), "the owner's words are introduced as theirs, with the date");
    assert(new RegExp(w.started).test(await own.locator('[data-brief-asked="assistant"]').innerText()), "work the owner did not ask for says who started it");
    assert((await own.innerText()).includes("Left over from the example deploy"), "and why");
    assert.equal(await own.locator('[data-brief-asked="owner"]').count(), 0);
    // folded: the words in two lines and one line that counts the rest, behind one control
    assert.equal((await mine.locator("[data-brief-summary]").innerText()).trim(), w.summary, "the folded brief counts the criteria, the time and what is out of bounds");
    assert(!(await mine.innerText()).includes("Each review comment has a reply or a commit"), "the criteria are folded");
    assert.equal(await mine.locator("button").count(), 1, "one control per brief");
    const toggle = mine.getByRole("button", { name: w.showAll });
    await toggle.waitFor();
    const clamp = mine.locator("[data-brief-clamp]").first();
    const cut = await clamp.evaluate((el) => el.clientHeight);
    await oneLine(card.locator("button"), "plan card buttons");
    await noOverflow(page, "plan card");
    // with both briefs folded, Start is in reach: inside what the tray shows, without scrolling it
    const tray = page.locator("section.assistant-action-tray");
    const start = card.locator("button").filter({ hasText: w.start }).first();
    const reach = async () => {
      const [t, head, b] = await Promise.all([tray.boundingBox(), tray.locator("> div").first().boundingBox(), start.boundingBox()]);
      return { top: Math.round(t.y + head.height), bottom: Math.round(t.y + t.height), startTop: Math.round(b.y), startBottom: Math.round(b.y + b.height), scrolled: await tray.evaluate((el) => el.scrollTop) };
    };
    const folded = await reach();
    assert.equal(folded.scrolled, 0, "the tray was not scrolled");
    assert(folded.startTop >= folded.top && folded.startBottom <= folded.bottom, `Start is visible in the tray with the briefs folded: ${JSON.stringify(folded)}`);
    // what the briefs add to the card, against what the tray shows (the card's buttons stay at the tray's edge when it is taller)
    const heights = { card: Math.round((await card.boundingBox()).height), briefs: await card.locator("[data-plan-brief]").evaluateAll((els) => els.map((el) => Math.round(el.getBoundingClientRect().height))) };
    await page.waitForTimeout(300);
    await page.screenshot({ path: name("plan") });
    // the one control opens the whole request and the rest in place
    await toggle.click();
    assert.equal(await clamp.getAttribute("data-brief-clamp"), "open");
    assert((await clamp.evaluate((el) => el.clientHeight)) > cut, "Show all opens the whole request in place");
    assert((await clamp.innerText()).includes("with how long you think each would take"), "the end of the request is readable");
    assert((await mine.innerText()).includes("Each review comment has a reply or a commit"), "the criteria are on the card");
    assert((await mine.innerText()).includes("Do not merge the change"), "what is out of bounds is on the card");
    assert.equal(await mine.locator("button").count(), 1, "still one control");
    const opened = await reach();
    assert(opened.startTop >= opened.top && opened.startBottom <= opened.bottom, `Start stays visible with a brief open: ${JSON.stringify(opened)}`);
    await page.screenshot({ path: name("plan-open") });

    // 2. the task's page: the brief above the criteria, the scope changes and the lineage under them, the overdue mark
    await page.goto(`${base}/todos?id=demo-widget`);
    const panel = page.locator('[data-testid="task-brief"]');
    await panel.waitFor();
    assert(new RegExp(w.asked).test(await panel.innerText()));
    assert((await panel.innerText()).includes("The example widget crashes when I rotate the phone"), "taken over twice, it still shows the owner's own words");
    assert.equal((await panel.locator('[data-testid="brief-overdue"]').innerText()).trim(), w.overdue);
    const checklist = page.locator('[data-testid="review-checklist"]');
    const history = page.locator('[data-testid="task-brief-history"]');
    await history.waitFor();
    const tops = await Promise.all([panel, checklist, history].map((l) => l.evaluate((el) => el.getBoundingClientRect().top)));
    assert(tops[0] < tops[1] && tops[1] < tops[2], `the brief is above the criteria, the history under them: ${tops}`);
    assert((await history.innerText()).includes(w.scope) && (await history.innerText()).includes(w.lineage));
    assert.equal(await history.locator('[data-testid="brief-lineage"] li').count(), 3);
    assert.equal(await history.locator("li[data-lineage-current]").count(), 1);
    for (const why of ["Its computer went offline", "The model service kept failing"]) assert((await history.innerText()).includes(why), `the lineage says why a step stopped: ${why}`);
    assert((await history.innerText()).includes("A snapshot test replaces the screenshots"));
    await noOverflow(page, "task page");
    await history.scrollIntoViewIfNeeded();
    await page.waitForTimeout(300);
    await page.screenshot({ path: name("task"), fullPage: true });

    // 3. a brief made later says so, and shows the hand-over prompt as that
    await page.goto(`${base}/todos?id=demo-blog`);
    const made = page.locator('[data-testid="task-brief"]');
    await made.waitFor();
    assert((await made.innerText()).includes(w.reconstructed));
    assert((await made.innerText()).includes(w.handover));
    assert.equal(await made.locator('[data-brief-asked="owner"]').count(), 0, "no words are put in the owner's mouth");
    assert.equal(await page.locator('[data-testid="task-brief-history"]').count(), 0, "nothing to tell: one to-do, no scope change");
    await made.scrollIntoViewIfNeeded();
    await page.screenshot({ path: name("reconstructed") });

    // 4. finished work: one list of criteria (the review box's own), then only what it cannot say: the criterion a
    // scope change dropped, with the change, and what else was done
    await page.goto(`${base}/todos?id=demo-release`);
    const box = page.locator('[data-testid="review-checklist"]');
    await box.locator('[data-testid="brief-result"]').waitFor();
    const text = await box.innerText();
    for (const criterion of [...releaseCriteria, "The notes name who to thank", "Each entry links its issue"]) assert.equal(text.split(criterion).length - 1, 1, `"${criterion}" is listed once`);
    assert.deepEqual(await box.locator("[data-criterion-state]").evaluateAll((els) => els.map((el) => el.getAttribute("data-criterion-state"))), ["dropped"], "the result adds only the dropped criterion");
    assert(text.includes(w.states[3]) && text.includes("Links are added by the release script"), "a dropped criterion says so and names the scope change");
    assert(text.includes("Two fixes are under features"), "the list's own evidence is there");
    assert(text.includes(w.also) && text.includes("Fixed two typos"));
    await noOverflow(page, "finished task");
    await box.scrollIntoViewIfNeeded();
    await page.waitForTimeout(300);
    await page.screenshot({ path: name("result"), fullPage: true });
    assert.deepEqual(errors, []);
    assert.deepEqual(writes, [], "showing a brief writes nothing");
    console.log(`PASS ${size} ${theme} ${language}: plan briefs=2, Start visible folded and open (tray ${folded.top}-${folded.bottom}, Start ${folded.startTop}-${folded.startBottom}; card ${heights.card}px, folded briefs ${heights.briefs.join("+")}px), result extras: dropped=1, lineage steps=3`);
  } catch (error) {
    await page.screenshot({ path: name("failed") }).catch(() => {});
    throw error;
  } finally {
    await context.close();
  }
}

let browser;
try {
  browser = await chromium.launch({ executablePath: flag("--chromium") || "/usr/bin/google-chrome", headless: true });
  for (const theme of ["light", "dark"]) {
    await run(browser, "wide", theme, "en");
    await run(browser, "narrow", theme, "en");
  }
  for (const language of ["zh-Hans", "ja"]) await run(browser, "narrow", "light", language);
  console.log("PASS task-brief browser verification");
} catch (error) {
  console.error("FAIL task-brief browser verification", error);
  process.exitCode = 1;
} finally {
  clearTimeout(watchdog);
  await browser?.close();
}
