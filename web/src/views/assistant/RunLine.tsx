// One run in a task's history: shared by Routines' run list and TaskEditor's history so Routines keeps TaskEditor
// off its initial chunk until the user opens the editor dialog.
import { t } from "../../i18n";
import { momentLabel, parseTime, runDetail, runStatusWord, type Run } from "../../lib/routines";
import { cx } from "../../ui/atoms";
import { Icon } from "../../ui/icons";

/** One run in a task's history: when, how it went. */
export function RunLine({ run, now, onOpen, showTitle }: { run: Run; now: Date; onOpen?: () => void; showTitle?: boolean }) {
  const at = parseTime(run.at);
  const failed = run.status !== "ok" && run.status !== "skipped";
  const body = (
    <>
      <RunMark status={run.status} />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-meta text-ink">{showTitle ? run.title : at ? momentLabel(at, now) : runStatusWord(run)}</span>
        <span className={cx("block truncate text-submeta", failed ? "text-red" : "text-ink-3")}>
          {showTitle && at ? `${String(at.getHours()).padStart(2, "0")}:${String(at.getMinutes()).padStart(2, "0")} · ` : ""}
          {runDetail(run)}
        </span>
      </span>
      {onOpen && <Icon name="chevronRight" size={12} stroke={2} className="shrink-0 text-ink-3" />}
    </>
  );
  return <div>{onOpen ? (
    <button type="button" onClick={onOpen} title={t("assistant-taskeditor.open_run")} className="flex w-full items-center gap-2.5 px-3.5 py-2 text-left transition-colors hover:bg-hover">
      {body}
    </button>
  ) : (
    <div className="flex items-center gap-2.5 px-3.5 py-2">{body}</div>
  )}
    {run.execution && <details className="px-3.5 pb-2 text-submeta text-ink-3">
      <summary className="cursor-pointer">{t("assistant-taskeditor.execution_used")}</summary>
      {run.conversation && <p>{t(`assistant-taskeditor.conversation_${run.conversation}`)}{run.conversation_reason ? ` · ${t("assistant-taskeditor.conversation_changed")}` : ""}</p>}
      <p className="break-all">{[run.execution.agent, run.execution.launcher, run.execution.model || t("settings-modelconfiguration.provider_default"), run.execution.effort, run.execution.machine_id, run.execution.cwd, run.execution.permission_mode].filter(Boolean).join(" · ")}</p>
    </details>}
  </div>;
}

export function RunMark({ status }: { status: string }) {
  if (status === "ok")
    return (
      <span aria-label={t("assistant-taskeditor.status_ok")} role="img" className="flex size-[18px] shrink-0 items-center justify-center rounded-full bg-green-tint text-green">
        <Icon name="check" size={11} stroke={3} />
      </span>
    );
  if (status === "skipped") return <span aria-label={t("assistant-taskeditor.status_skipped")} role="img" className="size-[18px] shrink-0 rounded-full border-[1.5px] border-line-strong" />;
  return (
    <span aria-label={t("assistant-taskeditor.status_failed")} role="img" className="flex size-[18px] shrink-0 items-center justify-center rounded-full bg-field text-red">
      <Icon name="x" size={11} stroke={3} />
    </span>
  );
}
