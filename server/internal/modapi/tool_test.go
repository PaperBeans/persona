package modapi

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/server/internal/wireids"
)

type echoArgs struct {
	Text  string `json:"text"`
	Times int    `json:"times,omitempty"`
}

func echoTool(calls *int) MCPTool {
	return Tool("echo", "Echo", ToolSchema(`{"type":"object"}`), func(_ context.Context, call ToolCall, a echoArgs) (ToolResult, error) {
		*calls++
		return ToolText(call.Machine.String() + "/" + call.Session.String() + ":" + a.Text), nil
	})
}

func TestToolDecodesArgumentsOnceAndStrictly(t *testing.T) {
	cases := []struct{ name, args, out, err string }{
		{"declared fields", `{"text":"hi","times":2}`, "mac/s1:hi", ""},
		{"absent arguments are an empty object", ``, "mac/s1:", ""},
		{"whitespace only", " \n", "mac/s1:", ""},
		{"unknown field", `{"text":"hi","extra":true}`, "", `invalid arguments: json: unknown field "extra"`},
		{"wrong type", `{"text":7}`, "", "invalid arguments: json: cannot unmarshal number into Go struct field echoArgs.text of type string"},
		{"not an object", `["hi"]`, "", "invalid arguments: json: cannot unmarshal array into Go value of type modapi.echoArgs"},
		{"data after the object", `{"text":"hi"} {"text":"again"}`, "", "invalid arguments: unexpected data after the arguments object"},
		{"malformed", `{"text":`, "", "invalid arguments: unexpected EOF"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			calls := 0
			res, err := echoTool(&calls).Run(context.Background(), ToolCall{Machine: wireids.MachineID("mac"), Session: wireids.SessionID("s1"), Args: ToolArgs(c.args)})
			said := ""
			if err != nil {
				said = err.Error()
			}
			if res.Text != c.out || said != c.err {
				t.Fatalf("Run(%s) = %q, error %q; want %q, error %q", c.args, res.Text, said, c.out, c.err)
			}
			if want := map[bool]int{true: 1, false: 0}[c.err == ""]; calls != want {
				t.Fatalf("handler ran %d time(s), want %d", calls, want)
			}
		})
	}
}

func TestToolWithoutAHandlerHasNoRun(t *testing.T) {
	if tool := Tool[echoArgs]("echo", "Echo", nil, nil); tool.Run != nil || tool.Name != "echo" {
		t.Fatalf("a nil handler must leave Run nil for Host.AddMCPTool to refuse: %+v", tool)
	}
}

// The named opaque types are JSON, not bytes: embedded in a struct they are written as they are.
func TestToolSchemaAndArgsMarshalAsJSON(t *testing.T) {
	got, err := json.Marshal(struct {
		Schema ToolSchema `json:"schema"`
		Args   ToolArgs   `json:"args"`
		None   ToolSchema `json:"none"`
	}{Schema: ToolSchema(`{"type":"object"}`), Args: ToolArgs(`{"x":1}`)})
	if want := `{"schema":{"type":"object"},"args":{"x":1},"none":null}`; err != nil || string(got) != want {
		t.Fatalf("marshal = %s, %v; want %s", got, err, want)
	}
}
