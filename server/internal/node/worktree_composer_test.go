package node

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nwverse/persona/server/internal/orcart/orcarttest"
)

func composerNode(t *testing.T) *orcaWorktreeFixture {
	t.Helper()
	w := orcaWorktreeNode(t)
	w.o.Handle("repo.show", func(p map[string]any) any {
		return map[string]any{"repo": map[string]any{"displayName": "Example project", "worktreeBaseRef": "main"}}
	})
	w.o.Handle("repo.sparsePresets", func(p map[string]any) any {
		return map[string]any{"presets": []any{map[string]any{"id": "frontend", "name": "Frontend", "directories": []string{"src"}}}}
	})
	w.o.SetStatus(map[string]any{"capabilities": composerTestCapabilities})
	return w
}

func TestWorktreeComposerUsesRepositoryBaseAndRuntimeLocation(t *testing.T) {
	w := composerNode(t)
	prepared, err := request(t, w.n, "worktree.prepare", map[string]any{"kind": "orca", "workspace_id": w.src, "composer": true})
	if err != nil {
		t.Fatal(err)
	}
	options := prepared["composer"].(map[string]any)
	if options["base_ref"] != "main" || options["project"] != "Example project" {
		t.Fatal(prepared)
	}
	res, err := request(t, w.n, "worktree.create", map[string]any{"kind": "orca", "workspace_id": w.src, "auto_name": true, "composer": true,
		"name": "review", "no_parent": true, "note": "Investigate", "sparse_preset_id": "frontend"})
	if err != nil {
		t.Fatal(err)
	}
	p := w.o.Params("worktree.create")[0]
	if p["baseBranch"] != "main" || p["noParent"] != true || p["parentWorktree"] != nil || p["comment"] != "Investigate" || p["branchNameOverride"] != nil || p["nameWasGenerated"] != nil {
		t.Fatal(p)
	}
	if p["sparseCheckout"].(map[string]any)["presetId"] != "frontend" {
		t.Fatal(p)
	}
	if res["path"] != filepath.Join(w.dir, "review") || res["branch"] != "devuser/review" {
		t.Fatal(res)
	}
	if head := runPullGit(t, str(res["path"]), "rev-parse", "HEAD"); head == w.devHead {
		t.Fatal("sidebar parent must not implicitly choose the Git base")
	}
}

func TestWorktreeComposerRefusesInvalidChoicesBeforeCreate(t *testing.T) {
	w := composerNode(t)
	for _, extra := range []map[string]any{
		{"parent_workspace_id": "missing"}, {"no_parent": true, "parent_workspace_id": w.src}, {"base_ref": "missing"},
		{"branch_override": "dev"}, {"branch_override": "-bad"}, {"name": "../outside"}, {"sparse_preset_id": "missing"},
	} {
		extra["kind"], extra["workspace_id"], extra["composer"], extra["auto_name"] = "orca", w.src, true, true
		if _, err := request(t, w.n, "worktree.create", extra); err == nil {
			t.Fatalf("accepted %v", extra)
		}
	}
	if w.o.Count("worktree.create") != 0 {
		t.Fatal("invalid choices reached the runtime")
	}
}

func TestWorktreeComposerReusesAFreeLocalBranchWithoutChangingItsTip(t *testing.T) {
	w := composerNode(t)
	expected := runPullGit(t, w.repo, "rev-parse", "spare")
	res, err := request(t, w.n, "worktree.create", map[string]any{"kind": "orca", "workspace_id": w.src, "composer": true,
		"auto_name": true, "branch_override": "spare", "base_ref": "dev", "name": "reuse-spare"})
	if err != nil {
		t.Fatal(err)
	}
	if got := w.o.Params("worktree.create")[0]["baseBranch"]; got != "spare" {
		t.Fatalf("existing branch must be checked out as-is: %v", got)
	}
	if got := w.o.Params("worktree.create")[0]["branchIntent"]; got != "open-existing-v2" {
		t.Fatalf("existing branch must remain selected through directory-name retries: %v", got)
	}
	if res["branch"] != "spare" || runPullGit(t, str(res["path"]), "rev-parse", "HEAD") != expected {
		t.Fatal(res)
	}
}

