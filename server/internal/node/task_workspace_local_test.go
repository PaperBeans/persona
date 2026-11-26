package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	repository "github.com/nwverse/persona/server/internal/repositorywire"
	"github.com/nwverse/persona/server/internal/taskworkspace"
	"github.com/nwverse/persona/server/internal/workspacenaming"
)

func localWorkspaceSource(t *testing.T, n *Node, cwd, branch string) taskworkspace.LocalSource {
	t.Helper()
	raw, err := json.Marshal(taskworkspace.LocalSourceRequest{Cwd: cwd, TaskBranch: branch})
	if err != nil {
		t.Fatal(err)
	}
	out, err := n.handle(t.Context(), taskworkspace.LocalSourceMethod, raw)
	if err != nil {
		t.Fatal(err)
	}
	return out.(taskworkspace.LocalSource)
}

func localWorkspacePlan(t *testing.T, n *Node, source taskworkspace.LocalSource, branch string) workspacenaming.Plan {
	t.Helper()
	if source.Error != "" || source.Repository == nil {
		t.Fatalf("source lookup: %+v", source)
	}
	s := workspacePlan(t, n.home, repository.LayoutContainer).Source
	s.Layout, s.TaskBranch = repository.LayoutRoot, branch
	s.Rules = workspacenaming.Rules{BaseDirectory: "{home}/workspace", TaskDirectoryTemplate: "isolated/{task-id}", BranchTemplate: "review/{base-branch}/{task-id}"}
	var err error
	s.Repositories, err = repository.LocalSnapshot(s.Manager, s.TodoID, *source.Repository)
	if err != nil {
		t.Fatal(err)
	}
	got := namingGuardPreview(t, n, s)
	if got.Plan == nil {
		t.Fatal(got)
	}
	return *got.Plan
}

func TestWorkspaceLocalSourceAcceptsPlainFolderWithoutGit(t *testing.T) {
	home := workspaceTemp(t)
	n := &Node{home: home, dir: filepath.Join(home, ".persona")}
	t.Setenv("PATH", workspaceTemp(t))
	if got := localWorkspaceSource(t, n, home, ""); got != (taskworkspace.LocalSource{}) {
		t.Fatalf("expected a repository-free source, got %+v", got)
	}
}

func TestWorkspaceLocalSourceRefusesBrokenCheckout(t *testing.T) {
	home := workspaceTemp(t)
	n := &Node{home: home, dir: filepath.Join(home, ".persona")}
	checkout := filepath.Join(home, "checkout")
	child := filepath.Join(checkout, "child")
	if err := os.MkdirAll(child, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, ".git"), []byte("gitdir: missing-git-directory\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := localWorkspaceSource(t, n, child, ""); got.Error != repository.LocalSourceUnavailable || got.Repository != nil {
		t.Fatalf("expected an unavailable checkout, got %+v", got)
	}
}

