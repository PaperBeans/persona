// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.ResizeObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
  g.matchMedia = (q: string) => ({
    matches: false,
    media: q,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
  });
});
import { t } from "../../i18n";
import { getState, reset, savePins } from "../../lib/store";
import type { Machine, Session } from "../../lib/types";
import { AgentsList } from "../Sidebar";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const machines: Machine[] = [
  { id: "gcp", name: "Cloud Dev", online: true },
  { id: "mac", name: "Mac Studio", online: true },
];

const sessions: Session[] = [
  {
    machine_id: "gcp",
    session_id: "s1",
    title: "Implement Agents Tab Filter Bar",
    cwd: "/workspace/persona",
    repo_root: "/workspace/persona",
    status: "working",
    state: "working",
    agent: "claude",
    model: "claude-opus-5-5",
    updated_at: "2026-11-09T11:30:00Z",
    last_text: "Updated composable filter bar and chat-list rows.",
  },
  {
    machine_id: "mac",
    session_id: "s2",
    title: "Fix WebSocket Reconnect",
    cwd: "/workspace/orca",
    repo_root: "/workspace/orca",
    status: "blocked",
    state: "needs_you",
    agent: "codex",
    model: "gpt-5",
    updated_at: "2026-11-09T11:00:00Z",
    asks: "Should we retry after 5 seconds?",
    turn_error: "Socket closed unexpectedly",
  },
];

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  localStorage.clear();
  reset();
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string) => {
      if (String(url).includes("/pins")) {
        return Promise.resolve(new Response(JSON.stringify({ spaces: [], sessions: ["mac/s2"] })));
      }
      return Promise.resolve(new Response("{}"));
    }),
  );
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  vi.unstubAllGlobals();
});

const menuRow = (label: string) =>
  [...document.querySelectorAll<HTMLElement>('[role="menuitem"], [role="menuitemradio"], [role="menuitemcheckbox"]')].find((el) =>
    el.textContent?.includes(label),
  );

async function openFilterMenu() {
  await act(async () => {
    document.querySelector<HTMLButtonElement>('[data-testid="agent-filter-button"]')!.click();
  });
}

async function chooseRow(label: string) {
  const row = menuRow(label);
  expect(row, `menu row ${label}`).toBeTruthy();
  await act(async () => {
    row!.click();
  });
}

function seed() {
  Object.assign(getState(), {
    loaded: true,
    machines,
    sessions: { gcp: [sessions[0]], mac: [sessions[1]] },
    pins: { spaces: [], sessions: ["mac/s2"] },
    todosLoaded: true,
  });
}

describe("Agents list search and Filter menu (#4397)", () => {
  it("is one row when idle: the search field and a Filter button, no pills, chips or plan bar", async () => {
    seed();
    await act(async () => {
      root.render(<AgentsList route={{ name: "agent-list" }} onNewAgent={() => {}} />);
    });
    const bar = host.querySelector<HTMLElement>('[data-testid="agent-dimension-filter-bar"]')!;
    expect(bar.children).toHaveLength(1);
    expect(bar.querySelectorAll("button")).toHaveLength(1);
    expect(host.querySelector('[data-testid="agent-filter-button"]')).not.toBeNull();
    expect(host.querySelector('[data-testid="scoped-filter-strip"]')).toBeNull();
    expect(host.querySelector('[data-testid="search-as-code-plan"]')).toBeNull();
    // the grouped list stays the default; chat-list rows only when picked
    expect(host.querySelectorAll('[data-testid="agent-chatlist-row"]')).toHaveLength(0);
  });

  it("filters from the menu, shows removable chips, and holds the chat-list view and Search as Code", async () => {
    seed();
    await act(async () => {
      root.render(<AgentsList route={{ name: "agent-list" }} onNewAgent={() => {}} />);
    });

    await openFilterMenu();
    await chooseRow(t("lib-agent-filter.chat_list_view"));
    const rows = [...host.querySelectorAll<HTMLElement>('[data-testid="agent-chatlist-row"]')];
    expect(rows.map((r) => r.getAttribute("data-session-id"))).toEqual(["s2", "s1"]);
    expect(rows[0].getAttribute("data-pinned")).toBe("true");
    expect(rows[0].textContent).toContain("Mac Studio");
    expect(rows[0].textContent).toContain("orca");

    // a facet page: pick one machine, the menu stays open and the chip appears
    await openFilterMenu();
    await chooseRow(t("lib-agent-filter.filter_machine"));
    await chooseRow("Cloud Dev");
    expect(menuRow("Mac Studio")).toBeTruthy();
    expect([...host.querySelectorAll<HTMLElement>('[data-testid="agent-chatlist-row"]')].map((r) => r.getAttribute("data-session-id"))).toEqual(["s1"]);
    const strip = host.querySelector<HTMLElement>('[data-testid="scoped-filter-strip"]')!;
    expect(strip.textContent).toContain(`${t("lib-agent-filter.filter_machine")}: Cloud Dev`);
    expect(host.querySelector('[data-testid="agent-filter-button"]')!.textContent).toBe("1");

    // the chip's × removes it, and the strip goes with the last one
    await act(async () => {
      strip.querySelector<HTMLButtonElement>("button[aria-label]")!.click();
    });
    expect(host.querySelector('[data-testid="scoped-filter-strip"]')).toBeNull();
    expect(host.querySelectorAll('[data-testid="agent-chatlist-row"]')).toHaveLength(2);

    // Search as Code: off the list until turned on in the menu, then its plan bar, which closes it again
    await act(async () => {
      document.body.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    });
    await openFilterMenu();
    await chooseRow(t("lib-agent-filter.search_as_code"));
    expect(host.querySelector('[data-testid="search-as-code-plan"]')).not.toBeNull();
    await act(async () => {
      host.querySelector<HTMLButtonElement>('[data-testid="search-as-code-off"]')!.click();
    });
    expect(host.querySelector('[data-testid="search-as-code-plan"]')).toBeNull();

    await act(async () => {
      savePins({ spaces: [], sessions: ["gcp/s1"] });
    });
    const updatedRows = [...host.querySelectorAll<HTMLElement>('[data-testid="agent-chatlist-row"]')];
    expect(updatedRows[0].getAttribute("data-session-id")).toBe("s1");
    expect(updatedRows[0].getAttribute("data-pinned")).toBe("true");
  });
});
