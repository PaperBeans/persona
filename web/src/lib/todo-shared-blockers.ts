// Shared external blockers projection helpers for the Web To-dos view under todos_v2 (#14728).
// Mirrors the wire format emitted by server/internal/assistant/todo/projection/shared_blockers.go.

import type { Todo } from "./todos";

export type SharedBlockerKind = "pull_request" | "issue" | "external_target";

export interface SharedBlocker {
  key: string;
  kind: SharedBlockerKind;
  label: string;
  url?: string;
  todo_ids: string[];
  count: number;
}

const VALID_KINDS: ReadonlySet<string> = new Set<SharedBlockerKind>([
  "pull_request",
  "issue",
  "external_target",
]);

/** Returns a trimmed http(s) URL string, or undefined for missing / unsafe URLs. */
export function safeBlockerUrl(raw: unknown): string | undefined {
  if (typeof raw !== "string") return undefined;
  const trimmed = raw.trim();
  if (!trimmed || /\s/.test(trimmed)) return undefined;
  try {
    const parsed = new URL(trimmed);
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") return undefined;
    return trimmed;
  } catch {
    return undefined;
  }
}

/**
 * Tolerantly parses the optional `shared_blockers` field from `GET /api/v1/todos`.
 * - Safely degrades to `[]` when omitted (older hubs) or malformed.
 * - Retains only valid entries with `count >= 2` and `todo_ids.length >= 2`.
 * - When `visibleTodoIds` is supplied, scopes `todo_ids` to the currently visible
 *   assistant's to-dos and re-checks the `>= 2` threshold so cross-manager or deleted
 *   items never leave dead filter chips.
 */
export function parseSharedBlockers(
  raw: unknown,
  visibleTodoIds?: ReadonlySet<string>,
): SharedBlocker[] {
  if (!Array.isArray(raw) || raw.length === 0) return [];

  const seenKeys = new Set<string>();
  const out: SharedBlocker[] = [];

  for (const item of raw) {
    if (!item || typeof item !== "object") continue;
    const rec = item as Record<string, unknown>;

    const key = typeof rec.key === "string" ? rec.key.trim() : "";
    const label = typeof rec.label === "string" ? rec.label.trim() : "";
    if (!key || !label || seenKeys.has(key)) continue;

    const rawCount = typeof rec.count === "number" && Number.isFinite(rec.count) ? Math.trunc(rec.count) : 0;
    if (rawCount < 2 || !Array.isArray(rec.todo_ids)) continue;

    const idSet = new Set<string>();
    for (const id of rec.todo_ids) {
      if (typeof id !== "string") continue;
      const trimmed = id.trim();
      if (!trimmed) continue;
      if (visibleTodoIds && !visibleTodoIds.has(trimmed)) continue;
      idSet.add(trimmed);
    }
    if (idSet.size < 2) continue;

    const kind: SharedBlockerKind =
      typeof rec.kind === "string" && VALID_KINDS.has(rec.kind)
        ? (rec.kind as SharedBlockerKind)
        : "external_target";
    const url = safeBlockerUrl(rec.url);
    const todoIds = [...idSet];

    seenKeys.add(key);
    out.push({
      key,
      kind,
      label,
      ...(url ? { url } : {}),
      todo_ids: todoIds,
      count: visibleTodoIds ? todoIds.length : Math.max(rawCount, todoIds.length),
    });
  }

  out.sort((a, b) => b.count - a.count || a.key.localeCompare(b.key));
  return out;
}

/** Returns `selectedKey` only if it still exists in `blockers`; otherwise `null` to prevent empty deadlocks. */
export function resolveSelectedBlockerKey(
  selectedKey: string | null,
  blockers: readonly SharedBlocker[],
): string | null {
  if (!selectedKey) return null;
  return blockers.some((b) => b.key === selectedKey) ? selectedKey : null;
}

/** Finds the active shared blocker for `selectedKey`, or `undefined` if none is selected or it disappeared. */
export function findSelectedBlocker(
  selectedKey: string | null,
  blockers: readonly SharedBlocker[],
): SharedBlocker | undefined {
  if (!selectedKey) return undefined;
  return blockers.find((b) => b.key === selectedKey);
}

/** Predicate matching a to-do against an active shared blocker (always true when no blocker is selected). */
export function matchesSharedBlocker(
  todo: Pick<Todo, "id">,
  blocker: SharedBlocker | undefined,
): boolean {
  if (!blocker) return true;
  return blocker.todo_ids.includes(todo.id);
}

/** Pure filter returning only the to-dos blocked by the selected shared blocker (or all to-dos if none/stale). */
export function filterTodosBySharedBlocker<T extends Pick<Todo, "id">>(
  todos: readonly T[],
  blockers: readonly SharedBlocker[],
  selectedKey: string | null,
): T[] {
  const active = findSelectedBlocker(selectedKey, blockers);
  if (!active) return [...todos];
  const ids = new Set(active.todo_ids);
  return todos.filter((todo) => ids.has(todo.id));
}
