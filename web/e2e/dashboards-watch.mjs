// Dashboards phase 2 watch pages and Overview cards on the web (#13020) against the mock hub's Dashboards domain
// (scripts/mock/dashboards_mock.py, MOCK_DASHBOARDS=watch):
//   1. Pixel-identical regression check against --before (origin/main) for MOCK_DASHBOARDS=off and MOCK_DASHBOARDS=1
//      (phase 1), comparing both the sidebar and Overview (top + bottom) in light and dark at 1280 and 390 wide.
//   2. Watch pages (number, status, table, trend) in empty, normal and alert states, light and dark, 1280 and 390 wide
//      in en; the index rows' value, spark and icon color; no wrapped button text; no page errors.
//   3. Watch footer transitions: Checked…/next line, Refresh now -> Checking now… -> Already checking -> ?advance=1,
//      Pause updates -> Updates paused (no Refresh now) -> Resume updates, Alert me switch on/off, Show on Overview
//      switch on/off (also verified under desktop platform context).
//   4. Overview with the two -pages g_<hex> cards (Elm Street rent, Morning weight): rendered as ordinary Overview
//      cards, live update on extension_ui invalidation, click opens /extension/-pages/<pg id>, hidden from Settings ›
//      Extensions; plus Large text + accent theme check.
//   5. zh-Hans and ja captures for the watch pages, Dashboards index and Overview (light and dark, 1280 and 390).
//
//   MOCK_DASHBOARDS=watch PORT=18781 python3 scripts/mock/mock_hub.py &
//   (cd web && PERSONA_HUB_URL=http://127.0.0.1:18781 npx vite --port 5782 --strictPort &)
//   (cd <origin/main>/web && PERSONA_HUB_URL=http://127.0.0.1:18781 npx vite --port 5781 --strictPort &)   # optional
//   node web/e2e/dashboards-watch.mjs --after http://127.0.0.1:5782 --hub http://127.0.0.1:18781 --out <dir> \
//     [--before http://127.0.0.1:5781] [--channel chrome]
//
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
const channel = flag("--channel", "");
if (!after || !hub || !out) {
  console.error("usage: node web/e2e/dashboards-watch.mjs --after <web URL> --hub <mock hub URL> --out <dir> [--before <web URL>] [--channel chrome]");
  process.exit(2);
}
mkdirSync(out, { recursive: true });
const watchdog = setTimeout(() => { console.log("VERDICT TIMEOUT dashboards-watch"); process.exit(2); }, 900_000);
const failures = [];
const check = (ok, what, detail = "") => { console.log(`${ok ? "ok  " : "FAIL"} ${what}${detail ? `: ${detail}` : ""}`); if (!ok) failures.push(what); };

const hubGet = async (path) => {
  const r = await fetch(`${hub}${path}`);
  if (!r.ok) throw new Error(`mock ${path}: ${r.status}`);
  return r.json();
};
const setMode = (m) => hubGet(`/mock/dashboards?mode=${m}`);
const setWatch = (w, s) => hubGet(`/mock/dashboards?watch=${w}&state=${s}`);
const advance = () => hubGet("/mock/dashboards?advance=1");

const WATCH = {
  number: { pg: "pg_1a2b3c4d5e6f7081", g: "g_1a2b3c4d5e6f7081" },
  status: { pg: "pg_2b3c4d5e6f708192", g: "g_2b3c4d5e6f708192" },
  table: { pg: "pg_3c4d5e6f708192a3", g: "g_3c4d5e6f708192a3" },
  trend: { pg: "pg_4d5e6f708192a3b4", g: "g_4d5e6f708192a3b4" },
};

