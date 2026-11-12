package node

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/orcart/orcarttest"
)

// A folder used as a plain working directory is often a repository nobody has committed to: HEAD names an unborn
// branch. A worktree of it has nothing to start from, so neither the dialog nor a task may take the new-checkout path
// there, and the refusal is a plain sentence with a code the hub acts on, not git's "invalid reference" (#5548).

// unbornRepo is a repository with an unborn main and an untracked file, the way a never-committed project folder looks.
func unbornRepo(t *testing.T, dir string) string {
	t.Helper()
	repo := mkdir(t, dir)
	runPullGit(t, repo, "init", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(repo, "notes.txt"), []byte("draft\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return repo
}

func assertNoCommits(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil || rpcCode(err) != "worktree_no_commits" || strings.Contains(err.Error(), "invalid reference") ||
		err.Error() == "" {
		t.Fatalf("%s on a repository without commits = %v (code %q)", what, err, rpcCode(err))
	}
}

func TestOrcaWorktreeRefusesARepositoryWithoutCommits(t *testing.T) {
	w := orcaWorktreeNode(t)
	empty := unbornRepo(t, filepath.Join(w.home, "scratchpad"))
	w.o.SetWorktrees(orcarttest.Worktree(w.src, w.repo, "refs/heads/dev"), orcarttest.Worktree("repo2::"+empty, empty, "refs/heads/main"))
	// Orca adds the checkout from the base it is given, as `git worktree add -b <branch> -- <path> <base>` does.
	w.o.Handle("worktree.create", func(p map[string]any) any {
		path := filepath.Join(w.home, "orca", "workspaces", "scratchpad", str(p["name"]))
		branch := str(p["branchNameOverride"])
		if branch == "" {
			branch = "devuser/" + str(p["name"])
		}
		if out, err := gitRun(t.Context(), empty, gitOpTimeout, "worktree", "add", "-b", branch, "--", path, str(p["baseBranch"])); err != nil {
			return orcarttest.Fail{Code: out}
		}
		return map[string]any{"worktree": map[string]any{"id": "repo2::" + path, "path": path, "branch": "refs/heads/" + branch}}
	})

	_, err := request(t, w.n, "worktree.create", map[string]any{"cwd": empty, "branch": "task/one",
		"path": filepath.Join(w.dir, "task-one")})
	assertNoCommits(t, "create", err)
	_, err = request(t, w.n, "worktree.prepare", map[string]any{"cwd": empty})
	assertNoCommits(t, "prepare", err)
	_, err = request(t, w.n, "worktree.create", map[string]any{"cwd": empty, "auto_name": true})
	assertNoCommits(t, "automatic create", err)
	if n := w.o.Count("worktree.create"); n != 0 {
		t.Fatalf("Orca was asked to create %d worktrees from an unborn branch", n)
	}
	// The committed project next to it still prepares.
	if _, err := request(t, w.n, "worktree.prepare", map[string]any{"cwd": w.repo}); err != nil {
		t.Fatal(err)
	}
}

func TestWorktreeComposerRefusesARepositoryWithoutCommits(t *testing.T) {
	w := composerNode(t)
	empty := unbornRepo(t, filepath.Join(w.home, "scratchpad"))
	id := "repo2::" + empty
	w.o.SetWorktrees(orcarttest.Worktree(w.src, w.repo, "refs/heads/dev"), orcarttest.Worktree(id, empty, "refs/heads/main"))
	_, err := request(t, w.n, "worktree.prepare", map[string]any{"kind": "orca", "workspace_id": id, "composer": true, "locale": "en"})
	assertNoCommits(t, "composer prepare", err)
	if !strings.Contains(err.Error(), "has no commits yet") {
		t.Fatalf("the refusal is not the plain English sentence: %v", err)
	}
	_, err = request(t, w.n, "worktree.create", map[string]any{"kind": "orca", "workspace_id": id, "composer": true, "auto_name": true, "no_parent": true})
	assertNoCommits(t, "composer create", err)
	if n := w.o.Count("worktree.create"); n != 0 {
		t.Fatalf("Orca was asked to create %d worktrees from an unborn branch", n)
	}
}

func TestHerdrWorktreeRefusesARepositoryWithoutCommits(t *testing.T) {
	w := worktreeNode(t)
	setWorktreeSupport(t, false) // the git fallback: `git worktree add` would fail with "invalid reference: HEAD"
	runPullGit(t, w.repo, "checkout", "--orphan", "fresh")
	runPullGit(t, w.repo, "branch", "-D", "main", "spare")
	_, err := request(t, w.n, "worktree.prepare", map[string]any{"workspace_id": "w1"})
	assertNoCommits(t, "prepare", err)
	_, err = request(t, w.n, "worktree.create", map[string]any{"workspace_id": "w1", "branch": "task/one",
		"path": filepath.Join(w.home, "trees", "task-one")})
	assertNoCommits(t, "create", err)
	if got := w.f.trace("worktree.create", "workspace.create"); len(got) != 0 {
		t.Fatalf("a workspace was opened for a repository without commits: %v", got)
	}
}

// Commits on any ref are enough: HEAD on an orphan branch, or a repository whose only commits were fetched, still has
// something to check out, so neither is refused as "no commits".
func TestOrcaWorktreeWithCommitsElsewhereIsNotRefused(t *testing.T) {
	w := orcaWorktreeNode(t)
	runPullGit(t, w.repo, "checkout", "--orphan", "gh-pages")
	if _, err := request(t, w.n, "worktree.prepare", map[string]any{"cwd": w.repo}); err != nil {
		t.Fatalf("prepare with HEAD on an orphan branch: %v", err)
	}
	if _, err := w.create(t, "spare"); err != nil {
		t.Fatalf("checking out an existing branch with HEAD on an orphan branch: %v", err)
	}
}

func TestWorktreeComposerAcceptsAFetchedOnlyRepository(t *testing.T) {
	w := composerNode(t)
	fetched := unbornRepo(t, filepath.Join(w.home, "fetched"))
	runPullGit(t, fetched, "remote", "add", "origin", w.repo)
	runPullGit(t, fetched, "fetch", "--quiet", "origin")
	id := "repo2::" + fetched
	w.o.SetWorktrees(orcarttest.Worktree(w.src, w.repo, "refs/heads/dev"), orcarttest.Worktree(id, fetched, "refs/heads/main"))
	prepared, err := request(t, w.n, "worktree.prepare", map[string]any{"kind": "orca", "workspace_id": id, "composer": true})
	if err != nil {
		t.Fatalf("composer prepare of a repository with only fetched commits: %v", err)
	}
	if refs, _ := prepared["composer"].(map[string]any)["refs"].([]any); !slices.Contains(refs, any("origin/main")) {
		t.Fatalf("fetched branches are not offered as bases: %v", prepared)
	}
}

// The plain (non-composer) calls carry the caller's locale too.
func TestWorktreeNoCommitsFollowsTheCallersLocale(t *testing.T) {
	w := orcaWorktreeNode(t)
	empty := unbornRepo(t, filepath.Join(w.home, "scratchpad"))
	w.o.SetWorktrees(orcarttest.Worktree(w.src, w.repo, "refs/heads/dev"), orcarttest.Worktree("repo2::"+empty, empty, "refs/heads/main"))
	for locale, want := range map[string]string{"en": "has no commits yet", "ja": "コミットがない"} {
		_, err := request(t, w.n, "worktree.prepare", map[string]any{"cwd": empty, "locale": locale})
		assertNoCommits(t, "prepare in "+locale, err)
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("prepare in %s: %v", locale, err)
		}
	}
}
