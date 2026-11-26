// A short configuration step before the existing TaskEditor. Reviewing hands over a Task; only that editor's Add/Run
// action can create or execute anything.
import { useEffect, useState } from "react";
import { api } from "../../lib/api";
import {
  defaultScenarioDraft,
  instantiateScenario,
  scenarioProblem,
  scenarioSources,
  withScenarioRepeat,
  withScenarioSource,
  type ScenarioCatalog,
  type ScenarioDefinition,
  type ScenarioDraft,
  type ScenarioSourceKey,
} from "../../lib/scenario-routines";
import { MAX_EVERY, MIN_EVERY, WORKDAYS, type Task } from "../../lib/routines";
import { t } from "../../i18n";
import { Button, Dialog } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { Field, inputClass, SectionTitle } from "./parts";

const HINT = "mt-1.5 px-1 text-submeta leading-relaxed text-ink-3";

export function ScenarioTemplateDialog({ template, readOptions, onClose, onReview }: {
  template: ScenarioDefinition;
  readOptions?: Parameters<typeof api>[1];
  onClose: () => void;
  onReview: (task: Task) => void;
}) {
  const [draft, setDraft] = useState<ScenarioDraft>(() => defaultScenarioDraft(template.key));
  const [catalogs, setCatalogs] = useState<readonly ScenarioCatalog[] | null>(null);
  const [catalogFailed, setCatalogFailed] = useState(false);
  const [edited, setEdited] = useState(false);

  useEffect(() => {
    let live = true;
    api<{ catalogs?: ScenarioCatalog[] }>("/subscriptions/catalog", readOptions).then((reply) => {
      if (live) setCatalogs(reply.catalogs ?? []);
    }, () => {
      if (live) {
        setCatalogs([]);
        setCatalogFailed(true);
      }
    });
    return () => { live = false; };
  }, [readOptions]);

  useEffect(() => {
    if (catalogs === null || edited) return;
    // An untouched PR draft moves to GitHub only when its read-only tools are confirmed. A user choice made while the
    // catalog was loading always wins; without the capability the safe conversation fallback stays selected.
    const preferred = defaultScenarioDraft(template.key, catalogs);
    setDraft((current) => current.source === "conversation" && preferred.source === "github" ? preferred : current);
  }, [catalogs, edited, template.key]);

  const options = scenarioSources(template.key, catalogs);
  const selected = options.find((source) => source.key === draft.source);
  const fallbackNotice = template.key === "pr-review" && draft.source === "conversation"
    ? options.find((source) => source.key === "github")?.note
    : undefined;
  const problem = scenarioProblem(draft, catalogs);
  const edit = (next: Partial<ScenarioDraft>) => {
    setEdited(true);
    setDraft((current) => ({ ...current, ...next }));
  };
  const chooseSource = (source: ScenarioSourceKey) => {
    setEdited(true);
    setDraft((current) => withScenarioSource(current, source));
  };
  const chooseRepeat = (repeat: ScenarioDraft["repeat"]) => {
    setEdited(true);
    setDraft((current) => withScenarioRepeat(current, repeat));
  };
  const review = () => {
    const task = instantiateScenario(draft, catalogs);
    if (task) onReview(task);
  };

  const footer = <>
    <span className="mr-auto min-w-0 truncate text-submeta text-red">{edited && problem ? problem : ""}</span>
    <Button tone="quiet" onClick={onClose}>{t("common.cancel")}</Button>
    <Button tone="primary" disabled={!!problem} onClick={review}>{t("assistant-scenario-templates.review")}</Button>
  </>;

  return <Dialog open onClose={onClose} width={560} title={t(`assistant-scenario-templates.${template.key}_title`)} footer={footer}>
    <div className="flex flex-col gap-5">
      <p className="text-meta leading-relaxed text-ink-2">{t(`assistant-scenario-templates.${template.key}_description`)}</p>
      <div className="rounded-control bg-inset px-3 py-2 text-label leading-relaxed text-ink-2 shadow-hairline">
        {t("assistant-scenario-templates.safe_default")}
      </div>

      <section>
        <SectionTitle>{t("assistant-scenario-templates.section_source")}</SectionTitle>
        <div className="flex flex-col gap-3">
          <Field label={t("assistant-scenario-templates.source_label")}>
            <select className={inputClass} value={draft.source} onChange={(event) => chooseSource(event.target.value as ScenarioSourceKey)}>
              {options.map((source) => <option key={source.key} value={source.key}>
                {source.label}{source.available ? "" : ` · ${t("assistant-scenario-templates.unavailable")}`}
              </option>)}
            </select>
          </Field>
          <Field
            label={t("assistant-scenario-templates.scope_label")}
            note={draft.key === "pr-review" && draft.source === "github"
              ? t("assistant-scenario-templates.repository_hint")
              : t("assistant-scenario-templates.scope_hint")}
            error={edited && !!problem && (!draft.scope.trim() || draft.scope.trim().length > 500 || (draft.key === "pr-review" && draft.source === "github"))}
          >
            <input
              className={inputClass}
              value={draft.scope}
              maxLength={500}
              placeholder={draft.key === "pr-review" && draft.source === "github" ? t("assistant-scenario-templates.repository_placeholder") : undefined}
              onChange={(event) => edit({ scope: event.target.value })}
            />
          </Field>
          {catalogFailed && <p role="alert" className="text-label leading-relaxed text-orange">{t("assistant-scenario-templates.capability_check_failed")}</p>}
          {!selected?.note && fallbackNotice && <p role="status" className="rounded-control bg-inset px-3 py-2 text-label leading-relaxed text-ink-2 shadow-hairline">{fallbackNotice}</p>}
          {selected?.note && <div role="alert" className="rounded-control bg-inset px-3 py-2 shadow-hairline">
            <p className="text-label leading-relaxed text-ink-2">{selected.note}</p>
            {selected.fallback && <Button tone="quiet" className="mt-1 px-0 text-accent-ink" onClick={() => chooseSource(selected.fallback!)}>
              {t("assistant-scenario-templates.use_fallback")}
            </Button>}
          </div>}
        </div>
      </section>

      <section>
        <SectionTitle>{t("assistant-scenario-templates.section_schedule")}</SectionTitle>
        <div className="grid grid-cols-1 gap-3 rounded-card bg-inset p-3 shadow-hairline sm:grid-cols-2">
          <Field label={t("assistant-scenario-templates.schedule_kind")}>
            <select className={inputClass} value={draft.repeat} onChange={(event) => chooseRepeat(event.target.value as ScenarioDraft["repeat"])}>
              <option value="daily">{t("assistant-taskeditor.repeat_daily")}</option>
              <option value="interval">{t("assistant-taskeditor.repeat_interval")}</option>
            </select>
          </Field>
          {draft.repeat === "daily" ? <>
            <Field label={t("assistant-taskeditor.time")}>
              <input type="time" className={inputClass} value={draft.time} onChange={(event) => edit({ time: event.target.value.slice(0, 5) })} />
            </Field>
            <Field label={t("assistant-taskeditor.which_days")}>
              <select className={inputClass} value={draft.weekdays.length ? "weekdays" : "every"} onChange={(event) => edit({ weekdays: event.target.value === "weekdays" ? [...WORKDAYS] : [] })}>
                <option value="every">{t("assistant-taskeditor.every_day")}</option>
                <option value="weekdays">{t("assistant-taskeditor.weekdays")}</option>
              </select>
            </Field>
          </> : <Field label={t("assistant-scenario-templates.interval_label")} note={t("assistant-scenario-templates.interval_hint", { min: MIN_EVERY, max: MAX_EVERY })}>
            <input
              type="number"
              inputMode="numeric"
              min={MIN_EVERY}
              max={MAX_EVERY}
              step={1}
              className={inputClass}
              value={draft.every_minutes || ""}
              onChange={(event) => edit({ every_minutes: event.target.value === "" ? 0 : Number(event.target.value) })}
            />
          </Field>}
        </div>
        <p className={HINT}>{t("assistant-scenario-templates.schedule_hint")}</p>
      </section>

      <details className="group rounded-card bg-inset p-3 shadow-hairline">
        <summary className="flex cursor-pointer list-none items-center gap-2 text-meta font-medium text-ink [&::-webkit-details-marker]:hidden">
          <Icon name="chevron" size={13} className="shrink-0 -rotate-90 transition-transform group-open:rotate-0" />
          {t("assistant-scenario-templates.advanced")}
        </summary>
        <p className="mt-2 text-label leading-relaxed text-ink-3">{t("assistant-scenario-templates.advanced_note")}</p>
      </details>
    </div>
  </Dialog>;
}
