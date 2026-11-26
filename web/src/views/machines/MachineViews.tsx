import { Suspense } from "react";
import { t } from "../../i18n";
import { lazyPage } from "../../lib/lazy-page";
import { href, onLinkClick } from "../../lib/router";
import type { MachineSubview } from "../../lib/machine-route";
import type { Machine } from "../../lib/types";
import { Loading } from "../../ui/atoms";

const PortsProcessesPanel = lazyPage(() => import("./PortsProcessesPanel").then((m) => m.PortsProcessesPanel));
const ToolchainPanel = lazyPage(() => import("./ToolchainPanel").then((m) => m.ToolchainPanel));
const ServicesPanel = lazyPage(() => import("./ServicesPanel").then((m) => m.ServicesPanel));
const FileBrowserPanel = lazyPage(() => import("./FileBrowserPanel").then((m) => m.FileBrowserPanel));
const NetworkPanel = lazyPage(() => import("./NetworkPanel").then((m) => m.NetworkPanel));
const StoragePanel = lazyPage(() => import("./StoragePanel").then((m) => m.StoragePanel));

const labels = {
  work: "machines.view_work", monitoring: "machines-machinedetail.monitoring", files: "machines.view_files",
  storage: "machines.view_storage", diagnostics: "machines.view_diagnostics", management: "machines.view_management",
  procs: "machines.view_procs", services: "machines.view_services", network: "machines-network.tab_label", toolchain: "machines.view_toolchain",
} as const;
const primary = ["work", "monitoring", "files", "storage", "diagnostics", "management"] as const;
const tools = { procs: PortsProcessesPanel, services: ServicesPanel, toolchain: ToolchainPanel, network: NetworkPanel };
type Diagnostic = keyof typeof tools;
function isDiagnostic(view: MachineSubview): view is Diagnostic {
  return Object.hasOwn(tools, view);
}

export function MachineViewLinks({ mid, view }: { mid: string; view: MachineSubview }) {
  const active = isDiagnostic(view) ? "diagnostics" : view === "config" ? "management" : view;
  function links(items: readonly (keyof typeof labels)[], current: string) {
    return <nav aria-label={t(items === primary ? "machines.view_label" : "machines.view_diagnostics")} className="flex flex-wrap gap-1 rounded-card bg-inset p-1">
      {items.map(v => {
        const to = { name: "machine" as const, mid, subview: v === "work" ? undefined : v };
        return <a key={v} href={href(to)} onClick={e => onLinkClick(e, to)} aria-current={current === v ? "page" : undefined}
          className="flex min-h-11 items-center whitespace-nowrap rounded-control px-3 text-label font-medium text-ink-2 hover:bg-hover aria-[current=page]:bg-surface aria-[current=page]:text-ink aria-[current=page]:shadow-btn">{t(items !== primary && v === "diagnostics" ? "node-health.title" : labels[v])}</a>;
      })}
    </nav>;
  }
  return <div className="grid gap-2">{links(primary, active)}
    {active === "diagnostics" && links(["diagnostics", ...Object.keys(tools) as Diagnostic[]], view)}
  </div>;
}

export function MachineToolView({ machine, view, mode, path }: { machine: Machine; view: MachineSubview; path?: string; mode?: "reclaim" }) {
  const Panel = isDiagnostic(view) ? tools[view] : null;
  return <Suspense fallback={<Loading />}>
    {view === "files" && <FileBrowserPanel key={path} machine={machine} path={path} routed />}
    {Panel && <Panel machine={machine} />}
    {view === "storage" && <StoragePanel machine={machine} focus={mode && { mid: machine.id, mode, seq: 1 }} />}
  </Suspense>;
}
