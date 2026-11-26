// @vitest-environment jsdom
/// <reference types="node" />
// StarredMessages draws the saved list on mount, while StarredBackupDialog loads on demand when the user clicks
// Backup (lazyOverlay, no idle preload), leaving StarredBackupDialog.tsx untouched.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, resolve } from "node:path";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initI18n, t } from "../i18n";
import { setToken } from "../lib/api";
import { resetManagerChoice, settleRosterDefault } from "../lib/manager-choice";
import { resetStarredForTest } from "../lib/starred";
import type { Star } from "../lib/starred-rows";

const backupMock = vi.hoisted(() => ({ loads: vi.fn() }));
vi.mock("./StarredBackupDialog", async (importOriginal) => {
  backupMock.loads();
  return importOriginal<typeof import("./StarredBackupDialog")>();
});

vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, addEventListener() {}, removeEventListener() {} })) as typeof window.matchMedia;
});

import { StarredMessages } from "./StarredMessages";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const rootDir = resolve(__dirname, "..");
const staticBackupImport = /^import\s+(?!type\b)[^;]*from\s+["'][./]*(?:views\/)?StarredBackupDialog["']/m;

function sources(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) return sources(p);
    return /\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name) ? [p] : [];
  });
}

const star: Star = {
  id: "star-1",
  machine_id: "m1",
  session_id: "s1",
  item_id: "item-1",
  conversation: "assistant",
  role: "assistant",
  title: "Weekly notes",
  speaker: "Persona",
  text: "Remember to archive the finished checklist.",
  starred_at: "2026-11-21T03:04:00Z",
};
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

let host: HTMLDivElement;
let root: Root;

beforeEach(async () => {
  await initI18n("en");
  resetStarredForTest();
  resetManagerChoice();
  setToken("starred-lazy-test");
  settleRosterDefault();
  vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL) => {
    const path = String(input);
    if (path === "/api/v1/assistant/starred?index=1") return Promise.resolve(json({ refs: [{ id: star.id, machine_id: star.machine_id, session_id: star.session_id, item_id: star.item_id }], can_write: true }));
    if (path === "/api/v1/assistant/starred") return Promise.resolve(json({ stars: [star], can_write: true }));
    if (path === "/api/v1/assistant/starred/import") return Promise.resolve(json({ can_import: true }));
    throw new Error(`Unexpected request: ${path}`);
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(async () => {
  act(() => root.unmount());
  host.remove();
  resetStarredForTest();
  resetManagerChoice();
  setToken("");
  vi.unstubAllGlobals();
  await initI18n("zh-Hans");
});

const flush = async () => {
  for (let i = 0; i < 12; i++) await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
};

const waitForDialog = async () => {
  for (let i = 0; i < 80; i++) {
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 10)); });
    if (document.querySelector('[role="dialog"]')) return;
  }
};

describe("StarredMessages StarredBackupDialog lazy boundary", () => {
  it("keeps StarredBackupDialog out of all static imports and loads it via lazyOverlay without idle preloading", () => {
    for (const file of sources(rootDir)) {
      expect(readFileSync(file, "utf8"), file).not.toMatch(staticBackupImport);
    }
    const view = readFileSync(resolve(__dirname, "StarredMessages.tsx"), "utf8");
    expect(view).toContain('lazyOverlay(() => import("./StarredBackupDialog").then((m) => m.StarredBackupDialog))');
    expect(view).not.toMatch(/import\(["']\.\/StarredBackupDialog["']\)[^;]*idle\s*:\s*true/);
  });

  it("renders StarredMessages without loading StarredBackupDialog, then loads and opens it on clicking Backup", async () => {
    await act(async () => root.render(<StarredMessages />));
    await flush();

    expect(host.textContent).toContain(star.title);
    expect(host.textContent).toContain(star.text);
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(backupMock.loads).not.toHaveBeenCalled();

    const backupButton = [...host.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent === t("starred.backup"));
    expect(backupButton).toBeDefined();
    await act(async () => backupButton!.click());
    await waitForDialog();
    expect(document.querySelector('[role="dialog"]')).not.toBeNull();
    expect(backupMock.loads).toHaveBeenCalledTimes(1);
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(t("starred.backup_title"));
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(t("starred.backup_json"));

    // Closing unmounts cleanly; reopening reuses the loaded module without reloading.
    const doneButton = [...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find((b) => b.textContent === t("starred.backup_done"));
    expect(doneButton).toBeDefined();
    await act(async () => doneButton!.click());
    await flush();
    expect(document.querySelector('[role="dialog"]')).toBeNull();

    await act(async () => backupButton!.click());
    await waitForDialog();
    expect(document.querySelector('[role="dialog"]')).not.toBeNull();
    expect(backupMock.loads).toHaveBeenCalledTimes(1);
  });

  it("resolves the dynamic import to StarredBackupDialog", async () => {
    const mod = await import("./StarredBackupDialog");
    expect(typeof mod.StarredBackupDialog).toBe("function");
  });
});
