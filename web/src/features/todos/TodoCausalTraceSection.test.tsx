// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const apiMock = vi.fn(async (_path: string, _init?: unknown): Promise<unknown> => ({}));
vi.mock("../../lib/api", async (orig) => ({
  ...(await orig<typeof import("../../lib/api")>()),
  api: (path: string, init?: unknown) => apiMock(path, init),
}));

const { initI18n } = await import("../../i18n");
const { TodoCausalTraceSection } = await import("./TodoCausalTraceSection");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;

const flush = async () => {
  for (let i = 0; i < 4; i++) {
    await act(async () => {
      await Promise.resolve();
    });
  }
};

beforeEach(async () => {
  await initI18n("en");
  apiMock.mockReset();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  document.body.innerHTML = "";
});

describe("TodoCausalTraceSection (#16433)", () => {
  it("renders nothing and fires zero requests when todos_v2 is disabled", async () => {
    await act(async () => {
      root.render(<TodoCausalTraceSection todoId="td-1" enabled={false} />);
    });
    await flush();

    expect(host.querySelector('[data-testid="todo-causal-trace"]')).toBeNull();
    expect(apiMock).not.toHaveBeenCalled();
  });

  it("starts collapsed with zero fetches, fetches lazily on expand, and supports manual refresh", async () => {
    apiMock.mockResolvedValueOnce({
      todo_id: "td-1",
      title: "Ship feature",
      count: 2,
      events: [
        {
          id: "lifecycle:created",
          at: "2026-11-26T10:00:00Z",
          category: "lifecycle",
          kind: "created",
          summary: "To-do created",
        },
        {
          id: "pr:opened",
          at: "2026-11-26T11:30:00Z",
          category: "pr",
          kind: "opened",
          summary: "PR #42 opened",
          ref: "#42",
          url: "https://github.com/org/repo/pull/42",
        },
      ],
    });

    await act(async () => {
      root.render(<TodoCausalTraceSection todoId="td-1" enabled={true} />);
    });
    await flush();

    // Collapsed by default: zero network requests.
    const toggle = host.querySelector<HTMLButtonElement>('[data-testid="todo-causal-trace-toggle"]')!;
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(apiMock).not.toHaveBeenCalled();

    // Expand triggers lazy fetch of GET /todos/td-1/trace.
    await act(async () => toggle.click());
    await flush();

    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    expect(apiMock).toHaveBeenCalledTimes(1);
    expect(apiMock).toHaveBeenCalledWith("/todos/td-1/trace", expect.anything());

    const items = host.querySelectorAll('[data-testid="todo-causal-trace-event"]');
    expect(items.length).toBe(2);
    expect(items[0].textContent).toContain("To-do created");
    expect(items[1].textContent).toContain("PR #42 opened");
    const link = items[1].querySelector<HTMLAnchorElement>('[data-testid="todo-causal-trace-link"]');
    expect(link?.href).toBe("https://github.com/org/repo/pull/42");

    // Manual refresh button re-fetches.
    apiMock.mockResolvedValueOnce({
      todo_id: "td-1",
      title: "Ship feature",
      count: 0,
      events: [],
    });
    const refreshBtn = host.querySelector<HTMLButtonElement>('[data-testid="todo-causal-trace-refresh"]')!;
    await act(async () => refreshBtn.click());
    await flush();
    expect(apiMock).toHaveBeenCalledTimes(2);
    expect(host.querySelector('[data-testid="todo-causal-trace-empty"]')).not.toBeNull();
  });
});
