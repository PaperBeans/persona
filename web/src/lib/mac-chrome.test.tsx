// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { appleG2Contour, useMacChrome } from "./mac-chrome";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

describe("macOS unified titlebar bridge & Apple G2 contour", () => {
  let host: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });

  afterEach(() => {
    act(() => root.unmount());
    host.remove();
    vi.unstubAllGlobals();
  });

  it("defaults safely outside macOS desktop and subscribes when bridge is present", () => {
    function Probe() {
      const c = useMacChrome();
      return <span data-u={String(c.unifiedTitlebar)} data-fs={String(c.fullScreen)} data-f={String(c.focused)} />;
    }
    act(() => root.render(<Probe />));
    expect({ ...host.querySelector("span")!.dataset }).toEqual({ u: "false", fs: "false", f: "true" });

    let emit: ((next: { unifiedTitlebar: boolean; fullScreen: boolean; focused: boolean }) => void) | undefined;
    const off = vi.fn();
    vi.stubGlobal("window", Object.assign(window, {
      personaDesktop: {
        windowChrome: () => ({ unifiedTitlebar: true, fullScreen: false, focused: true }),
        onWindowChromeChange: (cb: typeof emit) => { emit = cb; return off; },
      },
    }));

    act(() => root.unmount());
    root = createRoot(host);
    act(() => root.render(<Probe />));
    expect({ ...host.querySelector("span")!.dataset }).toEqual({ u: "true", fs: "false", f: "true" });

    act(() => emit?.({ unifiedTitlebar: true, fullScreen: true, focused: false }));
    expect({ ...host.querySelector("span")!.dataset }).toEqual({ u: "true", fs: "true", f: "false" });
  });

  it("builds 44 px co-linear G2 curves for rail and hidden bays and a clean rect otherwise", () => {
    const plain = appleG2Contour(900, 600, null);
    expect(plain.clip.startsWith("M 14,0 ")).toBe(true);

    const rail = appleG2Contour(900, 600, "rail");
    expect(rail.clip.startsWith("M 110,0 ")).toBe(true);
    expect(rail.clip).toContain("14,44 H 78 C 89.5,44");
    expect(rail.stroke).toContain("14,44.5 H 78.5");

    const hidden = appleG2Contour(900, 600, "hidden");
    expect(hidden.clip.startsWith("M 160,0 ")).toBe(true);
    expect(hidden.clip).toContain("14,44 H 128");
  });
});
