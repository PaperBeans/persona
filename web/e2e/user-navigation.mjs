// Browser acceptance of user-input navigation in the actual app, not an isolated component.
// Usage: node web/e2e/user-navigation.mjs <baseUrl> <artifactDir>
// The caller owns the already-running mock hub and web server. This runner starts neither.
import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { chromium } from "playwright-core";

const [address, artifactDir, ...extra] = process.argv.slice(2);
if (!address || !artifactDir || extra.length) {
  console.error("Usage: node web/e2e/user-navigation.mjs <baseUrl> <artifactDir>");
  process.exit(2);
}
const base = new URL(address);
assert.ok(["127.0.0.1", "localhost", "[::1]"].includes(base.hostname), "Only a loopback web server is allowed");
assert.ok(["http:", "https:"].includes(base.protocol) && !base.username && !base.password, "Use a plain loopback URL without credentials");
assert.equal(base.pathname, "/", "Use the web server's origin, without a route prefix");
assert.ok(!base.search && !base.hash, "The URL must not carry a token or query");
const output = resolve(artifactDir);
await mkdir(output, { recursive: true });

const CHAT = "[data-chat-scroll]";
const RAIL = "[data-nav-rail]";
const PREVIEW = "[data-nav-preview], [data-nav-rail-preview]";
const MID = "demo";
const SID = "navigation-demo";
const WIDE = { width: 1440, height: 900 };
const NARROW = { width: 390, height: 844 };
const INPUTS = 16;
const MANY_INPUTS = 240;
const TIMEOUT_MS = 15_000;
const results = {
  fixture: "Existing mock hub, actual web app, browser-intercepted invented user inputs and answers",
  status: "running", browser: null, checks: [], screenshots: [], runs: [], errors: [],
};
let browser;
let timedOut = false;
const deadline = setTimeout(() => {
  timedOut = true;
  void browser?.close();
}, 4 * 60_000);

