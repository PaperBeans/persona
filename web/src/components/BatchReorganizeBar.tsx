import { useEffect, useMemo, useState } from "react";
import { t } from "../i18n";
import {
  buildReorganizeRequest,
  canTransferSupervision,
  eligibleParentTargets,
  type ReorganizeDirectionOption,
  type ReorganizeTarget,
  type TodoReorganizeRequest,
} from "../lib/todo-reorganize";
import type { Todo } from "../lib/todos";
import { Button, cx } from "../ui/atoms";

const KEEP_VALUE = "__keep__";
const CLEAR_VALUE = "__clear__";

function targetToValue(target: ReorganizeTarget): string {
  if (target.mode === "keep") return KEEP_VALUE;
  if (target.mode === "clear") return CLEAR_VALUE;
  return target.id;
}

function valueToTarget(value: string): ReorganizeTarget {
  if (value === KEEP_VALUE) return { mode: "keep" };
  if (value === CLEAR_VALUE) return { mode: "clear" };
  return { mode: "set", id: value };
}

/**
 * Compact batch reorganization toolbar shown at the top of the Web To-dos list
 * when `todos_v2 === true` and organize mode is active (#14758).
 */
export function BatchReorganizeBar({
  todos,
  visibleIds,
  selectedIds,
  directions,
  submitting,
  error,
  feedback,
  onSelectAll,
  onClearSelection,
  onApply,
  onExit,
}: {
  todos: readonly Todo[];
  visibleIds: readonly string[];
  selectedIds: ReadonlySet<string>;
  directions: readonly ReorganizeDirectionOption[];
  submitting: boolean;
  error: string | null;
  feedback: string | null;
  onSelectAll: () => void;
  onClearSelection: () => void;
  onApply: (req: TodoReorganizeRequest) => Promise<boolean>;
  onExit: () => void;
}) {
  const [partOf, setPartOf] = useState<ReorganizeTarget>({ mode: "keep" });
  const [direction, setDirection] = useState<ReorganizeTarget>({ mode: "keep" });
  const [includeChildren, setIncludeChildren] = useState(false);
  const [transferSupervision, setTransferSupervision] = useState(false);

  const eligibleParents = useMemo(
    () => eligibleParentTargets(todos, selectedIds),
    [todos, selectedIds],
  );
  const eligibleParentIds = useMemo(
    () => new Set(eligibleParents.map((td) => td.id)),
    [eligibleParents],
  );
  const activeDirectionIds = useMemo(
    () => new Set(directions.filter((d) => d.active).map((d) => d.id)),
    [directions],
  );

  // If the currently chosen parent target becomes ineligible (selected or descendant of selected), reset to keep.
  useEffect(() => {
    if (partOf.mode === "set" && !eligibleParentIds.has(partOf.id)) {
      setPartOf({ mode: "keep" });
    }
  }, [partOf, eligibleParentIds]);

  const transferAllowed = canTransferSupervision(direction, activeDirectionIds);
  useEffect(() => {
    if (!transferAllowed && transferSupervision) {
      setTransferSupervision(false);
    }
  }, [transferAllowed, transferSupervision]);

  const request = useMemo(
    () =>
      buildReorganizeRequest({
        ids: [...selectedIds],
        partOf,
        direction,
        includeChildren,
        transferSupervision,
        activeDirectionIds,
      }),
    [selectedIds, partOf, direction, includeChildren, transferSupervision, activeDirectionIds],
  );

  const allVisibleSelected =
    visibleIds.length > 0 && visibleIds.every((id) => selectedIds.has(id));

  const handleApply = async () => {
    if (!request || submitting) return;
    const ok = await onApply(request);
    if (ok) {
      setPartOf({ mode: "keep" });
      setDirection({ mode: "keep" });
      setIncludeChildren(false);
      setTransferSupervision(false);
    }
  };

  return (
    <section
      aria-label={t("todos.reorganize_region")}
      data-testid="batch-reorganize-bar"
      className="px-3 pb-2"
    >
      <div className="rounded-control border border-line bg-elevated/60 px-2.5 py-2">
        {/* Top row: selection count + quick selection actions + exit */}
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex min-w-0 items-center gap-2 text-xs">
            <span
              data-testid="reorganize-selected-count"
              className="font-semibold text-ink tabular-nums"
            >
              {t("todos.reorganize_selected_count", { count: selectedIds.size })}
            </span>
            <button
              type="button"
              data-testid="reorganize-select-all"
              disabled={visibleIds.length === 0 || allVisibleSelected}
              onClick={onSelectAll}
              className="touch-hit rounded-sm px-1 text-xs font-medium whitespace-nowrap text-accent transition-colors enabled:hover:underline disabled:opacity-40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40"
            >
              {t("todos.reorganize_select_all")}
            </button>
            <button
              type="button"
              data-testid="reorganize-clear-selection"
              disabled={selectedIds.size === 0}
              onClick={onClearSelection}
              className="touch-hit rounded-sm px-1 text-xs font-medium whitespace-nowrap text-ink-3 transition-colors enabled:hover:text-ink disabled:opacity-40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40"
            >
              {t("todos.reorganize_clear_selection")}
            </button>
          </div>

          <button
            type="button"
            data-testid="reorganize-exit"
            onClick={onExit}
            className="touch-hit shrink-0 rounded-sm px-1 text-xs font-medium whitespace-nowrap text-ink-3 transition-colors hover:text-ink focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40"
          >
            {t("todos.reorganize_exit")}
          </button>
        </div>

        {/* Target selectors: part_of & direction_id */}
        <div className="mt-2 grid grid-cols-1 gap-1.5 sm:grid-cols-2">
          <select
            data-testid="reorganize-part-of"
            aria-label={t("todos.reorganize_part_of_label")}
            value={targetToValue(partOf)}
            disabled={submitting}
            onChange={(e) => setPartOf(valueToTarget(e.target.value))}
            className="h-8 w-full min-w-0 rounded-control border border-line bg-surface px-2 text-xs text-ink focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40 disabled:opacity-50"
          >
            <option value={KEEP_VALUE}>{t("todos.reorganize_part_of_keep")}</option>
            <option value={CLEAR_VALUE}>{t("todos.reorganize_part_of_clear")}</option>
            {eligibleParents.map((td) => (
              <option key={td.id} value={td.id}>
                {td.title}
              </option>
            ))}
          </select>

          <select
            data-testid="reorganize-direction"
            aria-label={t("todos.reorganize_direction_label")}
            value={targetToValue(direction)}
            disabled={submitting}
            onChange={(e) => setDirection(valueToTarget(e.target.value))}
            className="h-8 w-full min-w-0 rounded-control border border-line bg-surface px-2 text-xs text-ink focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40 disabled:opacity-50"
          >
            <option value={KEEP_VALUE}>{t("todos.reorganize_direction_keep")}</option>
            <option value={CLEAR_VALUE}>{t("todos.reorganize_direction_clear")}</option>
            {directions.map((d) => (
              <option key={d.id} value={d.id}>
                {d.label}
              </option>
            ))}
          </select>
        </div>

        {/* Options & Apply */}
        <div className="mt-2 flex flex-wrap items-center justify-between gap-2">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-ink-2">
            <label className="inline-flex cursor-pointer items-center gap-1.5 select-none">
              <input
                type="checkbox"
                data-testid="reorganize-include-children"
                checked={includeChildren}
                disabled={submitting}
                onChange={(e) => setIncludeChildren(e.target.checked)}
                className="size-3.5 accent-accent"
              />
              <span>{t("todos.reorganize_include_children")}</span>
            </label>

            <label
              className={cx(
                "inline-flex items-center gap-1.5 select-none",
                transferAllowed ? "cursor-pointer text-ink-2" : "cursor-not-allowed text-ink-3 opacity-60",
              )}
            >
              <input
                type="checkbox"
                data-testid="reorganize-transfer-supervision"
                checked={transferAllowed && transferSupervision}
                disabled={submitting || !transferAllowed}
                onChange={(e) => setTransferSupervision(e.target.checked)}
                className="size-3.5 accent-accent"
              />
              <span>{t("todos.reorganize_transfer_supervision")}</span>
            </label>
          </div>

          <Button
            tone="accent"
            size="sm"
            busy={submitting}
            disabled={!request || submitting}
            onClick={() => void handleApply()}
            className="px-2.5 text-xs"
            testId="reorganize-apply"
          >
            {t("todos.reorganize_apply")}
          </Button>
        </div>

        {error && (
          <p role="alert" data-testid="reorganize-error" className="mt-1.5 text-xs text-red">
            {error}
          </p>
        )}
        {!error && feedback && (
          <p role="status" data-testid="reorganize-feedback" className="mt-1.5 text-xs text-green">
            {feedback}
          </p>
        )}
      </div>
    </section>
  );
}
