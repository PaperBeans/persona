// Run the real web client against a disposable loopback mock hub started with MOCK_REWIND=safeguards.
// No shared browser profile, CDP connection or production hub is used. All waits and requests are bounded.
// Edit last message (#2945) and ↑ in an empty composer (#4503) start one edit: the last prompt goes back into the box
// with an Editing bar; Esc or × cancels with nothing asked of the hub; the conversation-only rewind runs when the edit
// is sent, right before the message. The keys that must not start it are checked first, against the unchanged mock.
import assert from "node:assert/strict";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const flag = (name, fallback) => (args.includes(name) ? args[args.indexOf(name) + 1] : fallback);
const base = args[0];
const hub = flag("--hub");
const out = flag("--out");
const channel = flag("--channel", "chrome");
if (!base || !hub || !out) throw new Error("usage: edit-last-message.mjs <web URL> --hub <mock hub URL> --out <directory> [--channel msedge]");
for (const url of [base, hub]) assert.equal(new URL(url).hostname, "127.0.0.1", "only a loopback mock is allowed");
mkdirSync(out, { recursive: true });
const mid = "macbook";
const sid = "a1f3c2d4-0000-4000-8000-000000000006";
const path = `/api/v1/sessions/${mid}/${sid}`;
const request = async (url) => (await fetch(url, { signal: AbortSignal.timeout(5000) })).json();
const rewindCount = async () => (await request(`${hub}/mock/rewinds`)).rewinds.length;
const browser = await chromium.launch({ channel, headless: true });
const results = [];
const timer = setTimeout(() => { void browser.close(); }, 180000);
try {
  // light: the mock's own safeguards fixture, ↑ commits; dark, at the Large text size: the prompt left after that, the
  // card's button commits
  for (const [theme, action, textSize] of [["light", "up", "standard"], ["dark", "button", "large"]]) {
    const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, colorScheme: theme });
    const page = await context.newPage();
    page.setDefaultTimeout(12000);
    const errors = [];
    const writes = [];
    page.on("pageerror", (e) => errors.push(e.message));
    // what changes the conversation: a rewind, a message, an interrupt (opening the chat may post other things, like read marks)
    page.on("request", (r) => { if (r.method() === "POST" && r.url().includes(path) && /\/(rewind|messages|interrupt)$/.test(r.url())) writes.push({ url: r.url(), body: r.postDataJSON() }); });
    await context.route("**/*", async (route) => {
      const url = new URL(route.request().url());
      if (![new URL(base).origin, new URL(hub).origin].includes(url.origin)) return route.abort();
      return route.continue();
    });
    // two messages sent from this browser before (#2434): ↑ on an untouched edit goes on to the older one
    await page.addInitScript(({ theme, key, textSize }) => {
      localStorage.setItem("persona.language", "en");
      localStorage.setItem("persona.theme", theme);
      localStorage.setItem("persona.text-size", textSize);
      localStorage.setItem(`persona.sent.${key}`, JSON.stringify([{ text: "An older message", at: 1 }, { text: "Revise this paragraph", at: 2 }]));
    }, { theme, key: `${mid}/${sid}`, textSize });
    // The second run edits the last remaining mock input; add the same API error to its HTTP transcript, leaving prompt
    // ids/counts intact for the mock's actual rewind handler.
    if (action === "button") await page.route(`**${path}/items*`, async (route) => {
      const response = await route.fetch();
      const data = await response.json();
      if (Array.isArray(data.items) && data.items.length) {
        const last = data.items.at(-1);
        if (last.role === "assistant") last.text = "API Error: Opus 5.5's safeguards flagged this message. Details: [frontier_llm]";
      }
      await route.fulfill({ response, json: data });
    });
    await page.goto(`${base}/?token=mock`);
    await page.locator("main textarea").waitFor();
    await page.evaluate((sid) => {
      history.pushState(null, "", `/s/macbook/${sid}`);
      dispatchEvent(new PopStateEvent("popstate"));
    }, sid);
    const input = page.locator("main textarea");
    const bar = page.getByTestId("composer-editing");
    const edit = page.getByRole("button", { name: "Edit last message", exact: true });
    const cancel = page.getByRole("button", { name: "Cancel editing", exact: true });
    const quoteBar = page.getByLabel("Quoted message");
    // a screenshot once the bar's fade-in and the quote's pop-in have finished
    const shot = async (name) => {
      await page.evaluate(() => Promise.all(document.getAnimations().filter((a) => a.effect?.getTiming().iterations !== Infinity).map((a) => a.finished)));
      await page.screenshot({ path: join(out, `${theme}-${name}.png`) });
    };
    await edit.waitFor();
    await shot("error");
    assert.equal(await page.getByRole("button", { name: "Retry", exact: true }).count(), 0);
    const before = await rewindCount();
    const untouched = async (what, value = "") => {
      assert.equal(await bar.count(), 0, `${what}: no Editing bar`);
      assert.equal(await input.inputValue(), value, `${what}: the box keeps ${JSON.stringify(value)}`);
      assert.equal(await rewindCount(), before, `${what}: no rewind`);
      assert.equal(writes.length, 0, `${what}: no rewind, message or interrupt posted`);
    };
    const checks = [];
    const expectEditing = async (entry) => {
      await bar.waitFor();
      assert.equal(await input.inputValue(), "Revise this paragraph");
      assert.equal(await rewindCount(), before, `${entry}: starting an edit rewinds nothing`);
      assert.equal(writes.length, 0, `${entry}: starting an edit posts nothing`);
      // the bar is one line (no wrapped labels) and the box still fits the page
      assert.ok((await bar.boundingBox()).height <= 30, "the Editing bar stays on one line");
      checks.push(`${entry} starts the edit`);
    };

    if (action === "up") {
      // ── what must not start an edit ──
      await input.fill("Keep this draft");
      await input.press("ArrowUp");
      await untouched("↑ over a typed draft", "Keep this draft");
      await input.fill("line one\nline two");
      await input.press("End");
      await input.press("ArrowUp");
      await untouched("↑ with the caret on the second line", "line one\nline two");
      assert.equal(await input.evaluate((el) => el.value.slice(0, el.selectionStart).includes("\n")), false, "↑ moved the caret to the first line");
      await input.fill("");
      for (const mod of ["Shift", "Alt", "Control", "Meta"]) {
        await input.press(`${mod}+ArrowUp`);
        await untouched(`${mod}+↑`);
      }
      // an input method choosing a candidate with ↑ (isComposing, keyCode 229)
      await input.evaluate((el) => {
        const e = new KeyboardEvent("keydown", { key: "ArrowUp", code: "ArrowUp", bubbles: true, cancelable: true, isComposing: true });
        Object.defineProperty(e, "keyCode", { value: 229 });
        el.dispatchEvent(e);
      });
      await untouched("↑ while an input method composes");
      await input.press("Control+r");
      const search = page.getByRole("combobox", { name: "Search sent messages" });
      await search.waitFor();
      await page.keyboard.press("ArrowUp");
      await untouched("↑ in the Ctrl+R search");
      await page.keyboard.press("Escape");
      await search.waitFor({ state: "detached" });
      await input.fill("/");
      await input.press("ArrowUp");
      await untouched("↑ in the / menu", "/");
      await input.press("Escape");
      await input.fill("");
      checks.push("typed draft, caret below the first line, modifiers, IME, Ctrl+R and / menu leave ↑ alone");

      // ── ↑ starts the edit; ↑ again walks on into the sent history, ↓ back to an empty box ──
      await input.press("ArrowUp");
      await expectEditing("↑");
      await quoteBar.waitFor();
      assert.ok((await quoteBar.textContent()).includes("Example quote"));
      await shot("editing");
      await input.press("ArrowUp");
      await bar.waitFor({ state: "detached" });
      assert.equal(await input.inputValue(), "An older message");
      assert.equal(await quoteBar.count(), 0, "leaving the edit for the history drops its quote");
      await input.press("ArrowDown");
      assert.equal(await input.inputValue(), "Revise this paragraph");
      assert.equal(await bar.count(), 0, "the history's copy of the message is not an edit");
      await input.press("ArrowDown");
      await untouched("↑ ↑ ↓ ↓ through the history");
      checks.push("↑ on an untouched edit goes on to the older sent message; ↓ walks back to an empty box");

      // ── Esc cancels: nothing asked of the hub, Claude not interrupted ──
      await input.press("ArrowUp");
      await expectEditing("↑ again");
      await input.fill("Changed my mind");
      await input.press("Escape");
      await bar.waitFor({ state: "detached" });
      await untouched("Esc while editing");
      assert.equal(await quoteBar.count(), 0);
      checks.push("Esc cancels with no rewind, no message and no interrupt");

      // ── Esc Esc starts the same edit; × cancels it ──
      await input.press("Escape");
      await input.press("Escape");
      await expectEditing("Esc Esc");
      await cancel.click();
      await bar.waitFor({ state: "detached" });
      await untouched("× while editing");
      checks.push("× cancels the same way");

      // ── the card's button starts the same edit ──
      await edit.click();
      await expectEditing("Edit last message");
      await input.press("Escape");
      await untouched("Esc after the card's button");
      // a narrow window: the bar keeps one line, the hint truncates
      await page.setViewportSize({ width: 390, height: 844 });
      await input.press("ArrowUp");
      await expectEditing("↑ (narrow)");
      await shot("editing-narrow");
      await input.press("Escape");
      await page.setViewportSize({ width: 1280, height: 900 });
      await input.press("ArrowUp");
    } else {
      await input.press("ArrowUp");
      await bar.waitFor();
      assert.ok((await bar.boundingBox()).height <= 36, "the Editing bar stays on one line at the Large text size");
      await shot("editing");
      await input.press("Escape");
      await bar.waitFor({ state: "detached" });
      await untouched("Esc while editing");
      await edit.click();
    }

    // ── sending the edit: the rewind first, then the message ──
    await bar.waitFor();
    const original = await input.inputValue();
    assert.equal(await rewindCount(), before);
    const edited = `Revised paragraph (${theme})`;
    await input.fill(edited);
    if (action === "up") await input.press("Enter");
    else await page.getByRole("button", { name: "Send", exact: true }).click();
    await page.waitForResponse((r) => r.request().method() === "POST" && r.url().endsWith(`${path}/messages`));
    const after = (await request(`${hub}/mock/rewinds`)).rewinds;
    assert.equal(after.length, before + 1);
    assert.equal(after.at(-1).restore, "conversation");
    assert.equal(after.at(-1).occurrence, 1);
    assert.equal(after.at(-1).prompts_after, 0);
    assert.ok(after.at(-1).prompt.endsWith(original), "the rewind is to before the message being edited");
    assert.deepEqual(writes.map((w) => w.url.slice(w.url.lastIndexOf("/") + 1)), ["rewind", "messages"], "the rewind goes before the message");
    const sent = writes.filter((w) => w.url.endsWith("/messages"));
    assert.ok(sent[0].body.text.endsWith(edited));
    if (action === "up") assert.equal(sent[0].body.text, `> Example quote\n\n${edited}`);
    await bar.waitFor({ state: "detached" });
    await shot("sent");
    assert.equal(writes.filter((w) => w.url.endsWith("/interrupt")).length, 0);
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
    assert.deepEqual(errors, []);
    checks.push("sending runs one conversation-only rewind for that message, then one message with the edit");
    results.push({ theme, textSize, action, rewinds: after.length - before, explicitSends: sent.length, consoleErrors: errors.length, checks, pass: true });
    await context.close();
  }
  writeFileSync(join(out, "results.json"), JSON.stringify(results, null, 2) + "\n");
  console.log(`PASS edit-last-message: ${results.length} themes; ${results.reduce((n, r) => n + r.checks.length, 0)} checks; one rewind, sent with the edit, per run`);
} finally {
  clearTimeout(timer);
  await browser.close();
}
