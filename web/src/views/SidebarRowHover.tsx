import { useEffect, useSyncExternalStore } from "react";
import type { AgentSession } from "../lib/agents";
import { subscribeRoute } from "../lib/router";
import type { draftLine } from "../lib/row-line";
import type { Session } from "../lib/types";
import { isContextKey } from "../ui/menu-model";
import { lazyOverlay } from "./LazyOverlay";

export type RowHoverDetail = { title: string } & (
  | { session: Session; titleDetail?: string; draft?: ReturnType<typeof draftLine>; working?: boolean; tab?: string }
  | { agent: AgentSession }
);
export type RowHover = { row: HTMLElement; sidebar: HTMLElement; detail: RowHoverDetail };

const Card = lazyOverlay(() => import("./SidebarRowHoverCard").then((m) => m.default));
const listeners = new Set<() => void>();
let open: RowHover | null = null;
let anchor: HTMLElement | null = null;
let stop: (() => void) | undefined;

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}
const snapshot = () => open;

function dismiss() {
  stop?.();
  stop = undefined;
  anchor = null;
  if (!open) return;
  open = null;
  listeners.forEach((listener) => listener());
}

export function sidebarRowHover(read: () => RowHoverDetail) {
  return {
    onPointerEnter(e: React.PointerEvent<HTMLElement>) {
      const row = e.currentTarget;
      const sidebar = row.closest<HTMLElement>("[data-sidebar-root]");
      if (!sidebar || e.pointerType === "touch" || e.buttons) return;
      const media = window.matchMedia?.("(hover: hover) and (pointer: fine)");
      if (!media?.matches) return;
      dismiss();
      anchor = row;

      const controller = new AbortController();
      const { signal } = controller;
      const capture = { capture: true, signal };
      for (const event of ["pointerdown", "click", "contextmenu"]) window.addEventListener(event, dismiss, capture);
      sidebar.addEventListener("scroll", dismiss, capture);
      window.addEventListener("resize", dismiss, { signal });
      window.addEventListener("keydown", (event) => {
        if (event.key === "Escape" || isContextKey(event)) dismiss();
      }, capture);
      media.addEventListener("change", dismiss, { signal });
      const unroute = subscribeRoute(dismiss);
      const timer = window.setTimeout(() => {
        if (!row.isConnected || !media.matches) return dismiss();
        open = { row, sidebar, detail: read() };
        listeners.forEach((listener) => listener());
      }, 250);
      stop = () => {
        window.clearTimeout(timer);
        controller.abort();
        unroute();
      };
    },
    onPointerLeave(e: React.PointerEvent<HTMLElement>) {
      if (anchor === e.currentTarget) dismiss();
    },
  };
}

/** The only hover subscriber. The lazy import runs after a dwell leaves an open card. */
export function SidebarRowHoverHost() {
  const hover = useSyncExternalStore(subscribe, snapshot, () => null);
  useEffect(() => dismiss, []);
  return hover && <Card hover={hover} onClose={dismiss} />;
}
