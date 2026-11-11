// Knowledge points (topics, docs/assistant/knowledge.md "Topics"): their kinds, grouping and sources as the topic page
// and the knowledge map show them. Kept out of lib/kb.ts, which the first screen loads: only those two lazy pages show
// points, so neither this code nor its words are in the startup bundle and catalog (src/startup-preload.ts
// FIRST_SCREEN_BUDGET).
import { formatDate, t } from "../i18n";
import type { KBEntry } from "./kb";

const monthDay = (d: Date) => formatDate(d, { month: "short", day: "numeric" });
const yearMonthDay = (d: Date) => formatDate(d, { year: "numeric", month: "short", day: "numeric" });

/** A point's kind as a word; an unknown kind as "Point". */
export function pointKindLabel(k: string): string {
  return isPointKind(k) ? t(`knowledge-topicpoints.point_${k}`) : t("knowledge-topicpoints.point_plain");
}

/** What a knowledge point is. The hub may add kinds; an unknown one shows as a plain point. */
export const POINT_KINDS = ["concept", "decision", "practice", "pitfall", "fact", "question"] as const;
export type PointKind = (typeof POINT_KINDS)[number];

export function isPointKind(k: unknown): k is PointKind {
  return typeof k === "string" && (POINT_KINDS as readonly string[]).includes(k);
}

/** Pitfalls and open questions are what to watch out for: they get the warning tone. */
export function pointWarns(k: string): boolean {
  return k === "pitfall" || k === "question";
}

export interface TopicSource {
  kind: string;
  id: string;
  /** The day the source said it, YYYY-MM-DD. */
  day?: string;
}

export interface TopicPoint {
  id: string;
  /** Subtopics below the topic, outermost first (0–2). */
  path?: string[] | null;
  kind: string;
  text: string;
  /** decisions: the reason; pitfalls: the cause. */
  why?: string;
  sources?: TopicSource[] | null;
  /** When it was last confirmed, YYYY-MM-DD. */
  when?: string;
}

export interface PointGroup {
  /** The subtopic path this group is under ([] for the points directly under the topic). */
  path: string[];
  points: TopicPoint[];
  groups: PointGroup[];
}

/**
 * A topic's points as the page shows them: the ones directly under the topic first, then one group per subtopic in the
 * order the subtopics first appear, each with its own points and its subtopics' groups. Blank path parts are dropped.
 */
export function groupPoints(points?: TopicPoint[] | null): PointGroup {
  const root: PointGroup = { path: [], points: [], groups: [] };
  for (const p of points ?? []) {
    if (!p?.text) continue;
    let g = root;
    for (const raw of p.path ?? []) {
      const name = typeof raw === "string" ? raw.trim() : "";
      if (!name) continue;
      let next = g.groups.find((x) => x.path[x.path.length - 1] === name);
      if (!next) g.groups.push((next = { path: [...g.path, name], points: [], groups: [] }));
      g = next;
    }
    g.points.push(p);
  }
  return root;
}

/** A point's sources that the detail names, with their names (unknown ones are left out: no raw ids on the page). */
export function pointSources(p: TopicPoint, links?: KBEntry[] | null): KBEntry[] {
  const seen = new Set<string>();
  const out: KBEntry[] = [];
  for (const s of p.sources ?? []) {
    const e = (links ?? []).find((l) => l.kind === s.kind && l.id === s.id);
    const key = `${s.kind}/${s.id}`;
    if (e && !seen.has(key)) {
      seen.add(key);
      out.push(e);
    }
  }
  return out;
}


/** A point's day (YYYY-MM-DD) as a short date: month and day this year, with the year otherwise. */
export function pointDay(day: string, now = new Date()): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(day);
  if (!m) return day;
  const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
  return d.getFullYear() === now.getFullYear() ? monthDay(d) : yearMonthDay(d);
}
