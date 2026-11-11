import { useSyncExternalStore } from "react";
import { contextMenuProps } from "../ui/context-menu";
import type { MenuAt } from "../ui/menu-model";
import { onCredentialChange } from "./api";
import { onManagerChoice } from "./manager-choice";
import type { WorkspaceTarget } from "./workspace-target";

export type OwnedWorkspaceTarget = WorkspaceTarget & { owner: string; manager: string };
export type { ResolvedWorktreeAction } from "./worktree-actions-context";

export interface WorktreeActionsRequest {
  id: number;
  target: OwnedWorkspaceTarget;
  anchor: HTMLElement;
  at?: MenuAt;
}
let request: WorktreeActionsRequest | null = null;
let serial = 0;
const listeners = new Set<() => void>();
const notify = () => listeners.forEach((listener) => listener());
const snapshot = () => request;
const subscribe = (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; };
export const useWorktreeActionsRequest = () => useSyncExternalStore(subscribe, snapshot, snapshot);

/** Mount WorktreeActionsHost once. Supply the owner/manager stamp from the authoritative M2 catalog. */
export function openWorktreeActions(target: WorkspaceTarget, anchor: HTMLElement, at?: MenuAt): void {
  // Missing stamps cannot acquire mutation rights by borrowing whatever owner is current later.
  request = { id: ++serial, target: { ...target, owner: target.owner || "", manager: target.manager || "" }, anchor, at };
  notify();
}
export function closeWorktreeActions(id: number): void {
  if (request?.id !== id) return;
  request = null;
  notify();
}
export const worktreeActionsCurrent = (id: number) => request?.id === id;
export const worktreeActionProps = (target: WorkspaceTarget) => contextMenuProps((at, anchor) => openWorktreeActions(target, anchor, at));

// Close immediately on scope retirement, including an away-and-back change before React commits.
onCredentialChange(() => { if (request) closeWorktreeActions(request.id); });
onManagerChoice(() => { if (request) closeWorktreeActions(request.id); });
