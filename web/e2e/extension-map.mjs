// UI version 12 (#14730): vector `map` component on an extension nav_page, compact overview_card,
// and assistant inline tool_views in a real browser against mock_hub.py.
//
// Start the mock with MOCK_EXTENSIONS=1 MOCK_EXTENSION_MAP=1 and Vite preview pointing at it:
//   node web/e2e/extension-map.mjs <loopback web URL> <artifact directory> [Chromium executable]
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const [base, shots, executable] = process.argv.slice(2);
if (!base || !shots || !/^https?:\/\/(127\.0\.0\.1|localhost)(:\d+)?\/?$/.test(base)) {
  console.error("usage: node web/e2e/extension-map.mjs <loopback web URL> <artifact directory> [Chromium executable]");
  process.exit(2);
}
mkdirSync(shots, { recursive: true });
const watchdog = setTimeout(() => {
  console.error("RESULT: extension-map timed out (180s)");
  process.exit(124);
}, 180_000);

const root = base.replace(/\/$/, "");
const PAGE = "/extension/example-maps/route-planner";
const CHAT = "/persona/b2e4d6f8-0000-4000-8000-000000000012";
const results = [];
const check = (name, ok, detail) => {
  results.push({ name, ok: Boolean(ok), detail });
  console.log(`${ok ? "ok  " : "FAIL"} ${name}${detail === undefined ? "" : ` ${JSON.stringify(detail)}`}`);
};

const browser = await chromium.launch(executable ? { executablePath: executable } : { channel: "chrome" });

async function openPage({ width, height, theme, at = PAGE, waitForSelector = "[data-map-stage]" }) {
  const context = await browser.newContext({ viewport: { width, height }, colorScheme: theme, deviceScaleFactor: 2, serviceWorkers: "block" });
  await context.addInitScript(([t]) => {
    localStorage.setItem("persona.theme", t);
    localStorage.setItem("persona.language", "en");
  }, [theme]);
  await context.route("**/*", (route) => (new URL(route.request().url()).origin === new URL(root).origin ? route.continue() : route.abort()));
  const p = await context.newPage();
  await p.goto(`${root}/?token=mock`);
  await p.waitForFunction(() => localStorage.getItem("persona.token"));
  await p.goto(root + at);
  await p.locator(waitForSelector).first().waitFor({ state: "visible", timeout: 30_000 });
  await p.waitForFunction(() => document.getAnimations().every((a) => a.playState !== "running" || a.effect?.getTiming().iterations === Infinity));
  return p;
}

