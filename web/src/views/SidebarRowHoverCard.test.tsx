// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../i18n";
import { personalAgentDraftKey, type AgentSession } from "../lib/agents";
import { resetAgentsDisplay, seedAgentsDisplay } from "../lib/agents-display";
import { storePageDraft } from "../lib/drafts";
import { navigate } from "../lib/router";
import { stateName } from "../lib/state-labels";
import { getState, reset } from "../lib/store";
import type { Session } from "../lib/types";
import { SidebarRowHoverHost } from "./SidebarRowHover";
import { AgentRow, SessionRow } from "./SidebarRows";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const title = "1125 | Design | A long conversation title about comparing the accessibility of two sidebar designs";
const updated = new Date(2026, 10, 15, 10, 15).toISOString();
const session: Session = {
  machine_id: "studio", session_id: "review", title, cwd: "/work/design", workspace_cwd: "/work/design/review",
  status: "working", state: "working", agent: "agy", launcher: "design-cli", model: "claude-opus-5-5",
  updated_at: updated, branch: "feat/sidebar", todo: { id: "launch", title: "Review the design" },
  pane_id: "w1:p1", slot_id: "w1:t1", slot_name: "Review tab",
  last_text: "**The comparison is ready.** Here are the results for the two versions of the sidebar.",
};
const personal: AgentSession = { id: "example:brief", source: "example", sessionKey: "brief", title: "Weekly brief", provider: "bot", unreadCount: 3, canSend: true, rankTimeMs: Date.parse(updated) };
const common = { reg: () => () => {}, hover: () => {}, active: false, go: () => (e: React.MouseEvent) => e.preventDefault() };

let root: Root;
let host: HTMLElement;
let fine: boolean;
let rowTop: number;
let sidebarRight: number;
const card = () => document.querySelector<HTMLElement>("[data-sidebar-row-hover-card]");
const preview = () => card()?.querySelector("[data-sidebar-hover-preview]")?.textContent;
function metadata() {
  const labels = [...(card()?.querySelectorAll("dt") ?? [])];
  return Object.fromEntries(labels.map((label) => [label.textContent, label.nextElementSibling?.textContent]));
}
const mockResponse = (data: unknown) => new Response(JSON.stringify(data), { headers: { "Content-Type": "application/json" } });

function render(row: ReactNode = <SessionRow {...common} s={session} k="s:studio/review" to={{ name: "session", mid: "studio", sid: "review" }} onMenu={() => {}} />) {
  act(() => root.render(<>
    <aside data-sidebar-root><div data-sidebar-rows>{row}</div></aside>
    <SidebarRowHoverHost />
    <div data-outside />
  </>));
  return host.querySelector<HTMLAnchorElement>("aside a")!;
}

function enter(row: HTMLElement, pointerType = "mouse") {
  act(() => row.dispatchEvent(new PointerEvent("pointerover", { pointerType, bubbles: true, relatedTarget: document.body })));
}

