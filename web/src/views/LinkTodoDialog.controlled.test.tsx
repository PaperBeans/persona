// @vitest-environment jsdom
// Controlled LinkTodoDialog supplies todos, sessions, projects, and mutation/notification callbacks without touching default stores, hub APIs, or toast; default fallbacks stay pinned too.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../lib/api";
import type { Todo } from "../lib/todos";
import type { TodoProject } from "../lib/todo-projects";
import type { Session } from "../lib/types";
import type { LinkTargetSpace, LinkTodoDialogProps } from "./LinkTodoDialog";

const mocks = vi.hoisted(() => ({
  useStore: vi.fn(),
  refresh: vi.fn(),
  refreshTodos: vi.fn(),
  setSpacesSupported: vi.fn(),
  useAgentSessions: vi.fn(),
  useTodoProjects: vi.fn(),
  createTodo: vi.fn(),
  linkAgentToTodo: vi.fn(),
  linkSpaceToTodo: vi.fn(),
  unlinkAgentFromTodo: vi.fn(),
  unlinkSpaceFromTodo: vi.fn(),
  toast: vi.fn(),
}));

vi.mock("../lib/store", () => ({
  useStore: (...args: unknown[]) => mocks.useStore(...args),
  refresh: (...args: unknown[]) => mocks.refresh(...args),
  refreshTodos: (...args: unknown[]) => mocks.refreshTodos(...args),
  setSpacesSupported: (...args: unknown[]) => mocks.setSpacesSupported(...args),
}));

vi.mock("../lib/sessions", () => ({
  useAgentSessions: (...args: unknown[]) => mocks.useAgentSessions(...args),
}));

vi.mock("../lib/todo-projects", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/todo-projects")>()),
  useTodoProjects: (...args: unknown[]) => mocks.useTodoProjects(...args),
}));

vi.mock("../lib/todos", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/todos")>()),
  createTodo: (...args: unknown[]) => mocks.createTodo(...args),
  linkAgentToTodo: (...args: unknown[]) => mocks.linkAgentToTodo(...args),
  linkSpaceToTodo: (...args: unknown[]) => mocks.linkSpaceToTodo(...args),
  unlinkAgentFromTodo: (...args: unknown[]) => mocks.unlinkAgentFromTodo(...args),
  unlinkSpaceFromTodo: (...args: unknown[]) => mocks.unlinkSpaceFromTodo(...args),
}));

vi.mock("../ui/atoms", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../ui/atoms")>()),
  toast: (...args: unknown[]) => mocks.toast(...args),
}));

vi.hoisted(() => {
  (globalThis as Record<string, unknown>).matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
});

const { LinkTodoDialog } = await import("./LinkTodoDialog");
const { t } = await import("../i18n");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const NOW = new Date(2026, 0, 15, 12).getTime();
const minutesAgo = (m: number) => new Date(NOW - m * 60_000).toISOString();
const makeTodo = (id: string, title: string, patch: Partial<Todo> = {}): Todo => ({
  id,
  title,
  created_at: minutesAgo(60),
  updated_at: minutesAgo(5),
  ...patch,
});
const makeSession = (mid: string, sid: string, title = sid, patch: Partial<Session> = {}): Session => ({
  machine_id: mid,
  session_id: sid,
  title,
  cwd: `/work/${sid}`,
  status: "running",
  ...patch,
});

const SAMPLE_PROJECTS: TodoProject[] = [{ slug: "alpha", name: "Alpha Project", long: true }];

let host: HTMLDivElement;
let root: Root;
let fetchSpy: ReturnType<typeof vi.fn>;

const mount = async (props: LinkTodoDialogProps) => {
  await act(async () => {
    root.render(<LinkTodoDialog {...props} />);
  });
};

const flush = async () => {
  for (let i = 0; i < 4; i++) {
    await act(async () => {
      await Promise.resolve();
    });
  }
};

const dialog = () => document.querySelector<HTMLElement>('[role="dialog"]');
const todoRows = () => [...document.querySelectorAll<HTMLButtonElement>("[data-link-todos] button")];
const findButton = (label: string) => [...dialog()!.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === label);
const setInputValue = (el: HTMLInputElement, value: string) => act(() => {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(el, value);
  el.dispatchEvent(new Event("input", { bubbles: true }));
});

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(NOW);
  vi.clearAllMocks();
  fetchSpy = vi.fn(() => {
    throw new Error("unexpected fetch");
  });
  vi.stubGlobal("fetch", fetchSpy);
  mocks.useStore.mockImplementation((selector: (s: { todos: Todo[] }) => unknown) => selector({ todos: [] }));
  mocks.useAgentSessions.mockReturnValue([]);
  mocks.useTodoProjects.mockReturnValue([]);
  mocks.refresh.mockResolvedValue(undefined);
  mocks.refreshTodos.mockResolvedValue(undefined);
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

