// @vitest-environment jsdom
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
vi.mock("./ConfigurationManager", () => ({ ConfigurationManager: ({ children }: { children: (id: string) => unknown }) => children("assistant") }));
vi.mock("./ModelProfiles", () => ({ ModelProfiles: () => null }));
vi.mock("../../lib/store", () => ({ useStore: (select: (s: { machines: [] }) => unknown) => select({ machines: [] }) }));
vi.mock("../../ui/atoms", async (original) => ({ ...(await original<typeof import("../../ui/atoms")>()), toast: vi.fn() }));
import { initI18n, t } from "../../i18n";
import type { ModelBinding, ModelProvider, ModelPurpose } from "../../lib/model-config";
import { ModelConfiguration } from "./ModelConfiguration";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const agents: ModelPurpose = {
  id: "agent.new", category_id: "agent.new", name: "Agents", group: "Agents", kind: "launcher", launch: "engine", revision: 99,
  override: { provider: "launcher.default", model: "default-model", effort: "medium" },
  effective: { provider: "launcher.default", model: "default-model", effort: "medium" },
  fields: ["provider", "model", "effort"],
};
const providers: ModelProvider[] = [
  { id: "launcher.default", name: "Default launcher", kind: "launcher" },
  { id: "launcher.custom", name: "Custom launcher", kind: "launcher" },
  { id: "api.messages", name: "Messages provider", kind: "api", api: "messages", model: "api-default" },
  { id: "launcher.codex", name: "Codex", kind: "launcher", agent: "codex" },
  { id: "api.responses", name: "Responses provider", kind: "api", api: "responses" },
];
const lark = (override: ModelBinding = {}, revision = 7): ModelPurpose => ({
  id: "lark.agent", name: "Lark /agent", group: "Agents", kind: "launcher", launch: "headless", parent: "agent.new", category_id: "agent.new",
  override, effective: { ...agents.effective, ...override }, revision,
  inherited_from: Object.keys(override).length ? undefined : "agent.new",
  fields: ["provider", "model", "effort"], provider_ids: providers.filter((p) => p.id !== "api.responses").map((p) => p.id),
});
let current: ModelPurpose;
let readonly: ModelPurpose;
let conflict: boolean;
let listFails: boolean;
let writes: { path: string; method: string; body: Record<string, unknown> }[];
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status });
const hub = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
  const path = String(input).split("?")[0];
  if (init?.method === "PATCH") {
    const body = JSON.parse(String(init.body)) as Record<string, unknown>;
    writes.push({ path, method: init.method, body });
    if (conflict) return json({ error: "revision conflict" }, 409);
    expect(body.revision).toBe(current.revision);
    const override: ModelBinding = {};
    for (const field of ["provider", "model", "effort"] as const) {
      if (typeof body[field] === "string") override[field] = body[field];
    }
    current = lark(override, current.revision + 1);
    return json({ purpose: current, providers });
  }
  if (path.endsWith("/model-config")) return json({ purposes: [
    { ...agents, id: "assistant", name: "主对话", launch: "headless", runtimes: ["cli", "agentsdk"] },
    agents, current, readonly,
    { ...agents, id: "kb", name: "信息处理", kind: "api", effective: { provider: "api.messages" } },
    { ...agents, id: "extension.example", name: "Example extension", kind: "api", effective: { provider: "api.responses" } },
  ], providers });
  if (path.endsWith("/purposes/lark.agent")) return json({ purpose: current, providers });
  if (path.endsWith("/purposes/inbox.agent")) return json({ purpose: readonly, providers });
  if (path.endsWith("/models")) return listFails ? json({ error: "unavailable" }, 404) : json({
    agent: "claude", models: [
      { id: "default-model", name: "Default model", default: true, efforts: ["low", "medium", "high"] },
      { id: "custom-model", name: "Custom model", efforts: ["low", "high"] },
    ],
  });
  throw new Error(`Unexpected request: ${path}`);
};
let host: HTMLDivElement;
let root: Root;
const flush = async () => { for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); }); };
const dialog = () => document.querySelector<HTMLElement>('[role="dialog"]')!;
const button = (label: string, scope: ParentNode = document) => [...scope.querySelectorAll("button")].find((b) => b.textContent === label)!;
const row = () => [...host.querySelectorAll("button")].find((b) => b.textContent?.includes("Lark /agent"))!;
const effort = () => dialog().querySelector<HTMLInputElement>('input[list="model-efforts"]')!;
function type(input: HTMLInputElement, value: string) {
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
}
async function choose(id: string) {
  await act(async () => {
    const select = dialog().querySelector("select")!;
    select.value = id;
    select.dispatchEvent(new Event("change", { bubbles: true }));
  });
  await flush();
}
async function open() {
  await act(async () => row().click());
  await flush();
}
async function render() {
  function Harness() {
    const [open, onOpen] = useState<string | null>(null);
    return <ModelConfiguration open={open} onOpen={onOpen} onProviders={() => {}} />;
  }
  await act(async () => root.render(<Harness />));
  await flush();
}
beforeEach(async () => {
  await initI18n("zh-Hans");
  current = lark();
  readonly = { ...lark(), id: "inbox.agent", name: "Inbox Agent", fields: [] };
  writes = [];
  conflict = false;
  listFails = false;
  vi.stubGlobal("fetch", vi.fn(hub));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
  await initI18n("zh-Hans");
});