function input(index) {
  // Deliberately statements and instructions, not only sentences ending with a question mark.
  const action = ["Compare the two layouts", "Keep the heading on one line", "Explain the trade-off", "Summarize the next steps"][index % 4];
  return `Navigation input ${String(index + 1).padStart(3, "0")}: ${action}.`;
}
function answer(index) {
  return `Answer ${String(index + 1).padStart(3, "0")}: The synthetic example is ready.`;
}
function transcript(count) {
  const start = Date.UTC(2026, 0, 1);
  return Array.from({ length: count }, (_, index) => {
    const seq = index * 2 + 1;
    // Keep enough content below the last anchor for it to reach the top on a jump.
    const paragraphs = count > INPUTS ? (index === count - 1 ? 14 : 2) : 7;
    return [
      { id: `nav-user-${index}#0`, seq, ts: new Date(start + seq * 1000).toISOString(), role: "user", kind: "text", text: input(index) },
      { id: `nav-answer-${index}#0`, seq: seq + 1, ts: new Date(start + (seq + 1) * 1000).toISOString(), role: "assistant", kind: "text",
        text: `**${answer(index)}**\n\n${Array.from({ length: paragraphs }, (_, n) => `Example paragraph ${n + 1}. This invented response supplies enough body content to scroll the actual conversation. No account, private message, external link or real project is used.`).join("\n\n")}` },
    ];
  }).flat();
}
const tick = (page, index) => page.locator(`${RAIL} button[data-tick="${index}"]`);
async function check(name, fn) {
  const started = Date.now();
  try {
    const detail = await fn();
    results.checks.push({ name, status: "pass", elapsedMs: Date.now() - started, ...(detail === undefined ? {} : { detail }) });
    console.log(`PASS ${name}`);
  } catch (error) {
    results.checks.push({ name, status: "fail", elapsedMs: Date.now() - started, error: error.message });
    throw error;
  }
}
async function healthy(page, run) {
  assert.equal(await page.locator("vite-error-overlay").count(), 0, "No Vite error overlay may cover the app");
  assert.deepEqual(run.pageErrors, [], "No uncaught page errors");
  assert.deepEqual(run.consoleErrors, [], "No browser console errors");
  assert.deepEqual(run.blockedExternal, [], "No request may leave the fixture origin");
  assert.deepEqual(run.failedReads, [], "No application or asset read may fail");
}
async function screenshot(page, run, state) {
  await healthy(page, run);
  await page.evaluate(() => document.fonts.ready);
  const name = `navigation-${run.name}-${state}.png`;
  await page.screenshot({ path: join(output, name), fullPage: false });
  results.screenshots.push(name);
}
async function geometry(page) {
  return page.evaluate(([chat, rail]) => {
    const sc = document.querySelector(chat), nav = document.querySelector(rail);
    if (!sc || !nav) return null;
    const rect = (el) => { const r = el.getBoundingClientRect(); return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, width: r.width, height: r.height }; };
    const content = sc.querySelector("[data-chat-content]");
    const sidebar = document.querySelector("[data-sidebar-column]");
    return { chat: rect(sc), rail: rect(nav), content: rect(content), sidebar: rect(sidebar), overflow: document.documentElement.scrollWidth - innerWidth };
  }, [CHAT, RAIL]);
}
async function leftGutter(page) {
  const box = await geometry(page);
  assert.ok(box, "The body scroller and rail exist");
  assert.ok(box.rail.width > 0 && box.rail.height > 0, "The rail has a real hit strip");
  assert.ok(box.rail.left > box.chat.left && box.rail.left > box.sidebar.right, "The rail starts inside the chat pane after the app sidebar");
  assert.ok(box.content.left - box.rail.right >= 12, "The rail fits clear of the centered transcript");
  assert.ok(box.rail.left - box.chat.left <= 16, "The rail follows the left edge of the pane");
  assert.ok(box.overflow <= 1, "The page does not overflow horizontally");
  return box;
}
async function waitForCurrent(page, index) {
  await page.waitForFunction(([rail, i]) => {
    const all = [...document.querySelectorAll(`${rail} [aria-current=true]`)];
    return all.length === 1 && all[0].getAttribute("data-tick") === String(i);
  }, [RAIL, index]);
}
async function landed(page, index) {
  // Check the real DOM anchor as well as aria-current: an optimistic highlight alone is not a jump.
  await page.waitForFunction(([chat, words]) => {
    const sc = document.querySelector(chat);
    const bubble = sc && [...sc.querySelectorAll("[data-user-bubble]")].find((el) => el.textContent.includes(words));
    const row = bubble?.closest("[data-line]")?.firstElementChild ?? bubble;
    if (!row || !sc) return false;
    const top = row.getBoundingClientRect().top - sc.getBoundingClientRect().top;
    const atEnd = sc.scrollHeight - sc.scrollTop - sc.clientHeight < 2;
    return top >= -2 && (top <= 48 || (atEnd && top < sc.clientHeight));
  }, [CHAT, input(index)]);
  await waitForCurrent(page, index);
}
async function selectedIndex(page) {
  return page.evaluate((rail) => {
    const nav = document.querySelector(rail);
    if (document.activeElement !== nav) return null;
    const activeId = nav.getAttribute("aria-activedescendant");
    const described = nav.getAttribute("aria-describedby");
    const selected = activeId ? document.getElementById(activeId) : [...nav.querySelectorAll("button[data-tick]")].find((el) => described && el.getAttribute("aria-describedby") === described);
    return selected ? Number(selected.getAttribute("data-tick")) : null;
  }, RAIL);
}
async function waitForSelection(page, index) {
  await page.waitForFunction(([rail, i]) => {
    const nav = document.querySelector(rail);
    if (document.activeElement !== nav) return false;
    const activeId = nav.getAttribute("aria-activedescendant");
    const described = nav.getAttribute("aria-describedby");
    const selected = activeId ? document.getElementById(activeId) : [...nav.querySelectorAll("button[data-tick]")].find((el) => described && el.getAttribute("aria-describedby") === described);
    return selected?.getAttribute("data-tick") === String(i);
  }, [RAIL, index]);
  const label = await tick(page, index).getAttribute("aria-label");
  assert.ok(label?.includes(input(index)), "The screen-reader label identifies the actual user input");
  const description = await page.locator(RAIL).evaluate((nav) => document.getElementById(nav.getAttribute("aria-describedby"))?.textContent ?? "");
  assert.ok(description.includes(input(index)) && description.includes(answer(index)), "The focused edge's accessible description names the selected input and answer");
}
async function preview(page, index) {
  await page.waitForFunction(([selector, question, reply]) => {
    const card = document.querySelector(selector);
    return !!card && getComputedStyle(card).visibility !== "hidden" && card.textContent.includes(question) && card.textContent.includes(reply);
  }, [PREVIEW, input(index), answer(index)]);
  const card = page.locator(PREVIEW);
  const data = await card.evaluate((el) => {
    const r = el.getBoundingClientRect();
    return { text: el.textContent, left: r.left, right: r.right, top: r.top, bottom: r.bottom, viewportWidth: innerWidth, viewportHeight: innerHeight };
  });
  assert.ok(data.text.length < 420, "The answer preview is an excerpt, not the complete response");
  assert.ok(data.left >= 0 && data.right <= data.viewportWidth + 1 && data.top >= 0 && data.bottom <= data.viewportHeight + 1, "The hover preview stays inside the viewport");
}
async function previewStyle(page, index) {
  await page.waitForFunction(([rail, index]) => {
    const mark = document.querySelector(`${rail} button[data-tick="${index}"] [data-nav-mark]`);
    return mark && Math.abs(mark.getBoundingClientRect().width - parseFloat(mark.style.width)) < 0.1;
  }, [RAIL, index]);
  const style = await page.locator(PREVIEW).evaluate((card) => {
    const surface = card.querySelector("[data-nav-preview-surface]");
    const title = card.querySelector("[data-nav-preview-title]");
    const excerpt = card.querySelector("[data-nav-preview-excerpt]");
    const s = getComputedStyle(surface), h = getComputedStyle(title), p = getComputedStyle(excerpt);
    return { radius: s.borderRadius, padding: s.paddingTop, border: s.borderTopWidth, shadow: s.boxShadow,
      surface: s.backgroundColor, title: h.color, body: p.color, weight: h.fontWeight,
      titleWhitespace: h.whiteSpace, lines: p.webkitLineClamp };
  });
  assert.equal(style.radius, "16px");
  assert.equal(style.padding, "16px");
  assert.equal(style.border, "1px");
  assert.notEqual(style.shadow, "none");
  assert.ok(Number(style.weight) >= 600);
  assert.equal(style.titleWhitespace, "nowrap");
  assert.equal(style.lines, "3");
  const box = await page.locator(PREVIEW).boundingBox(), mark = await tick(page, index).boundingBox();
  assert.ok(Math.abs(box.x - (mark.x + mark.width)) <= 1, "The card's hover bridge starts at the right edge of the tick");
  assert.ok(Math.abs(box.y + box.height / 2 - mark.y - mark.height / 2) < 2, "The card aligns vertically with its input");
  const profile = await page.locator(`${RAIL} [data-nav-mark]`).evaluateAll((marks) => marks.map((mark) => ({ width: parseFloat(mark.style.width), opacity: parseFloat(mark.style.opacity) })));
  assert.ok(profile[index - 1].width > profile[index - 2].width && profile[index - 2].width > profile[index - 3].width, "Marks shorten smoothly away from hover");
  assert.ok(profile[index - 3].width < 10 && profile[index - 3].opacity < profile[index - 1].opacity, "Far marks are shorter and lighter");
  return style;
}

