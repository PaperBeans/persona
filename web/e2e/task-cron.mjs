// End-to-end check of a cron scheduled task on the web (#4399, docs/assistant/assistant.md "Scheduled tasks"): the list
// shows its expression, the hub's words for it and its next run; the editor offers "Cron" when the hub has task_cron,
// checks the expression with the hub as it is typed (its words and next runs, or what is wrong, with Save held back),
// fills in an example, and saves the expression.
//
// Needs the web page against the fake hub with MOCK_CRON=1, and Chrome (playwright-core drives it):
//
//   MOCK_CRON=1 PORT=18799 python3 scripts/mock/mock_hub.py &
//   (cd web && PERSONA_HUB_URL=http://127.0.0.1:18799 npx vite --port 5399 --strictPort &)
//   node web/e2e/task-cron.mjs http://127.0.0.1:5399 --hub http://127.0.0.1:18799 [--shots <dir>]
//
// Runs wide (1280) and phone width (390), light and dark, in Chinese; --shots saves the list, the editor, a refused
// expression and an example picked. Prints one line per check and a verdict; exits 1 on a failure.
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const flag = (name) => {
  const i = args.indexOf(name);
  return i >= 0 ? args[i + 1] : undefined;
};
const base = (args[0] ?? "").replace(/\/$/, "");
const hub = (flag("--hub") ?? "").replace(/\/$/, "");
const shots = flag("--shots");
if (!base || !hub) {
  console.error("usage: node web/e2e/task-cron.mjs <web URL> --hub <fake hub URL> [--shots <dir>]");
  process.exit(2);
}
if (shots) mkdirSync(shots, { recursive: true });

const TASK = "工作时间巡检"; // the fake hub's cron task (rt_cron)
const SIZES = { wide: { width: 1280, height: 900 }, narrow: { width: 390, height: 844 } };

const ok = (msg) => console.log(`ok  ${msg}`);
function fail(msg) {
  console.log(`FAIL ${msg}`);
  process.exit(1);
}

async function hubTask(id) {
  const r = await fetch(`${hub}/api/v1/assistant/tasks`, { headers: { Authorization: "Bearer mock" }, signal: AbortSignal.timeout(5000) });
  return (await r.json()).tasks.find((t) => t.id === id);
}

async function shot(page, name) {
  if (!shots) return;
  await page.waitForTimeout(400); // the preview's fade and the dialog settle first
  await page.screenshot({ path: join(shots, `web-task-cron-${name}.png`) });
}

async function visible(locator, what) {
  try {
    await locator.first().waitFor({ state: "visible", timeout: 8000 });
  } catch {
    fail(`${what} (not visible)`);
  }
}

async function run(browser, size, theme) {
  const tag = `${size}/${theme}`;
  await fetch(`${hub}/mock/schedule/reset`, { signal: AbortSignal.timeout(5000) });
  const ctx = await browser.newContext({ viewport: SIZES[size], deviceScaleFactor: 2, colorScheme: theme, locale: "zh-CN", hasTouch: size === "narrow" });
  const page = await ctx.newPage();
  await page.goto(`${base}/overview?token=mock`);
  await page.waitForFunction(() => localStorage.getItem("persona.token"));
  await page.evaluate((t) => {
    localStorage.setItem("persona.theme", t);
    localStorage.setItem("persona.language", "zh-Hans");
  }, theme);
  await page.goto(`${base}/routines`);

  // The list: expression first (a one-line row always shows it), then the hub's words, then the next run.
  const row = page.locator('[role="button"]', { hasText: TASK }).filter({ hasText: "*/30 9-18 * * 1-5 · 每隔 30 分钟" });
  await visible(row, `${tag}: the row with its expression and words`);
  await visible(row.filter({ hasText: "下次" }), `${tag}: the row's next run`);
  await row.first().scrollIntoViewIfNeeded();
  await shot(page, `${size}-${theme}-list`);
  ok(`${tag}: the list shows the expression, its words and the next run`);

  // The editor: Cron picked, the expression, the hub's words and five next runs in the user's time zone.
  await page.getByText(TASK, { exact: true }).first().click();
  const dialog = page.getByRole("dialog");
  const cron = dialog.getByRole("radio", { name: "Cron" });
  await visible(cron, `${tag}: the Cron rule`);
  if ((await cron.getAttribute("aria-checked")) !== "true") fail(`${tag}: Cron is not the picked rule`);
  const field = dialog.getByLabel("Cron 表达式");
  if ((await field.inputValue()) !== "*/30 9-18 * * 1-5") fail(`${tag}: the field has ${await field.inputValue()}`);
  await visible(dialog.getByText("每隔 30 分钟, 在 09:00 和 18:59 之间, 星期一至星期五"), `${tag}: the hub's words`);
  await visible(dialog.getByText("接下来的运行时间（Asia/Taipei）"), `${tag}: the next runs' heading`);
  const runs = dialog.locator("ul li");
  await runs.nth(4).waitFor({ state: "visible", timeout: 8000 }).catch(() => fail(`${tag}: five next runs`));
  await field.scrollIntoViewIfNeeded();
  await shot(page, `${size}-${theme}-editor`);
  ok(`${tag}: the editor shows Cron, the expression, its words and five next runs`);

  // A bad expression: the hub's reason under the field, Save held back.
  await field.fill("0 9 * *");
  await visible(dialog.getByText("cron 表达式是 5 段").first(), `${tag}: why "0 9 * *" can't be used`);
  const save = dialog.getByRole("button", { name: "保存" });
  if (await save.isEnabled()) fail(`${tag}: Save is enabled for a refused expression`);
  await shot(page, `${size}-${theme}-invalid`);
  ok(`${tag}: a refused expression says why and can't be saved`);

  // An example fills the field; saving sends it.
  await dialog.getByRole("button", { name: "每月 1 号 10:00" }).click();
  if ((await field.inputValue()) !== "0 10 1 * *") fail(`${tag}: the example filled ${await field.inputValue()}`);
  await visible(dialog.getByText("在10:00, 限每月 1 号"), `${tag}: the example's words`);
  await shot(page, `${size}-${theme}-example`);
  await save.click();
  await page.waitForTimeout(800);
  const saved = await hubTask("rt_cron");
  if (saved?.repeat !== "cron" || saved?.cron !== "0 10 1 * *") fail(`${tag}: saved ${JSON.stringify(saved && { repeat: saved.repeat, cron: saved.cron })}`);
  ok(`${tag}: an example fills the field and Save sends the expression`);
  await ctx.close();
}

const browser = await chromium.launch({ channel: "chrome" });
try {
  for (const size of Object.keys(SIZES)) {
    for (const theme of ["light", "dark"]) await run(browser, size, theme);
  }
} finally {
  await browser.close();
}
console.log("PASS");