async function dwell(ms = 250) {
  await act(async () => { vi.advanceTimersByTime(ms); });
  await act(async () => { await vi.dynamicImportSettled(); });
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date(2026, 10, 25, 11, 15));
  localStorage.clear();
  reset();
  getState().machines = [{ id: "studio", name: "Studio", online: true }];
  history.replaceState(null, "", "/");
  fine = true;
  rowTop = 120;
  sidebarRight = 284;
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
  vi.stubGlobal("matchMedia", (media: string) => ({ matches: fine, media, addEventListener() {}, removeEventListener() {} }));
  vi.stubGlobal("fetch", vi.fn(async (url: string) => mockResponse(
    new URL(url, location.href).pathname.endsWith("/model-config") ? { providers: [{ id: "launcher.design-cli", name: "Design launcher" }] } : {},
  )));
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
    return { top: rowTop, right: sidebarRight, height: 32 } as DOMRect;
  });
  vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockReturnValue(240);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  resetAgentsDisplay();
  reset();
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("sidebar row hover details", () => {
  it("waits for the dwell, then portals the complete title, plain-text preview and structured metadata", async () => {
    const row = render();
    row.setAttribute("aria-describedby", "existing-description");
    expect(row.hasAttribute("title")).toBe(false);
    expect(row.href).toContain("/s/studio/review");
    enter(row);
    await dwell(249);
    expect(card()).toBeNull();
    expect(fetch).not.toHaveBeenCalled();
    await dwell(1);
    expect(card()?.parentElement).toBe(document.body);
    expect(card()?.getAttribute("role")).toBe("tooltip");
    expect(row.getAttribute("aria-describedby")).toBe(`existing-description ${card()!.id}`);
    expect(card()?.firstElementChild?.textContent).toBe(title);
    expect(preview()).toBe("The comparison is ready. Here are the results for the two versions of the sidebar.");
    expect(card()?.querySelector("time")?.dateTime).toBe(updated);
    expect(metadata()).toMatchObject({
      [t("sidebar-row-hover.status")]: stateName("working"),
      [t("sidebar-row-hover.tool")]: expect.stringContaining("Design launcher"),
      [t("sidebar-row-hover.model")]: "opus 5.5",
      [t("sidebar-row-hover.active")]: `${t("lib-relative-time.days_ago", { count: 10 })} · 2026-11-15 10:15`,
      [t("sidebar-row-hover.computer")]: "Studio",
      [t("sidebar-row-hover.workspace")]: "/work/design/review",
      [t("sidebar-row-hover.branch")]: "feat/sidebar",
      [t("sidebar-row-hover.tab")]: "Review tab",
      [t("sidebar-row-hover.todo")]: "Review the design",
    });
    expect(card()?.style.left).toBe("292px");
    expect(card()?.style.top).toBe("120px");
    act(() => row.dispatchEvent(new PointerEvent("pointerout", { pointerType: "mouse", bubbles: true, relatedTarget: document.body })));
    expect(row.getAttribute("aria-describedby")).toBe("existing-description");
    expect(card()).toBeNull();
  });

  it("shows a pending question and prefers a draft when one is saved", async () => {
    const chat = { ...session, session_id: "trip", assistant: true, state: "needs_you" as const, todo: undefined, branch: undefined };
    getState().interactions = [{ id: "q", machine_id: "studio", session_id: "trip", kind: "question", summary: "Which version should we try?" }];
    const row = render(<SessionRow {...common} s={chat} k="j:trip" to={{ name: "persona", sid: "trip" }} showState={false} />);
    enter(row);
    await dwell();
    expect(preview()).toContain("Which version should we try?");
    expect(metadata()[t("sidebar-row-hover.status")]).toBe(stateName("needs_you"));
    expect(metadata()).not.toHaveProperty(t("sidebar-row-hover.todo"));
    expect(metadata()).not.toHaveProperty(t("sidebar-row-hover.branch"));
    act(() => window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" })));
    storePageDraft("studio/trip", "Use the second version\nwith a smaller sidebar.");
    enter(row);
    await dwell();
    expect(preview()).toBe(t("todos.kicker_line", { kicker: t("sidebar.draft") }) + "Use the second version with a smaller sidebar.");
  });

  it("keeps the conversation title in the card when the terminal tab names the row", async () => {
    seedAgentsDisplay({ configured: true, show_botmux: true, use_herdr_tab_name: true });
    const row = render();
    expect(row.textContent).toContain("Review tab");
    expect(row.textContent).not.toContain(title);
    enter(row);
    await dwell();
    expect([...card()!.querySelectorAll("p")].slice(0, 2).map((p) => p.textContent)).toEqual(["Review tab", title]);
  });

  it("clamps the card within a narrow viewport and lifts it above the bottom edge", async () => {
    vi.stubGlobal("innerWidth", 416);
    vi.stubGlobal("innerHeight", 400);
    rowTop = 350;
    sidebarRight = 300;
    enter(render());
    await dwell();
    expect(card()?.style.left).toBe("48px");
    expect(card()?.style.top).toBe("152px");
    expect(card()?.style.maxHeight).toBe("384px");
  });

  it("never opens for touch or a coarse pointer", async () => {
    const row = render();
    enter(row, "touch");
    await dwell();
    expect(card()).toBeNull();
    fine = false;
    enter(row);
    await dwell();
    expect(card()).toBeNull();
    expect(fetch).not.toHaveBeenCalled();
  });

  it.each([
    ["pointer leave", (row: HTMLElement) => row.dispatchEvent(new PointerEvent("pointerout", { pointerType: "mouse", bubbles: true, relatedTarget: document.body }))],
    ["row click", (row: HTMLElement) => row.click()],
    ["the more button", () => host.querySelector<HTMLButtonElement>('button[aria-haspopup="menu"]')!.click()],
    ["the context menu", (row: HTMLElement) => row.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true }))],
    ["the keyboard context menu", (row: HTMLElement) => row.dispatchEvent(new KeyboardEvent("keydown", { key: "F10", shiftKey: true, bubbles: true }))],
    ["Escape from outside the list", () => host.querySelector("[data-outside]")!.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }))],
    ["sidebar scroll", () => host.querySelector("[data-sidebar-rows]")!.dispatchEvent(new Event("scroll"))],
    ["programmatic navigation", () => navigate(location.pathname === "/" ? "/overview" : "/")],
  ] as const)("cancels a pending dwell and closes an open card on %s", async (_name, dismiss) => {
    const row = render();
    enter(row);
    act(() => { dismiss(row); });
    await dwell();
    expect(card()).toBeNull();
    enter(row);
    await dwell();
    expect(card()).not.toBeNull();
    act(() => { dismiss(row); });
    expect(card()).toBeNull();
  });

  it("reads the personal agent's latest text only on dwell and cancels a pending read on leave", async () => {
    let resolveRead!: (response: Response) => void;
    vi.mocked(fetch).mockImplementation(() => new Promise<Response>((resolve) => { resolveRead = resolve; }));
    const row = render(<AgentRow {...common} s={personal} pinned={false} onMenu={() => {}} />);
    enter(row);
    await dwell();
    const request = () => vi.mocked(fetch).mock.lastCall!;
    expect(String(request()[0])).toContain("/messages?limit=5");
    expect(metadata()[t("sidebar-row-hover.status")]).toBe(stateName("unread"));
    expect(preview()).toBeUndefined();
    const leave = () => act(() => row.dispatchEvent(new PointerEvent("pointerout", { pointerType: "mouse", bubbles: true, relatedTarget: document.body })));
    const signal = request()[1]?.signal;
    leave();
    expect(signal?.aborted).toBe(true);
    await act(async () => resolveRead(mockResponse({ messages: [] })));
    expect(card()).toBeNull();

    enter(row);
    await dwell();
    await act(async () => resolveRead(mockResponse({ messages: [
      { externalId: "n2", position: 2, blocks: [{ type: "text", text: "**Latest brief** is ready." }] },
      { externalId: "n1", position: 1, blocks: [{ type: "text", text: "Previous brief." }] },
      { externalId: "n3", position: 3, blocks: [{ type: "tool-call", name: "read" }] },
    ] })));
    expect(preview()).toBe("Latest brief is ready.");

    leave();
    const reads = vi.mocked(fetch).mock.calls.length;
    storePageDraft(personalAgentDraftKey(personal.id), "Add one more item.");
    enter(row);
    await dwell();
    expect(preview()).toContain("Add one more item.");
    expect(fetch).toHaveBeenCalledTimes(reads);
  });
});