async function idleMarks(page) {
  assert.equal(await page.locator(PREVIEW).count(), 0, "Idle widths are checked without a preview");
  await page.waitForFunction((rail) => [...document.querySelectorAll(`${rail} [data-nav-mark]`)].every((mark) => {
    const box = mark.getBoundingClientRect();
    return Math.abs(box.width - parseFloat(mark.style.width)) < 0.1
      && Math.abs(box.height - parseFloat(mark.style.height)) < 0.1
      && Math.abs(Number(getComputedStyle(mark).opacity) - Number(mark.style.opacity)) < 0.001;
  }), RAIL);
  const marks = await page.locator(`${RAIL} [data-nav-mark]`).evaluateAll((nodes) => nodes.map((mark) => ({
    width: mark.getBoundingClientRect().width, height: mark.getBoundingClientRect().height,
    current: mark.dataset.navMark === "current", color: getComputedStyle(mark).backgroundColor,
    opacity: getComputedStyle(mark).opacity,
  })));
  assert.ok(marks.length > 1, "Compare multiple rendered marks");
  assert.ok(marks.filter((mark) => !mark.current).every((mark) => Math.abs(mark.width - 6) < 0.1 && Math.abs(mark.height - 1) < 0.1), "Idle marks stay short and thin");
  const current = marks.find((mark) => mark.current), other = marks.find((mark) => !mark.current);
  assert.ok(current && other && current.color !== other.color && Number(current.opacity) > 0.999 && current.width > other.width && current.height > other.height, "The current mark has stronger color, width and thickness");
  return { marks: marks.length, widths: [...new Set(marks.map((mark) => mark.width))], current };
}

async function armExitProbe(page) {
  await page.evaluate(([rail, preview]) => {
    const nav = document.querySelector(rail);
    const sample = { leftAt: null, hiddenAt: null };
    window.__navExitSample = sample;
    const left = (event) => { if (!nav.contains(event.relatedTarget)) sample.leftAt = performance.now(); };
    const observer = new MutationObserver(() => {
      if (sample.leftAt !== null && !document.querySelector(preview)) sample.hiddenAt ??= performance.now();
    });
    nav.addEventListener("mouseout", left);
    observer.observe(nav, { childList: true, subtree: true });
    window.__navExitCleanup = () => { nav.removeEventListener("mouseout", left); observer.disconnect(); };
  }, [RAIL, PREVIEW]);
}

