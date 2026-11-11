import { t } from "../i18n";
import { api, ApiError, errorText } from "./api";
import { runtimeIdentity } from "./runtime-list";
import { compareListIds, PAGE_SIZE, type ListPage } from "./session-list";
import { isWorkFacets, WORK_FILTER_KEY, workSessionMetadata, type WorkDimension, type WorkFacets, type WorkSession, type WorkSessionMetadata } from "./work-session-data";
import type { WorkSessionView } from "./work-session-preferences";

export type WorkSessionFilters = Partial<Record<WorkDimension, readonly string[]>>;
type WorkQuery = { scope: "work"; machine_id?: readonly string[]; project_id?: readonly string[]; harness?: readonly string[]; q: string; state: string; group_by?: string; sort?: string; direction?: string };
type WorkCounts = { state: Record<string, number>; facets: WorkFacets };
type WorkPage = ListPage<WorkSession> & { metadata: WorkSessionMetadata };
type Snapshot = {
  rows: WorkSession[]; loaded: boolean; loading: boolean; error: string | null;
  total: number; hasMore: boolean; dimensions?: WorkSessionMetadata;
};
type Action = "load" | "more";

export function workSessionQuery(filters: WorkSessionFilters, search: string, state: string, view: WorkSessionView): WorkQuery {
  return {
    scope: "work", machine_id: filters.device, project_id: filters.project, harness: filters.harness,
    q: search.trim(), state, group_by: view.groupBy.join(","), sort: view.sort, direction: view.direction,
  };
}

function pathFor(query: Partial<WorkQuery>, extra: Record<string, string>): string {
  const params = new URLSearchParams({ facets: "1", ...extra });
  for (const [key, value] of Object.entries(query)) {
    if (typeof value === "string") { if (value) params.set(key, value); }
    else if (value) for (const item of [...new Set(value.filter(Boolean))].sort(compareListIds)) params.append(key, item);
  }
  return `/sessions?${params}`;
}

/** Omit only the control being enumerated so users can change a filter even when the current selection has no matches. */
export function workSessionCountsPath(query: WorkQuery, except: WorkDimension | "state"): string {
  const scope: Partial<WorkQuery> = { ...query };
  delete scope[except === "state" ? "state" : WORK_FILTER_KEY[except]];
  delete scope.sort;
  delete scope.group_by;
  delete scope.direction;
  return pathFor(scope, { counts: "1" });
}

export async function readWorkSessionCounts(path: string, signal?: AbortSignal): Promise<WorkCounts> {
  const result = await api<WorkCounts>(path, { signal });
  if (!result?.state || !isWorkFacets(result.facets)) throw new Error(t("paged-agents.invalid_page"));
  return result;
}

const expired = (error: unknown) => error instanceof ApiError && error.status === 409 &&
  (error.code === "cursor_expired" || error.code === "revision_expired");
const empty = (): Snapshot => ({ rows: [], loaded: false, loading: false, error: null, total: 0, hasMore: false });

/** One immutable query over a frozen Hub-ordered window. A push can move whole groups, so refresh the loaded
 * prefix atomically rather than applying a delta to an incomplete client-side view. Kept out of startup lists. */
export class WorkSessionList {
  private snapshot = empty();
  private listeners = new Set<() => void>();
  private pending?: Promise<void>;
  private controller?: AbortController;
  private refreshQueued = false;
  private failed: Action = "load";
  private cursor?: string;
  private revision?: string;
  private depth = PAGE_SIZE;
  private groupDepth: number;

  constructor(private query: WorkQuery) {
    this.groupDepth = query.group_by ? query.group_by.split(",").length : 0;
  }

  getSnapshot = () => this.snapshot;
  subscribe = (fn: () => void) => { this.listeners.add(fn); return () => { this.listeners.delete(fn); }; };

  load = () => this.run("load");
  more = () => this.cursor ? this.run("more") : Promise.resolve();
  retry = () => this.failed === "more" ? this.more() : this.load();

