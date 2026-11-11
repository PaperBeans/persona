// A `/t/…` page whose tab is not among the rows the page has (a paged hub pushes only active and pinned summaries):
// ask the hub once for that machine's sessions before saying the tab is gone.
import { useEffect, useState } from "react";
import { api } from "./api";
import { resolveTab } from "./herdr-tree";
import type { Session } from "./types";

export interface TabRoute { mid: string; space: string; slot: string; sid?: string }

export type TabLookup = { state: "idle" } | { state: "asking" } | { state: "found"; mid: string; sid: string } | { state: "missing" };

export const tabLookupKey = (r: TabRoute) => `${r.mid}\u0000${r.space}\u0000${r.slot}\u0000${r.sid ?? ""}`;

type Page = { sessions?: Session[] | null; next_cursor?: string };
const MAX_PAGES = 20;
const readPage = (r: TabRoute, cursor?: string) => {
  const q = new URLSearchParams({ machine_id: r.mid, group: r.space, limit: "200" });
  if (cursor) q.set("cursor", cursor);
  return api<Page>(`/sessions?${q}`);
};

/** Looks the tab up in the hub's list of its machine and space (every page of it; a hub without paging sends all). */
export async function lookupTab(r: TabRoute, read: (r: TabRoute, cursor?: string) => Promise<Page> = readPage): Promise<TabLookup> {
  const rows: Session[] = [];
  let cursor: string | undefined;
  for (let i = 0; i < MAX_PAGES; i++) {
    const page = await read(r, cursor);
    rows.push(...(page.sessions ?? []).filter((s) => s.machine_id === r.mid));
    cursor = page.next_cursor;
    if (!cursor) break;
  }
  const pane = resolveTab(rows, r);
  return pane ? { state: "found", mid: pane.machine_id, sid: pane.session_id } : { state: "missing" };
}

/** While `need` (the list is in and the tab is not on it), the hub's answer for the route; idle otherwise. */
export function useTabLookup(route: TabRoute | undefined, need: boolean): TabLookup {
  const key = route ? tabLookupKey(route) : "";
  const [result, setResult] = useState<{ key: string; lookup: TabLookup }>({ key: "", lookup: { state: "idle" } });
  useEffect(() => {
    if (!route || !need) return;
    let live = true;
    setResult({ key, lookup: { state: "asking" } });
    lookupTab(route).then((lookup) => live && setResult({ key, lookup }), () => live && setResult({ key, lookup: { state: "missing" } }));
    return () => { live = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, need]);
  if (!route || !need) return { state: "idle" };
  return result.key === key ? result.lookup : { state: "asking" };
}
