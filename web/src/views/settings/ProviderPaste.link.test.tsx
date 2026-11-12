// @vitest-environment jsdom
// Settings › Model providers › Add opened from a provider import link (#5420): the wizard starts on the paste view with
// the link in it, never shown; it is read at once, in one request whose body is the link alone; a draft goes on like a
// paste's; a refused link says so in link words; "Paste something else" leaves it for an empty box; and a hub that
// can't read links says so instead of being asked.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((media: string) => ({ matches: false, media, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
import { t } from "../../i18n";
import { noteText, type PasteDraft, type PasteResult } from "../../lib/provider-paste";
import { resetReadiness } from "../../lib/readiness";
import { getState } from "../../lib/store";
import { ProviderWizard } from "./ProviderWizard";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const LINK = "https://persona.example.invalid/settings?tab=models#import=eyJraW5kIjoiY3VzdG9tIn0";
const FEATURES = ["builtin_presets", "launcher_auto_name", "launcher_import_parse", "launcher_auth_auto", "launcher_import_link"];
const GW: PasteDraft = {
  label: "Example Gateway",
  provider: { kind: "launcher", agent: "claude", api: "messages", display_name: "Example Gateway", base_url: "https://gateway.example.invalid", model: "example-sonnet", models: [{ id: "example-sonnet" }] },
};
const answer = (drafts: PasteDraft[], notes: PasteResult["notes"] = []): PasteResult => ({ format: "link", drafts, notes });

type Call = { method: string; path: string; body?: Record<string, unknown> };
let calls: Call[];
let parse: () => Response | Promise<Response>;
let host: HTMLDivElement;
let root: Root;
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
const flush = async () => { for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); }); };
const text = () => document.body.textContent ?? "";
const button = (label: string) => {
  const found = [...document.body.querySelectorAll("button")].find((b) => b.textContent?.trim() === label);
  expect(found, label).toBeDefined();
  return found!;
};
const parses = () => calls.filter((c) => c.path.endsWith("/launchers/import/parse"));
const click = async (el: Element) => { act(() => (el as HTMLElement).click()); await flush(); };

async function open(features = FEATURES, link: string | null = LINK) {
  Object.assign(getState(), { features });
  act(() => root.render(<ProviderWizard existing="new" link={link} onClose={() => {}} onSaved={() => {}} />));
  await flush();
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  calls = [];
  parse = () => json(answer([GW]));
  resetReadiness();
  vi.stubGlobal("fetch", vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
    const call = { method: init?.method ?? "GET", path: String(url).split("?")[0]!, body: init?.body ? JSON.parse(String(init.body)) : undefined };
    calls.push(call);
    if (call.path.endsWith("/assistant/readiness")) return Promise.resolve(json({ error: "not found" }, 404));
    if (call.path.endsWith("/launchers/presets")) return Promise.resolve(json({ presets: [] }));
    if (call.path.endsWith("/launchers/import/parse")) return Promise.resolve(parse());
    if (call.path.endsWith("/launchers/discover")) return Promise.resolve(json({ models: [] }));
    if (call.path.endsWith("/launchers/test")) return Promise.resolve(json({ ok: true, ms: 90, model: "example-sonnet" }));
    return Promise.resolve(json({}));
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
  Object.assign(getState(), { features: [] });
});

