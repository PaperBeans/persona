package assistant

import (
	"context"
	"path"
	"strings"

	"github.com/nwverse/persona/server/internal/proto"
)

func standingDirectoryContains(scope, cwd string) bool {
	return scope == cwd || strings.HasPrefix(cwd, scope+"/")
}

// standingScopeMatches uses the node's local Git metadata, never a card's repository name or path basename.
// Repository subdirectory grants retain that relative boundary in another linked worktree. Independent clones require separate grants.
func (s *Service) standingScopeMatches(ctx context.Context, machine, directory, cwd string) (bool, error) {
	if !validStandingDirectory(directory) || !path.IsAbs(cwd) || strings.Contains(cwd, "\\") {
		return false, nil
	}
	cwd = path.Clean(cwd)
	if cwd == directory {
		return true, nil
	}
	var grant, task proto.RepositoryIdentity
	read := func(dir string, out *proto.RepositoryIdentity) bool {
		return s.call(ctx, machine, "git.status", map[string]any{"cwd": dir, "identity_only": true}, out) == nil && out.Version == 1 && path.IsAbs(out.Cwd)
	}
	if !read(directory, &grant) || !read(cwd, &task) {
		// A path below the grant may be a nested independent repository. Without
		// identities, even lexical containment cannot prove a cross-directory scope.
		return false, nil
	}
	if !grant.IsRepo {
		return standingDirectoryContains(grant.Cwd, task.Cwd), nil
	}
	if !task.IsRepo || !path.IsAbs(grant.Root) || !path.IsAbs(task.Root) {
		return false, nil
	}
	same := grant.CommonDir != "" && grant.CommonDir == task.CommonDir
	if !same {
		return false, nil
	}
	relative := strings.TrimPrefix(grant.Cwd, grant.Root)
	if grant.Cwd != grant.Root && !strings.HasPrefix(grant.Cwd, grant.Root+"/") {
		return false, nil
	}
	return standingDirectoryContains(path.Clean(task.Root+relative), task.Cwd), nil
}
