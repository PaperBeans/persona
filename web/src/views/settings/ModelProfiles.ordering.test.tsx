// @vitest-environment jsdom
import { act, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const hub = vi.hoisted(() => ({ events: new Set<() => void>(), reload: vi.fn() }));
vi.mock("../../lib/store", () => ({ onHubEvent: (type: string, listener: () => void) => {
  if (type === "model_profile") hub.events.add(listener);
  return () => { hub.events.delete(listener); };
} }));
vi.mock("../../lib/edge-auth", async (original) => ({ ...await original<typeof import("../../lib/edge-auth")>(), reloadForSignIn: hub.reload }));
import { handleUnauthorized, setPairedToken, setToken } from "../../lib/api";
import { CREDENTIAL_EPOCH_KEY } from "../../lib/credential-epoch";
import { chooseManager, resetManagerChoice } from "../../lib/manager-choice";
import { initI18n, t } from "../../i18n";
import { ModelProfiles } from "./ModelProfiles";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
window.matchMedia ??= ((media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {} })) as typeof window.matchMedia;

type Call = { method: string; path: string; headers: Headers; answer: (response: Response) => void };
let host: HTMLDivElement, root: Root, calls: Call[];
const unauthorized = vi.fn(), onChanged = vi.fn();
const data = (active: string, revision: number | undefined = 1) => ({
  ...(revision === undefined ? {} : { revision }), active,
  profiles: ["performance", "economy"].map((id) => ({ id, name: id, active: id === active, changes: [], tiers: {} })),
  rollback: { to: active === "economy" ? "performance" : "economy", changes: [] },
});
const flush = async () => { for (let i = 0; i < 8; i++) await act(async () => { await Promise.resolve(); }); };
const button = (text: string, scope: ParentNode = document) => [...scope.querySelectorAll("button")].find((b) => b.textContent?.includes(text))!;
const row = (id: string) => button(id, host);
const active = (id: string) => row(id)?.textContent?.includes(t("settings-modelprofiles.active")) ?? false;
const reads = () => calls.filter((c) => c.method === "GET");
const writes = () => calls.filter((c) => c.method === "POST");
const lastRead = () => reads().at(-1)!;
const dialog = () => document.querySelector('[role="dialog"]')!;
const alert = () => document.querySelector('[role="alert"]');
async function answerRaw(call: Call, response: Response) { expect(call).toBeTruthy(); await act(async () => call.answer(response)); await flush(); }
async function answer(call: Call, value: unknown, status = 200) { await answerRaw(call, Response.json(value, { status })); }
async function event() { await act(async () => hub.events.forEach((listener) => listener())); await flush(); }
async function render(manager = "assistant") { await act(async () => root.render(<ModelProfiles manager={manager} onChanged={onChanged} />)); await flush(); }
async function select(id: string) { await act(async () => row(id).click()); await flush(); expect(dialog()).toBeTruthy(); }
async function apply(id = "economy") { await select(id); await act(async () => button(t("settings-modelprofiles.apply"), dialog()).click()); await flush(); }
async function rollback() { await act(async () => button(t("settings-modelprofiles.rollback"), host).click()); await flush(); }

beforeEach(async () => {
  localStorage.clear(); resetManagerChoice(); setToken("synthetic-A"); calls = []; hub.events.clear(); hub.reload.mockClear(); unauthorized.mockClear(); onChanged.mockClear();
  handleUnauthorized(unauthorized); await initI18n();
  // Deliberately ignore AbortSignal: a retired transport can still complete.
  vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => new Promise<Response>((resolve) => calls.push({
    method: init?.method ?? "GET", path: String(input), headers: new Headers(init?.headers), answer: resolve,
  }))));
  host = document.createElement("div"); document.body.append(host); root = createRoot(host); await render();
});
afterEach(async () => {
  await act(async () => root.unmount()); host.remove(); document.body.innerHTML = ""; handleUnauthorized(() => {}); setToken(""); resetManagerChoice(); vi.unstubAllGlobals();
});

