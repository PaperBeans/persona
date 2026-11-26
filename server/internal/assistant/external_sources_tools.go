package assistant

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"time"

	approvalops "github.com/nwverse/persona/server/internal/approval"
	"github.com/nwverse/persona/server/internal/assistant/externalbinding/externalbindingadapt"
	"github.com/nwverse/persona/server/internal/assistant/externalsourcestore"
	w "github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

const (
	externalSourcesReadTool  = "external_sources_read"
	externalSourcesWriteTool = "external_sources_write"
)

func isExternalSourceTool(name string) bool {
	return name == externalSourcesReadTool || name == externalSourcesWriteTool
}

// The rules can read and affect multiple directions. Only the owner's main
// conversation, on its configured machine, can discover or call these tools.
func (s *Service) externalSourceToolCaller(ctx context.Context, machine, session string) bool {
	if machine == "" || session == "" || s.isWorkMachine(machine) {
		return false
	}
	saved, ok := s.savedSettings(ctx)
	return ok && saved.Main == session && s.sameMachine(machine, s.effective(ctx, saved).mainRouteMachine())
}

func (s *Service) externalSourceTools(ctx context.Context, machine, session string) []proto.Tool {
	if !s.externalSourceToolCaller(ctx, machine, session) {
		return nil
	}
	return localizedMCPTools(toolsNamed([]string{externalSourcesReadTool, externalSourcesWriteTool}), s.toolLanguage(ctx))
}

type externalSourceChangeStore interface {
	externalSourceStore
	ChangeExternalBinding(context.Context, w.ExternalBinding, *w.ExternalBindingInput) error
	ChangeExternalSourcePolicy(context.Context, w.ExternalScopedPolicy, w.ExternalPolicy) error
}

func (s *Service) toolExternalSources(ctx context.Context, call ToolCall, raw json.RawMessage) proto.ToolResult {
	if !s.externalSourceToolCaller(ctx, call.MachineID, call.SessionID) {
		return proto.TextResult("Manage external source routing from the main Persona conversation.", true)
	}
	store, ok := s.store().(externalSourceStore)
	if !ok {
		return proto.TextResult("External source routing is unavailable.", true)
	}
	if call.Name == externalSourcesReadTool {
		result, err := readExternalSources(ctx, store, raw)
		if err != nil {
			return proto.TextResult(err.Error(), true)
		}
		text, err := json.Marshal(result)
		if err != nil {
			return proto.TextResult(err.Error(), true)
		}
		return proto.TextResult(string(text), false)
	}
	writer, ok := store.(externalSourceChangeStore)
	if !ok {
		return proto.TextResult("External source routing is unavailable.", true)
	}
	text, err := s.writeExternalSources(ctx, call, writer, raw)
	if err != nil {
		return proto.TextResult(err.Error(), true)
	}
	return proto.TextResult(text, false)
}

type externalSourcesReadInput struct {
	Action     string               `json:"action"`
	Ref        *w.ExternalRef       `json:"external_ref"`
	Binding    w.ExternalBindingID  `json:"binding_id"`
	TargetKind w.ExternalTargetKind `json:"target_kind"`
	TargetID   w.ExternalTargetID   `json:"target_id"`
}

