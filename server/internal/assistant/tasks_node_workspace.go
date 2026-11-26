package assistant

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/repository"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/repositorywire"
	"github.com/nwverse/persona/server/internal/taskworkspace"
	"github.com/nwverse/persona/server/internal/workspacenaming"
)

type dispatchedWorkspacePlan struct {
	plan     *workspacenaming.Plan
	revision int64
	reuse    bool
}

func (t *PlanTask) nodeDispatchedWorkspace() bool {
	return t.Workspace != nil && t.Workspace.Mode == workspaceNew &&
		t.WorkspaceResolved != nil && t.WorkspaceResolved.OperationID != ""
}

func (t *PlanTask) usesPreparedWorkspace() bool {
	return t.backendWorkspace() || t.nodeDispatchedWorkspace()
}

// Preview a new dispatch without allocating a path. Only a negative capability
// answer permits the engine path. A failed lookup can be retried on the same node.
func (s *Service) previewDispatchedWorkspace(ctx context.Context, t *PlanTask, resolved *TaskWorkspaceResolved) (bool, error) {
	support, err := s.backendWorkspaceCapabilities(ctx, t.Machine, "", t.Cwd)
	if err != nil {
		if problem, ok := errors.AsType[*taskworkspace.Problem](err); ok && problem.Code == "unsupported" {
			return false, nil
		}
		return false, err
	}
	if support.Capability != taskworkspace.Capability || !support.LocalGit || !support.Start {
		return false, nil
	}
	naming, ok := s.store().(workspaceNamingStore)
	if !ok {
		return false, taskworkspace.Failure("store_unavailable", "plan", "", "", "")
	}
	settings, ok := s.savedSettings(ctx)
	if !ok {
		return false, taskworkspace.Failure("store_unavailable", "plan", "", "", "")
	}
	id := firstNonEmpty(t.TodoID, t.ReuseTodo, t.ReservedTodoID)
	source := workspacenaming.Source{
		Manager: s.managerIdentity(), TodoID: id, Title: t.Title, MachineID: t.Machine,
		Timezone: settings.Timezone, PlannedAt: time.Now().UTC(), Layout: repository.LayoutContainer, TaskBranch: t.Workspace.Branch,
	}
	var rules workspacenaming.Rules
	var rulesRevision int64
	var set repository.TaskSet
	if t.TodoID != "" || t.ReuseTodo != "" {
		todo, ok := s.todoNow(id)
		if !ok {
			return false, db.ErrTodoNotFound
		}
		source.Title = firstNonEmpty(todo.Title, t.Title)
		rec, err := naming.TodoWorkspaceRules(ctx, id)
		if err != nil {
			return false, err
		}
		rules, rulesRevision = rec.Rules, rec.Revision
		set, err = s.savedTodoRepositories(ctx, id)
		if err != nil {
			return false, err
		}
	}
	source.Rules, source.RuleSources = workspacenaming.Effective(settings.WorkspaceRules, rules)
	var selected []string
	for _, row := range set.Repositories {
		if row.SourceSnapshot.CloneURL != "" {
			selected = append(selected, row.RepoID)
		}
	}
	if len(selected) == 0 {
		var local taskworkspace.LocalSource
		if err := s.call(ctx, t.Machine, taskworkspace.LocalSourceMethod,
			taskworkspace.LocalSourceRequest{Cwd: t.Cwd, TaskBranch: t.Workspace.Branch}, &local); err != nil {
			return false, err
		}
		if local.Error != "" {
			return false, taskworkspace.Failure(string(local.Error), "source", "", "", t.Cwd)
		}
		if local.ExistingPath != "" {
			if t.Workspace.Branch == "" || !proto.NodeAbs(local.ExistingPath) {
				return false, taskworkspace.Failure("result_mismatch", "plan", "", "", local.ExistingPath)
			}
			resolved.Path, resolved.Branch = local.ExistingPath, t.Workspace.Branch
			t.dispatchedWorkspace = &dispatchedWorkspacePlan{reuse: true}
			return true, nil
		}
		if local.Repository != nil {
			source.Layout = repository.LayoutRoot
			source.Repositories, err = repositorywire.LocalSnapshot(source.Manager, id, *local.Repository)
		} else {
			if t.cwdStatus != nil && t.cwdStatus.Path != "" || t.Workspace.Branch != "" {
				return false, taskworkspace.Failure("source_unavailable", "source", "", "", t.Cwd)
			}
			if !support.EmptyRepositories {
				return false, nil
			}
			source.Repositories, err = repository.Snapshot(source.Manager, id, set.Revision, nil, nil)
		}
	} else {
		if len(selected) == 1 {
			source.Layout = repository.LayoutRoot
		}
		source.Repositories, err = repository.Snapshot(source.Manager, id, set.Revision, set.Repositories, selected)
	}
	if err != nil {
		return false, err
	}
	var preview workspacenaming.Preview
	if err := s.call(ctx, t.Machine, "workspace.naming.resolve", source, &preview); err != nil {
		return false, taskworkspace.Failure("node_unavailable", "plan", "", "", "")
	}
	if preview.Plan == nil {
		var codes []string
		for _, diagnostic := range preview.Diagnostics {
			if diagnostic.Severity == "error" {
				codes = append(codes, diagnostic.Code)
			}
		}
		return false, fmt.Errorf("workspace naming failed (%s): %w", strings.Join(codes, ", "), taskworkspace.Failure("naming_incomplete", "plan", "", "", ""))
	}
	if err := validateWorkspaceNamingPlan(*preview.Plan, source); err != nil {
		return false, err
	}
	resolved.Path = preview.Plan.RootPath
	for _, repo := range preview.Plan.Repositories {
		resolved.Repositories = append(resolved.Repositories, BackendLaunchRepository{RepositoryPlan: repo})
	}
	if len(preview.Plan.Repositories) == 1 {
		resolved.Branch = preview.Plan.Repositories[0].TaskBranch
	}
	t.dispatchedWorkspace = &dispatchedWorkspacePlan{plan: preview.Plan, revision: rulesRevision}
	return true, nil
}

