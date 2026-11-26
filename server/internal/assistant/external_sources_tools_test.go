package assistant

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/mcptoolgolden"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestGoldenExternalSourceTools(t *testing.T) {
	f := newFakeHub().withMachine()
	f.hello.Locale = "en"
	s := newTestService(t, f)
	setSettings(t, s, func(cfg *Settings) { cfg.Main = "main" })
	mcptoolgolden.CheckList(t, "testdata/prompts/external_sources", "main", s.externalSourceTools(t.Context(), "server", "main"))
}

func TestExternalSourceToolsRequireMainConversationAndOwnerApproval(t *testing.T) {
	s, store := externalSourceHTTPFixture(t)
	ctx := t.Context()
	setSettings(t, s, func(cfg *Settings) {
		cfg.Main = "main"
		cfg.Leaders = []string{"dir_source"}
	})
	const read, write = "external_sources_read", "external_sources_write"
	for _, name := range []string{read, write} {
		if !toolListed(s.sessionMCPTools(ctx, "server", "main", true, false), name) ||
			!toolListed(s.ToolsFor(ctx, "server", "main"), name) ||
			toolListed(s.sessionMCPTools(ctx, "server", "dir_source", true, false), name) ||
			toolListed(s.ToolsFor(ctx, "server", "dir_source"), name) || toolListed(s.AgentTools(), name) {
			t.Fatalf("incorrect discovery scope for %s", name)
		}
	}
	const inspect = `{"action":"inspect","external_ref":{"provider":"lark"}}`
	const escalation = `{"action":"set_policy","external_ref":{"provider":"lark"},"policy":{"new_item_policy":"auto_todo","mode":"realtime"}}`
	call := func(machine, session, tool, args string, assistant bool) proto.ToolResult {
		return s.mcpCall(ctx, machine, session, tool, json.RawMessage(args), assistant)
	}
	for _, target := range []struct {
		machine, session string
		assistant        bool
	}{{"server", "dir_source", true}, {"server", "side", true}, {"other", "main", true}, {"server", "main", false}} {
		if got := call(target.machine, target.session, write, escalation, target.assistant); !got.IsError {
			t.Fatalf("unauthorized caller %+v: %s", target, toolText(got))
		}
	}
	if got := call("server", "main", read, inspect, true); got.IsError || !strings.Contains(toolText(got), `"new_item_policy":"ask_owner"`) {
		t.Fatalf("frictionless read of defaults: %s", toolText(got))
	}
	if len(s.pendingQuestions()) != 0 {
		t.Fatal("denied calls and reads must leave no approval pending")
	}
	readMode := func(want bool) {
		t.Helper()
		rows, _, err := store.ExternalSourcePolicies(ctx, []assistantwire.ExternalRef{{Provider: "lark"}}, "")
		if err != nil || (len(rows) != 0) != want {
			t.Fatalf("stored policy: %+v, %v, want present=%t", rows, err, want)
		}
		if want && (rows[0].Policy.NewItemPolicy == nil || *rows[0].Policy.NewItemPolicy != "auto_todo") {
			t.Fatalf("approved policy: %+v", rows[0])
		}
	}
	for _, answer := range []proto.Resolution{
		{Allow: new(true), AnsweredBy: proto.AnsweredByAssistant}, {Allow: new(false)}, {Allow: new(true)},
	} {
		result := make(chan proto.ToolResult, 1)
		go func() { result <- call("server", "main", write, escalation, true) }()
		card, ok := awaitCard(s)
		if !ok || card.Kind != proto.IxPermission || card.ToolName != write {
			t.Fatalf("missing owner approval: %+v", card)
		}
		readMode(false)
		if !strings.Contains(string(card.ToolInput), "auto_todo") || !strings.Contains(card.Summary, "Lark") {
			t.Fatalf("card omits the proposed change: %q %s", card.Summary, card.ToolInput)
		}
		raw, err := json.Marshal(answer)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Action(ctx, Action{Op: "answer", InteractionID: card.ID, Answer: raw}); err != nil {
			t.Fatal(err)
		}
		<-result
		readMode(answer.UserAllowed())
	}
}

