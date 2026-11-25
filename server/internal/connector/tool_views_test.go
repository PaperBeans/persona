package connector

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
)

func sampleMapView(title string) wire.View {
	lat1, lon1 := 39.984, 116.318
	lat2, lon2 := 39.946, 116.412
	return wire.View{
		Title: title,
		Sections: []wire.Section{{
			Items: []wire.Component{{
				Type: wire.ComponentMap,
				Alt:  title + " route",
				Markers: []wire.MapMarker{
					{ID: "origin", Lat: &lat1, Lon: &lon1, Kind: wire.MapMarkerKindOrigin, Label: "Home"},
					{ID: "dest", Lat: &lat2, Lon: &lon2, Kind: wire.MapMarkerKindDestination, Label: "Office",
						Action: &wire.Action{ID: "open-dest", Open: "https://maps.example.com/office"}},
				},
				Routes: []wire.MapRoute{{
					ID:     "fast",
					Mode:   wire.MapTravelModeDriving,
					Points: []wire.MapCoordinate{{Lat: &lat1, Lon: &lon1}, {Lat: &lat2, Lon: &lon2}},
				}},
			}},
		}},
	}
}

func TestSanitizeToolViewsAndToolResult(t *testing.T) {
	v := sampleMapView("Commute")
	v.Fallback = "Home to Office"
	v.Pages = map[string]wire.View{"detail": sampleMapView("Detail")}
	v.Sections = append(v.Sections, wire.Section{
		Items: []wire.Component{{Type: wire.ComponentHtml, Html: "<b>bad</b>"}},
	})

	refView := wire.View{
		Sections: []wire.Section{{
			Items: []wire.Component{{Type: wire.ComponentImage, Src: &wire.ImageSource{Ref: "secret.png"}, Alt: "secret"}},
		}},
	}
	badLat := 120.0
	lon := 116.3
	invalidView := wire.View{
		Sections: []wire.Section{{
			Items: []wire.Component{{
				Type:    wire.ComponentMap,
				Alt:     "bad lat",
				Markers: []wire.MapMarker{{ID: "m1", Lat: &badLat, Lon: &lon, Label: "Bad"}},
			}},
		}},
	}

	got := SanitizeToolViews([]wire.View{refView, invalidView, v})
	if len(got) != 1 {
		t.Fatalf("got %d views, want 1", len(got))
	}
	if got[0].Fallback != "" || got[0].Pages != nil || len(got[0].Sections) != 1 {
		t.Fatalf("assistant-only or empty sections leaked: %+v", got[0])
	}
	if got[0].Sections[0].Items[0].Markers[1].Action != nil {
		t.Fatalf("assistant-only marker action leaked: %+v", got[0].Sections[0].Items[0].Markers[1])
	}
	if v.Fallback == "" || v.Sections[0].Items[0].Markers[1].Action == nil {
		t.Fatal("SanitizeToolViews mutated caller slice")
	}

	res := ToolResult(ToolAnswer{Text: "Driving takes 28 min", Views: []wire.View{v}})
	if len(res.Content) != 1 || res.Content[0].Text != "Driving takes 28 min" {
		t.Fatalf("model content changed: %+v", res.Content)
	}
	if len(res.ToolViews) != 1 || res.ToolViews[0].Title != "Commute" {
		t.Fatalf("unexpected ToolViews: %+v", res.ToolViews)
	}
}

func TestSanitizeToolViewsBounds(t *testing.T) {
	many := make([]wire.View, ToolViewsMost+3)
	for i := range many {
		many[i] = sampleMapView("R")
	}
	if got := SanitizeToolViews(many); len(got) != ToolViewsMost {
		t.Fatalf("count cap: got %d, want %d", len(got), ToolViewsMost)
	}

	// Build a view that alone exceeds ToolViewsBytesMost cleanly without exceeding Component limits:
	// combine ~130 long text items (each <= 2000 chars).
	var items []wire.Component
	bigText := strings.Repeat("a", 1500)
	for range 100 {
		items = append(items, wire.Component{Type: wire.ComponentText, Text: bigText})
	}
	huge := wire.View{Sections: []wire.Section{{Items: items}}}
	small := sampleMapView("Small")
	got := SanitizeToolViews([]wire.View{huge, small})
	if len(got) != 1 || got[0].Title != "Small" {
		t.Fatalf("byte budget failed: %+v", got)
	}
}

type viewFake struct{ *fake }

func (f viewFake) CallImages(_ context.Context, name string, args json.RawMessage) (ToolAnswer, error) {
	return ToolAnswer{Text: "route from " + name, Views: []wire.View{sampleMapView("Route")}}, nil
}

func TestRegistryCallsWithViews(t *testing.T) {
	r := NewRegistry(nil)
	if err := r.Add(viewFake{&fake{id: "mapf", status: Status{State: StateOK}}}); err != nil {
		t.Fatal(err)
	}
	res := r.Call(context.Background(), "mapf_read", nil, nil)
	if len(res.Content) != 1 || res.Content[0].Text != "route from mapf_read" || len(res.ToolViews) != 1 {
		t.Fatalf("got %+v", res)
	}
}