describe("model profile freshness", () => {
  it.each([
    ["apply", 1], ["rollback", 1], ["apply", null], ["rollback", null],
  ] as const)("keeps %s and its rollback preview after an older read (revision %s)", async (operation, version) => {
    const revision = version ?? undefined;
    const before = operation === "apply" ? "performance" : "economy";
    const after = operation === "apply" ? "economy" : "performance";
    // Explicitly omit the revision for old hubs.
    const snapshot = (value: string) => version === null ? (({ revision: _revision, ...rest }) => rest)(data(value)) : data(value, revision);
    await answer(reads()[0], snapshot(before)); await event(); const old = lastRead();
    if (operation === "apply") await apply(); else await rollback();
    expect(writes()[0].path).toContain(operation === "apply" ? "/economy/apply" : "/rollback");
    await answer(writes()[0], snapshot(after));
    await event(); const fresh = lastRead(); expect(fresh).not.toBe(old);
    await answer(fresh, snapshot(after)); expect(active(after)).toBe(true);
    await answer(old, snapshot(before));
    expect(active(after)).toBe(true); expect(active(before)).toBe(false);
    expect(button(t("settings-modelprofiles.rollback"), host).parentElement?.parentElement?.textContent).toContain(before);
    expect(alert()).toBeNull();
  });

  it("does not let an older event read replace a newer event read", async () => {
    await answer(reads()[0], data("performance")); await event(); const old = lastRead(); await event();
    await answer(lastRead(), data("economy")); expect(active("economy")).toBe(true);
    await answer(old, data("performance")); expect(active("economy")).toBe(true);
  });

  it("keeps the current read error when an older success arrives", async () => {
    await answer(reads()[0], data("performance")); await event(); const old = lastRead(); await event();
    await answer(lastRead(), { error: "Current read refusal" }, 400);
    await answer(old, data("economy"));
    expect(active("performance")).toBe(true); expect(alert()?.textContent).toContain("Current read refusal");
  });

  it("ignores an older read failure after a confirmed switch", async () => {
    await answer(reads()[0], data("performance")); await event(); const old = lastRead();
    await apply(); await answer(writes()[0], data("economy")); expect(active("economy")).toBe(true);
    await answer(old, { code: "internal", error: "synthetic obsolete read failure" }, 500);
    expect(alert()).toBeNull();
  });

  it("keeps a newer event result when the initial read fails", async () => {
    const old = reads()[0]; await event(); await answer(lastRead(), data("economy"));
    await answer(old, { error: "synthetic failure" }, 400);
    expect(active("economy")).toBe(true); expect(alert()).toBeNull();
  });

  it("reads again after an event during a pending switch", async () => {
    await answer(reads()[0], data("performance")); await apply(); const count = reads().length;
    await event(); expect(reads()).toHaveLength(count);
    await answer(writes()[0], data("economy")); expect(reads()).toHaveLength(count + 1);
    // The post-write read can reveal a later external commit, rather than asserting the user's intent won.
    await answer(lastRead(), data("performance")); expect(active("performance")).toBe(true);
  });

  it("does not let an old read's completion release a pending action", async () => {
    await answer(reads()[0], data("performance")); await event(); const old = lastRead(); await apply();
    await answer(old, data("performance"));
    expect(button(t("settings-modelprofiles.apply"), dialog()).disabled).toBe(true);
    await answer(writes()[0], data("economy")); expect(active("economy")).toBe(true);
  });

  it("retires the first effect's read under StrictMode", async () => {
    await act(async () => root.render(<StrictMode><ModelProfiles manager="assistant" onChanged={onChanged} /></StrictMode>)); await flush();
    const old = reads().at(-2)!; const current = lastRead();
    await answer(current, data("economy")); await answer(old, data("performance"));
    expect(active("economy")).toBe(true);
  });

  it("reports a current read error, then clears it on a successful event retry", async () => {
    await answer(reads()[0], { error: "Synthetic read refusal" }, 400);
    expect(alert()?.textContent).toContain("Synthetic read refusal");
    await event(); await answer(lastRead(), data("performance"));
    expect(active("performance")).toBe(true); expect(alert()).toBeNull();
  });

  it("keeps old-hub 404 behavior and loads when a later event succeeds", async () => {
    await answer(reads()[0], { error: "unsupported" }, 404); expect(host.textContent).toBe("");
    await event(); const { revision: _revision, ...oldHub } = data("performance"); await answer(lastRead(), oldHub);
    expect(active("performance")).toBe(true); expect(alert()).toBeNull();
  });

  it("preserves a current action failure across refresh and allows retry", async () => {
    await answer(reads()[0], data("performance")); await apply();
    await answer(writes()[0], { error: "Synthetic switch refusal" }, 400);
    expect(dialog().querySelector('[role="alert"]')?.textContent).toContain("Synthetic switch refusal");
    await answer(lastRead(), data("performance"));
    expect(dialog().querySelector('[role="alert"]')?.textContent).toContain("Synthetic switch refusal");
    await act(async () => button(t("settings-modelprofiles.apply"), dialog()).click()); await flush();
    expect(writes()).toHaveLength(2); await answer(writes()[1], data("economy"));
    expect(dialog()).toBeNull(); expect(active("economy")).toBe(true); expect(alert()).toBeNull();
  });

  it("preserves a selection made during rollback and admits only one write at a time", async () => {
    await answer(reads()[0], data("economy"));
    await act(async () => { button(t("settings-modelprofiles.rollback"), host).click(); button(t("settings-modelprofiles.rollback"), host).click(); }); await flush();
    expect(writes()).toHaveLength(1); await select("economy");
    expect(button(t("settings-modelprofiles.apply"), dialog()).disabled).toBe(true);
    await answer(writes()[0], data("performance"));
    expect(dialog().textContent).toContain("economy"); expect(button(t("settings-modelprofiles.apply"), dialog()).disabled).toBe(false);
    await act(async () => button(t("settings-modelprofiles.apply"), dialog()).click()); await flush();
    expect(writes()).toHaveLength(2); await answer(writes()[1], data("economy"));
    expect(active("economy")).toBe(true);
  });

  it("normally switches a profile and shows its new active value", async () => {
    await answer(reads()[0], data("performance")); await apply(); await answer(writes()[0], data("economy"));
    expect(active("economy")).toBe(true); expect(active("performance")).toBe(false); expect(onChanged).toHaveBeenCalledTimes(1);
  });
});

