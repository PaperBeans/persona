// @vitest-environment jsdom
// Unified todos_v2 Labs toggle (#14642): lightweight All / To-dos / Tasks filter and hiding empty Uncategorized
// placeholders strictly gated behind the selected assistant's `todos_v2 === true`, with instant zero-migration rollback.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Todo } from "../lib/todos";
import type { TodoSummary } from "../lib/todo-view-wire";

vi.hoisted(() => {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent: () => false,
  })) as typeof window.matchMedia;
});

const api = vi.fn(async (_path: string, _init?: unknown): Promise<unknown> => ({}));
vi.mock("../lib/api", async (orig) => ({
  ...(await orig<typeof import("../lib/api")>()),
  api: (path: string, init?: unknown) => api(path, init),
}));

const { getState, patchAssistant, reset } = await import("../lib/store");
const { TodosView } = await import("./Todos");
const { t } = await import("../i18n");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const NOW = Date.parse("2026-11-24T12:00:00.000Z");
const HOUR = 3_600_000;
const iso = (agoMs: number) => new Date(NOW - agoMs).toISOString();

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); });
};
const seed = (patch: Record<string, unknown>) => Object.assign(getState() as unknown as Record<string, unknown>, patch);

const view = (section: string, reason: string, phase: string, rank = 0): TodoSummary => ({ phase, section, reason, rank });

const sampleTodos: Todo[] = [
  // Personal to-do without project
  {
    id: "personal_plain",
    title: "Buy coffee beans",
    created_at: iso(5 * HOUR),
    updated_at: iso(4 * HOUR),
    view: view("todo", "rest", "todo", 0),
  },
  // Personal to-do with explicit audience="human" and assigned project
  {
    id: "personal_proj",
    title: "Plan Q4 offsite",
    project: "ops",
    audience: "human",
    created_at: iso(4 * HOUR),
    updated_at: iso(3 * HOUR),
    view: view("todo", "rest", "todo", 1),
  },
  // Agent-backed task with assigned project
  {
    id: "task_agent",
    title: "Fix login timeout",
    project: "persona",
    agents: [{ machine_id: "m1", session_id: "s1", linked_at: iso(2 * HOUR) }],
    created_at: iso(3 * HOUR),
    updated_at: iso(2 * HOUR),
    view: view("working", "agent_running", "working", 0),
  },
  // Supervised task without project
  {
    id: "task_supervised",
    title: "Audit release checklist",
    origin: "conv-main",
    supervision: { supervisor: "main", state: "needs_user" },
    created_at: iso(2 * HOUR),
    updated_at: iso(1 * HOUR),
    view: view("needs_you", "owner_decides", "needs_you", 0),
  },
  // Explicit autonomous task, done
  {
    id: "task_done",
    title: "Run nightly benchmark",
    audience: "autonomous",
    created_at: iso(6 * HOUR),
    updated_at: iso(1 * HOUR),
    done_at: iso(1 * HOUR),
    view: view("done", "closed", "done", 0),
  },
];

const kbProjects = [
  { slug: "persona", name: "Persona Core" },
  { slug: "ops", name: "Operations" },
];

beforeEach(() => {
  reset();
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(NOW);
  api.mockReset();
  api.mockImplementation(async (path: string) => {
    if (path.includes("/kb")) return { items: kbProjects };
    return {};
  });
  localStorage.clear();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  document.body.innerHTML = "";
  localStorage.clear();
  reset();
  vi.useRealTimers();
});

const render = async ({
  todos = sampleTodos,
  sharedBlockers = [],
  sessions = {},
  todosV2,
  assistantManager,
  leaders,
  conversations = [],
  grouping = "status",
  selectedId,
}: {
  todos?: Todo[];
  sharedBlockers?: unknown[];
  sessions?: Record<string, unknown[]>;
  todosV2?: boolean;
  assistantManager?: string;
  leaders?: string[];
  conversations?: unknown[];
  grouping?: "status" | "folder";
  selectedId?: string;
} = {}) => {
  localStorage.setItem("persona.todos.grouping", grouping);
  seed({
    todos,
    sharedBlockers,
    todosLoaded: true,
    sessions,
    interactions: [],
    answered: {},
    features: ["todo_view"],
    assistant:
      todosV2 === undefined && assistantManager === undefined && leaders === undefined && conversations.length === 0
        ? { machine_id: "m1", main: "main", conversations: [] }
        : {
            machine_id: "m1",
            main: "main",
            conversations,
            ...(leaders !== undefined ? { leaders } : {}),
            ...(assistantManager !== undefined ? { manager: assistantManager } : {}),
            ...(todosV2 !== undefined ? { todos_v2: todosV2 } : {}),
          },
  });
  await act(async () => root.render(<TodosView selectedId={selectedId} />));
  await flush();
};

const summary = () => host.querySelector("[data-testid=todos-summary]")?.textContent ?? "";
const kindFilterEl = () => host.querySelector<HTMLElement>("[data-testid=todos-kind-filter]");
const visibleTodoIds = () => [...host.querySelectorAll<HTMLElement>("[data-todo]")].map((el) => el.dataset.todo);
const clickKind = async (label: string) => {
  const btn = [...(kindFilterEl()?.querySelectorAll<HTMLButtonElement>("button[role=radio]") ?? [])].find((b) => b.textContent === label);
  expect(btn, `expected kind filter option "${label}"`).toBeTruthy();
  await act(async () => btn!.click());
  await flush();
};

