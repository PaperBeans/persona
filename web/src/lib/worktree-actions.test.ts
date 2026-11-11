// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { setToken, credentialOwner } from "./api";
import { chooseManager, resetManagerChoice, setRosterDefault } from "./manager-choice";
import type { Machine, Session } from "./types";
import type { HostWorkspace, WorkspaceCatalogSnapshot } from "./workspace-catalog";
import type { OwnedWorkspaceTarget } from "./worktree-actions";
import { firstDirectWorktreeAgent, resolveWorktreeAction } from "./worktree-actions-context";

let target: OwnedWorkspaceTarget;
let catalog: WorkspaceCatalogSnapshot;
let machines: Machine[];
const row = (id: string, extra: Partial<HostWorkspace> = {}): HostWorkspace => ({ id, machine_id: "computer", source: "orca", source_id: id, title: id, cwd: `/repo/${id}`, ...extra });
const resolve = (writable = true) => resolveWorktreeAction(target, { catalog, machines, writable });
beforeEach(() => {
  resetManagerChoice(); setRosterDefault("manager"); setToken("synthetic-actions");
  catalog = { owner: credentialOwner(), manager: "manager", workspaces: [row("work"), row("parent")] };
  target = { owner: catalog.owner, manager: catalog.manager, machineId: "computer", host: "orca", workspaceId: "work", cwd: "/repo/work", label: "Work" };
  machines = [{ id: "computer", manager: "manager", name: "Computer", online: true, engine_hosts: ["orca", "herdr"] }];
});
afterEach(() => resetManagerChoice());

describe("exact worktree action admission", () => {
  it("uses the chosen assistant's catalog and rejects the previous assistant's target", () => {
    chooseManager("other");
    expect(resolve().writable).toBe(false);
    catalog = { ...catalog, manager: "other" };
    target = { ...target, manager: "other" };
    machines = [{ ...machines[0], manager: "other" }];
    expect(resolve().canRename).toBe(true);
  });
  it("resolves same-host cross-project parent without guessing by directory", () => {
    catalog.workspaces[0].parent_source_id = "parent";
    catalog.workspaces[0].project_id = "other-project";
    catalog.workspaces.push(row("parent", { machine_id: "other" }), row("parent", { source: "herdr" }));
    expect(resolve().parent).toMatchObject({ owner: target.owner, manager: "manager", machineId: "computer", host: "orca", workspaceId: "parent" });
  });
  it("does not invent an ambiguous parent", () => {
    catalog.workspaces[0].parent_source_id = "parent";
    catalog.workspaces.push(row("parent"));
    expect(resolve().parent).toBeUndefined();
  });
  for (const field of ["owner", "manager", "machineId", "host", "workspaceId", "cwd"] as const) {
    it(`rejects a mismatched ${field} even when paths and labels look alike`, () => {
      target = { ...target, [field]: "other" };
      expect(resolve().writable).toBe(false);
    });
  }
  it("fails closed for missing owner/manager, duplicate identity and compatibility rows", () => {
    expect(resolveWorktreeAction({ ...target, owner: undefined }, { catalog, machines, writable: true }).writable).toBe(false);
    catalog.workspaces.push(row("work")); expect(resolve().writable).toBe(false);
    catalog.workspaces = [row("work", { source: "session-directory" })]; target.host = "session-directory";
    expect(resolve().workspace).toBeUndefined();
  });
  it("blocks readonly, offline, unavailable and disabled hosts", () => {
    expect(resolve(false).canRename).toBe(false);
    machines[0].online = false; expect(resolve().writable).toBe(false); machines[0].online = true;
    catalog.workspaces[0].available = false; expect(resolve().writable).toBe(false); catalog.workspaces[0].available = true;
    machines[0].engine_hosts = ["herdr"]; expect(resolve().writable).toBe(false);
    machines[0].engine_hosts = ["orca"]; machines[0].engines = [{ id: "orca", name: "Orca", available: false }];
    expect(resolve().writable).toBe(false);
  });
  it("blocks deletion of main and parents, while preserving supported non-Orca actions", () => {
    catalog.workspaces[0].is_main = true; expect(resolve().canDelete).toBe(false);
    catalog.workspaces[0].is_main = false; catalog.workspaces.push(row("child", { parent_source_id: "work" })); expect(resolve().canDelete).toBe(false);
    target.host = "herdr"; catalog.workspaces[0].source = "herdr";
    expect(resolve()).toMatchObject({ canRename: true, canCreate: true, canDelete: false });
    target.host = "future"; catalog.workspaces[0].source = "future"; machines[0].engine_hosts = ["future"];
    expect(resolve()).toMatchObject({ writable: true, canRename: false, canCreate: false, canDelete: false });
  });
});

it("opens the first direct Agent, including pins, without using descendants or another owner/machine/host", () => {
  catalog.workspaces.push(row("child", { parent_source_id: "work" }));
  const agent = (id: string, extra: Partial<Session>): Session => ({ machine_id: "computer", session_id: id, title: id, cwd: target.cwd, host: "orca", workspace_id: "work", status: "idle", ...extra });
  const ordered = [agent("descendant", { workspace_id: "child" }), agent("wrong-manager", { manager: "other" }), agent("wrong-host", { host: "herdr" }), agent("wrong-computer", { machine_id: "other" }), agent("pinned-direct", {}), agent("direct", {})];
  expect(firstDirectWorktreeAgent(target, catalog, ordered)?.session_id).toBe("pinned-direct");
  expect(firstDirectWorktreeAgent(target, catalog, ordered.slice(0, 4))).toBeUndefined();
  expect(firstDirectWorktreeAgent({ ...target, owner: "old" }, catalog, ordered)).toBeUndefined();
});
