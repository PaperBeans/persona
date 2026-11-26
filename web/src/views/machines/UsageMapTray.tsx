// The tray under the disk usage map (#14576, docs/server/machine-usage-map.md "The map"): the "what if"
// line (free after, the gain, used before → after), the selection as rows with a remove button each (the map's
// accessible, non-visual equivalent), the actions on it, the free-up goal slider, the trash notice with Undo and
// Delete Permanently, and an offload job's progress. Nothing here calls the hub; UsageMap owns the state.
import { t } from "../../i18n";
import { bytesBin, percent } from "../../lib/machine-detail";
import type { UsageNode, WhatIf } from "../../lib/usage-map";
import { Button, cx, IconButton } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { Slider } from "../../ui/Slider";

export interface TrayJob {
  title: string;
  detail: string;
  done: number;
  total: number;
  state: "running" | "done" | "failed" | "canceled" | string;
  onCancel?: () => void;
  onDismiss?: () => void;
}

export interface TrayNotice {
  text: string;
  detail?: string;
  items?: { key: string; text: string; tone: "ok" | "warn" }[];
  onUndo?: () => void;
  onPurge?: () => void;
  onDismiss: () => void;
  busy?: boolean;
}

export function UsageMapTray(p: {
  what: WhatIf | null;
  local: readonly UsageNode[];
  cloud: readonly UsageNode[];
  nameOf: (n: UsageNode) => string;
  trashable: number;
  offloadable: number;
  cleanable: number;
  restorable: number;
  hasProvider: boolean;
  onTrash: () => void;
  onOffload: () => void;
  onClean: () => void;
  onRestore: () => void;
  onClear: () => void;
  onRemove: (n: UsageNode) => void;
  goal: { max: number; value: number; step: number; onInput: (v: number) => void; onCommit: (v: number) => void } | null;
  job: TrayJob | null;
  notice: TrayNotice | null;
}) {
  const rows = [...p.local, ...p.cloud];
  const any = rows.length > 0;
  return (
    <div className="mt-3 space-y-3" data-testid="usage-tray">
      {p.notice && (
        <div role="status" className="rounded-control bg-field px-3 py-2 text-meta" data-testid="usage-notice">
          <div className="flex flex-wrap items-center gap-2">
            <Icon name="info" size={14} className="shrink-0 text-ink-3" />
            <span className="min-w-0 flex-1 text-ink">{p.notice.text}</span>
            {p.notice.onUndo && (
              <Button size="sm" tone="secondary" icon="undo" disabled={p.notice.busy} onClick={p.notice.onUndo} testId="usage-undo">
                {t("machines-usagemap.trash_undo")}
              </Button>
            )}
            {p.notice.onPurge && (
              <Button size="sm" tone="quiet" icon="trash" disabled={p.notice.busy} onClick={p.notice.onPurge} testId="usage-purge">
                {t("machines-usagemap.trash_purge")}
              </Button>
            )}
            <IconButton icon="x" label={t("machines-usagemap.tray_dismiss")} onClick={p.notice.onDismiss} size={26} iconSize={13} />
          </div>
          {p.notice.detail && <p className="mt-1 text-submeta text-ink-3">{p.notice.detail}</p>}
          {p.notice.items && p.notice.items.length > 0 && (
            <ul className="mt-1 space-y-0.5">
              {p.notice.items.map((i) => (
                <li key={i.key} className={cx("text-submeta", i.tone === "warn" ? "text-orange" : "text-ink-3")}>
                  {i.text}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}

      {p.job && (
        <div role="status" className="rounded-control bg-inset px-3 py-2 text-meta" data-testid="usage-job" data-state={p.job.state}>
          <div className="flex flex-wrap items-center gap-2">
            <span className="min-w-0 flex-1 text-ink">{p.job.title}</span>
            <span className="shrink-0 text-ink-3 tabular-nums">{p.job.detail}</span>
            {p.job.onCancel && (
              <Button size="sm" tone="quiet" onClick={p.job.onCancel} testId="usage-job-cancel">
                {t("machines-usagemap.offload_job_cancel")}
              </Button>
            )}
            {p.job.onDismiss && <IconButton icon="x" label={t("machines-usagemap.tray_dismiss")} onClick={p.job.onDismiss} size={26} iconSize={13} />}
          </div>
          {p.job.state === "running" && (
            <div
              className="mt-1.5 h-1 overflow-hidden rounded-full bg-field"
              role="progressbar"
              aria-label={p.job.title}
              aria-valuemin={0}
              aria-valuemax={100}
              aria-valuenow={p.job.total ? Math.round((100 * p.job.done) / p.job.total) : 0}
            >
              <div className="h-full rounded-full bg-accent transition-[width] duration-200" style={{ width: `${p.job.total ? Math.min(100, (100 * p.job.done) / p.job.total) : 0}%` }} />
            </div>
          )}
        </div>
      )}

      {any && (
        <div className="rounded-control bg-inset px-3 py-2.5" data-testid="usage-selection">
          {p.what && p.local.length > 0 && (
            <p className="text-meta font-medium text-ink tabular-nums" data-testid="usage-whatif">
              {t("machines-usagemap.tray_free_after", { free: bytesBin(p.what.freeAfter), gain: bytesBin(p.what.freed), before: percent(p.what.pctBefore), after: percent(p.what.pctAfter) })}
            </p>
          )}
          <ul className="mt-1.5 max-h-48 space-y-0.5 overflow-y-auto" aria-label={t("machines-usagemap.tray_list_label")} data-testid="usage-selection-rows">
            {rows.map((n) => (
              <li key={n.id} className="flex items-center gap-2 text-meta" data-row={n.path ?? n.id}>
                <Icon name={n.cloud ? "cloud" : n.kind === "file" ? "file" : "folder"} size={13} className="shrink-0 text-ink-3" />
                <span className="min-w-0 flex-1 truncate text-ink">{p.nameOf(n)}</span>
                <span className="shrink-0 text-ink-2 tabular-nums">{bytesBin(n.bytes)}</span>
                <IconButton icon="x" label={t("machines-usagemap.tray_remove", { name: p.nameOf(n) })} onClick={() => p.onRemove(n)} size={24} iconSize={12} />
              </li>
            ))}
          </ul>
          <div className="mt-2 flex flex-wrap items-center gap-2">
            {p.hasProvider && p.offloadable > 0 && (
              <Button tone="primary" icon="upload" onClick={p.onOffload} testId="usage-tray-offload">
                {t("machines-usagemap.tray_offload", { count: p.offloadable })}
              </Button>
            )}
            {p.trashable > 0 && (
              <Button tone="secondary" icon="trash" onClick={p.onTrash} testId="usage-tray-trash">
                {t("machines-usagemap.tray_trash", { count: p.trashable })}
              </Button>
            )}
            {p.cleanable > 0 && (
              <Button tone="secondary" icon="sparkles" onClick={p.onClean} testId="usage-tray-clean">
                {t("machines-usagemap.tray_clean", { count: p.cleanable })}
              </Button>
            )}
            {p.restorable > 0 && (
              <Button tone="secondary" icon="download" onClick={p.onRestore} testId="usage-tray-restore">
                {t("machines-usagemap.tray_restore", { count: p.restorable })}
              </Button>
            )}
            <Button tone="quiet" onClick={p.onClear} testId="usage-tray-clear">
              {t("machines-usagemap.tray_clear")}
            </Button>
          </div>
        </div>
      )}

      {p.goal && p.goal.max > 0 && (
        <div className="flex flex-wrap items-center gap-3" data-testid="usage-goal">
          <span className="shrink-0 text-meta text-ink-2 tabular-nums">{t("machines-usagemap.goal_label", { size: bytesBin(p.goal.value) })}</span>
          <Slider
            className="min-w-[160px] flex-1"
            value={p.goal.value}
            min={0}
            max={p.goal.max}
            step={p.goal.step}
            label={t("machines-usagemap.goal_a11y")}
            valueText={bytesBin(p.goal.value)}
            onInput={p.goal.onInput}
            onCommit={p.goal.onCommit}
          />
          <p className="w-full text-submeta text-ink-3">{t("machines-usagemap.goal_hint")}</p>
        </div>
      )}
    </div>
  );
}
