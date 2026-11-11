package node

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func writeGitFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The layouts git itself writes: a main checkout, a linked worktree elsewhere on disk (Orca, Claude Code and herdr keep
// them in their own folders), a submodule and a detached HEAD.
func TestReadGitPlace(t *testing.T) {
	home := t.TempDir()
	// readGitPlace walks the filesystem itself, so GIT_CEILING_DIRECTORIES does not apply. An empty .git file
	// stops discovery at the fixture boundary even when the temporary directory's parent is a repository.
	writeGitFile(t, filepath.Join(home, ".git"), "")
	repo := filepath.Join(home, "Code", "app")
	writeGitFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	if err := os.MkdirAll(filepath.Join(repo, "web", "src"), 0o755); err != nil {
		t.Fatal(err)
	}

	// git worktree add ~/orca/workspaces/app/fix-login -b fix/login: an absolute gitdir, a relative commondir.
	tree := filepath.Join(home, "orca", "workspaces", "app", "fix-login")
	meta := filepath.Join(repo, ".git", "worktrees", "fix-login")
	writeGitFile(t, filepath.Join(tree, ".git"), "gitdir: "+meta+"\n")
	writeGitFile(t, filepath.Join(meta, "commondir"), "../..\n")
	writeGitFile(t, filepath.Join(meta, "HEAD"), "ref: refs/heads/fix/login\n")

	// A worktree inside the repository with a relative gitdir (git worktree add --relative-paths), detached.
	inner := filepath.Join(repo, ".claude", "worktrees", "probe")
	innerMeta := filepath.Join(repo, ".git", "worktrees", "probe")
	writeGitFile(t, filepath.Join(inner, ".git"), "gitdir: ../../../.git/worktrees/probe\n")
	writeGitFile(t, filepath.Join(innerMeta, "commondir"), "../..\n")
	writeGitFile(t, filepath.Join(innerMeta, "HEAD"), "0123456789abcdef0123456789abcdef01234567\n")

	// A submodule: a .git file, no commondir: its own project.
	sub := filepath.Join(repo, "vendor", "lib")
	subMeta := filepath.Join(repo, ".git", "modules", "lib")
	writeGitFile(t, filepath.Join(sub, ".git"), "gitdir: ../../.git/modules/lib\n")
	writeGitFile(t, filepath.Join(subMeta, "HEAD"), "ref: refs/heads/trunk\n")

	// git -C vendor/lib worktree add ../lib-linked -b linked: the shared git dir is under the parent's .git/modules,
	// and its config names the submodule's checkout.
	writeGitFile(t, filepath.Join(subMeta, "config"), "[core]\n\tbare = false\n\tworktree = ../../../vendor/lib\n")
	subTree := filepath.Join(repo, "vendor", "lib-linked")
	subTreeMeta := filepath.Join(subMeta, "worktrees", "lib-linked")
	writeGitFile(t, filepath.Join(subTree, ".git"), "gitdir: "+subTreeMeta+"\n")
	writeGitFile(t, filepath.Join(subTreeMeta, "commondir"), "../..\n")
	writeGitFile(t, filepath.Join(subTreeMeta, "HEAD"), "ref: refs/heads/linked\n")

	// A bare repository's worktree: the bare repository is the project.
	bare := filepath.Join(home, "bare.git")
	writeGitFile(t, filepath.Join(bare, "config"), "[core]\n\tbare = true\n")
	bareTree := filepath.Join(home, "bare-wt")
	writeGitFile(t, filepath.Join(bareTree, ".git"), "gitdir: "+filepath.Join(bare, "worktrees", "bare-wt")+"\n")
	writeGitFile(t, filepath.Join(bare, "worktrees", "bare-wt", "commondir"), "../..\n")
	writeGitFile(t, filepath.Join(bare, "worktrees", "bare-wt", "HEAD"), "ref: refs/heads/main\n")

	plain := filepath.Join(home, "notes")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name, cwd string
		want      gitPlace
	}{
		{"main checkout", repo, gitPlace{repo, repo, "main"}},
		{"folder inside the main checkout", filepath.Join(repo, "web", "src"), gitPlace{repo, repo, "main"}},
		{"trailing slash", repo + "/", gitPlace{repo, repo, "main"}},
		{"linked worktree elsewhere", tree, gitPlace{repo, tree, "fix/login"}},
		{"detached worktree inside the repo", inner, gitPlace{repo, inner, ""}},
		{"submodule", sub, gitPlace{sub, sub, "trunk"}},
		{"submodule's linked worktree", subTree, gitPlace{sub, subTree, "linked"}},
		{"bare repository's worktree", bareTree, gitPlace{bare, bareTree, "main"}},
		{"not a repository", plain, gitPlace{}},
		{"relative path", "Code/app", gitPlace{}},
		{"empty", "", gitPlace{}},
	} {
		if got := readGitPlace(c.cwd); got != c.want {
			t.Errorf("%s: readGitPlace(%q) = %+v, want %+v", c.name, c.cwd, got, c.want)
		}
	}
}

