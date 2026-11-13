// @vitest-environment jsdom
// Settings › Model providers › a custom gateway's sign-in method (#5329, docs/integrations/launchers.md): on a hub with
// launcher_auth_auto a new gateway starts on Automatic, the Connect step's check finds Bearer or x-api-key, a line names
// it and the save stores it; the choice waits under "More options". A saved provider shows its method and keeps it unless
// it is changed; an older hub keeps the two-way choice on the step and is never sent "auto".
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((media: string) => ({ matches: false, media, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
import { t } from "../../i18n";
import type { ProviderView } from "../../lib/providers";
import { resetReadiness } from "../../lib/readiness";
import { getState } from "../../lib/store";
import { ProviderWizard } from "./ProviderWizard";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

type Call = { method: string; path: string; body?: Record<string, unknown> };
const NEW_HUB = ["builtin_presets", "launcher_auto_name", "launcher_auth_auto"];
const OLD_HUB = ["builtin_presets", "launcher_auto_name"];
const saved: ProviderView = { name: "gw", display_name: "Team gateway", base_url: "https://gw.example.com", has_key: true, auth: "bearer", model: "m1", models: [{ id: "m1" }] };
let calls: Call[];
// what "Test connection" answers for a body
let answer: (body: Record<string, unknown>) => Record<string, unknown>;
let host: HTMLDivElement;
let root: Root;
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
const flush = async () => { for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); }); };
const text = () => document.body.textContent ?? "";
const buttons = (key: string) => [...document.body.querySelectorAll("button")].filter((el) => el.textContent?.trim().startsWith(t(key)));
function click(key: string) {
  const [found] = buttons(key);
  expect(found, key).toBeDefined();
  act(() => found!.click());
}
function input(selector: string) {
  const found = document.body.querySelector<HTMLInputElement>(selector);
  expect(found, selector).not.toBeNull();
  return found!;
}
function type(el: HTMLInputElement, value: string) {
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(el, value);
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
}
const urlInput = () => input('input[inputmode="url"]');
const keyInput = () => input('input[type="password"]');
/** The Bearer / x-api-key choice, wherever it is drawn; null when it is not. */
const authGroup = () => document.body.querySelector<HTMLElement>(`[role="radiogroup"][aria-label="${t("settings-providerwizard.auth")}"]`);
const authOptions = () => [...(authGroup()?.querySelectorAll<HTMLButtonElement>('[role="radio"]') ?? [])].map((b) => [b.textContent, b.getAttribute("aria-checked")]);
function pickAuth(label: string) {
  const option = [...authGroup()!.querySelectorAll<HTMLButtonElement>('[role="radio"]')].find((b) => b.textContent === label);
  act(() => option!.click());
}
const moreOptions = () => [...document.body.querySelectorAll<HTMLButtonElement>("button[aria-expanded]")].find((b) => b.textContent?.startsWith(t("settings-providerwizard.more_options")))!;
const probes = (kind: "test" | "discover") => calls.filter((c) => c.path.endsWith(`/launchers/${kind}`));
const sentAuth = () => calls.filter((c) => c.body && "auth" in c.body).map((c) => c.body!.auth);
const foundLine = (method: string) => t("settings-providerwizard.auth_found", { method });
const currentLine = (method: string) => t("settings-providerwizard.auth_current", { method });
// The Connect step's check waits this long after the address and key stop changing (AUTO_TEST_MS in ProviderFields.tsx).
const AUTO = 700;
async function advance(ms: number) {
  await act(async () => { await vi.advanceTimersByTimeAsync(ms); });
  await flush();
}
async function draw(existing: ProviderView | "new" | null) {
  act(() => root.render(<ProviderWizard existing={existing} onClose={() => {}} onSaved={() => {}} />));
  await flush();
}
/** A new custom gateway with its address and key typed, its check run. */
async function newGateway(url = "https://gw.example.com", key = "sk-example-123456") {
  await draw("new");
  click("settings-providerwizard.custom");
  type(urlInput(), url);
  act(() => { urlInput().focus(); urlInput().blur(); });
  type(keyInput(), key);
  await advance(AUTO);
}
async function saveNew() {
  click("settings-providerwizard.next");
  click("common.save");
  await flush();
  return calls.find((c) => c.method === "POST" && c.path.endsWith("/launchers"))!.body!;
}
async function saveEdit() {
  click("common.save");
  await flush();
  return calls.find((c) => c.method === "PUT")!.body!;
}

