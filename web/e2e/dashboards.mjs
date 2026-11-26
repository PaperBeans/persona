// Dashboards on the web (#15017) against the mock hub's Dashboards domain (scripts/mock/dashboards_mock.py): the sidebar unchanged
// with the switch off and with it on but nothing kept (compared pixel by pixel with a build of origin/main when --before
// is given), then with Dashboards and one pinned page (the desktop list and the phone's page grid), the index, the
// running page after a tap ("Received"), an unpinned page opened by its address (titled by itself, with the ownership
// line), and the switch in Settings › Assistant turning the place off and on live (assistant_pages). With --extensions
// (a mock with three extension nav pages) it checks Dashboards stays in the list and an extension page waits under More.
// Light and dark, 1280 and 390 wide, in each --langs language.
//
//   MOCK_DASHBOARDS=off PORT=18741 python3 scripts/mock/mock_hub.py &
//   (cd web && PERSONA_HUB_URL=http://127.0.0.1:18741 npx vite --port 5742 --strictPort &)   # this branch
//   (cd <origin/main>/web && PERSONA_HUB_URL=http://127.0.0.1:18741 npx vite --port 5741 --strictPort &)   # optional
//   node web/e2e/dashboards.mjs --after http://127.0.0.1:5742 --hub http://127.0.0.1:18741 --out <dir> \
//     [--before http://127.0.0.1:5741] [--langs en,zh-Hans,ja] [--extensions] [--channel chrome]
//
// The extensions run starts the mock with MOCK_EXTENSIONS=1 MOCK_EXTENSION_COLUMNS=1 MOCK_EXTENSION_GAUGE=1 as well.
// Prints one line per check and a verdict; exits 1 on a failed check and 2 on its timeout.
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const flag = (name, fallback) => { const i = args.indexOf(name); return i >= 0 ? args[i + 1] : fallback; };
const after = (flag("--after", "") ?? "").replace(/\/$/, "");
const before = (flag("--before", "") ?? "").replace(/\/$/, "");
const hub = (flag("--hub", "") ?? "").replace(/\/$/, "");
const out = flag("--out", "");
const langs = flag("--langs", "en").split(",");
const extensions = args.includes("--extensions");
const channel = flag("--channel", "");
if (!after || !hub || !out) {
  console.error("usage: node web/e2e/dashboards.mjs --after <web URL> --hub <mock hub URL> --out <dir> [--before <web URL>] [--langs en,zh-Hans,ja] [--extensions] [--channel chrome]");
  process.exit(2);
}
mkdirSync(out, { recursive: true });
const watchdog = setTimeout(() => { console.log("VERDICT TIMEOUT dashboards"); process.exit(2); }, 900_000);
const failures = [];
const check = (ok, what, detail = "") => { console.log(`${ok ? "ok  " : "FAIL"} ${what}${detail ? `: ${detail}` : ""}`); if (!ok) failures.push(what); };
const mode = async (m) => { const r = await fetch(`${hub}/mock/dashboards?mode=${m}`); if (!r.ok) throw new Error(`mock mode ${m}: ${r.status}`); };
const RUN = "pg_3f9a1c0d5e7b2a41";
const READING = "pg_8c2e4b6d1a0f9e37";
const WORDS = {
  en: { dashboards: "Dashboards", run: "Morning run", reading: "Reading list", owned: "Kept up to date by Persona", done: "Done today", received: "Received", toggle: "Let Persona make dashboards", sidebar: "Sidebar", more: "More" },
  "zh-Hans": { dashboards: "仪表盘", run: "晨跑打卡", reading: "阅读清单", owned: "由 Persona 维护", done: "今天完成", received: "已收到", toggle: "让 Persona 做仪表盘", sidebar: "侧边栏", more: "更多" },
  ja: { dashboards: "ダッシュボード", run: "朝ラン", reading: "読書リスト", owned: "Persona が更新しています", done: "今日は完了", received: "受け取りました", toggle: "Persona にダッシュボードを作らせる", sidebar: "サイドバー", more: "その他" },
};

