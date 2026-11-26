import { useLayoutEffect, useMemo, useReducer } from "react";
import type { NavRailTick } from "../../lib/nav-rail";
import type { UserInputIndex, UserInputPosition } from "../../lib/user-input-index";
import { readInputPreviews } from "./input-navigation";

const NEIGHBORS = 4;

/** A rail owns one cache per history. Only missing positions near an explicitly previewed tick are requested. */
export function useInputPreviews(mid: string, sid: string, scope: string, index: UserInputIndex | null,
  ticks: NavRailTick[], historyChanged: () => void) {
  const history = index?.history;
  const cache = useMemo(() => ({
    resolved: new Map<string, UserInputPosition | undefined>(),
    pending: new Set<string>(),
    failed: new Set<string>(),
    requests: new Set<AbortController>(),
    unavailable: false,
  }), [scope, history]);
  const [, changed] = useReducer((n: number) => n + 1, 0);
  useLayoutEffect(() => () => {
    for (const request of cache.requests) request.abort();
    cache.requests.clear();
    cache.pending.clear();
  }, [cache]);

  const loadPreview = async (at: number) => {
    const target = index?.inputs[at];
    if (!index || !target || !ticks[at]?.loadPreview || cache.unavailable || cache.resolved.has(target.id) || cache.pending.has(target.id)) return;
    const start = Math.max(0, at - NEIGHBORS);
    const positions = index.inputs.slice(start, at + NEIGHBORS + 1).filter(({ id }, i) =>
      ticks[start + i].loadPreview && !cache.resolved.has(id) && !cache.pending.has(id));
    const request = new AbortController();
    cache.requests.add(request);
    for (const { id } of positions) {
      cache.pending.add(id);
      cache.failed.delete(id);
    }
    changed();
    try {
      const results = await readInputPreviews(mid, sid, positions, index.history, request.signal);
      if (request.signal.aborted || cache.unavailable) return;
      if (results === "unsupported" || results === "stale") {
        cache.unavailable = true;
        if (results === "stale") historyChanged();
      } else {
        // Omitted positions also settle, so moving over them again costs no read.
        for (const { id } of positions) cache.resolved.set(id, undefined);
        for (const input of results) cache.resolved.set(input.id, input);
      }
    } catch {
      if (!request.signal.aborted) for (const { id } of positions) cache.failed.add(id);
    } finally {
      if (!request.signal.aborted) {
        cache.requests.delete(request);
        for (const { id } of positions) cache.pending.delete(id);
        changed();
      }
    }
  };
  return {
    loadPreview,
    previewAt: (at: number) => {
      const id = index?.inputs[at]?.id;
      if (!id || !ticks[at].loadPreview) return;
      return {
        excerpt: cache.resolved.get(id),
        loading: !cache.unavailable && !cache.resolved.has(id) && !cache.failed.has(id),
      };
    },
  };
}
