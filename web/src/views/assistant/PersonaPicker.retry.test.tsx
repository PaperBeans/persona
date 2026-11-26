// @vitest-environment jsdom
// Retry the look store from the picker, retaining the default choice while the hub is unavailable (#4493).
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

type Pending = { resolve: (value: unknown) => void; reject: (error: unknown) => void };
const pending = vi.hoisted(() => [] as Pending[]);
vi.mock("../../lib/api", async (original) => ({
  ...(await original<typeof import("../../lib/api")>()),
  api: () => new Promise((resolve, reject) => pending.push({ resolve, reject })),
}));

import { PersonaPicker } from "./PersonaPicker";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let root: Root;
let host: HTMLDivElement;
beforeEach(() => {
  localStorage.clear();
  pending.length = 0;
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

const retry = () => [...host.querySelectorAll("button")].find((button) => button.textContent === "重试");

it("retries a failed look list, prevents duplicate retries, and shows the recovered looks", async () => {
  await act(async () => root.render(<PersonaPicker />));
  expect(pending).toHaveLength(1);
  expect(host.querySelectorAll('[role="radio"]')).toHaveLength(1);

  await act(async () => pending[0].reject(new Error("hub unavailable")));
  expect(host.querySelector('[role="alert"]')?.textContent).toContain("hub unavailable");
  expect(retry()!.disabled).toBe(false);

  await act(async () => retry()!.click());
  expect(pending).toHaveLength(2);
  expect(retry()!.disabled).toBe(true);
  expect(host.querySelectorAll('[role="radio"]')).toHaveLength(1);
  await act(async () => retry()!.click());
  expect(pending).toHaveLength(2);

  await act(async () => pending[1].reject(new Error("still unavailable")));
  expect(host.querySelector('[role="alert"]')?.textContent).toContain("still unavailable");
  expect(host.textContent).not.toContain("hub unavailable");
  expect(retry()!.disabled).toBe(false);

  await act(async () => retry()!.click());
  expect(pending).toHaveLength(3);
  await act(async () => pending[2].resolve({
    current: { id: "persona", effective: "persona" },
    looks: [{
      id: "example-look", source: "extension", name: "Example look", app_icon: "persona", rev: "1",
      manifest: {
        format: 1, id: "example-look", name: "Example look",
        avatar: { sizes: { "128": "avatar.png" } },
        dot: { states: { idle: { file: "idle.png" } } },
      },
    }],
  }));
  expect(host.querySelector('[role="alert"]')).toBeNull();
  expect(retry()).toBeUndefined();
  expect(host.querySelectorAll('[role="radio"]')).toHaveLength(2);
  expect(host.querySelector('[role="radio"][aria-label="Example look"]')).not.toBeNull();
  expect(host.querySelector('[role="radio"][aria-checked="true"]')?.getAttribute("aria-label")).toContain("团子");
});
