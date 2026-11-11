// End-to-end check of the Agent header's permission mode (#3956, iOS #3719, docs/web/web.md "Header permission mode"): every
// Claude agent's header ends with an icon-only mode button in its tone (Ask First gray, Plan Only teal, Auto-Edit and Auto
// purple, Allow all red; a mode not reported yet the plain shield), named for screen readers; Codex has none; the wide state
// pill has no second dot; its menu offers Allow all only where the agent can take it (disabled with its reason elsewhere),
// asks again going into it, and asks leaving it where there is no way back.
//
// Needs the web page against the fake hub with one agent per mode, and Chrome or Edge (playwright-core drives it):
//
//   MOCK_HEADER_MODES=1 PORT=18985 python3 scripts/mock/mock_hub.py &
//   (cd web && PERSONA_HUB_URL=http://127.0.0.1:18985 npx vite --port 5985 --strictPort &)
//   node web/e2e/header-mode.mjs http://127.0.0.1:5985 --hub http://127.0.0.1:18985 [--shots <dir>] [--before] [--channel msedge]
//
// Runs wide (1280) and phone width (390), light and dark, in Chinese; --shots saves each state; --before only saves the two
// pages (against an app without the button, for before / after). Prints one line per check and a verdict; exits 1 on a failure.
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
const before = args.includes("--before");
const channel = flag("--channel") ?? "chrome";
if (!base || !hub) {
  console.error("usage: node web/e2e/header-mode.mjs <web URL> --hub <fake hub URL> [--shots <dir>] [--before] [--channel msedge]");
  process.exit(2);
}
if (shots) mkdirSync(shots, { recursive: true });

// the fake hub's agents (scripts/mock/mock_hub.py, MOCK_HEADER_MODES=1)
const AGENTS = {
  asks: ["macbook", "a1f3c2d4-0000-4000-8000-000000000002"], // tests: Ask First
  plans: ["studio", "a1f3c2d4-0000-4000-8000-000000000005"], // photos: Plan Only
  edits: ["macbook", "a1f3c2d4-0000-4000-8000-000000000001"], // widget: Auto-Edit, can take Allow all
  auto: ["macbook", "a1f3c2d4-0000-4000-8000-000000000008"], // release: Auto
  allows: ["studio", "a1f3c2d4-0000-4000-8000-000000000003"], // blog: Allow all, no way back, background that waits
  unknown: ["macbook", "a1f3c2d4-0000-4000-8000-000000000006"], // review: nothing reported yet
  codex: ["server", "a1f3c2d4-0000-4000-8000-000000000004"], // orders: Codex, none
};
const WORDS = { asks: "权限模式：手动确认", plans: "权限模式：只做计划", edits: "权限模式：自动改文件", auto: "权限模式：自动", allows: "权限模式：全部放行", unknown: "权限模式：未知" };
const TONE = { asks: "asks", plans: "plans", edits: "acts", auto: "acts", allows: "allows", unknown: "unknown" };

const ok = (msg) => console.log(`ok  ${msg}`);
function fail(msg) {
  console.log(`FAIL ${msg}`);
  process.exit(1);
}
const modesLog = async () => (await (await fetch(`${hub}/mock/modes`, { signal: AbortSignal.timeout(5000) })).json()).modes ?? [];

async function open(browser, theme, width) {
  const ctx = await browser.newContext({ viewport: { width, height: width > 600 ? 800 : 844 }, deviceScaleFactor: 2, colorScheme: theme, locale: "zh-CN", hasTouch: width <= 600 });
  const page = await ctx.newPage();
  await page.goto(`${base}/?token=mock`);
  await page.waitForFunction(() => localStorage.getItem("persona.token"), null, { timeout: 60000 }); // a cold dev server compiles first
  await page.evaluate((t) => {
    localStorage.setItem("persona.theme", t);
    localStorage.setItem("persona.language", "zh-Hans");
  }, theme);
  await page.reload();
  await page.waitForSelector("main", { timeout: 60000 });
  return { ctx, page };
}

async function agent(page, which) {
  const [mid, sid] = AGENTS[which];
  await page.goto(`${base}/s/${mid}/${sid}`);
  await page.waitForSelector("header h1", { timeout: 15000 });
  await page.waitForTimeout(700); // the transcript and the session row settle
}

/** Escape closes a menu; on a narrow screen that also steps back the history entry the menu added (Back closes it), so
 *  the next page load waits for that to land. */
async function closeMenu(page) {
  await page.keyboard.press("Escape");
  await page.waitForTimeout(600);
}