func TestFillGitPlaceKeepsHostRoot(t *testing.T) {
	repo := filepath.Join(physicalTempDir(t), "app")
	writeGitFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	awaitGitPlace(t, gitPlaces, repo)

	s := proto.Session{Cwd: repo}
	fillGitPlace(&s)
	if s.RepoRoot != repo || s.Worktree != repo || s.Branch != "main" {
		t.Fatalf("filled %q %q %q", s.RepoRoot, s.Worktree, s.Branch)
	}

	// A host that names the project (Paseo, a Codex project) keeps its own root.
	hosted := proto.Session{Cwd: repo, RepoRoot: "/elsewhere/app"}
	fillGitPlace(&hosted)
	if hosted.RepoRoot != "/elsewhere/app" || hosted.Worktree != repo || hosted.Branch != "main" {
		t.Fatalf("hosted %q %q %q", hosted.RepoRoot, hosted.Worktree, hosted.Branch)
	}

	// The assistant's conversations are not in a project.
	chat := proto.Session{Cwd: repo, Assistant: true}
	fillGitPlace(&chat)
	if chat.RepoRoot != "" || chat.Worktree != "" || chat.Branch != "" {
		t.Fatalf("assistant got %q %q %q", chat.RepoRoot, chat.Worktree, chat.Branch)
	}
}

