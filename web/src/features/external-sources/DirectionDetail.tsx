import { useCallback } from "react";
import { t } from "../../i18n";
import { errorText } from "../../lib/api";
import { useShownManager } from "../../lib/manager-choice";
import { navigate } from "../../lib/router";
import { Button, IconButton, Loading } from "../../ui/atoms";
import { RowFailed } from "../../views/settings/SettingsParts";
import { readExternalDirections } from "./api";
import { ExternalSourcesEntry } from "./ExternalSourcesEntry";
import { useExternalRead } from "./useExternalRead";

export function DirectionDetail({ id, manager: named, onToggleSidebar }: {
  id: string;
  manager?: string;
  onToggleSidebar?: () => void;
}) {
  const current = useShownManager();
  const manager = named ?? current;
  const directions = useExternalRead(useCallback((signal: AbortSignal) => readExternalDirections(manager, signal), [manager]));
  const direction = directions.data?.find((entry) => entry.id === id);
  const title = direction?.name || direction?.title || t("external-sources.untitled");
  return (
    <div className="h-full min-w-0 overflow-y-auto">
      <div className="mx-auto max-w-3xl px-4 py-4 sm:px-6">
        <div className="flex flex-wrap items-center gap-2">
          {onToggleSidebar && <IconButton icon="sidebar" label={t("agents-agentsview.sidebar")} onClick={onToggleSidebar} className="lg:hidden" />}
          <Button tone="quiet" className="min-h-11" icon="chevronLeft" onClick={() => navigate({ name: "todos" })}>{t("todos.back_to_list")}</Button>
        </div>
        <h1 className="mt-3 text-card-title font-semibold text-ink [overflow-wrap:anywhere]">{direction ? title : t("external-sources.direction_details")}</h1>
        {directions.loading && <Loading />}
        {directions.error !== undefined && <RowFailed text={errorText(directions.error)} retry={t("todos.retry")} onRetry={directions.retry} />}
        {directions.data && !direction && <p className="mt-3 text-meta text-ink-3">{t("external-sources.direction_unavailable")}</p>}
        {direction && <ExternalSourcesEntry id={id} manager={manager} kind="direction" title={title} />}
      </div>
    </div>
  );
}
