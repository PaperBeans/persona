package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestLarkAgentExistingConversationFollowsModelChanges(t *testing.T) {
	s, f, _ := modelService(t)
	ctx := context.Background()
	putLaunchers(t, s,
		storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "lark-custom", Model: "selected"}},
		storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "other-custom", Agent: "claude", Model: "other"}})
	f.setSessions("server", proto.Session{SessionID: "lark-chat", MachineID: "server", Assistant: true, Launcher: "lark-custom", Agent: "claude", Model: "selected", Effort: "high"})
	s.originSet(ctx, "server", "lark-chat", OriginLarkAgent, "lark-extension", "lark-extension")
	setModel(t, s, purposeLarkAgent, ModelBinding{Provider: "launcher.lark-custom", Model: "selected", Effort: "high"})
	params := map[string]any{}
	if err := s.sendModel(ctx, "server", "lark-chat", params, s.lang(ctx)); err != nil {
		t.Fatal(err)
	}
	if params["launcher"] != "lark-custom" || params["model"] != "selected" || params["effort"] != "high" {
		t.Fatalf("initial inherited binding: %v", params)
	}
	setModel(t, s, purposeLarkAgent, ModelBinding{Provider: "launcher.lark-custom", Model: "selected-new", Effort: "low"})
	params = map[string]any{}
	if err := s.sendModel(ctx, "server", "lark-chat", params, s.lang(ctx)); err != nil {
		t.Fatal(err)
	}
	if params["launcher"] != "lark-custom" || params["model"] != "selected-new" || params["effort"] != "low" {
		t.Fatalf("same-launcher model or effort stayed stale: %v", params)
	}
	setModel(t, s, purposeLarkAgent, ModelBinding{Provider: "launcher.other-custom", Model: "changed", Effort: "medium"})
	params = map[string]any{}
	if err := s.sendModel(ctx, "server", "lark-chat", params, s.lang(ctx)); err != nil {
		t.Fatal(err)
	}
	if params["launcher"] != "other-custom" || params["model"] != "changed" || params["effort"] != "medium" {
		t.Fatalf("existing Lark /agent kept its old model: %v", params)
	}
	// An explicit per-message choice keeps its established precedence.
	params = map[string]any{"launcher": "lark-custom", "model": "one-message", "effort": "low"}
	if err := s.sendModel(ctx, "server", "lark-chat", params, s.lang(ctx)); err != nil {
		t.Fatal(err)
	}
	if params["launcher"] != "lark-custom" || params["model"] != "one-message" || params["effort"] != "low" {
		t.Fatalf("explicit message choice was overwritten: %v", params)
	}
}

func TestLarkAgentExistingBusyConversationQueuesOneSwitch(t *testing.T) {
	r := newConversationSwitchTestRig(t)
	ctx := context.Background()
	sid := "busy-chat"
	r.s.originSet(ctx, "server", sid, OriginLarkAgent, "lark-extension", "lark-extension")
	setModel(t, r.s, purposeLarkAgent, ModelBinding{Provider: "launcher.codex", Model: "new-model", Effort: "high"})
	for i := 0; i < 2; i++ {
		if err := r.s.larkAgentStartSwitch(ctx, "server", sid, conversationExecution{Machine: "server", Launcher: "codex", Model: "new-model", Effort: "high"}); err != nil {
			t.Fatal(err)
		}
	}
	state, _, err := r.s.loadConversationSwitches(ctx)
	if err != nil || len(state.Batches) != 1 || len(state.Batches[0].Items) != 1 {
		t.Fatalf("duplicate automatic switches: %+v %v", state, err)
	}
	item := state.Batches[0].Items[0]
	if item.Outcome != conversationSwitchQueued || item.ConversationID != sid || len(conversationSwitchTestPreparations(r)) != 0 {
		t.Fatalf("busy turn was interrupted or switch was not queued: %+v", item)
	}
}