func TestLocalTaskWorkspaceFromUnpushedCommit(t *testing.T) {
	home := workspaceTemp(t)
	n := &Node{home: home, dir: filepath.Join(home, ".persona")}
	repo := workspaceRemote(t, "trunk")
	checkout := filepath.Join(workspaceTemp(t), "older-checkout")
	workspaceGit(t, repo, "worktree", "add", "-b", "feature/base", "--", checkout)
	workspaceGit(t, checkout, "remote", "add", "origin", filepath.Join(workspaceTemp(t), "absent-remote"))
	if err := os.Mkdir(filepath.Join(checkout, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "child", "local.txt"), []byte("Local commit."), 0600); err != nil {
		t.Fatal(err)
	}
	workspaceGit(t, checkout, "add", ".")
	workspaceGit(t, checkout, "commit", "-m", "Unpushed work")
	head := strings.TrimSpace(workspaceGit(t, checkout, "rev-parse", "HEAD"))
	src := localWorkspaceSource(t, n, filepath.Join(checkout, "child"), "")
	plan := localWorkspacePlan(t, n, src, "")
	root := filepath.Join(home, "workspace", "isolated", "todo_example")
	branch := "review/feature-base/todo_example"
	if plan.RootPath != root || plan.Repositories[0].TaskBranch != branch || src.Repository.Local.Commit != head {
		t.Fatal(plan, src)
	}
	// Advance HEAD after planning. Preparation must use the frozen commit.
	workspaceGit(t, checkout, "commit", "--allow-empty", "-m", "Advance local branch")
	req := taskworkspace.Request{WorkspaceID: "ws_local", OperationID: "op_local", Plan: plan}
	ready := workspaceCall(t, n, "prepare", req)
	if ready.State != "ready" || len(ready.Repositories) != 1 || ready.Repositories[0].ResolvedCommit != head ||
		ready.Repositories[0].CommonDir != filepath.Join(repo, ".git") || strings.TrimSpace(workspaceGit(t, root, "rev-parse", "HEAD")) != head {
		t.Fatal(ready)
	}
	if _, err := os.Stat(filepath.Join(home, ".herdr")); !os.IsNotExist(err) {
		t.Fatal("engine checkout path was used", err)
	}
	if _, err := os.Stat(filepath.Join(n.dir, "task-workspaces", taskworkspace.Hash(req.WorkspaceID), "git")); !os.IsNotExist(err) {
		t.Fatal("local checkout allocated a private clone", err)
	}
	if err := n.guardBackendDeletion(root); err == nil {
		t.Fatal("prepared checkout lacked deletion guard")
	}
	if err := n.guardBackendDeletion(repo); err != nil {
		t.Fatal("source checkout was treated as owned by the task", err)
	}
	if _, err := n.worktreeRemove(t.Context(), "", root, true, true); err == nil || !strings.Contains(err.Error(), "task_workspace_resource_conflict") {
		t.Fatal("ordinary worktree deletion bypassed the ownership receipt", err)
	}
	// The new checkout remains owned when the previous checkout is removed and the node restarts.
	workspaceGit(t, repo, "worktree", "remove", "--", checkout)
	n = &Node{home: home, dir: n.dir}
	if got := workspaceCall(t, n, "prepare", req); got.State != "ready" || got.Error != nil {
		t.Fatal(got)
	}
	workspaceGit(t, root, "commit", "--allow-empty", "-m", "Task work")
	req.DeleteBranch = true
	if got := workspaceCall(t, n, "cleanup", req); got.Error == nil || got.Error.Code != "worktree_unmerged" {
		t.Fatal(got)
	}
	req.DeleteBranch = false
	if got := workspaceCall(t, n, "cleanup", req); got.State != "removed" || got.Error != nil {
		t.Fatal(got)
	}
	if got := strings.TrimSpace(workspaceGit(t, repo, "log", "-1", "--format=%s", "refs/heads/"+branch)); got != "Task work" {
		t.Fatal(got)
	}
}

func TestLocalTaskWorkspacePreservesAnExplicitExistingBranch(t *testing.T) {
	home := workspaceTemp(t)
	n := &Node{home: home, dir: filepath.Join(home, ".persona")}
	repo := workspaceRemote(t, "trunk")
	workspaceGit(t, repo, "branch", "topic/keep")
	src := localWorkspaceSource(t, n, repo, "topic/keep")
	if src.Repository == nil || !src.Repository.Local.ExistingBranch {
		t.Fatal(src)
	}
	plan := localWorkspacePlan(t, n, src, "topic/keep")
	req := taskworkspace.Request{WorkspaceID: "ws_local", OperationID: "op_local", Plan: plan}
	ready := workspaceCall(t, n, "prepare", req)
	if ready.State != "ready" || ready.Repositories[0].TaskBranch != "topic/keep" {
		t.Fatal(ready)
	}
	if got := localWorkspaceSource(t, n, repo, "topic/keep"); got.ExistingPath != plan.RootPath {
		t.Fatal(got)
	}
	req.DeleteBranch, req.Force = true, true
	if got := workspaceCall(t, n, "cleanup", req); got.State != "removed" || got.Repositories[0].BranchDeleted {
		t.Fatal(got)
	}
	workspaceGit(t, repo, "show-ref", "--verify", "refs/heads/topic/keep")
}