describe("TodosView under todos_v2 Labs toggle (#14642)", () => {
  it("renders identically to legacy when todos_v2 is false or omitted by an older Hub", async () => {
    await render({ todosV2: undefined, selectedId: "personal_plain" });
    expect(kindFilterEl()).toBeNull();
    expect(host.querySelector("[data-testid=todo-project-tag]")).toBeNull();
    const legacyHtml = host.querySelector("[data-testid=todos-list-column]")?.innerHTML;
    const legacySummary = summary();
    const legacySelectOption = host.querySelector<HTMLOptionElement>("[data-testid=todo-plan-project] option[value='']")?.textContent;
    expect(legacySelectOption).toBe(t("todos.project_none"));

    await act(async () => root.unmount());
    root = createRoot(host);
    await render({ todosV2: false, selectedId: "personal_plain" });
    expect(kindFilterEl()).toBeNull();
    expect(host.querySelector("[data-testid=todo-project-tag]")).toBeNull();
    expect(host.querySelector("[data-testid=todos-list-column]")?.innerHTML).toBe(legacyHtml);
    expect(summary()).toBe(legacySummary);
  });

  it("shows All / To-dos / Tasks filter under todos_v2=true with consistent counts before view placement", async () => {
    await render({ todosV2: true });
    expect(kindFilterEl()).toBeTruthy();
    // Under todos_v2 (#14830), redundant 5-part summary is replaced by concise needs-you pill; Done starts folded.
    expect(host.querySelector("[data-testid=todos-summary]")).toBeNull();
    expect(host.querySelector("[data-testid=todos-header-attention]")?.textContent).toContain(t("todos.v2_header_needs_you", { count: 1 }));
    expect(visibleTodoIds()).toEqual(["task_supervised", "task_agent", "personal_plain", "personal_proj"]);
    expect(host.querySelector('[data-testid="todo-section"][data-section="done"]')?.getAttribute("data-todos")).toBe("1");

    // Filter to personal To-dos only (no needs_you -> attention pill hides)
    await clickKind(t("todos.kind_filter_todos"));
    expect(visibleTodoIds()).toEqual(["personal_plain", "personal_proj"]);
    expect(host.querySelector("[data-testid=todos-header-attention]")).toBeNull();

    // Filter to Agent / supervised Tasks only (1 needs_you -> attention pill returns; Done stays folded with count 1)
    await clickKind(t("todos.kind_filter_tasks"));
    expect(visibleTodoIds()).toEqual(["task_supervised", "task_agent"]);
    expect(host.querySelector("[data-testid=todos-header-attention]")?.textContent).toContain(t("todos.v2_header_needs_you", { count: 1 }));
    expect(host.querySelector('[data-testid="todo-section"][data-section="done"]')?.getAttribute("data-todos")).toBe("1");

    // Back to All
    await clickKind(t("todos.kind_filter_all"));
    expect(visibleTodoIds()).toEqual(["task_supervised", "task_agent", "personal_plain", "personal_proj"]);
    expect(host.querySelector("[data-testid=todos-header-attention]")?.textContent).toContain(t("todos.v2_header_needs_you", { count: 1 }));
  });

  it("shows assigned project tags while omitting empty Uncategorized placeholders under todos_v2=true", async () => {
    await render({ todosV2: true, selectedId: "personal_plain" });
    // Assigned projects render their tag
    expect(host.querySelector('[data-todo="personal_proj"] [data-testid="todo-project-tag"]')?.textContent).toBe("ops");
    expect(host.querySelector('[data-todo="task_agent"] [data-testid="todo-project-tag"]')?.textContent).toBe("persona");
    // Unassigned rows have no project tag and no Uncategorized / None placeholder
    expect(host.querySelector('[data-todo="personal_plain"] [data-testid="todo-project-tag"]')).toBeNull();
    expect(host.querySelector('[data-todo="task_supervised"] [data-testid="todo-project-tag"]')).toBeNull();
    expect(host.querySelector('[data-todo="personal_plain"]')?.textContent).not.toContain(t("todos.project_none"));
    expect(host.querySelector('[data-todo="personal_plain"]')?.textContent).not.toContain(t("lib-todo-tree.unsorted"));
    // Detail project select leaves empty option blank instead of showing "None / 不归类"
    const emptyOption = host.querySelector<HTMLOptionElement>("[data-testid=todo-plan-project] option[value='']");
    expect(emptyOption?.textContent).toBe("");
    expect(emptyOption?.getAttribute("aria-label")).toBe(t("todos.project_none"));

    // Folder grouping: named project headers remain, while Unsorted header is omitted
    await act(async () => root.unmount());
    root = createRoot(host);
    await render({ todosV2: true, grouping: "folder" });
    const groups = [...host.querySelectorAll<HTMLElement>('[data-testid="todo-group"]')];
    const unsortedGroup = groups.find((el) => el.dataset.group === "todos-project:\u0000");
    expect(unsortedGroup).toBeTruthy();
    expect(unsortedGroup?.querySelector("button[aria-expanded]")).toBeNull();
    expect(unsortedGroup?.textContent).not.toContain(t("lib-todo-tree.unsorted"));
    const namedGroup = groups.find((el) => el.dataset.group === "todos-kb:persona");
    expect(namedGroup?.querySelector("button[aria-expanded]")?.textContent).toContain("persona");
  });

  it("immediately reverts to standard view when todos_v2 is switched off", async () => {
    await render({ todosV2: true });
    await clickKind(t("todos.kind_filter_todos"));
    expect(visibleTodoIds()).toEqual(["personal_plain", "personal_proj"]);
    expect(host.querySelector('[data-todo="personal_proj"] [data-testid="todo-project-tag"]')).toBeTruthy();

    // Disable todos_v2 in store -> immediate rollback without reload or migration
    await act(async () => patchAssistant({ todos_v2: false }));
    await flush();
    expect(kindFilterEl()).toBeNull();
    expect(host.querySelector("[data-testid=todo-project-tag]")).toBeNull();
    expect(summary()).toBe(t("todos.summary_triage", { open: 4, parked: 0, shelved: 0, done: 1, cancelled: 0 }));
    expect(visibleTodoIds()).toEqual(["task_supervised", "task_agent", "personal_plain", "personal_proj", "task_done"]);
  });

  it("stays off when assistant payload belongs to a different manager", async () => {
    await render({ todosV2: true, assistantManager: "other-manager" });
    expect(kindFilterEl()).toBeNull();
    expect(host.querySelector("[data-testid=todo-project-tag]")).toBeNull();
  });
});

