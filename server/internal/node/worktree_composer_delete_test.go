package node

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/orcart/orcarttest"
)

var composerTestCapabilities = []string{"worktree.open-existing-branch.v2-request.v1", "worktree.create.exact-name.v1", "worktree.set.folder-parent.v1",
	"worktree.remove-branch-policy.v1", "worktree.archive-failure-blocking.v1", "folder-workspace.unique-id-mutation.v1"}

type deleteFixture struct {
	*orcaWorktreeFixture
	id, path    string
	main, child map[string]any
}

func deletionNode(t *testing.T) *deleteFixture {
	t.Helper()
	w := composerNode(t)
	path := filepath.Join(w.dir, "disposable")
	runPullGit(t, w.repo, "worktree", "add", "-b", "disposable", path, "main")
	id := "repo1::" + path
	main := orcarttest.Worktree(w.src, w.repo, "dev")
	main["isMainWorktree"], main["worktreeInstanceId"] = true, "main-instance"
	child := orcarttest.Worktree(id, path, "disposable")
	child["isMainWorktree"], child["worktreeInstanceId"], child["hostId"] = false, "child-instance", "local"
	w.o.SetWorktrees(main, child)
	return &deleteFixture{orcaWorktreeFixture: w, id: id, path: path, main: main, child: child}
}

func (w *deleteFixture) preview(t *testing.T) string {
	t.Helper()
	res, err := request(t, w.n, "worktree.delete", map[string]any{"workspace_id": w.id, "kind": "orca", "preview": true})
	if err != nil || res["instance_id"] != "child-instance" || res["host_id"] != "local" || res["workspace_id"] != w.id {
		t.Fatalf("preview: %v %v", res, err)
	}
	return str(res["preview_token"])
}

func (w *deleteFixture) confirm(t *testing.T, token string, extra map[string]any) (map[string]any, error) {
	t.Helper()
	if extra == nil {
		extra = map[string]any{}
	}
	extra["workspace_id"], extra["kind"], extra["preview_token"] = w.id, "orca", token
	return request(t, w.n, "worktree.delete", extra)
}

func TestWorktreeDeletionKeepsBranchesAndRequiresActualCompletion(t *testing.T) {
	w := deletionNode(t)
	if err := os.WriteFile(filepath.Join(w.path, "untracked.txt"), []byte("reviewed"), 0600); err != nil {
		t.Fatal(err)
	}
	token := w.preview(t)
	if _, err := w.confirm(t, token, nil); err == nil {
		t.Fatal("dirty removal did not require confirmation")
	}
	w.o.Handle("worktree.rm", func(p map[string]any) any {
		if p["worktree"] != "id:"+w.id || p["hostId"] != "local" || p["deleteBranch"] != false ||
			p["runHooks"] != true || p["waitForBackgroundRemoval"] != true || p["force"] != true ||
			p["allowFailedArchiveHook"] != nil || p["allowUnverifiedPtyStop"] != nil {
			t.Errorf("unsafe remove: %v", p)
		}
		runPullGit(t, w.repo, "worktree", "remove", "--force", w.path)
		w.o.SetWorktrees(w.main)
		return map[string]any{"removed": true}
	})
	res, err := w.confirm(t, token, map[string]any{"force": true})
	if err != nil || res["removed"] != true || res["state"] != "removed" || !branchExists(t.Context(), w.repo, "disposable") {
		t.Fatalf("completed removal: %v %v", res, err)
	}
	if _, err := w.confirm(t, token, nil); err == nil {
		t.Fatal("reused dispatched token")
	}
	res, err = w.confirm(t, token, map[string]any{"reconcile": true})
	if err != nil || res["removed"] != true || w.o.Count("worktree.rm") != 1 {
		t.Fatalf("reconcile: %v %v", res, err)
	}
}

