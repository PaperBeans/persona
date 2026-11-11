import { WORK_DIMENSIONS, WORK_SORTS, type WorkDimension, type WorkSessionDirection, type WorkSessionSort } from "./work-session-data";

export type WorkSessionView = { groupBy: WorkDimension[]; sort: WorkSessionSort; direction: WorkSessionDirection };

const KEY = "persona.workSessionView.v1";
export const defaultWorkSessionView = (): WorkSessionView => ({ groupBy: [], sort: "recent", direction: "desc" });

function normalize(value: unknown): WorkSessionView {
  if (!value || typeof value !== "object") return defaultWorkSessionView();
  const { groupBy, sort, direction } = value as Partial<WorkSessionView>;
  const dimensions = Array.isArray(groupBy)
    ? groupBy.filter((dimension): dimension is WorkDimension => WORK_DIMENSIONS.includes(dimension))
    : [];
  return {
    groupBy: [...new Set(dimensions)],
    sort: sort && WORK_SORTS.includes(sort) ? sort : "recent",
    direction: direction === "asc" ? "asc" : "desc",
  };
}

export function readWorkSessionView(store?: Pick<Storage, "getItem">): WorkSessionView {
  try { return normalize(JSON.parse((store ?? localStorage).getItem(KEY) ?? "null")); }
  catch { return defaultWorkSessionView(); }
}

export function writeWorkSessionView(view: WorkSessionView, store?: Pick<Storage, "setItem">): void {
  try { (store ?? localStorage).setItem(KEY, JSON.stringify(normalize(view))); }
  catch { /* The current view still works when browser storage is disabled. */ }
}