describe("TodosView goal folding and blocker surfacing under todos_v2 (#14661)", () => {
  const goalTodos: Todo[] = [
    // Top-level personal goal
    {
      id: "goal_ship",
      title: "Launch Q4 release",
      audience: "human",
      created_at: iso(8 * HOUR),
      updated_at: iso(2 * HOUR),
      view: view("working", "agent_running", "working", 0),
    },
    // Non-urgent child task: working
    {
      id: "child_work",
      title: "Build release artifacts",
      part_of: "goal_ship",
      audience: "autonomous",
      agents: [{ machine_id: "m1", session_id: "s-work", linked_at: iso(2 * HOUR) }],
      created_at: iso(6 * HOUR),
      updated_at: iso(1 * HOUR),
      view: view("working", "agent_running", "working", 1),
    },
    // Non-urgent child task: completed
    {
      id: "child_done",
      title: "Freeze release branch",
      part_of: "goal_ship",
      audience: "autonomous",
      created_at: iso(7 * HOUR),
      updated_at: iso(3 * HOUR),
      done_at: iso(3 * HOUR),
      view: view("done", "closed", "done", 0),
    },
    // Urgent child task: needs_you (blocker)
    {
      id: "child_blocker",
      title: "Approve production cutover",
      part_of: "goal_ship",
      audience: "autonomous",
      supervision: { supervisor: "main", state: "needs_user" },
      ask: { verdict: "Ready to cut over?", asked_at: iso(1 * HOUR), outcome: "decide", recommend: "Proceed" },
      created_at: iso(5 * HOUR),
      updated_at: iso(1 * HOUR),
      view: view("needs_you", "owner_decides", "needs_you", 0),
    },
    // Standalone top-level personal to-do
    {
      id: "solo_todo",
      title: "Send release announcement",
      audience: "human",
      created_at: iso(4 * HOUR),
      updated_at: iso(2 * HOUR),
      view: view("todo", "rest", "todo", 0),
    },
  ];

  it("keeps legacy flat DOM and counts pixel-identical when todos_v2 is false or omitted", async () => {
    await render({ todos: goalTodos, todosV2: undefined, selectedId: "goal_ship" });
    expect(host.querySelector("[data-testid=todo-goal-toggle]")).toBeNull();
    expect(host.querySelector("[data-testid=todo-parent-goal]")).toBeNull();
    const legacyHtml = host.querySelector("[data-testid=todos-list-column]")?.innerHTML;
    const legacyIds = visibleTodoIds();
    expect(legacyIds).toEqual(["child_blocker", "goal_ship", "child_work", "solo_todo", "child_done"]);

    await act(async () => root.unmount());
    root = createRoot(host);
    await render({ todos: goalTodos, todosV2: false, selectedId: "goal_ship" });
    expect(host.querySelector("[data-testid=todo-goal-toggle]")).toBeNull();
    expect(host.querySelector("[data-testid=todo-parent-goal]")).toBeNull();
    expect(host.querySelector("[data-testid=todos-list-column]")?.innerHTML).toBe(legacyHtml);
  });

  it("folds non-urgent children, shows done/total progress & inline expand without opening detail, and surfaces needs_you blocker at top", async () => {
    await render({ todos: goalTodos, todosV2: true, selectedId: "solo_todo" });

    // Non-urgent children (child_work, child_done) are folded; blocker surfaces in waiting (`needs_you`)
    expect(visibleTodoIds()).toEqual(["child_blocker", "goal_ship", "solo_todo"]);

    // Waiting section holds the surfaced blocker with parent goal hint
    const waitingSec = host.querySelector<HTMLElement>('[data-testid="todo-section"][data-section="waiting"]');
    expect(waitingSec?.querySelector('[data-todo="child_blocker"]')).toBeTruthy();
    expect(waitingSec?.querySelector('[data-todo="child_blocker"] [data-testid="todo-parent-goal"]')?.textContent).toContain("Launch Q4 release");

    // Parent goal row shows compact 1/3 progress, blocked indicator, and accessible toggle
    const parentRow = host.querySelector<HTMLElement>('[data-todo="goal_ship"]')!;
    const toggle = parentRow.querySelector<HTMLButtonElement>('[data-testid="todo-goal-toggle"]')!;
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(toggle.className).toContain("touch-hit");
    expect(parentRow.querySelector('[data-testid="todo-goal-progress"]')?.textContent).toBe("1/3");
    expect(parentRow.querySelector('[data-testid="todo-goal-blocked"]')?.textContent).toBe(t("todos.goal_blocked", { count: 1 }));
    expect(host.querySelector('[data-testid="todo-goal-children"]')).toBeNull();

    // Clicking toggle expands inline children WITHOUT changing the selected detail (solo_todo stays selected)
    await act(async () => toggle.click());
    await flush();
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    expect(host.querySelector('[data-todo="solo_todo"]')?.getAttribute("aria-current")).toBe("true");
    expect(parentRow.getAttribute("aria-current")).toBeNull();

    const childrenBox = host.querySelector<HTMLElement>('[data-testid="todo-goal-children"][data-parent="goal_ship"]')!;
    expect(childrenBox).toBeTruthy();
    const childIds = [...childrenBox.querySelectorAll<HTMLElement>("[data-todo]")].map((el) => el.dataset.todo);
    expect(childIds).toEqual(["child_blocker", "child_work", "child_done"]);

    // Clicking toggle again collapses children
    await act(async () => toggle.click());
    await flush();
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(host.querySelector('[data-testid="todo-goal-children"]')).toBeNull();
  });

  it("interoperates with All / To-dos / Tasks filter and reverts immediately when todos_v2 is switched off", async () => {
    await render({ todos: goalTodos, todosV2: true });
    expect(visibleTodoIds()).toEqual(["child_blocker", "goal_ship", "solo_todo"]);

    // Under Tasks filter, personal parent goal_ship is excluded -> its task children stay visible as top-level rows (child_done is inside folded Done)
    await clickKind(t("todos.kind_filter_tasks"));
    expect(visibleTodoIds()).toEqual(["child_blocker", "child_work"]);
    expect(host.querySelector('[data-testid="todo-section"][data-section="done"]')?.getAttribute("data-todos")).toBe("1");
    expect(host.querySelector('[data-testid="todo-parent-goal"]')).toBeNull();
    expect(host.querySelector('[data-testid="todo-goal-toggle"]')).toBeNull();

    // Under To-dos filter, task children are excluded -> personal parent renders without a fold toggle
    await clickKind(t("todos.kind_filter_todos"));
    expect(visibleTodoIds()).toEqual(["goal_ship", "solo_todo"]);
    expect(host.querySelector('[data-testid="todo-goal-toggle"]')).toBeNull();

    // Back to All -> goal folding resumes
    await clickKind(t("todos.kind_filter_all"));
    expect(visibleTodoIds()).toEqual(["child_blocker", "goal_ship", "solo_todo"]);

    // Switching todos_v2 off reverts immediately to flat list with zero migration
    await act(async () => patchAssistant({ todos_v2: false }));
    await flush();
    expect(host.querySelector('[data-testid="todo-goal-toggle"]')).toBeNull();
    expect(host.querySelector('[data-testid="todo-parent-goal"]')).toBeNull();
    expect(visibleTodoIds()).toEqual(["child_blocker", "goal_ship", "child_work", "solo_todo", "child_done"]);
  });

  it("gates the detail view Target Repo / Reference Repo matrix on todos_v2 and reverts immediately when switched off", async () => {
    const repoSet = {
      schema_version: 1,
      manager: "assistant",
      todo_id: "task_agent",
      revision: 1,
      repositories: [
        {
          repo_id: "docs",
          requested_ref: { kind: "branch", value: "docs/v2" },
          source_snapshot: {
            identity: { provider: "github", instance_url: "https://github.com", path: "example/docs", canonical_url: "https://github.com/example/docs" },
            clone_url: "https://github.com/example/docs",
            transport_origin: "canonical_default",
            catalog_revision: 1,
          },
          links: [{ kind: "issue", number: "5", canonical_url: "https://github.com/example/docs/issues/5", role: "reference" }],
        },
        {
          repo_id: "web",
          requested_ref: { kind: "default" },
          source_snapshot: {
            identity: { provider: "github", instance_url: "https://github.com", path: "example/web", canonical_url: "https://github.com/example/web" },
            clone_url: "https://github.com/example/web",
            transport_origin: "canonical_default",
            catalog_revision: 1,
          },
          links: [{ kind: "pull_request", number: "12", canonical_url: "https://github.com/example/web/pull/12", role: "delivery" }],
        },
      ],
    };
    api.mockImplementation(async (path: string) => {
      if (path.includes("/kb")) return { items: kbProjects };
      if (path === "/todos/task_agent/repositories") return repoSet;
      return {};
    });

    await render({ todos: sampleTodos, todosV2: true, selectedId: "task_agent" });
    const matrix = host.querySelector("[data-repo-matrix]");
    expect(matrix).toBeTruthy();
    const rows = [...matrix!.querySelectorAll<HTMLElement>("[data-repository-row]")];
    expect(rows.map((r) => r.dataset.repoRole)).toEqual(["target", "reference"]);
    expect(matrix!.querySelector('[data-repo-role-badge="target"]')?.textContent).toBe(t("task-repositories.role_target"));
    expect(matrix!.querySelector('[data-repo-role-badge="reference"]')?.textContent).toBe(t("task-repositories.role_reference"));

    await act(async () => patchAssistant({ todos_v2: false }));
    await flush();
    expect(host.querySelector("[data-repo-matrix]")).toBeNull();
    expect(host.querySelector("[data-repo-role-badge]")).toBeNull();
  });
});

