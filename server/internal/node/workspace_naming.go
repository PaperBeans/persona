package node

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/nwverse/persona/server/internal/workspacenaming"
)

func init() { nodeRPC("workspace.naming.resolve", (*Node).resolveWorkspaceNaming) }

func (n *Node) resolveWorkspaceNaming(ctx context.Context, raw json.RawMessage) (any, error) {
	var source workspacenaming.Source
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, err
	}
	preview := workspacenaming.Resolve(source, workspacenaming.NodeContext{Home: n.home, PersonaHome: n.dir})
	if preview.Plan == nil {
		return preview, nil
	}
	if problem := n.workspacePlanPathProblem(preview.Plan); problem != nil {
		preview.Plan = nil
		preview.Status = "invalid"
		preview.Diagnostics = append(preview.Diagnostics, *problem)
		return preview, nil
	}
	for _, repo := range preview.Plan.Repositories {
		checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := exec.CommandContext(checkCtx, "git", "check-ref-format", "--branch", repo.TaskBranch).Run()
		cancel()
		if err != nil {
			code := "git_validation_unavailable"
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				code = "branch_invalid"
			}
			preview.Plan = nil
			preview.Status = "invalid"
			preview.Diagnostics = append(preview.Diagnostics, workspacenaming.Problem{Code: code, Field: "branch_template", RepoID: repo.RepoID, Severity: "error"})
			break
		}
	}
	return preview, nil
}

// The same path policy runs before preview and preparation. Inspect resolved
// paths first to preserve diagnostics for links into private or credential paths.
func (n *Node) workspacePlanPathProblem(plan *workspacenaming.Plan) *workspacenaming.Problem {
	scopeDenied := func(repoID string) *workspacenaming.Problem {
		return &workspacenaming.Problem{Code: "base_scope_denied", Field: "base_directory", RepoID: repoID, Severity: "error"}
	}
	state := n.dir
	if state == "" {
		state = filepath.Join(n.home, ".persona")
	}
	state, err := n.realPath(state)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return scopeDenied("")
	}
	check := func(path, field, repoID string) *workspacenaming.Problem {
		real, err := n.realPath(path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) || n.offLimitsResolved(real) != nil || workspacenaming.InPrivateState(real, state) {
			return scopeDenied(repoID)
		}
		switch err = workspaceNoLinks(path); {
		case errors.Is(err, errWorkspaceSymlink):
			return &workspacenaming.Problem{Code: "path_symlink", Field: field, RepoID: repoID, Severity: "error"}
		case err != nil:
			return scopeDenied(repoID)
		}
		return nil
	}
	if problem := check(plan.BasePath, "base_directory", ""); problem != nil {
		return problem
	}
	if problem := check(plan.RootPath, "task_directory_template", ""); problem != nil {
		return problem
	}
	for _, repo := range plan.Repositories {
		if repo.AbsolutePath == plan.RootPath {
			continue
		}
		if problem := check(repo.AbsolutePath, "directory_alias", repo.RepoID); problem != nil {
			return problem
		}
	}
	return nil
}
