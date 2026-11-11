// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, beforeEach, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import type { PlanTask, TaskPlan } from "../../lib/feed";
import { noteConversation, resetManagerChoice } from "../../lib/manager-choice";
import { getState } from "../../lib/store";
import { loadPages } from "../../lib/lazy-page";

const startPlan = vi.fn(async (..._args: unknown[]) => {});
vi.mock("../../lib/feed", async (orig) => ({
  ...(await orig<typeof import("../../lib/feed")>()),
  startPlan: (...args: unknown[]) => startPlan(...args),
}));
const { PlanCard } = await import("./Tasks");
beforeAll(() => loadPages(), 60_000);
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let root: Root;
let host: HTMLDivElement;
let requests: { path: string; body: { tasks: Partial<PlanTask>[] }; manager: string | null }[];
const task: PlanTask = { title: "Fix login", prompt: "Fix login", machine: "box", cwd: "/code/app", workspace: "new",
  workspace_resolved: { machine: "box", path: "/wt/app/task-todo_example", branch: "task/todo_example" } };
const plan: TaskPlan = { id: "p-workspace", state: "open", conversation: "workspace-chat", tasks: [task] };
const flush = async () => { for (let i = 0; i < 8; i++) await act(async () => { await Promise.resolve(); }); };
const tick = async () => { await act(async () => vi.advanceTimersByTime(250)); await flush(); };
const choose = async (value: string) => {
  const select = host.querySelector<HTMLSelectElement>("[data-task-workspace-editor] select")!;
  await act(async () => { select.value = value; select.dispatchEvent(new Event("change", { bubbles: true })); });
  await tick();
};
const click = async (label: string) => {
  await act(async () => [...host.querySelectorAll("button")].find((b) => b.textContent === label)!.click());
  await flush();
};
const sent = () => startPlan.mock.calls.at(-1)?.[2] as Partial<PlanTask>[] | undefined;
beforeEach(() => {
  vi.useFakeTimers();
  startPlan.mockClear();
  requests = [];
  resetManagerChoice();
  noteConversation("workspace-chat", "work");
  Object.assign(getState(), { machines: [{ id: "box", name: "Box" }], features: [] });
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path.includes("/workspaces")) return new Response(JSON.stringify({ workspaces: [
      { id: "ws-one", machine_id: "box", title: "Login", cwd: "/code/other", branch: "fix/login", available: true },
      { id: "ws-other-machine", machine_id: "other", title: "Other machine", cwd: "/wrong", available: true },
    ] }));
    const body = JSON.parse(String(init?.body));
    requests.push({ path, body, manager: new Headers(init?.headers).get("X-Persona-Manager") });
    const edit = body.tasks[0];
    const selected = edit.workspace;
    const resolved = selected === "none" ? edit.cwd : selected === "new" ? "/wt/app/task-todo_example"
      : "existing" in selected ? (selected.existing === "ws-one" ? "/code/other" : selected.existing) : "/wt/app/custom-branch";
    return new Response(JSON.stringify({ plan: { ...plan, tasks: [{ ...task, ...edit, workspace_resolved: { machine: edit.machine, path: resolved } }] } }));
  }));
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove(); resetManagerChoice(); vi.useRealTimers(); vi.unstubAllGlobals();
});
async function render(value = plan) { await act(async () => root.render(<PlanCard plan={value} />)); await tick(); }

it("shows the engine preview and sends each new/existing/none choice on Start", async () => {
  await render();
  expect(host.querySelector("[data-workspace-path]")?.textContent).toBe("/wt/app/task-todo_example");
  await choose("existing");
  expect(host.querySelector("[data-workspace-path]")?.textContent).toBe("/code/app");
  expect(host.textContent).not.toContain("Other machine");
  await click(t("assistant-tasks.plan_start"));
  expect(sent()?.[0].workspace).toEqual({ existing: "/code/app" });
  await choose("none");
  await click(t("assistant-tasks.plan_start"));
  expect(sent()?.[0].workspace).toBe("none");
  await choose("new");
  await click(t("assistant-tasks.plan_start"));
  expect(sent()?.[0].workspace).toBe("new");
  expect(sent()?.[0]).not.toHaveProperty("workspace_resolved");
  expect(requests.every((r) => r.manager === "work")).toBe(true);
  expect(requests.every((r) => r.path.endsWith("/workspace-preview"))).toBe(true);
});

it("selects a machine-local existing workspace ID and shows its resolved absolute path", async () => {
  await render();
  await choose("existing");
  const select = host.querySelector<HTMLSelectElement>(`select[aria-label="${t("assistant-tasks.workspace_select")}"]`)!;
  await act(async () => { select.value = "ws-one"; select.dispatchEvent(new Event("change", { bubbles: true })); });
  await tick();
  expect(host.querySelector("[data-workspace-path]")?.textContent).toBe("/code/other");
  await click(t("assistant-tasks.plan_start"));
  expect(sent()?.[0].workspace).toEqual({ existing: "ws-one" });
});

it("keeps the card's policy when opening Edit and submits it with the other edits", async () => {
  await render();
  await choose("none");
  await click(t("assistant-tasks.plan_edit"));
  const select = document.querySelector<HTMLSelectElement>('[role="dialog"] [data-task-workspace-editor] select')!;
  expect(select.value).toBe("none");
  await act(async () => [...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find((b) => b.textContent === t("assistant-tasks.plan_start"))!.click());
  await flush();
  expect(sent()?.[0].workspace).toBe("none");
});

it("shows the actual path on a started plan without editable controls", async () => {
  await render({ ...plan, state: "started", tasks: [{ ...task, cwd: "/engine/actual", workspace_resolved: { machine: "box", path: "/engine/actual", created: true } }] });
  expect(host.querySelector("[data-task-workspace]")?.textContent).toContain("/engine/actual");
  expect(host.querySelector("select")).toBeNull();
});
