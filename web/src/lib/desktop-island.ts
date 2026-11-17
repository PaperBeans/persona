// The island at the MacBook notch (#7669, docs/desktop/desktop.md "Island at the MacBook notch"): what the desktop app
// draws there, worked out from what the Dock badge counts (useCounts) and the Agents list shows. Sent to a desktop that
// has the island; the desktop keeps the latest and ignores it while the island is off.
import { useEffect, useMemo, useSyncExternalStore } from "react";
import { t } from "../i18n";
import { useStore } from "./store";
import { sessionTitle, stateOf } from "./format";
import { sessionCard } from "./folded-rows";
import { paneRoute } from "./herdr-tree";
import { rowLine } from "./row-line";
import { href } from "./router";
import { useAgentSessions, useCounts, useFullAgentSessions } from "./sessions";
import { stateCount, stateName } from "./state-labels";
import type { Interaction, Session } from "./types";
import type { DesktopIsland, DesktopIslandRow, DesktopIslandState } from "./desktop-types";

/** The most rows the island lists, as the desktop draws them. */
export const ISLAND_ROWS = 5;

const RANK: Record<string, number> = { needs_you: 0, working: 1 };

/** A row line's colored lead and its words on one line, as the Agents list sets them side by side: no space after a
 *  full-width colon or a lead that ends in one. */
function joinLead(lead: string, text: string): string {
  if (!lead || !text) return lead || text;
  return /[：\s]$/.test(lead) ? lead + text : `${lead} ${text}`;
}

/** The island's state: the badge's counts, and the agents that need you, then the working ones, newest first, each with
 *  the line its Agents-list row shows (what it asks, or what it is doing). */
export function islandState(sessions: readonly Session[], interactions: readonly Interaction[], counts: { needs: number; working: number; unread: number }): DesktopIslandState {
  // the card a row speaks for, as the Agents list finds it: its own, else one a child session folded under it waits on
  const card = (s: Session) => sessionCard(interactions, s.machine_id, s.session_id);
  const time = (s: Session) => Date.parse(s.updated_at ?? "") || 0;
  // filter makes the copy that is sorted: the hooks' shared lists are never reordered
  const rows = sessions
    .filter((s) => !s.assistant && stateOf(s) in RANK)
    .sort((a, b) => RANK[stateOf(a)] - RANK[stateOf(b)] || time(b) - time(a))
    .slice(0, ISLAND_ROWS)
    .map((s): DesktopIslandRow => {
      const state = stateOf(s);
      const words = state === "needs_you" ? rowLine(s, card(s)) : undefined;
      const line = words ? joinLead(words.lead ?? "", words.text) : (s.activity?.trim() ?? "");
      return { title: sessionTitle(s), state, label: stateName(state), line, route: href(paneRoute(s)) };
    });
  const summary = [
    counts.needs > 0 && stateCount("needs_you", counts.needs),
    counts.working > 0 && stateCount("working", counts.working),
    counts.unread > 0 && stateCount("unread", counts.unread),
  ].filter(Boolean).join(t("home.summary_separator"));
  return { needsYou: counts.needs, working: counts.working, unread: counts.unread, rows, summary };
}

// Whether the desktop draws the island: its answer, asked when the web starts and given again by the Settings switch,
// the one place that turns it on or off.
let islandOn = false;
const islandListeners = new Set<() => void>();
const subscribeIsland = (listener: () => void) => {
  islandListeners.add(listener);
  return () => { islandListeners.delete(listener); };
};
const getIslandOn = () => islandOn;

/** Keeps the desktop's answer (DesktopIsland.enabled) for the island's rows: Settings gives it after the switch. */
export function setIslandOn(on: boolean): void {
  if (on === islandOn) return;
  islandOn = on;
  islandListeners.forEach((listener) => listener());
}

/** What the island shows of nobody: once the shell is gone (signed out, or the credential stopped working). */
const NOTHING: DesktopIslandState = { needsYou: 0, working: 0, unread: 0, rows: [], summary: "" };

/** Sends the island nothing of this account: the credential stopped working, or the shell is gone. */
export function clearDesktopIsland(): void {
  window.personaDesktop?.setIslandState?.(NOTHING);
}

/** Sends the island's state when it changes. A leaf, as AttentionSignals: a sessions push re-renders only it. */
export function DesktopIslandSignals(): null {
  const on = useSyncExternalStore(subscribeIsland, getIslandOn, getIslandOn);
  useEffect(() => {
    let live = true;
    window.personaDesktop?.getIsland?.().then((v: DesktopIsland) => { if (live) setIslandOn(v.enabled); }, () => {});
    return () => { live = false; };
  }, []);
  // The counts are the whole hub's, and so are the rows: a hub with the paged Agents list pushes only its first page, so
  // while the island is on (it is off by default) they come from the complete list, the pushed page until it has loaded.
  const pushed = useAgentSessions();
  const complete = useFullAgentSessions(on);
  const sessions = complete.loaded ? complete.sessions : pushed;
  const interactions = useStore((s) => s.interactions);
  const counts = useCounts();
  // No language among the inputs: a page shows one, and choosing another reloads it (i18n setLanguagePreference).
  const state = useMemo(() => islandState(sessions, interactions, counts), [sessions, interactions, counts]);
  const sent = JSON.stringify(state);
  useEffect(() => {
    window.personaDesktop?.setIslandState?.(JSON.parse(sent) as DesktopIslandState);
  }, [sent]);
  useEffect(() => () => clearDesktopIsland(), []);
  return null;
}
