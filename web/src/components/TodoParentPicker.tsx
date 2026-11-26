import { useMemo, useState } from "react";
import { t } from "../i18n";
import {
  buildReorganizeRequest,
  eligibleParentTargets,
  type ReorganizeTarget,
  type TodoReorganizeRequest,
} from "../lib/todo-reorganize";
import type { Todo } from "../lib/todos";
import { Button } from "../ui/atoms";

function valueToParentTarget(value: string): ReorganizeTarget {
  const trimmed = value.trim();
  if (!trimmed) return { mode: "clear" };
  return { mode: "set", id: trimmed };
}

/**
 * Compact single-item parent goal (`part_of`) picker panel rendered inside `TodoDetail`
 * strictly when `todos_v2 === true` (#16100).
 *
 * Calls `POST /api/v1/todos/reorganize` with `part_of` only (`transfer_supervision: false`)
 * so running Agents are never interrupted.
 */
export function TodoParentPicker({
  todoId,
  currentParentId,
  currentParentTitle,
  todos,
  onApply,
  onClose,
}: {
  todoId: string;
  currentParentId?: string;
  currentParentTitle?: string;
  todos: readonly Todo[];
  onApply: (req: TodoReorganizeRequest) => Promise<void>;
  onClose: () => void;
}) {
  const normalizedCurrent = currentParentId?.trim() ?? "";
  const [selectedValue, setSelectedValue] = useState(normalizedCurrent);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const options = useMemo(() => {
    const eligible = eligibleParentTargets(todos, [todoId]).filter(
      (td) => !td.done_at && !td.part_of?.trim(),
    );
    const list = eligible.map((td) => ({ id: td.id, title: td.title }));
    if (normalizedCurrent && !list.some((item) => item.id === normalizedCurrent)) {
      list.push({
        id: normalizedCurrent,
        title: currentParentTitle?.trim() || normalizedCurrent,
      });
    }
    return list;
  }, [todos, todoId, normalizedCurrent, currentParentTitle]);

  const changed = selectedValue.trim() !== normalizedCurrent;
  const request = useMemo(() => {
    if (!changed) return null;
    return buildReorganizeRequest({
      ids: [todoId],
      partOf: valueToParentTarget(selectedValue),
      direction: { mode: "keep" },
      includeChildren: false,
      transferSupervision: false,
    });
  }, [changed, todoId, selectedValue]);

  const promoteRequest = useMemo(() => {
    if (!normalizedCurrent) return null;
    return buildReorganizeRequest({
      ids: [todoId],
      partOf: { mode: "clear" },
      direction: { mode: "keep" },
      includeChildren: false,
      transferSupervision: false,
    });
  }, [normalizedCurrent, todoId]);

  const submitRequest = async (req: TodoReorganizeRequest | null) => {
    if (!req || submitting) return;
    setSubmitting(true);
    setError(null);
    try {
      await onApply(req);
      onClose();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <section
      aria-label={t("todos.detail_change_parent_region")}
      data-testid="todo-detail-parent-panel"
      className="mb-3 rounded-control border border-line bg-surface p-3"
    >
      <div className="flex flex-col gap-2">
        <label className="flex flex-col gap-1">
          <span className="text-label font-medium text-ink-2">{t("todos.reorganize_part_of_label")}</span>
          <select
            data-testid="todo-detail-parent-select"
            aria-label={t("todos.reorganize_part_of_label")}
            value={selectedValue}
            disabled={submitting}
            onChange={(e) => {
              setSelectedValue(e.target.value);
              setError(null);
            }}
            className="h-8 w-full min-w-0 rounded-control border border-line bg-inset px-2 text-xs text-ink focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40 disabled:opacity-50"
          >
            <option value="">{t("todos.detail_parent_standalone")}</option>
            {options.map((p) => (
              <option key={p.id} value={p.id}>
                {p.title}
              </option>
            ))}
          </select>
        </label>

        <div className="flex flex-wrap items-center justify-between gap-2 pt-0.5">
          <span className="text-submeta text-ink-3">{t("todos.detail_change_parent_hint")}</span>
          <div className="flex flex-wrap items-center gap-1.5">
            {promoteRequest && (
              <Button
                tone="quiet"
                size="sm"
                disabled={submitting}
                onClick={() => void submitRequest(promoteRequest)}
                testId="todo-detail-parent-promote"
                className="whitespace-nowrap text-xs"
              >
                {t("todos.detail_promote_top_level")}
              </Button>
            )}
            <Button
              tone="quiet"
              size="sm"
              disabled={submitting}
              onClick={onClose}
              testId="todo-detail-parent-cancel"
              className="whitespace-nowrap text-xs"
            >
              {t("common.cancel")}
            </Button>
            <Button
              tone="accent"
              size="sm"
              busy={submitting}
              disabled={!request || submitting}
              onClick={() => void submitRequest(request)}
              testId="todo-detail-parent-apply"
              className="whitespace-nowrap text-xs"
            >
              {t("todos.reorganize_apply")}
            </Button>
          </div>
        </div>

        {error && (
          <p role="alert" data-testid="todo-detail-parent-error" className="text-xs text-red">
            {error}
          </p>
        )}
      </div>
    </section>
  );
}