const WORDS = {
  en: {
    sidebar: "Sidebar", dashboards: "Dashboards", owned: "Kept up to date by Persona",
    numberTitle: "Elm Street rent", statusTitle: "Shop sites", tableTitle: "Elm Street listings", trendTitle: "Morning weight",
    refresh: "Refresh now", checking: "Checking now…", already: "Already checking",
    pause: "Pause updates", resume: "Resume updates", paused: "Updates paused",
    alertMe: "Alert me", showOverview: "Show on Overview",
    notChecked: "Not checked yet", checked: "Checked today 08:02",
    numberNormal: "2,150 USD", numberAlert: "1,985.5 USD",
    statusNormal: "All 3 sites up", statusAlert: "Shop site down",
    tableNormal: "2 rows", tableAlert: "3 rows",
    trendNormal: "77.9 kg", trendAlert: "80.3 kg",
  },
  "zh-Hans": {
    sidebar: "侧边栏", dashboards: "仪表盘", owned: "由 Persona 维护",
    numberTitle: "榆树街房租", statusTitle: "店铺网站", tableTitle: "低价房源", trendTitle: "晨间体重",
    refresh: "立即刷新", checking: "正在检查…", already: "已经在检查了",
    pause: "暂停更新", resume: "恢复更新", paused: "更新已暂停",
    alertMe: "触发时提醒", showOverview: "在总览显示",
    notChecked: "尚未检查", checked: "今天 08:02 检查",
    numberAlert: "1,985.5 美元", tableAlert: "3 条记录", trendAlert: "80.3 公斤",
  },
  ja: {
    sidebar: "サイドバー", dashboards: "ダッシュボード", owned: "Persona が更新しています",
    numberTitle: "エルム通りの家賃", statusTitle: "ショップのサイト", tableTitle: "新着の賃貸物件", trendTitle: "朝の体重",
    refresh: "今すぐ更新", checking: "確認中…", already: "すでに確認中です",
    pause: "更新を一時停止", resume: "更新を再開", paused: "更新は一時停止中",
    alertMe: "条件に達したら通知", showOverview: "概要に表示",
    notChecked: "未確認", checked: "今日 08:02 に確認",
    numberAlert: "1,985.5 ドル", tableAlert: "3 件", trendAlert: "80.3 kg",
  },
};

const FIXED_TIME = new Date("2026-11-25T09:00:00Z");
const browser = await chromium.launch({
  ...(channel ? { channel } : {}),
  ...(process.env.CHROMIUM_PATH ? { executablePath: process.env.CHROMIUM_PATH } : {}),
  headless: true,
});

async function signedIn(base, { theme, width, lang, textSize = "standard", accent = "default", desktop = false }) {
  const phone = width < 500;
  const ctx = await browser.newContext({
    viewport: { width, height: phone ? 844 : 900 },
    deviceScaleFactor: 2,
    locale: lang === "zh-Hans" ? "zh-CN" : lang,
    colorScheme: theme,
    reducedMotion: "reduce",
    hasTouch: phone,
    isMobile: phone,
  });
  if (desktop) {
    await ctx.addInitScript(() => {
      window.personaDesktop = {
        version: 1,
        platform: "darwin",
        deviceName: "Mock Desktop",
        notificationPermission: () => "granted",
        setBadge() {},
        notify: () => false,
        closeNotification() {},
        clearNotifications() {},
        onOpenNotification: () => () => {},
        desktopPresence: () => ({ active: true, notifies: true }),
        onPresenceChange: () => () => {},
        setLaunchAtLogin: async (enabled) => ({ enabled }),
        getLaunchAtLogin: async () => ({ enabled: false }),
      };
    });
  }
  const page = await ctx.newPage();
  await page.clock.setFixedTime(FIXED_TIME);
  const errors = [];
  const placesRequests = [];
  const renderPlatforms = [];
  page.on("pageerror", (e) => errors.push(String(e)));
  page.on("request", (req) => {
    const u = req.url();
    if (/\/api\/v1\/(?:extensions|plugins)\/-\/ui(?:\?|$)/.test(u) && req.method() === "GET") placesRequests.push(u);
    if (/\/api\/v1\/(?:extensions|plugins)\/-pages\/-\/ui\//.test(u) && req.method() === "POST") {
      try {
        const body = JSON.parse(req.postData() || "{}");
        if (body.context?.platform) renderPlatforms.push(body.context.platform);
      } catch {}
    }
  });
  await page.goto(`${base}/?token=mock`);
  await page.waitForFunction(() => localStorage.getItem("persona.token"), null, { timeout: 20_000 });
  await page.evaluate(([t, l, ts, ac]) => {
    localStorage.setItem("persona.theme", t);
    localStorage.setItem("persona.language", l);
    if (ts !== "standard") localStorage.setItem("persona.text-size", ts);
    if (ac !== "default") localStorage.setItem("persona.accent", ac);
  }, [theme, lang, textSize, accent]);
  return { ctx, page, errors, phone, placesRequests, renderPlatforms };
}

