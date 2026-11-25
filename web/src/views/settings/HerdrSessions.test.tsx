// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { HerdrSessions, herdrSessionNameError, type HerdrSessionsState } from "./HerdrSessions";
import { t } from "../../i18n";

const calls: { path: string; method: string; json?: { blocked: string[] }; resolve: (v: unknown) => void; reject: (e: Error) => void }[] = [];
vi.mock("../../lib/api", async (orig) => ({
  ...await orig<typeof import("../../lib/api")>(),
  api: (path: string, options?: { method?: string; json?: { blocked: string[] } }) =>
    new Promise((resolve, reject) => calls.push({ path, method: options?.method ?? "GET", json: options?.json, resolve, reject })),
}));
const scope = vi.hoisted(() => ({ machines: [] as { id: string; name: string; online: boolean }[] }));
const bothMachines = [{ id: "computer", name: "Computer", online: true }, { id: "other", name: "Other", online: false }];
vi.mock("../../lib/current-assistant", () => ({ useScopedMachines: () => scope.machines }));

let host: HTMLDivElement, root: Root;
beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  calls.length = 0;
  scope.machines = bothMachines;
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); host.remove(); });

const initial: HerdrSessionsState = {
  sessions: [
    { name: "agent", running: true, blocked: false },
    { name: "review", running: false, blocked: true },
  ],
  blocked: ["review"],
  live: true,
};

async function show(state: HerdrSessionsState = initial) {
  await act(async () => root.render(<HerdrSessions />));
  await act(async () => calls[0].resolve(state));
}

describe("HerdrSessions", () => {
  it("loads detected named sessions and blocks or unblocks one", async () => {
    await show();
    expect(calls[0].path).toBe("/machines/computer/herdr/sessions");
    expect(host.textContent).toContain("agent");
    expect(host.textContent).toContain(t("herdr-sessions.running_visible"));
    expect(host.textContent).toContain(t("herdr-sessions.stopped_blocked"));

    // Click Block on "agent" (first Block button in rows; last Block button is the add form).
    const buttons = [...host.querySelectorAll<HTMLButtonElement>("button")];
    const blockAgent = buttons.find((b) => b.textContent === t("herdr-sessions.block"))!;
    await act(async () => blockAgent.click());
    expect(calls[1]).toMatchObject({
      path: "/machines/computer/herdr/sessions/blocked",
      method: "PUT",
      json: { blocked: ["review", "agent"] },
    });
    await act(async () => calls[1].resolve({
      sessions: [
        { name: "agent", running: true, blocked: true },
        { name: "review", running: false, blocked: true },
      ],
      blocked: ["review", "agent"],
      live: true,
    }));
    expect(host.textContent).toContain(t("herdr-sessions.running_blocked"));
  });

  it("validates typed session names and allows blocking a custom session name", async () => {
    expect(herdrSessionNameError("default", [])).toBe(t("herdr-sessions.name_reserved"));
    expect(herdrSessionNameError("persona-abc", [])).toBe(t("herdr-sessions.name_reserved"));
    expect(herdrSessionNameError("bad/name", [])).toBe(t("herdr-sessions.name_invalid"));
    expect(herdrSessionNameError("review", ["review"])).toBe(t("herdr-sessions.name_exists"));
    expect(herdrSessionNameError("agent", ["review"])).toBe("");

    await show();
    const input = host.querySelector<HTMLInputElement>(`input[aria-label="${t("herdr-sessions.session")}"]`)!;
    const setVal = (v: string) => act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, v);
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await setVal("worker-1");
    const addBtn = [...host.querySelectorAll<HTMLButtonElement>("button")].at(-1)!;
    await act(async () => addBtn.click());
    expect(calls[1]).toMatchObject({
      path: "/machines/computer/herdr/sessions/blocked",
      method: "PUT",
      json: { blocked: ["review", "worker-1"] },
    });
    await act(async () => calls[1].resolve({
      sessions: initial.sessions,
      blocked: ["review", "worker-1"],
      live: true,
    }));
    expect(host.textContent).toContain("worker-1");
    expect(host.textContent).toContain(t("herdr-sessions.not_detected_blocked"));
  });

  it("rolls back on PUT failure and shows a retry button", async () => {
    await show();
    const unblock = [...host.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent === t("herdr-sessions.unblock"))!;
    await act(async () => unblock.click());
    await act(async () => calls[1].reject(new Error("unavailable")));
    expect(host.textContent).toContain("unavailable");
    expect(host.textContent).toContain(t("herdr-sessions.stopped_blocked"));
    expect(host.textContent).toContain(t("herdr-sessions.retry"));
  });

  it("switches computers and shows offline note when live is false", async () => {
    await show();
    const select = host.querySelector<HTMLSelectElement>("select")!;
    await act(async () => { select.value = "other"; select.dispatchEvent(new Event("change", { bubbles: true })); });
    expect(calls.at(-1)!.path).toBe("/machines/other/herdr/sessions");
    await act(async () => calls.at(-1)!.resolve({ sessions: [], blocked: [], live: false }));
    expect(host.textContent).toContain(t("herdr-sessions.empty"));
    expect(host.textContent).toContain(t("herdr-sessions.offline_note"));
  });
});
