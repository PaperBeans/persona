// Actual web-app acceptance with an invented paginated conversation. No real hub or conversation is read.
// Usage: node web/e2e/user-history-navigation.mjs <loopback origin> <artifact directory>
import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { chromium } from "playwright-core";
import { checkInterruptedJump, interruptedJumps } from "./interrupted-history-jump.mjs";

const [address, directory, count = "120", conversation = "main", initialPage = "2", textSize = "standard", mode] = process.argv.slice(2);
const base = new URL(address);
assert.ok(["127.0.0.1", "localhost", "[::1]"].includes(base.hostname));
assert.ok(!base.username && !base.password && !base.search && !base.hash && base.pathname === "/");
const output = resolve(directory); await mkdir(output, { recursive: true });
const COUNT = Number(count), MID = "example-node", SID = "synthetic-history", RAIL = "[data-nav-rail]";
assert.ok(Number.isSafeInteger(COUNT) && COUNT >= 2);
const compatibility = ["legacy", "slow", "cancel"].includes(mode);
const COMPAT_TARGET = Math.max(0, COUNT - 50);
const INITIAL_BODIES = Math.min(COUNT, Number(initialPage));
assert.ok(Number.isSafeInteger(INITIAL_BODIES) && INITIAL_BODIES >= 2);
const inputs = Array.from({ length: COUNT }, (_, i) => ({ id: `test-input-${String(i).padStart(3, "0")}#0`, seq: i * 4 + 1 }));
const items = inputs.flatMap((input, i) => [
  { ...input, role: "user", kind: "text", client_msg_id: `test-send-${i}`, queued: i % 8 === 0,
    text: `Navigation input ${i + 1}: Keep the synthetic layout readable.${i % 3 === 0 ? '\n<persona-context kind="context" title="Fixture">Invented context only.</persona-context>' : ""}` },
  { id: `test-answer-${i}#0`, seq: input.seq + 1, role: "assistant", kind: "text", text: `Answer ${i + 1}.\n\n${Array.from({ length: 7 }, (_, n) => `Synthetic paragraph ${n + 1}. This invented test conversation supplies enough content to scroll. No account, private conversation or real project is used.`).join("\n\n")}` },
  { id: `test-auto-${i}#0`, seq: input.seq + 2, role: "user", kind: "text", text: "Synthetic automatic supervision", injected: { kind: "supervision", line: "Synthetic automatic supervision" }, source: { kind: "supervision" } },
  { id: `test-compact-${i}#0`, seq: input.seq + 3, role: "system", kind: "notice", text: "Synthetic compaction boundary" },
]);
const indexedQuery = (ordinal) => `Workshop layout for group ${ordinal}`;
const indexedReply = "The wider layout leaves room to scan earlier exchanges. Keep the key decisions together and use the compact layout on smaller screens.";
const index = {
  total: COUNT,
  inputs: mode === "slow" || mode === "cancel" ? inputs.map((input, i) => ({ ...input, preview: indexedQuery(i + 1), answer: indexedReply })) : inputs,
  history: "synthetic-history-v1",
};
const session = { machine_id: MID, session_id: SID, title: "Synthetic full-history navigation", agent: "claude", status: "idle", state: "idle", capabilities: { read: true, send: true, terminal: false } };
const assistant = conversation === "leader"
  ? { machine_id: MID, main: "example-main", leaders: [SID], conversations: [{ ...session, session_id: "example-main" }, session] }
  : { machine_id: MID, main: SID, conversations: [session] };
const machine = { id: MID, name: "Example computer", online: true };
const results = { status: "running", realConversationUsed: false, messagesSent: 0, forwardedWrites: 0, runs: [], errors: [] };
let browser;
const deadline = setTimeout(() => { console.error("TIMEOUT full-history navigation"); process.exit(1); }, 180_000);

function pageFor(before = 0) {
  const eligible = inputs.filter((input) => !before || input.seq < before);
  const pageSize = before ? 50 : INITIAL_BODIES;
  const start = eligible.length > pageSize ? eligible.at(-pageSize).seq : eligible[0]?.seq ?? 0;
  return { items: items.filter((item) => item.seq >= start && (!before || item.seq < before)), has_more: eligible.length > pageSize, history: index.history, to_seq: before ? before - 1 : items.at(-1).seq };
}

