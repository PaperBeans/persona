import { lazy, memo, Suspense, useSyncExternalStore } from "react";
import { currentRoster, managerHostMatch, managerOf, subscribeRoster } from "../../lib/managers";
import { getState, subscribe } from "../../lib/store";
import type { Machine } from "../../lib/types";

const Notice = lazy(() => import("./AssistantUpdateNotice"));

const deployPhase = (machine?: Machine) => machine?.deploy_phase || (machine?.deploying ? "updating" : undefined);

function assistantUpdate(mid: string, manager: string | undefined) {
  const { machines } = getState();
  const computer = machines.find((m) => m.id === mid);
  const phase = deployPhase(computer);
  if (phase) return phase;

  const roster = currentRoster();
  const owner = managerOf(roster, manager ?? computer?.manager ?? roster?.default ?? "");
  if (!owner || owner.installing) return undefined;
  const pending = owner.pending_restart ? "pending_restart" : undefined;
  let legacyHost: Machine | undefined;
  for (const machine of machines) {
    const match = managerHostMatch(owner, machine);
    if (match === "uid") return deployPhase(machine) ?? pending;
    if (match === "host") legacyHost ??= machine;
  }
  return deployPhase(legacyHost) ?? pending;
}

function subscribeUpdate(listener: () => void) {
  let machines = getState().machines;
  const stopMachines = subscribe(() => {
    const next = getState().machines;
    if (machines === next) return;
    machines = next;
    listener();
  });
  const stopRoster = subscribeRoster(listener);
  return () => {
    stopMachines();
    stopRoster();
  };
}

/** A steady empty snapshot keeps both the notice chunk and the rest of the chat out of deployment renders. */
export const AssistantUpdateSlot = memo(function AssistantUpdateSlot({ mid, manager }: { mid: string; manager?: string }) {
  const snapshot = () => assistantUpdate(mid, manager);
  const phase = useSyncExternalStore(subscribeUpdate, snapshot, snapshot);
  return phase ? <Suspense fallback={null}><Notice phase={phase} /></Suspense> : null;
});
