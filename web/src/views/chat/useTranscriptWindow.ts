import { useCallback, useLayoutEffect, useRef, useState } from "react";
import { mergeItems } from "../../lib/turns";
import type { InputWindow } from "./input-navigation";
import type { Transcript } from "./useTranscript";

interface WindowView extends InputWindow {
  scope: string;
  key: number;
  historyChanged: () => void;
  earlierPage?: symbol;
  loading?: "before" | "after";
}

const noFold = async () => false; // the separate window and its pages read unfolded items

/** A bounded reading view has its own cursors. The live subscription and latest-page cache keep running separately. */
export function useTranscriptWindow(mid: string, sid: string, live: Transcript, failed: () => void) {
  const scope = JSON.stringify([mid, sid, live.history, live.replaceEpoch]);
  const [state, setState] = useState<WindowView | null>(null);
  const view = state?.scope === scope ? state : null;
  const current = useRef<WindowView | null>(null);
  const serial = useRef(0);
  const request = useRef<AbortController | null>(null);

  const close = useCallback(() => {
    request.current?.abort();
    request.current = null;
    if (!current.current) return false;
    current.current = null;
    setState(null);
    return true;
  }, []);
  useLayoutEffect(() => {
    setState((previous) => previous?.scope === scope ? previous : null);
    return () => {
      request.current?.abort();
      request.current = null;
      current.current = null;
    };
  }, [scope]);

  const open = useCallback((page: InputWindow, historyChanged: () => void) => {
    request.current?.abort();
    request.current = null;
    current.current = { ...page, scope, key: ++serial.current, historyChanged };
    setState(current.current);
  }, [scope]);

  const load = useCallback(async (direction: "before" | "after") => {
    const previous = current.current;
    const cursor = previous?.[direction];
    if (!previous || previous.scope !== scope || cursor === undefined || request.current) return;
    const ctl = new AbortController();
    request.current = ctl;
    const store = (next: WindowView) => {
      current.current = next;
      setState(next);
    };
    store({ ...previous, loading: direction });
    try {
      const { readInputWindowPage } = await import("./input-navigation");
      const result = await readInputWindowPage(mid, sid, previous.history, direction, cursor, ctl.signal);
      if (ctl.signal.aborted) return;
      if (result === "stale") {
        store(previous);
        previous.historyChanged();
        return;
      }
      const earlierPage = direction === "before" && result.items.length ? Symbol() : previous.earlierPage;
      store({ ...previous, [direction]: result.cursor, items: mergeItems(previous.items, result.items), earlierPage });
      return direction === "before" && result.items.length ? earlierPage : undefined;
    } catch {
      if (!ctl.signal.aborted) { store(previous); failed(); }
    } finally {
      if (request.current === ctl) request.current = null;
    }
  }, [mid, sid, scope, failed]);
  const loadEarlier = useCallback(() => load("before"), [load]);
  const loadLater = useCallback(() => void load("after"), [load]);

  const transcript: Transcript = view ? {
    ...live, items: view.items, streaming: "", streamEnded: false, streamEndedThrough: undefined,
    hasMore: view.before !== undefined, loadingEarlier: view.loading === "before", earlierPage: view.earlierPage,
    loadEarlier, expandFold: noFold,
  } : live;
  return { transcript, key: view?.key, open, close, loadLater, hasLater: view?.after !== undefined, loadingLater: view?.loading === "after" };
}