describe("Add opened from an import link", () => {
  it("starts on the paste view, says where it came from, and never shows the link", async () => {
    await open();
    expect(document.body.querySelector("textarea")).toBeNull();
    expect(document.body.querySelector("[data-paste-link]")).not.toBeNull();
    expect(text()).toContain(t("settings-providerpaste.link_title"));
    expect(text()).toContain(t("settings-providerpaste.link_hint"));
    expect(document.body.innerHTML).not.toContain("eyJr");
    expect(document.body.innerHTML).not.toContain("persona.example.invalid");
  });

  it("reads it at once, in one request whose body is the link alone", async () => {
    await open();
    expect(parses()).toEqual([{ method: "POST", path: "/api/v1/launchers/import/parse", body: { text: LINK } }]);
    // the link's line says what it is: no "read as" line under it
    expect(text()).not.toContain(t("settings-providerpaste.format_link"));
    expect(text()).toContain("Example Gateway");
    // the draft goes on like a paste's: the gateway's form, nothing saved
    await click(button(t("settings-providerwizard.paste_continue")));
    expect((document.body.querySelector("input[placeholder]") as HTMLInputElement | null)?.value ?? "").not.toContain("eyJr");
    expect(calls.filter((c) => c.method === "POST" && c.path.endsWith("/api/v1/launchers"))).toHaveLength(0);
  });

  it("says the link is being read while it is", async () => {
    let release!: () => void;
    parse = () => new Promise((r) => (release = () => r(json(answer([GW])))));
    await open();
    expect(text()).toContain(t("settings-providerpaste.link_reading"));
    await act(async () => release());
    await flush();
    expect(text()).not.toContain(t("settings-providerpaste.link_reading"));
    expect(text()).toContain("Example Gateway");
  });

  it("says in link words when nothing in it can be added, and why", async () => {
    parse = () => json(answer([], [{ code: "link_field", name: "endpoints[0].extra" }]));
    await open();
    expect(text()).toContain(t("settings-providerpaste.link_nothing"));
    expect(text()).not.toContain(t("settings-providerpaste.nothing"));
    expect(text()).toContain(noteText({ code: "link_field", name: "endpoints[0].extra" }));
    expect(button(t("settings-providerwizard.paste_continue")).disabled).toBe(true);
  });

  it("leaves the link for an empty box with Paste something else", async () => {
    await open();
    await click(button(t("settings-providerpaste.link_paste_instead")));
    const box = document.body.querySelector("textarea");
    expect(box).not.toBeNull();
    expect(box!.value).toBe("");
    expect(document.body.querySelector("[data-paste-link]")).toBeNull();
    expect(text()).not.toContain("Example Gateway");
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    expect(parses()).toHaveLength(1);
  });

  it("asks a hub that can't read links nothing, and says to update it", async () => {
    await open(FEATURES.filter((f) => f !== "launcher_import_link"));
    expect(parses()).toHaveLength(0);
    expect(text()).toContain(t("settings-providerpaste.link_hub_old"));
    expect(document.body.querySelector("textarea")).toBeNull();
  });

  it("says the same when an older hub has no parse route at all", async () => {
    parse = () => json({ error: "not found" }, 404);
    // the hub's features are not known yet: it is asked
    await open([]);
    expect(parses()).toHaveLength(1);
    expect(text()).toContain(t("settings-providerpaste.link_hub_old"));
  });

  it("drops what an unknown hub answered once its hello says it can't read links", async () => {
    await open([]);
    expect(text()).toContain("Example Gateway");
    Object.assign(getState(), { features: FEATURES.filter((f) => f !== "launcher_import_link") });
    act(() => root.render(<ProviderWizard existing="new" link={LINK} onClose={() => {}} onSaved={() => {}} />));
    await flush();
    expect(text()).not.toContain("Example Gateway");
    expect(text()).toContain(t("settings-providerpaste.link_hub_old"));
  });

  it("starts again on another link opened while it is open", async () => {
    await open();
    await click(button(t("settings-providerwizard.paste_continue")));
    const other = `${LINK}&v=1`;
    act(() => root.render(<ProviderWizard existing="new" link={other} onClose={() => {}} onSaved={() => {}} />));
    await flush();
    expect(document.body.querySelector("[data-paste-link]")).not.toBeNull();
    expect(parses().map((c) => c.body)).toEqual([{ text: LINK }, { text: other }]);
  });

  it("opens on the usual list without a link", async () => {
    await open(FEATURES, null);
    expect(document.body.querySelector("[data-paste-link]")).toBeNull();
    expect(parses()).toHaveLength(0);
    expect(text()).toContain(t("settings-providerwizard.paste_title"));
  });
});

describe("what a refused link says", () => {
  it("names where it is damaged, its version and its unknown field, and says when it is too long", () => {
    expect(noteText({ code: "link_invalid", name: "endpoints[0].baseUrl" })).toBe(t("lib-provider-paste.link_invalid_at", { name: "endpoints[0].baseUrl" }));
    expect(noteText({ code: "link_invalid" })).toBe(t("lib-provider-paste.link_invalid"));
    expect(noteText({ code: "link_version", name: "2" })).toBe(t("lib-provider-paste.link_version", { name: "2" }));
    expect(noteText({ code: "link_version" })).toBe(t("lib-provider-paste.link_version_unknown"));
    expect(noteText({ code: "link_too_long" })).toBe(t("lib-provider-paste.link_too_long"));
    expect(noteText({ code: "link_field", name: "extra" })).toBe(t("lib-provider-paste.link_field", { name: "extra" }));
    expect(noteText({ code: "link_field" })).toBe(t("lib-provider-paste.link_field_unknown"));
    for (const code of ["link_invalid", "link_version", "link_too_long", "link_field"]) expect(noteText({ code })).not.toBe(t("lib-provider-paste.other"));
  });

  it("says a cindy endpoint's own request path can't be set here", () => {
    expect(noteText({ code: "cindy_request_path" })).toBe(t("lib-provider-paste.cindy_request_path"));
    expect(noteText({ code: "cindy_request_path" })).not.toBe(t("lib-provider-paste.other"));
  });
});
