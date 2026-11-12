// The agent's goal (docs/integrations/agent-goal.md) in words: its status, its first line and what it has spent (iOS GoalInfo).
import { formatNumber, t } from "../i18n";
import type { GoalInfo } from "./types";

const known = new Set(["active", "complete", "paused", "blocked", "usage_limited", "budget_limited"]);

export function goalStatusLabel(g: GoalInfo): string {
  return known.has(g.status) ? t(`agent-goal.status_${g.status}`) : g.status;
}

/** The objective's first line, for the goal bar over the composer. */
export function goalHeadline(g: GoalInfo): string {
  return (g.objective.split("\n").find((l) => l.trim()) ?? g.objective).trim();
}

const compact = (n: number) => formatNumber(n, { notation: "compact", maximumFractionDigits: 1 });

/** What it has spent ("182K / 500K tokens · 42 min", with Chinese units), "" when nothing is known. */
export function goalUsage(g: GoalInfo): string {
  const parts: string[] = [];
  if (g.tokens_used && g.tokens_used > 0) {
    const used = compact(g.tokens_used);
    parts.push(g.token_budget && g.token_budget > 0 ? t("agent-goal.tokens_budget", { used, budget: compact(g.token_budget) }) : t("agent-goal.tokens", { used }));
  }
  const s = g.seconds ?? 0;
  if (s > 0) {
    parts.push(s < 3600 ? t("agent-goal.minutes", { minutes: Math.max(Math.floor(s / 60), 1) })
      : t("agent-goal.hours", { hours: Math.floor(s / 3600), minutes: Math.floor((s % 3600) / 60) }));
  }
  return parts.join(" · ");
}
