// @vitest-environment jsdom
/// <reference types="node" />
// Machines renders cards on mount while loading AddComputer, HelpDialog (ReadinessHelp), and MachineDeploy strictly on
// demand via lazyDialog / lazyOverlay (no idle preload), leaving AddComputer.tsx, MachineDeploy.tsx, and Readiness.tsx
// untouched.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, resolve } from "node:path";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { initI18n, t } from "../i18n";
import { resetManagerChoice, settleRosterDefault } from "../lib/manager-choice";
import { resetRoster } from "../lib/managers";
import type { Machine, Session } from "../lib/types";

const mocks = vi.hoisted(() => ({
  addComputerLoads: vi.fn(),
  helpLoads: vi.fn(),
  deployRenders: vi.fn(),
}));

vi.mock("./machines/AddComputer", async (importOriginal) => {
  mocks.addComputerLoads();
  return importOriginal<typeof import("./machines/AddComputer")>();
});

vi.mock("./assistant/ReadinessHelp", async (importOriginal) => {
  mocks.helpLoads();
  return importOriginal<typeof import("./assistant/ReadinessHelp")>();
});

vi.mock("./machines/MachineDeploy", async (importOriginal) => {
  const real = await importOriginal<typeof import("./machines/MachineDeploy")>();
  return {
    ...real,
    MachineDeploy: (props: Parameters<typeof real.MachineDeploy>[0]) => {
      mocks.deployRenders(props.machine.id);
      return real.MachineDeploy(props);
    },
  };
});

vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
  const kept = new Map<string, string>();
  (globalThis as Record<string, unknown>).localStorage = {
    getItem: (k: string) => kept.get(k) ?? null,
    setItem: (k: string, v: string) => void kept.set(k, v),
    removeItem: (k: string) => void kept.delete(k),
    clear: () => kept.clear(),
  };
});

const fake = vi.hoisted(() => ({ machines: [] as Machine[], features: [] as string[], sessions: [] as Session[] }));
vi.mock("../lib/store", async (importOriginal) => {
  const real = await importOriginal<typeof import("../lib/store")>();
  const state = () => ({ ...real.getState(), loaded: true, machines: fake.machines, features: fake.features });
  return { ...real, getState: state, refresh: () => Promise.resolve(), useStore: <T,>(select: (s: ReturnType<typeof real.getState>) => T) => select(state()) };
});

vi.mock("../lib/sessions", async (importOriginal) => {
  const real = await importOriginal<typeof import("../lib/sessions")>();
  return { ...real, useFullAgentSessions: () => ({ sessions: fake.sessions, loaded: true, error: "" }) };
});

import { Machines } from "./Machines";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const rootDir = resolve(__dirname, "..");
const staticAddComputerImport = /^import\s+(?!type\b)[^;]*from\s+["'][./]*(?:views\/)?(?:machines\/)?AddComputer["']/m;
const staticReadinessImport = /^import\s+(?!type\b)[^;]*from\s+["']\.\/assistant\/Readiness(?:Help)?["']/m;
const staticMachineDeployImport = /^import\s+(?!type\b)[^;]*from\s+["']\.\/machines\/MachineDeploy["']/m;

function sources(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) return sources(p);
    return /\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name) ? [p] : [];
  });
}

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

let host: HTMLDivElement;
let root: Root;

