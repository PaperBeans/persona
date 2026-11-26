import { Suspense, type ComponentType, type ReactNode } from "react";
import { t } from "../i18n";
import { href, onLinkClick } from "../lib/router";
import type { Machine } from "../lib/types";
import { OverviewMachineCards } from "./OverviewMachineCards";
import { OverviewMachineRows, OverviewMachineViewSwitch, useOverviewMachineView } from "./OverviewMachineView";

type Section = ComponentType<{ title: string; count: number; children: ReactNode; right?: ReactNode; hint?: string; grid?: true | "cards" | "tasks" }>;

/** Overview's Computers section, kept off the first screen until it is used. */
export function OverviewMachineSection({ Section, machines, agentCounts, offlineText }: {
  Section: Section;
  machines: Machine[];
  agentCounts?: ReadonlyMap<string, number>;
  offlineText: (machine: Machine) => string;
}) {
  const [machineView, chooseMachineView] = useOverviewMachineView();
  const rows = <OverviewMachineRows machines={machines} agentCounts={agentCounts} offlineText={offlineText} />;
  return (
    <Section
      title={t("home.computers")}
      count={machines.length}
      right={
        <div className="flex flex-wrap items-center justify-end gap-2 self-center">
          <a href={href({ name: "machines" })} onClick={(e) => onLinkClick(e, { name: "machines" })} className="whitespace-nowrap text-meta text-ink-3 hover:text-ink-2">{t("home.monitor")}</a>
          <OverviewMachineViewSwitch value={machineView} onChange={chooseMachineView} />
        </div>
      }
    >
      {machineView === "cards" ? (
        <Suspense fallback={rows}>
          <OverviewMachineCards machines={machines} agentCounts={agentCounts} offlineText={offlineText} />
        </Suspense>
      ) : rows}
    </Section>
  );
}
