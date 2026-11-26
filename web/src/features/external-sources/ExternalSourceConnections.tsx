import { useCallback, useState } from "react";
import { t } from "../../i18n";
import { errorText } from "../../lib/api";
import { Button, Dialog, Loading, toast } from "../../ui/atoms";
import { Group, Row, RowFailed } from "../../views/settings/SettingsParts";
import { deleteExternalBinding, isExternalSourcesUnsupported, listExternalBindings, type ExternalBinding, type TargetKind } from "./api";
import { BindingDialog } from "./BindingDialog";
import { externalRefLabel } from "./ReferenceFields";
import { useExternalRead } from "./useExternalRead";
import { useExternalWrite } from "./useExternalWrite";

export interface ExternalSourceConnectionsProps {
  manager: string;
  kind: TargetKind;
  id: string;
  title: string;
}

export function ExternalSourceConnections({ manager, kind, id, title }: ExternalSourceConnectionsProps) {
  const [edit, setEdit] = useState<ExternalBinding | "new">();
  const [disconnect, setDisconnect] = useState<ExternalBinding>();
  const { busy, error, write } = useExternalWrite();
  const list = useExternalRead(useCallback((signal: AbortSignal) =>
    listExternalBindings(manager, { target_kind: kind, target_id: id }, signal), [manager, kind, id]));
  if (isExternalSourcesUnsupported(list.error)) return null;
  const rows = list.data?.bindings ?? [];

  return (
    <>
      <Group title={t("external-sources.section")} right={list.data && (
        <Button tone="quiet" className="min-h-11" icon="plus" onClick={() => setEdit("new")}>{t("external-sources.add_binding")}</Button>
      )}>
        {list.loading && <div className="px-4 py-3"><Loading /></div>}
        {list.error !== undefined && <RowFailed text={errorText(list.error)} retry={t("todos.retry")} onRetry={list.retry} />}
        {list.data && !rows.length && <Row label={t("external-sources.no_bindings")} />}
        {rows.map((binding) => {
          const label = externalRefLabel(binding.external_ref);
          return (
            <div key={binding.id} className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 px-4 py-2">
              <span className="min-w-0 flex-1 text-meta text-ink [overflow-wrap:anywhere]">{label}</span>
              <div className="ml-auto flex flex-wrap justify-end gap-1">
                <Button tone="quiet" className="min-h-11" onClick={() => setEdit(binding)}>{t("external-sources.edit")}</Button>
                <Button tone="quiet" className="min-h-11" onClick={() => setDisconnect(binding)}>{t("external-sources.disconnect")}</Button>
              </div>
            </div>
          );
        })}
      </Group>
      {edit && (
        <BindingDialog manager={manager} binding={edit === "new" ? undefined : edit}
          target={{ target_kind: kind, target_id: id, title }} onClose={() => setEdit(undefined)} onSaved={list.retry} />
      )}
      {disconnect && (
        <Dialog open title={t("external-sources.disconnect_title")}
          onClose={() => { if (!busy) setDisconnect(undefined); }}
          footer={<>
            <Button tone="quiet" className="min-h-11" disabled={busy} onClick={() => setDisconnect(undefined)}>{t("common.cancel")}</Button>
            <Button tone="danger" className="min-h-11" busy={busy} onClick={() => void write(
              () => deleteExternalBinding(manager, disconnect.id),
              () => { setDisconnect(undefined); list.retry(); toast(t("external-sources.disconnected"), "ok"); },
            )}>{t("external-sources.disconnect")}</Button>
          </>}>
          <p className="mb-2 font-medium text-ink [overflow-wrap:anywhere]">{externalRefLabel(disconnect.external_ref)}</p>
          <p className="text-meta text-ink-2">{t("external-sources.disconnect_body")}</p>
          {error && <p className="mt-2 text-meta text-red" role="alert">{error}</p>}
        </Dialog>
      )}
    </>
  );
}
