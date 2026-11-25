// @vitest-environment jsdom
// Cross-engine and Antigravity regressions in ProviderWizard (#15870):
// 1. Switching provider kind clears untouched seeded engine defaults (Codex/omp), preserves user models across all
//    engines (including Antigravity), and never injects a phantom default model when unchecked models are present.
// 2. Hubs without launcher_auth_auto still hide the Anthropic auth selector for Codex, Antigravity, Responses, and Chat.
// 3. Test connection and Fetch models stay disabled when base_url is empty/whitespace/cleared on optional-URL engines,
//    while Next and Save still succeed.
// 4. Advanced hides Claude-only tier/subagent controls for Codex, Antigravity, and omp (with Claude positive control).
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((media: string) => ({ matches: false, media, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
import { initI18n, t } from "../../i18n";
import type { ProviderView } from "../../lib/providers";
import { resetReadiness } from "../../lib/readiness";
import { getState } from "../../lib/store";
import { ProviderWizard } from "./ProviderWizard";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

type Call = { method: string; path: string; body?: Record<string, unknown> };
let calls: Call[];
let host: HTMLDivElement;
let root: Root;

const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
const flush = async () => { for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); }); };
const buttonByKey = (key: string) => {
  const found = [...document.body.querySelectorAll("button")].find((el) => el.textContent?.trim().startsWith(t(key)));
  expect(found, key).toBeDefined();
  return found!;
};
const clickKey = (key: string) => act(() => buttonByKey(key).click());
const clickKind = (label: string) => {
  const radio = [...document.body.querySelectorAll<HTMLButtonElement>('[role="radio"]')].find((el) => el.textContent?.trim() === label);
  expect(radio, label).toBeDefined();
  act(() => radio!.click());
};
const authGroup = () => document.body.querySelector<HTMLElement>(`[role="radiogroup"][aria-label="${t("settings-providerwizard.auth")}"]`);
const input = (selector: string) => {
  const found = document.body.querySelector<HTMLInputElement>(selector);
  expect(found, selector).not.toBeNull();
  return found!;
};
const type = (el: HTMLInputElement, value: string) => {
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(el, value);
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
};
const modelIds = () => [...document.body.querySelectorAll<HTMLElement>("[data-model]")].map((el) => el.getAttribute("data-model"));
const defaultModelId = () =>
  [...document.body.querySelectorAll<HTMLElement>("[data-model]")]
    .find((row) => row.textContent?.includes(t("settings-providerwizard.default")))
    ?.getAttribute("data-model") ?? "";

async function draw(existing: ProviderView | "new" | null) {
  act(() => root.render(<ProviderWizard existing={existing} onClose={() => {}} onSaved={() => {}} />));
  await flush();
}

