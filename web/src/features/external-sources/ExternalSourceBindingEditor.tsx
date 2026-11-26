import { useCallback, useMemo, useState } from "react";
import { t } from "../../i18n";
import { errorText } from "../../lib/api";
import { useInbox } from "../../lib/inbox";
import { defaultManager } from "../../lib/manager-choice";
import { refreshTodos, useStore } from "../../lib/store";
import { Button, Dialog, Loading, toast } from "../../ui/atoms";
import { RowFailed } from "../../views/settings/SettingsParts";
import {
  effectiveExternalPolicy, lookupExternalBindings, readExternalDirections, saveExternalBinding,
  type ExternalBinding, type ExternalRef, type ExternalTarget, type TargetKind,
} from "./api";
import { policyDraft, policyInput, referenceDraft, referenceInput, refKey } from "./draft";
import { ExternalField, externalFieldClass, PolicyFields } from "./PolicyFields";
import { externalRefLabel, ReferenceFields } from "./ReferenceFields";
import { useExternalRead } from "./useExternalRead";
import { useExternalWrite } from "./useExternalWrite";

export interface ExternalSourceBindingEditorProps {
  manager: string;
  binding?: ExternalBinding;
  target?: ExternalTarget & { title: string };
  source?: { ref: ExternalRef; label: string };
  onClose: () => void;
  onSaved?: (binding: ExternalBinding) => void;
}

