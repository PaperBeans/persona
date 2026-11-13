// One display of a multi-display Mac on the live screen (#5685, #5729, docs/extensions/computer-use.md "Displays"), in a real
// browser against the mock hub's two-display Screen Sharing:
//   PORT=<port> MOCK_COMPUTER=ready MOCK_SCREEN=fake MOCK_SCREEN_DISPLAYS=2:node python3 scripts/mock/mock_hub.py
//   (cd web && PERSONA_HUB_URL=http://127.0.0.1:<port> npx vite --port <web port>)
//   node web/e2e/screen-displays.mjs <web URL> <mock hub URL> [--shots <dir>] [--executable <chromium>]
// The window is 1281 × 721, so the box that shows a display has a fractional left edge. Checks: noVNC draws the chosen
// display filling that box; a click in its middle reaches the Mac at the middle of display 2; a drag out over the
// fractional edge and one out over the top edge end on display 2's edges, with no page error; a new page for the same Mac
// opens on display 2. Ends with one line: PASS, FAIL or TIMEOUT.
import { mkdir } from "node:fs/promises";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const flag = (name) => {
  const i = args.indexOf(name);
  return i < 0 ? undefined : args.splice(i, 2)[1];
};
const shots = flag("--shots");
const executable = flag("--executable");
const [web, hub] = args;
if (!web || !hub) {
  console.log("usage: node web/e2e/screen-displays.mjs <web URL> <mock hub URL> [--shots <dir>] [--executable <chromium>]");
  process.exit(2);
}
if (shots) await mkdir(shots, { recursive: true });

const deadline = setTimeout(() => {
  console.log("TIMEOUT screen displays: no verdict in 120 s");
  process.exit(1);
}, 120_000);
const mock = async (query = "") => (await fetch(`${hub}/mock/screen-password${query}`)).json();
const near = (a, b) => Math.abs(a - b) <= 1;
const fail = (why) => {
  throw new Error(why);
};

const browser = await chromium.launch(executable ? { executablePath: executable } : { channel: "chrome" });
try {
  const context = await browser.newContext({ viewport: { width: 1281, height: 721 }, locale: "en-US", serviceWorkers: "block" });
  const errors = [];
  const open = async () => {
    const page = await context.newPage();
    page.setDefaultTimeout(20_000);
    page.on("pageerror", (e) => errors.push(String(e)));
    await page.goto(`${web}/screen/macbook#token=mock`);
    await page.fill("input[autocomplete=username]", "me");
    await page.fill("input[type=password]", "mock-pass");
    await page.click("button[type=submit]");
    await page.waitForSelector("[data-testid=screen-displays]");
    return page;
  };
  // The box and noVNC's canvas once the canvas fills it with display 2 (the framebuffer's right half).
  const settled = (page) =>
    page.waitForFunction(() => {
      const s = document.querySelector("[data-testid=screen]");
      const c = s?.querySelector("canvas");
      if (!s?.style.width || !c) return null;
      const b = s.parentElement.getBoundingClientRect(), r = c.getBoundingClientRect();
      const ok = Math.abs(r.left - (b.left - b.width)) <= 1 && Math.abs(r.width - 2 * b.width) <= 1 && Math.abs(r.top - b.top) <= 1 && Math.abs(r.height - b.height) <= 1;
      return ok && { left: b.left, top: b.top, width: b.width, height: b.height };
    }).then((h) => h.jsonValue());

  const page = await open();
  await page.click("[data-testid=screen-displays]");
  await page.click('[role=menuitemradio]:has-text("Display 2")');
  const box = await settled(page);
  if (Number.isInteger(box.left)) fail(`the box's left edge ${box.left} is whole: the window no longer checks a fractional edge`);
  if (shots) await page.screenshot({ path: `${shots}/screen-displays-display-2.png` });

  await page.click("text=Take control");
  const mid = { x: box.left + box.width / 2, y: box.top + box.height / 2 };
  await mock("?clear=1");
  await page.mouse.click(mid.x, mid.y);
  await page.waitForTimeout(300);
  const clicks = (await mock()).clicks;
  const click = clicks.find((c) => c.down === true);
  if (!click || !near(click.x, 720) || !near(click.y, 150)) fail(`a click in the middle reached ${JSON.stringify(clicks)}, not (720, 150)`);

  for (const [edge, to, want] of [["left", { x: box.left - 60, y: mid.y }, (u) => u.x === 480], ["top", { x: mid.x, y: box.top - 30 }, (u) => u.y === 0]]) {
    await mock("?clear=1");
    await page.mouse.move(mid.x, mid.y);
    await page.mouse.down();
    await page.mouse.move(to.x, to.y, { steps: 4 });
    await page.mouse.up();
    await page.waitForTimeout(300);
    const up = (await mock()).clicks.find((c) => c.down === false);
    if (!up || !want(up) || up.x < 480) fail(`a drag out over the ${edge} edge was released at ${JSON.stringify(up)}, not on display 2's ${edge} edge`);
  }
  await page.close();

  const again = await open();
  const label = await again.getAttribute("[data-testid=screen-displays]", "aria-label");
  if (label !== "Displays: Display 2") fail(`a new page says "${label}", not Display 2`);
  await settled(again);
  if (errors.length) fail(`page errors: ${errors.slice(0, 3).join(" | ")}`);
  console.log(`PASS screen displays: box left ${box.left.toFixed(2)}, click (${click.x}, ${click.y}), drags kept on display 2, a new page reopened it`);
} catch (e) {
  console.log(`FAIL screen displays: ${e instanceof Error ? e.message : e}`);
  process.exitCode = 1;
} finally {
  clearTimeout(deadline);
  await browser.close();
}