func TestExternalSourceToolsManageBindingsAndCompareApprovedState(t *testing.T) {
	s, store := externalSourceHTTPFixture(t)
	ctx := t.Context()
	setSettings(t, s, func(cfg *Settings) { cfg.Main = "main" })
	call := func(name, args string) proto.ToolResult {
		return s.mcpCall(ctx, "server", "main", name, json.RawMessage(args), true)
	}
	const scope = `"external_ref":{"provider":"feishu","container":{"kind":"chat","id":"oc_example"}}`
	const target = `"target_kind":"direction","target_id":"dir_source"`
	approve := func(args string, beforeAnswer func(proto.Interaction)) string {
		t.Helper()
		result := make(chan proto.ToolResult, 1)
		go func() { result <- call(externalSourcesWriteTool, args) }()
		card, ok := awaitCard(s)
		if !ok {
			t.Fatal("expected an owner card")
		}
		if beforeAnswer != nil {
			beforeAnswer(card)
		}
		if _, err := s.Action(ctx, Action{Op: "answer", InteractionID: card.ID, Answer: json.RawMessage(`{"allow":true}`)}); err != nil {
			t.Fatal(err)
		}
		got := <-result
		if got.IsError {
			t.Fatal(toolText(got))
		}
		return toolText(got)
	}
	var after struct {
		Saved assistantwire.ExternalBinding
	}
	text := approve(`{"action":"create_binding",`+scope+","+target+`,"policy":{"mode":"digest","new_item_policy":"auto_todo"}}`,
		func(card proto.Interaction) {
			if !strings.Contains(string(card.ToolInput), "auto_todo") {
				t.Fatalf("card does not show automatic creation: %s", card.ToolInput)
			}
			bindings, err := store.ListExternalBindings(ctx, "", "", "", nil)
			if err != nil || len(bindings) != 0 {
				t.Fatalf("binding written before approval: %+v, %v", bindings, err)
			}
		})
	if err := json.Unmarshal([]byte(text), &after); err != nil || after.Saved.ID == "" || after.Saved.ExternalRef.Provider != "lark" {
		t.Fatalf("create result: %s, %v", text, err)
	}
	binding := after.Saved
	id := fmt.Sprintf(`"binding_id":%q`, binding.ID)
	list := call(externalSourcesReadTool, `{"action":"list",`+target+`}`)
	if list.IsError || !strings.Contains(toolText(list), string(binding.ID)) {
		t.Fatalf("target list: %s", toolText(list))
	}
	inspect := `{"action":"inspect",` + scope + "," + id + `}`
	if got := call(externalSourcesReadTool, inspect); got.IsError ||
		!strings.Contains(toolText(got), `"source":"binding"`) || !strings.Contains(toolText(got), `"new_item_policy":"auto_todo"`) {
		t.Fatalf("inspect result: %s", toolText(got))
	}
	update := `{"action":"update_binding",` + id + "," + scope + "," + target + `,"policy":{"mode":"realtime","new_item_policy":"ask_owner"}}`
	text = approve(update, func(_ proto.Interaction) {
		edit := binding.ExternalBindingInput
		edit.Policy.Mode = new(assistantwire.ExternalForwardMode("off"))
		if _, err := store.UpdateExternalBinding(ctx, binding.ID, edit); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(text, "fresh approval card is required") || !strings.Contains(toolText(call(externalSourcesReadTool, inspect)), `"mode":"off"`) {
		t.Fatalf("stale update overwrote the owner's edit: %s", text)
	}
	if text := approve(update, nil); !strings.Contains(text, `"mode":"realtime"`) {
		t.Fatalf("update result: %s", text)
	}
	if text := approve(`{"action":"delete_binding",`+id+`}`, nil); !strings.Contains(text, `"saved":null`) {
		t.Fatalf("remove result: %s", text)
	}
	if got := call(externalSourcesReadTool, inspect); !got.IsError {
		t.Fatalf("deleted binding still resolves: %s", toolText(got))
	}

	const set = `{"action":"set_policy",` + scope + `,"policy":{"mode":"realtime"}}`
	const reset = `{"action":"set_policy",` + scope + `,"policy":{}}`
	text = approve(set, func(_ proto.Interaction) {
		if err := store.PutExternalSourcePolicy(ctx, assistantwire.ExternalScopedPolicy{ExternalRef: binding.ExternalRef,
			Policy: assistantwire.ExternalPolicy{Mode: new(assistantwire.ExternalForwardMode("on_demand"))}}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(text, "fresh approval card is required") {
		t.Fatalf("stale scoped policy overwrote an existing setting: %s", text)
	}
	if text := approve(reset, nil); !strings.Contains(text, `"mode":"on_demand"`) {
		t.Fatalf("reset did not return the previous override: %s", text)
	}
	if got := call(externalSourcesReadTool, `{"action":"inspect",`+scope+`}`); got.IsError ||
		!strings.Contains(toolText(got), `"overrides":[]`) || !strings.Contains(toolText(got), `"new_item_policy":"ask_owner"`) {
		t.Fatalf("reset did not restore inheritance: %s", toolText(got))
	}
}