// A repository reached through a symbolic link (a home on another mount, /home/me → /data/home/me) is one project
// whichever way a session's record writes its directory: Claude Code records the physical one, Codex the one it was
// started in (#4492).
func TestPhysicalGitPlaceResolvesSymlinks(t *testing.T) {
	base := physicalTempDir(t)
	writeGitFile(t, filepath.Join(base, ".git"), "") // stops discovery at the fixture (TestReadGitPlace)
	home := filepath.Join(base, "data", "home", "me")
	repo := filepath.Join(home, "projects", "app")
	writeGitFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	if err := os.MkdirAll(filepath.Join(repo, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "home-me")
	if err := os.Symlink(home, link); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	linked := filepath.Join(link, "projects", "app")

	// git -C <link>/projects/app worktree add …: git wrote the gitdir as it was reached, through the link.
	tree := filepath.Join(home, "trees", "fix")
	writeGitFile(t, filepath.Join(tree, ".git"), "gitdir: "+filepath.Join(linked, ".git", "worktrees", "fix")+"\n")
	writeGitFile(t, filepath.Join(repo, ".git", "worktrees", "fix", "commondir"), "../..\n")
	writeGitFile(t, filepath.Join(repo, ".git", "worktrees", "fix", "HEAD"), "ref: refs/heads/fix\n")

	// A link from outside into the repository, and one inside it that leads out: a session is in the repository its
	// physical directory is in, as git itself says there.
	into := filepath.Join(base, "config", "web")
	out := filepath.Join(repo, "vendor")
	elsewhere := filepath.Join(base, "elsewhere")
	for _, dir := range []string{filepath.Dir(into), elsewhere} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(repo, "web"), into); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, out); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name, cwd string
		want      gitPlace
	}{
		{"the physical directory", repo, gitPlace{root: repo, worktree: repo, branch: "main"}},
		{"through the link", linked, gitPlace{root: repo, worktree: repo, branch: "main"}},
		{"a folder inside, through the link", filepath.Join(linked, "web"), gitPlace{root: repo, worktree: repo, branch: "main"}},
		{"a linked worktree whose gitdir is written through the link", tree, gitPlace{root: repo, worktree: tree, branch: "fix"}},
		{"that worktree through the link", filepath.Join(link, "trees", "fix"), gitPlace{root: repo, worktree: tree, branch: "fix"}},
		{"not a repository", filepath.Join(link, "projects"), gitPlace{}},
		{"a link into the repository", into, gitPlace{root: repo, worktree: repo, branch: "main"}},
		{"a link out of the repository", out, gitPlace{}},
		{"a relative path", "projects/app", gitPlace{}},
	} {
		got := physicalGitPlace(c.cwd)
		if got != c.want {
			t.Errorf("%s: physicalGitPlace(%q) = %+v, want %+v", c.name, c.cwd, got, c.want)
		}
	}
	// As written, the same repository is two projects: what the sessions' records do not agree on.
	if got := readGitPlace(linked); got.root != linked || got.worktree != linked {
		t.Errorf("readGitPlace resolves links itself now: %+v", got)
	}

	// A host that names the project (Paseo: git's own word, the physical directory; another host may write it as it
	// was opened, through the link) for a session whose cwd goes through the link: either way its main checkout is
	// the project itself, not a linked worktree of it.
	old, oldDirs := gitPlaces, physicalDirs
	gitPlaces, physicalDirs = newGitPlaceCache(physicalGitPlace), newGitPlaceCache(oldDirs.read)
	defer func() { gitPlaces, physicalDirs = old, oldDirs }()
	awaitGitPlace(t, gitPlaces, linked)
	for _, named := range []string{repo, linked} {
		awaitGitPlace(t, physicalDirs, named)
		hosted := proto.Session{Cwd: linked, RepoRoot: named}
		fillGitPlace(&hosted)
		if hosted.RepoRoot != repo || hosted.Worktree != repo || hosted.Branch != "main" {
			t.Fatalf("hosted through the link, its root named %q: %q %q %q", named, hosted.RepoRoot, hosted.Worktree, hosted.Branch)
		}
	}
	// A root the host names that is not on this computer stays as it says, and so does a relative one.
	awaitGitPlace(t, physicalDirs, "/elsewhere/app")
	for _, named := range []string{"/elsewhere/app", "app"} {
		far := proto.Session{Cwd: linked, RepoRoot: named}
		fillGitPlace(&far)
		if far.RepoRoot != named || far.Worktree != repo {
			t.Fatalf("a root named %q: %q %q", named, far.RepoRoot, far.Worktree)
		}
	}
}

// physicalTempDir is t.TempDir() by its physical path, which is how a Git place names a directory (physicalGitPlace):
// macOS's own temporary directory is behind a symbolic link.
func physicalTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func awaitGitPlace(t *testing.T, cache *gitPlaceCache, cwd string) gitPlace {
	t.Helper()
	cache.lookup(cwd)
	cache.mu.Lock()
	var pending <-chan struct{}
	if e := cache.entries[cwd]; e != nil {
		pending = e.pending
	}
	cache.mu.Unlock()
	if pending != nil {
		select {
		case <-pending:
		case <-time.After(2 * time.Second):
			t.Fatal("Git decoration did not complete")
		}
	}
	return cache.lookup(cwd)
}