describe("TodosView shared external blockers strip under todos_v2 (#14728)", () => {
  const sampleBlockers = [
    {
      key: "pull_request:https://github.com/example/core/pull/42",
      kind: "pull_request",
      label: "example/core#42",
      url: "https://github.com/example/core/pull/42",
      todo_ids: ["task_agent", "task_supervised"],
      count: 2,
    },
    {
      key: "target:staging deploy",
      kind: "external_target",
      label: "staging deploy",
      todo_ids: ["personal_proj", "task_agent"],
      count: 2,
    },
  ];

  const stripEl = () => host.querySelector<HTMLElement>("[data-testid=todos-shared-blockers]");
  const chips = () => [...host.querySelectorAll<HTMLButtonElement>("[data-testid=shared-blocker-chip]")];

  it("renders no shared blockers DOM when todos_v2 is false or omitted, even if shared_blockers exists", async () => {
    await render({ todosV2: undefined, sharedBlockers: sampleBlockers });
    expect(stripEl()).toBeNull();
    const legacyHtml = host.querySelector("[data-testid=todos-list-column]")?.innerHTML;

    await act(async () => root.unmount());
    root = createRoot(host);
    await render({ todosV2: false, sharedBlockers: sampleBlockers });
    expect(stripEl()).toBeNull();
    expect(host.querySelector("[data-testid=todos-list-column]")?.innerHTML).toBe(legacyHtml);
  });

  it("renders nothing under todos_v2=true when shared_blockers is empty or absent", async () => {
    await render({ todosV2: true, sharedBlockers: [] });
    expect(stripEl()).toBeNull();
  });

  it("renders blocker labels, counts, and safe external links, and filters tasks on click with toggle and clear", async () => {
    await render({ todosV2: true, sharedBlockers: sampleBlockers });
    expect(stripEl()).toBeTruthy();

    const list = chips();
    expect(list).toHaveLength(2);
    expect(list[0].textContent).toContain("example/core#42");
    expect(list[0].textContent).toContain(t("todos.shared_blockers_count", { count: 2 }));
    expect(list[0].getAttribute("aria-pressed")).toBe("false");

    // External link renders only for valid http(s) URL, never nested inside the filter button
    const links = [...host.querySelectorAll<HTMLAnchorElement>("[data-testid=shared-blocker-link]")];
    expect(links).toHaveLength(1);
    expect(links[0].getAttribute("href")).toBe("https://github.com/example/core/pull/42");
    expect(links[0].getAttribute("target")).toBe("_blank");
    expect(links[0].getAttribute("rel")).toBe("noopener noreferrer");
    expect(links[0].closest("button")).toBeNull();
    expect(host.querySelector("[data-testid=shared-blockers-clear]")).toBeNull();

    // Click first blocker -> filters list to its todo_ids
    await act(async () => list[0].click());
    await flush();
    expect(list[0].getAttribute("aria-pressed")).toBe("true");
    expect(visibleTodoIds()).toEqual(["task_supervised", "task_agent"]);
    expect(host.querySelector("[data-testid=shared-blockers-clear]")).toBeTruthy();

    // Click same blocker again -> toggles off and restores all rows (Done remains folded under todos_v2)
    await act(async () => list[0].click());
    await flush();
    expect(list[0].getAttribute("aria-pressed")).toBe("false");
    expect(visibleTodoIds()).toEqual(["task_supervised", "task_agent", "personal_plain", "personal_proj"]);

    // Click second blocker, then click Clear filter button -> restores all rows
    await act(async () => chips()[1].click());
    await flush();
    expect(visibleTodoIds()).toEqual(["task_agent", "personal_proj"]);
    await act(async () => host.querySelector<HTMLButtonElement>("[data-testid=shared-blockers-clear]")!.click());
    await flush();
    expect(visibleTodoIds()).toEqual(["task_supervised", "task_agent", "personal_plain", "personal_proj"]);
  });

  it("auto-clears selection when the selected blocker disappears after refresh, and reverts when todos_v2 is switched off", async () => {
    await render({ todosV2: true, sharedBlockers: sampleBlockers });
    await act(async () => chips()[0].click());
    await flush();
    expect(visibleTodoIds()).toEqual(["task_supervised", "task_agent"]);

    // Refresh drops the selected blocker -> automatically exits filter instead of deadlocking empty
    await act(async () => {
      seed({ sharedBlockers: [sampleBlockers[1]] });
      root.render(<TodosView />);
    });
    await flush();
    expect(visibleTodoIds()).toEqual(["task_supervised", "task_agent", "personal_plain", "personal_proj"]);
    expect(host.querySelector("[data-testid=shared-blockers-clear]")).toBeNull();

    // Re-select remaining blocker, then turn todos_v2 off -> immediate rollback
    await act(async () => chips()[0].click());
    await flush();
    expect(visibleTodoIds()).toEqual(["task_agent", "personal_proj"]);

    await act(async () => patchAssistant({ todos_v2: false }));
    await flush();
    expect(stripEl()).toBeNull();
    expect(visibleTodoIds()).toEqual(["task_supervised", "task_agent", "personal_plain", "personal_proj", "task_done"]);
  });
});

