import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import test from "node:test";

const source = (name: string) => readFile(join(import.meta.dirname, "..", "..", "src", name), "utf8");

test("an explicit hub change closes the old window before making its own session and IPC", async () => {
  const main = await source("main.ts");
  const change = main.slice(main.indexOf("async function replaceMainWindow"), main.indexOf("async function showMain"));
  assert.match(change, /parseHubAddress\(previous\.webContents\.getURL\(\)\)\.url\.href === next\.url\.href/);
  assert.match(change, /const closed = new Promise<void>\(done => previous\.once\("closed", done\)\);/);
  assert.ok(change.indexOf("await closed;") < change.indexOf("mainWindow = undefined;"));
  const connect = main.slice(main.indexOf("connect: async"), main.indexOf("await setup.loadURL(SETUP_URL)"));
  assert.ok(connect.indexOf("const generation = hubWindow.generation") < connect.indexOf("await showMain()"));
  assert.ok(connect.indexOf("await showMain()") < connect.indexOf("await store().update({ hub: next.url.href })"));
  assert.ok(connect.indexOf("await store().update({ hub: next.url.href })") < connect.indexOf("setup.close()"));
  assert.match(connect, /generation !== hubWindow\.generation \|\| hub\?\.url\.href !== next\.url\.href/);
  assert.match(connect, /Could not save the hub connection/);
  assert.match(connect, /failedWindow\.destroy\(\)/);
  assert.match(main, /hubWindow\.replace\(\);/);
  assert.match(main, /hub: \(\) => \{\s+const url = current\.webContents\.getURL\(\);/);
  const ipc = await source("ipc.ts");
  assert.match(ipc, /inHub\(hub, event\.senderFrame\.url\)/);
});

test("local login-item IPC is separate from the surviving hub window's public bridge", async () => {
  const setup = await source("setup-ipc.ts");
  const preload = await source("setup-preload.cts");
  for (const suffix of ["get", "set"]) {
    assert.match(setup, new RegExp(`"desktop:setup-login-${suffix}"`));
    assert.match(preload, new RegExp(`"desktop:setup-login-${suffix}"`));
    assert.ok(!setup.includes(`"desktop:login-${suffix}"`), `setup must not own desktop:login-${suffix}`);
  }
  const hub = await source("ipc.ts");
  for (const name of ["desktop:login-get", "desktop:login-set"]) assert.match(hub, new RegExp(`"${name}"`));
});

test("closing setup when no main window exists quits cleanly without respawning (#9458)", async () => {
  const main = await source("main.ts");
  const closed = main.slice(main.indexOf('setup.once("closed"'), main.indexOf("const removeIPC = installSetupIPC"));
  assert.ok(closed.includes("if (quitting) return;"), "setup's closed handler must stop once the app is quitting");
  assert.ok(!closed.includes("showMain("), "closing setup must never respawn main/setup in a loop");
  assert.ok(!closed.includes("showSetup("), "closing setup must never reopen setup");
  assert.ok(closed.includes("app.quit()"), "closing onboarding or recovery with no main window must exit Persona");
  // Every quit (menu, Dock, signal, tray) passes before-quit, which runs before windows close.
  assert.match(main, /app\.on\("before-quit", \(\) => \{ quitting = true;/);
});

test("after Persona Link pairing succeeds, clicking sign-in connects to the Hub directly without bouncing back", async () => {
  const script = await source("setup.js");
  const idx = script.indexOf("usePairedHubButton.addEventListener");
  assert.notEqual(idx, -1, "setup.js must wire #use-paired-hub");
  const handler = script.slice(idx, script.indexOf("});", idx) + 3);
  assert.match(handler, /window\.desktopSetup\.saveHub\(pairedSuggestedHub\)/, "must connect directly using saveHub(pairedSuggestedHub)");
});

test("pinned TLS sessions enforce exact-origin requests and refresh when mainWindow is reused", async () => {
  const main = await source("main.ts");
  const helper = main.slice(main.indexOf("async function applySessionPin"), main.indexOf("const island = new NotchIsland"));
  assert.ok(helper.includes("webRequest.onBeforeRequest"), "must install webRequest.onBeforeRequest guard");
  assert.ok(helper.includes("allowPinnedSessionRequest"), "must filter pinned-hostname requests by exact origin");
  assert.ok(helper.includes("setCertificateVerifyProc"), "must install SPKI pin verifier");
  assert.ok(helper.includes("closeAllConnections"), "must close pooled connections on pin change or refresh");

  const showMain = main.slice(main.indexOf("async function showMain"), main.indexOf("async function showSetup"));
  const reused = showMain.slice(showMain.indexOf("if (mainWindow && !mainWindow.isDestroyed())"), showMain.indexOf("const state = restoreWindow"));
  const refreshAt = reused.indexOf("await applySessionPin(mainWindow, owner)");
  const loadAt = reused.indexOf("await mainWindow.loadURL(initial)");
  assert.notEqual(refreshAt, -1, "reused mainWindow must refresh session pin");
  assert.notEqual(loadAt, -1, "reused mainWindow must load initial pairing URL when queued");
  assert.ok(refreshAt < loadAt, "reused mainWindow must refresh pin and clear connections before loadURL(initial)");
  assert.ok(reused.includes("initial || windowPins.get(mainWindow) !== sessionPinKey(activePinFor(owner))"),
    "reused mainWindow must refresh on initial URL or active pin changes");

  const freshLoad = showMain.slice(showMain.indexOf("if (owner) {"), showMain.indexOf("await current.loadURL(targetURL)"));
  assert.ok(freshLoad.includes("await applySessionPin(current, owner)"), "new mainWindow must apply session pin before loadURL");
});
