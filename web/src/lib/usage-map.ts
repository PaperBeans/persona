// The disk usage map (#14576, docs/server/machine-usage-map.md): one computer's disk as a drill-down treemap, what is
// already offloaded drawn beside it on one scale, and the guarded actions on what is picked (Move to Trash, Offload,
// Restore; Clean cache reuses the existing cleanup in lib/machine-diagnosis.ts). This module holds the wire types of
// the manager's routes, the calls, and every rule that needs no DOM (the squarified layout, the shared scale, the age
// steps, the suggestions, the goal picker, the "what if" numbers, the links between the two sides and the tree patches),
// so each is unit-tested on its own (usage-map.test.ts).
import { t } from "../i18n";
import { api, ApiError, enc, errorText, pastHub } from "./api";

// ── wire types (snake_case JSON, the design doc is the contract) ──

export type UsageKind = "root" | "group" | "dir" | "file" | "app" | "mount" | "other" | "free" | "unmeasured";
export type UsageCategory = "apps" | "personal" | "appdata" | "cache" | "system" | "free" | "other";
export const CATEGORIES: readonly UsageCategory[] = ["apps", "personal", "appdata", "cache", "system", "free", "other"];

export const REFUSAL_CODES = [
  "not_found",
  "not_regular",
  "symlink",
  "outside_home",
  "home_root",
  "standard_folder",
  "app_managed",
  "system_cache",
  "dot_folder",
  "credentials",
  "agent_data",
  "persona_data",
  "inside_app",
  "app_bundle",
  "managed_library",
  "cloud_synced",
  "mount_point",
] as const;
export type RefusalCode = (typeof REFUSAL_CODES)[number];

export interface UsageNode {
  /** An absolute path, or "group:<key>" for a fixed box. */
  id: string;
  name: string;
  path?: string;
  /** The path in its "~/…" form, for people. */
  display?: string;
  kind: UsageKind | string;
  key?: string;
  category: UsageCategory | string;
  /** Allocated bytes: what frees when the item goes. */
  bytes: number;
  /** How many entries an "other" box stands for. */
  items?: number;
  /** Unix seconds: the newest change in the subtree. */
  used_at?: number;
  has_children: boolean;
  partial?: boolean;
  children?: UsageNode[];
  can_trash?: boolean;
  can_offload?: boolean;
  refusal?: RefusalCode | string;
  cache_id?: string;
  cache_in_use?: boolean;
  app?: { bundle_id: string; version?: string };
  /** Client side only: the inventory item a cloud box draws. */
  cloud?: OffloadItem;
  /** Client side only: a box that previews an offload (a dashed twin on the cloud side). */
  ghost?: boolean;
}

export interface UsageVolume {
  mount: string;
  total_bytes: number;
  used_bytes: number;
  free_bytes: number;
}

export interface UsageReport {
  machine_id: string;
  at: number;
  os: string;
  path: string;
  state: "scanning" | "ready";
  partial: boolean;
  cached: boolean;
  scanned_at?: number;
  duration_ms?: number;
  home?: string;
  volume?: UsageVolume;
  node: UsageNode;
}

export type TrashStatus = "preview" | "trashed" | "refused" | "missing" | "failed";

export interface TrashItem {
  path: string;
  display?: string;
  bytes: number;
  status: TrashStatus | string;
  refusal?: string;
  error?: string;
  trash_id?: string;
}

export interface TrashResult {
  machine_id?: string;
  dry_run: boolean;
  freed_bytes: number;
  pending_bytes: number;
  available_before?: number;
  available_after?: number;
  items: TrashItem[];
}

export interface PurgeResult {
  freed_bytes: number;
  available_before?: number;
  available_after?: number;
  items: { trash_id: string; status: "purged" | "missing" | "failed" | "unsupported" | string; error?: string }[];
}

export interface OffloadProvider {
  extension: string;
  id: string;
  name: string;
  kinds?: string[];
  restore?: boolean;
}

export interface OffloadTool {
  name: string;
  title?: string;
  version?: string;
  installed: boolean;
  source?: string;
  detail?: string;
}

export type OffloadItemKind = "file" | "dir" | "app" | "app_data" | "bundle" | "session" | "other";

export interface OffloadItem {
  id: string;
  name: string;
  kind: OffloadItemKind | string;
  bytes: number;
  stored_bytes?: number;
  origin_path?: string;
  stub_path?: string;
  local_bytes?: number;
  offloaded_at?: number;
  used_at?: number;
  group?: string;
  restorable: boolean;
}