beforeEach(async () => {
  await initI18n("en");
  calls = [];
  Object.assign(getState(), { features: ["builtin_presets", "launcher_auto_name", "launcher_auth_auto"], machines: [] });
  resetReadiness();
  vi.stubGlobal("fetch", vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
    const call: Call = { method: init?.method ?? "GET", path: String(url).split("?")[0]!, body: init?.body ? JSON.parse(String(init.body)) : undefined };
    calls.push(call);
    if (call.path.endsWith("/launchers/presets")) return Promise.resolve(json({ presets: [] }));
    if (call.path.endsWith("/launchers/discover")) return Promise.resolve(json({ models: [] }));
    if (call.path.endsWith("/launchers/test")) return Promise.resolve(json({ ok: true, ms: 50, model: "m1" }));
    if (call.path === "/api/v1/launchers" && call.method === "POST") return Promise.resolve(json({ launcher: { name: "saved-gw" } }));
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
});

describe("ProviderWizard cross-engine and Antigravity behavior (#15870)", () => {
  it("clears untouched seeded defaults across engines, preserves user models including on Antigravity, and avoids phantom defaults", async () => {
    await draw("new");
    clickKey("settings-providerwizard.custom");

    // Codex -> omp replaces Codex's untouched seed with omp's default.
    clickKind("Codex");
    clickKind("omp");
    clickKey("settings-providerwizard.next");
    expect(modelIds()).toEqual(["google-antigravity/gemini-4-argon:high"]);
    expect(defaultModelId()).toBe("google-antigravity/gemini-4-argon:high");

    // Back -> Codex replaces omp's untouched seed with gpt-5.4.
    clickKey("settings-providerwizard.back");
    clickKind("Codex");
    clickKey("settings-providerwizard.next");
    expect(modelIds()).toEqual(["gpt-5.4"]);
    expect(defaultModelId()).toBe("gpt-5.4");

    // Back -> Antigravity clears Codex's untouched seed instead of carrying gpt-5.4 into Antigravity.
    clickKey("settings-providerwizard.back");
    clickKind("Antigravity");
    clickKey("settings-providerwizard.next");
    expect(modelIds()).toEqual([]);

    // Adding a custom model on Antigravity and switching Antigravity -> Codex -> Antigravity preserves it.
    type(input(`input[placeholder="${t("settings-providerwizard.add_model_id")}"]`), "gemini-4-pro");
    clickKey("settings-providerwizard.add");
    expect(modelIds()).toEqual(["gemini-4-pro"]);
    clickKey("settings-providerwizard.back");
    clickKind("Codex");
    clickKind("Antigravity");
    clickKey("settings-providerwizard.next");
    expect(modelIds()).toEqual(["gemini-4-pro"]);
    expect(defaultModelId()).toBe("gemini-4-pro");

    // Unchecking the only model (non-empty models, empty default) and switching to Codex must NOT inject phantom default "gpt-5.4".
    const checkbox = document.body.querySelector<HTMLButtonElement>('[data-model="gemini-4-pro"] button[role="checkbox"]')!;
    act(() => checkbox.click());
    expect(defaultModelId()).toBe("");
    clickKey("settings-providerwizard.back");
    clickKind("Codex");
    clickKey("settings-providerwizard.next");
    expect(modelIds()).toEqual(["gemini-4-pro"]);
    expect(defaultModelId()).toBe("");
    expect(document.body.textContent).toContain(t("lib-providers.add_model"));
    expect(document.body.textContent).not.toContain(t("lib-providers.default_unchecked"));

    // Fresh form: Codex -> Claude Code (with URL+key) does not inherit Codex's untouched seed.
    await draw(null);
    await draw("new");
    clickKey("settings-providerwizard.custom");
    clickKind("Codex");
    clickKind("Claude Code");
    type(input('input[inputmode="url"]'), "https://gw.example.com");
    type(input('input[type="password"]'), "sk-example-123456");
    clickKey("settings-providerwizard.next");
    expect(modelIds()).toEqual([]);

    // Custom model on Claude Code survives switching to Direct API (and re-clicking active Direct API keeps protocol).
    type(input(`input[placeholder="${t("settings-providerwizard.add_model_id")}"]`), "custom-model-1");
    clickKey("settings-providerwizard.add");
    clickKey("settings-providerwizard.back");
    clickKind(t("settings-providerwizard.direct_api"));
    clickKind("OpenAI Responses");
    clickKind(t("settings-providerwizard.direct_api"));
    expect(document.body.querySelector('[role="radio"][aria-checked="true"]')?.textContent).not.toBe("Anthropic Messages");
    clickKey("settings-providerwizard.next");
    expect(modelIds()).toEqual(["custom-model-1"]);
  });

  it("matches asksAuth on hubs without launcher_auth_auto (hides auth for Codex, Antigravity, Responses, and Chat)", async () => {
    Object.assign(getState(), { features: ["builtin_presets", "launcher_auto_name"] });
    await draw("new");
    clickKey("settings-providerwizard.custom");

    // Claude Code and omp ask for Bearer / x-api-key.
    expect(authGroup()).not.toBeNull();
    clickKind("omp");
    expect(authGroup(), "omp").not.toBeNull();

    // Codex and Antigravity launch with their own key env vars: no Anthropic auth selector.
    for (const agent of ["Codex", "Antigravity"]) {
      clickKind(agent);
      expect(authGroup(), agent).toBeNull();
    }

    // Direct API: Messages asks auth; OpenAI Responses and Chat Completions hide it.
    clickKind(t("settings-providerwizard.direct_api"));
    expect(authGroup(), "Direct API Messages").not.toBeNull();
    clickKind("OpenAI Responses");
    expect(authGroup(), "OpenAI Responses").toBeNull();
    clickKind(t("settings-providerwizard.protocol_chat"));
    expect(authGroup(), "Chat Completions").toBeNull();
  });

  it("disables Test connection and Fetch models when base_url is empty, whitespace, or cleared, while Next and Save still work", async () => {
    await draw("new");
    clickKey("settings-providerwizard.custom");

    for (const agent of ["Codex", "Antigravity", "omp"]) {
      clickKind(agent);
      const testBtn = buttonByKey("settings-providerwizard.test");
      const discoverBtn = buttonByKey("settings-providerwizard.discover");
      expect(testBtn.disabled, `${agent} test disabled with empty URL`).toBe(true);
      expect(discoverBtn.disabled, `${agent} discover disabled with empty URL`).toBe(true);
      act(() => {
        testBtn.click();
        discoverBtn.click();
      });
    }

    // Whitespace-only URL also keeps probe buttons disabled.
    clickKind("Antigravity");
    type(input('input[inputmode="url"]'), "   ");
    expect(buttonByKey("settings-providerwizard.test").disabled).toBe(true);
    expect(buttonByKey("settings-providerwizard.discover").disabled).toBe(true);
    expect(calls.filter((c) => /\/launchers\/(test|discover)$/.test(c.path))).toHaveLength(0);

    // Providing a valid base_url enables both probe buttons; clearing it disables them again.
    type(input('input[inputmode="url"]'), "https://relay.example.com");
    expect(buttonByKey("settings-providerwizard.test").disabled).toBe(false);
    expect(buttonByKey("settings-providerwizard.discover").disabled).toBe(false);
    type(input('input[inputmode="url"]'), "");
    expect(buttonByKey("settings-providerwizard.test").disabled).toBe(true);
    expect(buttonByKey("settings-providerwizard.discover").disabled).toBe(true);

    // Optional-URL Codex provider still advances to Next and saves without base_url.
    clickKind("Codex");
    clickKey("settings-providerwizard.next");
    clickKey("common.save");
    await flush();
    expect(calls.find((c) => c.method === "POST" && c.path === "/api/v1/launchers")?.body).toMatchObject({
      kind: "launcher",
      agent: "codex",
      model: "gpt-5.4",
    });
  });

  it("hides Claude-only tier and subagent controls for Codex, Antigravity, and omp while keeping them for Claude Code", async () => {
    const tierLabels = [
      t("settings-providerwizard.tier_model", { tier: "Opus" }),
      t("settings-providerwizard.tier_model", { tier: "Sonnet" }),
      t("settings-providerwizard.tier_model", { tier: "Haiku" }),
      t("settings-providerwizard.tier_model", { tier: "Fable" }),
      t("settings-providerwizard.small_model"),
      t("settings-providerwizard.force_subagent"),
    ];

    for (const agent of ["codex", "agy", "omp"] as const) {
      await draw(null);
      await draw({
        name: `${agent}-provider`,
        kind: "launcher",
        agent,
        base_url: "https://relay.example.com",
        has_key: true,
        model: "m1",
        models: [{ id: "m1" }],
      });
      clickKey("settings-providerwizard.next");
      expect(document.body.textContent, `${agent} should not show Claude tiers section`).not.toContain(t("settings-providerwizard.tiers_title"));
      clickKey("settings-providerwizard.advanced");
      for (const label of tierLabels) {
        expect(document.body.querySelector(`[aria-label="${label}"]`), `${agent} should not show ${label}`).toBeNull();
      }
      expect(document.body.querySelector('input[placeholder="http://127.0.0.1:7890"]'), `${agent} proxy`).not.toBeNull();
    }

    // Positive control: Claude Code renders Claude tiers section, Fable tier select, and Force subagent switch.
    await draw(null);
    await draw({
      name: "claude-provider",
      kind: "launcher",
      agent: "claude",
      base_url: "https://relay.example.com",
      has_key: true,
      model: "m1",
      models: [{ id: "m1" }],
    });
    clickKey("settings-providerwizard.next");
    expect(document.body.textContent).toContain(t("settings-providerwizard.tiers_title"));
    clickKey("settings-providerwizard.advanced");
    expect(document.body.querySelector(`[aria-label="${t("settings-providerwizard.tier_model", { tier: "Fable" })}"]`)).not.toBeNull();
    expect(document.body.querySelector(`button[role="switch"][aria-label="${t("settings-providerwizard.force_subagent")}"]`)).not.toBeNull();
  });
});