function windowAt(target) {
  const from = Math.max(0, target - 41);
  const to = Math.min(items.at(-1).seq, target + 40);
  return {
    items: items.filter(item => item.seq > from && item.seq <= to),
    history: index.history, from_seq: from, to_seq: to, has_more: from > 0, more_after: to < items.at(-1).seq,
  };
}

async function verifyContextWindow({ page, run, shot, previewRequested, showPreview, append }) {
  const rail = page.locator(RAIL);
  const track = rail.locator("[data-nav-track]");
  const chat = page.locator("[data-chat-scroll]");
  const button = ordinal => track.locator(`[data-tick="${ordinal - 1}"]`);
  const top = () => track.evaluate(el => el.scrollTop);
  const chatTop = await chat.evaluate(el => el.scrollTop);
  await page.waitForFunction(() => document.querySelector("[data-nav-track]").scrollTop > 0);
  const initialTop = await top();
  const geometry = await track.evaluate(el => ({
    height: el.clientHeight, content: el.scrollHeight, pitch: Number.parseFloat(el.querySelector("button").style.height),
  }));
  assert.ok(geometry.pitch >= 7 && geometry.pitch <= 8);
  assert.equal(geometry.content, geometry.pitch * COUNT);
  run.mounted = await track.locator("button[data-tick]").count();
  assert.ok(run.mounted <= Math.ceil(geometry.height / geometry.pitch) + 9, "Only the viewport and overscan are mounted");
  assert.ok(run.mounted > 28 && run.mounted < COUNT);
  assert.equal(run.previews.length, 0, "The index opens without any preview requests");
  await shot("dense");

  const box = await track.boundingBox();
  await page.mouse.move(box.x + box.width / 2, box.y + box.height * 0.6);
  const step = Math.min(21, initialTop - 1);
  await page.mouse.wheel(0, -step);
  await page.waitForFunction(top => document.querySelector("[data-nav-track]").scrollTop < top, initialTop);
  run.wheelPixels = initialTop - await top();
  assert.ok(run.wheelPixels > 0 && run.wheelPixels <= step, "A small wheel delta scrolls a few pixels");
  await page.mouse.wheel(0, -geometry.content);
  await page.waitForFunction(() => document.querySelector("[data-nav-track]").scrollTop === 0);
  assert.equal(await chat.evaluate(el => el.scrollTop), chatTop, "Rail scroll leaves the chat viewport in place");

  const ordinal = 12;
  const target = inputs[ordinal - 1];
  const mark = button(ordinal);
  const row = chat.locator(`[data-line="seg:${target.id}"]`);
  assert.equal(await row.count(), 0);
  await mark.hover();
  await previewRequested;
  await page.locator("[data-nav-preview-loading]").waitFor();
  showPreview();
  await page.getByText(indexedQuery(ordinal), { exact: true }).waitFor();
  assert.equal(await page.locator("[data-nav-preview-excerpt]").textContent(), indexedReply);
  assert.equal(await row.count(), 0, "The preview leaves the transcript at the tail");
  await page.waitForFunction(i => document.querySelector(`[data-tick="${i}"] [data-nav-mark]`)?.getBoundingClientRect().width >= 21, ordinal - 1);
  await shot("scrolled-preview");
  assert.equal(run.olderReads, 0);
  assert.equal(run.aroundReads.length, 0);

  await mark.click();
  await chat.and(page.locator("[data-chat-context-window]")).waitFor();
  await row.waitFor({ state: "attached" });
  assert.deepEqual(run.aroundReads, [target.seq]);
  const pane = await chat.boundingBox();
  await page.mouse.move(pane.x + pane.width * 0.75, pane.y + pane.height / 2);
  await page.waitForFunction(id => {
    const chat = document.querySelector("[data-chat-scroll]");
    const row = chat.querySelector(`[data-line="seg:${id}"]`)?.firstElementChild;
    return row && row.querySelector("[data-search-hit]") && Math.abs(row.getBoundingClientRect().top - chat.getBoundingClientRect().top - 12) <= 2;
  }, target.id);
  await shot("target");
  assert.equal(await mark.getAttribute("aria-current"), "true", "The landed question becomes current");
  assert.equal(run.olderReads, 0);
  assert.equal(run.afterReads, 0, "The continuity pull never fills the intentional gap");

  append();
  await page.getByRole("button", { name: "New messages" }).waitFor();
  assert.equal(await chat.getByText("A new reply at the live end.", { exact: true }).count(), 0);
  await page.locator("[data-jump-pill]").click();
  await chat.getByText("A new reply at the live end.", { exact: true }).waitFor();
  assert.equal(await chat.getAttribute("data-chat-context-window"), null);
  assert.equal(await row.count(), 0);
  assert.equal(run.afterReads, 0);
  run.targetOrdinal = ordinal;
  run.recoveredLiveTail = true;
}

