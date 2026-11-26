import { isPending } from "../lib/add-computer";
import type { Machine, MachineMetrics } from "../lib/types";

/** Whether Machines' summary has something real to draw; disk-only data is not one of its gauges. */
export function hasCurrentSample(machine: Machine, metrics?: MachineMetrics): boolean {
  if (!machine.online || isPending(machine) || metrics?.online === false || !metrics) return false;
  if (metrics.usage) return true;
  const latest = metrics.latest;
  if (!latest) return false;
  return [latest.cpu, latest.load, latest.mem_used, latest.mem_total, latest.net_rx, latest.net_tx, latest.battery_percent, latest.temp_c]
    .some((value) => typeof value === "number" && Number.isFinite(value));
}
