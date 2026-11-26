export class AgentRepositoryPlace {
  readonly repo: string;
  readonly checkout: string;
  readonly main: boolean;
  constructor(repo: string, checkout: string, main: boolean) {
    this.repo = repo; this.checkout = checkout; this.main = main;
  }
}

/** Prefer the node's Git information. Older nodes expose only their working directory. */
export function agentRepositoryPlace(cwdPath: string, worktreePath: string, repositoryPath: string): AgentRepositoryPlace {
  const trim = (path: string): string => path.replace(/\/+$/, '') || path;
  const cwd = trim(cwdPath), tree = trim(worktreePath), repo = trim(repositoryPath);
  if (tree) return new AgentRepositoryPlace(repo || tree, tree, !repo || repo === tree);
  if (repo) {
    const main = !cwd || cwd === repo || cwd.startsWith(repo + '/');
    return new AgentRepositoryPlace(repo, main ? repo : cwd, main);
  }
  for (const marker of ['/.worktrees/', '/.claude/worktrees/', '/.herdr/worktrees/']) {
    const at = cwd.indexOf(marker);
    if (at <= 0) continue;
    const parts = cwd.slice(at + marker.length).split('/').filter((part: string) => !!part);
    const root = parts.length > 1 ? cwd.slice(0, at) + marker + parts[0] : cwd.slice(0, at);
    return new AgentRepositoryPlace(root, cwd, false);
  }
  return new AgentRepositoryPlace(cwd, cwd, true);
}
