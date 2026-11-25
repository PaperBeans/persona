package proc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/connector"
)

// viewProgram is exampleProgram whose read tools return out-of-band map views besides clean text.
type viewProgram struct{ *exampleProgram }

func (p viewProgram) CallImages(ctx context.Context, name string, args json.RawMessage) (connector.ToolAnswer, error) {
	text, err := p.exampleProgram.Call(ctx, name, args)
	lat1, lon1 := 39.984, 116.318
	lat2, lon2 := 39.946, 116.412
	return connector.ToolAnswer{
		Text: text,
		Views: []wire.View{{
			Title: "Route",
			Sections: []wire.Section{{
				Items: []wire.Component{{
					Type: wire.ComponentMap,
					Alt:  "Home to Office route",
					Markers: []wire.MapMarker{
						{ID: "origin", Lat: &lat1, Lon: &lon1, Kind: wire.MapMarkerKindOrigin, Label: "Home"},
						{ID: "dest", Lat: &lat2, Lon: &lon2, Kind: wire.MapMarkerKindDestination, Label: "Office"},
					},
					Routes: []wire.MapRoute{{
						ID:     "fast",
						Mode:   wire.MapTravelModeDriving,
						Points: []wire.MapCoordinate{{Lat: &lat1, Lon: &lon1}, {Lat: &lat2, Lon: &lon2}},
					}},
				}},
			}},
		}},
	}, err
}

func TestReadToolAnswersWithViews(t *testing.T) {
	for _, way := range []string{"pipe", "plugin"} {
		t.Run("over the "+way, func(t *testing.T) {
			sec := &memSecrets{}
			env := []string{"PERSONA_PROC_KIND=views"}
			c := New(program(t, sec, env...))
			if way == "plugin" {
				c = linked(t, sec, env...)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go c.Run(ctx)
			waitUntil(t, "the program's status", func() bool { return c.Status(ctx).State == connector.StateNotSignedIn })
			a, err := c.CallImages(ctx, "example_chats", json.RawMessage(`{}`))
			if err != nil || len(a.Views) != 1 || a.Text == "" {
				t.Fatalf("CallImages: %d views, text %q, %v", len(a.Views), a.Text, err)
			}
			res := connector.ToolResult(a)
			if len(res.Content) != 1 || res.Content[0].Text != a.Text || len(res.ToolViews) != 1 {
				t.Fatalf("ToolResult altered content or dropped views: %+v", res)
			}
			if text, err := c.Call(ctx, "example_chats", json.RawMessage(`{}`)); err != nil || text != a.Text {
				t.Fatalf("Call is text alone: %q %v", text, err)
			}
		})
	}
}
