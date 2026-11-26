import { useEffect, useState } from "react";
import { api, errorText } from "./api";
import { defaultManager, withManager } from "./manager-choice";
import { currentRoster, managerOrder, multiManager } from "./managers";
import type { MachineMetrics } from "./types";
import { documentPage, visiblePoll } from "./visible-poll";

/** The Machines page's shared monitoring read: one request per online assistant, every 10 seconds while visible. */
export function useMachineMetrics(): { metrics: MachineMetrics[] | null; error: string } {
  const [metrics, setMetrics] = useState<MachineMetrics[] | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    let dead = false;
    const previous = new Map<string, MachineMetrics[]>();
    const load = async () => {
      const roster = currentRoster();
      const ids = multiManager(roster) ? managerOrder(roster).filter((m) => m.online).map((m) => m.id) : [defaultManager()];
      const results = await Promise.allSettled(ids.map((id) => api<{ machines: MachineMetrics[] }>(withManager("/metrics", id))));
      if (dead) return;
      const failed = results.find((r) => r.status === "rejected");
      if (failed && results.every((r) => r.status === "rejected")) {
        setError(errorText(failed.reason));
        return;
      }
      const merged = new Map<string, MachineMetrics>();
      results.forEach((r, i) => {
        if (r.status === "fulfilled") previous.set(ids[i], r.value.machines ?? []);
        for (const m of previous.get(ids[i]) ?? []) {
          if (!merged.has(m.machine_id)) merged.set(m.machine_id, m);
        }
      });
      setMetrics([...merged.values()]);
      setError("");
    };
    const stop = visiblePoll(() => void load(), 10_000, documentPage);
    return () => {
      dead = true;
      stop();
    };
  }, []);

  return { metrics, error };
}
