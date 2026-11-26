// @vitest-environment jsdom
// Controlled ShellSwitch, ShellHeaderSlots, ChatSidebarButton, and ShellSwitchView bypass useScopedCounts, useStore, and router singletons; default fallbacks stay pinned too.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Route } from "../lib/router";
import type { Interaction, Session } from "../lib/types";
import type { ChatSidebarButtonDeps, ShellPage, ShellSwitchDeps } from "./ShellSwitch";

const mocks = vi.hoisted(() => ({
  useScopedCounts: vi.fn(() => ({ needs: 0, working: 0, waiting: 0, unread: 0, badge: 0, interactions: 0, quietCounted: false })),
  useStore: vi.fn(),
  href: vi.fn((to: Route) => (to.name === "persona" ? "/" : "/agents")),
  onLinkClick: vi.fn((e: { preventDefault: () => void }) => e.preventDefault()),
}));

vi.mock("../lib/current-assistant", () => ({
  useScopedCounts: mocks.useScopedCounts,
}));
vi.mock("../lib/store", () => ({
  useStore: mocks.useStore,
}));
vi.mock("../lib/router", () => ({
  href: mocks.href,
  onLinkClick: mocks.onLinkClick,
}));

const {
  ChatSidebarButton,
  ChatSidebarButtonView,
  ShellHeaderSlots,
  ShellHeaderSlotsView,
  ShellSwitch,
  ShellSwitchView,
} = await import("./ShellSwitch");
const { initI18n, t } = await import("../i18n");

const makeSession = (patch: Partial<Session> = {}): Session => ({
  machine_id: "mac",
  session_id: "s1",
  title: "Worker",
  cwd: "/code",
  status: "waiting_for_approval",
  state: "needs_you",
  ...patch,
});

const makeInteraction = (patch: Partial<Interaction> = {}): Interaction => ({
  id: "ix-1",
  machine_id: "mac",
  session_id: "s1",
  kind: "permission",
  title: "Allow tool",
  ...patch,
}) as Interaction;

let host: HTMLDivElement;
let root: Root;

const links = () => [...host.querySelectorAll<HTMLAnchorElement>("nav a")];
const sidebarBtn = () => host.querySelector<HTMLButtonElement>("button");

const expectNoDefaultSingletons = () => {
  expect(mocks.useScopedCounts).not.toHaveBeenCalled();
  expect(mocks.useStore).not.toHaveBeenCalled();
  expect(mocks.href).not.toHaveBeenCalled();
  expect(mocks.onLinkClick).not.toHaveBeenCalled();
};