func TestLarkAgentExistingConversationSwitchesExecutionBeforeSending(t *testing.T) {
	r := newConversationSwitchTestRig(t)
	ctx := context.Background()
	sid := "fresh-chat"
	r.s.originSet(ctx, "server", sid, OriginLarkAgent, "lark-extension", "lark-extension")
	setModel(t, r.s, purposeLarkAgent, ModelBinding{Provider: "launcher.codex", Model: "new-model", Effort: "high"})
	sends := &limitSends{}
	r.f.answerNode(DeviceSessionSend, func(_ string, raw json.RawMessage) (any, error) {
		var p map[string]any
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		sends.mu.Lock()
		sends.sent = append(sends.sent, p)
		sends.mu.Unlock()
		return map[string]any{"ok": true, "session_id": p["session_id"]}, nil
	})
	params, err := r.s.sendParams(ctx, SendParams{MachineID: "server", SessionID: sid, Params: map[string]any{
		"text": "continue in the existing topic", "client_msg_id": "lark-next-input",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if params["client_msg_id"] != "lark-next-input" {
		t.Fatalf("original input identity changed while preparing the switch: %v", params)
	}
	params["session_id"] = sid
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	_, handled, err := r.s.logicalCall(ctx, "server", DeviceSessionSend, raw)
	if err != nil || !handled {
		t.Fatalf("logical input was not admitted: handled=%v err=%v", handled, err)
	}
	if got := sends.to(sid); len(got) != 0 {
		t.Fatalf("the input ran on the old launcher before its switch: %v", got)
	}
	logical, err := r.d.LogicalConversation(ctx, "server", sid)
	if err != nil || logical == nil || logical.Switch == nil {
		t.Fatalf("switch not durable: %+v %v", logical, err)
	}
	operation := logical.Switch.OperationID
	if !strings.HasPrefix(operation, "lark-model:") {
		t.Fatalf("not the automatic Lark model switch: %q", operation)
	}
	pending, err := r.d.ConversationInputPending(ctx, "server", sid)
	if err != nil || !pending {
		t.Fatalf("the original input was not queued behind its switch: %v %v", pending, err)
	}
	// The ordinary switch worker, not a synthetic continuation, delivers the
	// original request with the saved context to the new execution segment.
	waitFor(t, "Lark model switch to deliver its original input", func() bool {
		r.s.wakeConversationSwitches()
		r.s.logicalSweep(ctx)
		return len(sends.to("prepared-chat-1")) == 1
	})
	first := sends.to("prepared-chat-1")
	if len(first) != 1 {
		logical, logicalErr := r.d.LogicalConversation(ctx, "server", sid)
		state, _, stateErr := r.s.loadConversationSwitches(ctx)
		input, inputErr := r.d.ConversationUnresolvedInput(ctx, "server", sid)
		t.Fatalf("new target did not receive exactly one original input: %v logical=%+v logicalErr=%v state=%+v stateErr=%v input=%+v inputErr=%v", sends.sent, logical, logicalErr, state, stateErr, input, inputErr)
	}
	text, _ := first[0]["text"].(string)
	if first[0]["client_msg_id"] != "lark-next-input" || first[0]["model"] != "new-model" || first[0]["effort"] != "high" || !strings.Contains(text, "continue in the existing topic") || !strings.Contains(text, "<persona-context") {
		t.Fatalf("original input or context handoff missing: %v", first[0])
	}
	waitFor(t, "Lark model switch to accept its delivered input", func() bool {
		r.s.wakeConversationSwitches()
		r.s.logicalSweep(ctx)
		active, err := r.d.LogicalConversation(ctx, "server", sid)
		return err == nil && active != nil && active.Switch == nil && active.Epoch == 2
	})
	active, err := r.d.LogicalConversation(ctx, "server", sid)
	if err != nil || active.Switch != nil || active.Epoch != 2 {
		t.Fatalf("switch not accepted after its first input: %+v %v", active, err)
	}
}
