package node

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/orcart"
	"github.com/nwverse/persona/server/internal/proto"
)

func worktreeComposerContext(ctx context.Context, raw json.RawMessage) context.Context {
	var in struct {
		Locale string `json:"locale"`
	}
	_ = json.Unmarshal(raw, &in)
	return i18n.With(ctx, i18n.Resolve(in.Locale))
}

func composerError(ctx context.Context, key string, args ...i18n.Args) error {
	return errors.New(i18n.T(i18n.From(ctx), "worktree-composer."+key, args...))
}

// Composer operations use runtime identities, never a client-supplied checkout path. Legacy branch/path creation
// remains available to old clients and other terminal hosts.
func (n *Node) orcaWorkspace(ctx context.Context, kind, id string) (*orcaHost, orcart.Worktree, []orcart.Worktree, error) {
	if kind != "orca" || id == "" {
		return nil, orcart.Worktree{}, nil, composerError(ctx, "workspace_required")
	}
	h, err := n.hostOfKind(kind)
	if err != nil {
		return nil, orcart.Worktree{}, nil, err
	}
	o, ok := h.(*orcaHost)
	if !ok {
		return nil, orcart.Worktree{}, nil, composerError(ctx, "unavailable")
	}
	trees, err := composerCatalog(ctx, o)
	if err != nil {
		return nil, orcart.Worktree{}, nil, orcaErr(err)
	}
	var selected *orcart.Worktree
	for _, w := range trees {
		if w.WorktreeID == id && localComposerWorkspace(w) {
			if selected != nil {
				return nil, orcart.Worktree{}, nil, composerError(ctx, "workspace_ambiguous")
			}
			copy := w
			selected = &copy
		}
	}
	if selected != nil {
		classified, e := classifyComposerWorkspace(ctx, o, *selected)
		return o, classified, trees, e
	}
	return nil, orcart.Worktree{}, nil, composerError(ctx, "workspace_missing")
}

// Safety decisions require a full, non-delta catalog; never infer absence or children from a truncated list.
func composerCatalog(ctx context.Context, o *orcaHost) ([]orcart.Worktree, error) {
	var res struct {
		Worktrees []orcart.Worktree `json:"worktrees"`
		Total     int               `json:"totalCount"`
		Truncated bool              `json:"truncated"`
		Unchanged bool              `json:"unchanged"`
	}
	if err := o.c.Call(ctx, orcart.ListTimeout, "worktree.ps", map[string]any{"limit": 100000, "afterSnapshotId": nil}, &res); err != nil {
		return nil, err
	}
	if res.Truncated || res.Unchanged || res.Total > len(res.Worktrees) {
		return nil, composerError(ctx, "catalog_incomplete")
	}
	return res.Worktrees, nil
}

func localComposerWorkspace(w orcart.Worktree) bool { return w.HostID == "" || w.HostID == "local" }

// Folder-project children still use the historical "git" catalog kind. Read their registered project kind
// instead of guessing from a missing branch or from whether their directory happens to contain a repository.
func classifyComposerWorkspace(ctx context.Context, o *orcaHost, w orcart.Worktree) (orcart.Worktree, error) {
	if folderComposerWorkspace(w) || w.Branch != "" {
		return w, nil
	}
	selector, err := orcaRepoSelector(ctx, w)
	if err != nil {
		return w, err
	}
	var info struct {
		Repo struct {
			Kind string `json:"kind"`
		} `json:"repo"`
	}
	if err = o.c.Call(ctx, orcart.CallTimeout, "repo.show", map[string]any{"repo": selector, "repoHostId": "local"}, &info); err != nil {
		return w, orcaErr(err)
	}
	if info.Repo.Kind == "folder" {
		w.WorkspaceKind = "folder"
	}
	return w, nil
}

func folderComposerWorkspace(w orcart.Worktree) bool {
	return w.WorkspaceKind == "folder" || w.WorkspaceKind == "folder-workspace"
}

func rejectComposerFields(ctx context.Context, raw json.RawMessage) error {
	var in map[string]json.RawMessage
	if err := json.Unmarshal(raw, &in); err != nil {
		return err
	}
	for _, key := range []string{"name", "branch_override", "base_ref", "no_parent", "note", "sparse_preset_id", "source_id", "source_url"} {
		if _, present := in[key]; present {
			return composerError(worktreeComposerContext(ctx, raw), "composer_required")
		}
	}
	return nil
}

