package assistant

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// launcher_import is the managed MCP facade over the existing shell-alias import engine. Its request deliberately has
// no credential, header, environment or URL fields: discovery and commit both read those only through launchers.zshrc.
// Explicit output DTOs keep the secret-bearing importGroup and storedLauncher out of model-facing JSON.
const (
	launcherImportMaxInputBytes    = 64 << 10
	launcherImportMaxNames         = 128
	launcherImportOperationTimeout = 30 * time.Second
)

type launcherImportInput struct {
	Action        string   `json:"action"`
	Machine       string   `json:"machine"`
	Names         []string `json:"names"`
	Split         bool     `json:"split"`
	HideOriginals bool     `json:"hide_originals"`
}

// launcherImportProvider is safe for both preview and commit readback. ID is present only for a stored provider;
// Existing and CreateAs describe the import landing. No field contains a credential, header or environment value.
type launcherImportProvider struct {
	ID         string                   `json:"id,omitempty"`
	Name       string                   `json:"name"`
	Aliases    []string                 `json:"aliases,omitempty"`
	BaseURL    string                   `json:"base_url"`
	Models     []proto.ProviderModel    `json:"models"`
	HasKey     bool                     `json:"has_key"`
	HasHeaders bool                     `json:"has_headers"`
	HasEnv     bool                     `json:"has_env"`
	EnvNames   []string                 `json:"env_names,omitempty"`
	Existing   string                   `json:"existing,omitempty"`
	CreateAs   string                   `json:"create_as,omitempty"`
	Differs    []string                 `json:"differs,omitempty"`
	Variants   []launcherImportProvider `json:"variants,omitempty"`
}

type launcherImportPreviewOutput struct {
	Machine   string                   `json:"machine"`
	Providers []launcherImportProvider `json:"providers"`
}

type launcherImportCommitOutput struct {
	Machine   string                   `json:"machine"`
	Created   []string                 `json:"created"`
	Updated   []string                 `json:"updated"`
	Hidden    []string                 `json:"hidden"`
	Providers []launcherImportProvider `json:"providers"`
}

var (
	errLauncherImportName      = errors.New("launcher_import: every name must identify a preview provider or shell alias")
	errLauncherImportDiscovery = errors.New("launcher_import: could not discover providers; check machine availability and node version")
	errLauncherImportCommit    = errors.New("launcher_import: import did not complete; check current provider state before retrying")
	errLauncherImportReadback  = errors.New("launcher_import: import committed, but non-sensitive readback is unavailable")
	errLauncherImportOperation = errors.New("launcher_import: operation failed without diagnostic details; check current provider state before retrying because the outcome may be uncertain")
)