describe("TodosView batch reorganize mode under todos_v2 (#14758)", () => {
  const toggleBtn = () => host.querySelector<HTMLButtonElement>("[data-testid=todos-reorganize-toggle]");
  const barEl = () => host.querySelector<HTMLElement>("[data-testid=batch-reorganize-bar]");
  const checkboxes = () => [...host.querySelectorAll<HTMLInputElement>("[data-testid=todo-reorganize-checkbox]")];
  const checkboxFor = (id: string) =>
    host.querySelector<HTMLInputElement>(`[data-testid=todo-reorganize-checkbox][data-todo-id="${id}"]`);

  it("renders no batch reorganize controls when todos_v2 is false or omitted by an older Hub", async () => {
    await render({ todosV2: undefined, selectedId: "personal_plain" });
    expect(toggleBtn()).toBeNull();
    expect(barEl()).toBeNull();
    expect(checkboxes()).toHaveLength(0);
    const legacyHeader = host.querySelector("header")?.innerHTML;
    const legacyList = host.querySelector("[data-testid=todos-list-column]")?.innerHTML;

    await act(async () => root.unmount());
    root = createRoot(host);
    await render({ todosV2: false, selectedId: "personal_plain" });
    expect(toggleBtn()).toBeNull();
    expect(barEl()).toBeNull();
    expect(checkboxes()).toHaveLength(0);
    expect(host.querySelector("header")?.innerHTML).toBe(legacyHeader);
    expect(host.querySelector("[data-testid=todos-list-column]")?.innerHTML).toBe(legacyList);
  });

  it("supports multi-select across states without opening detail, excludes cycle parent targets, and submits exact payload", async () => {
    const hierarchyTodos: Todo[] = [
      ...sampleTodos,
      {
        id: "child_of_plain",
        title: "Grind beans",
        part_of: "personal_plain",
        created_at: iso(3 * HOUR),
        updated_at: iso(2 * HOUR),
        view: view("todo", "rest", "todo", 2),
      },
    ];
    api.mockImplementation(async (path: string) => {
      if (path.includes("/kb")) return { items: kbProjects };
      if (path === "/todos/reorganize") return { updated: [hierarchyTodos[0], hierarchyTodos[2]] };
      if (path === "/todos") return { todos: hierarchyTodos };
      return {};
    });

    await render({
      todos: hierarchyTodos,
      todosV2: true,
      selectedId: "task_supervised",
      leaders: ["dir-platform"],
      conversations: [{ machine_id: "m1", session_id: "dir-platform", title: "Platform Direction" }],
    });

    expect(toggleBtn()?.getAttribute("aria-pressed")).toBe("false");
    expect(barEl()).toBeNull();

    // Enter organize mode
    await act(async () => toggleBtn()!.click());
    await flush();
    expect(toggleBtn()?.getAttribute("aria-pressed")).toBe("true");
    expect(barEl()).toBeTruthy();

    // Select a personal to-do and a working agent task; detail selection stays on task_supervised
    await act(async () => checkboxFor("personal_plain")!.click());
    await act(async () => checkboxFor("task_agent")!.click());
    await flush();
    expect(host.querySelector('[data-todo="task_supervised"]')?.getAttribute("aria-current")).toBe("true");
    expect(host.querySelector("[data-testid=reorganize-selected-count]")?.textContent).toBe(
      t("todos.reorganize_selected_count", { count: 2 }),
    );

    // Parent dropdown excludes selected items AND their descendants (child_of_plain) to prevent cycles
    const parentSelect = host.querySelector<HTMLSelectElement>("[data-testid=reorganize-part-of]")!;
    const parentValues = [...parentSelect.options].map((o) => o.value);
    expect(parentValues).not.toContain("personal_plain");
    expect(parentValues).not.toContain("task_agent");
    expect(parentValues).not.toContain("child_of_plain");
    expect(parentValues).toContain("personal_proj");

    // transfer_supervision is disabled and unchecked by default until an active direction is chosen
    const transferBox = host.querySelector<HTMLInputElement>("[data-testid=reorganize-transfer-supervision]")!;
    expect(transferBox.checked).toBe(false);
    expect(transferBox.disabled).toBe(true);

    // Choose target parent and active direction
    await act(async () => {
      parentSelect.value = "personal_proj";
      parentSelect.dispatchEvent(new Event("change", { bubbles: true }));
    });
    const dirSelect = host.querySelector<HTMLSelectElement>("[data-testid=reorganize-direction]")!;
    await act(async () => {
      dirSelect.value = "dir-platform";
      dirSelect.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await flush();
    expect(transferBox.disabled).toBe(false);
    expect(transferBox.checked).toBe(false);

    // Submit without checking transfer_supervision -> defaults to false
    await act(async () => host.querySelector<HTMLButtonElement>("[data-testid=reorganize-apply]")!.click());
    await flush();

    expect(api).toHaveBeenCalledWith(
      "/todos/reorganize",
      expect.objectContaining({
        method: "POST",
        json: {
          ids: ["personal_plain", "task_agent"],
          part_of: "personal_proj",
          direction_id: "dir-platform",
          include_children: false,
          transfer_supervision: false,
        },
      }),
    );
    // Refreshes list, clears selection, and shows feedback
    expect(api).toHaveBeenCalledWith("/todos", expect.anything());
    expect(host.querySelector("[data-testid=reorganize-selected-count]")?.textContent).toBe(
      t("todos.reorganize_selected_count", { count: 0 }),
    );
    expect(host.querySelector("[data-testid=reorganize-feedback]")?.textContent).toBe(
      t("todos.reorganize_success", { count: 2 }),
    );
  });

  it("shows inline error without losing selection on API failure, and resets immediately when todos_v2 turns off", async () => {
    api.mockImplementation(async (path: string) => {
      if (path.includes("/kb")) return { items: kbProjects };
      if (path === "/todos/reorganize") throw new Error("read-only role cannot modify todos");
      return {};
    });

    await render({ todosV2: true });
    await act(async () => toggleBtn()!.click());
    await flush();

    await act(async () => checkboxFor("personal_plain")!.click());
    await flush();

    const parentSelect = host.querySelector<HTMLSelectElement>("[data-testid=reorganize-part-of]")!;
    await act(async () => {
      parentSelect.value = "__clear__";
      parentSelect.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await act(async () => host.querySelector<HTMLButtonElement>("[data-testid=reorganize-apply]")!.click());
    await flush();

    expect(host.querySelector("[data-testid=reorganize-error]")?.textContent).toContain("read-only role cannot modify todos");
    // Selection preserved on error
    expect(checkboxFor("personal_plain")?.checked).toBe(true);
    expect(host.querySelector("[data-testid=reorganize-selected-count]")?.textContent).toBe(
      t("todos.reorganize_selected_count", { count: 1 }),
    );

    // Turning todos_v2 off exits organize mode and removes all controls immediately
    await act(async () => patchAssistant({ todos_v2: false }));
    await flush();
    expect(toggleBtn()).toBeNull();
    expect(barEl()).toBeNull();
    expect(checkboxes()).toHaveLength(0);
  });
});

describe("TodoDetail dual template, default-expanded sections & status summary under todos_v2 (#14777, #14851)", () => {
  const longNotes =
    "Line 1: Context about the migration plan.\nLine 2: Ensure backward compatibility.\nLine 3: Keep first screen bundle within budget.\nLine 4: Verify rollback path.\nLine 5: Validate i18n keys.\nLine 6: Ship clean PR.";

  const dualTodos: Todo[] = [
    {
      id: "personal_note",
      title: "Pick up groceries",
      audience: "human",
      notes: "Milk and eggs",
      created_at: iso(5 * HOUR),
      updated_at: iso(4 * HOUR),
      view: view("todo", "rest", "todo", 0),
    },
    {
      id: "goal_parent",
      title: "Ship Todos V2",
      direction_id: "dir-platform",
      created_at: iso(8 * HOUR),
      updated_at: iso(1 * HOUR),
      view: view("working", "agent_running", "working", 0),
    },
    {
      id: "work_goal_active",
      title: "Implement dual-template detail page",
      part_of: "goal_parent",
      direction_id: "dir-platform",
      priority: "high",
      notes: longNotes,
      pr_url: "https://github.com/nwverse/persona/pull/14777",
      agents: [
        { machine_id: "m1", session_id: "s-active", linked_at: iso(2 * HOUR), title: "Frontend worker" },
        { machine_id: "m1", session_id: "s-past", linked_at: iso(4 * HOUR), released_at: iso(3 * HOUR), title: "Earlier planner", cwd: "/repo/web" },
      ],
      created_at: iso(4 * HOUR),
      updated_at: iso(1 * HOUR),
      view: view("working", "agent_running", "working", 1),
    },
    {
      id: "work_goal_with_due",
      title: "Prepare launch notes",
      part_of: "goal_parent",
      due_date: "2026-12-01",
      supervision: { supervisor: "main", state: "needs_user" },
      created_at: iso(3 * HOUR),
      updated_at: iso(1 * HOUR),
      view: view("working", "agent_running", "working", 2),
    },
  ];

  const activeSessions = {
    m1: [
      {
        id: "s-active",
        machine_id: "m1",
        session_id: "s-active",
        agent: "claude",
        title: "Frontend worker",
        cwd: "/repo/web",
        state: "working",
        updated_at: iso(10 * 60_000),
      },
    ],
  };

  it("keeps TodoDetail DOM 100% identical when todos_v2 is false or omitted by an older Hub", async () => {
    await render({
      todos: dualTodos,
      sessions: activeSessions,
      todosV2: undefined,
      selectedId: "work_goal_active",
      leaders: ["dir-platform"],
      conversations: [{ machine_id: "m1", session_id: "dir-platform", title: "Platform Direction" }],
    });
    expect(host.querySelector("[data-testid=todo-detail-ownership]")).toBeNull();
    expect(host.querySelector("[data-testid=todo-detail-status-summary]")).toBeNull();
    expect(host.querySelector("[data-testid=todo-detail-priority-subtle]")).toBeNull();
    expect(host.querySelector("[data-testid=todo-summary-open-active-agent]")).toBeNull();
    expect(host.querySelector("[data-testid=todo-detail-personal-fields-toggle]")).toBeNull();
    expect(host.querySelector("[data-testid=todo-detail-notes-collapsed]")).toBeNull();
    expect(host.querySelector("[data-testid=todo-open-active-agent]")).toBeNull();
    expect(host.querySelector("[data-testid=todo-open-pr]")).toBeNull();
    expect(host.querySelector("[data-testid=todo-add-agent]")).toBeNull();
    // Released agent history remains collapsed by default in legacy view
    expect(host.querySelector("[data-testid=todo-detail]")?.textContent).not.toContain("Earlier planner");
    const legacyDetailHtml = host.querySelector("[data-testid=todo-detail]")?.innerHTML;

    await act(async () => root.unmount());
    root = createRoot(host);
    await render({
      todos: dualTodos,
      sessions: activeSessions,
      todosV2: false,
      selectedId: "work_goal_active",
      leaders: ["dir-platform"],
      conversations: [{ machine_id: "m1", session_id: "dir-platform", title: "Platform Direction" }],
    });
    expect(host.querySelector("[data-testid=todo-detail]")?.innerHTML).toBe(legacyDetailHtml);
  });

  it("keeps personal to-dos on the lightweight template under todos_v2=true", async () => {
    await render({ todos: dualTodos, todosV2: true, selectedId: "personal_note" });
    expect(host.querySelector("[data-testid=todo-detail-ownership]")).toBeNull();
    expect(host.querySelector("[data-testid=todo-detail-status-summary]")).toBeNull();
    expect(host.querySelector("[data-testid=todo-detail-personal-fields-toggle]")).toBeNull();
    expect(host.querySelector("[data-testid=todo-detail-subtasks]")).toBeNull();
    expect(host.querySelector("[data-testid=todo-plan]")).toBeTruthy();
    expect(host.querySelector("[data-testid=todo-plan-priority]")?.className).toContain("text-ink-3");
    expect(host.querySelector("[data-testid=todo-open-active-agent]")).toBeNull();
    expect(host.querySelector("[data-testid=todo-add-agent]")).toBeNull();
  });

  it("keeps all sections expanded by default, renders subtle priority & status summary strip, and dynamic CTA for work goals under todos_v2=true", async () => {
    api.mockImplementation(async (path: string) => {
      if (path.includes("/kb")) return { items: kbProjects };
      if (path.startsWith("/todos/work_goal_active/decisions")) {
        return {
          decisions: [
            {
              id: "d1",
              todo_id: "work_goal_active",
              question: "Keep sections expanded by default?",
              choice: "Yes, expand all sections",
              reason: "Avoid hiding schedule, notes, and decisions behind toggles",
              source: "manual",
              source_machine: "",
              source_session: "",
              source_message: "",
              supersedes_id: "",
              superseded_by: "",
              created_at: iso(1 * HOUR),
            },
          ],
        };
      }
      return {};
    });

    await render({
      todos: dualTodos,
      sessions: activeSessions,
      todosV2: true,
      selectedId: "work_goal_active",
      leaders: ["dir-platform"],
      conversations: [{ machine_id: "m1", session_id: "dir-platform", title: "Platform Direction" }],
    });

    // Ownership strip shows template badge, readable direction name, clickable parent goal link, and subtle secondary priority
    const ownership = host.querySelector("[data-testid=todo-detail-ownership]");
    expect(ownership).toBeTruthy();
    expect(host.querySelector("[data-testid=todo-detail-template-badge]")?.textContent).toBe(t("todos.detail_template_work_goal"));
    expect(host.querySelector("[data-testid=todo-detail-direction]")?.textContent).toContain("Platform Direction");
    const parentLink = host.querySelector<HTMLButtonElement>("[data-testid=todo-detail-parent-link]")!;
    expect(parentLink.textContent).toContain("Ship Todos V2");
    const subtlePriority = host.querySelector<HTMLElement>("[data-testid=todo-detail-priority-subtle]")!;
    expect(subtlePriority).toBeTruthy();
    expect(subtlePriority.textContent).toContain(t("todos.priority_high"));
    expect(subtlePriority.className).toContain("text-ink-3");

    // Concise status summary strip uses semantic tokens and surfaces active Agent primary CTA
    const statusSummary = host.querySelector<HTMLElement>("[data-testid=todo-detail-status-summary]")!;
    expect(statusSummary).toBeTruthy();
    expect(statusSummary.className).toContain("bg-inset");
    expect(statusSummary.className).toContain("border-line");
    expect(statusSummary.className).toContain("rounded-control");
    expect(host.querySelector("[data-testid=todo-detail-summary-status]")?.textContent).toBeTruthy();
    const summaryOpenAgentBtn = host.querySelector<HTMLButtonElement>("[data-testid=todo-summary-open-active-agent]")!;
    expect(summaryOpenAgentBtn.textContent).toBe(t("todos.detail_open_active_agent"));

    // Schedule & reminder fields, priority selector (muted), long notes, decisions, and participated history are ALL expanded by default
    expect(host.querySelector("[data-testid=todo-detail-personal-fields-toggle]")).toBeNull();
    expect(host.querySelector("[data-testid=todo-plan]")).toBeTruthy();
    expect(host.querySelector("[data-testid=todo-plan-priority]")?.className).toContain("text-ink-3");
    expect(host.querySelector("[data-testid=todo-detail-notes-collapsed]")).toBeNull();
    expect(host.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe(longNotes);
    expect(host.querySelector("[data-testid=todo-decisions]")?.textContent).toContain("Keep sections expanded by default?");
    expect(host.querySelector("[data-testid=todo-detail]")?.textContent).toContain("Earlier planner");

    // Dynamic footer action bar: Open active Agent primary CTA + safe Open PR link + demoted Add Agent button
    const openAgentBtn = host.querySelector<HTMLButtonElement>("[data-testid=todo-open-active-agent]")!;
    expect(openAgentBtn.textContent).toBe(t("todos.detail_open_active_agent"));
    const openPrLink = host.querySelector<HTMLAnchorElement>("[data-testid=todo-open-pr]")!;
    expect(openPrLink.getAttribute("href")).toBe("https://github.com/nwverse/persona/pull/14777");
    expect(openPrLink.getAttribute("target")).toBe("_blank");
    expect(openPrLink.getAttribute("rel")).toBe("noopener noreferrer");
    const addAgentBtn = host.querySelector<HTMLButtonElement>("[data-testid=todo-add-agent]")!;
    expect(addAgentBtn.textContent).toBe(t("todos.detail_add_agent"));

    // Clicking parent link navigates to parent goal, which displays its subtasks summary & checklist expanded by default
    await act(async () => parentLink.click());
    await flush();
    expect(host.querySelector("[data-testid=todo-detail-summary-subtasks]")?.textContent).toContain("0/2");
    const subtasks = host.querySelector("[data-testid=todo-detail-subtasks]");
    expect(subtasks).toBeTruthy();
    const subtaskIds = [...subtasks!.querySelectorAll<HTMLElement>("[data-testid=todo-detail-subtask-item]")].map(
      (el) => el.dataset.subtaskId,
    );
    expect(subtaskIds).toEqual(["work_goal_active", "work_goal_with_due"]);

    // Clicking a subtask with due_date keeps schedule/reminder expanded
    await act(async () =>
      subtasks!.querySelector<HTMLButtonElement>('[data-subtask-id="work_goal_with_due"]')!.click(),
    );
    await flush();
    expect(host.querySelector("[data-testid=todo-detail-personal-fields-toggle]")).toBeNull();
    expect(host.querySelector("[data-testid=todo-plan]")).toBeTruthy();

    // Turning todos_v2 off immediately reverts TodoDetail to legacy layout
    await act(async () => patchAssistant({ todos_v2: false }));
    await flush();
    expect(host.querySelector("[data-testid=todo-detail-ownership]")).toBeNull();
    expect(host.querySelector("[data-testid=todo-detail-status-summary]")).toBeNull();
    expect(host.querySelector("[data-testid=todo-detail-subtasks]")).toBeNull();
    expect(host.querySelector("[data-testid=todo-open-active-agent]")).toBeNull();
    expect(host.querySelector("[data-testid=todo-add-agent]")).toBeNull();
  });
});

describe("TodosView slim header & left pane under todos_v2 (#14830)", () => {
  const slimTodos: Todo[] = [
    {
      id: "task_decision",
      title: "Approve database cutover",
      priority: "high",
      direction_id: "dir-platform",
      supervision: { supervisor: "main", state: "needs_user" },
      ask: { verdict: "Proceed with blue-green swap?", asked_at: iso(1 * HOUR), outcome: "decide", recommend: "Proceed" },
      created_at: iso(4 * HOUR),
      updated_at: iso(1 * HOUR),
      view: view("needs_you", "owner_decides", "needs_you", 0),
    },
    {
      id: "task_pr_working",
      title: "Ship slim left pane",
      priority: "high",
      direction_id: "dir-platform",
      pr_url: "https://github.com/nwverse/persona/pull/14830",
      agents: [{ machine_id: "m1", session_id: "s-run", linked_at: iso(2 * HOUR) }],
      created_at: iso(5 * HOUR),
      updated_at: iso(1 * HOUR),
      view: view("working", "agent_running", "working", 0),
    },
    {
      id: "personal_due",
      title: "Renew passport",
      audience: "human",
      priority: "high",
      due_date: "2026-12-10",
      created_at: iso(6 * HOUR),
      updated_at: iso(2 * HOUR),
      view: view("todo", "rest", "todo", 0),
    },
    {
      id: "task_parked",
      title: "Wait for upstream review",
      supervision: {
        supervisor: "main",
        state: "parked",
        parked: { reason: "Waiting on CI mirror", until: new Date(NOW + 24 * HOUR).toISOString(), waiting_on: "external_event", count: 1 },
      },
      created_at: iso(8 * HOUR),
      updated_at: iso(3 * HOUR),
      view: view("parked", "parked", "parked", 0),
    },
    {
      id: "task_recent_done",
      title: "Publish release notes",
      audience: "autonomous",
      created_at: iso(10 * HOUR),
      updated_at: iso(1 * HOUR),
      done_at: iso(1 * HOUR),
      view: view("done", "closed", "done", 0),
    },
  ];

  it("keeps header, list width, toolbar, and rows 100% pixel-identical when todos_v2 is false or omitted", async () => {
    await render({ todos: slimTodos, todosV2: undefined, selectedId: "personal_due" });
    expect(host.querySelector("[data-testid=todos-header-attention]")).toBeNull();
    expect(host.querySelector("[data-testid=todos-v2-labs-link]")).toBeNull();
    expect(host.querySelector("[data-testid=todos-v2-toolbar]")).toBeNull();
    expect(host.querySelector("[data-todo-row-layout]")).toBeNull();
    expect(host.querySelector("[data-testid=todos-list-column]")?.className).toContain("md:w-[380px]");
    const normalizeReactIds = (html: string | undefined) => html?.replace(/_r_[0-9a-z]+_/g, "_r_ID_");
    const legacyHeader = host.querySelector("header")?.innerHTML;
    const legacyList = normalizeReactIds(host.querySelector("[data-testid=todos-list-column]")?.innerHTML);

    await act(async () => root.unmount());
    root = createRoot(host);
    await render({ todos: slimTodos, todosV2: false, selectedId: "personal_due" });
    expect(host.querySelector("header")?.innerHTML).toBe(legacyHeader);
    expect(normalizeReactIds(host.querySelector("[data-testid=todos-list-column]")?.innerHTML)).toBe(legacyList);
  });

  it("renders slim header with needs-you pill & Labs link, narrow single-row toolbar, 1–2 line rows preserving subtle priority & decisions, and folded parked/done sections", async () => {
    await render({
      todos: slimTodos,
      todosV2: true,
      leaders: ["dir-platform"],
      conversations: [{ machine_id: "m1", session_id: "dir-platform", title: "Platform" }],
    });

    // 1. De-duplicated header: no 5-part summary, concise needs-you pill, Labs · v2 link to Settings › Labs
    expect(host.querySelector("[data-testid=todos-summary]")).toBeNull();
    const attention = host.querySelector("[data-testid=todos-header-attention]");
    expect(attention?.getAttribute("data-count")).toBe("1");
    expect(attention?.textContent).toContain(t("todos.v2_header_needs_you", { count: 1 }));
    const labsLink = host.querySelector<HTMLAnchorElement>("[data-testid=todos-v2-labs-link]")!;
    expect(labsLink).toBeTruthy();
    expect(labsLink.getAttribute("href")).toBe("/settings?tab=labs");
    expect(labsLink.textContent).toContain(t("todos.v2_labs_badge"));

    // 2. Narrowed left column & single-row compact toolbar
    const listCol = host.querySelector<HTMLElement>("[data-testid=todos-list-column]")!;
    expect(listCol.className).toContain("md:w-[356px]");
    expect(listCol.className).toContain("lg:w-[368px]");
    const toolbar = host.querySelector("[data-testid=todos-v2-toolbar]")!;
    expect(toolbar).toBeTruthy();
    expect(toolbar.querySelector("[data-testid=todos-kind-filter]")).toBeTruthy();
    expect(toolbar.querySelector("[data-testid=todos-grouping-menu]")).toBeTruthy();
    expect(toolbar.querySelector("[data-testid=todos-filter]")).toBeTruthy();
    expect(toolbar.querySelector("[data-testid=todos-reorganize-toggle]")).toBeTruthy();

    // 3. Task rows: max 2 lines, no redundant status line, PR anchor, direction meta, subtle priority & pending decision preserved
    const workingRow = host.querySelector<HTMLElement>('[data-todo="task_pr_working"]')!;
    expect(workingRow.dataset.todoRowLayout).toBe("task-slim");
    expect(workingRow.querySelector("[data-testid=todo-status-line]")).toBeNull();
    expect(workingRow.querySelector("[data-testid=todo-slim-pr]")?.textContent).toBe("PR #14830");
    expect(workingRow.querySelector("[data-testid=todo-direction-tag]")?.textContent).toBe("Platform");
    expect(workingRow.querySelector("[data-testid=todo-priority-subtle]")?.textContent).toContain(t("todos.priority_option_high"));

    const decisionRow = host.querySelector<HTMLElement>('[data-todo="task_decision"]')!;
    expect(decisionRow.dataset.todoRowLayout).toBe("task-slim");
    expect(decisionRow.querySelector("[data-testid=todo-ask]")?.textContent).toContain("Proceed with blue-green swap?");
    expect(decisionRow.querySelector("[data-testid=todo-priority-subtle]")?.textContent).toContain(t("todos.priority_option_high"));

    // 4. Personal to-do: minimal single-line layout with due date & subtle priority
    const personalRow = host.querySelector<HTMLElement>('[data-todo="personal_due"]')!;
    expect(personalRow.dataset.todoRowLayout).toBe("personal-slim");
    expect(personalRow.querySelector("[data-testid=todo-slim-line2]")).toBeNull();
    expect(personalRow.querySelector("[data-testid=todo-due-compact]")?.textContent).toBeTruthy();
    expect(personalRow.querySelector("[data-testid=todo-priority-subtle]")?.textContent).toContain(t("todos.priority_option_high"));

    // 5. Parked & Done sections start compactly folded with counts and external wait hint
    const parkedSec = host.querySelector<HTMLElement>('[data-testid="todo-section"][data-section="parked"]')!;
    expect(parkedSec.querySelector("button[aria-expanded]")?.getAttribute("aria-expanded")).toBe("false");
    expect(parkedSec.getAttribute("data-todos")).toBe("1");
    expect(parkedSec.querySelector("[data-testid=todo-section-folded-hint]")?.textContent).toContain(t("todos.parked_folded_hint"));
    expect(parkedSec.querySelector('[data-todo="task_parked"]')).toBeNull();

    const doneSec = host.querySelector<HTMLElement>('[data-testid="todo-section"][data-section="done"]')!;
    expect(doneSec.querySelector("button[aria-expanded]")?.getAttribute("aria-expanded")).toBe("false");
    expect(doneSec.getAttribute("data-todos")).toBe("1");
    expect(doneSec.querySelector('[data-todo="task_recent_done"]')).toBeNull();

    // Expanding Parked reveals its compact row
    await act(async () => parkedSec.querySelector<HTMLButtonElement>("button[aria-expanded]")!.click());
    await flush();
    expect(parkedSec.querySelector('[data-todo="task_parked"]')).toBeTruthy();

    // Turning todos_v2 off immediately restores legacy header, width, and rows
    await act(async () => patchAssistant({ todos_v2: false }));
    await flush();
    expect(host.querySelector("[data-testid=todos-v2-labs-link]")).toBeNull();
    expect(host.querySelector("[data-testid=todos-v2-toolbar]")).toBeNull();
    expect(host.querySelector("[data-testid=todos-summary]")).toBeTruthy();
    expect(host.querySelector("[data-testid=todos-list-column]")?.className).toContain("md:w-[380px]");
  });
});