const browser = await chromium.launch({ ...(channel ? { channel } : {}), ...(process.env.CHROMIUM_PATH ? { executablePath: process.env.CHROMIUM_PATH } : {}), headless: true });

/** A fresh browser signed in to `base` with the theme and language set, at `width`. */
async function signedIn(base, { theme, width, lang }) {
  const phone = width < 500;
  const ctx = await browser.newContext({ viewport: { width, height: phone ? 844 : 900 }, deviceScaleFactor: 2, locale: lang === "zh-Hans" ? "zh-CN" : lang, colorScheme: theme, reducedMotion: "reduce", hasTouch: phone, isMobile: phone });
  const page = await ctx.newPage();
  const errors = [];
  page.on("pageerror", (e) => errors.push(String(e)));
  await page.goto(`${base}/?token=mock`);
  await page.waitForFunction(() => localStorage.getItem("persona.token"), null, { timeout: 20_000 });
  await page.evaluate(([theme, lang]) => {
    localStorage.setItem("persona.theme", theme);
    localStorage.setItem("persona.language", lang);
  }, [theme, lang]);
  return { ctx, page, errors, phone };
}

/** The sidebar as drawn: the desktop's column, or the phone's drawer (its page grid on top) opened from Settings. */
async function sidebar(page, phone, w, file) {
  if (phone) {
    await page.goto(`${page.url().split("/").slice(0, 3).join("/")}/settings`);
    await page.getByRole("button", { name: w.sidebar, exact: true }).first().click();
  } else {
    await page.goto(`${page.url().split("/").slice(0, 3).join("/")}/`);
  }
  const aside = page.locator("aside").first();
  await aside.waitFor({ timeout: 20_000 });
  await page.waitForLoadState("networkidle").catch(() => {});
  await page.waitForTimeout(1500);
  const box = await aside.boundingBox();
  const view = page.viewportSize();
  await page.mouse.move(view.width - 1, view.height - 1); // no hover on a row
  await page.screenshot({ path: file, clip: { x: Math.max(0, box.x), y: Math.max(0, box.y), width: Math.min(box.width, view.width), height: Math.min(box.height, view.height) }, animations: "disabled", caret: "hide" });
  return aside;
}

/** Pixels that differ between two PNGs of the same size, decoded by the browser. */
async function pixelDiff(a, b) {
  const page = await browser.newPage();
  try {
    return await page.evaluate(async ([a, b]) => {
      const decode = async (b64) => {
        const bitmap = await createImageBitmap(await (await fetch(`data:image/png;base64,${b64}`)).blob());
        const canvas = new OffscreenCanvas(bitmap.width, bitmap.height);
        const g = canvas.getContext("2d");
        g.drawImage(bitmap, 0, 0);
        return { w: bitmap.width, h: bitmap.height, data: g.getImageData(0, 0, bitmap.width, bitmap.height).data };
      };
      const [x, y] = [await decode(a), await decode(b)];
      if (x.w !== y.w || x.h !== y.h) return { size: `${x.w}x${x.h} vs ${y.w}x${y.h}`, differing: -1, total: x.w * x.h };
      let differing = 0;
      for (let i = 0; i < x.data.length; i += 4) if (x.data[i] !== y.data[i] || x.data[i + 1] !== y.data[i + 1] || x.data[i + 2] !== y.data[i + 2] || x.data[i + 3] !== y.data[i + 3]) differing++;
      return { size: `${x.w}x${x.h}`, differing, total: x.w * x.h };
    }, [readFileSync(a).toString("base64"), readFileSync(b).toString("base64")]);
  } finally {
    await page.close();
  }
}

