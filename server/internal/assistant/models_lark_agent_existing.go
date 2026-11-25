package assistant

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

const (
	larkAgentInheritedModelKey = "lark_agent_inherited_model"
	larkAgentSwitchPrefix      = "lark-model:"
)

// A Lark /agent conversation follows its purpose for every new input, not only
// when the extension first opens it. An execution change uses the same durable
// switch and first-input queue as every other cross-engine conversation move.
func (s *Service) larkAgentFollowsCurrentModel(ctx context.Context, machine, sid string, params map[string]any, lang i18n.Lang) (bool, error) {
	if launcher, _ := params["launcher"].(string); launcher != "" {
		return false, nil
	}
	if model, _ := params["model"].(string); strings.TrimSpace(model) != "" {
		return false, nil
	}
	if !s.larkAgentConversation(ctx, machine, sid) {
		return false, nil
	}
	binding, err := s.resolveModel(ctx, purposeLarkAgent)
	if err != nil {
		return false, err
	}
	launcher, ok := strings.CutPrefix(binding.Provider, "launcher.")
	if !ok || launcher == "" || proto.IsExternalLauncher(launcher) {
		// The ordinary purpose validation and start path reject unsupported
		// providers; do not guess an execution identity here.
		return false, nil
	}
	row, known := s.session(ctx, machine, sid)
	if !known || !row.Assistant || row.PaneID != "" || row.IsDeleted() {
		return false, nil
	}
	current, currentEngine, err := s.larkAgentCurrentExecution(ctx, machine, sid, row)
	if err != nil || current.Launcher == "" {
		return false, err
	}
	executionMachine := cmp.Or(current.Machine, machine)
	targetEngine := currentEngine
	if current.Launcher != launcher {
		targetEngine, err = s.larkAgentExecutionIdentity(ctx, executionMachine, launcher)
	}
	if err != nil {
		return true, err
	}
	if targetEngine == "" {
		return true, fmt.Errorf("the current Lark /agent launcher %q has no known execution identity; the message was not sent on the previous model", launcher)
	}
	if currentEngine != targetEngine {
		if err := s.larkAgentStartSwitch(ctx, machine, sid, conversationExecution{
			Machine: executionMachine, Launcher: launcher, Model: binding.Model, Effort: binding.Effort,
		}); err != nil {
			return false, err
		}
		// The caller's original input continues through sendModel to the
		// physical logical-send boundary, which queues it behind this switch.
		return false, nil
	}
	// A model or effort change within one execution path needs no new segment.
	// Apply the whole purpose binding, not just its launcher name.
	params["launcher"] = launcher
	if err := s.larkAgentApplyCurrentModel(ctx, params, binding, launcher, lang); err != nil {
		return true, err
	}
	params[larkAgentInheritedModelKey] = true
	defer delete(params, larkAgentInheritedModelKey)
	_, err = s.logicalSendModel(ctx, machine, sid, params)
	return true, err
}

func (s *Service) larkAgentConversation(ctx context.Context, machine, sid string) bool {
	b := &s.origins
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.originsLocked(ctx) {
		return false
	}
	o := b.m[sid]
	return o != nil && o.Origin == OriginLarkAgent && (o.MachineID == "" || o.MachineID == machine)
}

func (s *Service) larkAgentApplyCurrentModel(ctx context.Context, params map[string]any, binding ModelBinding, launcher string, lang i18n.Lang) error {
	delete(params, "model_fingerprint")
	delete(params, "model")
	delete(params, "effort")
	if err := s.applyModelBinding(ctx, purposeLarkAgent, params, false); err != nil {
		return err
	}
	if binding.Effort != "" && params["effort"] != binding.Effort {
		return fmt.Errorf("the current Lark /agent effort %q was not applied to launcher %q", binding.Effort, launcher)
	}
	return nil
}

func (s *Service) larkAgentCurrentExecution(ctx context.Context, machine, sid string, row proto.Session) (conversationExecution, string, error) {
	st, ok := s.store().(logicalSwitchStore)
	if !ok {
		return conversationExecutionNow(machine, row, nil), proto.AssistantExecutionIdentity(sessionEngineAgent(row), row.SourceKind, row.SourceInstanceID, row.Launcher), nil
	}
	logical, err := st.LogicalConversation(ctx, machine, sid)
	if err != nil {
		return conversationExecution{}, "", err
	}
	current := conversationExecutionNow(machine, row, logical)
	if logical != nil && logical.Epoch > 1 {
		if active, ok := logical.Active(); ok {
			return current, active.Engine, nil
		}
	}
	engine := proto.AssistantExecutionIdentity(sessionEngineAgent(row), row.SourceKind, row.SourceInstanceID, row.Launcher)
	return current, engine, nil
}

func (s *Service) larkAgentExecutionIdentity(ctx context.Context, machine, launcher string) (string, error) {
	_, segments := s.store().(logicalSwitchStore)
	if segments && s.segmentsEnabled(ctx) {
		target, err := s.logicalTarget(ctx, machine, launcher)
		if err != nil {
			return "", err
		}
		if target.Registered && target.Prepare {
			return target.Engine, nil
		}
		return "", fmt.Errorf("launcher %q cannot run an existing Lark /agent conversation on %s", launcher, machine)
	}
	// A manager before logical segments can still follow a model or effort
	// change on the same agent, but must not silently resume a foreign engine.
	return assistantKind(s.launcherAgent(ctx, machine, launcher)), nil
}

func (s *Service) larkAgentStartSwitch(ctx context.Context, machine, sid string, target conversationExecution) error {
	st, ok := s.store().(logicalSwitchStore)
	if !ok || !s.segmentsEnabled(ctx) {
		return errors.New("this hub cannot move an existing Lark /agent conversation to another runtime; update the manager")
	}
	book := &s.conversationSwitches
	book.mu.Lock()
	defer book.mu.Unlock()
	state, _, err := s.loadConversationSwitches(ctx)
	if err != nil {
		return err
	}
	if s.runConversationSwitchesLocked(ctx, &state) {
		if err := s.saveConversationSwitches(ctx, state); err != nil {
			return err
		}
	}
	defer book.settle(state)
	for _, batch := range state.Batches {
		for _, item := range batch.Items {
			if item.Machine == machine && item.ConversationID == sid &&
				(item.Outcome == conversationSwitchQueued || item.Outcome == conversationSwitchReady || item.Outcome == conversationSwitchSwitching) &&
				sameConversationExecution(item.Target, target) {
				return nil
			}
		}
	}
	item := s.planConversationSwitchItem(ctx, state, st, machine, sid, target, nil, s.conversationSwitchResolver(machine))
	if item.Outcome == conversationSwitchRefused {
		return fmt.Errorf("cannot move the existing Lark /agent conversation to %s: %s", target.Launcher, item.Reason)
	}
	item.OperationID, item.Outcome = larkAgentSwitchPrefix+randomHex(16), conversationSwitchQueued
	state.Batches = append(state.Batches, conversationSwitchBatch{ID: randomHex(16), Items: []conversationSwitchItem{item}, FirstTurn: true})
	if err := s.saveConversationSwitches(ctx, state); err != nil {
		return err
	}
	s.runConversationSwitchesLocked(ctx, &state)
	if err := s.saveConversationSwitches(ctx, state); err != nil {
		return err
	}
	s.logicalChanged()
	s.historyWake()
	return nil
}
