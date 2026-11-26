package mcpserver

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

const workSearchName = "work_tools_search"

var workSearchTool = map[string]any{
	"name":        workSearchName,
	"description": "Find Persona work tools by name or name fragment. Use the runtime's tool search to open a matching tool if it is deferred.",
	"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]string{"type": "string"}}, "required": []string{"query"}},
	"_meta":       map[string]bool{"anthropic/alwaysLoad": true},
}

// searchWorkTools keeps the old discovery entry point for resumed chats. Lookup
// uses the currently permitted list and leaves the advertised catalog alone.
// A nil result leaves the name to the ordinary tool handler.
func searchWorkTools(ctx context.Context, args json.RawMessage) map[string]any {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	snapshot, err := listTools(ctx)
	if err != nil {
		return toolResult(proto.TextResult(err.Error(), true))
	}
	if len(snapshot.WorkGuide) == 0 {
		return nil
	}
	var input struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(args, &input); err != nil || strings.TrimSpace(input.Query) == "" {
		return toolResult(proto.TextResult("Supply a tool name or name fragment.", true))
	}
	terms := strings.Fields(strings.ToLower(input.Query))
	var matches []string
	for name := range snapshot.WorkGuide {
		for _, term := range terms {
			if strings.Contains(name, term) {
				matches = append(matches, name)
				break
			}
		}
	}
	if len(matches) == 0 {
		return toolResult(proto.TextResult("No matching work tool in this conversation.", false))
	}
	slices.Sort(matches)
	return toolResult(proto.TextResult("Matches: "+strings.Join(matches, ", ")+". Use the runtime's tool search for deferred tools, then call the original tool.", false))
}
