// A `/t/…` link of an idle tab on a paging hub (sessions_list): the page's rows hold only the active ones, so the tab is
// looked up from the hub before the page says it is gone (lib/tab-lookup.ts). Against the fake hub in paging mode:
//
//   MOCK_SESSIONS_LIST=1 PORT=18997 python3 scripts/mock/mock_hub.py &
//   (cd web && PERSONA_HUB_URL=http://127.0.0.1:18997 npx vite --port 5998 --strictPort &)
//   node web/e2e/tab-lookup.mjs http://127.0.0.1:5998 /t/macbook/w1/w1%3At3 /tmp/tab.png [expect: OPEN|GONE]
//
// The browser trims the list reads to working / needs-you rows (a long list's first page) and drops the pins, so only
// the tab lookup's own read (by machine and group) can find the idle tab. Prints OPEN or GONE and a verdict.
import { chromium } from "playwright-core";
const [base, path, out, expect = "OPEN"] = process.argv.slice(2);
const b = await chromium.launch({ channel: process.env.CHANNEL || undefined, executablePath: process.env.CHROME || undefined, headless: true });
try {
  const p = await (await b.newContext({ viewport: { width: 1280, height: 800 } })).newPage();
  // a paging hub's features arrive before the first refresh, so the page never gets the legacy full list
  await p.route(/\/api\/v1\/pins$/, (r) => r.request().method() === "GET" ? r.fulfill({ json: {} }) : r.continue());
  // a long list: the idle tab is past the pages the page reads (only the tab lookup's own read, by group, sees it)
  await p.route(/\/api\/v1\/sessions\?/, async (r) => {
    const u = new URL(r.request().url());
    if (u.searchParams.has("group") || u.searchParams.has("counts") || u.searchParams.has("session_id")) return r.continue();
    const res = await r.fetch(); const body = await res.json();
    if (Array.isArray(body.sessions)) { body.sessions = body.sessions.filter((s) => s.state === "working" || s.state === "needs_you"); delete body.next_cursor; }
    return r.fulfill({ response: res, json: body });
  });
  await p.route(/\/api\/v1\/sessions$/, (r) => r.request().method() === "GET" ? r.fulfill({ json: { sessions: [] } }) : r.continue());
  await p.goto(`${base}/?token=mock`);
  await p.waitForFunction(() => localStorage.getItem("persona.token"), null, { timeout: 15000 });
  await p.goto(base + path);
  // settles on the chat's composer (opened) or the gone notice
  await p.locator("main textarea, main :text-matches('不在了|no longer exists', 'i')").first().waitFor({ timeout: 20_000 }); // i18n-ignore
  await p.screenshot({ path: out });
  const txt = await p.locator("main").innerText();
  const got = txt.includes("不在了") || /no longer exists/i.test(txt) ? "GONE" : "OPEN"; // i18n-ignore
  console.log(`VERDICT ${got === expect ? "PASS" : "FAIL"} ${path}: ${got} (expected ${expect}), screenshot ${out}`);
  process.exitCode = got === expect ? 0 : 1;
} finally { await b.close(); }
