import { formatNumber, t } from "../../i18n";
import { bytes, duration } from "../../lib/format";
import type { HubDevelopmentProgress, HubUpdateStatus } from "../../lib/hub-update";
import { updateStateTitle, updateWorking } from "../../lib/hub-update-settings";
import { Button, cx, Spinner } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { Pill } from "./SettingsParts";

type DevelopmentPhase = HubDevelopmentProgress["phase"];
type Stage = DevelopmentPhase | "downloading" | "waiting_idle" | "restarting";
type NodeState = "done" | "active" | "pending";

const RELEASE_STAGES: readonly Stage[] = ["downloading", "waiting_idle", "restarting"];
const BUILD_STAGES: readonly Stage[] = ["building", "backing_up", "waiting_idle", "restarting"];
const RESTORE_STAGES: readonly Stage[] = ["backing_up", "applying_backup", "waiting_idle", "restarting"];
const stageLabels: Record<Stage, string> = {
  building: "settings-hubsettings.pipeline_build",
  backing_up: "settings-hubsettings.pipeline_backup",
  applying_backup: "settings-hubsettings.pipeline_prepare_backup",
  downloading: "settings-hubsettings.pipeline_download",
  waiting_idle: "settings-hubsettings.pipeline_wait",
  restarting: "settings-hubsettings.pipeline_restart",
};

const phaseSteps: Record<DevelopmentPhase, readonly string[]> = {
  building: ["fetch", "npm_install", "web_build", "runtime_packages", "go_build", "manifest"],
  backing_up: ["quiesce", "database_dump", "archive_files", "archive_programs", "verify_snapshot"],
  applying_backup: ["verify_snapshot", "prepare_artifacts"],
};

interface PipelineRun {
  restoringBackup: boolean;
  backupId?: string;
  target?: string;
  startedAt?: number;
  elapsedMs?: number;
}

function activeProgress(s: HubUpdateStatus): HubDevelopmentProgress | undefined {
  const p = s.development_progress;
  return p && p.phase === s.state && Object.hasOwn(phaseSteps, p.phase) ? p : undefined;
}

function phaseElapsedMs(p?: HubDevelopmentProgress): number | undefined {
  const seconds = p?.elapsed_seconds;
  return seconds !== undefined && Number.isFinite(seconds) && seconds >= 0 ? seconds * 1000 : undefined;
}

export function observeHubUpdate(previous: PipelineRun | undefined, s: HubUpdateStatus, now: number): PipelineRun | undefined {
  if (!updateWorking(s)) return undefined;
  const backupId = s.latest?.backup_id || previous?.backupId;
  const restoringBackup = Boolean(backupId || s.state === "applying_backup" || previous?.restoringBackup);
  // The wire drops the candidate across a restart. On a fresh connection during confirmation, the running build is the target.
  const runningTarget = s.source === "web-dev" ? s.development?.current_commit?.slice(0, 12) || s.version : s.version;
  const target = s.latest?.commit?.slice(0, 12) || s.latest?.version || previous?.target || (s.state === "confirming" ? runningTarget : undefined);
  const phaseElapsed = phaseElapsedMs(activeProgress(s));
  const firstPhase = s.state === "building" || s.state === "applying_backup";
  // Only the first phase supplies a starting point for measuring the whole run.
  const startedAt = previous?.startedAt ?? (firstPhase && phaseElapsed !== undefined ? now - phaseElapsed : undefined);
  return { restoringBackup, backupId, target, startedAt, elapsedMs: startedAt === undefined ? undefined : now - startedAt };
}

function macroStages(s: HubUpdateStatus, restoringBackup: boolean) {
  const development = s.source === "web-dev" || s.state === "building" || s.state === "backing_up";
  const stages = restoringBackup ? RESTORE_STAGES : development ? BUILD_STAGES : RELEASE_STAGES;
  const first = restoringBackup ? "applying_backup" : development ? "building" : "downloading";
  switch (s.state) {
    case "installing":
    case "restarting":
    case "confirming":
      return { stages, active: "restarting", done: stages.slice(0, -1) };
    case "backing_up":
      return { stages, active: s.state, done: [first, "waiting_idle"] };
    case "waiting_idle":
      // The Hub takes its backup after the idle wait. The strip keeps the same positions throughout the run.
      return { stages, active: s.state, done: [first] };
    default:
      // Development downloads stage the artifacts of Build or Prepare backup.
      return { stages, active: first, done: [] };
  }
}

