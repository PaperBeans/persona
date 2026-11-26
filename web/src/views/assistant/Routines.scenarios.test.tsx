// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import { setToken } from "../../lib/api";
import { chooseManager, MANAGER_HEADER, resetManagerChoice } from "../../lib/manager-choice";
import type { Task } from "../../lib/routines";
import type { ScenarioCatalog, ScenarioKey } from "../../lib/scenario-routines";
import { getState } from "../../lib/store";
import { Routines } from "./Routines";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
});
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const tool = (name: string) => ({ name, subscribable: true });
const catalog = (connector: string, names: string[]): ScenarioCatalog => ({
  connector,
  status: "ok",
  sources: [{ id: connector, name: connector, tools: names.map(tool) }],
});
const capable = [
  catalog("github", ["search", "read", "diff", "checks"]),
  catalog("mail", ["accounts", "search", "read"]),
];

let host: HTMLDivElement;
let root: Root;
let catalogsReply: () => Response;
let writes: Record<string, unknown>[];
let created: Task[];
let requests: { path: string; method: string; manager: string | null }[];

beforeEach(() => {
  resetManagerChoice();
  setToken("scenario-test");
  Object.assign(getState(), { features: [], assistant: null, machines: [] });
  catalogsReply = () => json({ catalogs: capable });
  writes = [];
  created = [];
  requests = [];
  vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    const method = init?.method ?? "GET";
    const headers = new Headers(init?.headers);
    requests.push({ path, method, manager: headers.get(MANAGER_HEADER) });
    if (path === "/api/v1/assistant") return Promise.resolve(json({ conversations: [] }));
    if (path === "/api/v1/assistant/runs") return Promise.resolve(json({ runs: [] }));
    if (path === "/api/v1/subscriptions/catalog") return Promise.resolve(catalogsReply());
    if (path === "/api/v1/model-config") return Promise.resolve(json({ providers: [] }));
    if (path === "/api/v1/assistant/tasks" && method === "GET") return Promise.resolve(json({ tasks: created }));
    if (path === "/api/v1/assistant/tasks" && method === "POST") {
      const body = JSON.parse(String(init?.body)) as Record<string, unknown>;
      writes.push(body);
      const task = { ...body, id: `created-${writes.length}` } as unknown as Task;
      created = [task];
      return Promise.resolve(json({ task }));
    }
    throw new Error(`Unexpected request: ${method} ${path}`);
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
const button = (label: string) => {
  const buttons = [...document.querySelectorAll<HTMLButtonElement>("button")];
  return buttons.find((item) => item.textContent?.trim() === label)
    ?? buttons.find((item) => item.getAttribute("role") === "menuitem" && item.textContent?.includes(label));
};
const click = async (label: string) => {
  const target = button(label);
  expect(target, label).toBeDefined();
  await act(async () => target!.click());
  await flush();
};
const field = (label: string) => [...document.querySelectorAll("label")].find((item) => item.textContent?.startsWith(label))?.querySelector<HTMLInputElement | HTMLSelectElement>("input,select");
const change = async (label: string, value: string) => {
  const input = field(label);
  expect(input, label).toBeDefined();
  await act(async () => {
    const prototype = input instanceof HTMLSelectElement ? HTMLSelectElement.prototype : HTMLInputElement.prototype;
    Object.getOwnPropertyDescriptor(prototype, "value")!.set!.call(input, value);
    input!.dispatchEvent(new Event(input instanceof HTMLSelectElement ? "change" : "input", { bubbles: true }));
  });
  await flush();
};
const openScenario = async (key: ScenarioKey) => {
  await click(t("assistant-routines.new"));
  await click(t(`assistant-scenario-templates.${key}_title`));
  expect(document.querySelector('[role="dialog"]')?.textContent).toContain(t(`assistant-scenario-templates.${key}_title`));
};
const review = () => click(t("assistant-scenario-templates.review"));
const add = () => click(t("assistant-taskeditor.add"));

describe("scenario template UI", () => {
  it.each([
    { key: "daily-digest" as const, repeat: "daily", time: "08:30", every_minutes: 0 },
    { key: "inbox-triage" as const, repeat: "interval", time: "", every_minutes: 30 },
    { key: "pr-review" as const, repeat: "interval", time: "", every_minutes: 120 },
  ])("configures, reviews, and explicitly creates $key without side effects", async ({ key, ...schedule }) => {
    await act(async () => root.render(<Routines onToggleSidebar={() => {}} />));
    await flush();
    await openScenario(key);
    expect(requests.filter((request) => request.method === "POST")).toEqual([]);
    expect(document.querySelector('[role="dialog"]')?.textContent).toContain(t("assistant-scenario-templates.safe_default"));
    expect(field(t("settings-modelconfiguration.specific_model"))).toBeUndefined();

    if (key === "inbox-triage") {
      await change(t("assistant-scenario-templates.scope_label"), "Unread support messages");
      await change(t("assistant-scenario-templates.interval_label"), "30");
    }
    if (key === "pr-review") {
      await change(t("assistant-scenario-templates.source_label"), "github");
      expect(button(t("assistant-scenario-templates.review"))?.disabled).toBe(true);
      await change(t("assistant-scenario-templates.scope_label"), "example/project");
    }
    await review();
    // The scenario's editor is a lazy chunk; wait for its actual textarea, not the review dialog's first commit.
    await vi.waitFor(async () => {
      await act(async () => {});
      expect(document.querySelector("textarea")).not.toBeNull();
    });
    expect(document.querySelector("textarea")?.value).toContain(t("assistant-scenario-templates.read_only_safety"));
    if (key === "inbox-triage") expect(document.querySelector("textarea")?.value).toContain("Unread support messages");
    if (key === "pr-review") expect(document.querySelector("textarea")?.value).toContain("example/project");
    expect(writes).toEqual([]);

    await add();
    expect(writes).toHaveLength(1);
    expect(writes[0]).toMatchObject({
      title: t(`assistant-scenario-templates.${key}_title`),
      enabled: false,
      permission_mode: "plan",
      push: "asked",
      ...schedule,
    });
    expect(writes[0].prompt).toEqual(expect.stringContaining(t("assistant-scenario-templates.read_only_safety")));
    for (const name of ["launcher", "model", "effort", "cwd", "execution_machine_id"]) expect(writes[0]).not.toHaveProperty(name);
    expect(requests.filter((request) => request.method === "POST").map((request) => request.path)).toEqual(["/api/v1/assistant/tasks"]);
  });

  it("explains missing GitHub capabilities and creates only the conversation fallback", async () => {
    catalogsReply = () => json({ catalogs: [] });
    await act(async () => root.render(<Routines onToggleSidebar={() => {}} />));
    await flush();
    await openScenario("pr-review");
    expect(document.querySelector('[role="status"]')?.textContent).toContain("GitHub");
    await change(t("assistant-scenario-templates.source_label"), "github");
    expect(document.querySelector('[role="alert"]')?.textContent).toContain("GitHub");
    expect(button(t("assistant-scenario-templates.review"))?.disabled).toBe(true);
    expect(writes).toEqual([]);

    await click(t("assistant-scenario-templates.use_fallback"));
    expect(field(t("assistant-scenario-templates.source_label"))?.value).toBe("conversation");
    expect(button(t("assistant-scenario-templates.review"))?.disabled).toBe(false);
    await review();
    expect(document.querySelector("textarea")?.value).toContain(t("assistant-scenario-templates.access_conversation"));
    await add();
    expect(writes).toHaveLength(1);
    expect(writes[0].prompt).not.toContain("github_search");
    expect(writes[0]).toMatchObject({ enabled: false, permission_mode: "plan" });
  });

  it("explains a failed capability check and keeps a built-in source usable", async () => {
    catalogsReply = () => json({ error: "catalog unavailable" }, 503);
    await act(async () => root.render(<Routines onToggleSidebar={() => {}} />));
    await flush();
    await openScenario("daily-digest");
    expect(document.body.textContent).toContain(t("assistant-scenario-templates.capability_check_failed"));
    await change(t("assistant-scenario-templates.source_label"), "mail");
    expect(button(t("assistant-scenario-templates.review"))?.disabled).toBe(true);
    await click(t("assistant-scenario-templates.use_fallback"));
    expect(field(t("assistant-scenario-templates.source_label"))?.value).toBe("persona");
    await review();
    await add();
    expect(writes).toHaveLength(1);
  });

  it("cancel leaves no routine and performs no write", async () => {
    await act(async () => root.render(<Routines onToggleSidebar={() => {}} />));
    await flush();
    await openScenario("inbox-triage");
    await click(t("common.cancel"));
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(writes).toEqual([]);
    expect(requests.filter((request) => request.method !== "GET")).toEqual([]);
  });

  it("scopes the capability catalog request to the same non-default manager as task creation", async () => {
    chooseManager("m-2");
    await act(async () => root.render(<Routines onToggleSidebar={() => {}} />));
    await flush();
    await openScenario("pr-review");
    await change(t("assistant-scenario-templates.scope_label"), "example/project");
    await review();
    await add();
    const catalogRead = requests.find((request) => request.path === "/api/v1/subscriptions/catalog" && request.method === "GET");
    const taskWrite = requests.find((request) => request.path === "/api/v1/assistant/tasks" && request.method === "POST");
    expect(catalogRead?.manager).toBe("m-2");
    expect(taskWrite?.manager).toBe("m-2");
  });

  it.each(["inbox-triage", "pr-review"] as const)(
    "normalizes interval minutes after switching %s to Daily and back to Interval",
    async (key) => {
      await act(async () => root.render(<Routines onToggleSidebar={() => {}} />));
      await flush();
      await openScenario(key);
      if (key === "pr-review") await change(t("assistant-scenario-templates.source_label"), "conversation");
      await change(t("assistant-scenario-templates.schedule_kind"), "daily");
      await change(t("assistant-scenario-templates.schedule_kind"), "interval");
      expect(field(t("assistant-scenario-templates.interval_label"))?.value).toBe("30");
      await review();
      await add();
      expect(writes).toHaveLength(1);
      expect(writes[0]).toMatchObject({ repeat: "interval", time: "", every_minutes: 30 });
    },
  );
});