const flush = async () => {
  for (let i = 0; i < 12; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
};

const waitFor = async (check: () => unknown) => {
  for (let i = 0; i < 80; i++) {
    await act(async () => { await new Promise((r) => setTimeout(r, 10)); });
    if (check()) return;
  }
};

beforeEach(async () => {
  await initI18n("en");
  localStorage.clear();
  resetRoster();
  resetManagerChoice();
  settleRosterDefault();
  fake.machines = [];
  fake.features = [];
  fake.sessions = [];
  mocks.addComputerLoads.mockClear();
  mocks.helpLoads.mockClear();
  mocks.deployRenders.mockClear();
  vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL) => {
    const path = String(input).split("?")[0];
    if (path === "/api/v1/metrics") return Promise.resolve(json({ machines: [] }));
    if (path === "/api/v1/launchers/health") return Promise.resolve(json({ records: [] }));
    return Promise.resolve(json({ error: "not found" }, 404));
  }));
  history.replaceState(null, "", "/machines");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(async () => {
  act(() => root.unmount());
  host.remove();
  document.body.innerHTML = "";
  resetRoster();
  resetManagerChoice();
  vi.unstubAllGlobals();
  await initI18n("zh-Hans");
});

describe("Machines lazy dialog and deployment overlay boundaries", () => {
  it("keeps AddComputer out of all static imports and keeps HelpDialog and MachineDeploy off Machines.tsx without idle preloading", () => {
    for (const file of sources(rootDir)) {
      expect(readFileSync(file, "utf8"), file).not.toMatch(staticAddComputerImport);
    }
    const source = readFileSync(resolve(__dirname, "Machines.tsx"), "utf8");
    expect(source).toMatch(/^import\s+type\s+\{\s*PendingComputer\s*\}\s+from\s+["']\.\/machines\/AddComputer["']/m);
    expect(source).not.toMatch(staticReadinessImport);
    expect(source).not.toMatch(staticMachineDeployImport);
    expect(source).toContain('lazyDialog(() => import("./machines/AddComputer").then((m) => m.AddComputer))');
    expect(source).toContain('lazyOverlay(() => import("./assistant/ReadinessHelp").then((m) => m.HelpDialog))');
    expect(source).toContain('import("./machines/MachineDeploy")');
    expect(source).not.toMatch(/import\(["']\.\/machines\/(?:AddComputer|MachineDeploy)["']\)[^;]*idle\s*:\s*true/);
    expect(source).not.toMatch(/import\(["']\.\/assistant\/Readiness(?:Help)?["']\)[^;]*idle\s*:\s*true/);
  });

  it("paints healthy machines without loading AddComputer, HelpDialog, or mounting MachineDeploy, then loads AddComputer on click", async () => {
    fake.features = ["machine_trust", "machines_add"];
    fake.machines = [{ id: "studio", name: "Studio Box", online: true }];

    await act(async () => root.render(<Machines onToggleSidebar={() => {}} />));
    await flush();

    expect(host.textContent).toContain("Studio Box");
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(mocks.addComputerLoads).not.toHaveBeenCalled();
    expect(mocks.helpLoads).not.toHaveBeenCalled();
    expect(mocks.deployRenders).not.toHaveBeenCalled();

    const addButton = [...host.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === t("machines.add"));
    expect(addButton).toBeDefined();
    await act(async () => addButton!.click());
    await waitFor(() => document.querySelector('[role="dialog"]'));

    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(t("machines-addcomputer.title"));
    expect(mocks.addComputerLoads).toHaveBeenCalledTimes(1);
    expect(mocks.helpLoads).not.toHaveBeenCalled();

    // Closing dismisses cleanly; reopening reuses the already loaded AddComputer module.
    const cancelButton = [...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find((b) => b.textContent?.trim() === t("machines-addcomputer.cancel"));
    expect(cancelButton).toBeDefined();
    await act(async () => cancelButton!.click());
    await flush();
    expect(document.querySelector('[role="dialog"]')).toBeNull();

    await act(async () => addButton!.click());
    await waitFor(() => document.querySelector('[role="dialog"]'));
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(t("machines-addcomputer.title"));
    expect(mocks.addComputerLoads).toHaveBeenCalledTimes(1);
  });

  it("loads HelpDialog only when Add computer is clicked on a hub without machines_add", async () => {
    fake.features = [];
    fake.machines = [];

    await act(async () => root.render(<Machines onToggleSidebar={() => {}} />));
    await flush();

    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(mocks.helpLoads).not.toHaveBeenCalled();
    expect(mocks.addComputerLoads).not.toHaveBeenCalled();

    const addButton = [...host.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === t("machines.add"));
    expect(addButton).toBeDefined();
    await act(async () => addButton!.click());
    await waitFor(() => document.querySelector('[role="dialog"]'));

    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(t("assistant-readiness.help_add_title"));
    expect(mocks.helpLoads).toHaveBeenCalledTimes(1);
    expect(mocks.addComputerLoads).not.toHaveBeenCalled();

    const gotItButton = [...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find((b) => b.textContent?.trim() === t("assistant-readiness.got_it"));
    expect(gotItButton).toBeDefined();
    await act(async () => gotItButton!.click());
    await flush();
    expect(document.querySelector('[role="dialog"]')).toBeNull();

    await act(async () => addButton!.click());
    await waitFor(() => document.querySelector('[role="dialog"]'));
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(t("assistant-readiness.help_add_title"));
    expect(mocks.helpLoads).toHaveBeenCalledTimes(1);
  });

  it("mounts MachineDeploy only when a machine has a visible deployment banner and preserves Redeploy and Details", async () => {
    fake.machines = [{ id: "node-1", name: "Build Node", online: true }];
    await act(async () => root.render(<Machines onToggleSidebar={() => {}} />));
    await flush();
    expect(mocks.deployRenders).not.toHaveBeenCalled();
    expect(host.querySelector('[data-testid="deploy-details"]')).toBeNull();

    fake.machines = [{ id: "node-1", name: "Build Node", online: true, can_deploy: true, node_mismatch: true }];
    await act(async () => root.render(<Machines onToggleSidebar={() => {}} />));
    await waitFor(() => host.querySelector('[data-testid="deploy-details"]'));

    expect(mocks.deployRenders).toHaveBeenCalledWith("node-1");
    expect(host.textContent).toContain(t("lib-machine-deploy.redeploy"));

    // Clicking Details in the lazily mounted MachineDeploy strip still opens the alert sheet on mismatch.
    const details = host.querySelector<HTMLButtonElement>('[data-testid="deploy-details"]');
    expect(details).not.toBeNull();
    await act(async () => details!.click());
    await flush();
    expect(document.querySelector('[data-testid="alert-sheet"]')?.getAttribute("data-kind")).toBe("mismatch");
    expect(location.pathname).toBe("/machines");

    // Clearing the mismatch unmounts the strip again.
    fake.machines = [{ id: "node-1", name: "Build Node", online: true }];
    await act(async () => root.render(<Machines onToggleSidebar={() => {}} />));
    await flush();
    expect(host.querySelector('[data-testid="deploy-details"]')).toBeNull();
  });

  it("opens AddComputer in relink mode when clicking New code on a pending computer", async () => {
    fake.features = ["machine_trust", "machines_add"];
    fake.machines = [{ id: "pending-1", name: "Spare Mac", online: false, kind: "link", trust: "pending", error_code: "never_connected" }];

    await act(async () => root.render(<Machines onToggleSidebar={() => {}} />));
    await flush();
    expect(document.querySelector('[role="dialog"]')).toBeNull();

    const newCodeButton = [...host.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === t("machines.new_code"));
    expect(newCodeButton).toBeDefined();
    await act(async () => newCodeButton!.click());
    await waitFor(() => document.querySelector('[role="dialog"]'));
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(t("machines-addcomputer.title_again", { name: "Spare Mac" }));
  });

  it("resolves all three dynamic imports to their exported components", async () => {
    const [addMod, helpMod, deployMod] = await Promise.all([
      import("./machines/AddComputer"),
      import("./assistant/ReadinessHelp"),
      import("./machines/MachineDeploy"),
    ]);
    expect(typeof addMod.AddComputer).toBe("function");
    expect(typeof helpMod.HelpDialog).toBe("function");
    expect(typeof deployMod.MachineDeploy).toBe("function");
  });
});