func readExternalSources(ctx context.Context, store externalSourceStore, raw json.RawMessage) (any, error) {
	var in *externalSourcesReadInput
	if err := jsonv2.Unmarshal(raw, &in, jsonv2.RejectUnknownMembers(true)); err != nil || in == nil {
		return nil, errors.New("invalid external source query")
	}
	var path []w.ExternalRef
	if in.Ref != nil {
		var err error
		path, err = externalbindingadapt.Path(*in.Ref)
		if err != nil {
			return nil, err
		}
		in.Ref = &path[0]
	}
	switch in.Action {
	case "list":
		if in.Binding != "" {
			return nil, errors.New("use inspect to select a binding")
		}
		if in.TargetKind != "" || in.TargetID != "" {
			kind, err := externalbindingadapt.TargetKind(in.TargetKind)
			if err != nil {
				return nil, err
			}
			id, err := externalbindingadapt.TargetID(in.TargetID)
			if err != nil {
				return nil, err
			}
			in.TargetKind, in.TargetID = kind, id
		}
		list, err := store.ListExternalBindings(ctx, in.TargetKind, in.TargetID, "", in.Ref)
		return w.ExternalBindingList{Bindings: list}, err
	case "inspect":
		if len(path) == 0 || in.TargetKind != "" || in.TargetID != "" {
			return nil, errors.New("inspect takes an external source and an optional binding ID")
		}
		resolved, policies, err := resolveExternalSourcePolicy(ctx, store, path, in.Binding)
		if err != nil {
			return nil, err
		}
		lookup, err := store.LookupExternalBindings(ctx, path)
		return struct {
			Path      []w.ExternalRef           `json:"path"`
			Matches   []w.ExternalBindingMatch  `json:"matches"`
			Overrides []w.ExternalScopedPolicy  `json:"overrides"`
			Effective w.ExternalEffectivePolicy `json:"effective"`
		}{lookup.Path, lookup.Matches, policies, resolved}, err
	default:
		return nil, errors.New("action must be list or inspect")
	}
}

type externalSourcesWriteInput struct {
	Action     string               `json:"action"`
	ID         w.ExternalBindingID  `json:"binding_id,omitempty"`
	Ref        *w.ExternalRef       `json:"external_ref,omitempty"`
	TargetKind w.ExternalTargetKind `json:"target_kind,omitempty"`
	TargetID   w.ExternalTargetID   `json:"target_id,omitempty"`
	Policy     *w.ExternalPolicy    `json:"policy,omitempty"`
}

type externalSourceChange struct {
	summary string
	before  any
	apply   func(context.Context) (any, error)
}

func (s *Service) writeExternalSources(ctx context.Context, call ToolCall, store externalSourceChangeStore, raw json.RawMessage) (string, error) {
	var in *externalSourcesWriteInput
	if err := jsonv2.Unmarshal(raw, &in, jsonv2.RejectUnknownMembers(true)); err != nil || in == nil {
		return "", errors.New("invalid external source change")
	}
	change, err := s.prepareExternalSourceChange(ctx, store, in)
	if err != nil {
		return "", err
	}
	shown, err := json.Marshal(map[string]any{"change": in, "previous": change.before})
	if err != nil {
		return "", err
	}
	l := s.lang(ctx)
	now := time.Now().UTC()
	outcome, err := s.waitPreparedCard(ctx, proto.Interaction{
		ID: "ix_" + randomHex(6), MachineID: call.MachineID, SessionID: call.SessionID,
		Kind: proto.IxPermission, ToolName: externalSourcesWriteTool, Summary: change.summary,
		ToolInput: shown, SessionTitle: "Persona", CreatedAt: now, ExpiresAt: now.Add(settingCardWait),
	}, approvalops.Operation{
		Denied: i18n.T(l, "assistant-external_sources.declined"),
		Apply: func(ctx context.Context, _ proto.Resolution) (string, error) {
			if !s.externalSourceToolCaller(ctx, call.MachineID, call.SessionID) {
				return "", approvalops.ErrStale
			}
			if in.TargetID != "" {
				if err := s.checkExternalTarget(ctx, in.TargetKind, in.TargetID); err != nil {
					return "", fmt.Errorf("%w: %v", approvalops.ErrNotApplied, err)
				}
			}
			saved, err := change.apply(ctx)
			switch {
			case errors.Is(err, externalsourcestore.ErrChangeStale):
				return "", approvalops.ErrStale
			case errors.Is(err, externalsourcestore.ErrBindingExists), errors.Is(err, db.ErrTodoNotFound):
				return "", fmt.Errorf("%w: %v", approvalops.ErrNotApplied, err)
			case err != nil:
				return "", err
			}
			text, err := json.Marshal(map[string]any{"saved": saved, "previous": change.before})
			return string(text), err
		},
	})
	return outcome.Text, err
}