beforeEach(async () => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  await initI18n("en");
  mocks.useScopedCounts.mockReset().mockReturnValue({ needs: 0, working: 0, waiting: 0, unread: 0, badge: 0, interactions: 0, quietCounted: false });
  mocks.useStore.mockReset();
  mocks.href.mockReset().mockImplementation((to: Route) => (to.name === "persona" ? "/" : "/agents"));
  mocks.onLinkClick.mockReset().mockImplementation((e: { preventDefault: () => void }) => e.preventDefault());
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("ShellSwitch controlled deps and view separation", () => {
  it("runs ShellSwitch and ShellHeaderSlots fully controlled via waiting/counts/useScopedCounts and route deps", async () => {
    const routeHref = vi.fn((to: Route) => (to.name === "persona" ? "#shell/assistant" : "#shell/agents"));
    const onRouteClick = vi.fn((e: { preventDefault: () => void }, _to: Route, _page: ShellPage) => e.preventDefault());
    const onToggleSidebar = vi.fn();
    const deps: ShellSwitchDeps = {
      counts: () => ({ badge: 4, quietCounted: false }),
      routeHref,
      onRouteClick,
    };

    await act(async () => root.render(
      <ShellHeaderSlots page="assistant" onToggleSidebar={onToggleSidebar} end={<span data-end>End</span>} deps={deps} />,
    ));

    const [assistantLink, agentsLink] = links();
    expect(assistantLink.getAttribute("href")).toBe("#shell/assistant");
    expect(assistantLink.getAttribute("aria-current")).toBe("page");
    expect(agentsLink.getAttribute("href")).toBe("#shell/agents");
    expect(agentsLink.getAttribute("aria-current")).toBeNull();
    expect(agentsLink.getAttribute("aria-label")).toBe(t("shellswitch.waiting_label", { label: t("agentspage.title"), count: 4 }));
    expect(agentsLink.textContent).toContain("4");

    // Clicking active segment prevents default and does not call onRouteClick.
    const activeEvent = new MouseEvent("click", { bubbles: true, cancelable: true });
    await act(async () => { assistantLink.dispatchEvent(activeEvent); });
    expect(activeEvent.defaultPrevented).toBe(true);
    expect(onRouteClick).not.toHaveBeenCalled();

    // Clicking inactive segment invokes controlled onRouteClick with target route and page.
    await act(async () => agentsLink.click());
    expect(onRouteClick).toHaveBeenCalledTimes(1);
    expect(onRouteClick).toHaveBeenCalledWith(expect.anything(), { name: "agent-list" }, "agents");

    // Custom useScopedCounts hook override and direct waiting override also skip default singletons.
    const customHook = vi.fn(() => ({ badge: 2 }));
    await act(async () => root.render(
      <ShellSwitch page="agents" deps={{ useScopedCounts: customHook, routeHref, onRouteClick }} />,
    ));
    expect(customHook).toHaveBeenCalledTimes(1);
    expect(links()[1].getAttribute("aria-current")).toBe("page");
    expect(links()[1].textContent).toContain("2");

    await act(async () => root.render(
      <ShellSwitch page="assistant" deps={{ waiting: 0, routeHref, onRouteClick }} />,
    ));
    expect(links()[1].querySelector("span[aria-hidden]")).toBeNull();

    await act(async () => root.render(
      <ShellSwitch page="assistant" deps={{ waiting: -3, routeHref, onRouteClick }} />,
    ));
    expect(links()[1].querySelector("span[aria-hidden]")).toBeNull();

    expectNoDefaultSingletons();
  });

  it("computes ChatSidebarButton waiting counts via controlled deps without touching useScopedCounts or useStore", async () => {
    const onClick = vi.fn();
    const openNeedsYou = makeSession({ state: "needs_you" });

    // Open needs_you session (not held) subtracts 1 from badge: 3 -> 2.
    await act(async () => root.render(
      <ChatSidebarButton session={openNeedsYou} counted onClick={onClick} deps={{ counts: { badge: 3, quietCounted: false }, isHeld: false }} />,
    ));
    expect(sidebarBtn()!.getAttribute("aria-label")).toBe(t("shellswitch.waiting_label", { label: t("chat-chatview.sidebar"), count: 2 }));
    expect(host.textContent).toContain("2");

    // Open needs_you session marked held via predicate keeps full badge: 3 -> 3.
    const isHeld = vi.fn((s: Session) => s.session_id === "s1");
    await act(async () => root.render(
      <ChatSidebarButton session={openNeedsYou} counted onClick={onClick} deps={{ counts: { badge: 3, quietCounted: false }, isHeld }} />,
    ));
    expect(isHeld).toHaveBeenCalledWith(openNeedsYou);
    expect(host.textContent).toContain("3");

    // Controlled attention with pushGrading=true and P1 card treats session as held (not subtracted).
    const gradedDeps: ChatSidebarButtonDeps = {
      useScopedCounts: () => ({ badge: 2, quietCounted: false }),
      attention: { interactions: [makeInteraction({ push_level: "P1" })], pushGrading: true },
    };
    await act(async () => root.render(
      <ChatSidebarButton session={openNeedsYou} counted onClick={onClick} deps={gradedDeps} />,
    ));
    expect(host.textContent).toContain("2");

    // Controlled attention with pushGrading=false and urgent card subtracts open session (2 -> 1).
    await act(async () => root.render(
      <ChatSidebarButton
        session={openNeedsYou}
        counted
        onClick={onClick}
        deps={{ counts: { badge: 2, quietCounted: false }, attention: () => ({ interactions: [makeInteraction({ push_level: "P0" })], pushGrading: false }) }}
      />,
    ));
    expect(host.textContent).toContain("1");

    // Supervisor-held card marks session held even when pushGrading=false (2 -> 2).
    await act(async () => root.render(
      <ChatSidebarButton
        session={openNeedsYou}
        counted
        onClick={onClick}
        deps={{ counts: { badge: 2, quietCounted: false }, attention: { interactions: [makeInteraction({ delegating: true })], pushGrading: false } }}
      />,
    ));
    expect(host.textContent).toContain("2");

    // Quiet session only subtracts when quietCounted=true.
    const quietNeedsYou = makeSession({ state: "needs_you", quiet: true });
    await act(async () => root.render(
      <ChatSidebarButton session={quietNeedsYou} counted onClick={onClick} deps={{ counts: { badge: 2, quietCounted: false }, isHeld: false }} />,
    ));
    expect(host.textContent).toContain("2");
    await act(async () => root.render(
      <ChatSidebarButton session={quietNeedsYou} counted onClick={onClick} deps={{ counts: { badge: 2, quietCounted: true }, isHeld: false }} />,
    ));
    expect(host.textContent).toContain("1");

    // counted=false hides badge and skips useStore even without isHeld/attention.
    await act(async () => root.render(
      <ChatSidebarButton session={openNeedsYou} counted={false} onClick={onClick} deps={{ counts: { badge: 5 } }} />,
    ));
    expect(host.querySelector("span[aria-hidden]")).toBeNull();
    expect(sidebarBtn()!.getAttribute("aria-label")).toBe(t("chat-chatview.sidebar"));

    // Direct waiting override respects counted flag and clamps <= 0.
    await act(async () => root.render(
      <ChatSidebarButton session={openNeedsYou} counted onClick={onClick} deps={{ waiting: 7 }} />,
    ));
    expect(host.textContent).toContain("7");
    await act(async () => root.render(
      <ChatSidebarButton session={openNeedsYou} counted={false} onClick={onClick} deps={{ waiting: 7 }} />,
    ));
    expect(host.querySelector("span[aria-hidden]")).toBeNull();

    await act(async () => sidebarBtn()!.click());
    expect(onClick).toHaveBeenCalledTimes(1);
    expectNoDefaultSingletons();
  });

  it("renders pure ShellSwitchView, ShellHeaderSlotsView, and ChatSidebarButtonView without default singletons", async () => {
    const onToggle = vi.fn();
    const routeHref = (to: Route) => `#v/${to.name}`;
    const onRouteClick = vi.fn((e: { preventDefault: () => void }) => e.preventDefault());

    await act(async () => root.render(
      <ShellHeaderSlotsView page="agents" waiting={3} onToggleSidebar={onToggle} endWide end={<button type="button">Action</button>} deps={{ routeHref, onRouteClick }} />,
    ));
    expect(links()[0].getAttribute("href")).toBe("#v/persona");
    expect(links()[1].getAttribute("aria-current")).toBe("page");
    expect(links()[1].textContent).toContain("3");
    await act(async () => links()[0].click());
    expect(onRouteClick).toHaveBeenCalledTimes(1);

    await act(async () => root.render(<ShellSwitchView page="assistant" waiting={1} deps={{ routeHref, onRouteClick }} />));
    expect(links()[0].getAttribute("aria-current")).toBe("page");

    const onSidebarClick = vi.fn();
    await act(async () => root.render(<ChatSidebarButtonView waiting={2} onClick={onSidebarClick} />));
    expect(host.textContent).toContain("2");
    await act(async () => sidebarBtn()!.click());
    expect(onSidebarClick).toHaveBeenCalledTimes(1);
    expectNoDefaultSingletons();
  });

  it("preserves default useScopedCounts, useStore, and router fallbacks when deps are omitted", async () => {
    mocks.useScopedCounts.mockReturnValue({ needs: 3, working: 0, waiting: 0, unread: 0, badge: 3, interactions: 0, quietCounted: false });
    const fakeStore = { pushGrading: false, interactions: [] as Interaction[] };
    mocks.useStore.mockImplementation((sel: (s: typeof fakeStore) => unknown) => sel(fakeStore));

    await act(async () => root.render(<ShellHeaderSlots page="assistant" onToggleSidebar={() => {}} end={null} />));
    expect(mocks.useScopedCounts).toHaveBeenCalled();
    expect(mocks.href).toHaveBeenCalledWith({ name: "persona" });
    expect(mocks.href).toHaveBeenCalledWith({ name: "agent-list" });
    expect(links()[1].textContent).toContain("3");

    await act(async () => links()[1].click());
    expect(mocks.onLinkClick).toHaveBeenCalledWith(expect.anything(), { name: "agent-list" });

    await act(async () => root.render(<ChatSidebarButton session={makeSession({ state: "needs_you" })} counted onClick={() => {}} />));
    expect(mocks.useStore).toHaveBeenCalled();
    expect(host.textContent).toContain("2");
  });
});
