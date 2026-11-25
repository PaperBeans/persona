import { describe, expect, it } from "vitest";
import { buildRepoMachineTree, showsWorktreeLevel, type RepoCatalogOptions, type RepoTree } from "./agent-repos";
import type { HostWorkspace } from "./workspace-catalog";
import type { Machine, Session } from "./types";

// By repo says a repository's name once, on its heading (#13454). A level under it that only says that name again is
// left out, and the main checkout is found when its host (herdr) does not say which workspace it is.
const machines: Machine[] = [{ id: "machine-a", name: "Computer A", online: true }];
const agent = (id: string, workspace: string, cwd: string, patch: Partial<Session> = {}): Session => ({
  session_id: id, machine_id: "machine-a", title: id, cwd, state: "idle", status: "idle", space_kind: "herdr", workspace_id: workspace, ...patch,
});
/** A herdr workspace as the node lists it: named after its folder, no project, no word on which one is main. */
const herdr = (id: string, cwd: string, patch: Partial<HostWorkspace> = {}): HostWorkspace => ({
  id: `qualified-${id}`, machine_id: "machine-a", source: "herdr", source_id: id, title: cwd.split("/").pop()!, cwd, ...patch,
});
const options = (workspaces: HostWorkspace[], patch: Partial<RepoCatalogOptions> = {}): RepoCatalogOptions => ({ owner: "credential-a", manager: "manager-a", workspaces, ...patch });
const only = (sessions: Session[], workspaces: HostWorkspace[], patch: Partial<RepoCatalogOptions> = {}): RepoTree => {
  const repos = buildRepoMachineTree(sessions, machines, options(workspaces, patch)).flatMap((m) => m.repos);
  expect(repos).toHaveLength(1);
  return repos[0]!;
};