export interface OffloadInventory {
  state: "ready" | "needs_install" | "needs_setup" | "unavailable" | string;
  detail?: string;
  tool?: OffloadTool;
  at?: number;
  total_bytes: number;
  stored_bytes?: number;
  partial?: boolean;
  items: OffloadItem[];
  more?: { count: number; bytes: number };
}

export type OffloadPhase = "install" | "check" | "upload" | "verify" | "cleanup" | "download";
export const OFFLOAD_PHASES: readonly OffloadPhase[] = ["install", "check", "upload", "verify", "cleanup", "download"];

export interface OffloadJob {
  state: "running" | "done" | "failed" | "canceled" | string;
  phase?: OffloadPhase | string;
  done_bytes: number;
  total_bytes: number;
  freed_bytes?: number;
  error?: string;
  items: { path?: string; id?: string; status: string; error?: string; bytes?: number }[];
}

// ── errors ──

export type UsageFailureKind = "node_outdated" | "hub_outdated" | "forbidden" | "offline" | "deadline" | "error";
export interface UsageFailure {
  kind: UsageFailureKind;
  text: string;
}

/** A failed usage, trash or offload call as the map says it. A hub from before these routes answers 404 without a code. */
export function usageFailure(e: unknown): UsageFailure {
  if (e instanceof ApiError) {
    const code = typeof e.body?.code === "string" ? e.body.code : "";
    if (e.status === 409 && code === "node_outdated") return { kind: "node_outdated", text: t("machines-usagemap.err_node_outdated") };
    if (e.status === 404 && (!code || !e.isJson)) return { kind: "hub_outdated", text: t("machines-usagemap.err_hub_outdated") };
    if (e.status === 403) return { kind: "forbidden", text: t("machines-usagemap.err_forbidden") };
    if (e.status === 503 || code === "machine_offline") return { kind: "offline", text: t("machines-usagemap.err_offline") };
    if (e.status === 504 || code === "deadline") return { kind: "deadline", text: t("machines-usagemap.err_deadline") };
  }
  return { kind: "error", text: errorText(e) };
}

// ── calls ──

const base = (mid: string) => `/machines/${enc(mid)}/storage`;

export function fetchUsage(mid: string, opts: { path?: string; rescan?: boolean; waitMs?: number; signal?: AbortSignal } = {}): Promise<UsageReport> {
  const q = new URLSearchParams();
  q.set("path", opts.path ?? "");
  if (opts.rescan) q.set("rescan", "1");
  if (opts.waitMs !== undefined) q.set("wait_ms", String(opts.waitMs));
  return api<UsageReport>(`${base(mid)}/usage?${q.toString()}`, { signal: opts.signal, timeout: pastHub(15_000) });
}

const sleepFor = (ms: number, signal?: AbortSignal) =>
  new Promise<void>((resolve, reject) => {
    if (signal?.aborted) return reject(new DOMException("aborted", "AbortError"));
    const timer = setTimeout(resolve, ms);
    signal?.addEventListener("abort", () => {
      clearTimeout(timer);
      reject(new DOMException("aborted", "AbortError"));
    }, { once: true });
  });

/** Asks for `path` and asks again every `intervalMs` while the node still scans it, handing each answer to `onReport`
 *  (boxes appear and grow instead of a spinner). Only the first call carries `rescan`. Resolves with the ready answer. */
export async function pollUsage(
  mid: string,
  path: string,
  opts: { rescan?: boolean; signal?: AbortSignal; intervalMs?: number; onReport?: (r: UsageReport) => void; fetcher?: typeof fetchUsage } = {},
): Promise<UsageReport> {
  const fetcher = opts.fetcher ?? fetchUsage;
  let rescan = !!opts.rescan;
  for (;;) {
    const r = await fetcher(mid, { path, rescan, signal: opts.signal });
    rescan = false;
    if (opts.signal?.aborted) throw new DOMException("aborted", "AbortError");
    opts.onReport?.(r);
    if (r.state !== "scanning") return r;
    await sleepFor(opts.intervalMs ?? 1000, opts.signal);
  }
}

export function trashPaths(mid: string, paths: readonly string[], mode: { dry_run?: boolean; confirm?: boolean }): Promise<TrashResult> {
  return api<TrashResult>(`${base(mid)}/trash`, { method: "POST", json: { paths, ...mode }, timeout: pastHub(60_000) });
}

