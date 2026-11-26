import { Suspense, useMemo } from "react";
import { lazyPage } from "../lib/lazy-page";
import { useMachineMetrics } from "../lib/machine-metrics";
import type { Machine } from "../lib/types";
import { OverviewSummaryBoundary } from "./OverviewSummaryBoundary";
import { OverviewMachineRows } from "./OverviewMachineView";

export { OVERVIEW_MACHINE_VIEW_KEY, OverviewMachineRows, OverviewMachineViewSwitch, useOverviewMachineView } from "./OverviewMachineView";
export type { OverviewMachineView } from "./OverviewMachineView";

const OverviewMachineCardList = lazyPage(() => import("./OverviewMachineCardList").then((m) => m.OverviewMachineCardList));

/** Overview's Computers in Cards mode: one shared metrics read and the exact Machines summary, charts included. */
export function OverviewMachineCards({ machines, agentCounts, offlineText }: {
  machines: Machine[];
  agentCounts?: ReadonlyMap<string, number>;
  offlineText: (machine: Machine) => string;
}) {
  // Section-level state keeps metric refreshes out of Home's Agent and task sections. No card starts work of its own.
  const { metrics, error } = useMachineMetrics();
  const byMachine = useMemo(() => new Map((metrics ?? []).map((m) => [m.machine_id, m])), [metrics]);
  const rows = <OverviewMachineRows machines={machines} agentCounts={agentCounts} offlineText={offlineText} />;
  return (
    <OverviewSummaryBoundary fallback={rows}>
      <Suspense fallback={rows}>
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 @min-[60rem]:grid-cols-3 @min-[84rem]:grid-cols-4 @min-[110rem]:grid-cols-5">
          <OverviewMachineCardList machines={machines} agentCounts={agentCounts} metricsByMachine={byMachine} error={error} offlineText={offlineText} />
        </div>
      </Suspense>
    </OverviewSummaryBoundary>
  );
}