async function bodyWheel(page, delta) {
  const box = await page.locator(CHAT).boundingBox();
  assert.ok(box, "The chat is visible");
  await page.mouse.move(box.x + box.width * 0.7, box.y + box.height * 0.5);
  const before = await page.locator(CHAT).evaluate((el) => el.scrollTop);
  await page.mouse.wheel(0, delta);
  await page.waitForFunction(([chat, top]) => Math.abs((document.querySelector(chat)?.scrollTop ?? top) - top) > 5, [CHAT, before]);
}
async function settleScroll(page) {
  await page.locator(CHAT).evaluate(async (el) => {
    let previous = el.scrollTop, still = 0;
    const end = performance.now() + 2500;
    while (performance.now() < end && still < 4) {
      await new Promise((done) => setTimeout(done, 50));
      const now = el.scrollTop;
      still = Math.abs(now - previous) < 0.5 ? still + 1 : 0;
      previous = now;
    }
    if (still < 4) throw new Error("The body scroll did not settle");
  });
}
async function quietAndFocus(page, run) {
  await page.evaluate(() => document.activeElement?.blur());
  await bodyWheel(page, 80);
  await settleScroll(page);
  // Let the former 2s idle delay plus its fade elapse.
  await page.waitForTimeout(2400);
  const opacity = await page.locator(RAIL).evaluate((el) => Number(getComputedStyle(el).opacity));
  assert.equal(opacity, 1, "The rail remains visible after scrolling settles");
  await screenshot(page, run, "idle");
  // Use actual Tab traversal, not focus() on a possibly negative-tabindex element.
  await page.evaluate(() => document.activeElement?.blur());
  let reached = false;
  const tabBudget = await page.evaluate(() => document.querySelectorAll("button, a[href], input, textarea, select, [tabindex]").length + 10);
  for (let n = 0; n < tabBudget; n++) {
    await page.keyboard.press("Tab");
    reached = await page.evaluate((rail) => !!document.activeElement?.closest(rail), RAIL);
    if (reached) break;
  }
  assert.ok(reached, "Tab reaches the navigation rail");
  assert.ok(await page.locator(PREVIEW).count(), "Tab focus opens the preview");
  const outline = await page.locator(`${RAIL} button[aria-describedby]`).evaluate((button) => getComputedStyle(button).outlineStyle);
  assert.equal(outline, "solid", "Keyboard focus is visible on the selected tick");
  const focusedMark = await page.locator(`${RAIL} [data-nav-mark='hover'], ${RAIL} [data-nav-mark='current-hover']`).evaluate(el => parseFloat(el.style.width));
  assert.ok(focusedMark >= 22, "Keyboard focus keeps the interactive expansion");
  await screenshot(page, run, "edge-focus");
  return { idleAfterSettledScrollMs: 2400, opacity, tabReachable: reached };
}
async function fixture(context, run, count) {
  const items = transcript(count);
  const session = { machine_id: MID, session_id: SID, title: "Synthetic navigation conversation", agent: "claude", status: "idle", state: "idle", updated_at: items.at(-1).ts, last_text: answer(count - 1), cwd: "/demo/project", capabilities: { read: true, send: true, terminal: false } };
  const machine = { id: MID, name: "Demo computer", online: true };
  await context.addInitScript(({ theme }) => {
    localStorage.setItem("persona.token", "synthetic-navigation-token");
    localStorage.setItem("persona.language", "en");
    localStorage.setItem("persona.theme", theme);
  }, { theme: run.theme });
  await context.route("**/*", async (route) => {
    const request = route.request(), url = new URL(request.url());
    if (url.origin !== base.origin) {
      run.blockedExternal.push({ method: request.method(), path: url.pathname });
      return route.abort("blockedbyclient");
    }
    if (!["GET", "HEAD"].includes(request.method())) {
      run.interceptedWrites.push({ method: request.method(), path: url.pathname });
      assert.ok(url.pathname.startsWith("/api/"), "Unexpected non-API write");
      return route.fulfill({ json: { ok: true } });
    }
    if (!url.pathname.startsWith("/api/")) {
      assert.ok(!url.pathname.startsWith("/mock/"), "Never invoke mutable mock control routes");
      return route.continue();
    }
    const path = url.pathname.replace(/^\/api\/v1/, "");
    run.reads.push({ path, query: url.search });
    let json;
    if (path.endsWith("/items")) {
      const after = Number(url.searchParams.get("after_seq") ?? 0);
      json = { items: after ? items.filter((item) => item.seq > after) : items, has_more: false, more_after: false, history: "synthetic-nav-history", from_seq: 1, to_seq: items.at(-1).seq };
    } else if (path === "/sessions") {
      json = { sessions: [session], total: 1, has_more: false, revision: "synthetic-nav-revision" };
    } else if (path === `/sessions/${MID}/${SID}/info`) {
      json = session;
    } else {
      const empty = {
        "/machines": { machines: [machine] }, "/assistant": { machine_id: MID, conversations: [] },
        "/interactions": { interactions: [] }, "/todos": { todos: [] }, "/mutes": { spaces: [], sessions: [], allowed: [] },
        "/pins": { spaces: [], sessions: [], sections: [] }, "/agents/sessions": { sessions: [] }, "/agents/prefs": {},
        "/managers": { managers: [], default_id: "" }, "/personas": { personas: [] }, "/assistant/personas": { personas: [] },
        "/workspaces": { workspaces: [] }, "/extensions": { extensions: [] },
      };
      json = empty[path] ?? {};
    }
    return route.fulfill({ json });
  });
  await context.routeWebSocket(/\/api\/v1\/ws(?:\?|$)/, (socket) => {
    const url = new URL(socket.url());
    assert.equal(url.host, base.host, "The app socket stays on the fixture origin");
    const upstream = socket.connectToServer();
    upstream.onMessage((message) => {
      const event = JSON.parse(String(message));
      if (event.type !== "hello") return; // No unrelated mock sessions, messages or personal-looking fixture text.
      assert.equal(event.server_version, "mock", "The socket must also come from the existing mock hub");
      run.mockHellos++;
      socket.send(JSON.stringify({ type: "hello", server_version: "mock", features: [], protocol: 1, hub_name: "Demo hub" }));
      socket.send(JSON.stringify({ type: "machines", machines: [machine] }));
      socket.send(JSON.stringify({ type: "sessions", machine_id: MID, sessions: [session] }));
    });
    socket.onMessage((message) => {
      const event = JSON.parse(String(message));
      run.socketClientTypes.push(event.type);
      if (event.type === "app_ping") socket.send(JSON.stringify({ type: "pong", id: event.id }));
      // Deliberately do not forward client_hello, subscribe, seen, drafts or other client actions upstream.
    });
  });
}
async function open(browser, name, theme, viewport, count, reducedMotion = "no-preference") {
  const run = { name, theme, viewport, inputs: count, mockHellos: 0, reads: [], interceptedWrites: [], socketClientTypes: [], blockedExternal: [], failedReads: [], pageErrors: [], consoleErrors: [] };
  results.runs.push(run);
  const context = await browser.newContext({ viewport, colorScheme: theme, locale: "en-US", hasTouch: false, reducedMotion, serviceWorkers: "block" });
  await fixture(context, run, count);
  const page = await context.newPage();
  page.setDefaultTimeout(TIMEOUT_MS);
  page.setDefaultNavigationTimeout(TIMEOUT_MS);
  page.on("pageerror", (error) => run.pageErrors.push(error.message));
  page.on("console", (message) => { if (message.type() === "error") run.consoleErrors.push(message.text()); });
  page.on("response", (response) => { if (response.status() >= 400) run.failedReads.push({ path: new URL(response.url()).pathname, status: response.status() }); });
  await page.goto(new URL(`/s/${MID}/${SID}`, base).href);
  await page.locator(`${CHAT} [data-user-bubble]`).getByText(input(count - 1), { exact: true }).waitFor();
  // Long chats keep anchors for earlier exchanges and mount rich bubbles as they approach the viewport.
  await page.waitForFunction(([chat, count]) => document.querySelector(chat)?.querySelectorAll('[data-line^="seg:nav-user-"]').length === count, [CHAT, count]);
  await page.waitForFunction((dark) => document.documentElement.classList.contains("dark") === dark, theme === "dark");
  if (viewport.width === WIDE.width) {
    try {
      await page.locator(RAIL).waitFor();
    } catch (error) {
      await page.screenshot({ path: join(output, `${name}-launch-failure.png`) });
      const diagnostic = await page.locator(CHAT).evaluate((sc) => ({ height: sc.clientHeight, width: sc.clientWidth, fine: matchMedia("(any-pointer: fine)").matches, lines: sc.querySelectorAll("[data-line]").length }));
      throw new Error(`${error.message}\n${JSON.stringify(diagnostic)}`);
    }
    assert.ok((await page.locator(RAIL).getAttribute("aria-label"))?.trim(), "The navigation region has a screen-reader label");
  }
  return { context, page, run };
}
async function standard(browser, theme, size) {
  const name = `${size}-${theme}`;
  const { context, page, run } = await open(browser, name, theme, size === "wide" ? WIDE : NARROW, size === "wide" ? INPUTS : INPUTS - 1);
  try {
    if (size === "narrow") {
      await check(`${name}: the narrow conversation scrolls without a rail or extra padding`, async () => {
        await bodyWheel(page, -420);
        await settleScroll(page);
        assert.equal(await page.locator(RAIL).count(), 0);
        const layout = await page.locator(CHAT).evaluate((sc) => {
          const { paddingLeft, paddingRight } = getComputedStyle(sc.querySelector("[data-chat-content]"));
          return { paddingLeft, paddingRight, width: innerWidth, pageWidth: document.documentElement.scrollWidth };
        });
        assert.equal(layout.paddingLeft, layout.paddingRight);
        assert.equal(layout.width, layout.pageWidth);
        await screenshot(page, run, "without-rail");
        await page.evaluate(() => { document.documentElement.dataset.textSize = "large"; });
        await settleScroll(page);
        assert.equal(await page.locator(RAIL).count(), 0);
        await screenshot(page, run, "large-text");
        return layout;
      });
      return;
    }
    await check(`${name}: the rail is visible by default in the left gutter`, async () => {
      const box = await leftGutter(page);
      assert.equal(await page.locator(RAIL).evaluate((el) => getComputedStyle(el).opacity), "1");
      assert.equal(await page.locator(PREVIEW).count(), 0);
      await screenshot(page, run, "rest");
      return box;
    });
    await check(`${name}: body scrolling updates the current input`, async () => {
      await bodyWheel(page, -420);
      await settleScroll(page);
      const box = await leftGutter(page);
      assert.equal(await page.locator(`${RAIL} button[data-tick]`).count(), INPUTS, "Every loaded user input has a mark");
      // Independent geometry oracle for the current input; do not infer it from aria-current itself.
      const expected = await page.locator(CHAT).evaluate((sc) => {
        const threshold = sc.getBoundingClientRect().top + 40;
        const bubbles = [...sc.querySelectorAll("[data-user-bubble]")];
        let at = 0;
        bubbles.forEach((bubble, i) => { const row = bubble.closest("[data-line]")?.firstElementChild ?? bubble; if (row.getBoundingClientRect().top <= threshold) at = i; });
        return at;
      });
      await waitForCurrent(page, expected);
      const idle = await idleMarks(page);
      await screenshot(page, run, "scroll");
      return { box, currentInput: expected + 1, idle };
    });
    await check(`${name}: hover previews a user input and a short answer`, async () => {
      const left = (await tick(page, 3).locator("[data-nav-mark]").boundingBox()).x;
      await tick(page, 3).hover();
      await preview(page, 3);
      await page.waitForTimeout(2200);
      assert.ok(await page.locator(RAIL).evaluate((el) => Number(getComputedStyle(el).opacity) > 0.95), "Hover keeps the navigation visible");
      const style = await previewStyle(page, 3);
      assert.equal((await tick(page, 3).locator("[data-nav-mark]").boundingBox()).x, left, "The tick's left edge stays fixed as it expands rightward");
      const profile = await page.locator(`${RAIL} button[data-tick]`).evaluateAll((buttons) => buttons.map((button) => {
        const mark = button.querySelector("[data-nav-mark]");
        return { index: Number(button.dataset.tick), current: button.getAttribute("aria-current") === "true", width: mark.getBoundingClientRect().width };
      }));
      const current = profile.find((mark) => mark.current);
      assert.ok(current && Math.abs(current.index - 3) > 3, "The viewport-current input is distinct and well away from the hovered input");
      for (const mark of profile) {
        const distance = mark.index - 3;
        const expected = 6 + (22 - 6) * Math.exp(-(distance * distance) / (2 * 1.5 * 1.5));
        assert.ok(Math.abs(mark.width - expected) < 0.1, "Every mark, including aria-current, follows only the pointer-centered bell width");
      }
      assert.ok(current.width < 7, "The distant viewport-current mark has no independent expansion");
      await screenshot(page, run, "preview");
      await page.evaluate(() => { document.documentElement.dataset.textSize = "large"; });
      await preview(page, 3);
      await screenshot(page, run, "preview-large-text");
      await page.evaluate(() => { delete document.documentElement.dataset.textSize; });
      return { style, hoveredInput: 4, currentInput: current.index + 1, currentWidth: current.width, pointerOnlyWidths: true };
    });
    await check(`${name}: rail/card hover ownership and immediate outside dismissal`, async () => {
      const card = await page.locator(PREVIEW).boundingBox();
      await page.mouse.move(card.x + card.width / 2, card.y + card.height / 2, { steps: 40 });
      await preview(page, 3);
      await page.waitForTimeout(2200);
      await preview(page, 3);
      await screenshot(page, run, "hover-card");
      await armExitProbe(page);
      const chat = await page.locator(CHAT).boundingBox();
      await page.mouse.move(chat.x + chat.width * 0.7, chat.y + chat.height / 2);
      await page.waitForFunction((selector) => !document.querySelector(selector), PREVIEW, { timeout: 350, polling: "raf" });
      const sample = await page.evaluate(() => { window.__navExitCleanup(); return window.__navExitSample; });
      const elapsedMs = sample.hiddenAt - sample.leftAt;
      assert.ok(sample.leftAt !== null && sample.hiddenAt !== null && elapsedMs >= 0 && elapsedMs < 100, "No grace-period timer may delay the card's removal");
      assert.equal(await page.locator(RAIL).evaluate((el) => getComputedStyle(el).opacity), "1");
      const idle = await idleMarks(page);
      await screenshot(page, run, "after-exit");
      return { elapsedMs, previewPresentAfterExit: false, railRetained: true, idle };
    });
    await check(`${name}: clicking lands on the actual input`, async () => {
      await tick(page, 5).click();
      await landed(page, 5);
      await screenshot(page, run, "click");
    });
    await check(`${name}: drag between horizontal marks and release to jump`, async () => {
      const a = await tick(page, 2).boundingBox(), b = await tick(page, 3).boundingBox();
      const c = await tick(page, 9).boundingBox(), d = await tick(page, 10).boundingBox();
      assert.ok(a && b && c && d, "Drag endpoints have real geometry");
      const x = a.x + a.width / 2;
      const start = (a.y + a.height / 2 + b.y + b.height / 2) / 2;
      const end = c.y + c.height / 2 + (d.y + d.height / 2 - c.y - c.height / 2) * 0.25;
      await page.mouse.move(x, start);
      await page.mouse.down();
      try {
        await page.mouse.move(x, end, { steps: 12 });
        await preview(page, 9);
        await screenshot(page, run, "drag");
      } finally { await page.mouse.up(); }
      await landed(page, 9);
      await screenshot(page, run, "release");
    });
    await check(`${name}: Arrow/Home/End, Enter and accessible input labels`, async () => {
      await page.locator(RAIL).focus();
      await page.keyboard.press("Home");
      await waitForSelection(page, 0);
      for (let index = 1; index <= 6; index++) {
        await page.keyboard.press("ArrowDown");
        await waitForSelection(page, index);
      }
      for (const [key, index] of [["ArrowUp", 5], ["ArrowDown", 6], ["Home", 0], ["End", INPUTS - 1], ["Home", 0], ["ArrowDown", 1]]) {
        await page.keyboard.press(key);
        await waitForSelection(page, index);
        await preview(page, index);
      }
      await page.keyboard.press("Enter");
      await landed(page, 1);
      await screenshot(page, run, "keyboard");
      const writesBeforeEscape = run.interceptedWrites.length;
      await page.keyboard.press("Escape");
      await page.waitForFunction((selector) => !document.querySelector(selector), PREVIEW);
      assert.equal(run.interceptedWrites.length, writesBeforeEscape, "Escape in the navigator must not interrupt the session");
    });
    await check(`${name}: the resting rail stays visible and is reachable by Tab`, () => quietAndFocus(page, run));
    await check(`${name}: no writes reach the hub and no page or overlay errors`, async () => {
      assert.ok(run.mockHellos > 0, "The actual mock socket handshake was observed");
      assert.ok(run.reads.some((r) => r.path.endsWith("/items")), "The actual app requested the synthetic transcript");
      await healthy(page, run);
      return { mockHellos: run.mockHellos, interceptedHttpWrites: run.interceptedWrites.length, forwardedSocketClientMessages: 0 };
    });
  } finally { await context.close(); }
}
async function many(browser, theme) {
  const name = `many-${theme}`;
  const { context, page, run } = await open(browser, name, theme, WIDE, MANY_INPUTS, "reduce");
  try {
    await check(`${name}: every loaded mark remains keyboard reachable, including the earliest`, async () => {
      await bodyWheel(page, -100_000);
      await page.waitForFunction(([rail, first]) => document.querySelector(`${rail} button[data-tick="0"]`)?.getAttribute("aria-label").includes(first), [RAIL, input(0)]);
      await page.locator(RAIL).focus();
      await page.keyboard.press("Home");
      await waitForSelection(page, 0);
      await page.keyboard.press("Enter");
      await landed(page, 0);
      await screenshot(page, run, "earliest");
      const reached = [];
      let maxRenderedMarks = 0;
      for (let index = 0; index < MANY_INPUTS; index++) {
        if (index > 0) await page.keyboard.press("ArrowDown");
        await waitForSelection(page, index);
        await preview(page, index);
        reached.push(await selectedIndex(page));
        maxRenderedMarks = Math.max(maxRenderedMarks, await page.locator(`${RAIL} button[data-tick]`).count());
        if (index === Math.floor(MANY_INPUTS / 2) || index === MANY_INPUTS - 1) {
          await page.keyboard.press("Enter");
          await landed(page, index);
          await screenshot(page, run, index === MANY_INPUTS - 1 ? "latest" : "middle");
        }
      }
      assert.deepEqual(reached, Array.from({ length: MANY_INPUTS }, (_, index) => index), "Windowing must not discard the older inputs");
      const track = page.locator("[data-nav-track]");
      const trackHeight = await track.evaluate(el => el.clientHeight);
      const pitch = (await tick(page, MANY_INPUTS - 1).boundingBox()).height;
      assert.ok(pitch >= 7 && pitch <= 8);
      assert.ok(maxRenderedMarks > 28 && maxRenderedMarks <= Math.ceil(trackHeight / pitch) + 9);
      await page.keyboard.press("Home");
      await waitForSelection(page, 0);
      await page.keyboard.press("Enter");
      await landed(page, 0);
      await check(`${name}: the rail's wheel scrolls its own track`, async () => {
        const count = await page.locator(`${RAIL} button[data-tick]`).count();
        const box = await track.boundingBox();
        assert.ok(box, "The rail has a visible wheel target");
        await page.waitForFunction(() => document.querySelector("[data-nav-track]").scrollTop === 0);
        const before = await page.locator(CHAT).evaluate((sc) => sc.scrollTop);
        await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
        await page.mouse.wheel(0, 400);
        await page.waitForFunction(() => document.querySelector("[data-nav-track]").scrollTop > 100);
        const later = Number(await track.locator("button[data-tick]").first().getAttribute("data-tick")) + 6;
        await tick(page, later).hover();
        await preview(page, later);
        await page.mouse.wheel(0, -400);
        await page.waitForFunction(() => document.querySelector("[data-nav-track]").scrollTop === 0);
        await tick(page, 0).hover();
        await preview(page, 0);
        assert.equal(await page.locator(CHAT).evaluate((sc) => sc.scrollTop), before, "Wheel input on the rail does not move the transcript");
        return { renderedMarks: count, laterInput: later + 1, earliestRestored: true };
      });
      await tick(page, 0).click();
      await landed(page, 0);
      await leftGutter(page);
      await healthy(page, run);
      return { reachedInputs: reached.length, maxRenderedMarks, earliestAccessibleAfterTraversingAll: true };
    });
  } finally { await context.close(); }
}