describe("By repo names a repository once", () => {
  it("lists a lone workspace's Agents under the repository when the workspace is named after its folder", () => {
    // A folder that is not a Git repository is its own section, and herdr names its workspace after it.
    const notes = only([agent("draft", "w-notes", "/code/notes")], [herdr("w-notes", "/code/notes")]);
    expect([notes.name, notes.worktrees.map((w) => w.name)]).toEqual(["notes", ["notes"]]);
    expect(showsWorktreeLevel(notes)).toBe(false);
    // A repository's main checkout, placed by Git, the same way.
    const app = only([agent("fix", "w-app", "/code/app", { repo_root: "/code/app", worktree: "/code/app" })], [herdr("w-app", "/code/app")]);
    expect(showsWorktreeLevel(app)).toBe(false);
  });

  it("keeps the workspace's level while it says more than the repository's name or has nothing to list", () => {
    const lone = (patch: Partial<HostWorkspace>, sessions = [agent("draft", "w-notes", "/code/notes")], opts?: Partial<RepoCatalogOptions>) => only(sessions, [herdr("w-notes", "/code/notes", patch)], opts);
    // A name of its own.
    expect(showsWorktreeLevel(lone({ title: "Weekly notes" }))).toBe(true);
    // A branch the host named (#1805).
    expect(showsWorktreeLevel(lone({ branch: "drafts" }))).toBe(true);
    // No Agent to list in its place (when opted in): its row is what is selected, renamed or removed.
    expect(showsWorktreeLevel(lone({}, [], { showEmptyWorkspaces: true }))).toBe(true);
    // A child workspace under it (Orca).
    const orca = { source: "orca", project_id: "p-notes", project_name: "notes" };
    const parent = herdr("w-notes", "/code/notes", orca);
    const child = herdr("w-child", "/code/notes-child", { ...orca, parent_source_id: "w-notes" });
    expect(showsWorktreeLevel(only([agent("draft", "w-notes", "/code/notes", { space_kind: "orca" })], [parent, child], { showEmptyWorkspaces: true }))).toBe(true);
  });

  it("marks the workspace at the repository's own folder as the main checkout when the host does not say", () => {
    // A linked tree whose name sorts before the repository's: the main checkout still comes first, with the home mark.
    const zeta = only([
      agent("lead", "w-zeta", "/code/zeta", { repo_root: "/code/zeta", worktree: "/code/zeta" }),
      agent("fix", "w-alpha", "/code/zeta/.worktrees/alpha", { repo_root: "/code/zeta", worktree: "/code/zeta/.worktrees/alpha", branch: "alpha" }),
    ], [herdr("w-alpha", "/code/zeta/.worktrees/alpha"), herdr("w-zeta", "/code/zeta")]);
    expect(zeta.worktrees.map((w) => [w.name, w.main])).toEqual([["zeta", true], ["alpha", false]]);
    // An empty linked workspace, placed by its folder alone, is no main checkout either.
    const empty = only([agent("lead", "w-zeta", "/code/zeta")], [herdr("w-zeta", "/code/zeta"), herdr("w-alpha", "/code/zeta/.worktrees/alpha")], { showEmptyWorkspaces: true });
    expect(empty.worktrees.map((w) => [w.name, w.main])).toEqual([["zeta", true], ["alpha", false]]);
  });

  it("leaves the host's own word on the main checkout, and the workspace's is_main as it came", () => {
    const said = only([agent("fix", "w-app", "/code/app")], [herdr("w-app", "/code/app", { is_main: false })]);
    expect(said.worktrees.map((w) => [w.name, w.main])).toEqual([["app", false]]);
    // The mark is the tree's: actions keep reading what the host said.
    const inferred = only([agent("fix", "w-app", "/code/app")], [herdr("w-app", "/code/app")]);
    expect(inferred.worktrees[0]!.main).toBe(true);
    expect(inferred.worktrees[0]!.workspace!.is_main).toBeUndefined();
  });

  it("does not mark a project's folders, which name no main checkout (#14497), and disambiguates multi-root project names", () => {
    // The Codex app and Claude Desktop name a project on every row and say nothing of a main checkout: each folder of a
    // project is its own row, with that folder as the row's project_path. The section is the project's, and its first
    // folder is no more the main checkout than the others.
    const folder = (id: string, source: string, project: string, cwd: string, patch: Partial<HostWorkspace> = {}): HostWorkspace =>
      herdr(id, cwd, { source, source_id: project, project_id: project, project_name: "app", project_path: cwd, title: "app", ...patch });
    const marks = (tree: RepoTree) => tree.worktrees.map((w) => [w.workspace!.cwd, w.name, w.main]);
    const codex = only([], [folder("c-app", "codex_app", "codex-project:p1", "/code/app"), folder("c-docs", "codex_app", "codex-project:p1", "/code/docs")], { showEmptyWorkspaces: true });
    expect(marks(codex)).toEqual([["/code/app", "app", false], ["/code/docs", "docs", false]]);
    const claude = only([], [folder("d-web", "claude_desktop", "env-1", "/code/web"), folder("d-api", "claude_desktop", "env-1", "/code/api")], { showEmptyWorkspaces: true });
    expect(marks(claude)).toEqual([["/code/api", "api", false], ["/code/web", "web", false]]);
    // Colliding folder basenames add parent directories until unique; custom root titles remain unchanged.
    const colliding = only([], [
      folder("r1", "codex_app", "codex-project:p2", "/work/ios/client", { project_name: "Client", title: "Client" }),
      folder("r2", "codex_app", "codex-project:p2", "/work/web/client", { project_name: "Client", title: "Client" }),
      folder("r3", "codex_app", "codex-project:p2", "/work/docs", { project_name: "Client", title: "Client" }),
    ], { showEmptyWorkspaces: true });
    expect(colliding.worktrees.map((w) => w.name)).toEqual(["docs", "ios/client", "web/client"]);
    // Windows multi-root collisions also disambiguate cleanly.
    const win = only([], [
      folder("w1", "codex_app", "codex-project:p3", "C:\\Projects\\App", { project_name: "Client", title: "Client" }),
      folder("w2", "codex_app", "codex-project:p3", "D:\\Projects\\App", { project_name: "Client", title: "Client" }),
    ], { showEmptyWorkspaces: true });
    expect(win.worktrees.map((w) => w.name)).toEqual(["C:/Projects/App", "D:/Projects/App"]);
    // One folder alone is not marked either; a host's own is_main still is.
    expect(marks(only([], [folder("c-app", "codex_app", "codex-project:p1", "/code/app")], { showEmptyWorkspaces: true }))).toEqual([["/code/app", "app", false]]);
    expect(marks(only([], [folder("c-app", "codex_app", "codex-project:p1", "/code/app", { is_main: true })], { showEmptyWorkspaces: true }))).toEqual([["/code/app", "app", true]]);
    // Non-desktop hosts (orca, paseo) preserve their native workspace titles even when two titles match project_name.
    for (const source of ["orca", "paseo"]) {
      const host = only([], [
        herdr("w1", "/work/dir-one", { source, project_id: "prj-1", project_name: "app", title: "app" }),
        herdr("w2", "/work/dir-two", { source, project_id: "prj-1", project_name: "app", title: "app" }),
      ], { showEmptyWorkspaces: true });
      expect(host.worktrees.map((w) => w.name)).toEqual(["app", "app"]);
    }
  });

  it("does not mark a workspace whose folder it cannot tell", () => {
    // herdr reports a workspace's folder only when its panes agree; without one it is never the main checkout.
    const loose = only([agent("draft", "w-notes", "/code/notes")], [herdr("w-notes", "", { title: "notes" })]);
    expect(loose.worktrees.map((w) => w.main)).toEqual([false]);
  });
});
