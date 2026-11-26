// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Todo } from "../lib/todos";
import { TodoParentPicker } from "./TodoParentPicker";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;

const makeTodo = (id: string, title: string, extra: Partial<Todo> = {}): Todo => ({
  id,
  title,
  created_at: "2026-11-24T10:00:00.000Z",
  updated_at: "2026-11-24T10:00:00.000Z",
  ...extra,
});

const flush = async () => {
  for (let i = 0; i < 4; i++) {
    await act(async () => {
      await Promise.resolve();
    });
  }
};

beforeEach(() => {
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
});

describe("TodoParentPicker (#16100)", () => {
  const todos: Todo[] = [
    makeTodo("goal_a", "Goal A"),
    makeTodo("goal_b", "Goal B"),
    makeTodo("child_of_a", "Child of A", { part_of: "goal_a" }),
    makeTodo("grandchild_of_a", "Grandchild of A", { part_of: "child_of_a" }),
    makeTodo("done_goal", "Done Goal", { done_at: "2026-11-24T11:00:00.000Z" }),
  ];

  it("lists only eligible top-level open parents, excluding self, descendants, nested children, and done items", async () => {
    const onApply = vi.fn(async () => {});
    const onClose = vi.fn();

    await act(async () => {
      root.render(
        <TodoParentPicker
          todoId="goal_a"
          todos={todos}
          onApply={onApply}
          onClose={onClose}
        />,
      );
    });
    await flush();

    const select = host.querySelector<HTMLSelectElement>("[data-testid=todo-detail-parent-select]")!;
    const values = [...select.options].map((o) => o.value);
    expect(values).toEqual(["", "goal_b"]);
    // Already top-level: promote button is omitted and Apply is disabled until changed
    expect(host.querySelector("[data-testid=todo-detail-parent-promote]")).toBeNull();
    expect(host.querySelector<HTMLButtonElement>("[data-testid=todo-detail-parent-apply]")?.disabled).toBe(true);
  });

  it("re-parents a todo via POST /api/v1/todos/reorganize payload with transfer_supervision=false", async () => {
    const onApply = vi.fn(async () => {});
    const onClose = vi.fn();

    await act(async () => {
      root.render(
        <TodoParentPicker
          todoId="goal_a"
          todos={todos}
          onApply={onApply}
          onClose={onClose}
        />,
      );
    });
    await flush();

    const select = host.querySelector<HTMLSelectElement>("[data-testid=todo-detail-parent-select]")!;
    await act(async () => {
      select.value = "goal_b";
      select.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await flush();

    const applyBtn = host.querySelector<HTMLButtonElement>("[data-testid=todo-detail-parent-apply]")!;
    expect(applyBtn.disabled).toBe(false);
    await act(async () => applyBtn.click());
    await flush();

    expect(onApply).toHaveBeenCalledWith({
      ids: ["goal_a"],
      part_of: "goal_b",
      include_children: false,
      transfer_supervision: false,
    });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("promotes a nested child to top-level in one click by sending empty part_of", async () => {
    const onApply = vi.fn(async () => {});
    const onClose = vi.fn();

    await act(async () => {
      root.render(
        <TodoParentPicker
          todoId="child_of_a"
          currentParentId="goal_a"
          currentParentTitle="Goal A"
          todos={todos}
          onApply={onApply}
          onClose={onClose}
        />,
      );
    });
    await flush();

    const promoteBtn = host.querySelector<HTMLButtonElement>("[data-testid=todo-detail-parent-promote]")!;
    expect(promoteBtn).toBeTruthy();
    await act(async () => promoteBtn.click());
    await flush();

    expect(onApply).toHaveBeenCalledWith({
      ids: ["child_of_a"],
      part_of: "",
      include_children: false,
      transfer_supervision: false,
    });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("shows an inline error and stays open when onApply rejects", async () => {
    const onApply = vi.fn(async () => {
      throw new Error("parent cycle rejected");
    });
    const onClose = vi.fn();

    await act(async () => {
      root.render(
        <TodoParentPicker
          todoId="child_of_a"
          currentParentId="goal_a"
          currentParentTitle="Goal A"
          todos={todos}
          onApply={onApply}
          onClose={onClose}
        />,
      );
    });
    await flush();

    await act(async () => host.querySelector<HTMLButtonElement>("[data-testid=todo-detail-parent-promote]")!.click());
    await flush();

    expect(host.querySelector("[data-testid=todo-detail-parent-error]")?.textContent).toContain("parent cycle rejected");
    expect(onClose).not.toHaveBeenCalled();
  });
});