func decodeLauncherImportInput(args json.RawMessage) (launcherImportInput, error) {
	var in launcherImportInput
	if len(args) > launcherImportMaxInputBytes {
		return in, fmt.Errorf("launcher_import: input exceeds %d bytes", launcherImportMaxInputBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return in, errors.New("launcher_import: invalid JSON input")
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return in, errors.New("launcher_import: exactly one JSON object is required")
	}
	in.Action = strings.TrimSpace(in.Action)
	in.Machine = strings.TrimSpace(in.Machine)
	if in.Action != "preview" && in.Action != "commit" {
		return in, errors.New("launcher_import: action must be preview or commit")
	}
	if in.Machine == "" {
		return in, errors.New("launcher_import: machine is required")
	}
	if in.Action == "preview" && (in.Split || in.HideOriginals) {
		return in, errors.New("launcher_import: split and hide_originals apply only to commit")
	}
	if in.Action == "commit" && len(in.Names) == 0 {
		return in, errors.New("launcher_import: commit requires a nonempty names list")
	}
	if len(in.Names) > launcherImportMaxNames {
		return in, fmt.Errorf("launcher_import: names accepts at most %d entries", launcherImportMaxNames)
	}
	names := make([]string, 0, len(in.Names))
	for _, name := range in.Names {
		name = strings.TrimSpace(name)
		if name == "" {
			return in, errors.New("launcher_import: names cannot contain an empty name")
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	in.Names = names
	return in, nil
}

// launcherImportGroups applies names without weakening the engine's grouping rules. A preview provider name selects
// its whole group; a raw shell alias (or a variant's normalized name) selects only that alias. Selected aliases are
// merged again through mergeZshrcAliases, so models, Existing and variants follow the same rules as an unfiltered import.
func (s *Service) launcherImportGroups(ctx context.Context, in launcherImportInput) ([]importGroup, []storedLauncher, error) {
	aliases, err := zshrcAliases(ctx, s, in.Machine)
	if err != nil {
		return nil, nil, errLauncherImportDiscovery
	}
	stored, known, err := s.storedKnown(ctx)
	if err != nil {
		return nil, nil, errLauncherImportDiscovery
	}
	groups := mergeZshrcAliases(in.Machine, aliases, known)
	if len(in.Names) == 0 {
		return groups, stored, nil
	}
	selected := map[string]bool{}
	for _, name := range in.Names {
		found := false
		for _, g := range groups {
			if g.Name == name {
				for _, alias := range g.Aliases {
					selected[alias] = true
				}
				found = true
				break
			}
			if slices.Contains(g.Aliases, name) {
				selected[name] = true
				found = true
				break
			}
			for _, variant := range g.variants {
				if variant.Name == name {
					selected[variant.Aliases[0]] = true
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return nil, nil, errLauncherImportName
		}
	}
	aliases = slices.DeleteFunc(slices.Clone(aliases), func(a zshrcAlias) bool { return !selected[a.Alias] })
	return mergeZshrcAliases(in.Machine, aliases, known), stored, nil
}

func launcherImportProviderOf(g importGroup) launcherImportProvider {
	out := launcherImportProvider{
		Name: g.Name, Aliases: nonNil(slices.Clone(g.Aliases)), BaseURL: g.BaseURL,
		Models: slices.Clone(g.Models), HasKey: g.HasKey, HasHeaders: g.HasHeaders, HasEnv: g.HasEnv,
		EnvNames: slices.Clone(g.EnvNames), Existing: g.Existing, CreateAs: g.CreateAs, Differs: slices.Clone(g.Differs),
	}
	if out.Models == nil {
		out.Models = []proto.ProviderModel{}
	}
	for _, variant := range g.variants {
		out.Variants = append(out.Variants, launcherImportProviderOf(variant))
	}
	return out
}

func launcherImportStoredProvider(l storedLauncher) launcherImportProvider {
	models := slices.Clone(l.Models)
	if models == nil {
		models = []proto.ProviderModel{}
	}
	return launcherImportProvider{
		ID: "launcher." + l.Name, Name: l.Name, BaseURL: l.LaunchBaseURL(), Models: models,
		HasKey: l.KeySealed != "", HasHeaders: l.HeadersSealed != "", HasEnv: l.EnvSealed != "",
	}
}

func launcherImportJSON(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", errors.New("launcher_import: could not encode the non-sensitive result")
	}
	return string(raw), nil
}

func (s *Service) launcherImportPreview(ctx context.Context, in launcherImportInput) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, launcherImportOperationTimeout)
	defer cancel()
	groups, stored, err := s.launcherImportGroups(ctx, in)
	if err != nil {
		return "", err
	}
	ctx = withSettingsMemo(ctx)
	rc := s.zshrcLaunchers(ctx, false)
	importNames(groups, s.importTaken(ctx, rc, stored))
	s.importPreviewSources(ctx, in.Machine, rc, stored, groups)
	out := launcherImportPreviewOutput{Machine: in.Machine, Providers: make([]launcherImportProvider, 0, len(groups))}
	for _, g := range groups {
		out.Providers = append(out.Providers, launcherImportProviderOf(g))
	}
	return launcherImportJSON(out)
}

func launcherImportBool(l i18n.Lang, value bool) string {
	if value {
		return i18n.T(l, "assistant-settings_tool.on")
	}
	return i18n.T(l, "assistant-settings_tool.off")
}

func launcherImportCard(l i18n.Lang, picks []importGroup, in launcherImportInput) string {
	lines := make([]string, 0, len(picks)+1)
	for _, g := range picks {
		action, name := i18n.T(l, "assistant-settings_tool.launcher_import_create"), cmp.Or(g.CreateAs, g.Name)
		if g.Existing != "" {
			action, name = i18n.T(l, "assistant-settings_tool.launcher_import_update"), g.Existing
		}
		models := make([]string, 0, len(g.Models))
		for _, model := range g.Models {
			models = append(models, model.ID)
		}
		modelList := i18n.T(l, "assistant-settings_tool.none")
		if len(models) > 0 {
			modelList = strings.Join(models, ", ")
		}
		lines = append(lines, i18n.T(l, "assistant-settings_tool.launcher_import_provider", i18n.Args{
			"action": action, "name": name, "aliases": strings.Join(g.Aliases, ", "), "base_url": g.BaseURL,
			"models": modelList, "key": launcherImportBool(l, g.HasKey), "headers": launcherImportBool(l, g.HasHeaders),
			"env": launcherImportBool(l, g.HasEnv),
		}))
	}
	lines = append(lines, i18n.T(l, "assistant-settings_tool.launcher_import_options", i18n.Args{
		"split": launcherImportBool(l, in.Split), "hide": launcherImportBool(l, in.HideOriginals),
	}))
	return strings.Join(lines, "\n")
}

func (s *Service) prepareLauncherImportCommit(ctx context.Context, in launcherImportInput) (change *ownChange, err error) {
	// Re-preparation also runs on the approval-resolution goroutine, outside toolLauncherImport's recovery stack.
	defer func() {
		if recover() != nil {
			change, err = nil, errLauncherImportOperation
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, launcherImportOperationTimeout)
	defer cancel()
	groups, stored, err := s.launcherImportGroups(ctx, in)
	if err != nil {
		return nil, err
	}
	ctx = withSettingsMemo(ctx)
	rc := s.zshrcLaunchers(ctx, false)
	importNames(groups, s.importTaken(ctx, rc, stored))
	body := importBody{Machine: in.Machine, Providers: make([]string, 0, len(groups)), HideAliases: in.HideOriginals}
	for _, g := range groups {
		body.Providers = append(body.Providers, g.Name)
		if in.Split && len(g.variants) > 0 {
			body.Split = append(body.Split, g.Name)
		}
	}
	picks, err := importPicks(groups, body)
	if err != nil || len(picks) == 0 {
		return nil, errLauncherImportName
	}
	remote := s.importRemoteAliases(ctx, groups, body, rc, stored)
	return &ownChange{
		cardText: func(l i18n.Lang) string { return launcherImportCard(l, picks, in) },
		apply: func(ctx context.Context) (text string, err error) {
			committed := false
			defer func() {
				if recover() == nil {
					return
				}
				text = ""
				if committed {
					err = errLauncherImportReadback
				} else {
					err = errLauncherImportOperation
				}
			}()
			ctx, cancel := context.WithTimeout(ctx, launcherImportOperationTimeout)
			defer cancel()
			result, err := s.importLaunchers(ctx, groups, body, rc, remote)
			if err != nil {
				return "", errLauncherImportCommit
			}
			committed = true
			var list []storedLauncher
			func() {
				s.models.mu.Lock()
				defer s.models.mu.Unlock()
				list, err = s.readStoredLaunchers(ctx)
			}()
			if err != nil {
				return "", errLauncherImportReadback
			}
			out := launcherImportCommitOutput{
				Machine: in.Machine, Created: nonNil(result.Created), Updated: nonNil(result.Updated), Hidden: nonNil(result.Hidden),
				Providers: make([]launcherImportProvider, 0, len(result.Created)+len(result.Updated)),
			}
			for _, name := range append(slices.Clone(result.Created), result.Updated...) {
				if i := slices.IndexFunc(list, func(l storedLauncher) bool { return l.Name == name }); i >= 0 && !slices.ContainsFunc(out.Providers, func(p launcherImportProvider) bool { return p.Name == name }) {
					out.Providers = append(out.Providers, launcherImportStoredProvider(list[i]))
				}
			}
			return launcherImportJSON(out)
		},
	}, nil
}

func redactedLauncherImportError(err error) error {
	// Approval wraps some apply errors with %w. Match known fixed diagnostics, but return the sentinel itself: the
	// original wrapper or its Error method may still contain discovered credentials.
	for _, fixed := range []error{
		errLauncherImportName, errLauncherImportDiscovery, errLauncherImportCommit,
		errLauncherImportReadback, errLauncherImportOperation,
	} {
		if errors.Is(err, fixed) {
			return fixed
		}
	}
	return errLauncherImportOperation
}

func (s *Service) toolLauncherImport(ctx context.Context, call ToolCall, raw json.RawMessage) (text string, err error) {
	// This is a confidentiality boundary, including for broken RPC/store error implementations. Do not let an outer
	// recovery format a panic value that may contain discovered credentials; the model gets one fixed diagnostic.
	defer func() {
		if recover() != nil {
			text, err = "", errLauncherImportOperation
		}
	}()
	in, err := decodeLauncherImportInput(raw)
	if err != nil {
		return "", err
	}
	if in.Action == "preview" {
		return s.launcherImportPreview(ctx, in)
	}
	setting := ownSetting{
		Setting: Setting{Key: "launcher_import", Title: "Launcher import"}, toolName: "launcher_import",
		cardTitle: func(l i18n.Lang) string { return i18n.T(l, "assistant-settings_tool.title_launcher_import") },
		set: func(ctx context.Context, _ json.RawMessage) (*ownChange, error) {
			return s.prepareLauncherImportCommit(ctx, in)
		},
	}
	text, err = s.ownSettingAction(ctx, call, setting, "set", raw)
	if err != nil {
		return "", redactedLauncherImportError(err)
	}
	return text, nil
}