function Marker({ state, spinning }: { state: NodeState; spinning?: boolean }) {
  return (
    <span aria-hidden className={cx("relative grid size-5 shrink-0 place-items-center rounded-full",
      state === "done" && "bg-green-tint text-green", state === "active" && "bg-accent-tint")}>
      {state === "done" ? <Icon name="check" size={12} stroke={2.5} />
        : state === "active" ? spinning ? <Spinner /> : <span className="size-2 rounded-full bg-accent" />
        : <span className="size-2.5 rounded-full border border-line-strong bg-inset" />}
    </span>
  );
}

function AnnouncedState({ state }: { state: NodeState }) {
  if (state === "active") return null;
  return <span className="sr-only"> · {t(state === "done" ? "settings-hubsettings.pipeline_done" : "settings-hubsettings.pipeline_pending")}</span>;
}

function StageStrip({ s, restoringBackup }: { s: HubUpdateStatus; restoringBackup: boolean }) {
  const { stages, active, done } = macroStages(s, restoringBackup);
  return (
    <ol role="list" aria-label={t("settings-hubsettings.pipeline_stages")} className="flex">
      {stages.map((stage, i) => {
        const state = stage === active ? "active" : done.includes(stage) ? "done" : "pending";
        return (
          <li key={stage} data-stage={stage} data-state={state} aria-current={state === "active" ? "step" : undefined}
            className="flex min-w-0 flex-1 flex-col items-center gap-1.5 text-center">
            <div aria-hidden className="flex w-full items-center">
              <span className={cx("h-px flex-1", i > 0 && (done.includes(stages[i - 1]) ? "bg-green/60" : "bg-line-strong"))} />
              <Marker state={state} />
              <span className={cx("h-px flex-1", i < stages.length - 1 && (state === "done" ? "bg-green/60" : "bg-line-strong"))} />
            </div>
            <span className={cx("max-w-full px-0.5 text-label leading-tight break-words",
              state === "active" ? "font-semibold text-accent-ink" : state === "done" ? "text-ink-2" : "text-ink-3")}>
              {t(stageLabels[stage])}<AnnouncedState state={state} />
            </span>
          </li>
        );
      })}
    </ol>
  );
}

function stepLabel(step: string): string {
  const key = `settings-hubsettings.progress_step_${step}`;
  const localized = t(key);
  return localized === key ? step.replaceAll("_", " ").trim() || t("settings-hubsettings.pipeline_unknown_step") : localized;
}

function PhaseSteps({ p, builtinQuiesce }: { p: HubDevelopmentProgress; builtinQuiesce?: boolean }) {
  const steps = [...phaseSteps[p.phase]];
  if (p.phase === "backing_up" && (p.step_total >= 6 || p.step === "resume_writers" || builtinQuiesce === false)) {
    steps.splice(steps.indexOf("verify_snapshot"), 0, "resume_writers");
  }
  let active = steps.indexOf(p.step);
  if (active < 0) {
    // Keep all known steps and give a new step its own node so its telemetry remains visible.
    active = p.step_index > 0 && Number.isInteger(p.step_index) ? Math.min(p.step_index - 1, steps.length) : steps.length;
    steps.splice(active, 0, p.step ?? "");
  }
  return (
    <ol role="list" aria-label={t("settings-hubsettings.pipeline_steps")} className="mt-2.5">
      {steps.map((step, i) => {
        const state = i < active ? "done" : i === active ? "active" : "pending";
        const label = stepLabel(step);
        return (
          <li key={step} data-step={step} data-state={state} aria-current={state === "active" ? "step" : undefined}
            className="relative flex min-w-0 gap-2.5 pb-2 last:pb-0">
            {i < steps.length - 1 && <span aria-hidden className="absolute top-5 bottom-0 left-2.5 w-px bg-line-strong" />}
            <Marker state={state} spinning />
            <div className="min-w-0 flex-1 pt-0.5">
              {state === "active" ? <ActiveStep p={p} label={label} /> : (
                <span className={cx("block text-label leading-snug break-words", state === "done" ? "text-ink-2" : "text-ink-3")}>
                  {label}<AnnouncedState state={state} />
                </span>
              )}
            </div>
          </li>
        );
      })}
    </ol>
  );
}

