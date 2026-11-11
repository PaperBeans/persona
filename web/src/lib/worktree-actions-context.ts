import { t } from "../i18n";
import { credentialOwner } from "./api";
import { currentHosts } from "./engines";
import { defaultManager, shownManager } from "./manager-choice";
import { sessionWorkspace } from "./agent-repos";
import type { Machine, Session } from "./types";
import { workspaceTarget, type HostWorkspace, type WorkspaceCatalogSnapshot } from "./workspace-catalog";
import type { WorkspaceTarget } from "./workspace-target";

export const worktreeActionKey = (target: WorkspaceTarget) => JSON.stringify([
  target.owner, target.manager, target.machineId, target.host, target.workspaceId, target.cwd,
]);

export interface WorktreeActionContext {
  catalog: WorkspaceCatalogSnapshot;
  machines: readonly Machine[];
  /** Granted only after an owner-bound full-device permission check. */
  writable: boolean;
}

/** All entry points resolve the exact host-owned catalog row; paths are checked for replacement, never used to find it. */
export function resolveWorktreeAction(target: WorkspaceTarget, context: WorktreeActionContext) {
  const { catalog, machines, writable } = context;
  const owned = Boolean(target.owner && target.manager && target.owner === credentialOwner()
    && target.manager === shownManager() && catalog.owner === target.owner && catalog.manager === target.manager);
  const matches = owned ? catalog.workspaces.filter((w) => w.machine_id === target.machineId
    && w.source === target.host && w.source_id === target.workspaceId && w.source !== "session-directory") : [];
  const workspace = matches.length === 1 && matches[0].cwd === target.cwd ? matches[0] : undefined;
  const computers = machines.filter((m) => m.id === target.machineId && (m.manager || defaultManager()) === target.manager);
  const machine = computers.length === 1 ? computers[0] : undefined;
  const available = Boolean(workspace && machine?.online && workspace.online !== false && workspace.available !== false
    && currentHosts(machine).hosts.includes(target.host) && !machine.engines?.some((e) => e.id === target.host && !e.available));
  const reason = !owned ? t("worktree-actions.owner_changed") : !workspace || !available
    ? t("worktree-actions.unavailable") : !writable ? t("worktree-actions.read_only") : "";
  const parentRows = workspace?.parent_source_id ? catalog.workspaces.filter((w) => w.machine_id === target.machineId
    && w.source === target.host && w.source_id === workspace.parent_source_id && w.source_id !== workspace.source_id) : [];
  const parent = parentRows.length === 1 ? workspaceTarget(parentRows[0], catalog.owner, catalog.manager) : undefined;
  const hasChildren = Boolean(workspace && catalog.workspaces.some((w) => w.machine_id === target.machineId
    && w.source === target.host && w.parent_source_id === workspace.source_id));
  return { workspace, machine, parent, available, writable: !reason, reason, hasChildren,
    // These adapters implement host.Organizer + host.Launcher. Other runtimes retain copy/open compatibility.
    canRename: !reason && ["herdr", "orca"].includes(target.host),
    canCreate: !reason && ["herdr", "orca"].includes(target.host),
    canReconcile: owned && writable && Boolean(machine?.online && currentHosts(machine).hosts.includes(target.host)
      && !machine.engines?.some((e) => e.id === target.host && !e.available))
      && (matches.length === 0 || Boolean(workspace && workspace.online !== false && workspace.available !== false)),
    canDelete: !reason && target.host === "orca" && workspace?.is_main !== true && !hasChildren,
  };
}
export type ResolvedWorktreeAction = ReturnType<typeof resolveWorktreeAction>;

/** Preserve caller order, including pins, while using M2's exact attribution instead of matching cwd. */
export function firstDirectWorktreeAgent(target: WorkspaceTarget, catalog: WorkspaceCatalogSnapshot, sessions: readonly Session[]): Session | undefined {
  if (target.owner !== catalog.owner || target.manager !== catalog.manager) return;
  return sessions.find((session) => {
    const w = sessionWorkspace(session, catalog.workspaces, catalog.manager);
    return w?.machine_id === target.machineId && w.source === target.host && w.source_id === target.workspaceId;
  });
}

/** A catalog entry's safety-relevant metadata; title changes do not replace a runtime destination. */
export function worktreeSafetyStamp(workspace: HostWorkspace): string {
  return JSON.stringify([workspace.machine_id, workspace.source, workspace.source_id, workspace.cwd, workspace.workspace_kind, workspace.is_main]);
}
