import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import test from "node:test";

const source = (name: string) => readFile(join(import.meta.dirname, "..", "..", "src", name), "utf8");

test("showMain configures macOS hiddenInset titlebar and publishes window chrome changes", async () => {
  const main = await source("main.ts");
  const showMain = main.slice(main.indexOf("async function showMain"), main.indexOf("async function showSetup"));
  assert.ok(showMain.includes('titleBarStyle: "hiddenInset"'), "showMain must enable hiddenInset on darwin");
  assert.ok(showMain.includes("trafficLightPosition: { x: 14, y: 19 }"), "showMain must anchor traffic lights inside the 44px bay");
  assert.ok(showMain.includes('desktop:window-chrome-changed'), "showMain must notify renderer when fullscreen/focus changes");
  assert.ok(main.includes('setVibrancy("under-window")'), "applySurfaceStyle must toggle macOS vibrancy for Liquid Glass");
});

test("window chrome and surface style IPC handlers validate input and clean up on close", async () => {
  const ipc = await source("ipc.ts");
  const preload = await source("preload.cts");
  assert.ok(ipc.includes('"desktop:window-chrome"'), "ipc must register desktop:window-chrome");
  assert.ok(ipc.includes('"desktop:surface-style"'), "ipc must register desktop:surface-style");
  assert.match(ipc, /value === "default" \|\| value === "liquid-glass"/);
  assert.ok(preload.includes("windowChrome(): DesktopWindowChrome"));
  assert.ok(preload.includes("onWindowChromeChange("));
  assert.ok(preload.includes("setSurfaceStyle(style: DesktopSurfaceStyle)"));
});