func TestWorktreeComposerBranchOverrideKeepsFolderNamingAutomatic(t *testing.T) {
	w := composerNode(t)
	res, err := request(t, w.n, "worktree.create", map[string]any{"kind": "orca", "workspace_id": w.src, "composer": true,
		"auto_name": true, "branch_override": "feature/custom"})
	if err != nil {
		t.Fatal(err)
	}
	p := w.o.Params("worktree.create")[0]
	if p["nameWasGenerated"] != true || p["branchNameOverride"] != "feature/custom" || p["branchIntent"] != "create-exact" || p["name"] == "feature-custom" {
		t.Fatalf("a branch override must not become an implicit checkout path: %v", p)
	}
	if res["branch"] != "feature/custom" || res["path"] != filepath.Join(w.dir, str(p["name"])) {
		t.Fatal(res)
	}
}

func TestWorktreeComposerReturnsAcknowledgedRuntimeNames(t *testing.T) {
	w := composerNode(t)
	w.o.Handle("worktree.create", func(p map[string]any) any {
		return map[string]any{"worktree": map[string]any{"id": "repo1::/code/review-2", "path": "/code/review-2", "branch": "refs/heads/review-2"}}
	})
	res, err := request(t, w.n, "worktree.create", map[string]any{"kind": "orca", "workspace_id": w.src, "composer": true,
		"auto_name": true, "name": "review", "branch_override": "review"})
	if err != nil || res["workspace_id"] != "repo1::/code/review-2" || res["branch"] != "review-2" || res["path"] != "/code/review-2" {
		t.Fatalf("a confirmed workspace must not become an error that invites duplicate creation: %v %v", res, err)
	}
}

func TestWorktreeComposerBindsGitHubSelectionAndUsesResolvedPRHead(t *testing.T) {
	w := composerNode(t)
	w.o.Handle("github.workItem", func(p map[string]any) any {
		return map[string]any{"type": "pr", "number": 42, "title": "Review change", "url": "https://github.com/example/app/pull/42", "branchName": "review/head", "baseRefName": "main", "isCrossRepository": true}
	})
	w.o.Handle("worktree.resolvePrBase", func(p map[string]any) any {
		return map[string]any{"baseBranch": w.devHead, "branchNameOverride": "review/pr-42", "compareBaseRef": "main",
			"pushTarget": map[string]any{"remoteName": "origin", "branchName": "review/head"}}
	})
	body := map[string]any{"kind": "orca", "workspace_id": w.src, "composer": true, "auto_name": true, "source_id": "pr:42", "source_url": "https://github.com/example/other/pull/42"}
	if _, err := request(t, w.n, "worktree.create", body); err == nil || w.o.Count("worktree.create") != 0 {
		t.Fatal("a source from another repository was accepted")
	}
	body["source_url"] = "https://github.com/example/app/pull/42"
	res, err := request(t, w.n, "worktree.create", body)
	if err != nil {
		t.Fatal(err)
	}
	p := w.o.Params("worktree.create")[0]
	if p["linkedPR"] != float64(42) || p["baseBranch"] != w.devHead || p["branchNameOverride"] != "review/pr-42" || p["compareBaseRef"] != "main" || p["pushTarget"] == nil {
		t.Fatal(p)
	}
	if runPullGit(t, str(res["path"]), "rev-parse", "HEAD") != w.devHead {
		t.Fatal("the PR's resolved head was not checked out")
	}
}