func composerCapabilities(ctx context.Context, o *orcaHost) ([]string, error) {
	st, err := o.c.Probe(ctx)
	if err != nil {
		return nil, orcaErr(err)
	}
	// V2's request capability proves that explicit branch intent survives directory-name collisions.
	if !slices.Contains(st.Capabilities, "worktree.open-existing-branch.v2-request.v1") {
		return nil, composerError(ctx, "update_composer")
	}
	caps := []string{"create", "source.github", "sparse_presets"}
	if slices.Contains(st.Capabilities, "worktree.create.exact-name.v1") {
		caps = append(caps, "branch_override")
	}
	if slices.Contains(st.Capabilities, "worktree.set.folder-parent.v1") {
		caps = append(caps, "folder_parent")
	}
	return caps, nil
}

func orcaRepoSelector(ctx context.Context, w orcart.Worktree) (string, error) {
	id := w.RepoID
	if id == "" {
		id, _, _ = strings.Cut(w.WorktreeID, "::")
	}
	if id == "" {
		return "", composerError(ctx, "project_missing")
	}
	return "id:" + id, nil
}

func (n *Node) worktreeComposerPrepare(ctx context.Context, kind, id string) (proto.WorktreePrepare, error) {
	o, w, _, err := n.orcaWorkspace(ctx, kind, id)
	if err != nil {
		return proto.WorktreePrepare{}, err
	}
	if folderComposerWorkspace(w) {
		return proto.WorktreePrepare{}, composerError(ctx, "git_source_required")
	}
	caps, err := composerCapabilities(ctx, o)
	if err != nil {
		return proto.WorktreePrepare{}, err
	}
	repo, err := repoAt(ctx, w.Path)
	if err != nil {
		return proto.WorktreePrepare{}, err
	}
	if err = requireCommit(ctx, repo); err != nil {
		return proto.WorktreePrepare{}, err
	}
	selector, err := orcaRepoSelector(ctx, w)
	if err != nil {
		return proto.WorktreePrepare{}, err
	}
	var info struct {
		Repo struct {
			DisplayName string `json:"displayName"`
			BaseRef     string `json:"worktreeBaseRef"`
		} `json:"repo"`
	}
	if err = o.c.Call(ctx, orcart.CallTimeout, "repo.show", map[string]any{"repo": selector, "repoHostId": "local"}, &info); err != nil {
		return proto.WorktreePrepare{}, orcaErr(err)
	}
	var presets struct {
		Presets []proto.WorktreePreset `json:"presets"`
	}
	if err = o.c.Call(ctx, orcart.CallTimeout, "repo.sparsePresets", map[string]any{"repo": selector}, &presets); err != nil {
		return proto.WorktreePrepare{}, orcaErr(err)
	}
	refs, err := git(ctx, repo.root, "for-each-ref", "--format=%(refname:short)", "refs/heads", "refs/remotes", "refs/tags")
	if err != nil {
		return proto.WorktreePrepare{}, err
	}
	choices := []string{}
	for _, ref := range strings.Split(strings.TrimSpace(refs), "\n") {
		if ref != "" && !strings.HasSuffix(ref, "/HEAD") {
			choices = append(choices, ref)
		}
	}
	if presets.Presets == nil {
		presets.Presets = []proto.WorktreePreset{}
	}
	base := info.Repo.BaseRef
	if base == "" {
		// Match Orca's repository default rather than implicitly stacking on the parent's current branch.
		if head, e := git(ctx, repo.root, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); e == nil {
			base = strings.TrimSpace(head)
		}
		if base == "" {
			for _, candidate := range []string{"origin/main", "origin/master", "main", "master", repo.branch} {
				if slices.Contains(choices, candidate) {
					base = candidate
					break
				}
			}
		}
	}
	project := info.Repo.DisplayName
	if project == "" {
		project = repo.name
	}
	return proto.WorktreePrepare{Root: repo.root, RepoName: repo.name, Branch: repo.branch,
		ChooseParent: true, AutomaticName: true,
		Composer: &proto.WorktreeComposer{Capabilities: caps, Project: project, BaseRef: base, Refs: choices, Presets: presets.Presets}}, nil
}