func (s *Service) prepareExternalSourceChange(ctx context.Context, store externalSourceChangeStore, in *externalSourcesWriteInput) (externalSourceChange, error) {
	var change externalSourceChange
	l := s.lang(ctx)
	if in.Action == "set_policy" {
		if in.Ref == nil || in.Policy == nil || in.ID != "" || in.TargetKind != "" || in.TargetID != "" {
			return change, errors.New("set_policy takes an external source and a replacement policy")
		}
		path, err := externalbindingadapt.Path(*in.Ref)
		if err != nil {
			return change, err
		}
		policy, err := externalbindingadapt.Policy(*in.Policy)
		if err != nil {
			return change, err
		}
		row := w.ExternalScopedPolicy{ExternalRef: path[0], Policy: policy}
		in.Ref, in.Policy = &row.ExternalRef, &row.Policy
		policies, _, err := store.ExternalSourcePolicies(ctx, path[:1], "")
		if err != nil {
			return change, err
		}
		before := w.ExternalScopedPolicy{ExternalRef: row.ExternalRef}
		if len(policies) != 0 {
			before = policies[0]
		}
		change.summary = i18n.T(l, "assistant-external_sources.set_policy", i18n.Args{"source": externalSourceLabel(row.ExternalRef)}) +
			externalPolicyApprovalText(l, policy)
		change.before = before
		change.apply = func(ctx context.Context) (any, error) {
			return row, store.ChangeExternalSourcePolicy(ctx, row, before.Policy)
		}
		return change, nil
	}
	switch in.Action {
	case "create_binding", "update_binding":
		if in.Ref == nil || (in.Action == "update_binding") != (in.ID != "") {
			return change, errors.New("create_binding takes a source and target. update_binding also takes the binding ID")
		}
	case "delete_binding":
		if in.ID == "" || in.Ref != nil || in.TargetKind != "" || in.TargetID != "" || in.Policy != nil {
			return change, errors.New("delete_binding takes the binding ID alone")
		}
	default:
		return change, errors.New("unknown external source change")
	}
	var binding w.ExternalBindingInput
	if in.Ref != nil {
		binding = w.ExternalBindingInput{ExternalRef: *in.Ref, TargetKind: in.TargetKind, TargetID: in.TargetID}
		if in.Policy != nil {
			binding.Policy = *in.Policy
		}
		normalized, err := externalbindingadapt.Binding(binding)
		if err != nil {
			return change, err
		}
		binding = normalized
		if err := s.checkExternalTarget(ctx, binding.TargetKind, binding.TargetID); err != nil {
			return change, err
		}
		in.Ref, in.TargetKind, in.TargetID, in.Policy = &binding.ExternalRef, binding.TargetKind, binding.TargetID, &binding.Policy
	}
	var before *w.ExternalBinding
	if in.ID != "" {
		var err error
		_, before, err = store.ExternalSourcePolicies(ctx, []w.ExternalRef{}, in.ID)
		if err != nil {
			return change, err
		}
		if before == nil {
			return change, externalsourcestore.ErrBindingNotFound
		}
		change.before = before
	}
	switch in.Action {
	case "create_binding":
		change.summary = i18n.T(l, "assistant-external_sources.create_binding", i18n.Args{"binding": s.externalBindingLabel(ctx, binding)}) +
			externalPolicyApprovalText(l, binding.Policy)
		change.apply = func(ctx context.Context) (any, error) {
			return store.CreateExternalBinding(ctx, binding)
		}
	case "update_binding":
		change.summary = i18n.T(l, "assistant-external_sources.update_binding", i18n.Args{
			"before": s.externalBindingLabel(ctx, before.ExternalBindingInput), "after": s.externalBindingLabel(ctx, binding),
		}) + externalPolicyApprovalText(l, binding.Policy)
		change.apply = func(ctx context.Context) (any, error) {
			return w.ExternalBinding{ID: in.ID, ExternalBindingInput: binding}, store.ChangeExternalBinding(ctx, *before, &binding)
		}
	case "delete_binding":
		change.summary = i18n.T(l, "assistant-external_sources.delete_binding", i18n.Args{"binding": s.externalBindingLabel(ctx, before.ExternalBindingInput)})
		change.apply = func(ctx context.Context) (any, error) { return nil, store.ChangeExternalBinding(ctx, *before, nil) }
	}
	return change, nil
}

