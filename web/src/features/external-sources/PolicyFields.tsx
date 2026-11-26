import { cloneElement, useId, type ReactElement, type ReactNode } from "react";
import { t } from "../../i18n";
import { cx } from "../../ui/atoms";
import { settingsInputClass } from "../../views/settings/SettingsParts";
import type { ExternalPolicy, PolicyBaseline, PolicySource, PreFilter } from "./api";
import {
  ACTION_ROLES, allowsInterval, FILTER_FIELDS, FORWARD_MODES, MAX_INTERVAL, MIN_INTERVAL, NEW_ITEM_POLICIES,
  type PolicyDraft,
} from "./draft";

export const externalFieldClass = cx(settingsInputClass, "min-h-11 min-w-0 text-meta");

export function ExternalField({ label, hint, children }: {
  label: string; hint?: ReactNode; children: ReactElement<{ id?: string; "aria-describedby"?: string }>;
}) {
  const id = useId();
  const hintId = `${id}-hint`;
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <label htmlFor={id} className="text-meta font-medium text-ink-2">{label}</label>
      {cloneElement(children, { id, "aria-describedby": hint ? hintId : undefined })}
      {hint && <div id={hintId} className="text-submeta leading-snug text-ink-3 [overflow-wrap:anywhere]">{hint}</div>}
    </div>
  );
}

export const policyOptionLabel = (value: string) => t(`external-sources.option_${value}`);
const originLabel = (source: PolicySource) => t(`external-sources.from_${source}`);

function Origin({ baseline, field, overridden, overrideSource }: {
  baseline?: PolicyBaseline;
  field: keyof ExternalPolicy;
  overridden: boolean;
  overrideSource: "binding" | "channel";
}) {
  const source = overridden ? overrideSource : baseline?.provenance[field]?.source;
  return source ? <span data-policy-origin={source}>{originLabel(source)}</span> : null;
}

function PolicyChoice<T extends string>({ label, value, options, inherited, origin, onChange }: {
  label: string;
  value: T | "";
  options: readonly T[];
  inherited?: T;
  origin: ReactNode;
  onChange: (value: T | "") => void;
}) {
  const hint = !value && inherited ? <>{policyOptionLabel(inherited)}{" · "}{origin}</> : origin;
  return (
    <ExternalField label={label} hint={hint}>
      <select className={externalFieldClass} value={value} onChange={(e) => onChange(e.target.value as T | "")}>
        <option value="">{t("external-sources.inherit")}</option>
        {options.map((choice) => <option key={choice} value={choice}>{policyOptionLabel(choice)}</option>)}
      </select>
    </ExternalField>
  );
}

function InheritedFilter({ filter }: { filter?: PreFilter }) {
  if (!filter) return null;
  const fields = FILTER_FIELDS.filter((field) => filter[field]?.length);
  if (!fields.length) return <p>{t("external-sources.no_filter")}</p>;
  return (
    <dl className="space-y-1">
      {fields.map((field) => (
        <div className="flex flex-wrap gap-x-2" key={field}>
          <dt className="font-medium">{t(`external-sources.${field}`)}</dt>
          <dd>{filter[field]?.join(" · ")}</dd>
        </div>
      ))}
    </dl>
  );
}

export function PolicyFields({ value, onChange, baseline, overrideSource, disabled }: {
  value: PolicyDraft;
  onChange: (draft: PolicyDraft) => void;
  baseline?: PolicyBaseline;
  overrideSource: "binding" | "channel";
  disabled?: boolean;
}) {
  const origin = (field: keyof ExternalPolicy, overridden: boolean) => (
    <Origin baseline={baseline} field={field} overridden={overridden} overrideSource={overrideSource} />
  );
  const set = <K extends keyof PolicyDraft,>(key: K, next: PolicyDraft[K]) => onChange({ ...value, [key]: next });
  const useInterval = allowsInterval(value.mode);
  return (
    <fieldset className="min-w-0 space-y-3" disabled={disabled}>
      <legend className="mb-2 text-card-title font-semibold text-ink">{t("external-sources.policy")}</legend>
      <div className="grid gap-3 sm:grid-cols-2">
        <PolicyChoice label={t("external-sources.mode")} value={value.mode} options={FORWARD_MODES}
          inherited={baseline?.policy.mode} origin={origin("mode", Boolean(value.mode))} onChange={(mode) => set("mode", mode)} />
        <ExternalField label={t("external-sources.interval")} hint={
          useInterval
            ? <>{origin("digest_interval_minutes", Boolean(value.interval))}{!baseline?.provenance.digest_interval_minutes && !value.interval && t("external-sources.interval_hint")}</>
            : t("external-sources.interval_hint")
        }>
          <input className={externalFieldClass} type="number" min={MIN_INTERVAL} max={MAX_INTERVAL} step={1}
            inputMode="numeric" value={useInterval ? value.interval : ""} disabled={!useInterval}
            placeholder={baseline?.policy.digest_interval_minutes?.toString() ?? t("external-sources.inherit")}
            onChange={(e) => set("interval", e.target.value)} />
        </ExternalField>
        <PolicyChoice label={t("external-sources.role")} value={value.role} options={ACTION_ROLES}
          inherited={baseline?.policy.action_role} origin={origin("action_role", Boolean(value.role))} onChange={(role) => set("role", role)} />
        <PolicyChoice label={t("external-sources.new_item")} value={value.newItem} options={NEW_ITEM_POLICIES}
          inherited={baseline?.policy.new_item_policy} origin={origin("new_item_policy", Boolean(value.newItem))} onChange={(newItem) => set("newItem", newItem)} />
      </div>
      <ExternalField label={t("external-sources.filter")} hint={
        <div className="space-y-1">
          {origin("pre_filter", value.filter === "replace")}
          {value.filter === "inherit" && <InheritedFilter filter={baseline?.policy.pre_filter} />}
          {value.filter === "replace" && <p>{t("external-sources.filter_hint")}</p>}
        </div>
      }>
        <select className={externalFieldClass} value={value.filter} onChange={(e) => set("filter", e.target.value === "replace" ? "replace" : "inherit")}>
          <option value="inherit">{t("external-sources.inherit")}</option>
          <option value="replace">{t("external-sources.filter_custom")}</option>
        </select>
      </ExternalField>
      {value.filter === "replace" && (
        <div className="grid gap-3 sm:grid-cols-3">
          {FILTER_FIELDS.map((field) => (
            <ExternalField key={field} label={t(`external-sources.${field}`)}>
              <textarea className={cx(externalFieldClass, "h-auto resize-y py-2")}
                rows={2} value={value[field]} onChange={(e) => set(field, e.target.value)} />
            </ExternalField>
          ))}
        </div>
      )}
    </fieldset>
  );
}