beforeEach(() => {
  // only timers are faked: promises, fetch and React's scheduling keep running for real
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  calls = [];
  // a gateway that takes x-api-key only: "auto" finds it, Bearer is refused
  answer = (b) => (b.auth === "bearer" ? { ok: false, ms: 80, model: b.model, error: "The gateway refused this key", code: "auth" } : { ok: true, ms: 120, model: b.model, ...(b.auth === "auto" ? { auth: "api_key" } : {}) });
  Object.assign(getState(), { features: NEW_HUB });
  resetReadiness();
  vi.stubGlobal("fetch", vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
    const call: Call = { method: init?.method ?? "GET", path: String(url).split("?")[0]!, body: init?.body ? JSON.parse(String(init.body)) : undefined };
    calls.push(call);
    if (call.path.endsWith("/launchers/presets")) return Promise.resolve(json({ presets: [] }));
    if (call.path.endsWith("/launchers/discover")) return Promise.resolve(json({ models: [{ id: "m1" }] }));
    if (call.path.endsWith("/launchers/test")) return Promise.resolve(json(answer(call.body!)));
    if (call.path.endsWith("/launchers") && call.method === "POST") return Promise.resolve(json({ launcher: { name: "gw-example-com", display_name: "gw.example.com" } }));
    if (call.method === "PUT") return Promise.resolve(json({ launcher: { name: "gw", display_name: "Team gateway" } }));
    return Promise.resolve(json({ error: "not found" }, 404));
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("a new gateway on a hub that finds the sign-in method", () => {
  it("asks nothing on the step: Automatic waits under More options, which says it holds it", async () => {
    await draw("new");
    click("settings-providerwizard.custom");
    expect(authGroup()).toBeNull();
    expect(moreOptions().textContent).toContain(t("settings-providerwizard.more_options_badge_auth"));
    act(() => moreOptions().click());
    expect(authOptions()).toEqual([[t("settings-providerwizard.auth_auto"), "true"], ["Bearer", "false"], ["x-api-key", "false"]]);
    expect(text()).toContain(t("settings-providerwizard.auth_auto_hint"));
    // nothing found yet: no line names a method
    expect(text()).not.toContain(foundLine("Bearer"));
    expect(text()).not.toContain(foundLine("x-api-key"));
  });

  it("finds the method with its check, names it, sends it from then on and saves it", async () => {
    await newGateway();
    expect(probes("discover")[0]!.body).toMatchObject({ auth: "auto" });
    expect(probes("test")[0]!.body).toMatchObject({ auth: "auto", model: "m1" });
    expect(text()).toContain(foundLine("x-api-key"));
    // another test or fetch by hand sends what was found
    act(() => buttons("settings-providerwizard.test")[0]!.click());
    await flush();
    act(() => buttons("settings-providerwizard.discover")[0]!.click());
    await flush();
    expect(probes("test")[1]!.body).toMatchObject({ auth: "api_key" });
    expect(probes("discover")[1]!.body).toMatchObject({ auth: "api_key" });
    expect(text()).toContain(foundLine("x-api-key"));
    // Change opens More options with Automatic still chosen
    click("settings-providerwizard.auth_change");
    expect(moreOptions().getAttribute("aria-expanded")).toBe("true");
    expect(authOptions()[0]).toEqual([t("settings-providerwizard.auth_auto"), "true"]);
    expect(buttons("settings-providerwizard.auth_change")).toHaveLength(0);
    expect((await saveNew()).auth).toBe("api_key");
    expect(sentAuth().filter((a) => a === "auto")).toHaveLength(2);
  });

  it.each(["key", "address"] as const)("forgets the method found when the %s changes, and checks again", async (field) => {
    await newGateway();
    expect(text()).toContain(foundLine("x-api-key"));
    // this one only takes Bearer
    answer = (b) => (b.auth === "api_key" ? { ok: false, ms: 80, model: b.model, error: "The gateway refused this key", code: "auth" } : { ok: true, ms: 90, model: b.model, ...(b.auth === "auto" ? { auth: "bearer" } : {}) });
    if (field === "key") type(keyInput(), "sk-example-654321");
    else type(urlInput(), "https://relay.example.com");
    expect(text()).not.toContain(foundLine("x-api-key"));
    if (field === "address") act(() => { urlInput().focus(); urlInput().blur(); });
    await advance(AUTO);
    expect(probes("discover").at(-1)!.body).toMatchObject({ auth: "auto" });
    expect(probes("test").at(-1)!.body).toMatchObject({ auth: "auto" });
    expect(text()).toContain(foundLine("Bearer"));
    expect((await saveNew()).auth).toBe("bearer");
  });

  it("finds nothing when the check fails: no line, and saving keeps Bearer", async () => {
    answer = (b) => ({ ok: false, ms: 80, model: b.model, error: "The gateway refused this key", code: "auth" });
    await newGateway();
    expect(text()).toContain("The gateway refused this key");
    expect(text()).not.toContain(foundLine("Bearer"));
    expect(text()).not.toContain(foundLine("x-api-key"));
    expect(buttons("settings-providerwizard.auth_change")).toHaveLength(0);
    expect((await saveNew()).auth).toBe("bearer");
  });

  it("drops a test's answer that comes for a method no longer chosen", async () => {
    let release: () => void = () => {};
    await draw("new");
    click("settings-providerwizard.custom");
    type(urlInput(), "https://gw.example.com");
    act(() => { urlInput().focus(); urlInput().blur(); });
    // hold the check's test until the method was changed by hand
    const fetch = globalThis.fetch as ReturnType<typeof vi.fn>;
    const plain = fetch.getMockImplementation() as typeof globalThis.fetch;
    fetch.mockImplementation((url: RequestInfo | URL, init?: RequestInit) => {
      const reply = plain(url, init);
      if (!String(url).endsWith("/launchers/test") || JSON.parse(String(init!.body)).auth !== "auto") return reply;
      return new Promise<Response>((resolve) => { release = () => resolve(reply); });
    });
    type(keyInput(), "sk-example-123456");
    await advance(AUTO);
    expect(probes("test")).toHaveLength(1);
    act(() => moreOptions().click());
    pickAuth("Bearer");
    await act(async () => release());
    await flush();
    expect(text()).not.toContain(foundLine("x-api-key"));
    expect(text()).toContain(currentLine("Bearer"));
  });

  it("names a method chosen by hand and sends it as it is", async () => {
    await newGateway();
    act(() => moreOptions().click());
    pickAuth("Bearer");
    expect(text()).toContain(currentLine("Bearer"));
    expect(text()).not.toContain(foundLine("x-api-key"));
    expect(text()).toContain(t("settings-providerwizard.auth_bearer_hint"));
    // a new method is a new connection: it is checked again with it
    await advance(AUTO);
    expect(probes("discover").at(-1)!.body).toMatchObject({ auth: "bearer" });
    expect(probes("test").at(-1)!.body).toMatchObject({ auth: "bearer" });
    expect(text()).toContain("The gateway refused this key");
    pickAuth("x-api-key");
    await advance(AUTO);
    expect(probes("test").at(-1)!.body).toMatchObject({ auth: "api_key" });
    expect(text()).toContain(currentLine("x-api-key"));
    expect((await saveNew()).auth).toBe("api_key");
  });

  it("asks nothing for Responses or Chat Completions, which always use Bearer", async () => {
    await draw("new");
    click("settings-providerwizard.custom");
    act(() => [...document.body.querySelectorAll("button")].find((b) => b.textContent === t("settings-providerwizard.direct_api"))!.click());
    act(() => [...document.body.querySelectorAll("button")].find((b) => b.textContent === "OpenAI Responses")!.click());
    expect(moreOptions().textContent).toContain(t("settings-providerwizard.startup_id"));
    expect(moreOptions().textContent).not.toContain(t("settings-providerwizard.auth"));
    act(() => moreOptions().click());
    expect(authGroup()).toBeNull();
  });
});

describe("a saved gateway on a hub that finds the sign-in method", () => {
  it("names its method, sends nothing on opening, and Change opens More options with it chosen", async () => {
    await draw({ ...saved, auth: "api_key" });
    await advance(AUTO);
    expect(probes("discover")).toHaveLength(0);
    expect(probes("test")).toHaveLength(0);
    expect(authGroup()).toBeNull();
    expect(text()).toContain(currentLine("x-api-key"));
    click("settings-providerwizard.auth_change");
    expect(authOptions()).toEqual([[t("settings-providerwizard.auth_auto"), "false"], ["Bearer", "false"], ["x-api-key", "true"]]);
    // the button that opened it is gone: the choice has the focus
    expect(buttons("settings-providerwizard.auth_change")).toHaveLength(0);
    expect(document.activeElement?.textContent).toBe("x-api-key");
    expect((await saveEdit()).auth).toBe("api_key");
  });

  it("checks which method works once Automatic is chosen, and saves the one found", async () => {
    await draw(saved);
    click("settings-providerwizard.auth_change");
    pickAuth(t("settings-providerwizard.auth_auto"));
    expect(text()).not.toContain(currentLine("Bearer"));
    await advance(AUTO);
    expect(probes("discover")[0]!.body).toMatchObject({ name: "gw", auth: "auto" });
    expect(probes("test")[0]!.body).toMatchObject({ name: "gw", auth: "auto" });
    expect(text()).toContain(foundLine("x-api-key"));
    expect((await saveEdit()).auth).toBe("api_key");
  });

  it("keeps what is stored when Automatic finds nothing", async () => {
    answer = (b) => ({ ok: false, ms: 80, model: b.model, error: "The gateway refused this key", code: "auth" });
    await draw(saved);
    click("settings-providerwizard.auth_change");
    pickAuth(t("settings-providerwizard.auth_auto"));
    await advance(AUTO);
    expect(probes("test")).toHaveLength(1);
    const body = await saveEdit();
    expect(body.auth).toBe("bearer");
    expect(sentAuth().filter((a) => a !== "auto")).toEqual(["bearer"]);
  });
});

describe("an older hub", () => {
  beforeEach(() => {
    Object.assign(getState(), { features: OLD_HUB });
  });

  it("keeps Bearer / x-api-key on the step, Bearer for a new gateway, and never sends Automatic", async () => {
    await newGateway();
    expect(authOptions()).toEqual([["Bearer", "true"], ["x-api-key", "false"]]);
    expect(moreOptions().textContent).toContain(t("settings-providerwizard.startup_id"));
    expect(moreOptions().textContent).not.toContain(t("settings-providerwizard.more_options_badge_auth"));
    expect(buttons("settings-providerwizard.auth_change")).toHaveLength(0);
    expect(text()).not.toContain(currentLine("Bearer"));
    expect(probes("test")[0]!.body).toMatchObject({ auth: "bearer" });
    pickAuth("x-api-key");
    await advance(AUTO);
    expect(probes("test").at(-1)!.body).toMatchObject({ auth: "api_key" });
    expect((await saveNew()).auth).toBe("api_key");
    expect(sentAuth()).not.toContain("auto");
  });

  it("takes Bearer for Automatic when the hub turns out not to find the method", async () => {
    Object.assign(getState(), { features: NEW_HUB });
    await draw("new");
    click("settings-providerwizard.custom");
    Object.assign(getState(), { features: OLD_HUB });
    await draw("new");
    expect(authOptions()).toEqual([["Bearer", "true"], ["x-api-key", "false"]]);
    type(urlInput(), "https://gw.example.com");
    act(() => { urlInput().focus(); urlInput().blur(); });
    type(keyInput(), "sk-example-123456");
    await advance(AUTO);
    expect(sentAuth()).toEqual(["bearer", "bearer"]);
  });

  it("opens a saved gateway with its method on the step", async () => {
    await draw({ ...saved, auth: "api_key" });
    expect(authOptions()).toEqual([["Bearer", "false"], ["x-api-key", "true"]]);
    expect(text()).not.toContain(currentLine("x-api-key"));
    expect((await saveEdit()).auth).toBe("api_key");
  });
});

describe("an agent that does not launch with the method", () => {
  it("asks nothing for Codex or Antigravity and names no method to the hub: they launch with their own key variables", async () => {
    for (const agent of ["Codex", "Antigravity"]) {
      calls = [];
      await draw(null);
      await draw("new");
      click("settings-providerwizard.custom");
      const kind = [...document.body.querySelectorAll<HTMLButtonElement>('[role="radio"]')].find((b) => b.textContent === agent);
      act(() => kind!.click());
      type(urlInput(), "https://gw.example.com");
      act(() => { urlInput().focus(); urlInput().blur(); });
      type(keyInput(), "sk-example-123456");
      await advance(AUTO);
      act(() => moreOptions().click());
      expect(authGroup(), agent).toBeNull();
      expect(probes("discover").length, agent).toBeGreaterThan(0);
      expect(sentAuth(), agent).toEqual([]);
      expect(text()).not.toContain(foundLine("x-api-key"));
    }
  });
});