export function undoTrash(mid: string, trashIds: readonly string[]): Promise<{ restored: { path: string }[] }> {
  return api(`${base(mid)}/trash/undo`, { method: "POST", json: { trash_ids: trashIds }, timeout: pastHub(30_000) });
}

export function purgeTrash(mid: string, trashIds: readonly string[]): Promise<PurgeResult> {
  return api<PurgeResult>(`${base(mid)}/trash/purge`, { method: "POST", json: { trash_ids: trashIds, confirm: true }, timeout: pastHub(120_000) });
}

/** The enabled offload providers; a hub without the point (404) has none. */
export async function fetchProviders(mid: string, signal?: AbortSignal): Promise<OffloadProvider[]> {
  try {
    const r = await api<{ providers?: OffloadProvider[] }>(`/extensions/-/offload?machine=${enc(mid)}`, { signal });
    return r.providers ?? [];
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) return [];
    throw e;
  }
}

const offloadBase = (p: Pick<OffloadProvider, "extension" | "id">) => `/extensions/${enc(p.extension)}/-/offload/${enc(p.id)}`;

export function fetchInventory(p: OffloadProvider, mid: string, opts: { refresh?: boolean; signal?: AbortSignal } = {}): Promise<OffloadInventory> {
  return api<OffloadInventory>(`${offloadBase(p)}/inventory?machine=${enc(mid)}${opts.refresh ? "&refresh=1" : ""}`, { signal: opts.signal, timeout: pastHub(20_000) });
}

export type RunOutcome =
  | { kind: "started"; job: string }
  | { kind: "needs_install"; tool: OffloadTool; text: string }
  | { kind: "refused"; items: { path?: string; id?: string; reason: string }[]; text: string };

/** Starts an offload or a restore the user confirmed. A missing tool (409 tool_install_required) and refused paths
 *  (422 offload_refused) are answers, not failures: the first asks the second confirmation, the second is listed. */
export async function runOffload(
  p: OffloadProvider,
  body: { machine: string; action: "offload" | "restore"; paths?: string[]; items?: string[]; install?: boolean },
): Promise<RunOutcome> {
  try {
    const r = await api<{ job?: string; status?: string; tool?: OffloadTool; items?: { path?: string; id?: string; reason: string }[] }>(`${offloadBase(p)}/run`, {
      method: "POST",
      json: { ...body, confirm: true },
      timeout: pastHub(30_000),
    });
    if (r.status === "needs_install" && r.tool) return { kind: "needs_install", tool: r.tool, text: "" };
    if (r.status === "refused") return { kind: "refused", items: r.items ?? [], text: "" };
    return { kind: "started", job: r.job ?? "" };
  } catch (e) {
    if (e instanceof ApiError) {
      const code = typeof e.body?.code === "string" ? e.body.code : "";
      const text = typeof e.body?.error === "string" ? e.body.error : "";
      if (e.status === 409 && code === "tool_install_required") return { kind: "needs_install", tool: (e.body.tool as OffloadTool) ?? { name: "", installed: false }, text };
      if (e.status === 422 && code === "offload_refused") return { kind: "refused", items: (e.body.items as { path?: string; id?: string; reason: string }[]) ?? [], text };
    }
    throw e;
  }
}

export function fetchJob(p: OffloadProvider, mid: string, job: string, signal?: AbortSignal): Promise<OffloadJob> {
  return api<OffloadJob>(`${offloadBase(p)}/jobs/${enc(job)}?machine=${enc(mid)}`, { signal, timeout: pastHub(10_000) });
}

export function cancelJob(p: OffloadProvider, mid: string, job: string): Promise<unknown> {
  return api(`${offloadBase(p)}/jobs/${enc(job)}?machine=${enc(mid)}`, { method: "DELETE", timeout: pastHub(10_000) });
}

// ── layout ──

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface Cell<T> extends Rect {
  child: T;
  index: number;
}

/** Squarified treemap (Bruls, Huizing, van Wijk): lays `children` into the rectangle so each one's area is its share of
 *  the bytes and the boxes stay near square. Largest first; children without bytes get no box. */
