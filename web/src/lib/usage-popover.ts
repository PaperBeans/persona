// Popover-only pure helpers for subscription usage cards and the reset timeline (#13173, #13870, #13964).
// Kept in a separate module so UsagePopover's lazy chunk owns them instead of the cold first screen.
import { t as tr } from "../i18n";
import { canonicalUsageProvider, windowMinutes, type UsageAccount, type UsageWindow } from "./usage-windows";

/** The plan to write beside the name, unless the name already says it ("Codex Pro" and plan "pro"). */
export function planNote(a: UsageAccount): string {
  const plan = (a.plan ?? "").trim();
  return plan && !a.name.toLowerCase().includes(plan.toLowerCase()) ? plan : "";
}

/** The longest stretch the reset timeline shows: windows resetting later are drawn to its end. */
export const TIMELINE_CAP_MS = 7 * 24 * 3600_000;

/** The windows of one account, in the order a card lists them: the plan's own 5h, then 7d, then the other plan windows
 *  by length, and last the per-model allowances (by length too). */
export function sortedWindows(a: Pick<UsageAccount, "windows">): UsageWindow[] {
  const rank = (w: UsageWindow) => {
    const m = windowMinutes(w);
    return (w.model ? 2e6 : 0) + (m === 300 ? 0 : m === 10080 ? 1 : 2 + m);
  };
  return [...a.windows].sort((x, y) => rank(x) - rank(y));
}

/** When an account's plan 7d window resets, in ms; Infinity without one (or without a reset time): those sort last. */
export function weeklyResetMs(a: Pick<UsageAccount, "windows">): number {
  const w = a.windows.find((x) => !x.model && windowMinutes(x) === 10080 && x.resets_at);
  const at = w?.resets_at ? Date.parse(w.resets_at) : NaN;
  return Number.isFinite(at) ? at : Infinity;
}

/** A card's accounts: the soonest 7d reset first; accounts without one last, then by name (case-insensitive) and id. */
export function sortForCard(accounts: UsageAccount[]): UsageAccount[] {
  return [...accounts].sort((a, b) => {
    const x = weeklyResetMs(a);
    const y = weeklyResetMs(b);
    if (x !== y) return x < y ? -1 : 1;
    return a.name.localeCompare(b.name, undefined, { sensitivity: "base" }) || a.name.localeCompare(b.name) || a.id.localeCompare(b.id);
  });
}

export interface ProviderGroup {
  provider: string;
  /** Sorted by `sortForCard`. */
  accounts: UsageAccount[];
  /** The newest `sampled_at` of the group, "" when none reads as a time. */
  sampledAt: string;
}

/** The accounts grouped by provider (account type), groups in the order the hub first listed them. */
export function groupByProvider(accounts: UsageAccount[]): ProviderGroup[] {
  const by = new Map<string, UsageAccount[]>();
  for (const a of accounts) {
    const provider = canonicalUsageProvider(a.provider);
    by.set(provider, [...(by.get(provider) ?? []), a]);
  }
  return [...by].map(([provider, list]) => {
    const newest = list.reduce((m, a) => Math.max(m, Date.parse(a.sampled_at) || -Infinity), -Infinity);
    return { provider, accounts: sortForCard(list), sampledAt: Number.isFinite(newest) ? new Date(newest).toISOString() : "" };
  });
}

const PROVIDER_TITLES: Record<string, string> = { xai: "xAI", deepseek: "DeepSeek", kimi: "Kimi" };
/** A provider's name for a card title: its own spelling when known, else the id with a capital. */
export function providerTitle(provider: string): string {
  const id = canonicalUsageProvider(provider);
  return PROVIDER_TITLES[id] ?? id.charAt(0).toUpperCase() + id.slice(1);
}

const HOUR_MS = 3600_000;
/** The shared axis: now, then these hours from now. The first 24 hours take 45% of the width, 1 to 7 days the other 55%. */
export const TIMELINE_TICKS_HOURS = [6, 12, 24, 72, 120, 168] as const;
/** Where `ms` from now falls along the axis, 0-1 (clamped at 7 days). */
export function timelinePosition(ms: number): number {
  const h = Math.max(0, Math.min(ms, TIMELINE_CAP_MS)) / HOUR_MS;
  return h <= 24 ? (h / 24) * 0.45 : 0.45 + ((h - 24) / 144) * 0.55;
}

/** Time left as a short label: "45m", "1.5h", "2d 3h" (each language's units). */
export function remainingText(ms: number): string {
  const mins = Math.ceil(ms / 60_000);
  if (mins < 60) return tr("usage.minutes", { m: Math.max(1, mins) });
  const hours = ms / HOUR_MS;
  if (hours < 24) return tr("usage.hours", { h: Math.round(hours * 10) / 10 });
  const d = Math.floor(hours / 24);
  const h = Math.round(hours - d * 24);
  return h === 0 ? tr("usage.days", { d }) : tr("usage.days_hours", { d, h });
}
