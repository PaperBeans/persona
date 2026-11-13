// The goal an Agent works toward (docs/integrations/agent-goal.md, iOS GoalBar / GoalSheet), over the composer as cindy's
// goal indicator (#5421, `GoalIndicator`): one line, the goal mark, its status, the objective and what it has spent; red
// while it is held up (blocked, out of usage or budget). A click shows all of it and the latest check's reason.
import { useState } from "react";
import { t } from "../../i18n";
import { goalHeadline, goalStatusLabel, goalUsage } from "../../lib/goal";
import type { GoalInfo } from "../../lib/types";
import { cx, Dialog } from "../../ui/atoms";
import { Icon } from "../../ui/icons";

/** The statuses that need the owner (cindy's attention statuses): drawn red. */
const HELD = new Set(["blocked", "usage_limited", "budget_limited"]);

export function GoalBar({ goal }: { goal: GoalInfo }) {
  const [open, setOpen] = useState(false);
  const status = goalStatusLabel(goal);
  const headline = goalHeadline(goal);
  const usage = goalUsage(goal);
  const held = HELD.has(goal.status);
  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        aria-label={t("agent-goal.chip_label", { status, headline })}
        title={t("agent-goal.chip_title", { status, headline })}
        data-goal-bar={goal.status}
        className={cx(
          "flex w-full min-w-0 shrink-0 items-center gap-2 rounded-[8px] border bg-field px-2.5 py-1.5 text-left text-[12px] transition-colors hover:bg-hover",
          held ? "border-red/40" : "border-line",
        )}
      >
        <Icon name="target" size={13} stroke={2} className={cx("shrink-0", held ? "text-red" : "text-ink-2")} />
        <span className={cx("shrink-0 font-medium whitespace-nowrap", held ? "text-red" : "text-ink-2")}>{status}</span>
        <span className="min-w-0 flex-1 truncate text-ink">{headline}</span>
        {usage && <span className="shrink-0 whitespace-nowrap text-ink-3 tabular-nums max-sm:hidden">{usage}</span>}
      </button>
      <Dialog open={open} onClose={() => setOpen(false)} title={t("agent-goal.title")} width={520}>
        <div className="flex flex-col gap-3">
          <div className={cx("flex items-center gap-1.5 text-[12.5px] font-semibold", held ? "text-red" : "text-ink-2")}>
            <Icon name="target" size={14} stroke={2.2} className="shrink-0" />
            <span className="whitespace-nowrap">{status}</span>
            {usage && <span className="truncate font-normal text-ink-3">{usage}</span>}
          </div>
          <p className="text-[13.5px] leading-relaxed whitespace-pre-wrap text-ink select-text">{goal.objective}</p>
          {goal.reason && (
            <div className="flex flex-col gap-1 rounded-card bg-stripe-bg px-3.5 py-3 shadow-hairline">
              <span className="text-[11.5px] font-semibold text-ink-3">{t(goal.status === "complete" ? "agent-goal.reason_met" : "agent-goal.reason_check")}</span>
              <p className="text-[12.5px] leading-relaxed whitespace-pre-wrap text-ink-2 select-text">{goal.reason}</p>
            </div>
          )}
          <p className="text-[12px] text-ink-3">{t("agent-goal.hint")}</p>
        </div>
      </Dialog>
    </>
  );
}