func TestWorktreeComposerPresetValidationAndSave(t *testing.T) {
	w := composerNode(t)
	w.o.Handle("repo.saveSparsePreset", func(p map[string]any) any { return map[string]any{"preset": p} })
	for _, directories := range []string{"../secret", "/outside", "web/../../secret", "web/../.git", "C:\\outside"} {
		if _, err := request(t, w.n, "worktree.preset", map[string]any{"kind": "orca", "workspace_id": w.src, "name": "Frontend", "directories": directories}); err == nil {
			t.Fatalf("accepted unsafe preset: %s", directories)
		}
	}
	if _, err := request(t, w.n, "worktree.preset", map[string]any{"kind": "orca", "workspace_id": w.src, "name": " Frontend ", "directories": "web\nshared/components"}); err != nil {
		t.Fatal(err)
	}
	if w.o.Count("repo.saveSparsePreset") != 1 {
		t.Fatal("invalid directories reached Orca")
	}
	p := w.o.Params("repo.saveSparsePreset")[0]
	if p["repo"] != "id:repo1" || p["name"] != "Frontend" || len(p["directories"].([]any)) != 2 {
		t.Fatal(p)
	}
}

func TestWorktreeComposerRefusesUnsupportedModesWithoutLegacyFallback(t *testing.T) {
	w := composerNode(t)
	w.o.SetStatus(map[string]any{"capabilities": []string{"worktree.open-existing-branch.v2"}})
	for _, method := range []string{"worktree.prepare", "worktree.create", "worktree.source", "worktree.preset"} {
		if _, err := request(t, w.n, method, map[string]any{"workspace_id": w.src, "kind": "orca", "composer": true}); err == nil {
			t.Fatalf("unsupported %s accepted", method)
		}
	}
	for _, body := range []map[string]any{
		{"workspace_id": w.src, "kind": "orca", "auto_name": true, "base_ref": "main"},
		{"workspace_id": w.src, "kind": "orca", "composer": "true", "auto_name": true},
		{"workspace_id": w.src, "kind": "herdr", "composer": true},
	} {
		if _, err := request(t, w.n, "worktree.create", body); err == nil {
			t.Fatalf("fell back to legacy: %v", body)
		}
	}
	if w.o.Count("worktree.create") != 0 {
		t.Fatal("unsupported composer reached create")
	}
	// The original automatic request still works against a host without composer capabilities.
	if _, err := request(t, w.n, "worktree.create", map[string]any{"workspace_id": w.src, "auto_name": true}); err != nil {
		t.Fatal(err)
	}
}

func TestWorktreeComposerExistingBranchSurvivesDirectoryCollision(t *testing.T) {
	w := composerNode(t)
	if err := os.MkdirAll(filepath.Join(w.dir, "occupied"), 0700); err != nil {
		t.Fatal(err)
	}
	tip := runPullGit(t, w.repo, "rev-parse", "spare")
	res, err := request(t, w.n, "worktree.create", map[string]any{"workspace_id": w.src, "kind": "orca", "composer": true, "name": "occupied", "branch_override": "spare", "base_ref": "dev"})
	if err != nil || res["branch"] != "spare" || res["path"] != filepath.Join(w.dir, "occupied-2") {
		t.Fatalf("branch/path intent: %v %v", res, err)
	}
	if runPullGit(t, str(res["path"]), "rev-parse", "HEAD") != tip {
		t.Fatal("existing branch tip changed")
	}
}

func TestWorktreeComposerProjectAndParentAreIndependent(t *testing.T) {
	w := composerNode(t)
	parent := orcarttest.Worktree("other-project::collection", mkdir(t, filepath.Join(w.home, "collection")), "")
	parent["workspaceKind"], parent["hostId"] = "folder-workspace", "local"
	w.o.SetWorktrees(orcarttest.Worktree(w.src, w.repo, "dev"), parent)
	res, err := request(t, w.n, "worktree.create", map[string]any{"workspace_id": w.src, "kind": "orca", "composer": true, "name": "collection-child", "parent_workspace_id": parent["worktreeId"]})
	if err != nil || res["parent_workspace_id"] != parent["worktreeId"] {
		t.Fatalf("parent choice: %v %v", res, err)
	}
	p := w.o.Params("worktree.create")[0]
	if p["repo"] != "id:repo1" || p["baseBranch"] != "main" || p["parentWorktree"] != "id:other-project::collection" || p["matchFolderWorkspaces"] != true {
		t.Fatal(p)
	}
}

