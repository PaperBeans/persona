package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestGitIdentityAcrossWorktreesAndClones(t *testing.T) {
	f := newPullFixture(t)
	tree := filepath.Join(t.TempDir(), "outside-checkout")
	runPullGit(t, f.local, "worktree", "add", "-b", "identity-worker", tree)
	clone := filepath.Join(t.TempDir(), "another-checkout")
	runPullGit(t, f.local, "clone", filepath.Join(filepath.Dir(f.local), "remote.git"), clone)
	read := func(cwd string) proto.RepositoryIdentity {
		t.Helper()
		params, _ := json.Marshal(map[string]any{"cwd": cwd, "identity_only": true})
		out, err := f.node.handle(context.Background(), "git.status", params)
		if err != nil {
			t.Fatal(err)
		}
		identity, ok := out.(proto.RepositoryIdentity)
		if !ok || identity.Version != 1 || !identity.PathResolved || !identity.IsRepo {
			t.Fatalf("identity-only response: %#v", out)
		}
		return identity
	}
	main, worktree, otherClone := read(f.local), read(tree), read(clone)
	if main.CommonDir != worktree.CommonDir || main.Root == worktree.Root {
		t.Fatalf("worktree identity: %+v %+v", main, worktree)
	}
	if main.Origin == "" || main.Origin != otherClone.Origin || main.CommonDir == otherClone.CommonDir {
		t.Fatalf("clone identity: %+v %+v", main, otherClone)
	}
	if main.Root != realPath(f.local) || worktree.Cwd != realPath(tree) {
		t.Fatalf("physical paths: %+v %+v", main, worktree)
	}
	if info, err := f.node.gitIdentity(context.Background(), t.TempDir()); err != nil || info.IsRepo || info.Version != 1 {
		t.Fatalf("plain directory: %+v %v", info, err)
	}
	if status := runPullGit(t, f.local, "status", "--porcelain"); status != "" {
		t.Fatal(status)
	}
}

func TestGitIdentityCanonicalOrigin(t *testing.T) {
	for _, remote := range []string{"git@example.com:team/app.git", "ssh://git@example.com/team/app.git", "https://example.com/team/app.git", "https://credential:password@example.com/team/app.git", "ssh://git@example.com:22/team/app.git", "https://example.com:443/team/app"} {
		got := canonicalGitOrigin(remote, t.TempDir())
		if got != "example.com/team/app" || strings.Contains(got, "credential") || strings.Contains(got, "password") {
			t.Errorf("canonical origin did not remove transport credentials: %q", got)
		}
	}
	for _, remote := range []string{"https://example.com/team/other.git", "git@example.net:team/app.git", "ssh://git@example.com:2222/team/app.git"} {
		if got := canonicalGitOrigin(remote, t.TempDir()); got == "example.com/team/app" || got == "" {
			t.Errorf("distinct origin collapsed: %q", got)
		}
	}
	if a, b := canonicalGitOrigin("alice@example.com:app.git", "/tmp"), canonicalGitOrigin("bob@example.com:app.git", "/tmp"); a == b || strings.Contains(a, "alice") {
		t.Fatal("custom SSH home identities collapsed or exposed the account")
	}
	for _, remote := range []string{"https://example.com/team/app?token=secret", "https://example.com/team/app#secret", "https://example.com/team/../app", "ext::command"} {
		if got := canonicalGitOrigin(remote, t.TempDir()); got != "" {
			t.Errorf("unsupported origin accepted: %q", got)
		}
	}
	root := t.TempDir()
	remote := filepath.Join(root, "origin.git")
	if err := os.MkdirAll(remote, 0700); err != nil {
		t.Fatal(err)
	}
	if a, b := canonicalGitOrigin("origin.git", root), canonicalGitOrigin("file://"+remote, root); a != b {
		t.Fatalf("local origins differ: %q %q", a, b)
	}
}

func TestGitIdentityResolvesSymlinkBeforeParent(t *testing.T) {
	f := newPullFixture(t)
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(outside, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "child"), filepath.Join(f.local, "escape")); err != nil {
		t.Fatal(err)
	}
	identity, err := f.node.gitIdentity(t.Context(), f.local+"/escape/..")
	if err != nil || identity.Cwd != realPath(outside) || identity.IsRepo {
		t.Fatalf("link/.. identity: %+v %v", identity, err)
	}
	if _, err := f.node.gitIdentity(t.Context(), filepath.Join(f.local, "missing")); err == nil {
		t.Fatal("missing path returned a usable identity")
	}
}