function ActiveStep({ p, label }: { p: HubDevelopmentProgress; label: string }) {
  const percent = p.percent !== undefined && Number.isFinite(p.percent) ? Math.min(100, Math.max(0, p.percent)) : undefined;
  const size = p.bytes_done === undefined ? undefined :
    p.bytes_total === undefined ? bytes(p.bytes_done) : `${bytes(p.bytes_done)} / ${bytes(p.bytes_total)}`;
  const speed = p.bytes_per_second === undefined ? undefined : t("settings-hubsettings.progress_speed", {
    speed: formatNumber(p.bytes_per_second / (1024 * 1024), { maximumFractionDigits: 2 }),
  });
  const countKey = p.step === "archive_files" ? "settings-hubsettings.progress_files" :
    p.step === "database_dump" ? "settings-hubsettings.progress_databases" : "settings-hubsettings.progress_items";
  const items = p.item_done === undefined || p.item_total === undefined ? undefined :
    t(countKey, { done: formatNumber(p.item_done), total: formatNumber(p.item_total) });
  const detail = [p.item_label, size, speed, items].filter(Boolean).join(" · ");
  return (
    <div className="space-y-1.5">
      <div className="flex items-baseline justify-between gap-2 text-label leading-snug text-accent-ink">
        <span className="min-w-0 font-semibold break-words">{label}</span>
        {percent !== undefined && <span className="shrink-0 tabular-nums">{formatNumber(percent / 100, { style: "percent" })}</span>}
      </div>
      {percent !== undefined && (
        <div role="progressbar" aria-label={label} aria-valuenow={percent} aria-valuemin={0} aria-valuemax={100}
          className="h-1 overflow-hidden rounded-full bg-line-strong">
          <div className="h-full rounded-full bg-accent" style={{ width: `${percent}%` }} />
        </div>
      )}
      {detail && <p className="text-submeta leading-snug break-words text-ink-2 tabular-nums">{detail}</p>}
    </div>
  );
}

export function HubUpdatePipeline({ s, run, onNow }: { s: HubUpdateStatus; run: PipelineRun; onNow: () => void }) {
  const progress = activeProgress(s);
  const title = updateStateTitle(s);
  const phaseElapsed = phaseElapsedMs(progress);
  const elapsed = run.elapsedMs !== undefined ? t("settings-hubsettings.progress_elapsed", { time: duration(run.elapsedMs) }) :
    phaseElapsed !== undefined ? t("settings-hubsettings.progress_phase_elapsed", { time: duration(phaseElapsed) }) : undefined;
  return (
    <section aria-label={t("settings-hubsettings.pipeline_title")} className="flex min-w-0 flex-col gap-3 px-4 py-3">
      <header className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1.5">
        <h3 className="text-meta font-semibold text-ink">{t("settings-hubsettings.pipeline_title")}</h3>
        {(run.target || elapsed) && <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
          {run.target && <Pill wrap><span className="min-w-0 truncate tabular-nums" title={run.target}>{t("settings-hubsettings.pipeline_target", { target: run.target })}</span></Pill>}
          {elapsed && <span className="text-submeta text-ink-3 tabular-nums">{elapsed}</span>}
        </div>}
      </header>
      <StageStrip s={s} restoringBackup={run.restoringBackup} />
      <div className="rounded-control bg-inset p-3">
        <div className="flex flex-wrap items-baseline justify-between gap-x-2 gap-y-1 text-label text-ink-2">
          <div role="status" className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
            <span className="flex items-center gap-2 font-medium">
              {!progress && <Spinner />}
              {title}{progress && <span className="sr-only"> · {stepLabel(progress.step ?? "")}</span>}
            </span>
            {s.state === "downloading" && s.progress && <span className="min-w-0 break-words text-ink-3 tabular-nums">{s.progress}</span>}
          </div>
          {progress && progress.step_index > 0 && progress.step_total > 0 && (
            <span className="text-ink-3 tabular-nums">{formatNumber(progress.step_index)}/{formatNumber(progress.step_total)}</span>
          )}
        </div>
        {progress && <PhaseSteps p={progress} builtinQuiesce={s.development?.builtin_quiesce} />}
        {s.state === "waiting_idle" && s.busy.length > 0 && (
          <>
            <ul role="list" className="mt-2 divide-y divide-line border-y border-line">
              {s.busy.map(b => (
                <li key={`${b.machine_id}/${b.session_id ?? b.title}`} className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-0.5 py-1.5 text-label">
                  <span className="min-w-0 break-words text-ink">{b.title}</span>
                  <span className="min-w-0 break-words text-ink-3">{b.machine}</span>
                </li>
              ))}
            </ul>
            <div className="mt-2 flex justify-end">
              <Button tone="quiet" onClick={onNow} className="text-red enabled:hover:text-red pointer-coarse:min-h-11">{t("settings-hubsettings.update_now_ellipsis")}</Button>
            </div>
          </>
        )}
      </div>
    </section>
  );
}
