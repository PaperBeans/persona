package node

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/repositorywire"
	"github.com/nwverse/persona/server/internal/taskworkspace"
	"github.com/nwverse/persona/server/internal/workspacenaming"
)

func TestWorkspaceNamingUsesTargetHomeWithoutEngineOrCreation(t *testing.T) {
	for range 2 {
		home := workspaceTemp(t)
		n := &Node{home: home, dir: filepath.Join(home, ".persona")}
		source := workspacenaming.Source{TodoID: "todo_example", Title: "Example", PlannedAt: time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC), Timezone: "UTC", Rules: workspacenaming.Defaults(), Layout: repositorywire.LayoutRoot, Repositories: repositorywire.PreparationSnapshot{Repositories: []repositorywire.PreparationRepository{{RepoID: "web", RequestedRef: repositorywire.RequestedRef{Kind: "default"}}}}}
		raw, _ := json.Marshal(source)
		out, err := n.handle(t.Context(), "workspace.naming.resolve", raw)
		if err != nil {
			t.Fatal(err)
		}
		preview := out.(workspacenaming.Preview)
		if preview.Plan == nil || preview.Plan.RootPath != filepath.Join(home, "workspace", "task-todo_example") || preview.Plan.Repositories[0].RelativePath != "." {
			t.Fatal(preview)
		}
		files, err := os.ReadDir(home)
		if err != nil || len(files) != 0 {
			t.Fatal("preview created resources", files, err)
		}
	}
}