describe("LinkTodoDialog with controlled props", () => {
  it("renders controlled todos, projects, and sessions and handles session link/unlink/create without touching default singletons", async () => {
    const session = makeSession("m1", "s1", "Search refactor");
    const todos = [
      makeTodo("t1", "Ship search", { project: "alpha" }),
      makeTodo("t2", "Completed item", { done_at: minutesAgo(1) }),
    ];
    const onClose = vi.fn();
    const onLinkSession = vi.fn().mockResolvedValue(undefined);
    const onUnlinkSession = vi.fn().mockResolvedValue(undefined);
    const onCreateTodo = vi.fn().mockResolvedValue(makeTodo("t3", "New follow-up"));
    const onRefresh = vi.fn().mockResolvedValue(undefined);
    const onNotify = vi.fn();

    await mount({
      open: true,
      session,
      onClose,
      todos,
      currentTodoId: "t1",
      sessions: [session],
      projects: SAMPLE_PROJECTS,
      onLinkSession,
      onUnlinkSession,
      onCreateTodo,
      onRefresh,
      onNotify,
    });

    expect(todoRows()).toHaveLength(1);
    expect(todoRows()[0].textContent).toContain("Ship search");
    expect(todoRows()[0].textContent).toContain("Alpha Project");

    // Link existing todo
    await act(async () => todoRows()[0].click());
    await flush();
    expect(onLinkSession).toHaveBeenCalledWith(todos[0], session);
    expect(onRefresh).toHaveBeenCalledWith("all");
    expect(onNotify).toHaveBeenCalledWith(t("linktododialog.session_linked", { title: "Ship search" }), "ok");
    expect(onClose).toHaveBeenCalledTimes(1);

    // Unlink current todo
    await act(async () => findButton(t("linktododialog.session_unlink"))!.click());
    await flush();
    expect(onUnlinkSession).toHaveBeenCalledWith("t1", session);
    expect(onNotify).toHaveBeenLastCalledWith(t("linktododialog.session_unlinked"), "ok");
    expect(onClose).toHaveBeenCalledTimes(2);

    // Create and link new todo
    act(() => findButton(t("linktododialog.new_todo"))!.click());
    const input = dialog()!.querySelector<HTMLInputElement>(`input[placeholder="${t("linktododialog.new_placeholder")}"]`)!;
    setInputValue(input, "  New follow-up  ");
    await act(async () => findButton(t("linktododialog.create_and_link"))!.click());
    await flush();
    expect(onCreateTodo).toHaveBeenCalledWith("New follow-up");
    expect(onLinkSession).toHaveBeenLastCalledWith(expect.objectContaining({ id: "t3", title: "New follow-up" }), session);
    expect(onNotify).toHaveBeenLastCalledWith(t("linktododialog.session_created_linked", { title: "New follow-up" }), "ok");
    expect(onClose).toHaveBeenCalledTimes(3);

    expect(mocks.useStore).not.toHaveBeenCalled();
    expect(mocks.useAgentSessions).not.toHaveBeenCalled();
    expect(mocks.useTodoProjects).not.toHaveBeenCalled();
    expect(mocks.linkAgentToTodo).not.toHaveBeenCalled();
    expect(mocks.unlinkAgentFromTodo).not.toHaveBeenCalled();
    expect(mocks.createTodo).not.toHaveBeenCalled();
    expect(mocks.refresh).not.toHaveBeenCalled();
    expect(mocks.refreshTodos).not.toHaveBeenCalled();
    expect(mocks.setSpacesSupported).not.toHaveBeenCalled();
    expect(mocks.toast).not.toHaveBeenCalled();
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("routes controlled space legacy-404 errors and skips global refreshes when onRefresh is omitted", async () => {
    const space: LinkTargetSpace = { machineId: "m1", space: "web" };
    const todos = [makeTodo("t1", "Space work")];
    const onClose = vi.fn();
    const legacy404 = new ApiError(404, "Not Found", { error: "Not Found", code: "not_found" }, true);
    const onLinkSpace = vi.fn().mockRejectedValueOnce(legacy404);
    const onSpacesUnsupported = vi.fn();
    const onNotify = vi.fn();

    await mount({
      open: true,
      space,
      onClose,
      todos,
      currentTodoId: null,
      sessions: [],
      projects: [],
      onLinkSpace,
      onSpacesUnsupported,
      onNotify,
    });

    await act(async () => todoRows()[0].click());
    await flush();

    expect(onLinkSpace).toHaveBeenCalledWith(todos[0], space);
    expect(onSpacesUnsupported).toHaveBeenCalledTimes(1);
    expect(onNotify).toHaveBeenCalledWith(t("linktododialog.space_unsupported"), "error");
    expect(onClose).not.toHaveBeenCalled();
    expect(mocks.setSpacesSupported).not.toHaveBeenCalled();
    expect(mocks.refreshTodos).not.toHaveBeenCalled();
    expect(mocks.refresh).not.toHaveBeenCalled();
    expect(mocks.toast).not.toHaveBeenCalled();
  });

  it("isolates in-flight busy state, notify, and onClose across ABA target switches", async () => {
    const sessionA = makeSession("m1", "session-a", "Session A");
    const sessionB = makeSession("m1", "session-b", "Session B");
    const todos = [makeTodo("t1", "Target todo")];
    const pending: { sid: string; resolve: () => void; reject: (err: Error) => void }[] = [];
    const onLinkSession = vi.fn((_todo: Todo, s: Session) => new Promise<void>((resolve, reject) => {
      pending.push({ sid: s.session_id, resolve, reject });
    }));
    const onClose = vi.fn();
    const onNotify = vi.fn();
    const baseProps = {
      open: true,
      onClose,
      todos,
      currentTodoId: null,
      sessions: [sessionA, sessionB],
      projects: [],
      onLinkSession,
      onNotify,
    };

    // Start request 1 on target A
    await mount({ ...baseProps, session: sessionA });
    await act(async () => todoRows()[0].click());
    expect(todoRows()[0].disabled).toBe(true);
    expect(pending).toHaveLength(1);

    // Switch to target B: busy clears immediately
    await mount({ ...baseProps, session: sessionB });
    expect(todoRows()[0].disabled).toBe(false);

    // Switch back to target A (ABA) and start request 2
    await mount({ ...baseProps, session: sessionA });
    expect(todoRows()[0].disabled).toBe(false);
    await act(async () => todoRows()[0].click());
    expect(todoRows()[0].disabled).toBe(true);
    expect(pending).toHaveLength(2);

    // Settling stale request 1 must not unlock busy, notify, or close target A's active request 2
    await act(async () => { pending[0].resolve(); });
    await flush();
    expect(todoRows()[0].disabled).toBe(true);
    expect(onNotify).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();

    // Settling active request 2 completes normally
    await act(async () => { pending[1].resolve(); });
    await flush();
    expect(onNotify).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(todoRows()[0].disabled).toBe(false);

    // Reopen A, start request 3, close dialog, reopen A, reject stale request 3: no stale error toast
    await mount({ ...baseProps, session: sessionA });
    await act(async () => todoRows()[0].click());
    expect(pending).toHaveLength(3);
    await mount({ ...baseProps, open: false, session: sessionA });
    await mount({ ...baseProps, open: true, session: sessionA });
    expect(todoRows()[0].disabled).toBe(false);
    await act(async () => { pending[2].reject(new Error("stale failure")); });
    await flush();
    expect(onNotify).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});

describe("LinkTodoDialog default compatibility", () => {
  it("uses default store, todo APIs, refresh, and toast when optional props are omitted", async () => {
    const session = makeSession("m1", "s-default", "Default session", { todo: { id: "t1", title: "Default todo" } });
    const todos = [makeTodo("t1", "Default todo", { project: "alpha" })];
    mocks.useStore.mockImplementation((selector: (s: { todos: Todo[] }) => unknown) => selector({ todos }));
    mocks.useAgentSessions.mockReturnValue([session]);
    mocks.useTodoProjects.mockReturnValue(SAMPLE_PROJECTS);
    mocks.linkAgentToTodo.mockResolvedValue(undefined);
    mocks.unlinkAgentFromTodo.mockResolvedValue(undefined);
    const onClose = vi.fn();

    await mount({ open: true, session, onClose });
    expect(mocks.useStore).toHaveBeenCalled();
    expect(mocks.useAgentSessions).toHaveBeenCalled();
    expect(mocks.useTodoProjects).toHaveBeenCalled();
    expect(todoRows()[0].textContent).toContain("Alpha Project");

    await act(async () => todoRows()[0].click());
    await flush();
    expect(mocks.linkAgentToTodo).toHaveBeenCalledWith("t1", "m1", "s-default");
    expect(mocks.refreshTodos).toHaveBeenCalledTimes(1);
    expect(mocks.refresh).toHaveBeenCalledTimes(1);
    expect(mocks.toast).toHaveBeenCalledWith(t("linktododialog.session_linked", { title: "Default todo" }), "ok");
    expect(onClose).toHaveBeenCalledTimes(1);

    await act(async () => findButton(t("linktododialog.session_unlink"))!.click());
    await flush();
    expect(mocks.unlinkAgentFromTodo).toHaveBeenCalledWith("t1", "m1", "s-default");
    expect(mocks.toast).toHaveBeenLastCalledWith(t("linktododialog.session_unlinked"), "ok");
  });
});