async function captureSidebar(page, phone, w, file) {
  const origin = page.url().split("/").slice(0, 3).join("/");
  if (phone) {
    await page.goto(`${origin}/settings`);
    await page.getByRole("button", { name: w.sidebar, exact: true }).first().click();
  } else {
    await page.goto(`${origin}/`);
  }
  const aside = page.locator("aside").first();
  await aside.waitFor({ timeout: 20_000 });
  await page.waitForLoadState("networkidle").catch(() => {});
  await page.waitForTimeout(1000);
  const box = await aside.boundingBox();
  const view = page.viewportSize();
  await page.mouse.move(view.width - 1, view.height - 1);
  await page.screenshot({
    path: file,
    clip: { x: Math.max(0, box.x), y: Math.max(0, box.y), width: Math.min(box.width, view.width), height: Math.min(box.height, view.height) },
    animations: "disabled",
    caret: "hide",
  });
}

async function captureOverview(page, scrollBottom, file) {
  const origin = page.url().split("/").slice(0, 3).join("/");
  await page.goto(`${origin}/overview`);
  await page.locator("main h1").first().waitFor({ timeout: 20_000 });
  await page.waitForLoadState("networkidle").catch(() => {});
  await page.waitForTimeout(1000);
  const scroller = page.locator("main .overflow-y-auto").first();
  await scroller.evaluate((el, bottom) => { el.scrollTop = bottom ? el.scrollHeight : 0; }, scrollBottom);
  await page.waitForTimeout(300);
  const view = page.viewportSize();
  await page.mouse.move(view.width - 1, view.height - 1);
  await page.screenshot({ path: file, animations: "disabled", caret: "hide" });
}

async function checkNoWrappedButtons(page, label) {
  const wrapped = await page.evaluate(() => {
    const bad = [];
    for (const btn of document.querySelectorAll("main section button")) {
      if (!btn.className.includes("inline-flex")) continue;
      const r = btn.getBoundingClientRect();
      if (r.height > 40) bad.push(`${btn.textContent?.trim()} (${Math.round(r.height)}px)`);
    }
    return bad;
  });
  check(wrapped.length === 0, `${label}: no button text wraps`, wrapped.join(", "));
}

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
      for (let i = 0; i < x.data.length; i += 4) {
        if (x.data[i] !== y.data[i] || x.data[i + 1] !== y.data[i + 1] || x.data[i + 2] !== y.data[i + 2] || x.data[i + 3] !== y.data[i + 3]) differing++;
      }
      return { size: `${x.w}x${x.h}`, differing, total: x.w * x.h };
    }, [readFileSync(a).toString("base64"), readFileSync(b).toString("base64")]);
  } finally {
    await page.close();
  }
}