  invalidate() {
    if (this.pending) this.refreshQueued = true;
    else void this.load().catch(() => {});
  }

  stop() {
    this.controller?.abort();
    this.controller = undefined;
    this.pending = undefined;
    this.refreshQueued = false;
  }

  private publish(patch: Partial<Snapshot>) {
    this.snapshot = { ...this.snapshot, ...patch };
    this.listeners.forEach((fn) => fn());
  }

  private run(action: Action): Promise<void> {
    if (this.pending) return this.pending;
    const controller = this.controller = new AbortController();
    const { signal } = controller;
    return this.pending = Promise.resolve().then(async () => {
      if (signal.aborted) return;
      this.publish({ loading: true, error: null });
      try {
        do {
          this.refreshQueued = false;
          this.failed = action;
          const depth = action === "more" ? this.depth + PAGE_SIZE : this.depth;
          try {
            if (action === "more" && this.cursor) await this.append(this.cursor, signal);
            else await this.readPrefix(depth, signal);
          } catch (error) {
            if (!expired(error) || signal.aborted) throw error;
            await this.readPrefix(depth, signal);
          }
          if (!signal.aborted) this.depth = depth;
          action = "load";
        } while (!signal.aborted && this.refreshQueued);
      } catch (error) {
        if (signal.aborted) return;
        this.publish({ error: errorText(error) });
        throw error;
      } finally {
        if (!signal.aborted) {
          this.pending = undefined;
          this.controller = undefined;
          this.publish({ loading: false });
        }
      }
    });
  }

  private async page(cursor: string | undefined, signal: AbortSignal): Promise<WorkPage> {
    const response = await api<Partial<ListPage<WorkSession>>>(pathFor(this.query, {
      summary: "1", limit: String(PAGE_SIZE), ...(cursor ? { cursor } : {}),
    }), { signal });
    const metadata = workSessionMetadata(response, this.groupDepth);
    if (!metadata || !Array.isArray(response.sessions) || !response.revision ||
        typeof response.total !== "number" || typeof response.has_more !== "boolean" ||
        (response.has_more && (!response.next_cursor || !response.sessions.length))) {
      throw new Error(t("paged-agents.invalid_page"));
    }
    return { ...response as ListPage<WorkSession>, metadata };
  }

  private async append(cursor: string, signal: AbortSignal) {
    const page = await this.page(cursor, signal);
    if (signal.aborted) return;
    if (page.revision !== this.revision || page.next_cursor === cursor) throw new Error(t("paged-agents.inconsistent_page"));
    const rows = [...this.snapshot.rows, ...page.sessions];
    if (new Set(rows.map(runtimeIdentity)).size !== rows.length) throw new Error(t("paged-agents.inconsistent_page"));
    this.cursor = page.has_more ? page.next_cursor : undefined;
    this.publish({ rows, hasMore: !!this.cursor });
  }

  private async readPrefix(depth: number, signal: AbortSignal) {
    const rows = new Map<string, WorkSession>();
    const cursors = new Set<string>();
    let first: WorkPage | undefined;
    let cursor: string | undefined;
    do {
      const page = await this.page(cursor, signal);
      if (signal.aborted) return;
      first ??= page;
      if (page.revision !== first.revision) throw new Error(t("paged-agents.inconsistent_page"));
      for (const row of page.sessions) {
        const id = runtimeIdentity(row);
        if (rows.has(id)) throw new Error(t("paged-agents.inconsistent_page"));
        rows.set(id, row);
      }
      cursor = page.has_more ? page.next_cursor : undefined;
      if (cursor && cursors.has(cursor)) throw new Error(t("paged-agents.repeated_cursor"));
      if (cursor) cursors.add(cursor);
    } while (cursor && rows.size < depth);
    this.cursor = cursor;
    this.revision = first.revision;
    this.publish({ rows: [...rows.values()], total: first.total, loaded: true, hasMore: !!cursor, dimensions: first.metadata });
  }
}
