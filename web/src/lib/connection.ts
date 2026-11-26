// What the page says about its connection to the hub once it has dropped (App.tsx's pill and its details, the
// sidebar's footer), after the app's ConnectionTitle / ConnectionDetails: which phase, why, since when, the next try.
// Pure: store.ts keeps the facts (`State.link`), the tests drive these directly.
import { formatDate, t } from "../i18n";
import { BUSY_MOTION } from "./busy-motion";
import type { Conn } from "./store";

/** The socket's facts behind the phase. Times in ms, 0: none. */
export interface ConnLink {
  /** When the page was last connected (the drop began); 0: never this page's life. */
  lastOnline: number;
  /** When the next attempt starts. */
  nextTry: number;
  /** The hub said it was restarting (hub_restarting / close 1001): an update or a planned restart. */
  restarting: boolean;
  /** The browser has a network (navigator.onLine). */
  networkUp: boolean;
  /** Optional error reason probed via HTTP after a rejected WebSocket upgrade. */
  lastError?: string;
}

/** How long a restart is told as one ("Hub is updating…"): after it the hub is just out of reach (the app's
 *  hubRestartTimeout), so a crash loop or a hub that never comes back does not promise "reconnects when done" forever. */
export const RESTARTING_MS = 300_000;

export const NO_LINK: ConnLink = { lastOnline: 0, nextTry: 0, restarting: false, networkUp: true };

/** What the pill says: no network, a hub restarting, still trying, or tried a few times and still nothing. */
export type ConnPhase = "online" | "no_network" | "restarting" | "connecting" | "unreachable";

export function connPhase(conn: Conn, link: ConnLink): ConnPhase {
  if (conn === "online") return "online";
  if (!link.networkUp) return "no_network";
  if (link.restarting) return "restarting";
  return conn === "connecting" ? "connecting" : "unreachable";
}

/** The dot beside the phase (the pill, its details, the sidebar's footer and rail): red once it gave up for now,
 *  green online, orange otherwise; it breathes while trying. `color` is the dot's classes: its colour, and for a dot that
 *  breathes the busy-motion mark too, so it keeps breathing (slowly) under reduced motion (lib/busy-motion.ts). */
export function phaseTone(p: ConnPhase): { color: string; breathing: boolean } {
  const breathing = p === "connecting" || p === "restarting";
  const color = p === "unreachable" ? "bg-red" : p === "online" ? "bg-green" : "bg-orange";
  return { color: breathing ? `${color} ${BUSY_MOTION}` : color, breathing };
}

/** The breathing of a dot that is still trying (the `breathe` keyframes in index.css). */
export const BREATHING = { animation: "breathe 1.4s ease-in-out infinite" } as const;

/** The pill's words (and the sidebar footer's while not online). */
export function phaseTitle(p: ConnPhase): string {
  switch (p) {
    case "online":
      return t("lib-connection.online");
    case "no_network":
      return t("lib-connection.no_network");
    case "restarting":
      return t("lib-connection.restarting");
    case "connecting":
      return t("lib-connection.connecting");
    case "unreachable":
      return t("app.reconnecting");
  }
}

/** The sidebar's footer and rail: the computers online, or why not ("No network" / "Hub is updating…" as the pill says). */
export function connStatus(p: ConnPhase, online: number, total: number): string {
  switch (p) {
    case "online":
      return t("sidebar.computers_online", { online, total, count: total });
    case "no_network":
    case "restarting":
      return phaseTitle(p);
    case "connecting":
      return t("sidebar.connecting");
    case "unreachable":
      return t("sidebar.unreachable");
  }
}

/** One sentence on why. */
export function phaseExplanation(p: ConnPhase): string {
  switch (p) {
    case "online":
      return t("lib-connection.online_detail");
    case "no_network":
      return t("lib-connection.no_network_detail");
    case "restarting":
      return t("lib-connection.restarting_detail");
    case "connecting":
    case "unreachable":
      return t("lib-connection.unreachable_detail");
  }
}

/** Short lines under it: when the next try is, and since when the page has been away. */
export function connFacts(p: ConnPhase, link: ConnLink, now: number): string[] {
  if (p === "online") return [];
  const out: string[] = [];
  if (p !== "no_network" && link.lastError) out.push(link.lastError);
  if (p !== "no_network" && link.nextTry) {
    const s = Math.ceil((link.nextTry - now) / 1000);
    out.push(s > 0 ? t("lib-connection.retry_in", { count: s }) : t("lib-connection.retrying"));
  }
  if (link.lastOnline) out.push(t("lib-connection.last_online", { ago: ago(link.lastOnline, now) }));
  return out;
}

/** "12 s ago", "3 min ago", then the time of day, with the date once it was another day (a laptop shut overnight). */
export function ago(at: number, now: number): string {
  const s = Math.max(0, Math.floor((now - at) / 1000));
  if (s < 60) return t("lib-connection.seconds_ago", { count: s });
  if (s < 3600) return t("lib-connection.minutes_ago", { count: Math.floor(s / 60) });
  const time = formatDate(at, { hour: "2-digit", minute: "2-digit" });
  if (new Date(at).toDateString() === new Date(now).toDateString()) return time;
  return t("lib-format.date_time", { date: formatDate(at, { month: "short", day: "numeric" }), time });
}
