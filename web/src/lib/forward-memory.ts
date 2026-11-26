// The targets forwards went to lately, latest first (Forward to's Recent, #3899; iOS ForwardMemory): kept in this browser
// for this hub (localStorage is per origin, as Hand to AI's memory, lib/handoff-memory.ts): ids only, at most
// FORWARD_MEMORY_LIMIT, never synced, forgotten on sign-out (lib/disconnect.ts).

export const FORWARD_MEMORY_KEY = "forward.recentTargets";
export const FORWARD_MEMORY_LIMIT = 8;

/** "persona" (the main chat) or "session:<machine>/<session>"; New agent is not a place to go back to. */
export type ForwardTargetId = string;

export const PERSONA_TARGET = "persona";

export function sessionTarget(mid: string, sid: string): ForwardTargetId {
  return `session:${mid}/${sid}`;
}

function defaultStore(): Storage | null {
  try {
    return (typeof localStorage === "undefined" ? null : localStorage) ?? null;
  } catch {
    return null;
  }
}

/** The remembered targets, latest first; what does not read is left out. */
export function forwardTargets(store?: Storage): ForwardTargetId[] {
  const s = store ?? defaultStore();
  if (!s) return [];
  try {
    const list = JSON.parse(s.getItem(FORWARD_MEMORY_KEY) ?? "[]");
    return Array.isArray(list) ? list.filter((x): x is string => typeof x === "string" && (x === PERSONA_TARGET || /^session:[^/]+\/.+/.test(x))) : [];
  } catch {
    return [];
  }
}

/** The remembered sessions (`<machine>/<session>`), latest first. */
export function forwardSessions(store?: Storage): string[] {
  return forwardTargets(store).flatMap((x) => (x.startsWith("session:") ? [x.slice("session:".length)] : []));
}

export function rememberForward(target: ForwardTargetId, store?: Storage) {
  const s = store ?? defaultStore();
  if (!s) return;
  const list = [target, ...forwardTargets(s).filter((x) => x !== target)].slice(0, FORWARD_MEMORY_LIMIT);
  try {
    s.setItem(FORWARD_MEMORY_KEY, JSON.stringify(list));
  } catch {
    // a full or blocked storage only loses the memory
  }
}

export function forgetForwards(store?: Storage) {
  try {
    (store ?? defaultStore())?.removeItem(FORWARD_MEMORY_KEY);
  } catch {
    // blocked storage must not interrupt sign-out
  }
}