export function ExternalSourceBindingEditor({ manager, binding, target, source, onClose, onSaved }: ExternalSourceBindingEditorProps) {
  const [reference, setReference] = useState(() => referenceDraft(binding?.external_ref ?? source?.ref));
  const [policy, setPolicy] = useState(() => policyDraft(binding?.policy));
  const initial = binding ?? target;
  const [kind, setKind] = useState<TargetKind>(initial?.target_kind ?? "direction");
  const [targetId, setTargetId] = useState(initial?.target_id ?? "");
  const { busy, error, write } = useExternalWrite();

  const inboxItems = useInbox((state) => state.items);
  const sources = useMemo(() => {
    const choices = new Map<string, { ref: ExternalRef; label: string }>();
    if (source) choices.set(refKey(source.ref), source);
    for (const item of inboxItems) {
      const ref = item.external_path?.[0];
      if (!ref || (item.manager ?? defaultManager()) !== manager) continue;
      const key = refKey(ref);
      if (!choices.has(key)) choices.set(key, { ref, label: item.where || item.title || externalRefLabel(ref) });
    }
    return [...choices.values()];
  }, [inboxItems, manager, source]);

  const directions = useExternalRead(useCallback((signal: AbortSignal) => readExternalDirections(manager, signal), [manager]));
  const todos = useStore((state) => state.todos);
  const todosLoaded = useStore((state) => state.todosLoaded);
  const todosError = useStore((state) => state.todosError);
  const targets: { id: string; title?: string }[] = kind === "todo"
    ? todos.filter((todo) => (todo.manager ?? defaultManager()) === manager).map((todo) => ({ id: todo.id, title: todo.title }))
    : (directions.data ?? []).filter((direction) => !direction.ended || direction.id === targetId)
        .map((direction) => ({ id: direction.id, title: direction.name || direction.title }));
  if (initial?.target_kind === kind && initial.target_id === targetId && !targets.some((row) => row.id === targetId)) {
    targets.unshift({ id: targetId, title: target?.title });
  }

  // Serialize only the selected nodes, so changing an unused draft field never invalidates the inherited read.
  const ref = referenceInput(reference);
  const refJSON = ref && JSON.stringify(ref);
  const boundRef = binding && refKey(binding.external_ref);
  const needsSavedInterval = binding?.policy.mode === "digest" && binding.policy.digest_interval_minutes === undefined;
  const bindingId = binding?.id;
  const readReference = useCallback(async (signal: AbortSignal) => {
    const selected = JSON.parse(refJSON!) as ExternalRef;
    const [baseline, lookup, savedEffective] = await Promise.all([
      effectiveExternalPolicy(manager, selected, signal),
      lookupExternalBindings(manager, selected, signal),
      // The hub exposes an inherited interval only when the resolved mode is digest.
      needsSavedInterval && refKey(selected) === boundRef ? effectiveExternalPolicy(manager, selected, signal, bindingId) : undefined,
    ]);
    return { baseline, lookup, savedEffective };
  }, [manager, refJSON, boundRef, bindingId, needsSavedInterval]);
  const preview = useExternalRead(refJSON ? readReference : null);
  const checkedRef = preview.data?.baseline.external_ref;
  const baseline = preview.data?.baseline;
  const withSavedInterval = preview.data?.savedEffective;
  const inherited = baseline && policy.mode === "digest" && withSavedInterval ? {
    policy: { ...baseline.policy, digest_interval_minutes: withSavedInterval.policy.digest_interval_minutes },
    provenance: { ...baseline.provenance, digest_interval_minutes: withSavedInterval.provenance.digest_interval_minutes },
  } : baseline;
  const matches = checkedRef && preview.data?.lookup.matches?.filter((match) =>
    match.target_kind === kind && match.target_id === targetId && refKey(match.external_ref) === refKey(checkedRef),
  );
  const duplicate = matches?.some((match) => match.source === "explicit" && match.binding_id !== binding?.id);
  const projected = matches?.some((match) => match.source === "repository_link");
  const savedPolicy = policyInput(policy);
  const canSave = !busy && savedPolicy && targetId && checkedRef && !duplicate;
  const close = () => { if (!busy) onClose(); };

  const save = () => {
    if (!canSave) return;
    void write(
      () => saveExternalBinding(manager, {
        external_ref: checkedRef, target_kind: kind, target_id: targetId, policy: savedPolicy,
      }, binding?.id),
      (kept) => {
        onSaved?.(kept);
        toast(t("external-sources.binding_saved"), "ok");
        onClose();
      },
    );
  };

  return (
    <Dialog open title={t(binding ? "external-sources.edit_binding" : "external-sources.add_binding")} onClose={close} footer={<>
      <Button tone="quiet" disabled={busy} className="min-h-11" onClick={close}>{t("common.cancel")}</Button>
      <Button tone="primary" busy={busy} disabled={!canSave} className="min-h-11" onClick={save}>{t("common.save")}</Button>
    </>}>
      <div className="space-y-4">
        <ReferenceFields value={reference} onChange={setReference} sources={sources} disabled={busy} />
        <fieldset className="min-w-0 border-t border-line pt-3" disabled={busy}>
          <legend className="text-card-title font-semibold text-ink">{t("external-sources.destination")}</legend>
          <div className="grid gap-3 sm:grid-cols-2">
            <ExternalField label={t("external-sources.target_kind")}>
              <select className={externalFieldClass} value={kind} onChange={(e) => {
                setKind(e.target.value === "todo" ? "todo" : "direction");
                setTargetId("");
              }}>
                <option value="direction">{t("external-sources.direction")}</option>
                <option value="todo">{t("external-sources.todo")}</option>
              </select>
            </ExternalField>
            <ExternalField label={t(kind === "direction" ? "external-sources.direction" : "external-sources.todo")}>
              <select className={externalFieldClass} value={targetId} onChange={(e) => setTargetId(e.target.value)}>
                <option value="">{t("external-sources.pick_target")}</option>
                {targets.map(({ id, title }) => <option key={id} value={id}>{title || t("external-sources.untitled")}</option>)}
              </select>
            </ExternalField>
          </div>
          {kind === "direction" && directions.error !== undefined
            ? <RowFailed text={errorText(directions.error)} retry={t("todos.retry")} onRetry={directions.retry} />
            : kind === "todo" && todosError
              ? <RowFailed text={todosError} retry={t("todos.retry")} onRetry={() => void refreshTodos()} />
              : (kind === "direction" ? directions.loading : !todosLoaded) && <Loading />}
        </fieldset>
        <div className="border-t border-line pt-3">
          <PolicyFields value={policy} onChange={setPolicy} baseline={inherited} overrideSource="binding" disabled={busy} />
          {preview.loading && <Loading />}
          {preview.error !== undefined && <RowFailed text={errorText(preview.error)} retry={t("todos.retry")} onRetry={preview.retry} />}
        </div>
        {projected && <p className="text-meta text-ink-3">{t("external-sources.projected_hint")}</p>}
        {(error || duplicate || !savedPolicy) && (
          <p className="text-meta text-red" role="alert">
            {error || t(duplicate ? "external-sources.duplicate" : "external-sources.invalid_policy")}
          </p>
        )}
      </div>
    </Dialog>
  );
}
