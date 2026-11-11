import { useSyncExternalStore } from "react";
import { credentialOwner, onCredentialChange } from "./api";
import { shownManager, onManagerChoice } from "./manager-choice";
import type { WorkspaceTarget } from "./workspace-target";

let selected: WorkspaceTarget | undefined;
const listeners = new Set<() => void>();
const emit = () => listeners.forEach((listener) => listener());
const clear = () => { if (selected) { selected = undefined; emit(); } };
onCredentialChange(clear);
onManagerChoice(clear);

/** Selection shares the catalog identity and cannot survive a connection or manager change. */
export function selectWorkspace(target: WorkspaceTarget): void {
  if (target.owner !== credentialOwner() || target.manager !== shownManager()) return;
  selected = { ...target };
  emit();
}
function snapshot(): string {
  return selected && selected.owner === credentialOwner() && selected.manager === shownManager()
    ? JSON.stringify([selected.owner, selected.manager, selected.machineId, selected.host, selected.workspaceId]) : "";
}
const subscribe = (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; };
export function useWorkspaceSelection(): string {
  return useSyncExternalStore(subscribe, snapshot, snapshot);
}