export function squarify<T extends { bytes: number }>(children: readonly T[], x: number, y: number, w: number, h: number): Cell<T>[] {
  const items = children.map((child, index) => ({ child, index, bytes: child.bytes > 0 ? child.bytes : 0, area: 0 })).filter((i) => i.bytes > 0);
  // stable: equal sizes keep their order
  items.sort((a, b) => b.bytes - a.bytes || a.index - b.index);
  const total = items.reduce((s, i) => s + i.bytes, 0);
  const out: Cell<T>[] = [];
  if (!total || w <= 0 || h <= 0) return out;
  const scale = (w * h) / total;
  for (const i of items) i.area = i.bytes * scale;
  const worst = (row: typeof items, side: number) => {
    const sum = row.reduce((s, r) => s + r.area, 0);
    let max = 0;
    let min = Infinity;
    for (const r of row) {
      max = Math.max(max, r.area);
      min = Math.min(min, r.area);
    }
    return Math.max((side * side * max) / (sum * sum), (sum * sum) / (side * side * min));
  };
  const place = (row: typeof items) => {
    const sum = row.reduce((s, r) => s + r.area, 0);
    if (w >= h) {
      const cw = sum / h;
      let cy = y;
      for (const r of row) {
        const rh = r.area / cw;
        out.push({ child: r.child, index: r.index, x, y: cy, w: cw, h: rh });
        cy += rh;
      }
      x += cw;
      w -= cw;
    } else {
      const rh = sum / w;
      let cx = x;
      for (const r of row) {
        const cw = r.area / rh;
        out.push({ child: r.child, index: r.index, x: cx, y, w: cw, h: rh });
        cx += cw;
      }
      y += rh;
      h -= rh;
    }
  };
  let row: typeof items = [];
  for (const item of items) {
    const side = Math.min(w, h);
    if (row.length && worst([...row, item], side) > worst(row, side)) {
      place(row);
      row = [];
    }
    row.push(item);
  }
  if (row.length) place(row);
  return out;
}

/** The top level of a map: free space gets a strip along one edge (the right, the bottom when narrow), so the used
 *  boxes stay together and the free strip reads as one piece. */
export function layoutTop<T extends { bytes: number; key?: string }>(children: readonly T[], width: number, height: number, narrow: boolean): Cell<T>[] {
  const fi = children.findIndex((c) => c.key === "free" && c.bytes > 0);
  const total = children.reduce((s, c) => s + Math.max(0, c.bytes), 0);
  const share = fi >= 0 && total ? children[fi].bytes / total : 0;
  const strip = Math.round((narrow ? height : width) * share);
  if (fi < 0 || strip < 28 || share > 0.9) return squarify(children, 0, 0, width, height);
  const rest = children.map((c, i) => (i === fi ? { ...c, bytes: 0 } : c));
  const free = children[fi];
  return narrow
    ? [...squarify(rest, 0, 0, width, height - strip).map((c) => ({ ...c, child: children[c.index] })), { child: free, index: fi, x: 0, y: height - strip, w: width, h: strip }]
    : [...squarify(rest, 0, 0, width - strip, height).map((c) => ({ ...c, child: children[c.index] })), { child: free, index: fi, x: width - strip, y: 0, w: strip, h: height }];
}

/** Snaps a rectangle's edges to whole pixels, so neighbours share one line. */
export function snap(r: Rect): Rect {
  const x = Math.round(r.x);
  const y = Math.round(r.y);
  return { x, y, w: Math.round(r.x + r.w) - x, h: Math.round(r.y + r.h) - y };
}

export interface SplitScale {
  local: Rect;
  cloud: Rect;
  /** 1 when both sides share the scale; N (> 1) when the cloud side is drawn 1 : N. */
  ratio: number;
}

/** One scale for both sides: with the height fixed, the cloud side's width is its share of volume + cloud. A cloud side
 *  narrower than `minCloud` keeps that width and gets shorter, so its area still tells the truth; one wider than
 *  `maxShare` of the width is compressed and says by how much (`ratio`). Widths are of the two sides together. */
