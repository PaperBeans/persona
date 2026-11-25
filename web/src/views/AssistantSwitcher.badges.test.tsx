// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});

let badges: Record<string, number> = {};
vi.mock("../lib/manager-attention", () => ({ useManagerBadges: () => badges }));
import { ManagerBadgesCollector } from "./AssistantSwitcher";

describe("ManagerBadgesCollector", () => {
  let host: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
    badges = {};
  });
  afterEach(() => {
    act(() => root.unmount());
    host.remove();
  });

  it("publishes a real change, not another reference to the same counts", async () => {
    const onChange = vi.fn();
    badges = { other: 2 };
    await act(async () => root.render(<ManagerBadgesCollector onChange={onChange} />));
    expect(onChange).toHaveBeenCalledTimes(1);
    badges = { other: 2 };
    await act(async () => root.render(<ManagerBadgesCollector onChange={onChange} />));
    expect(onChange).toHaveBeenCalledTimes(1);
    badges = { other: 3 };
    await act(async () => root.render(<ManagerBadgesCollector onChange={onChange} />));
    expect(onChange).toHaveBeenCalledTimes(2);
    expect(onChange).toHaveBeenLastCalledWith({ other: 3 });
  });
});
