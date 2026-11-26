import { useSyncExternalStore } from "react";
import type { DesktopWindowChrome } from "./desktop-types";

export const DEFAULT_MAC_CHROME: DesktopWindowChrome = { unifiedTitlebar: false, fullScreen: false, focused: true };

const readChrome = (): DesktopWindowChrome =>
  (typeof window !== "undefined" ? window.personaDesktop?.windowChrome?.() : undefined) ?? DEFAULT_MAC_CHROME;

const sameChrome = (a: DesktopWindowChrome, b: DesktopWindowChrome): boolean =>
  a.unifiedTitlebar === b.unifiedTitlebar && a.fullScreen === b.fullScreen && a.focused === b.focused;

let bridgeRef: unknown = typeof window !== "undefined" ? window.personaDesktop : undefined;
let currentChrome: DesktopWindowChrome = readChrome();
const listeners = new Set<() => void>();
let detachBridge: (() => void) | undefined;

function syncBridge(): void {
  const bridge = typeof window !== "undefined" ? window.personaDesktop : undefined;
  if (bridge === bridgeRef) return;
  bridgeRef = bridge;
  const next = readChrome();
  if (!sameChrome(currentChrome, next)) currentChrome = next;
}

function getSnapshot(): DesktopWindowChrome {
  syncBridge();
  return currentChrome;
}

function subscribe(listener: () => void): () => void {
  syncBridge();
  listeners.add(listener);
  if (!detachBridge && typeof window !== "undefined") {
    detachBridge = window.personaDesktop?.onWindowChromeChange?.((next: DesktopWindowChrome) => {
      if (sameChrome(currentChrome, next)) return;
      currentChrome = next;
      listeners.forEach((f) => f());
    });
  }
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0 && detachBridge) {
      detachBridge();
      detachBridge = undefined;
    }
  };
}

/** Subscribes to macOS unified-titlebar chrome state; returns browser-safe defaults outside macOS desktop. */
export const useMacChrome = (): DesktopWindowChrome => useSyncExternalStore(subscribe, getSnapshot, () => DEFAULT_MAC_CHROME);

/** Computes the 44 px co-linear Apple G2 continuous-curvature SVG cutout and contour paths for `<main>`. */
export function appleG2Contour(W: number, H: number, bay: "rail" | "hidden" | null): { clip: string; stroke: string; rim: string } {
  const clipTail = `H ${W - 14} A 14,14 0 0 1 ${W},14 V ${H - 14} A 14,14 0 0 1 ${W - 14},${H} H 14 A 14,14 0 0 1 0,${H - 14}`;
  const strokeTail = `H ${W - 14} A 13.5,13.5 0 0 1 ${W - 0.5},14 V ${H - 14} A 13.5,13.5 0 0 1 ${W - 14},${H - 0.5} H 14 A 13.5,13.5 0 0 1 0.5,${H - 14}`;
  if (!bay) {
    return {
      clip: `M 14,0 ${clipTail} V 14 A 14,14 0 0 1 14,0 Z`,
      stroke: `M 14,0.5 ${strokeTail} V 14 A 13.5,13.5 0 0 1 14,0.5 Z`,
      rim: `M 14,1.25 H ${W - 14}`,
    };
  }
  const by = 44;
  const bx = bay === "rail" ? 94 : 144;
  const sx = bx + 0.5;
  const sy = by + 0.5;
  return {
    clip: `M ${bx + 22},0 ${clipTail} V ${by + 14} C 0,${by + 5} 5,${by} 14,${by} H ${bx - 10} C ${bx + 1.5},${by} ${bx + 3.5},${by - 6} ${bx + 6},${by / 2} C ${bx + 8.5},6 ${bx + 10.5},0 ${bx + 22},0 Z`,
    stroke: `M ${sx + 22},0.5 ${strokeTail} V ${sy + 13.5} C 0.5,${sy + 5} 5,${sy} 14,${sy} H ${sx - 10} C ${sx + 1.5},${sy} ${sx + 3.5},${sy - 6} ${sx + 6},${by / 2 + 0.25} C ${sx + 8.5},6.25 ${sx + 10.5},0.5 ${sx + 22},0.5 Z`,
    rim: `M ${bx + 22},1.25 H ${W - 14}`,
  };
}