export function splitScale(volumeTotal: number, cloudTotal: number, width: number, height: number, minCloud = 120, maxShare = 0.45): SplitScale {
  const W = Math.max(0, width);
  const H = Math.max(0, height);
  if (!(cloudTotal > 0) || !(volumeTotal > 0) || W <= 0) {
    return { local: { x: 0, y: 0, w: W, h: H }, cloud: { x: W, y: 0, w: 0, h: 0 }, ratio: 1 };
  }
  const share = cloudTotal / (volumeTotal + cloudTotal);
  let cw = W * share;
  if (share > maxShare) {
    cw = W * maxShare;
    const lw = W - cw;
    const ratio = (cloudTotal * lw) / (volumeTotal * cw);
    return { local: { x: 0, y: 0, w: lw, h: H }, cloud: { x: lw, y: 0, w: cw, h: H }, ratio: Math.max(1, ratio) };
  }
  if (cw < minCloud) {
    cw = Math.min(minCloud, W * maxShare);
    const lw = W - cw;
    const area = (cloudTotal * lw * H) / volumeTotal; // the cloud's true area at the local side's scale
    const ch = area / cw;
    if (ch > H) return { local: { x: 0, y: 0, w: lw, h: H }, cloud: { x: lw, y: 0, w: cw, h: H }, ratio: ch / H };
    return { local: { x: 0, y: 0, w: lw, h: H }, cloud: { x: lw, y: 0, w: cw, h: ch }, ratio: 1 };
  }
  return { local: { x: 0, y: 0, w: W - cw, h: H }, cloud: { x: W - cw, y: 0, w: cw, h: H }, ratio: 1 };
}

/** The scale chip's number ("1 : 3"); undefined while both sides share one scale. */
export function scaleChip(ratio: number): number | undefined {
  return ratio > 1.05 ? Math.max(2, Math.round(ratio)) : undefined;
}

// ── age ──

export type AgeStep = "recent" | "months" | "half_year" | "year";
export const AGE_STEPS: readonly AgeStep[] = ["recent", "months", "half_year", "year"];
const DAY = 86_400;

/** Four steps from recent to over a year (coldest strongest); undefined when the age is unknown. `now` in ms. */
export function ageStep(usedAt: number | undefined, now = Date.now()): AgeStep | undefined {
  if (!usedAt || usedAt <= 0) return undefined;
  const days = (now / 1000 - usedAt) / DAY;
  if (days < 30) return "recent";
  if (days < 180) return "months";
  if (days < 365) return "half_year";
  return "year";
}

// ── tree helpers ──

/** Calls `visit` for every node under `root` (root included) with its ancestors; stops descending when it returns false. */
export function walk(root: UsageNode, visit: (n: UsageNode, ancestors: UsageNode[]) => boolean | void, ancestors: UsageNode[] = []): void {
  if (visit(root, ancestors) === false) return;
  const next = [...ancestors, root];
  for (const c of root.children ?? []) walk(c, visit, next);
}

/** The trail from `root` to the node with `id` (both included), or null. */
export function trailTo(root: UsageNode, id: string): UsageNode[] | null {
  if (root.id === id) return [root];
  for (const c of root.children ?? []) {
    const t = trailTo(c, id);
    if (t) return [root, ...t];
  }
  return null;
}

export function findNode(root: UsageNode, id: string): UsageNode | null {
  const trail = trailTo(root, id);
  return trail ? trail[trail.length - 1] : null;
}

/** Whether `path` is `parent` or inside it. */
export function within(path: string, parent: string): boolean {
  if (!parent) return false;
  if (path === parent) return true;
  const p = parent.endsWith("/") || parent.endsWith("\\") ? parent : parent + (parent.includes("\\") && !parent.includes("/") ? "\\" : "/");
  return path.startsWith(p);
}

/** The folder that holds `path` ("" for a root). */
export function parentPath(path: string): string {
  const trimmed = path.replace(/[\\/]+$/, "");
  const i = Math.max(trimmed.lastIndexOf("/"), trimmed.lastIndexOf("\\"));
  if (i < 0) return "";
  return i === 0 ? trimmed[0] : trimmed.slice(0, i);
}

/** Drops nodes nested in another of the list (a folder and its child count once, as the folder). */
export function outermost<T extends { path?: string }>(nodes: readonly T[]): T[] {
  const withPath = nodes.filter((n) => n.path);
  return withPath.filter((n) => !withPath.some((o) => o !== n && o.path !== n.path && within(n.path!, o.path!))).filter((n, i, all) => all.findIndex((o) => o.path === n.path) === i);
}

/** `root` with `fresh` in the place of the node with the same id: a folder's scan (or rescan) spliced into the map.
 *  The ancestors' bytes change by the difference, so the boxes around keep adding up. */