func TestWorktreeDeletionRefusesStaleOrUnsafePreview(t *testing.T) {
	for _, scenario := range []string{"instance", "host", "branch", "filesystem", "dirty", "dirty-content", "head", "main", "children", "ambiguous", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			w := deletionNode(t)
			if scenario == "dirty-content" {
				if err := os.WriteFile(filepath.Join(w.path, "note"), []byte("old"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			token := w.preview(t)
			switch scenario {
			case "instance":
				w.child["worktreeInstanceId"] = "replacement"
			case "host":
				w.child["hostId"] = "ssh:other"
			case "branch":
				w.child["branch"] = "another"
			case "filesystem":
				if err := os.Rename(w.path, w.path+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(w.path, 0700); err != nil {
					t.Fatal(err)
				}
			case "dirty", "dirty-content":
				if err := os.WriteFile(filepath.Join(w.path, "note"), []byte("new"), 0600); err != nil {
					t.Fatal(err)
				}
			case "head":
				runPullGit(t, w.path, "commit", "--allow-empty", "-m", "new commit after preview")
			case "main":
				w.child["isMainWorktree"] = true
			case "children":
				child := orcarttest.Worktree("other-project::child", "/example/child", "")
				child["parentWorktreeId"], child["hostId"] = w.id, "local"
				w.o.SetWorktrees(w.main, w.child, child)
			case "ambiguous":
				w.o.SetWorktrees(w.main, w.child, w.child)
			case "expired":
				composerDeletions.Lock()
				composerDeletions.previews[token].created = time.Now().Add(-11 * time.Minute)
				composerDeletions.Unlock()
			}
			if scenario != "children" && scenario != "ambiguous" {
				w.o.SetWorktrees(w.main, w.child)
			}
			if _, err := w.confirm(t, token, map[string]any{"force": true}); err == nil || w.o.Count("worktree.rm") != 0 {
				t.Fatalf("unsafe confirmation reached runtime: %v", err)
			}
		})
	}
}

func TestWorktreeDeletionTokenBindsNodeTargetAndMode(t *testing.T) {
	w := deletionNode(t)
	token := w.preview(t)
	for _, body := range []map[string]any{
		{"workspace_id": w.id, "kind": "orca"},
		{"workspace_id": w.src, "kind": "orca", "preview_token": token},
		{"workspace_id": w.id, "kind": "herdr", "preview_token": token},
		{"workspace_id": w.id, "kind": "orca", "preview_token": token, "preview": true},
	} {
		if _, err := request(t, w.n, "worktree.delete", body); err == nil {
			t.Fatalf("accepted %v", body)
		}
	}
	other := composerNode(t)
	if _, err := request(t, other.n, "worktree.delete", map[string]any{"workspace_id": w.id, "kind": "orca", "preview_token": token}); err == nil {
		t.Fatal("token crossed nodes")
	}
	if w.o.Count("worktree.rm") != 0 {
		t.Fatal("invalid token dispatched")
	}
}

func TestWorktreeDeletionRequiresSafetyCapabilitiesAndInstance(t *testing.T) {
	for _, missing := range []string{"worktree.remove-branch-policy.v1", "worktree.archive-failure-blocking.v1", "instance", "main-unknown"} {
		t.Run(missing, func(t *testing.T) {
			w := deletionNode(t)
			caps := []string{}
			for _, cap := range composerTestCapabilities {
				if cap != missing {
					caps = append(caps, cap)
				}
			}
			w.o.SetStatus(map[string]any{"capabilities": caps})
			if missing == "instance" {
				delete(w.child, "worktreeInstanceId")
			}
			if missing == "main-unknown" {
				delete(w.child, "isMainWorktree")
			}
			w.o.SetWorktrees(w.main, w.child)
			if _, err := request(t, w.n, "worktree.delete", map[string]any{"workspace_id": w.id, "kind": "orca", "preview": true}); err == nil {
				t.Fatal("unsafe preview advertised")
			}
		})
	}
}

func TestWorktreeDeletionUnknownAndBackgroundOnlyReconcile(t *testing.T) {
	for _, outcome := range []string{"background", "timeout", "false-completion", "catalog-only", "archive-failure"} {
		t.Run(outcome, func(t *testing.T) {
			w := deletionNode(t)
			token := w.preview(t)
			w.o.Handle("worktree.rm", func(p map[string]any) any {
				switch outcome {
				case "background":
					return map[string]any{"removing": true}
				case "timeout":
					return orcarttest.Fail{Code: "runtime_timeout"}
				case "catalog-only":
					w.o.SetWorktrees(w.main)
					return map[string]any{"removed": true}
				case "archive-failure":
					return orcarttest.Fail{Code: "archive_hook_failed"}
				default:
					return map[string]any{"removed": true}
				}
			})
			res, err := w.confirm(t, token, map[string]any{"force": true})
			if outcome == "archive-failure" {
				if err == nil {
					t.Fatal("archive failure was hidden")
				}
			} else if err != nil || res["removed"] != false || res["state"] == "removed" {
				t.Fatalf("acceptance reported completion: %v %v", res, err)
			}
			if _, err := w.confirm(t, token, nil); err == nil {
				t.Fatal("destructive retry permitted")
			}
			runPullGit(t, w.repo, "worktree", "remove", w.path)
			w.o.SetWorktrees(w.main)
			res, err = w.confirm(t, token, map[string]any{"reconcile": true})
			if err != nil || res["removed"] != true || w.o.Count("worktree.rm") != 1 {
				t.Fatalf("reconcile: %v %v", res, err)
			}
		})
	}
}

func TestWorktreeDeletionFolderUnregisterKeepsDirectory(t *testing.T) {
	for _, scoped := range []bool{false, true} {
		t.Run(map[bool]string{false: "folder-project-child", true: "folder-workspace"}[scoped], func(t *testing.T) {
			w := deletionNode(t)
			if scoped {
				w.id = "folder:sample"
				w.child["worktreeId"] = w.id
			}
			w.child["workspaceKind"], w.child["branch"] = "folder-workspace", ""
			if !scoped {
				w.child["workspaceKind"] = "git"
				w.o.Handle("repo.show", func(p map[string]any) any { return map[string]any{"repo": map[string]any{"kind": "folder"}} })
			}
			w.o.SetWorktrees(w.main, w.child)
			token := w.preview(t)
			method := "worktree.rm"
			if scoped {
				method = "folderWorkspace.delete"
			}
			w.o.Handle(method, func(p map[string]any) any {
				if scoped && (p["folderWorkspaceId"] != "sample" || p["requireUniqueId"] != true) {
					t.Errorf("unscoped folder mutation: %v", p)
				}
				w.o.SetWorktrees(w.main)
				if scoped {
					return map[string]any{"deleted": true}
				}
				return map[string]any{"removed": true}
			})
			res, err := w.confirm(t, token, nil)
			if err != nil || res["removed"] != true {
				t.Fatalf("folder unregister: %v %v", res, err)
			}
			if _, err := os.Stat(w.path); err != nil {
				t.Fatal("folder directory removed", err)
			}
		})
	}
}

func TestWorktreeDeletionExplicitBranchDeletionAndMainCheckoutGuard(t *testing.T) {
	w := deletionNode(t)
	w.main["isMainWorktree"] = false // Even incorrect runtime metadata cannot authorize deleting Git's main checkout.
	w.main["hostId"] = "local"
	w.o.SetWorktrees(w.main, w.child)
	if _, err := request(t, w.n, "worktree.delete", map[string]any{"workspace_id": w.src, "kind": "orca", "preview": true}); err == nil {
		t.Fatal("Git main checkout accepted")
	}
	token := w.preview(t)
	w.o.Handle("worktree.rm", func(p map[string]any) any {
		if p["deleteBranch"] != true {
			t.Error("explicit branch deletion was dropped", p)
		}
		runPullGit(t, w.repo, "worktree", "remove", w.path)
		runPullGit(t, w.repo, "branch", "-D", "disposable")
		w.o.SetWorktrees(w.main)
		return map[string]any{"removed": true, "branchDeleted": true}
	})
	res, err := w.confirm(t, token, map[string]any{"delete_branch": true})
	if err != nil || res["removed"] != true || res["branch_deleted"] != true || branchExists(t.Context(), w.repo, "disposable") {
		t.Fatalf("branch deletion result: %v %v", res, err)
	}
}