describe("Lark /agent model configuration", () => {
  it("shows four execution groups, backend chips and inheritance without nested controls", async () => {
    await render();
    const headings = [...host.querySelectorAll("section h2")].map((h) => h.textContent);
    expect(headings).toEqual(["Persona 对话 · 后台进程", "Agents · 交互式", "后台处理 · 直接 API", "扩展"]);
    expect(host.textContent).toContain("后台进程 · claude -p");
    expect(row().textContent).toContain("后台进程 · claude -p");
    expect(row().querySelector(".rounded-full")?.classList.contains("whitespace-normal")).toBe(true);
    expect(row().textContent).toContain("继承自 Agents 的模型 · 以后台进程运行");
    const background = host.querySelector('[data-model-purpose="assistant"]')!.closest("section")!;
    expect(background.querySelector('[data-model-purpose="lark.agent"]')).not.toBeNull();
    expect(background.querySelector('[data-model-purpose="agent.new"]')).toBeNull();
    expect(host.querySelector('[data-model-purpose="agent.new"]')?.textContent).toContain("新建 Agent");
    expect(host.textContent).toContain("直接 API · Messages");
    expect(host.textContent).toContain("直接 API · Responses");
    expect(host.querySelector("button button")).toBeNull();
  });

  it("opens help on hover and click, dismisses with Escape or outside click, without opening the editor", async () => {
    await render();
    const help = host.querySelector<HTMLButtonElement>('button[aria-label="Lark /agent的执行形态说明"]')!;
    const details = () => document.getElementById(help.getAttribute("aria-controls")!);
    await act(async () => help.dispatchEvent(new MouseEvent("mouseover", { bubbles: true })));
    expect(help.getAttribute("aria-expanded")).toBe("true");
    expect(details()?.textContent).toContain("工作目录");
    expect(details()?.textContent).toContain("当前：claude -p（Default launcher）");
    expect(details()?.textContent).toContain("未验证");
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    await act(async () => help.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    expect(details()).toBeNull();
    await act(async () => help.click());
    expect(details()).not.toBeNull();
    await act(async () => help.click());
    expect(details()).toBeNull();
    await act(async () => help.click());
    await act(async () => document.body.dispatchEvent(new Event("pointerdown", { bubbles: true })));
    expect(details()).toBeNull();
  });

  it("updates backend detail when the provider changes while keeping headless form and server compatibility", async () => {
    await render();
    await open();
    expect(dialog().querySelector('[data-execution-preview]')?.textContent).toBe("后台进程 · claude -p");
    expect([...dialog().querySelectorAll("select option")].map((o) => o.getAttribute("value"))).not.toContain("api.responses");
    await choose("launcher.codex");
    expect(dialog().querySelector('[data-execution-preview]')?.textContent).toBe("后台进程 · codex app-server");
    await act(async () => button(t("common.save"), dialog()).click());
    await flush();
    expect(row().textContent).toContain("后台进程 · codex app-server");
    expect(row().textContent).toContain("单独设置");
  });

  it.each([
    ["en", "Follow Agents", "Separately configured"],
    ["zh-Hans", "跟随 Agents", "单独设置"],
    ["ja", "Agentsに従う", "個別設定"],
  ])("localizes following and independent states in %s", async (lang, follow, separate) => {
    await initI18n(lang);
    await render();
    expect(row().textContent).toContain(t("settings-modelconfiguration.lark_headless_inheritance"));
    expect(row().querySelector("span")?.className).not.toContain("hidden");
    await open();
    expect(dialog().textContent).toContain(t("settings-modelconfiguration.inherited_from", { name: "Agents" }));
    expect(button(follow, dialog())).toBeDefined();
    await act(async () => button(t("common.cancel"), dialog()).click());
    // A model-only override is independent even when its effective value matches Agents.
    current = lark({ model: "default-model" });
    await act(async () => host.querySelector<HTMLButtonElement>(`button[aria-label="${t("settings-modelconfiguration.refresh")}"]`)!.click());
    await flush();
    expect(row().textContent).toContain(separate);
    await open();
    expect(dialog().textContent).toContain(separate);
  });

  it("edits the child record's launcher, model and effort without redirecting to its category", async () => {
    await render();
    await open();
    await choose("launcher.custom");
    await act(async () => dialog().querySelector<HTMLButtonElement>("button[aria-expanded]")!.click());
    await flush();
    await act(async () => [...dialog().querySelectorAll<HTMLButtonElement>('[role="option"]')].find((b) => b.textContent?.includes("Custom model"))!.click());
    type(effort(), "high");
    await act(async () => button(t("common.save"), dialog()).click());
    await flush();
    expect(writes).toEqual([{ path: "/api/v1/model-config/purposes/lark.agent", method: "PATCH", body: {
      revision: 7, provider: "launcher.custom", model: "custom-model", effort: "high",
    } }]);
    expect(row().textContent).toContain(t("settings-modelconfiguration.separately_configured"));
    expect(agents.revision).toBe(99);
  });

  it("allows an agent-capable Messages provider with a model and effort", async () => {
    await render();
    await open();
    await choose("api.messages");
    type(dialog().querySelector<HTMLInputElement>(`input[aria-label="${t("settings-modelconfiguration.specific_model")}"]`)!, "api-model");
    type(effort(), "low");
    await act(async () => button(t("common.save"), dialog()).click());
    expect(writes[0].body).toEqual({ revision: 7, provider: "api.messages", model: "api-model", effort: "low" });
  });

  it("restores following by clearing all three fields using the child's revision", async () => {
    current = lark({ provider: "launcher.custom", model: "custom-model", effort: "high" }, 12);
    await render();
    await open();
    await act(async () => button(t("settings-modelconfiguration.follow_agents"), dialog()).click());
    await flush();
    expect(writes[0].body).toEqual({ revision: 12, provider: null, model: null, effort: null });
    expect(current.override).toEqual({});
    expect(row().textContent).toContain(t("settings-modelconfiguration.lark_headless_inheritance"));
    await open();
    expect(dialog().textContent).toContain(t("settings-modelconfiguration.inherited_from", { name: "Agents" }));
    expect(dialog().querySelector("select")!.value).toBe("");
    expect(effort().value).toBe("");
  });

  it("keeps conflicting edits and blocks save/reset until reloading the latest child revision", async () => {
    listFails = true;
    await render();
    await open();
    const model = () => dialog().querySelector<HTMLInputElement>(`input[aria-label="${t("settings-modelconfiguration.specific_model")}"]`)!;
    type(model(), "draft-model");
    type(effort(), "high");
    conflict = true;
    await act(async () => button(t("common.save"), dialog()).click());
    expect(model().value).toBe("draft-model");
    expect(effort().value).toBe("high");
    expect(button(t("common.save"), dialog()).disabled).toBe(true);
    expect(button(t("settings-modelconfiguration.follow_agents"), dialog()).disabled).toBe(true);
    expect(dialog().textContent).toContain(t("settings-modelconfiguration.conflict"));
    current = lark({ model: "elsewhere" }, 8);
    conflict = false;
    await act(async () => button(t("settings-modelconfiguration.reload_discard"), dialog()).click());
    expect(model().value).toBe("elsewhere");
    type(model(), "new-draft");
    await act(async () => button(t("common.save"), dialog()).click());
    expect(writes.map((w) => w.body.revision)).toEqual([7, 8]);
  });

  it("keeps other children read-only when their fields are empty", async () => {
    await render();
    const other = [...host.querySelectorAll("button")].find((b) => b.textContent?.includes("Inbox Agent"))!;
    await act(async () => other.click());
    expect(dialog().querySelector("select")).toBeNull();
    expect(dialog().querySelector("input")).toBeNull();
    expect(button(t("common.save"), dialog()).disabled).toBe(true);
    expect(button(t("settings-modelconfiguration.restore"), dialog()).disabled).toBe(true);
    expect(writes).toEqual([]);
  });
});
