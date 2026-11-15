package node

import (
	"context"
	"strings"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// Branch lookup is inspection only. General worktree creation still refuses duplicate checkouts.
func (n *Node) worktreePrepareBranch(ctx context.Context, kind, workspace, cwd, branch string) (proto.WorktreePrepare, error) {
	prep, err := n.worktreePrepare(ctx, kind, workspace, cwd)
	if err != nil || branch == "" {
		return prep, err
	}
	if err := checkBranch(ctx, prep.Root, branch); err != nil {
		return prep, err
	}
	at, err := branchCheckedOutAt(ctx, prep.Root, branch)
	if err != nil {
		return prep, err
	}
	if at == "" {
		return prep, nil
	}
	unverified := func(err error) error {
		return composerError(ctx, "checkout_unverified", i18n.Args{"branch": branch, "path": at, "reason": gitFailure(err)})
	}
	actual, err := repoAt(ctx, at)
	if err != nil {
		return prep, unverified(err)
	}
	common, sourceErr := absGit(ctx, prep.Root, "--git-common-dir")
	other, targetErr := absGit(ctx, at, "--git-common-dir")
	if sourceErr != nil || targetErr != nil || actual.branch != branch ||
		realPath(actual.root) != realPath(at) || realPath(common) != realPath(other) {
		return prep, composerError(ctx, "checkout_changed", i18n.Args{"branch": branch, "path": at})
	}
	if err := n.refreshHostInventory(ctx, true); err != nil {
		return prep, unverified(err)
	}
	n.mu.Lock()
	owner := ""
	for id, a := range n.agents {
		if a.AgentStatus == proto.StatusOffline {
			continue
		}
		for _, dir := range []string{a.Cwd, a.ForegroundCwd} {
			if dir != "" && within(realPath(dir), realPath(at)) {
				owner = id
				if name := firstOf(a.Name, a.Title); name != "" {
					owner = name + " (" + id + ")"
				}
				break
			}
		}
		if owner != "" {
			break
		}
	}
	n.mu.Unlock()
	if owner != "" {
		return prep, composerError(ctx, "checkout_in_use", i18n.Args{"branch": branch, "path": at, "owner": owner})
	}
	out, err := git(ctx, at, "status", "--porcelain", "--untracked-files=normal", "--ignore-submodules=none")
	if err != nil {
		return prep, unverified(err)
	}
	if strings.TrimSpace(out) != "" {
		return prep, composerError(ctx, "checkout_dirty", i18n.Args{"branch": branch, "path": at})
	}
	prep.Existing = &proto.WorktreeCheckout{Path: at, Branch: branch}
	return prep, nil
}
