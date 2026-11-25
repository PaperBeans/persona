package modapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/nwverse/persona/server/internal/wireids"
)

// ToolSchema is a tool's input schema: the JSON Schema object the model is shown as the tool's input_schema, byte for
// byte as the module wrote it. The core never reads it; it only hands it to the model.
type ToolSchema json.RawMessage

// MarshalJSON writes the schema as the JSON it is (a named byte slice would otherwise be written as base64).
func (s ToolSchema) MarshalJSON() ([]byte, error) { return json.RawMessage(s).MarshalJSON() }

// ToolArgs is one call's arguments: the JSON object the model sent for the tool, undecoded. Only the tool knows its
// shape, so the core carries the bytes to the tool's decoder (Tool decodes them once, strictly) and never reads them.
type ToolArgs json.RawMessage

// MarshalJSON writes the arguments as the JSON they are.
func (a ToolArgs) MarshalJSON() ([]byte, error) { return json.RawMessage(a).MarshalJSON() }

// ToolCall is one call of a module's tool: who called, and with what.
type ToolCall struct {
	// Machine is the computer the calling session runs on.
	Machine wireids.MachineID
	// Session is the calling Agent session on Machine: an assistant conversation's session, or another Agent's that
	// was offered the tool. Empty when the core itself calls the tool with no session behind it.
	Session wireids.SessionID
	// Args are the model's arguments, as sent.
	Args ToolArgs
}

// ToolResult is what a tool gives back to the model. A failure is the handler's error, not a result.
type ToolResult struct {
	// Text is the tool's output, sent to the model as it is.
	Text string
}

// ToolText is the result that says text.
func ToolText(text string) ToolResult { return ToolResult{Text: text} }

// MCPTool is a module's assistant tool: its definition as the model sees it, and its handler. Tool builds one whose
// arguments are decoded into a struct; a tool that must read the raw arguments itself fills Run.
type MCPTool struct {
	Name        string
	Description string
	InputSchema ToolSchema
	Run         func(context.Context, ToolCall) (ToolResult, error)
}

// Tool is an MCPTool whose arguments are decoded into A before run is called: once, strictly. An unknown field, a
// value of the wrong type or anything after the object fails the call with "invalid arguments: …" and run is not
// called; absent arguments are an empty object. A is the tool's argument struct (json tags name the schema's
// properties), never a map. A nil run gives a tool with no handler, which Host.AddMCPTool refuses.
func Tool[A any](name, description string, schema ToolSchema, run func(context.Context, ToolCall, A) (ToolResult, error)) MCPTool {
	t := MCPTool{Name: name, Description: description, InputSchema: schema}
	if run == nil {
		return t
	}
	t.Run = func(ctx context.Context, call ToolCall) (ToolResult, error) {
		var args A
		if err := decodeToolArgs(call.Args, &args); err != nil {
			return ToolResult{}, fmt.Errorf("invalid arguments: %w", err)
		}
		return run(ctx, call, args)
	}
	return t
}

func decodeToolArgs(raw ToolArgs, into any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = ToolArgs(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("unexpected data after the arguments object")
	}
	return nil
}