let failed = false;
try {
  // Desktop Light & Dark: nav_page interactions + full screen + push + back + overview card + inline chat tool_views
  for (const theme of ["light", "dark"]) {
    const p = await openPage({ width: 1440, height: 900, theme });

    const markerCount = await p.locator("[data-map-marker]").count();
    const routeLines = await p.locator("polyline[data-map-route-line]").count();
    const linkHref = await p.locator("[data-map-link]").first().getAttribute("href");
    const resetInitiallyDisabled = await p.locator('button[aria-label="Reset view"]').isDisabled();
    check(`1440 ${theme}: nav_page renders 3 markers, 2 routes, reset disabled`, markerCount === 3 && routeLines === 2 && resetInitiallyDisabled, { markerCount, routeLines, resetInitiallyDisabled });
    check(`1440 ${theme}: HTTPS link preserved`, linkHref === "https://maps.example.com/route?id=commute", linkHref);
    await p.screenshot({ path: join(shots, `map-page-1440-${theme}.png`) });

    // Switch route tabs
    await p.locator('[data-map-route="r-alt"]').click();
    const altSelected = await p.locator('[data-map-route-line="r-alt"]').getAttribute("data-selected");
    check(`1440 ${theme}: switching route selects r-alt polyline`, altSelected === "true", altSelected);
    await p.locator('[data-map-route="r-fastest"]').click();

    // Zoom in enables Reset view, clicking Reset view disables it
    await p.locator('button[aria-label="Zoom in"]').click();
    const resetEnabledAfterZoom = !(await p.locator('button[aria-label="Reset view"]').isDisabled());
    check(`1440 ${theme}: Zoom in enables Reset view`, resetEnabledAfterZoom);
    await p.locator('button[aria-label="Reset view"]').click();
    const resetDisabledAgain = await p.locator('button[aria-label="Reset view"]').isDisabled();
    check(`1440 ${theme}: Reset view re-fits and disables itself`, resetDisabledAgain);

    // Open Full screen -> capture screenshot -> click actionable stop -> verify pushed view & back
    await p.locator('button[aria-label="Full screen"]').click();
    const dialogVisible = await p.locator('[role="dialog"]').waitFor({ state: "visible", timeout: 10_000 }).then(() => true, () => false);
    check(`1440 ${theme}: Full screen opens dialog`, dialogVisible);
    await p.screenshot({ path: join(shots, `map-full-1440-${theme}.png`) });
    await p.locator('[role="dialog"] [data-map-stop="waypoint"]').click();
    const pushedOk = await p.waitForFunction(() => document.body.textContent?.includes("Stop details for Coffee Stop"), null, { timeout: 15_000 }).then(() => true, () => false);
    check(`1440 ${theme}: clicking stop in Full screen pushes stop detail page`, pushedOk);
    await p.goBack();
    const backOk = await p.locator("[data-map-stage]").waitFor({ state: "visible", timeout: 15_000 }).then(() => true, () => false);
    const backDialogCount = await p.locator('[role="dialog"]').count();
    check(`1440 ${theme}: Back from stop detail returns to map page without modal`, backOk && backDialogCount === 0, { backOk, backDialogCount });
    await p.context().close();

    // Overview card compact preview -> scroll into view for screenshot -> opens Full screen on click -> Escape returns focus
    const ov = await openPage({ width: 1440, height: 900, theme, at: "/overview", waitForSelector: ".map-preview" });
    await ov.locator(".map-preview").first().scrollIntoViewIfNeeded();
    const previewH = await ov.locator(".map-preview").first().evaluate((el) => Math.round(el.getBoundingClientRect().height));
    check(`1440 ${theme}: overview card compact preview is ~200px tall`, Math.abs(previewH - 200) <= 4, previewH);
    await ov.screenshot({ path: join(shots, `map-overview-1440-${theme}.png`) });
    await ov.locator(".map-preview").first().click();
    const ovDialog = await ov.locator('[role="dialog"]').waitFor({ state: "visible", timeout: 10_000 }).then(() => true, () => false);
    check(`1440 ${theme}: overview card preview opens Full screen`, ovDialog);
    await ov.keyboard.press("Escape");
    const closed = await ov.locator('[role="dialog"]').count();
    check(`1440 ${theme}: Escape closes overview Full screen`, closed === 0);
    await ov.context().close();

    // Chat inline tool_views between work fold and final reply
    const chat = await openPage({ width: 1440, height: 900, theme, at: CHAT, waitForSelector: '[data-tool-views]' });
    const chatPreviewCount = await chat.locator('[data-tool-views] .map-preview').count();
    const chatText = await chat.locator("body").textContent();
    check(`1440 ${theme}: chat inline tool_views renders map and reply`, chatPreviewCount >= 1 && chatText.includes("Commute Options") && chatText.includes("Fastest drive takes 23 min"), { chatPreviewCount });
    await chat.screenshot({ path: join(shots, `map-chat-1440-${theme}.png`) });
    await chat.locator('[data-tool-views] .map-preview').first().click();
    const chatDialog = await chat.locator('[role="dialog"]').waitFor({ state: "visible", timeout: 10_000 }).then(() => true, () => false);
    check(`1440 ${theme}: chat inline map preview opens Full screen`, chatDialog);
    await chat.screenshot({ path: join(shots, `map-chat-full-1440-${theme}.png`) });
    await chat.keyboard.press("Escape");
    await chat.context().close();
  }

  // Mobile 390x844 Light & Dark: nav_page and chat inline tool_views layout checks (no horizontal overflow)
  for (const theme of ["light", "dark"]) {
    const mp = await openPage({ width: 390, height: 844, theme });
    const mBox = await mp.locator("[data-map-stage]").boundingBox();
    const overflow = await mp.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1);
    check(`390 ${theme}: mobile stage fits viewport without overflow`, Boolean(mBox && mBox.height >= 320 && mBox.height <= 900 && !overflow), { mBox, overflow });
    await mp.screenshot({ path: join(shots, `map-page-390-${theme}.png`) });
    await mp.context().close();

    const mchat = await openPage({ width: 390, height: 844, theme, at: CHAT, waitForSelector: '[data-tool-views]' });
    const chatOverflow = await mchat.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1);
    check(`390 ${theme}: mobile chat inline map fits without overflow`, !chatOverflow, chatOverflow);
    await mchat.screenshot({ path: join(shots, `map-chat-390-${theme}.png`) });
    await mchat.context().close();
  }
} catch (e) {
  failed = true;
  console.error(e);
} finally {
  await browser.close();
  clearTimeout(watchdog);
}

writeFileSync(join(shots, "results.json"), JSON.stringify(results, null, 2));
const bad = results.filter((r) => !r.ok);
console.log(`RESULT: ${failed || bad.length ? "FAIL" : "PASS"} (${results.length - bad.length}/${results.length} checks)`);
process.exit(failed || bad.length ? 1 : 0);
