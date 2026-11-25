package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nwverse/persona/server/internal/modapi"
)

// What the model sees of a module's tool does not depend on how the module contract types the slot (#15397, P8): the
// definition, the output and the failure below are the bytes origin/main produced at 63b0634f7 for this same tool
// registered through the old AddMCPTool(name, description, schema, run(ctx, machine, session, args)), captured by
// running it there. A change to any of them changes what a model is shown, and is not a typing change.
func TestModuleToolWireIsPinned(t *testing.T) {
	const (
		name        = "module_pin"
		description = "Pin what the model sees of a module tool."
		schema      = `{"type":"object","properties":{"text":{"type":"string","description":"What to say back"}},"required":["text"]}`
		definition  = `{"name":"module_pin","description":"Pin what the model sees of a module tool.","input_schema":` + schema + `}`
	)
	type pinArgs struct {
		Text string `json:"text"`
	}
	say := func(call modapi.ToolCall, a pinArgs) (modapi.ToolResult, error) {
		if a.Text == "" {
			return modapi.ToolResult{}, errors.New("text is required")
		}
		return modapi.ToolText("pin " + call.Machine.String() + "/" + call.Session.String() + ": " + a.Text + " <" + string(call.Args) + ">"), nil
	}
	// raw reads its arguments itself, as every handler did before; typed lets modapi.Tool decode them.
	raw := modapi.MCPTool{Name: name, Description: description, InputSchema: modapi.ToolSchema(schema),
		Run: func(_ context.Context, call modapi.ToolCall) (modapi.ToolResult, error) {
			var a pinArgs
			if err := json.Unmarshal(call.Args, &a); err != nil {
				return modapi.ToolResult{}, err
			}
			return say(call, a)
		}}
	typed := modapi.Tool(name, description, modapi.ToolSchema(schema),
		func(_ context.Context, call modapi.ToolCall, a pinArgs) (modapi.ToolResult, error) {
			return say(call, a)
		})

	cases := []struct {
		args, out, err, wire string
		rawOnly              bool // the old handler ignored an undeclared argument; a typed tool refuses it (below)
	}{
		{args: `{"text":"hi"}`, out: `pin server/main: hi <{"text":"hi"}>`,
			wire: `{"content":[{"type":"text","text":"pin server/main: hi \u003c{\"text\":\"hi\"}\u003e"}],"is_error":false}`},
		{args: `{"text": "hi" , "n":1}`, out: `pin server/main: hi <{"text": "hi" , "n":1}>`, rawOnly: true,
			wire: `{"content":[{"type":"text","text":"pin server/main: hi \u003c{\"text\": \"hi\" , \"n\":1}\u003e"}],"is_error":false}`},
		{args: `{}`, err: "text is required", wire: `{"content":[{"type":"text","text":"text is required"}],"is_error":true}`},
	}
	for label, tool := range map[string]modapi.MCPTool{"raw": raw, "typed": typed} {
		t.Run(label, func(t *testing.T) {
			m := testSlotModule{info: moduleTestInfo("tool-pin"), register: func(h modapi.Host) error { return h.AddMCPTool(tool) }}
			m.info.MCPTools = []string{name}
			s := New(Options{Hub: newFakeHub().withMachine(), Store: func() Store { return &memStore{} }, Logf: testLogf(t), Modules: []modapi.Module{m}})
			ctx := context.Background()
			var listed []byte
			for _, def := range s.Tools(ctx) {
				if def.Name == name {
					listed, _ = json.Marshal(def)
				}
			}
			if string(listed) != definition {
				t.Fatalf("definition sent to the model:\n got %s\nwant %s", listed, definition)
			}
			for _, c := range cases {
				if c.rawOnly && label == "typed" {
					continue
				}
				out, err := s.runTool(ctx, "server", "main", name, json.RawMessage(c.args))
				if said := pinErr(err); out != c.out || said != c.err {
					t.Errorf("runTool(%s) = %q, error %q; want %q, error %q", c.args, out, said, c.out, c.err)
				}
				res, _ := json.Marshal(s.Tool(ctx, ToolCall{Name: name, MachineID: "server", SessionID: "main", Arguments: json.RawMessage(c.args)}))
				if string(res) != c.wire {
					t.Errorf("Tool(%s) result sent to the model:\n got %s\nwant %s", c.args, res, c.wire)
				}
			}
			// A call with no arguments reaches the handler as "{}" through Tool, and as sent through runTool.
			res, _ := json.Marshal(s.Tool(ctx, ToolCall{Name: name, MachineID: "server", SessionID: "main"}))
			if want := `{"content":[{"type":"text","text":"text is required"}],"is_error":true}`; string(res) != want {
				t.Errorf("Tool() with no arguments:\n got %s\nwant %s", res, want)
			}
		})
	}

	// The one difference a typed tool chooses: an argument its struct does not declare is refused, not ignored.
	m := testSlotModule{info: moduleTestInfo("tool-pin"), register: func(h modapi.Host) error { return h.AddMCPTool(typed) }}
	m.info.MCPTools = []string{name}
	s := New(Options{Hub: newFakeHub().withMachine(), Store: func() Store { return &memStore{} }, Logf: testLogf(t), Modules: []modapi.Module{m}})
	res, _ := json.Marshal(s.Tool(context.Background(), ToolCall{Name: name, MachineID: "server", SessionID: "main", Arguments: json.RawMessage(`{"text":"hi","n":1}`)}))
	if want := `{"content":[{"type":"text","text":"invalid arguments: json: unknown field \"n\""}],"is_error":true}`; string(res) != want {
		t.Errorf("typed tool with an undeclared argument:\n got %s\nwant %s", res, want)
	}
}

func pinErr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