try {
  browser = await chromium.launch({ channel: "chrome", executablePath: process.env.CHROME_BIN, headless: true, ignoreDefaultArgs: ["--hide-scrollbars"] });
  const cases = mode === "cancel" ? interruptedJumps :
    (compatibility ? ["wide"] : ["wide", "narrow"]).flatMap(size => ["light", "dark"].map(theme => ({ theme, size })));
  for (const { theme, size, source, action } of cases) {
    const name = `${size}-${theme}${action ? "-" + action.toLowerCase().replaceAll(" ", "-") : ""}`;
    const run = { name, serverTotal: COUNT, olderReads: 0, afterReads: 0, previews: [], aroundReads: [], pageErrors: [], screenshots: [] };
    results.runs.push(run);
    const context = await browser.newContext({ viewport: size === "wide" ? { width: 1440, height: 900 } : { width: 390, height: 844 }, colorScheme: theme, locale: "en-US", hasTouch: false, serviceWorkers: "block" });
    await context.addInitScript(({ theme, textSize }) => { localStorage.setItem("persona.token", "synthetic-only"); localStorage.setItem("persona.language", "en"); localStorage.setItem("persona.theme", theme); localStorage.setItem("persona.text-size", textSize); }, { theme, textSize });
    let release;
    const ready = new Promise((done) => { release = done; });
    let olderGate;
    let olderStarted;
    let failNextOlder = false;
    const heldPreview = Promise.withResolvers();
    const previewRequested = Promise.withResolvers();
    await context.route("**/*", async route => {
      const request = route.request(), url = new URL(request.url());
      if (url.origin !== base.origin) return route.abort("blockedbyclient");
      if (!["GET", "HEAD"].includes(request.method())) return route.fulfill({ json: { ok: true } });
      if (!url.pathname.startsWith("/api/")) return route.continue();
      const path = url.pathname.replace(/^\/api\/v1/, "");
      if (path.endsWith("/items") && url.searchParams.get("user_inputs") === "1") {
        assert.equal(url.searchParams.get("meta_only"), "1");
        await ready;
        return route.fulfill({ json: index });
      }
      if (path.endsWith("/items")) {
        if (url.searchParams.has("around_seq")) {
          if (compatibility) return route.fulfill({ status: 409, json: { code: "node_outdated" } });
          assert.equal(url.searchParams.get("history"), index.history);
          assert.equal(url.searchParams.get("radius"), "40");
          const target = Number(url.searchParams.get("around_seq"));
          run.aroundReads.push(target);
          return route.fulfill({ json: windowAt(target) });
        }
        if (url.searchParams.has("preview")) {
          if (compatibility) return route.fulfill({ status: 409, json: { code: "node_outdated" } });
          const positions = url.searchParams.getAll("preview");
          assert.ok(positions.length <= 9);
          assert.equal(url.searchParams.get("history"), index.history);
          assert.ok(url.search.includes("%23"));
          run.previews.push(positions.length);
          if (positions.includes(`${inputs[11].seq}:${inputs[11].id}`)) {
            previewRequested.resolve();
            await heldPreview.promise;
          }
          const excerpts = inputs.flatMap((input, i) => positions.includes(`${input.seq}:${input.id}`) ?
            [{ ...input, preview: indexedQuery(i + 1), answer: indexedReply }] : []);
          return route.fulfill({ json: { inputs: excerpts, history: index.history } });
        }
        const before = Number(url.searchParams.get("before_seq") ?? 0);
        if (before) {
          run.olderReads++;
          olderStarted?.();
          if (olderGate) await olderGate;
          if (failNextOlder) {
            failNextOlder = false;
            return route.fulfill({ status: 400, json: { code: "bad_request", error: "Synthetic older-page failure" } });
          }
        }
        const after = Number(url.searchParams.get("after_seq") ?? 0);
        if (url.searchParams.has("after_seq")) run.afterReads++;
        return route.fulfill({ json: after ? { items: [], history: index.history, to_seq: items.at(-1).seq, more_after: false } : pageFor(before) });
      }
      const json = {
        "/sessions": { sessions: [session], total: 1, has_more: false }, [`/sessions/${MID}/${SID}/info`]: session,
        "/machines": { machines: [machine] }, "/assistant": assistant,
        "/interactions": { interactions: [] }, "/todos": { todos: [] }, "/mutes": { spaces: [], sessions: [], allowed: [] },
        "/pins": { spaces: [], sessions: [], sections: [] }, "/agents/sessions": { sessions: [] }, "/agents/prefs": {},
        "/managers": { managers: [], default_id: "" }, "/personas": { personas: [] }, "/assistant/personas": { personas: [] },
        "/workspaces": { workspaces: [] }, "/extensions": { extensions: [] },
      }[path] ?? {};
      return route.fulfill({ json });
    });
    let hubSocket;
    await context.routeWebSocket("**/*", socket => {
      if (new URL(socket.url()).pathname.startsWith("/api/")) hubSocket = socket;
      assert.equal(new URL(socket.url()).host, base.host);
      socket.send(JSON.stringify({ type: "hello", server_version: "mock", protocol: 1, features: [] }));
      socket.send(JSON.stringify({ type: "machines", machines: [machine] }));
      socket.send(JSON.stringify({ type: "sessions", machine_id: MID, sessions: [session] }));
      socket.onMessage(raw => { const event = JSON.parse(String(raw)); if (event.type === "app_ping") socket.send(JSON.stringify({ type: "pong", id: event.id })); });
    });
    const page = await context.newPage(); page.setDefaultTimeout(20_000);
    page.on("pageerror", error => run.pageErrors.push(error.message));
    if (mode === "cancel") {
      const held = Promise.withResolvers();
      olderGate = held.promise;
      release();
      const url = new URL(`/persona/${SID}`, base);
      await checkInterruptedJump({
        page, url, run, output, source, action, release: held.resolve,
        target: inputs[COMPAT_TARGET], targetOrdinal: COMPAT_TARGET + 1, second: inputs.at(-4), secondOrdinal: COUNT - 3,
        append: () => {
          const seq = items.at(-1).seq + 1;
          const live = [
            { id: "compact", seq, role: "system", kind: "notice", compact: { trigger: "manual", pre_tokens: 12000 } },
            { id: "supervisor", seq: seq + 1, role: "user", kind: "text", text: "Check the fictional task.", injected: { kind: "system", state: "supervise", line: "Review the task" } },
            { id: "report", seq: seq + 2, role: "assistant", kind: "text", text: "Checking the task.", narration: true },
          ];
          hubSocket.send(JSON.stringify({ type: "items", machine_id: MID, session_id: SID, history: index.history, items: live, to_seq: live.at(-1).seq }));
        },
      });
      await context.close();
      continue;
    }
    await page.goto(new URL(conversation === "main" ? "/" : `/s/${MID}/${SID}`, base).href);
    const rail = page.locator(RAIL);
    await page.waitForFunction(count => document.querySelectorAll("[data-user-bubble]").length === count, INITIAL_BODIES);
    if (size === "wide") await rail.locator("button[data-tick]").first().waitFor();
    const chat = page.locator("[data-chat-scroll]"); const box = await chat.boundingBox();
    run.initialBodies = await page.locator("[data-user-bubble]").count();
    run.beforeTicks = await rail.locator("button[data-tick]").count();
    const shot = async state => { const name = `history-${run.name}-${state}.png`; await page.screenshot({ path: join(output, name) }); run.screenshots.push(name); };
    release();
    await page.waitForFunction(([rail, total]) => document.querySelector(rail)?.dataset.navTotal === String(total) && document.querySelector(rail)?.dataset.navSource === "server", [RAIL, COUNT]);
    assert.equal(await page.locator("[data-user-bubble]").count(), INITIAL_BODIES, "Index loading must not load every transcript body");
    assert.equal(run.olderReads, 0);
    if (!compatibility) {
      await verifyContextWindow({ page, run, shot, previewRequested: previewRequested.promise, showPreview: heldPreview.resolve, append: () => {
        const end = items.at(-1).seq;
        hubSocket.send(JSON.stringify({ type: "items", machine_id: MID, session_id: SID, from_seq: end, to_seq: end + 1, history: index.history,
          items: [{ id: "live-reply#0", seq: end + 1, role: "assistant", kind: "text", text: "A new reply at the live end." }] }));
      } });
      assert.deepEqual(run.pageErrors, []);
      console.log(`PASS ${run.name}: ${run.mounted} mounted ticks, ${run.wheelPixels} px wheel, one context read, live tail recovered`);
      await context.close();
      continue;
    }
    run.afterTicks = await rail.locator("button[data-tick]").count();
    const firstOrdinal = COMPAT_TARGET + 1;
    const byOrdinal = ordinal => rail.getByRole("button", { name: new RegExp(`^Jump to user input ${ordinal}:`) });
    await rail.focus();
    await page.keyboard.press("End");
    for (let i = COUNT; i > firstOrdinal; i--) await page.keyboard.press("ArrowUp");
    const first = byOrdinal(firstOrdinal);
    assert.ok((await first.getAttribute("aria-label")).includes(String(firstOrdinal)), "Labels use full-history ordinals");
    await first.hover(); assert.equal(run.olderReads, 0, "Hover does not fetch old transcript bodies");
    await page.locator("[data-nav-preview]").waitFor(); await shot("hover");
    const title = await page.locator("[data-nav-preview-title]").textContent();
    if (INITIAL_BODIES < COUNT - COMPAT_TARGET && mode !== "legacy") {
      assert.equal(title, indexedQuery(firstOrdinal));
      assert.equal(await page.locator("[data-nav-preview-excerpt]").textContent(), indexedReply);
    } else if (INITIAL_BODIES < COUNT - COMPAT_TARGET) {
      assert.equal(title, `User input ${firstOrdinal}`);
      assert.equal(await page.locator("[data-nav-preview-excerpt]").count(), 0);
    } else {
      assert.ok(title.startsWith(`Navigation input ${firstOrdinal}:`), "Loaded text takes precedence over the index");
    }
    const started = Date.now();
    if (mode === "slow") {
      failNextOlder = true;
      let resume;
      olderGate = new Promise(resolve => { resume = resolve; });
      await page.evaluate(() => {
        window.navFrameGaps = [];
        let last = performance.now();
        const frame = now => {
          window.navFrameGaps.push(now - last);
          last = now;
          window.navFrame = requestAnimationFrame(frame);
        };
        window.navFrame = requestAnimationFrame(frame);
      });
      await first.click();
      await page.locator("[data-nav-loading]").waitFor();
      run.feedbackMs = Date.now() - started;
      await first.locator("[data-nav-mark^='current']").waitFor();
      assert.equal(await first.getAttribute("aria-current"), null, "The pending mark is highlighted without moving the current position");
      await shot("loading");
      resume();
      olderGate = undefined;
      await page.getByText("This input could not be loaded. Try again.", { exact: true }).waitFor();
      await page.locator("[data-nav-loading]").waitFor({ state: "hidden" });
      // Both selections arrive during backoff, before the server receives another page request.
      const next = byOrdinal(firstOrdinal + 1);
      const readsBefore = run.olderReads;
      olderGate = new Promise(resolve => { resume = resolve; });
      const retryRequest = new Promise(resolve => { olderStarted = resolve; });
      const retryStartedAt = Date.now();
      await first.click();
      await page.locator("[data-nav-loading]").waitFor();
      await next.click();
      await next.locator("[data-nav-mark^='current']").waitFor();
      assert.equal(run.olderReads, readsBefore, "Both selections precede a retry");
      await retryRequest;
      olderStarted = undefined;
      assert.equal(run.olderReads, readsBefore + 1, "Overlapping selections share the retry");
      resume();
      olderGate = undefined;
      await page.waitForFunction(id => {
        const row = document.querySelector(`[data-line='seg:${id}']`)?.firstElementChild;
        const sc = document.querySelector("[data-chat-scroll]");
        return row && sc && Math.abs(row.getBoundingClientRect().top - sc.getBoundingClientRect().top - 12) <= 2;
      }, inputs[COMPAT_TARGET + 1].id);
      run.retryLandingMs = Date.now() - retryStartedAt;
      run.maxFrameGapMs = await page.evaluate(() => {
        cancelAnimationFrame(window.navFrame);
        return Math.max(...window.navFrameGaps.slice(1));
      });
      await page.locator("[data-nav-loading]").waitFor({ state: "hidden" });
      assert.equal(run.olderReads, readsBefore + 1, "The latest target is in the shared page");
    }
    const warmStarted = Date.now();
    const readsBeforeRepeat = run.olderReads;
    await first.click();
    await page.waitForFunction(([id]) => {
      const row = document.querySelector(`[data-line='seg:${id}']`)?.firstElementChild;
      const sc = document.querySelector("[data-chat-scroll]");
      return row && sc && row.getBoundingClientRect().top - sc.getBoundingClientRect().top >= -2 && row.getBoundingClientRect().top - sc.getBoundingClientRect().top <= 32;
    }, [inputs[COMPAT_TARGET].id]);
    if (mode === "slow") {
      run.repeatLandingMs = Date.now() - warmStarted;
      assert.equal(run.olderReads, readsBeforeRepeat, "Loaded repeat requires no older read");
    }
    if (INITIAL_BODIES < COUNT - COMPAT_TARGET) assert.ok(run.olderReads > 0, "The unloaded target must fetch earlier pages before positioning");
    await first.hover();
    assert.ok((await page.locator("[data-nav-preview-title]").textContent()).startsWith(`Navigation input ${firstOrdinal}:`), "The card switches to the parsed user query after landing");
    assert.ok((await page.locator("[data-nav-preview-excerpt]").textContent()).startsWith(`Answer ${firstOrdinal}.`));
    if (conversation === "main") assert.ok(await page.locator("[data-nav-preview-state]").textContent(), "The loaded question shows its state");
    await shot("loaded-preview");
    await page.mouse.move(box.x + box.width * 0.7, box.y + box.height / 2);
    await rail.blur();
    await page.waitForFunction(() => !document.querySelector("[data-nav-preview]"));
    if (COUNT > 50) {
      await chat.evaluate(el => { el.scrollTop = 1; });
      await page.mouse.wheel(0, -500);
      await rail.focus();
      await page.keyboard.press("Home");
      run.earliestAfterScroll = await page.waitForFunction(([selector, ordinal]) => {
        const label = document.querySelector(`${selector} button[data-tick="0"]`)?.getAttribute("aria-label");
        const earlier = Number(label?.match(/^Jump to user input (\d+):/)?.[1]);
        return earlier > 0 && earlier < ordinal ? earlier : false;
      }, [RAIL, firstOrdinal]).then(result => result.jsonValue());
      await rail.blur();
    }
    run.afterScrollTicks = await rail.locator("button[data-tick]").count();
    assert.equal(Number(await rail.getAttribute("data-nav-total")), COUNT);
    await shot("earlier-loaded");
    assert.deepEqual(run.pageErrors, []);
    run.selectedInputPositioned = true;
    console.log(`PASS ${run.name}: initial user bodies=${run.initialBodies}, ticks ${run.beforeTicks}->${run.afterTicks}->${run.afterScrollTicks}, global total=${run.serverTotal}, selected input=${firstOrdinal}, backward reads=${run.olderReads}`);
    await context.close();
  }
  results.status = "pass";
} catch (error) { results.status = "fail"; results.errors.push(error.message); console.error(error.message); process.exitCode = 1; }
finally {
  clearTimeout(deadline); await browser?.close();
  await writeFile(join(output, "results.json"), JSON.stringify(results, null, 2));
  console.log(`${results.status.toUpperCase()} full-history user navigation`);
}
