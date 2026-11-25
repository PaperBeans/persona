// The way into the disk usage map from elsewhere (#14576, docs/server/machine-usage-map.md "Low-disk guidance"): a
// computer's disk figure opens the Machines page's Storage view on that computer with the map in reclaim mode. The
// request rides in the history entry's state (the page's address stays /machines), and the page takes it once, so a
// reload or a later visit does not open the map again. Kept apart from lib/usage-map.ts so the pages that only link
// here do not load the map's code.
import { navigate } from "./router";

export interface UsageFocusRequest {
  mid: string;
  mode: "reclaim";
}

const KEY = "usage_focus";

/** Opens the Machines page's Storage view on `mid` with the map in reclaim mode. */
export function openUsageMap(mid: string): void {
  navigate({ name: "machines" }, false, { [KEY]: { mid, mode: "reclaim" } });
}

/** The request the current history entry carries, taken off it (so it is acted on once); null without one. */
export function takeUsageFocus(): UsageFocusRequest | null {
  const state = typeof history !== "undefined" ? (history.state as Record<string, unknown> | null) : null;
  const req = state?.[KEY] as UsageFocusRequest | undefined;
  if (!req || typeof req.mid !== "string" || req.mode !== "reclaim") return null;
  const rest = { ...state };
  delete rest[KEY];
  history.replaceState(rest, "", location.href);
  return { mid: req.mid, mode: "reclaim" };
}