type worktreeComposeRequest struct {
	Name      string `json:"name"`
	Branch    string `json:"branch_override"`
	Base      string `json:"base_ref"`
	Parent    string `json:"parent_workspace_id"`
	NoParent  bool   `json:"no_parent"`
	Note      string `json:"note"`
	Preset    string `json:"sparse_preset_id"`
	Source    string `json:"source_id"`
	SourceURL string `json:"source_url"`
}

func (n *Node) worktreeCompose(ctx context.Context, kind, id string, raw json.RawMessage) (proto.WorktreeCreateResult, error) {
	var res proto.WorktreeCreateResult
	var in worktreeComposeRequest
	if err := json.Unmarshal(raw, &in); err != nil {
		return res, err
	}
	var legacy struct {
		Branch string `json:"branch"`
		Path   string `json:"path"`
	}
	_ = json.Unmarshal(raw, &legacy)
	if legacy.Branch != "" || legacy.Path != "" {
		return res, composerError(ctx, "composer_conflict")
	}
	o, w, trees, err := n.orcaWorkspace(ctx, kind, id)
	if err != nil {
		return res, err
	}
	if in.NoParent && in.Parent != "" {
		return res, composerError(ctx, "parent_conflict")
	}
	if !in.NoParent {
		if in.Parent == "" {
			in.Parent = id
		}
		if !slices.ContainsFunc(trees, func(p orcart.Worktree) bool {
			return p.WorktreeID == in.Parent && localComposerWorkspace(p)
		}) {
			return res, composerError(ctx, "parent_missing")
		}
	}
	prepared, err := n.worktreeComposerPrepare(ctx, kind, id)
	if err != nil {
		return res, err
	}
	if !in.NoParent {
		_, parent, _, e := n.orcaWorkspace(ctx, kind, in.Parent)
		if e != nil {
			return res, e
		}
		if folderComposerWorkspace(parent) && !slices.Contains(prepared.Composer.Capabilities, "folder_parent") {
			return res, composerError(ctx, "folder_parent_unsupported")
		}
	}
	selector, _ := orcaRepoSelector(ctx, w)
	branch := strings.TrimSpace(in.Branch)
	if branch != "" {
		if !slices.Contains(prepared.Composer.Capabilities, "branch_override") {
			return res, composerError(ctx, "update_composer")
		}
		if err = checkBranch(ctx, w.Path, branch); err != nil {
			return res, err
		}
		if at, e := branchCheckedOutAt(ctx, w.Path, branch); e != nil {
			return res, e
		} else if at != "" {
			return res, composerError(ctx, "branch_taken", i18n.Args{"branch": branch, "path": at})
		}
	}
	base := strings.TrimSpace(in.Base)
	if base == "" {
		base = prepared.Composer.BaseRef
	}
	if base == "" || !slices.Contains(prepared.Composer.Refs, base) {
		return res, composerError(ctx, "base_missing")
	}
	// Reusing a free local branch means checking it out as-is. Orca's implicit reuse otherwise depends on its base
	// matching that branch's tip and may silently choose a suffixed branch when the two commits differ.
	reuseBranch := branch != "" && branchExists(ctx, w.Path, branch)
	if reuseBranch {
		base = branch
	}
	name := strings.TrimSpace(in.Name)
	generated := name == ""
	if name == "" {
		var seed [1]byte
		if _, err = rand.Read(seed[:]); err != nil {
			return res, err
		}
		name = orcaGeneratedNames[int(seed[0])%len(orcaGeneratedNames)]
	}
	if len(name) > maxBranch || strings.ContainsAny(name, "/\\\r\n\x00") || name == "." || name == ".." {
		return res, composerError(ctx, "name_invalid")
	}
	p := map[string]any{"repo": selector, "repoHostId": "local", "name": name, "baseBranch": base, "activate": false, "setupDecision": "inherit"}
	if generated {
		p["nameWasGenerated"] = true
	}
	if branch != "" {
		p["branchNameOverride"] = branch
		// Pin a selected branch independently of directory-name retries. Review sources carry their own branch intent.
		if !strings.HasPrefix(in.Source, "pr:") {
			p["branchIntent"] = "create-exact"
			if reuseBranch {
				p["branchIntent"] = "open-existing-v2"
			}
		}
	}
	if in.NoParent {
		p["noParent"] = true
	} else {
		p["parentWorktree"] = "id:" + in.Parent
		p["matchFolderWorkspaces"] = true
	}
	if note := strings.TrimSpace(in.Note); note != "" {
		p["comment"] = note
	}
	if in.Preset != "" {
		found := false
		for _, preset := range prepared.Composer.Presets {
			if preset.ID == in.Preset {
				p["sparseCheckout"] = map[string]any{"directories": preset.Directories, "presetId": preset.ID}
				found = true
				break
			}
		}
		if !found {
			return res, composerError(ctx, "preset_missing")
		}
	}
	if in.Source != "" {
		typ, number, ok := strings.Cut(in.Source, ":")
		num, e := strconv.Atoi(number)
		if !ok || e != nil || num < 1 || (typ != "pr" && typ != "issue") {
			return res, composerError(ctx, "source_invalid")
		}
		var item *worktreeSource
		if e = o.c.Call(ctx, 30*time.Second, "github.workItem", map[string]any{"repo": selector, "number": num, "type": typ}, &item); e != nil {
			return res, e
		}
		if item == nil || item.Number != num || item.Type != typ {
			return res, composerError(ctx, "source_missing")
		}
		if in.SourceURL == "" || item.URL != in.SourceURL {
			return res, composerError(ctx, "source_changed")
		}
		p["displayName"], p["displayNameKind"] = item.Title, "user"
		delete(p, "nameWasGenerated")
		if strings.TrimSpace(in.Name) == "" {
			p["name"] = typ + "-" + number
		}
		if typ == "pr" && reuseBranch {
			return res, composerError(ctx, "source_branch_conflict")
		}
		if typ == "issue" {
			p["linkedIssue"] = num
		} else {
			p["linkedPR"] = num
			var resolved struct {
				Base    string          `json:"baseBranch"`
				Compare string          `json:"compareBaseRef"`
				Branch  string          `json:"branchNameOverride"`
				Push    json.RawMessage `json:"pushTarget"`
				Error   string          `json:"error"`
			}
			if e = o.c.Call(ctx, orcart.WorktreeTimeout, "worktree.resolvePrBase", map[string]any{"repo": selector, "prNumber": num, "headRefName": item.Branch,
				"baseRefName": item.Base, "isCrossRepository": item.Cross}, &resolved); e != nil {
				return res, e
			}
			if resolved.Error != "" {
				return res, errors.New(resolved.Error)
			}
			if resolved.Base == "" {
				return res, composerError(ctx, "pr_unresolved")
			}
			p["baseBranch"] = resolved.Base
			if resolved.Compare != "" {
				p["compareBaseRef"] = resolved.Compare
			}
			if branch == "" && resolved.Branch != "" {
				p["branchNameOverride"] = resolved.Branch
			}
			if len(resolved.Push) > 0 && string(resolved.Push) != "null" {
				p["pushTarget"] = resolved.Push
			}
		}
	}
	var result struct {
		Worktree orcart.CreatedWorktree `json:"worktree"`
	}
	if err = o.c.Call(ctx, orcart.WorktreeTimeout, "worktree.create", p, &result); err != nil {
		return res, orcaErr(err)
	}
	if result.Worktree.ID == "" || result.Worktree.Path == "" {
		return res, composerError(ctx, "create_unconfirmed")
	}
	actualBranch := strings.TrimPrefix(result.Worktree.Branch, "refs/heads/")
	// A confirmed creation is always returned with the runtime's actual branch/path, including review suffixes.
	// Rejecting an acknowledged result would turn a harmless naming choice into a duplicate creation on retry.
	_ = n.refreshHosts(ctx, o)
	return proto.WorktreeCreateResult{WorkspaceID: result.Worktree.ID, Path: result.Worktree.Path,
		Branch: actualBranch, ParentWorkspaceID: in.Parent}, nil
}

