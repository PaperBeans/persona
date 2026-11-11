import { useEffect, useState } from "react";
import { t } from "../i18n";
import { api } from "../lib/api";
import { PAGE_SIZE, type ListPage } from "../lib/session-list";
import { onHubEvent } from "../lib/store";
import type { Session } from "../lib/types";

type Read = (path: string, signal: AbortSignal) => Promise<ListPage<Session>>;
const readPage: Read = (path, signal) => api<ListPage<Session>>(path, { signal });

/** Read the whole *pending* work set. The scope excludes the main/health chats on the hub; an independent work
 * conversation can have Assistant=true and still belong here. Never use the app's recent-session window. */
export async function readPendingWorkSessions(manager: string | undefined, signal: AbortSignal, read: Read = readPage) {
  const sessions: Session[] = [];
  const cursors = new Set<string>();
  let revision: string | undefined;
  let cursor: string | undefined;
  do {
    const query = new URLSearchParams({ scope: "work", state: "needs_you", summary: "1", limit: String(PAGE_SIZE) });
    if (manager) query.set("manager", manager);
    if (cursor) query.set("cursor", cursor);
    const page = await read(`/sessions?${query}`, signal);
    signal.throwIfAborted();
    if (!Array.isArray(page?.sessions) || !page.revision || (revision !== undefined && page.revision !== revision) ||
        typeof page.has_more !== "boolean" || (page.has_more && (!page.next_cursor || !page.sessions.length || cursors.has(page.next_cursor)))) {
      throw new Error(t("paged-agents.invalid_page"));
    }
    revision = page.revision;
    sessions.push(...page.sessions);
    cursor = page.has_more ? page.next_cursor : undefined;
    if (cursor) cursors.add(cursor);
  } while (cursor);
  return { sessions, revision };
}

const EMPTY: Session[] = [];
// Coalesce list hints from active sessions; an in-flight read gets at most one trailing read.
const REFRESH_DELAY = 750;
const RETRY_DELAY = 3000;

export function usePendingWorkSessions(manager: string | undefined) {
  const [snapshot, setSnapshot] = useState<{ manager?: string; sessions: Session[] }>();
  useEffect(() => {
    const abort = new AbortController();
    let revision: string | undefined;
    let reading = false, dirty = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const schedule = (delay = REFRESH_DELAY) => {
      dirty = true;
      if (!reading && timer === undefined && document.visibilityState !== "hidden") timer = setTimeout(() => void refresh(), delay);
    };
    const refresh = async () => {
      timer = undefined;
      if (abort.signal.aborted || !dirty || document.visibilityState === "hidden") return;
      reading = true;
      dirty = false;
      let delay = REFRESH_DELAY;
      try {
        const next = await readPendingWorkSessions(manager, abort.signal);
        revision = next.revision;
        setSnapshot({ manager, sessions: next.sessions });
      } catch {
        dirty = true;
        delay = RETRY_DELAY;
      } finally {
        reading = false;
        if (dirty && !abort.signal.aborted) schedule(delay);
      }
    };
    const offList = onHubEvent("sessions_list", (msg) => { if (revision === undefined || revision !== msg?.revision) schedule(); });
    const offHello = onHubEvent("hello", () => schedule());
    const onVisible = () => { if (dirty && document.visibilityState !== "hidden") schedule(0); };
    document.addEventListener("visibilitychange", onVisible);
    void refresh();
    return () => {
      abort.abort();
      clearTimeout(timer);
      offList();
      offHello();
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [manager]);
  const current = snapshot && snapshot.manager === manager ? snapshot : undefined;
  return { sessions: current?.sessions ?? EMPTY, loaded: Boolean(current) };
}