const diffs = [];
try {
  for (const lang of langs) for (const theme of ["light", "dark"]) for (const width of [1280, 390]) {
    const w = WORDS[lang];
    const tag = `${extensions ? "ext-" : ""}${lang}-${theme}-${width}`;
    const dashboardsRow = (page) => page.locator(`aside a[href$="/extension/-pages/index"]`);

    // 1. nothing changes with the switch off, or on with nothing kept (principle 2)
    const shots = {};
    if (before) {
      await mode("off");
      const b = await signedIn(before, { theme, width, lang });
      shots.before = join(out, `sidebar-before-${tag}.png`);
      await sidebar(b.page, b.phone, w, shots.before);
      await b.ctx.close();
    }
    for (const m of ["off", "empty"]) {
      await mode(m);
      const s = await signedIn(after, { theme, width, lang });
      shots[m] = join(out, `sidebar-${m}-${tag}.png`);
      await sidebar(s.page, s.phone, w, shots[m]);
      check(await dashboardsRow(s.page).count() === 0, `${tag}: no Dashboards row with the switch ${m === "off" ? "off" : "on and nothing kept"}`);
      check(s.errors.length === 0, `${tag}: no page errors (${m})`, s.errors.join(" | "));
      await s.ctx.close();
    }
    for (const [x, y] of [["before", "off"], ["before", "empty"], ["off", "empty"]]) {
      if (!shots[x] || !shots[y]) continue;
      const d = await pixelDiff(shots[x], shots[y]);
      diffs.push({ tag, pair: `${x} vs ${y}`, ...d });
      check(d.differing === 0, `${tag}: sidebar ${x} and ${y} are pixel-identical`, `${d.size}, ${d.differing} of ${d.total} pixels differ`);
    }

    // 2. Dashboards and one pinned page
    await mode("1");
    const s = await signedIn(after, { theme, width, lang });
    const { page } = s;
    await sidebar(page, s.phone, w, join(out, `sidebar-dashboards-${tag}.png`));
    const row = s.phone ? page.locator(`nav a[href$="/extension/-pages/index"]`) : dashboardsRow(page);
    check(await row.count() === 1, `${tag}: Dashboards is listed`);
    check(await page.locator(`a[href$="/extension/-pages/${RUN}"]`).count() >= 1, `${tag}: the pinned page is listed`);
    if (extensions) {
      // three extension nav pages installed: Dashboards (kept, so in the navigation record's shown) stays in the list
      const moreButton = s.phone ? page.locator(`nav button[aria-haspopup="menu"]`) : page.locator("aside").getByRole("button", { name: new RegExp(w.more) }).first();
      check(await moreButton.count() >= 1, `${tag}: an extension page waits under More`);
      check(await row.isVisible(), `${tag}: Dashboards is in the list itself, not under More`);
    }

    // 3. the index, then a page opened from it: titled by itself, the ownership line under its title
    await page.goto(`${after}/extension/-pages/index`);
    await page.getByText(w.run, { exact: true }).first().waitFor({ timeout: 20_000 });
    await page.waitForTimeout(600);
    check((await page.locator("header h1").first().textContent()) === w.dashboards, `${tag}: the index is titled ${w.dashboards}`);
    check(await page.locator("header [data-ownership-line]").count() === 0, `${tag}: the index has no ownership line`);
    await page.screenshot({ path: join(out, `index-${tag}.png`), animations: "disabled", caret: "hide" });
    await page.locator("main").getByRole("button", { name: new RegExp(w.reading) }).first().click();
    await page.waitForURL(new RegExp(`/extension/-pages/${READING}$`), { timeout: 10_000 });
    await page.locator("header [data-ownership-line]").waitFor({ timeout: 10_000 });
    check((await page.locator("header h1").first().textContent()) === w.reading, `${tag}: a page the index opens is titled by itself`);

    // 4. an unpinned page by its address: its title, never pg_…, and the ownership line at this width
    await page.goto(`${after}/extension/-pages/${READING}`);
    await page.locator("header [data-ownership-line]").waitFor({ timeout: 20_000 });
    await page.waitForFunction((t) => document.querySelector("header h1")?.textContent === t, w.reading, { timeout: 10_000 });
    check(!(await page.locator("header").first().textContent()).includes("pg_"), `${tag}: an unpinned page's header never shows its id`);
    check(await page.locator("header [data-ownership-line]").isVisible(), `${tag}: the ownership line is visible at ${width}`);
    check((await page.locator("header [data-ownership-line]").textContent()) === w.owned, `${tag}: the ownership line's words`, w.owned);
    await page.screenshot({ path: join(out, `page-unpinned-${tag}.png`), animations: "disabled", caret: "hide" });

    // 5. the running page, tapped: "Received" at once, as a toast and in its footer
    await page.goto(`${after}/extension/-pages/${RUN}`);
    const done = page.getByRole("button", { name: w.done, exact: true });
    await done.waitFor({ timeout: 20_000 });
    check((await page.locator("header h1").first().textContent()) === w.run, `${tag}: the pinned page is titled by itself`);
    await page.waitForTimeout(400);
    await page.screenshot({ path: join(out, `page-${tag}.png`), animations: "disabled", caret: "hide" });
    const tapped = Date.now();
    await done.click();
    await page.getByText(w.received, { exact: true }).first().waitFor({ timeout: 5_000 });
    const receipt = Date.now() - tapped;
    check(receipt < 1000, `${tag}: "Received" within a second of the tap`, `${receipt} ms`);
    await page.waitForTimeout(300);
    await page.locator('[data-settings-group], main').last().evaluate((el) => el.closest(".overflow-y-auto")?.scrollTo(0, 99999));
    await page.waitForTimeout(300);
    await page.screenshot({ path: join(out, `page-received-${tag}.png`), animations: "disabled", caret: "hide" });

    // 6. the switch in Settings › Assistant, turned off and on: the place goes and comes back without a reload
    await page.goto(`${after}/settings?tab=persona#dashboards`);
    const toggle = page.locator('[data-settings-group="dashboards"] [role="switch"]');
    await toggle.waitFor({ timeout: 20_000 });
    check(await toggle.getAttribute("aria-checked") === "true", `${tag}: the switch shows the hub's value`);
    check((await page.locator('[data-settings-group="dashboards"]').textContent()).includes(w.toggle), `${tag}: the switch's label`, w.toggle);
    await page.locator('[data-settings-group="dashboards"]').scrollIntoViewIfNeeded();
    await page.waitForTimeout(400);
    await page.screenshot({ path: join(out, `settings-${tag}.png`), animations: "disabled", caret: "hide" });
    if (!s.phone) {
      await toggle.click();
      await page.waitForFunction(() => document.querySelector('[data-settings-group="dashboards"] [role="switch"]')?.getAttribute("aria-checked") === "false");
      await page.waitForFunction(() => !document.querySelector('aside a[href$="/extension/-pages/index"]'), null, { timeout: 5_000 }).then(() => check(true, `${tag}: the switch off takes the place away live`), () => check(false, `${tag}: the switch off takes the place away live`));
      await toggle.click();
      await page.waitForFunction(() => !!document.querySelector('aside a[href$="/extension/-pages/index"]'), null, { timeout: 5_000 }).then(() => check(true, `${tag}: the switch on brings it back live`), () => check(false, `${tag}: the switch on brings it back live`));
    }
    check(s.errors.length === 0, `${tag}: no page errors`, s.errors.join(" | "));
    await s.ctx.close();
  }
} finally {
  writeFileSync(join(out, `pixel-diff${extensions ? "-ext" : ""}.json`), JSON.stringify(diffs, null, 2));
  await mode("off").catch(() => {});
  await browser.close();
  clearTimeout(watchdog);
}
console.log(failures.length ? `VERDICT FAIL dashboards: ${failures.length} check(s) failed` : "VERDICT PASS dashboards");
process.exit(failures.length ? 1 : 0);
