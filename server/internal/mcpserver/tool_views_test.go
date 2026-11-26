package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/proto"
)

// Super Rule 2: out-of-band tool_views are never sent to MCP model runtimes.
func TestToolResultDropsToolViews(t *testing.T) {
	lat, lon := 39.984, 116.318
	res := proto.ToolResult{
		Content: []proto.Content{{Type: "text", Text: "Route: 28 min"}},
		ToolViews: []wire.View{{
			Title: "Route",
			Sections: []wire.Section{{
				Items: []wire.Component{{
					Type:    wire.ComponentMap,
					Alt:     "Route",
					Markers: []wire.MapMarker{{ID: "m1", Lat: &lat, Lon: &lon, Label: "Home"}},
				}},
			}},
		}},
	}
	b, _ := json.Marshal(toolResult(res))
	want := `{"content":[{"text":"Route: 28 min","type":"text"}],"isError":false}`
	if string(b) != want || strings.Contains(string(b), "tool_views") {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
}
