// @vitest-environment jsdom
/// <reference types="node" />
// Routines draws the scheduled task list and run history (RunLine) on mount, while TaskEditor (~40 KB) loads on
// demand when the user opens a task or creates one (lazyOverlay, no idle preload).
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, resolve } from "node:path";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import { setToken } from "../../lib/api";
import { resetManagerChoice } from "../../lib/manager-choice";
import type { Run, Task } from "../../lib/routines";
import { getState } from "../../lib/store";

const editorMock = vi.hoisted(() => ({ loads: vi.fn() }));
vi.mock("./TaskEditor", async (importOriginal) => {
  editorMock.loads();
  return importOriginal<typeof import("./TaskEditor")>();
});

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
});

import { Routines } from "./Routines";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const rootDir = resolve(__dirname, "../..");
const staticTaskEditorImport = /^import\s+(?!type\b)[^;]*from\s+["'][./]*(?:views\/)?(?:assistant\/)?TaskEditor["']/m;

function sources(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) return sources(p);
    return /\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name) ? [p] : [];
  });
}

const task: Task = { id: "task-1", title: "Daily summary", prompt: "Summarize the day", repeat: "daily", time: "09:00", enabled: true };
const run: Run = { id: "run-1", kind: "task", ref_id: task.id, title: "Completed summary", due: "2026-11-05T09:00:00Z", at: "2026-11-05T09:00:00Z", status: "ok" };
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  resetManagerChoice();
  setToken("routines-lazy-test");
  Object.assign(getState(), { features: [], assistant: { machine_id: "server", conversations: [] }, machines: [] });
  vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL) => {
    const path = String(input);
    if (path === "/api/v1/assistant/tasks") return Promise.resolve(json({ tasks: [task] }));
    if (path === "/api/v1/assistant/runs") return Promise.resolve(json({ runs: [run] }));
    if (path.startsWith("/api/v1/assistant/runs?ref=")) return Promise.resolve(json({ runs: [run] }));
    if (path === "/api/v1/model-config") return Promise.resolve(json({ providers: [] }));
    throw new Error(`Unexpected request: ${path}`);
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  resetManagerChoice();
  setToken("");
  vi.unstubAllGlobals();
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

describe("Routines TaskEditor lazy boundary", () => {
  it("keeps TaskEditor out of all static imports and loads it via lazyOverlay without idle preloading", () => {
    for (const file of sources(rootDir)) {
      expect(readFileSync(file, "utf8"), file).not.toMatch(staticTaskEditorImport);
    }
    const routines = readFileSync(resolve(__dirname, "Routines.tsx"), "utf8");
    expect(routines).toMatch(/^import\s+\{\s*RunLine\s*\}\s+from\s+["']\.\/RunLine["']/m);
    expect(routines).toContain('lazyOverlay(() => import("./TaskEditor").then((m) => m.TaskEditor))');
    expect(routines).not.toMatch(/import\(["']\.\/TaskEditor["']\)[^;]*idle\s*:\s*true/);
  });

  it("renders tasks and RunLine without loading TaskEditor, then loads and opens TaskEditor on click", async () => {
    await act(async () => root.render(<Routines onToggleSidebar={() => {}} />));
    await flush();

    // First paint renders both the task row and inline RunLine history without loading TaskEditor.
    expect(host.textContent).toContain(task.title);
    expect(host.textContent).toContain(run.title);
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(editorMock.loads).not.toHaveBeenCalled();

    // Clicking the task row triggers the lazy import and opens the real TaskEditor dialog.
    const row = [...host.querySelectorAll<HTMLElement>('[role="button"]')].find((el) => el.getAttribute("aria-label")?.includes(task.title));
    expect(row).toBeDefined();
    await act(async () => row!.click());
    await waitForDialog();
    expect(document.querySelector('[role="dialog"]')).not.toBeNull();
    expect(editorMock.loads).toHaveBeenCalledTimes(1);
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(task.title);

    // Closing unmounts the dialog cleanly; reopening reuses the loaded module without reloading.
    const cancel = [...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find((b) => b.textContent === t("common.cancel"));
    expect(cancel).toBeDefined();
    await act(async () => cancel!.click());
    await flush();
    expect(document.querySelector('[role="dialog"]')).toBeNull();

    await act(async () => row!.click());
    await waitForDialog();
    expect(document.querySelector('[role="dialog"]')).not.toBeNull();
    expect(editorMock.loads).toHaveBeenCalledTimes(1);
  });

  it("preserves TaskEditor's RunLine and RunMark re-exports", async () => {
    const mod = await import("./TaskEditor");
    expect(typeof mod.TaskEditor).toBe("function");
    expect(typeof mod.RunLine).toBe("function");
    expect(typeof mod.RunMark).toBe("function");
  });
});