export function spliceNode(root: UsageNode, fresh: UsageNode): UsageNode {
  if (root.id === fresh.id) return { ...fresh, name: fresh.name || root.name, key: fresh.key ?? root.key, display: fresh.display ?? root.display };
  if (!root.children) return root;
  let delta = 0;
  let changed = false;
  const children = root.children.map((c) => {
    const next = spliceNode(c, fresh);
    if (next !== c) {
      changed = true;
      delta += next.bytes - c.bytes;
    }
    return next;
  });
  return changed ? { ...root, children, bytes: Math.max(0, root.bytes + delta) } : root;
}

/** `root` without the nodes at `paths` (moved to the trash or offloaded), each ancestor smaller by what left under it. */
export function removePaths(root: UsageNode, paths: readonly string[]): UsageNode {
  if (!paths.length || !root.children) return root;
  let removed = 0;
  const children: UsageNode[] = [];
  let changed = false;
  for (const c of root.children) {
    if (c.path && paths.includes(c.path)) {
      removed += c.bytes;
      changed = true;
      continue;
    }
    const next = removePaths(c, paths);
    if (next !== c) {
      changed = true;
      removed += c.bytes - next.bytes;
    }
    children.push(next);
  }
  return changed ? { ...root, children, bytes: Math.max(0, root.bytes - removed) } : root;
}

/** The overview with its free box set to what the volume reports now. */
export function withFree(root: UsageNode, volume: UsageVolume | undefined): UsageNode {
  if (!volume || !root.children) return root;
  const i = root.children.findIndex((c) => c.key === "free");
  if (i < 0 || root.children[i].bytes === volume.free_bytes) return root;
  const delta = volume.free_bytes - root.children[i].bytes;
  const children = root.children.slice();
  children[i] = { ...children[i], bytes: volume.free_bytes };
  return { ...root, children, bytes: Math.max(0, root.bytes + delta) };
}

/** Whether a box can be opened: it has children drawn, or a folder the node can still list. */
export function zoomable(n: UsageNode): boolean {
  if (n.kind === "free" || n.kind === "unmeasured" || n.kind === "other" || n.kind === "file") return false;
  return !!n.children?.length || (n.has_children && !!n.path);
}

/** Whether a box takes an action, so it can be selected. */
export function actionable(n: UsageNode): boolean {
  return !!n.path && (!!n.can_trash || !!n.can_offload || (!!n.cache_id && !n.cache_in_use));
}

// ── "what if" ──

export interface WhatIf {
  freed: number;
  freeBefore: number;
  freeAfter: number;
  /** Used share of the volume, 0–100. */
  pctBefore: number;
  pctAfter: number;
  count: number;
}

/** What the volume would look like without the selection (nested picks count once). */
export function whatIf(volume: UsageVolume | undefined, selection: readonly UsageNode[]): WhatIf {
  const picked = outermost(selection);
  const freed = picked.reduce((s, n) => s + Math.max(0, n.bytes), 0);
  const total = volume?.total_bytes ?? 0;
  const used = volume?.used_bytes ?? 0;
  const free = volume?.free_bytes ?? 0;
  const pct = (u: number) => (total > 0 ? Math.max(0, Math.min(100, (100 * u) / total)) : 0);
  return { freed, freeBefore: free, freeAfter: free + freed, pctBefore: pct(used), pctAfter: pct(used - freed), count: picked.length };
}

// ── suggestions and the goal ──

export type ReclaimAction = "clean" | "trash" | "offload";

export interface ReclaimCandidate {
  node: UsageNode;
  action: ReclaimAction;
  bytes: number;
}

const COLD_DAYS = 180;
const MIN_SUGGESTION = 100 * 1024 ** 2;

function providerTakes(providers: readonly OffloadProvider[], n: UsageNode): boolean {
  const kind = n.kind === "app" ? "app" : n.kind === "file" ? "file" : "dir";
  return providers.some((p) => (p.kinds?.length ? p.kinds : ["file", "dir"]).includes(kind));
}

/** Every measured item an action may take, in the goal's order: unused catalog caches first (largest first), then
 *  trashable caches and cold offloadable items by size × age. `cold` keeps only items untouched 180 days or more. */
