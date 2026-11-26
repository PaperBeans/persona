// @vitest-environment jsdom
// Controlled task links receive explicit data and route callbacks; default callers still use the store and router fallbacks.
import { act } from "react";
import type { MouseEvent as ReactMouseEvent } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import type { InboxItem } from "../../lib/inbox";
import type { Route } from "../../lib/router";
import type { Todo } from "../../lib/todos";
import type { InboxTaskLinksProps } from "./InboxTaskLinks";

const mocks = vi.hoisted(() => ({
  useStore: vi.fn(),
  useKnownDefault: vi.fn(),
  href: vi.fn(),
  onLinkClick: vi.fn(),
}));

vi.mock("../../lib/store", () => ({ useStore: mocks.useStore }));
vi.mock("../../lib/manager-choice", () => ({ useKnownDefault: mocks.useKnownDefault }));
vi.mock("../../lib/router", () => ({ href: mocks.href, onLinkClick: mocks.onLinkClick }));
vi.mock("../../ui/icons", () => ({ Icon: ({ name }: { name: string }) => <span data-icon={name} /> }));

const { InboxTaskLinks } = await import("./InboxTaskLinks");
const { t } = await import("../../i18n");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const item = {
  id: "event-controlled",
  source: "example",
  kind: "dm",
  account: "example-account",
  account_name: "Example",
  title: "A controlled inbox item",
  preview: "Open the linked work",
  count: 1,
  agent: { state: "drafted", todo_id: "todo-controlled", machine_id: "worker", session_id: "current" },
} as InboxItem;

const todo: Todo = {
  id: "todo-controlled",
  title: "Controlled task",
  created_at: "",
  updated_at: "",
  agents: [
    { machine_id: "worker", session_id: "previous", title: "Previous Agent", linked_at: "" },
    { machine_id: "worker", session_id: "current", title: "Current Agent", linked_at: "" },
  ],
  lineage: [{
    todo_id: "todo-controlled",
    machine: "worker",
    session_id: "current",
    agent: "Current Agent",
    current: true,
    started: "",
  }],
};

let host: HTMLDivElement;
let root: Root;
let resolveHref: Mock<(route: Route) => string>;
let onRouteClick: Mock<(event: ReactMouseEvent, route: Route) => void>;

const mount = async (props: Partial<InboxTaskLinksProps> = {}) => {
  await act(async () => root.render(<InboxTaskLinks item={item} {...props} />));
};
const links = () => [...host.querySelectorAll<HTMLAnchorElement>("[data-inbox-task-links] a")];

beforeEach(() => {
  vi.clearAllMocks();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  resolveHref = vi.fn((route) => route.name === "todos"
    ? `/panel/tasks/${route.id ?? ""}`
    : `/panel/sessions/${"mid" in route ? route.mid : ""}/${"sid" in route ? route.sid : ""}`);
  onRouteClick = vi.fn((event) => { event.preventDefault(); });
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("InboxTaskLinks with controlled data and navigation", () => {
  it("renders explicit tasks without calling default store or router singletons", async () => {
    await mount({ todos: [todo], defaultManager: "assistant", resolveHref, onRouteClick });

    const rendered = links();
    expect(rendered.map((link) => link.textContent)).toEqual([
      `${t("inbox-inboxdetail.linked_task")}Controlled task`,
      `${t("inbox-inboxdetail.linked_agent")}Current Agent`,
    ]);
    expect(rendered.map((link) => link.getAttribute("href"))).toEqual([
      "/panel/tasks/todo-controlled",
      "/panel/sessions/worker/current",
    ]);
    expect(host.textContent).not.toContain("Previous Agent");
    expect(mocks.useStore).not.toHaveBeenCalled();
    expect(mocks.useKnownDefault).not.toHaveBeenCalled();
    expect(mocks.href).not.toHaveBeenCalled();
    expect(mocks.onLinkClick).not.toHaveBeenCalled();
  });

  it("sends task and Agent route activation to the controlled callback", async () => {
    await mount({ todos: [todo], defaultManager: "assistant", resolveHref, onRouteClick });

    act(() => links()[0].click());
    expect(onRouteClick).toHaveBeenCalledWith(expect.any(Object), { name: "todos", id: "todo-controlled" });
    act(() => links()[1].click());
    expect(onRouteClick).toHaveBeenCalledWith(expect.any(Object), { name: "session", mid: "worker", sid: "current" });
    expect(mocks.onLinkClick).not.toHaveBeenCalled();
  });
});

describe("InboxTaskLinks default compatibility", () => {
  it("uses the default manager, store and router helpers when controlled props are absent", async () => {
    mocks.useKnownDefault.mockReturnValue("assistant");
    mocks.useStore.mockImplementation(<T,>(selector: (state: { todos: Todo[] }) => T) => selector({ todos: [todo] }));
    mocks.href.mockImplementation((route) => `/default/${route.name}`);
    mocks.onLinkClick.mockImplementation((event) => { event.preventDefault(); });

    await mount();

    expect(mocks.useKnownDefault).toHaveBeenCalledTimes(1);
    expect(mocks.useStore).toHaveBeenCalledWith(expect.any(Function));
    expect(mocks.href).toHaveBeenCalledWith({ name: "todos", id: "todo-controlled" });
    expect(mocks.href).toHaveBeenCalledWith({ name: "session", mid: "worker", sid: "current" });
    expect(links().map((link) => link.getAttribute("href"))).toEqual(["/default/todos", "/default/session"]);

    act(() => links()[0].click());
    expect(mocks.onLinkClick).toHaveBeenCalledWith(expect.any(Object), { name: "todos", id: "todo-controlled" });
    expect(onRouteClick).not.toHaveBeenCalled();
  });
});