func TestWorkspaceNamingRejectsPrivateSymlinkAncestors(t *testing.T) {
	for _, level := range []string{"base", "task", "repository"} {
		for _, target := range []string{"state", "ssh", "missing-ssh", "missing-step-secret"} {
			t.Run(level+"/"+target, func(t *testing.T) {
				home := workspaceTemp(t)
				state := filepath.Join(workspaceTemp(t), "private-state")
				if err := os.Mkdir(state, 0o700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PERSONA_HOME", state)
				n := &Node{home: home, dir: state}
				to := state
				if target != "state" {
					to = filepath.Join(home, ".ssh")
					if target == "ssh" {
						if err := os.Mkdir(to, 0o700); err != nil {
							t.Fatal(err)
						}
					}
					if target == "missing-step-secret" {
						to = filepath.Join(home, "missing") + string(filepath.Separator) + ".." + string(filepath.Separator) + ".ssh"
					}
				}
				base := filepath.Join(home, "workspace")
				at := base
				if level == "task" {
					at = filepath.Join(base, "task-todo_example")
				}
				if level == "repository" {
					at = filepath.Join(base, "task-todo_example", "web")
				}
				if err := os.MkdirAll(filepath.Dir(at), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(to, at); err != nil {
					t.Skip("symlinks:", err)
				}
				s := namingGuardSource()
				before := namingGuardTree(t, home, state)
				got := namingGuardPreview(t, n, s)
				if got.Plan != nil || got.Status != "invalid" || len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != "base_scope_denied" {
					t.Fatalf("%s link into %s accepted: %+v", level, target, got)
				}
				if level == "repository" && got.Diagnostics[0].RepoID != "web" {
					t.Fatal("missing repository attribution", got)
				}
				if !reflect.DeepEqual(before, namingGuardTree(t, home, state)) {
					t.Fatal("naming created or changed resources")
				}
			})
		}
	}
}

func TestWorkspaceNamingResolvesPrivateStateItself(t *testing.T) {
	home, state := workspaceTemp(t), workspaceTemp(t)
	t.Setenv("PERSONA_HOME", state)
	alias := filepath.Join(home, "state-alias")
	if err := os.Symlink(state, alias); err != nil {
		t.Skip("symlinks:", err)
	}
	if err := os.Symlink(state, filepath.Join(home, "workspace")); err != nil {
		t.Fatal(err)
	}
	s := namingGuardSource()
	s.Layout, s.Repositories.Repositories = repositorywire.LayoutRoot, s.Repositories.Repositories[:1]
	n := &Node{home: home, dir: alias}
	before := namingGuardTree(t, home, state)
	got := namingGuardPreview(t, n, s)
	if got.Plan != nil || got.Status != "invalid" || len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != "base_scope_denied" {
		t.Fatal("physical private state accepted", got)
	}
	if !reflect.DeepEqual(before, namingGuardTree(t, home, state)) {
		t.Fatal("preview changed resources")
	}
}

func TestWorkspaceNamingMissingPrivateStateBehindSymlink(t *testing.T) {
	for _, link := range []string{"ancestor", "state-root"} {
		for _, level := range []string{"base", "task", "repository"} {
			for _, target := range []string{"state", "descendant", "sibling-prefix"} {
				t.Run(link+"/"+level+"/"+target, func(t *testing.T) {
					home, physical := workspaceTemp(t), physicalTempDir(t)
					physicalState := filepath.Join(physical, "missing", "private-state")
					alias := filepath.Join(home, "state-alias")
					state, to := alias, physicalState
					if link == "ancestor" {
						state, to = filepath.Join(alias, "missing", "private-state"), physical
					}
					if err := os.Symlink(to, alias); err != nil {
						t.Skip("symlinks:", err)
					}
					t.Setenv("PERSONA_HOME", state)
					n := &Node{home: home, dir: state}
					resolved, err := n.realPath(state)
					if !errors.Is(err, fs.ErrNotExist) || resolved != physicalState {
						t.Fatalf("missing state = %q, %v; want physical tail %q", resolved, err, physicalState)
					}
					to = physicalState
					if target == "descendant" {
						to = filepath.Join(to, "project")
					} else if target == "sibling-prefix" {
						to += "-projects"
					}
					at := filepath.Join(home, "workspace")
					if level != "base" {
						at = filepath.Join(at, "task-todo_example")
					}
					if level == "repository" {
						at = filepath.Join(at, "web")
					}
					if err := os.MkdirAll(filepath.Dir(at), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(to, at); err != nil {
						t.Fatal(err)
					}
					source := namingGuardSource()
					if lexical := workspacenaming.Resolve(source, workspacenaming.NodeContext{Home: home, PersonaHome: state}); lexical.Plan == nil {
						t.Fatal("fixture did not reach the physical guard", lexical)
					}
					before := namingGuardTree(t, home, physical)
					got := namingGuardPreview(t, n, source)
					code := "base_scope_denied"
					if target == "sibling-prefix" {
						code = "path_symlink"
					}
					if got.Plan != nil || got.Status != "invalid" || len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != code {
						t.Fatal("workspace link was accepted or misclassified", got)
					}
					if level == "repository" && got.Diagnostics[0].RepoID != "web" {
						t.Fatal("missing repository attribution", got)
					}
					if !reflect.DeepEqual(before, namingGuardTree(t, home, physical)) {
						t.Fatal("preview changed resources")
					}
				})
			}
		}
	}
}

func TestWorkspaceNamingFailsClosedOnPathResolutionErrors(t *testing.T) {
	for _, kind := range []string{"loop", "not-directory"} {
		t.Run(kind, func(t *testing.T) {
			home := workspaceTemp(t)
			at := filepath.Join(home, "workspace")
			if kind == "loop" {
				if err := os.Symlink(at, at); err != nil {
					t.Skip("symlinks:", err)
				}
			} else if err := os.WriteFile(at, []byte("ordinary file"), 0o600); err != nil {
				t.Fatal(err)
			}
			n := &Node{home: home, dir: filepath.Join(home, ".persona")}
			got := namingGuardPreview(t, n, namingGuardSource())
			if got.Plan != nil || got.Status != "invalid" || len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != "base_scope_denied" {
				t.Fatal("unresolved path accepted", got)
			}
		})
	}
}

func TestWorkspaceNamingAndPrepareCheckTheSamePaths(t *testing.T) {
	for _, level := range []string{"missing tail", "base", "task", "repository"} {
		t.Run(level, func(t *testing.T) {
			home := workspaceTemp(t)
			n := &Node{home: home, dir: filepath.Join(home, ".persona")}
			plan := workspacePlan(t, home, repositorywire.LayoutContainer, workspaceRemote(t, "trunk"))
			linkAt, field, repo := "", "", ""
			switch level {
			case "base":
				linkAt, field = plan.BasePath, "base_directory"
			case "task":
				linkAt, field = plan.RootPath, "task_directory_template"
			case "repository":
				linkAt, field, repo = plan.Repositories[0].AbsolutePath, "directory_alias", "web"
			}
			if linkAt != "" {
				if err := os.MkdirAll(filepath.Dir(linkAt), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(workspaceTemp(t), linkAt); err != nil {
					t.Skip("symlinks:", err)
				}
			}
			before := namingGuardTree(t, home)
			got := namingGuardPreview(t, n, plan.Source)
			if !reflect.DeepEqual(before, namingGuardTree(t, home)) {
				t.Fatal("preview created resources")
			}
			req := taskworkspace.Request{WorkspaceID: "ws_parity", OperationID: "op_parity", Plan: plan}
			prepared := workspaceCall(t, n, "prepare", req)
			if linkAt == "" {
				if got.Plan == nil || got.Plan.Digest != plan.Digest || prepared.State != "ready" {
					t.Fatal(got, prepared)
				}
				return
			}
			if got.Plan != nil || got.Status != "invalid" || len(got.Diagnostics) != 1 ||
				got.Diagnostics[0].Code != "path_symlink" || got.Diagnostics[0].Field != field || got.Diagnostics[0].RepoID != repo ||
				prepared.Error == nil || prepared.Error.Code != got.Diagnostics[0].Code || prepared.Error.RepoID != repo {
				t.Fatal(got, prepared)
			}
			if !reflect.DeepEqual(before, namingGuardTree(t, home)) {
				t.Fatal("refused preparation created resources")
			}
		})
	}
}

func namingGuardSource() workspacenaming.Source {
	return workspacenaming.Source{TodoID: "todo_example", Title: "Example", PlannedAt: time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC), Timezone: "UTC", Rules: workspacenaming.Defaults(), Layout: repositorywire.LayoutContainer,
		Repositories: repositorywire.PreparationSnapshot{Repositories: []repositorywire.PreparationRepository{
			{RepoID: "api", DirectoryAlias: "api", RequestedRef: repositorywire.RequestedRef{Kind: "default"}},
			{RepoID: "web", DirectoryAlias: "web", RequestedRef: repositorywire.RequestedRef{Kind: "default"}},
		}}}
}

func namingGuardPreview(t *testing.T, n *Node, s workspacenaming.Source) workspacenaming.Preview {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	out, err := n.handle(t.Context(), "workspace.naming.resolve", raw)
	if err != nil {
		t.Fatal(err)
	}
	return out.(workspacenaming.Preview)
}

func namingGuardTree(t *testing.T, roots ...string) map[string]os.FileMode {
	t.Helper()
	entries := map[string]os.FileMode{}
	for _, root := range roots {
		if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := d.Info()
			if err == nil {
				entries[path] = info.Mode()
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	return entries
}