func (s *Service) checkExternalTarget(ctx context.Context, kind w.ExternalTargetKind, id w.ExternalTargetID) error {
	if kind == w.ExternalTargetDirection {
		exists, err := s.externalDirectionExists(ctx, id)
		if err != nil {
			return err
		}
		if !exists {
			return errNoDirection
		}
		return nil
	}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return err
	}
	if _, exists := s.todoNow(string(id)); !exists {
		return db.ErrTodoNotFound
	}
	return nil
}

func externalSourceLabel(ref w.ExternalRef) string {
	provider := string(ref.Provider)
	switch ref.Provider {
	case "lark":
		provider = "Lark"
	case "github":
		provider = "GitHub"
	}
	parts := []string{provider}
	for _, part := range []string{string(ref.Channel), string(ref.Container.ID), string(ref.Item.ID)} {
		if part != "" {
			parts = append(parts, clip(oneLine(part), 128))
		}
	}
	return strings.Join(parts, " / ")
}

func (s *Service) externalBindingLabel(ctx context.Context, binding w.ExternalBindingInput) string {
	label := string(binding.TargetID)
	if binding.TargetKind == w.ExternalTargetTodo {
		if t, ok := s.todoNow(label); ok && t.Title != "" {
			label = t.Title
		}
	} else {
		s.directions.mu.Lock()
		if s.directionsLocked(ctx) {
			if d := s.directions.m[label]; d != nil && d.Name != "" {
				label = d.Name
			}
		}
		s.directions.mu.Unlock()
	}
	return externalSourceLabel(binding.ExternalRef) + " → " + clip(oneLine(label), 120)
}

func externalPolicyApprovalText(l i18n.Lang, p w.ExternalPolicy) string {
	const prefix = "assistant-external_sources."
	var parts []string
	if p.Mode != nil {
		switch *p.Mode {
		case "realtime":
			parts = append(parts, i18n.T(l, prefix+"realtime"))
		case "digest":
			parts = append(parts, i18n.T(l, prefix+"digest"))
		case "on_demand":
			parts = append(parts, i18n.T(l, prefix+"on_demand"))
		case "off":
			parts = append(parts, i18n.T(l, prefix+"off"))
		}
	}
	if p.DigestIntervalMinutes != nil {
		parts = append(parts, i18n.T(l, prefix+"interval", i18n.Args{"minutes": fmt.Sprint(*p.DigestIntervalMinutes)}))
	}
	if p.ActionRole != nil {
		if *p.ActionRole == "fyi" {
			parts = append(parts, i18n.T(l, prefix+"fyi"))
		} else {
			parts = append(parts, i18n.T(l, prefix+"owner"))
		}
	}
	switch {
	case p.NewItemPolicy == nil:
		parts = append(parts, i18n.T(l, prefix+"inherit_new_work"))
	case *p.NewItemPolicy == "auto_todo":
		parts = append(parts, i18n.T(l, prefix+"auto_todo"))
	default:
		parts = append(parts, i18n.T(l, prefix+"ask_owner"))
	}
	return "\n\n" + strings.Join(parts, " ")
}