export function reclaimCandidates(tree: UsageNode, providers: readonly OffloadProvider[], now = Date.now(), opts: { cold?: boolean; minBytes?: number } = {}): ReclaimCandidate[] {
  const min = opts.minBytes ?? 0;
  const caches: ReclaimCandidate[] = [];
  const rest: (ReclaimCandidate & { score: number })[] = [];
  walk(tree, (n) => {
    if (!n.path || n.bytes < min) return;
    if (n.cache_id) {
      // a catalog cache is cleaned, never trashed or uploaded; one a running tool uses is left alone
      if (!n.cache_in_use) caches.push({ node: n, action: "clean", bytes: n.bytes });
      return false;
    }
    const days = n.used_at ? Math.max(0, (now / 1000 - n.used_at) / DAY) : 0;
    if (n.category === "cache" && n.can_trash) {
      rest.push({ node: n, action: "trash", bytes: n.bytes, score: n.bytes * Math.max(days, 1) });
      return false;
    }
    if (n.can_offload && providers.length && providerTakes(providers, n) && (!opts.cold || days >= COLD_DAYS)) {
      rest.push({ node: n, action: "offload", bytes: n.bytes, score: n.bytes * Math.max(days, 1) });
    }
  });
  caches.sort((a, b) => b.bytes - a.bytes);
  rest.sort((a, b) => b.score - a.score);
  return [...caches, ...rest.map(({ score: _score, ...c }) => c)];
}

/** Picks candidates in order until their sum reaches `goal`, skipping one nested in (or holding) a pick. */
export function pickForGoal(candidates: readonly ReclaimCandidate[], goal: number): ReclaimCandidate[] {
  const out: ReclaimCandidate[] = [];
  let sum = 0;
  for (const c of candidates) {
    if (sum >= goal) break;
    const p = c.node.path!;
    if (out.some((o) => within(p, o.node.path!) || within(o.node.path!, p))) continue;
    out.push(c);
    sum += c.bytes;
  }
  return out;
}

/** Up to five suggestions for focus=reclaim, largest first: unused catalog caches (Clean), trashable cache folders,
 *  and items untouched 180 days that a provider can take (Offload). */
export function suggestReclaim(tree: UsageNode, providers: readonly OffloadProvider[], now = Date.now(), limit = 5): ReclaimCandidate[] {
  const all = reclaimCandidates(tree, providers, now, { cold: true, minBytes: MIN_SUGGESTION });
  const bySize = [...all].sort((a, b) => b.bytes - a.bytes);
  return pickForGoal(bySize, Infinity).slice(0, limit);
}

// ── the two sides' links ──

export interface LinkIndex {
  /** The items whose origin is `path` or inside it. */
  under(path: string): OffloadItem[];
  /** The id of the deepest box among `boxes` that holds the item's origin, or undefined. */
  anchor(item: OffloadItem, boxes: readonly { id: string; path?: string }[]): string | undefined;
}

/** Maps each offloaded item's origin to the local side: badges count what a box held, ribbons find a cloud box's partner. */
export function linkIndex(items: readonly OffloadItem[]): LinkIndex {
  const placed = items.filter((i) => i.origin_path);
  return {
    under: (path) => placed.filter((i) => within(i.origin_path!, path)),
    anchor: (item, boxes) => {
      if (!item.origin_path) return undefined;
      let best: { id: string; len: number } | undefined;
      for (const b of boxes) {
        if (!b.path || !within(item.origin_path, b.path)) continue;
        if (!best || b.path.length > best.len) best = { id: b.id, len: b.path.length };
      }
      return best?.id;
    },
  };
}

/** The cloud side as a tree: the inventory grouped by its own groups (else by kind), plus one box for what it did not list. */
export function cloudTree(inv: OffloadInventory | null, pending: readonly UsageNode[] = []): UsageNode {
  const groups = new Map<string, UsageNode[]>();
  for (const it of inv?.items ?? []) {
    const g = it.group || it.kind || "other";
    const node: UsageNode = {
      id: `cloud:${it.id}`,
      name: it.name,
      path: undefined,
      display: it.origin_path,
      kind: it.kind === "dir" || it.kind === "app" || it.kind === "file" ? it.kind : "other",
      category: it.kind === "app" ? "apps" : it.kind === "app_data" ? "appdata" : "personal",
      bytes: Math.max(0, it.bytes),
      used_at: it.used_at,
      has_children: false,
      cloud: it,
    };
    groups.set(g, [...(groups.get(g) ?? []), node]);
  }
  const children: UsageNode[] = [...groups].map(([g, kids]) => ({
    id: `cloud-group:${g}`,
    name: g,
    key: `cloud_${g}`,
    kind: "group",
    category: kids[0]?.category ?? "other",
    bytes: kids.reduce((s, k) => s + k.bytes, 0),
    has_children: true,
    children: kids,
  }));
  if (inv?.more && inv.more.bytes > 0) children.push({ id: "cloud-group:more", name: "", key: "other", kind: "other", category: "other", bytes: inv.more.bytes, items: inv.more.count, has_children: false });
  for (const p of pending) children.push({ ...p, id: `ghost:${p.id}`, ghost: true, children: undefined, has_children: false });
  return { id: "cloud:root", name: "", kind: "root", category: "other", bytes: children.reduce((s, c) => s + c.bytes, 0), has_children: children.length > 0, children };
}