const save = async (page, name, clip) => {
  if (!shots) return;
  await page.waitForTimeout(300);
  await page.screenshot({ path: join(shots, `web-header-mode-${name}.png`), ...(clip ? { clip } : {}) });
};

/** The header and what hangs under it (a menu): the top of the page. */
const top = (page, height) => ({ x: 0, y: 0, width: page.viewportSize().width, height });

const browser = await chromium.launch({ channel, headless: true });
try {
  for (const width of [1280, 390]) {
    for (const theme of ["light", "dark"]) {
      const tag = `${width}-${theme}`;
      const { ctx, page } = await open(browser, theme, width);
      if (before) {
        for (const which of ["allows", "edits"]) {
          await agent(page, which);
          await save(page, `before-${which}-${tag}`);
        }
        await ctx.close();
        ok(`${tag}: before`);
        continue;
      }
      // the two pages, as a whole
      for (const which of ["allows", "edits"]) {
        await agent(page, which);
        await save(page, `after-${which}-${tag}`);
      }
      // every mode's header: symbol, tone, words
      for (const which of Object.keys(WORDS)) {
        await agent(page, which);
        const button = page.getByTestId("header-mode");
        if ((await button.count()) !== 1) fail(`${tag} ${which}: no mode button`);
        if ((await button.getAttribute("aria-label")) !== WORDS[which]) fail(`${tag} ${which}: named ${await button.getAttribute("aria-label")}`);
        if ((await button.getAttribute("data-mode-tone")) !== TONE[which]) fail(`${tag} ${which}: tone ${await button.getAttribute("data-mode-tone")}`);
        await save(page, `header-${which}-${tag}`, top(page, 56));
      }
      ok(`${tag}: every mode in its tone and words`);
      await agent(page, "codex");
      if ((await page.getByTestId("header-mode").count()) !== 0) fail(`${tag}: Codex shows a mode`);
      await save(page, `header-codex-${tag}`, top(page, 56));
      ok(`${tag}: Codex shows none`);
      // the state once: wide, the pill has its words and no dot of its own
      if (width > 600) {
        await agent(page, "allows");
        const pill = page.locator("header [data-state-pill]");
        if ((await pill.count()) !== 1) fail(`${tag}: no state pill`);
        if ((await pill.locator("*").count()) !== 0) fail(`${tag}: the pill still has a dot`);
        ok(`${tag}: the state pill has no second dot`);
      }
      // the menu without Allow all on offer: there, disabled, saying why
      await agent(page, "asks");
      await page.getByTestId("header-mode").click();
      const bypassRow = page.getByRole("menuitem", { name: /全部放行/ });
      await bypassRow.waitFor({ state: "visible", timeout: 5000 });
      if (!(await bypassRow.isDisabled())) fail(`${tag}: Allow all is offered to an agent that can't take it`);
      await save(page, `menu-${tag}`, top(page, width > 600 ? 420 : 520));
      await closeMenu(page);
      ok(`${tag}: Allow all disabled where it can't be taken`);
      // the menu with it on offer, and going into it asks again
      await agent(page, "edits");
      await page.getByTestId("header-mode").click();
      const into = page.getByRole("menuitemradio", { name: /全部放行/ });
      await into.waitFor({ state: "visible", timeout: 5000 });
      await save(page, `menu-bypass-${tag}`, top(page, width > 600 ? 420 : 520));
      const sent = (await modesLog()).length;
      await into.click();
      await page.getByText("确认切到全部放行").waitFor({ state: "visible", timeout: 5000 });
      if ((await modesLog()).length !== sent) fail(`${tag}: Allow all was sent before the second choice`);
      await save(page, `confirm-bypass-${tag}`, top(page, width > 600 ? 420 : 520));
      await closeMenu(page);
      ok(`${tag}: going into Allow all asks again`);
      // leaving it where there is no way back asks too
      await agent(page, "allows");
      await page.getByTestId("header-mode").click();
      const out = page.getByRole("menuitemradio", { name: /只做计划/ });
      await out.waitFor({ state: "visible", timeout: 5000 });
      await out.click();
      await page.getByText("切到 只做计划").waitFor({ state: "visible", timeout: 5000 });
      if ((await modesLog()).length !== sent) fail(`${tag}: leaving was sent before the second choice`);
      await save(page, `confirm-leave-${tag}`, top(page, width > 600 ? 420 : 520));
      await closeMenu(page);
      ok(`${tag}: leaving Allow all asks where it can't come back`);
      await ctx.close();
    }
  }
  console.log("VERDICT ok");
} finally {
  await browser.close();
}
