import { api } from "./api";
import { directionTitle, leaderDirections, type DirectionTree } from "./direction-tree";
import { currentHeaders } from "./manager-choice";
import type { Todo } from "./todos";
import type { Session } from "./types";

/** Maximum number of to-do IDs accepted by POST /api/v1/todos/reorganize. */
export const MAX_REORGANIZE_IDS = 100;

/**
 * Tri-state target selection for `part_of` or `direction_id`:
 * - `keep`: omit from request payload (leave unchanged)
 * - `clear`: send `""` (detach to standalone / remove from direction)
 * - `set`: send the chosen target ID
 */
export type ReorganizeTarget =
  | { mode: "keep" }
  | { mode: "clear" }
  | { mode: "set"; id: string };

export interface ReorganizeDirectionOption {
  id: string;
  label: string;
  /** True only for active Persona-supervised directions that can accept supervision handover. */
  active: boolean;
}

export interface ReorganizeDraft {
  ids: readonly string[];
  partOf: ReorganizeTarget;
  direction: ReorganizeTarget;
  includeChildren?: boolean;
  transferSupervision?: boolean;
  /** Active direction IDs allowed to receive runtime supervision handover. */
  activeDirectionIds?: ReadonlySet<string>;
}

/** Wire request body for `POST /api/v1/todos/reorganize` (#14704). */
export interface TodoReorganizeRequest {
  ids: string[];
  part_of?: string;
  direction_id?: string;
  include_children: boolean;
  transfer_supervision: boolean;
}

/** Wire response body for `POST /api/v1/todos/reorganize` (#14704). */
export interface TodoReorganizeResponse {
  updated: Todo[];
  transferred_ids?: string[];
}

/**
 * Whether `transfer_supervision` may be enabled for the chosen direction target.
 * Only allowed when a non-empty target direction is selected AND it is an active direction.
 */
export function canTransferSupervision(
  direction: ReorganizeTarget,
  activeDirectionIds?: ReadonlySet<string>,
): boolean {
  if (direction.mode !== "set") return false;
  const id = direction.id.trim();
  if (!id) return false;
  return Boolean(activeDirectionIds?.has(id));
}

/**
 * Builds a validated `POST /api/v1/todos/reorganize` payload from the UI draft.
 * Returns `null` if no valid IDs are selected, too many IDs are selected, neither
 * `part_of` nor `direction_id` is being changed, or a `set` target has an empty ID.
 */
export function buildReorganizeRequest(draft: ReorganizeDraft): TodoReorganizeRequest | null {
  const ids = Array.from(new Set(draft.ids.map((id) => id.trim()).filter(Boolean)));
  if (ids.length === 0 || ids.length > MAX_REORGANIZE_IDS) return null;

  let partOf: string | undefined;
  if (draft.partOf.mode === "clear") {
    partOf = "";
  } else if (draft.partOf.mode === "set") {
    const id = draft.partOf.id.trim();
    if (!id) return null;
    partOf = id;
  }

  let directionId: string | undefined;
  if (draft.direction.mode === "clear") {
    directionId = "";
  } else if (draft.direction.mode === "set") {
    const id = draft.direction.id.trim();
    if (!id) return null;
    directionId = id;
  }

  if (partOf === undefined && directionId === undefined) return null;

  const includeChildren = Boolean(draft.includeChildren);
  const transferSupervision = Boolean(draft.transferSupervision) && canTransferSupervision(draft.direction, draft.activeDirectionIds);

  return {
    ids,
    ...(partOf !== undefined ? { part_of: partOf } : {}),
    ...(directionId !== undefined ? { direction_id: directionId } : {}),
    include_children: includeChildren,
    transfer_supervision: transferSupervision,
  };
}

/**
 * Returns to-dos that are safe `part_of` parent targets for `selectedIds`.
 * Filters out every selected ID and any descendant of a selected ID (cycle-safe)
 * to prevent self-loops and multi-hop cycles before sending to the server.
 */
export function eligibleParentTargets(
  todos: readonly Todo[],
  selectedIds: ReadonlySet<string> | readonly string[],
): Todo[] {
  const selected = selectedIds instanceof Set ? selectedIds : new Set(selectedIds);
  if (selected.size === 0) return [...todos];

  const byId = new Map(todos.map((td) => [td.id, td]));
  return todos.filter((candidate) => {
    const visited = new Set<string>();
    let cur: string | undefined = candidate.id;
    while (cur && !visited.has(cur)) {
      if (selected.has(cur)) return false;
      visited.add(cur);
      cur = byId.get(cur)?.part_of?.trim() || undefined;
    }
    return true;
  });
}

/**
 * Collects selectable direction options from the direction tree and assistant leaders list.
 */
export function reorganizeDirectionOptions(
  tree: DirectionTree | null | undefined,
  conversations?: readonly Session[],
  leaderIds?: readonly string[],
): ReorganizeDirectionOption[] {
  const out: ReorganizeDirectionOption[] = [];
  const seen = new Set<string>();

  for (const d of leaderDirections(tree)) {
    const id = d.id.trim();
    if (!id || seen.has(id)) continue;
    seen.add(id);
    const convTitle = conversations?.find((c) => c.session_id === id)?.title;
    out.push({
      id,
      label: directionTitle(d, convTitle),
      active: !d.external,
    });
  }

  for (const raw of leaderIds ?? []) {
    const id = raw.trim();
    if (!id || seen.has(id)) continue;
    seen.add(id);
    const convTitle = conversations?.find((c) => c.session_id === id)?.title?.trim();
    out.push({
      id,
      label: convTitle || id,
      active: true,
    });
  }

  return out;
}

/**
 * Calls `POST /api/v1/todos/reorganize` with multi-manager headers.
 */
export async function reorganizeTodos(req: TodoReorganizeRequest): Promise<TodoReorganizeResponse> {
  return api<TodoReorganizeResponse>("/todos/reorganize", {
    method: "POST",
    headers: currentHeaders(),
    json: req,
  });
}