// ── boxes to draw ──

export const HEADER = 22;

export interface MapBox extends Rect {
  node: UsageNode;
  parent: UsageNode | null;
  depth: number;
  /** Draws its children inside under a title strip. */
  nested: boolean;
  showName: boolean;
  showSize: boolean;
  /** Size under the name (a tall leaf) or beside it. */
  inline: boolean;
}

/** Width of a line of text in px; `fallback` guesses from the length where the page cannot measure (tests). */
export type Measure = (text: string, weight: number, px: number) => number;
export const roughMeasure: Measure = (text, _weight, px) => Math.ceil(text.length * px * 0.6);

/** Every box of `focus`'s map, outermost first (a child after its frame, so it sits on top): nested up to `maxDepth`
 *  levels, edges snapped, a name or size only where it fits whole. */
export function layoutBoxes(
  focus: UsageNode,
  width: number,
  height: number,
  opts: { maxDepth: number; narrow: boolean; label: (n: UsageNode, parent: UsageNode | null) => string; size: (n: UsageNode) => string; measure?: Measure; top?: boolean },
): MapBox[] {
  const measure = opts.measure ?? roughMeasure;
  const out: MapBox[] = [];
  const box = (node: UsageNode, parent: UsageNode | null, rect: Rect, depth: number) => {
    const { x, y, w, h } = snap(rect);
    if (w < 4 || h < 4) return;
    const kids = node.children?.length ? node.children : null;
    const nested = !!kids && depth < opts.maxDepth && w >= (depth >= 2 ? 96 : 72) && h >= HEADER + (depth >= 2 ? 50 : 28);
    const big = !nested && w >= 160 && h >= 72;
    const label = opts.label(node, parent);
    const size = opts.size(node);
    const nameW = measure(label, 600, 12);
    const sizeW = measure(size, 400, 11.5);
    const inner = w - (big ? 22 : 14);
    let showName = false;
    let showSize = false;
    let inline = false;
    if (nested) {
      showName = w >= 48;
      showSize = showName && nameW + 6 + sizeW <= inner;
      inline = true;
    } else if (h >= 22 && inner >= Math.min(nameW, 28)) {
      showName = true;
      if (h >= 42 && sizeW <= inner) showSize = true;
      else if (nameW + 6 + sizeW <= inner) {
        showSize = true;
        inline = true;
      }
    }
    out.push({ node, parent, depth, x, y, w, h, nested, showName, showSize, inline });
    if (nested) for (const c of squarify(kids!, x + 2, y + HEADER, w - 4, h - HEADER - 2)) box(c.child, node, c, depth + 1);
  };
  const top = focus.children?.length ? (opts.top ? layoutTop(focus.children, width, height, opts.narrow) : squarify(focus.children, 0, 0, width, height)) : null;
  if (top) for (const c of top) box(c.child, focus, c, 1);
  else box(focus, null, { x: 0, y: 0, w: width, h: height }, opts.maxDepth);
  return out;
}

export type Direction = "left" | "right" | "up" | "down";

/** The box an arrow key moves to: the nearest one whose centre lies that way, preferring ones in line. */
export function nearestBox<T extends Rect>(boxes: readonly T[], from: T, dir: Direction): T | undefined {
  const cx = from.x + from.w / 2;
  const cy = from.y + from.h / 2;
  let best: { b: T; d: number } | undefined;
  for (const b of boxes) {
    if (b === from) continue;
    const bx = b.x + b.w / 2;
    const by = b.y + b.h / 2;
    const dx = bx - cx;
    const dy = by - cy;
    const along = dir === "left" ? -dx : dir === "right" ? dx : dir === "up" ? -dy : dy;
    const across = dir === "left" || dir === "right" ? Math.abs(dy) : Math.abs(dx);
    if (along <= 0.5) continue;
    const d = along + across * 2;
    if (!best || d < best.d) best = { b, d };
  }
  return best?.b;
}
