package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantskills"
	"github.com/nwverse/persona/server/internal/proto"
)

// Exercise the same list, discovery and original call handlers that a native MCP client uses.
func TestWorkGuideCatalogStableAcrossSearchAndReconnect(t *testing.T) {
	t.Setenv("PERSONA_ASSISTANT", "1")
	t.Setenv("PERSONA_SESSION_ID", "assistant-test")
	t.Chdir(t.TempDir())
	tools := []proto.Tool{
		{Name: "todo_get", Description: "get item", InputSchema: json.RawMessage(`{"type":"object","required":["id"]}`)},
		{Name: "todo_list", Description: "list items", DeferLoading: true},
		{Name: "other", Description: "unrelated"},
	}
	var call map[string]any
	saved := dialNode
	t.Cleanup(func() { dialNode = saved })
	dialNode = func(_ context.Context, req any) ([]byte, error) {
		r := req.(map[string]any)
		if r["kind"] == "mcp.tools" {
			return json.Marshal(map[string]any{"tools": tools})
		}
		call = r
		if r["name"] == "todo_get" && slices.ContainsFunc(tools, func(tool proto.Tool) bool { return tool.Name == "todo_get" }) {
			return json.Marshal(proto.TextResult("original handler returned the item", false))
		}
		return json.Marshal(proto.TextResult("original handler declined", true))
	}

	connect := func() (func(string, string) json.RawMessage, *syncBuf, func()) {
		in, input := io.Pipe()
		output := &syncBuf{}
		done := make(chan error, 1)
		go func() { done <- Serve(t.Context(), "test", in, output) }()
		closeClient := sync.OnceFunc(func() {
			_ = input.Close()
			if err := <-done; err != nil {
				t.Error(err)
			}
		})
		t.Cleanup(closeClient)
		requestID := 0
		return func(method, params string) json.RawMessage {
			t.Helper()
			requestID++
			id := strconv.Itoa(requestID)
			req := request{JSONRPC: "2.0", ID: json.RawMessage(id), Method: method, Params: json.RawMessage(params)}
			if err := json.NewEncoder(input).Encode(req); err != nil {
				t.Fatal(err)
			}
			var reply struct {
				ID     json.RawMessage `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *rpcError       `json:"error"`
			}
			waitFor(t, method+" reply", func() bool {
				for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
					if json.Unmarshal([]byte(line), &reply) == nil && string(reply.ID) == id {
						return true
					}
					reply.ID = nil
				}
				return false
			})
			if reply.Error != nil {
				t.Fatal(reply.Error)
			}
			return reply.Result
		}, output, closeClient
	}
	list := func(exchange func(string, string) json.RawMessage) []map[string]any {
		t.Helper()
		var result struct {
			Tools []map[string]any `json:"tools"`
		}
		if err := json.Unmarshal(exchange("tools/list", ""), &result); err != nil {
			t.Fatal(err)
		}
		return result.Tools
	}
	exchange, output, disconnect := connect()
	invoke := func(name, args string) string {
		t.Helper()
		return string(exchange("tools/call", `{"name":"`+name+`","arguments":`+args+`}`))
	}
	baseline := list(exchange)
	tools[0].WorkGuideTool, tools[1].WorkGuideTool = true, true
	if got := list(exchange); !reflect.DeepEqual(got, baseline) {
		t.Fatal("missing managed guide must retain the full catalog", got)
	}
	for _, dir := range []string{".claude", ".agents"} {
		if err := assistantskills.Sync(filepath.Join(dir, "skills"), t.TempDir(), assistantskills.Selection{WorkGuide: true}, t.Logf); err != nil {
			t.Fatal(err)
		}
	}
	initial := list(exchange)
	want := []map[string]any{baseline[2], baseline[0], baseline[1]}
	if len(initial) != len(want)+1 || !reflect.DeepEqual(initial[:len(want)], want) || initial[len(want)]["name"] != workSearchName {
		t.Fatalf("initial catalog should contain the original tools with their original schemas and loading metadata: %v", initial)
	}
	for _, query := range []string{"todo_", "todo_get", "todo_get", "todo_list"} {
		if out := invoke(workSearchName, `{"query":"`+query+`"}`); !strings.Contains(out, "Matches: todo_") || call != nil {
			t.Fatalf("search %q: %s, call %v", query, out, call)
		}
		if got := list(exchange); !reflect.DeepEqual(got, initial) {
			t.Fatalf("search %q changed catalog: %v", query, got)
		}
	}
	if strings.Contains(output.String(), "notifications/tools/list_changed") {
		t.Fatal("search notified the client of a catalog change")
	}
	disconnect()
	slices.Reverse(tools) // The manager can return the same definitions in a different order.
	exchange, _, _ = connect()
	if got := list(exchange); !reflect.DeepEqual(got, initial) {
		t.Fatalf("reconnect changed catalog: %v", got)
	}
	if out := invoke("todo_get", `{"id":"synthetic"}`); !strings.Contains(out, "original handler returned the item") {
		t.Fatalf("original tool unavailable: %s", out)
	}
	if call["kind"] != "mcp.call" || call["name"] != "todo_get" || call["session_id"] != "assistant-test" ||
		string(call["args"].(json.RawMessage)) != `{"id":"synthetic"}` {
		t.Fatalf("original call changed: %v", call)
	}
	allowed := tools
	tools = tools[:2] // The manager withdraws todo_get.
	if out := invoke(workSearchName, `{"query":"todo_get"}`); !strings.Contains(out, "No matching work tool") {
		t.Fatalf("search found a withdrawn tool: %s", out)
	}
	if out := invoke("todo_get", `{"id":"synthetic"}`); !strings.Contains(out, "original handler declined") || !strings.Contains(out, `"isError":true`) {
		t.Fatalf("original decision lost: %s", out)
	}
	tools = allowed
	slices.Reverse(tools)
	tools[0].WorkGuideTool, tools[1].WorkGuideTool = false, false
	if got := list(exchange); !reflect.DeepEqual(got, baseline) {
		t.Fatal("rollback changed original schema or loading metadata", got)
	}
	tools[0].WorkGuideTool, tools[1].WorkGuideTool = true, true
	if got := list(exchange); !reflect.DeepEqual(got, initial) {
		t.Fatal("re-enabled with a different catalog", got)
	}
	tools = append(tools, proto.Tool{Name: workSearchName, Description: "extension"})
	if got := list(exchange); len(got) != len(tools) || got[len(got)-1]["description"] != "extension" {
		t.Fatal("extension name collision", got)
	}
	invoke(workSearchName, `{}`)
	if call["name"] != workSearchName {
		t.Fatal("extension tool was intercepted", call)
	}
}