type worktreeSource struct {
	Type   string `json:"type"`
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Branch string `json:"branchName,omitempty"`
	Base   string `json:"baseRefName,omitempty"`
	Cross  bool   `json:"isCrossRepository,omitempty"`
}

func (n *Node) worktreeSources(ctx context.Context, kind, id string, raw json.RawMessage) (any, error) {
	var in struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	o, w, _, err := n.orcaWorkspace(ctx, kind, id)
	if err != nil {
		return nil, err
	}
	if _, err = composerCapabilities(ctx, o); err != nil {
		return nil, err
	}
	if folderComposerWorkspace(w) {
		return nil, composerError(ctx, "git_source_required")
	}
	selector, err := orcaRepoSelector(ctx, w)
	if err != nil {
		return nil, err
	}
	query := strings.TrimSpace(in.Query)
	number, typ := strings.TrimPrefix(query, "#"), ""
	urlQuery := strings.ToLower(query)
	if strings.HasPrefix(urlQuery, "https://") || strings.HasPrefix(urlQuery, "http://") {
		u, e := url.Parse(query)
		if e != nil {
			return nil, composerError(ctx, "url_invalid")
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if (u.Scheme != "https" && u.Scheme != "http") || len(parts) != 4 || (parts[2] != "pull" && parts[2] != "issues") {
			return nil, composerError(ctx, "url_invalid")
		}
		number = parts[3]
		if parts[2] == "pull" {
			typ = "pr"
		} else {
			typ = "issue"
		}
	}
	if num, e := strconv.Atoi(number); e == nil && num > 0 {
		p := map[string]any{"repo": selector, "number": num}
		if typ != "" {
			p["type"] = typ
		}
		var item *worktreeSource
		if err = o.c.Call(ctx, 30*time.Second, "github.workItem", p, &item); err != nil {
			return nil, err
		}
		if item == nil {
			return nil, composerError(ctx, "source_not_found")
		}
		if typ != "" && !strings.EqualFold(strings.TrimRight(item.URL, "/"), strings.TrimRight(query, "/")) {
			return nil, composerError(ctx, "url_project")
		}
		return map[string]any{"items": []worktreeSource{*item}}, nil
	}
	var result json.RawMessage
	err = o.c.Call(ctx, 30*time.Second, "github.listWorkItems", map[string]any{"repo": selector, "query": query, "limit": 30}, &result)
	if err == nil {
		var response map[string]any
		if json.Unmarshal(result, &response) == nil {
			if sourceErrors, ok := response["errors"].(map[string]any); ok && len(sourceErrors) > 0 {
				response["warning"] = i18n.T(i18n.From(ctx), "worktree-composer.github_unavailable")
				return response, nil
			}
		}
	}
	return result, err
}

func (n *Node) worktreeSavePreset(ctx context.Context, kind, id string, raw json.RawMessage) (any, error) {
	var in struct{ Name, Directories string }
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	o, w, _, err := n.orcaWorkspace(ctx, kind, id)
	if err != nil {
		return nil, err
	}
	if _, err = composerCapabilities(ctx, o); err != nil {
		return nil, err
	}
	if folderComposerWorkspace(w) {
		return nil, composerError(ctx, "git_source_required")
	}
	selector, err := orcaRepoSelector(ctx, w)
	if err != nil {
		return nil, err
	}
	dirs := []string{}
	for _, dir := range strings.Split(in.Directories, "\n") {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		clean := filepath.ToSlash(filepath.Clean(dir))
		if strings.ContainsAny(dir, "\x00\r") || filepath.IsAbs(dir) || strings.Contains(dir, "\\") || clean == ".." || strings.HasPrefix(clean, "../") || clean == ".git" || strings.HasPrefix(clean, ".git/") {
			return nil, composerError(ctx, "preset_path")
		}
		dirs = append(dirs, clean)
	}
	if strings.TrimSpace(in.Name) == "" || len(dirs) == 0 {
		return nil, composerError(ctx, "preset_empty")
	}
	var result json.RawMessage
	err = o.c.Call(ctx, orcart.CallTimeout, "repo.saveSparsePreset", map[string]any{"repo": selector, "name": strings.TrimSpace(in.Name), "directories": dirs}, &result)
	return result, err
}