const diffs = [];
try {
  // ── 1. Pixel-identical regression proof: MOCK_DASHBOARDS=off and MOCK_DASHBOARDS=1 (phase 1) ──
  if (before) {
    for (const m of ["off", "1"]) {
      await setMode(m);
      for (const theme of ["light", "dark"]) for (const width of [1280, 390]) {
        const tag = `${m}-en-${theme}-${width}`;
        const w = WORDS.en;
        const shots = {};
        for (const [which, base] of [["before", before], ["after", after]]) {
          const s = await signedIn(base, { theme, width, lang: "en" });
          shots[`${which}-sidebar`] = join(out, `diff-sidebar-${which}-${tag}.png`);
          await captureSidebar(s.page, s.phone, w, shots[`${which}-sidebar`]);
          shots[`${which}-overview-top`] = join(out, `diff-overview-top-${which}-${tag}.png`);
          await captureOverview(s.page, false, shots[`${which}-overview-top`]);
          shots[`${which}-overview-bot`] = join(out, `diff-overview-bot-${which}-${tag}.png`);
          await captureOverview(s.page, true, shots[`${which}-overview-bot`]);
          if (which === "after") {
            check(await s.page.locator('[data-extension-card^="-pages/"]').count() === 0, `${tag}: no -pages Overview cards in mode=${m}`);
            check(s.placesRequests.some((u) => u.includes("pages_overview=1")), `${tag}: places requested with ?pages_overview=1`);
            check(s.errors.length === 0, `${tag}: no page errors`, s.errors.join(" | "));
          }
          await s.ctx.close();
        }
        for (const surface of ["sidebar", "overview-top", "overview-bot"]) {
          const d = await pixelDiff(shots[`before-${surface}`], shots[`after-${surface}`]);
          diffs.push({ mode: m, surface, theme, width, ...d });
          check(d.differing === 0, `${tag} ${surface}: pixel-identical to origin/main`, `${d.size}, ${d.differing} of ${d.total} pixels differ`);
        }
      }
    }
  }

  // ── 2. Four watch displays (empty / normal / alert), index rows, and Overview cards in en ──
  await setMode("watch");
  for (const theme of ["light", "dark"]) for (const width of [1280, 390]) {
    const tag = `en-${theme}-${width}`;
    const w = WORDS.en;
    const s = await signedIn(after, { theme, width, lang: "en" });
    const { page } = s;

    for (const state of ["empty", "normal", "alert"]) {
      await setWatch("all", state);
      for (const [display, ids] of Object.entries(WATCH)) {
        await page.goto(`${after}/extension/-pages/${ids.pg}`);
        await page.locator("header [data-ownership-line]").waitFor({ timeout: 20_000 });
        await page.locator("main section").first().waitFor({ timeout: 20_000 });
        await page.waitForTimeout(350);
        const text = await page.locator("main").innerText();
        const expectVal = state === "empty" ? "—"
          : display === "number" ? (state === "normal" ? w.numberNormal : w.numberAlert)
          : display === "status" ? (state === "normal" ? w.statusNormal : w.statusAlert)
          : display === "table" ? (state === "normal" ? w.tableNormal : w.tableAlert)
          : (state === "normal" ? w.trendNormal : w.trendAlert);
        check(text.includes(expectVal), `${tag} watch ${display}/${state}: shows value ${expectVal}`);
        if (state === "empty") check(text.includes(w.notChecked), `${tag} watch ${display}/empty: shows Not checked yet`);
        else check(text.includes(w.checked), `${tag} watch ${display}/${state}: shows Checked today 08:02`);
        if (display === "table" && state !== "empty") {
          const rows = await page.locator("main table tbody tr").count();
          check(rows === (state === "normal" ? 2 : 3), `${tag} watch table/${state}: draws ${state === "normal" ? 2 : 3} table rows`, `got ${rows}`);
        }
        if (display === "trend" && state !== "empty") {
          const spark = await page.locator("main section svg polyline").count();
          check(spark >= 1, `${tag} watch trend/${state}: draws sparkline`);
        }
        check(await page.getByRole("switch", { name: w.showOverview }).count() === 1, `${tag} watch ${display}/${state}: shows Show on Overview switch`);
        await checkNoWrappedButtons(page, `${tag} watch ${display}/${state}`);
        await page.screenshot({ path: join(out, `watch-${display}-${state}-${tag}.png`), animations: "disabled", caret: "hide" });
      }

      // Index (normal and alert)
      if (state !== "empty") {
        await page.goto(`${after}/extension/-pages/index`);
        await page.locator("main section button").first().waitFor({ timeout: 20_000 });
        await page.waitForTimeout(400);
        const idxText = await page.locator("main").innerText();
        check(idxText.includes(state === "normal" ? w.numberNormal : w.numberAlert), `${tag} index/${state}: number glance value`);
        check(idxText.includes(state === "normal" ? w.statusNormal : w.statusAlert), `${tag} index/${state}: status glance value`);
        check(idxText.includes(state === "normal" ? w.tableNormal : w.tableAlert), `${tag} index/${state}: table glance value`);
        check(idxText.includes(state === "normal" ? w.trendNormal : w.trendAlert), `${tag} index/${state}: trend glance value`);
        check(await page.locator("main section svg polyline").count() >= 1, `${tag} index/${state}: trend row sparkline`);
        await page.screenshot({ path: join(out, `index-${state}-${tag}.png`), animations: "disabled", caret: "hide" });
      }

      // Overview with the two watch cards (normal and alert)
      if (state !== "empty") {
        await page.goto(`${after}/overview`);
        await page.locator(`[data-extension-card="-pages/${WATCH.number.g}"]`).waitFor({ timeout: 20_000 });
        await page.locator(`[data-extension-card="-pages/${WATCH.trend.g}"]`).waitFor({ timeout: 20_000 });
        await page.locator("[data-overview-cards]").scrollIntoViewIfNeeded();
        await page.waitForTimeout(450);
        const cardsCount = await page.locator('[data-extension-card^="-pages/g_"]').count();
        check(cardsCount === 2, `${tag} overview/${state}: draws the two -pages cards`, `got ${cardsCount}`);
        const cardHref = await page.locator(`[data-extension-card="-pages/${WATCH.number.g}"] a`).first().getAttribute("href");
        check(cardHref?.endsWith(`/extension/-pages/${WATCH.number.pg}`) ?? false, `${tag} overview/${state}: card opens /extension/-pages/<pg id>`, cardHref ?? "");
        await page.screenshot({ path: join(out, `overview-${state}-${tag}.png`), animations: "disabled", caret: "hide" });
      }
    }

    check(s.placesRequests.some((u) => u.includes("pages_overview=1")), `${tag}: places asked with ?pages_overview=1`);
    check(s.renderPlatforms.every((p) => p === "web") && s.renderPlatforms.length > 0, `${tag}: context.platform is web`);
    check(s.errors.length === 0, `${tag}: no page errors`, s.errors.join(" | "));
    await s.ctx.close();
  }

  // ── 3. Interactive footer states, live Overview invalidation & click, desktop platform, Settings › Extensions isolation ──
  for (const theme of ["light", "dark"]) for (const width of [1280, 390]) {
    await setMode("watch");
    const tag = `en-${theme}-${width}`;
    const w = WORDS.en;
    const s = await signedIn(after, { theme, width, lang: "en" });
    const { page } = s;

    await page.goto(`${after}/extension/-pages/${WATCH.number.pg}`);
    await page.getByRole("button", { name: w.refresh, exact: true }).waitFor({ timeout: 20_000 });

    // Refresh now -> Checking now…
    await page.getByRole("button", { name: w.refresh, exact: true }).click();
    await page.locator("main section").getByText(w.checking, { exact: true }).waitFor({ timeout: 5_000 });
    await page.waitForTimeout(250);
    await page.screenshot({ path: join(out, `footer-checking-${tag}.png`), animations: "disabled", caret: "hide" });

    // Second click while checking -> Already checking toast
    await page.getByRole("button", { name: w.refresh, exact: true }).click();
    await page.getByText(w.already, { exact: true }).first().waitFor({ timeout: 5_000 });
    check(true, `${tag} footer: Already checking toast on second Refresh now`);

    // Advance -> finishes refresh, updates reading live, clears Checking now…
    await advance();
    await page.getByText(w.numberAlert, { exact: true }).waitFor({ timeout: 10_000 });
    check(await page.locator("main section").getByText(w.checking, { exact: true }).count() === 0, `${tag} footer: Checking now… cleared after advance`);

    // Pause updates -> Updates paused, no Refresh now, Resume updates
    await page.getByRole("button", { name: w.pause, exact: true }).click();
    await page.getByRole("button", { name: w.resume, exact: true }).waitFor({ timeout: 5_000 });
    check(await page.locator("main section").getByText(w.paused, { exact: true }).count() === 1, `${tag} footer: shows Updates paused`);
    check(await page.getByRole("button", { name: w.refresh, exact: true }).count() === 0, `${tag} footer: Refresh now hidden while paused`);
    await page.waitForTimeout(250);
    await page.screenshot({ path: join(out, `footer-paused-${tag}.png`), animations: "disabled", caret: "hide" });

    // Resume updates + Alert me switch on
    await page.getByRole("button", { name: w.resume, exact: true }).click();
    await page.getByRole("button", { name: w.refresh, exact: true }).waitFor({ timeout: 5_000 });
    const alertSw = page.getByRole("switch", { name: w.alertMe });
    await alertSw.click();
    await page.waitForFunction((label) => document.querySelector(`[role="switch"][aria-label="${label}"]`)?.getAttribute("aria-checked") === "true", w.alertMe, { timeout: 5_000 });
    await page.waitForTimeout(250);
    await page.screenshot({ path: join(out, `footer-alert-on-${tag}.png`), animations: "disabled", caret: "hide" });

    if (theme === "light" && width === 1280) {
      // Live Overview invalidation + tapping card opens /extension/-pages/<pg id>
      await page.goto(`${after}/overview`);
      const rentCard = page.locator(`[data-extension-card="-pages/${WATCH.number.g}"]`);
      await rentCard.waitFor({ timeout: 20_000 });
      await setWatch("number", "normal");
      await rentCard.getByText(w.numberNormal, { exact: true }).waitFor({ timeout: 10_000 });
      check(true, "overview live invalidation updates g_ card in place");
      await rentCard.click();
      await page.waitForURL(new RegExp(`/extension/-pages/${WATCH.number.pg}$`), { timeout: 10_000 });
      check(true, "clicking -pages Overview card opens /extension/-pages/<pg id>");

      // Settings › Extensions never lists -pages or g_ cards
      await page.goto(`${after}/settings?tab=extensions`);
      await page.waitForTimeout(800);
      const extText = await page.locator("main").innerText();
      check(!extText.includes(w.numberTitle) && !extText.includes("-pages") && !extText.includes("g_"), "Settings › Extensions never lists -pages Overview cards");
    }

    check(s.errors.length === 0, `${tag} footer flow: no page errors`, s.errors.join(" | "));
    await s.ctx.close();
  }

  // Verify desktop platform sends context.platform="desktop" and still renders Show on Overview
  await setMode("watch");
  {
    const s = await signedIn(after, { theme: "light", width: 1280, lang: "en", desktop: true });
    await s.page.goto(`${after}/extension/-pages/${WATCH.number.pg}`);
    await s.page.getByRole("switch", { name: WORDS.en.showOverview }).waitFor({ timeout: 15_000 });
    check(s.renderPlatforms.every((p) => p === "desktop") && s.renderPlatforms.length > 0, "desktop app sends context.platform=desktop and shows Show on Overview");
    await s.ctx.close();
  }

  // Verify web theme variants (Large text + accent coral & teal)
  await setMode("watch");
  await setWatch("all", "alert");
  {
    const s = await signedIn(after, { theme: "light", width: 390, lang: "en", textSize: "large", accent: "coral" });
    await s.page.goto(`${after}/extension/-pages/${WATCH.table.pg}`);
    await s.page.locator("header [data-ownership-line]").waitFor({ timeout: 15_000 });
    await s.page.waitForTimeout(350);
    await checkNoWrappedButtons(s.page, "en-light-390-large-coral watch table/alert");
    await s.page.screenshot({ path: join(out, "watch-table-alert-en-light-390-large-coral.png"), animations: "disabled", caret: "hide" });
    await s.ctx.close();
  }

  // ── 4. zh-Hans and ja: watch page, index and Overview (light & dark, 1280 & 390) ──
  await setMode("watch");
  await setWatch("all", "alert");
  for (const lang of ["zh-Hans", "ja"]) for (const theme of ["light", "dark"]) for (const width of [1280, 390]) {
    const tag = `${lang}-${theme}-${width}`;
    const w = WORDS[lang];
    const s = await signedIn(after, { theme, width, lang });
    const { page } = s;

    await page.goto(`${after}/extension/-pages/${WATCH.number.pg}`);
    await page.locator("header [data-ownership-line]").waitFor({ timeout: 20_000 });
    await page.waitForTimeout(350);
    const body = await page.locator("main").innerText();
    check(body.includes(w.numberAlert) && body.includes(w.refresh) && body.includes(w.alertMe) && body.includes(w.showOverview), `${tag} watch number/alert localized`);
    await checkNoWrappedButtons(page, `${tag} watch number/alert`);
    await page.screenshot({ path: join(out, `watch-number-alert-${tag}.png`), animations: "disabled", caret: "hide" });

    await page.goto(`${after}/extension/-pages/index`);
    await page.locator("main section button").first().waitFor({ timeout: 20_000 });
    await page.waitForTimeout(350);
    await page.screenshot({ path: join(out, `index-alert-${tag}.png`), animations: "disabled", caret: "hide" });

    await page.goto(`${after}/overview`);
    await page.locator(`[data-extension-card="-pages/${WATCH.number.g}"]`).waitFor({ timeout: 20_000 });
    await page.locator(`[data-extension-card="-pages/${WATCH.trend.g}"]`).waitFor({ timeout: 20_000 });
    await page.locator("[data-overview-cards]").scrollIntoViewIfNeeded();
    await page.waitForTimeout(450);
    const ov = await page.locator("[data-overview-cards]").innerText();
    check(ov.includes(w.numberTitle) && ov.includes(w.trendTitle), `${tag} overview localized cards`);
    await page.screenshot({ path: join(out, `overview-alert-${tag}.png`), animations: "disabled", caret: "hide" });

    check(s.errors.length === 0, `${tag}: no page errors`, s.errors.join(" | "));
    await s.ctx.close();
  }
} finally {
  writeFileSync(join(out, "pixel-diff-watch.json"), JSON.stringify(diffs, null, 2));
  await setMode("watch").catch(() => {});
  await browser.close();
  clearTimeout(watchdog);
}
const totalCompared = diffs.length;
const totalDiffering = diffs.reduce((acc, d) => acc + Math.max(0, d.differing), 0);
console.log(`PIXEL DIFF SUMMARY: ${totalCompared} images compared, ${totalDiffering} differing pixels`);
console.log(failures.length ? `VERDICT FAIL dashboards-watch: ${failures.length} check(s) failed` : "VERDICT PASS dashboards-watch");
process.exit(failures.length ? 1 : 0);