try {
  await check("safety: the provided web server proxies an existing mock hub", async () => {
    const response = await fetch(new URL("/api/v1/health", base), { redirect: "error", signal: AbortSignal.timeout(5000) });
    assert.ok(response.ok, "The caller's web server and mock hub must already be running");
    const health = await response.json();
    assert.equal(health.version, "mock", "Refuse a production hub, even when reached through loopback Vite");
    assert.equal(health.ok, true);
    return { version: health.version, readOnlyProbe: true };
  });
  browser = await chromium.launch({ channel: "chrome", executablePath: process.env.CHROME_BIN, headless: true, ignoreDefaultArgs: ["--hide-scrollbars"] });
  results.browser = browser.version();
  for (const theme of ["light", "dark"]) {
    await standard(browser, theme, "wide");
    await standard(browser, theme, "narrow");
    await many(browser, theme);
  }
  results.status = "pass";
} catch (error) {
  results.status = timedOut ? "timeout" : "fail";
  results.errors.push(error.message);
  console.error(error.stack);
  process.exitCode = 1;
} finally {
  clearTimeout(deadline);
  await browser?.close();
  await writeFile(join(output, "results.json"), `${JSON.stringify(results, null, 2)}\n`);
  console.log(`${results.status.toUpperCase()} user-navigation: ${results.checks.filter((c) => c.status === "pass").length}/${results.checks.length} checks; results.json written`);
}