describe("profile ownership and authentication", () => {
  it("retires a manager round trip synchronously even before React commits a new view", async () => {
    const old = reads()[0];
    await act(async () => {
      chooseManager("work"); chooseManager(null);
      old.answer(Response.json({ error: "retired" }, { status: 401 }));
      await Promise.resolve();
    });
    await flush(); expect(lastRead()).not.toBe(old); await answer(lastRead(), data("economy"));
    expect(active("economy")).toBe(true); expect(unauthorized).not.toHaveBeenCalled();
  });

  it("retires manager A reads after A to B to A, even on 401 or edge login", async () => {
    const old = reads()[0]; await render("work"); const middle = lastRead(); await render("assistant"); const current = lastRead();
    expect([old, middle, current].map((call) => call.headers.get("X-Persona-Manager"))).toEqual(["assistant", "work", "assistant"]);
    await answer(current, data("economy")); await answer(old, { error: "retired" }, 401);
    await answerRaw(middle, new Response(null, { status: 302, headers: { Location: "/synthetic-login" } }));
    expect(active("economy")).toBe(true); expect(unauthorized).not.toHaveBeenCalled(); expect(hub.reload).not.toHaveBeenCalled();
    expect(onChanged).not.toHaveBeenCalled();
  });

  it("retires A credential reads after A to B to A and captures each authorization", async () => {
    const old = reads()[0]; await act(async () => setToken("synthetic-B")); await flush(); const middle = lastRead();
    await act(async () => setToken("synthetic-A")); await flush(); const current = lastRead();
    expect([old, middle, current].map((call) => call.headers.get("Authorization"))).toEqual(["Bearer synthetic-A", "Bearer synthetic-B", "Bearer synthetic-A"]);
    await answer(current, data("economy")); await answer(old, { error: "retired" }, 401);
    await answerRaw(middle, new Response("<html>synthetic login</html>", { status: 200, headers: { "Content-Type": "text/html" } }));
    expect(active("economy")).toBe(true); expect(unauthorized).not.toHaveBeenCalled(); expect(hub.reload).not.toHaveBeenCalled();
  });

  it("retires a read after pairing again with the same credential", async () => {
    const old = reads()[0]; await act(async () => setPairedToken("synthetic-A")); await flush(); const current = lastRead();
    expect(current).not.toBe(old); expect(current.headers.get("Authorization")).toBe("Bearer synthetic-A");
    await answer(current, data("economy")); await answer(old, { error: "retired" }, 401);
    expect(active("economy")).toBe(true); expect(unauthorized).not.toHaveBeenCalled();
  });

  it("retires a read when another tab replaces the credential epoch", async () => {
    const old = reads()[0];
    await act(async () => {
      localStorage.setItem(CREDENTIAL_EPOCH_KEY, "synthetic-epoch");
      window.dispatchEvent(new StorageEvent("storage", { key: CREDENTIAL_EPOCH_KEY }));
    });
    await flush(); const current = lastRead(); expect(current).not.toBe(old);
    await answer(current, data("economy")); await answer(old, { error: "retired" }, 401);
    expect(active("economy")).toBe(true); expect(unauthorized).not.toHaveBeenCalled();
  });

  it("does not let a superseded same-owner read sign out or reload the current view", async () => {
    const old = reads()[0]; await event(); const next = lastRead(); await event(); const current = lastRead();
    await answer(current, data("economy")); await answer(old, { error: "retired" }, 401);
    await answerRaw(next, new Response(null, { status: 302, headers: { Location: "/synthetic-login" } }));
    expect(active("economy")).toBe(true); expect(unauthorized).not.toHaveBeenCalled(); expect(hub.reload).not.toHaveBeenCalled();
  });

  it("keeps current JSON 401 recovery and current edge login handling", async () => {
    await answer(reads()[0], { error: "current" }, 401); expect(unauthorized).toHaveBeenCalledOnce(); expect(hub.reload).not.toHaveBeenCalled();
    await event(); await answerRaw(lastRead(), new Response(null, { status: 302, headers: { Location: "/synthetic-login" } }));
    expect(hub.reload).toHaveBeenCalledOnce();
  });

  it("keeps current write 401 recovery", async () => {
    await answer(reads()[0], data("performance")); await apply();
    await answer(writes()[0], { error: "current switch refusal" }, 401);
    expect(unauthorized).toHaveBeenCalledOnce(); expect(hub.reload).not.toHaveBeenCalled();
    expect(dialog().querySelector('[role="alert"]')?.textContent).toContain("current switch refusal");
  });

  it("fences a retired write and blocks overlapping same-manager writes across remounts", async () => {
    await answer(reads()[0], data("performance")); await apply(); const first = writes()[0];
    await render("work"); await answer(lastRead(), data("performance"));
    await render("assistant"); await answer(lastRead(), data("performance")); await select("economy");
    expect(button(t("settings-modelprofiles.apply"), dialog()).disabled).toBe(true); expect(writes()).toHaveLength(1);
    await answer(first, data("economy")); expect(onChanged).not.toHaveBeenCalled();
    expect(button(t("settings-modelprofiles.apply"), dialog()).disabled).toBe(false);
    await answer(lastRead(), data("economy")); expect(active("economy")).toBe(true);
    await act(async () => button(t("settings-modelprofiles.apply"), dialog()).click()); await flush();
    expect(writes()).toHaveLength(2); await answer(writes()[1], data("economy")); expect(onChanged).toHaveBeenCalledOnce();
  });

  it("does not let a retired credential write 401 sign out the replacement", async () => {
    await answer(reads()[0], data("performance")); await apply(); const first = writes()[0];
    await act(async () => setToken("synthetic-B")); await flush(); await act(async () => setToken("synthetic-A")); await flush();
    await answer(lastRead(), data("performance")); await answer(first, { error: "retired" }, 401);
    expect(unauthorized).not.toHaveBeenCalled(); expect(onChanged).not.toHaveBeenCalled(); expect(alert()).toBeNull();
    await answer(lastRead(), data("performance")); expect(active("performance")).toBe(true);
  });

  it("does not let a retired manager write redirect reload its replacement", async () => {
    await answer(reads()[0], data("performance")); await apply(); const old = writes()[0];
    await render("work"); await answer(lastRead(), data("performance"));
    await answerRaw(old, new Response(null, { status: 302, headers: { Location: "/synthetic-login" } }));
    expect(hub.reload).not.toHaveBeenCalled(); expect(onChanged).not.toHaveBeenCalled(); expect(alert()).toBeNull();
  });
});