func TestWorktreeComposerSourceSearchPreservesColonQueries(t *testing.T) {
	w := composerNode(t)
	w.o.Handle("github.listWorkItems", func(p map[string]any) any {
		return map[string]any{"items": []any{map[string]any{"type": "issue", "number": 42, "title": "Search result"}}}
	})
	for _, tc := range []struct{ query, want string }{
		{"bug: login", "bug: login"},
		{"feat: nested worktrees", "feat: nested worktrees"},
		{"is:open login", "is:open login"},
		{"docs: https://github.com/example/app/pull/42", "docs: https://github.com/example/app/pull/42"},
		{"  fix: startup  ", "fix: startup"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			before := w.o.Count("github.listWorkItems")
			res, err := request(t, w.n, "worktree.source", map[string]any{"workspace_id": w.src, "kind": "orca", "query": tc.query})
			if err != nil || len(res["items"].([]any)) != 1 {
				t.Fatalf("source search %q: %v %v", tc.query, res, err)
			}
			calls := w.o.Params("github.listWorkItems")
			if len(calls) != before+1 || calls[before]["query"] != tc.want || calls[before]["repo"] != "id:repo1" {
				t.Fatalf("source search changed query or project: %v", calls)
			}
		})
	}
	if w.o.Count("github.workItem") != 0 {
		t.Fatal("keyword search must not resolve a numbered source")
	}
}

func TestWorktreeComposerSourceRejectsMalformedURLs(t *testing.T) {
	w := composerNode(t)
	w.o.Handle("github.listWorkItems", func(p map[string]any) any {
		return map[string]any{"items": []any{}}
	})
	for _, query := range []string{"https://%zz", "https://github.com/example/app/wiki/42"} {
		if _, err := request(t, w.n, "worktree.source", map[string]any{"workspace_id": w.src, "kind": "orca", "query": query}); err == nil {
			t.Fatalf("invalid source URL accepted: %q", query)
		}
	}
	if w.o.Count("github.listWorkItems") != 0 || w.o.Count("github.workItem") != 0 {
		t.Fatal("invalid source URL reached GitHub lookup")
	}
}

func TestWorktreeComposerSourcesAreRealAndRepositoryBound(t *testing.T) {
	w := composerNode(t)
	w.o.Handle("github.workItem", func(p map[string]any) any {
		return map[string]any{"type": "pr", "number": 42, "title": "Review", "url": "https://github.com/example/app/pull/42"}
	})
	for _, query := range []string{"42", "#42", "https://github.com/example/app/pull/42", "HTTPS://github.com/example/app/pull/42"} {
		res, err := request(t, w.n, "worktree.source", map[string]any{"workspace_id": w.src, "kind": "orca", "query": query})
		if err != nil || len(res["items"].([]any)) != 1 {
			t.Fatalf("source query %s: %v %v", query, res, err)
		}
	}
	if _, err := request(t, w.n, "worktree.source", map[string]any{"workspace_id": w.src, "kind": "orca", "query": "https://github.com/example/other/pull/42"}); err == nil {
		t.Fatal("foreign source URL accepted")
	}
	w.o.Handle("github.listWorkItems", func(p map[string]any) any {
		return map[string]any{"items": []any{}, "errors": map[string]any{"prs": map[string]any{"message": "unavailable"}}}
	})
	res, err := request(t, w.n, "worktree.source", map[string]any{"workspace_id": w.src, "kind": "orca", "query": "review", "locale": "en"})
	if err != nil || res["warning"] == nil {
		t.Fatalf("provider failure hidden: %v %v", res, err)
	}
}

func TestWorktreeComposerRefusesIncompleteCatalog(t *testing.T) {
	w := composerNode(t)
	w.o.Handle("worktree.ps", func(p map[string]any) any {
		return map[string]any{"worktrees": []any{orcarttest.Worktree(w.src, w.repo, "dev")}, "totalCount": 2, "truncated": true}
	})
	if _, err := request(t, w.n, "worktree.prepare", map[string]any{"workspace_id": w.src, "kind": "orca", "composer": true}); err == nil {
		t.Fatal("partial catalog used for safety decisions")
	}
}
