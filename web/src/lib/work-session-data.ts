import type { Session } from "./types";

export const WORK_SESSION_DIMENSIONS_FEATURE = "sessions_dimensions";
export const WORK_DIMENSIONS = ["device", "project", "harness"] as const;
export type WorkDimension = typeof WORK_DIMENSIONS[number];
export const WORK_SORTS = ["recent", "created", "title"] as const;
export type WorkSessionSort = typeof WORK_SORTS[number];
export type WorkSessionDirection = "asc" | "desc";

// Optional on the wire so a browser can also show work sessions from older hubs.
export type WorkSession = Session & {
  project_id?: string;
  project_name?: string;
  harness_id?: string;
  harness_name?: string;
};
export type WorkFacet = { id: string; name: string; count: number };
export type WorkFacets = Record<WorkDimension, WorkFacet[]>;
export type WorkGroup = { path: string[]; count: number };
export type WorkSessionMetadata = { facets: WorkFacets; groups: WorkGroup[] };

export const WORK_FILTER_KEY = {
  device: "machine_id",
  project: "project_id",
  harness: "harness",
} as const satisfies Record<WorkDimension, string>;

export function workDimensionId(session: WorkSession, dimension: WorkDimension): string | undefined {
  switch (dimension) {
    case "device": return session.machine_id;
    case "project": return session.project_id;
    case "harness": return session.harness_id;
  }
}

const record = (v: unknown): v is Record<string, unknown> => typeof v === "object" && v !== null;
const count = (v: unknown): v is number => typeof v === "number" && Number.isSafeInteger(v) && v >= 0;

export function isWorkFacets(v: unknown): v is WorkFacets {
  return record(v) && WORK_DIMENSIONS.every((dimension) => {
    const items = v[dimension];
    return Array.isArray(items) && items.every((item: unknown) => record(item) &&
      typeof item.id === "string" && item.id !== "" && typeof item.name === "string" && count(item.count));
  });
}

/** A requested dimension response must never silently fall back to legacy, path-based grouping. */
export function workSessionMetadata(v: unknown, depth: number): WorkSessionMetadata | null {
  if (!record(v) || !isWorkFacets(v.facets)) return null;
  if (!depth) return { facets: v.facets, groups: [] };
  if (!Array.isArray(v.groups) || !v.groups.every((g: unknown): g is WorkGroup => record(g) &&
    Array.isArray(g.path) && g.path.length > 0 && g.path.length <= depth &&
    g.path.every((id: unknown) => typeof id === "string" && id !== "") && count(g.count))) return null;
  return { facets: v.facets, groups: v.groups };
}

export type WorkSessionItem =
  | { kind: "group"; key: string; depth: number; dimension: WorkDimension; id: string; count?: number }
  | { kind: "session"; key: string; session: WorkSession };

/** Insert headers at the boundaries in the Hub's ordered stream. The loaded prefix does not determine
 *  group order, identity, or counts. A group crossing a page boundary gets just one header. */
export function workSessionItems(rows: readonly WorkSession[], groupBy: readonly WorkDimension[],
    groups: readonly WorkGroup[] = []): WorkSessionItem[] {
  const keyOf = (path: readonly string[]) => JSON.stringify(path);
  const counts = new Map(groups.map((g) => [keyOf(g.path), g.count]));
  const items: WorkSessionItem[] = [];
  let previous: string[] = [];
  for (const session of rows) {
    const path = groupBy.map((dimension) => workDimensionId(session, dimension) ?? "");
    let shared = 0;
    while (shared < path.length && path[shared] === previous[shared]) shared++;
    for (let depth = shared; depth < path.length; depth++) {
      const prefix = keyOf(path.slice(0, depth + 1));
      items.push({ kind: "group", key: `group:${prefix}`, depth, dimension: groupBy[depth], id: path[depth], count: counts.get(prefix) });
    }
    items.push({ kind: "session", key: `session:${keyOf([session.machine_id, session.session_id])}`, session });
    previous = path;
  }
  return items;
}
