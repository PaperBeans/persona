import { baseName, pathSlashes } from "./format";
import type { ChildStates } from "./herdr-tree";
import type { Machine, Session } from "./types";
import type { HostWorkspace, WorkspaceTarget } from "./workspace-catalog";

/** Where a session's cwd sits: the repository it belongs to and the working tree inside it. `apart`: the cwd has the
 *  shape of a tree in a folder of trees kept apart from its repository (`~/.herdr/worktrees/<repository>/<tree>`), and
 *  `repo` is that folder, which names the repository without being its checkout. Only such a folder at the computer's
 *  home is one (joinApart): a repository can keep its own trees in a `.worktrees` folder, and a cwd in a folder of one
 *  of those has the same shape. */
export type Repo = { repo: string; name: string; worktree: string; main: boolean; apart?: boolean };

export type RepoWorktree = {
  key: string;
  /** Its branch when the node says (#1805), else its folder's name (the main checkout: the repository's). A linked tree
   *  goes by the name its host gave the place instead, when its sessions agree on one (#4497, `hostPlaceName`). */
  name: string;
  /** The branch checked out there, when the node says. */
  branch?: string;
  path: string;
  main: boolean;
  sessions: Session[];
  /** Direct navigation candidates, including pinned Agents rendered elsewhere. */
  directSessions?: Session[];
  /** Direct navigation candidates throughout the subtree, including pinned Agents. */
  navigationTree?: Session[];
  workspace?: HostWorkspace;
  target?: WorkspaceTarget;
  /** Newest host pin in this visible subtree; ordering only, ancestors do not acquire a pin. */
  latestPinTime?: number;
  /** Orca child worktrees, sorted by name. */
  children: RepoWorktree[];
  /** This worktree's own sessions, followed by every descendant's sessions. */
  tree: Session[];
  /** Direct child worktrees needing you, working, or done, by each child's whole-subtree summary. */
  childStates: ChildStates;
};

/** One repository on one machine, with its top-level working trees (the main checkout first, then by name). */
export type RepoTree = {
  key: string;
  machineId: string;
  name: string;
  repo: string;
  count: number;
  worktrees: RepoWorktree[];
  /** Newest host pin among this repository's visible workspaces. */
  latestPinTime?: number;
  /** Keyed by a folder of trees kept apart from the repository at the computer's home (`Repo.apart`, joinApart), not by
   *  the repository itself. */
  apart?: boolean;
};

/** One computer's section in the repository tree: its live repositories ordered by name. */
export interface RepoMachine {
  key: string;
  machine: Machine;
  foldKey: string;
  repos: RepoTree[];
  sessions: Session[];
  needs: number;
}

export interface RepoCatalogOptions {
  workspaces: readonly HostWorkspace[];
  owner: string;
  manager: string;
  /** Same filtered membership as all, plus pinned Agents (which are not drawn twice). */
  navigationSessions?: readonly Session[];
  hideEmpty?: boolean;
  /** Opt-in (default false): keep live available empty workspaces when not narrowed by a filter. */
  showEmptyWorkspaces?: boolean;
  search?: string;
  compareSessions?: (a: Session, b: Session) => number;
  hiddenSources?: readonly string[];
}

// The layouts a working tree commonly lives in (herdr, Claude Code, a home-level collection): the parts before the
// last one name the repository, everything from there on is the tree.
export const WORKTREE_SEGMENTS = [["/.worktrees/"], ["/.claude/worktrees/"], ["/.herdr/worktrees/"]];
// The ones that keep every repository's trees in a folder of its own at the home level (`~/.worktrees/<repository>/<tree>`,
// herdr's `~/.herdr/worktrees/<repository>/<tree>`). Claude Code keeps a repository's trees inside it.
export const APART_SEGMENTS = ["/.worktrees/", "/.herdr/worktrees/"];

export const trimmed = (value?: string) => {
  const path = pathSlashes(value ?? "");
  if (/^[a-z]:\/+$/i.test(path)) return `${path.slice(0, 2)}/`;
  return path.replace(/\/+$/, "") || (path.startsWith("/") ? "/" : "");
};
// Keep display spelling, but compare Windows directory identities without case or separator differences.
export const pathKey = (value?: string) => {
  const path = trimmed(value);
  return /^(?:[a-z]:\/|\/\/)/i.test(path) ? path.toLowerCase() : path;
};
export const withinPath = (path: string, root: string) => path === root || path.startsWith(root.endsWith("/") ? root : `${root}/`);

/**
 * The repository and working tree a cwd belongs to. The host may name the repository (the session's repo_root, #2840:
 * Paseo keeps its worktrees under its own folder): a cwd inside it is its main checkout, any other is one of its
 * working trees. Otherwise from the path alone (docs/web/web.md): a tree under one of the known layouts belongs to the
 * directory above that marker; any other path is its own repository's main checkout.
 */
export function repoOf(cwd: string, repoRoot?: string): Repo {
  if (!cwd) return { repo: "", name: "", worktree: "", main: true };
  const path = trimmed(cwd);
  const root = trimmed(repoRoot);
  if (root) {
    const inside = withinPath(pathKey(path), pathKey(root));
    return { repo: root, name: baseName(root), worktree: inside ? root : path, main: inside };
  }
  for (const [marker] of WORKTREE_SEGMENTS) {
    // Search the display path itself: Unicode lowercasing can change string length and invalidate slice offsets.
    const at = /^(?:[a-z]:\/|\/\/)/i.test(path)
      ? path.search(new RegExp(marker.replaceAll(".", "\\."), "i"))
      : path.indexOf(marker);
    if (at <= 0) continue;
    const rest = path.slice(at + marker.length).split("/").filter(Boolean);
    const repo = path.slice(0, at);
    // ~/.worktrees/<repo>/<tree>: the marker sits at the home level, so the repository is the first part after it and
    // the tree is what follows.
    if (rest.length >= 2) return { repo: `${repo}${marker}${rest[0]}`, name: rest[0]!, worktree: path, main: false, ...(APART_SEGMENTS.includes(marker) && { apart: true }) };
    return { repo, name: baseName(repo), worktree: path, main: false };
  }
  return { repo: path, name: baseName(path), worktree: path, main: true };
}

/** Whether By repo shows a worktree row, rather than putting a lone main or equivalently named worktree's agents
 *  directly under the project. A worktree with children always keeps its level, and so does a branch the node named
 *  (#1805: machine → project → branch → sessions, as Orca shows it). A host's workspace keeps it while its name says
 *  more than the repository's, or while it has no Agent to list in its place; one named after its folder, which is the
 *  repository's, only repeated that name (#13454), and its heading carries its marks and menu instead. */
export function showsWorktreeLevel(tree: RepoTree): boolean {
  if (tree.worktrees.length !== 1) return true;
  const w = tree.worktrees[0]!;
  if (w.children.length > 0 || w.branch) return true;
  if (w.workspace) return w.name !== tree.name || w.sessions.length === 0;
  return !w.main && w.name !== tree.name;
}