func (t *PlanTask) dispatchedWorkspaceOperationID() string {
	return "op_" + taskworkspace.Hash([]string{t.TodoID, t.planID, strconv.Itoa(t.planIndex), "new"})[:24]
}

// A restart can land between saving the naming plan and updating the task.
// Read the frozen operation before probing the node or choosing a legacy checkout.
func (s *Service) recoverDispatchedWorkspace(ctx context.Context, t *PlanTask) error {
	if t.Workspace == nil || t.Workspace.Mode != workspaceNew || t.TodoID == "" || t.planID == "" || t.inPane != "" {
		return nil
	}
	if w := t.WorkspaceResolved; w != nil && (w.Created || w.Reused || w.OperationID != "") {
		return nil
	}
	if _, ok := s.store().(workspaceNamingStore); !ok {
		return nil
	}
	err := s.resolveSavedBackendPlan(ctx, t, t.TodoID, t.dispatchedWorkspaceOperationID(), false)
	if problem, ok := errors.AsType[*taskworkspace.Problem](err); ok && problem.Code == "not_found" {
		return nil
	}
	return err
}

func (s *Service) admitDispatchedWorkspace(ctx context.Context, t *PlanTask) error {
	if t.planID == "" || t.TodoID == "" {
		return taskworkspace.Failure("invalid_input", "admission", "", "", "")
	}
	op := t.WorkspaceResolved.OperationID
	if op == "" {
		op = t.dispatchedWorkspaceOperationID()
		naming, ok := s.store().(workspaceNamingStore)
		if !ok {
			return taskworkspace.Failure("store_unavailable", "plan", op, "", "")
		}
		draft := t.dispatchedWorkspace
		if _, err := naming.SaveWorkspaceNamingPlan(ctx, t.TodoID, op, draft.revision, *draft.plan); err != nil {
			return err
		}
		// Name the operation before any node side effect. A retry, including one
		// during a node rollback, must keep this choice and its frozen inputs.
		t.WorkspaceResolved.OperationID, t.MachinePolicy = op, machineFixed
		if err := s.patchPlanTask(ctx, t.planID, t.planIndex, func(pt *PlanTask) {
			pt.Workspace, pt.WorkspaceInferred = t.Workspace, t.WorkspaceInferred
			pt.WorkspaceResolved, pt.MachinePolicy = t.WorkspaceResolved, machineFixed
		}); err != nil {
			return err
		}
	}
	if err := s.resolveSavedBackendPlan(ctx, t, t.TodoID, op, false); err != nil {
		return err
	}
	support, err := s.backendWorkspaceCapabilities(ctx, t.Machine, op, t.Cwd)
	if err != nil {
		return err
	}
	if support.Capability != taskworkspace.Capability || !support.LocalGit || !support.Start {
		return taskworkspace.Failure("unsupported", "capability", op, "", t.Cwd)
	}
	return s.admitPreparedTaskWorkspace(ctx, t, op)
}
