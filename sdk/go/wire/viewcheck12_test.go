package wire

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func decodeMapView(t *testing.T, raw string) *View {
	t.Helper()
	var v View
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("decode view JSON: %v\n%s", err, raw)
	}
	return &v
}

func TestCheckViewMapAccepts(t *testing.T) {
	v := decodeMapView(t, `{
		"sections": [{
			"items": [{
				"type": "map",
				"key": "m1",
				"alt": "Route from Home to Office",
				"caption": "28 min · 14.2 km",
				"viewport": {"center": {"lat": 39.965, "lon": 116.365}, "span_lat": 0.08, "span_lon": 0.09},
				"markers": [
					{"id": "origin", "lat": 39.984, "lon": 116.318, "kind": "origin", "label": "Home", "tone": "positive"},
					{"id": "dest", "lat": 39.946, "lon": 116.412, "kind": "destination", "label": "Office", "color": "amber", "action": {"id": "focus"}}
				],
				"routes": [{
					"id": "r1",
					"label": "Fastest",
					"mode": "driving",
					"distance_meters": 14200,
					"duration_seconds": 1680,
					"selected": true,
					"tone": "positive",
					"points": [{"lat": 39.984, "lon": 116.318}, {"lat": 39.946, "lon": 116.412}]
				}],
				"links": [{"title": "Open", "url": "https://maps.example.com/dir"}]
			}]
		}]
	}`)
	if err := CheckView(v); err != nil {
		t.Fatalf("CheckView(map): %v", err)
	}
	if acts := Actions(v); len(acts) != 1 || acts[0].ID != "focus" {
		t.Fatalf("Actions(map) = %+v", acts)
	}
}

func TestCheckViewMapRejects(t *testing.T) {
	lat := 39.9
	lon := 116.4
	nan := math.NaN()

	cases := []struct {
		name string
		comp Component
		want string
	}{
		{
			name: "empty map without viewport markers or routes",
			comp: Component{Type: ComponentMap, Alt: "Empty"},
			want: "at least one",
		},
		{
			name: "duplicate marker id",
			comp: Component{
				Type: ComponentMap, Alt: "Dup",
				Markers: []MapMarker{
					{ID: "a", Lat: &lat, Lon: &lon, Label: "A"},
					{ID: "a", Lat: &lat, Lon: &lon, Label: "B"},
				},
			},
			want: "twice",
		},
		{
			name: "marker tone and color",
			comp: Component{
				Type: ComponentMap, Alt: "Both",
				Markers: []MapMarker{{ID: "a", Lat: &lat, Lon: &lon, Label: "A", Tone: TonePositive, Color: "amber"}},
			},
			want: "not both",
		},
		{
			name: "route duplicate id",
			comp: Component{
				Type: ComponentMap, Alt: "DupRoute",
				Routes: []MapRoute{
					{ID: "r", Points: []MapCoordinate{{Lat: &lat, Lon: &lon}, {Lat: &lat, Lon: &lon}}},
					{ID: "r", Points: []MapCoordinate{{Lat: &lat, Lon: &lon}, {Lat: &lat, Lon: &lon}}},
				},
			},
			want: "twice",
		},
		{
			name: "non-finite lat via CheckMap",
			comp: Component{
				Type:    ComponentMap,
				Alt:     "NaN",
				Markers: []MapMarker{{ID: "a", Lat: &nan, Lon: &lon, Label: "A"}},
			},
			want: "finite",
		},
		{
			name: "http link",
			comp: Component{
				Type:     ComponentMap,
				Alt:      "HTTP",
				Viewport: &MapViewport{Center: MapCoordinate{Lat: &lat, Lon: &lon}},
				Links:    []MapLink{{Title: "Open", URL: "http://maps.example.com"}},
			},
			want: "https",
		},
		{
			name: "too many points across routes",
			comp: Component{
				Type: ComponentMap,
				Alt:  "Many",
				Routes: func() []MapRoute {
					pts := make([]MapCoordinate, 501)
					for i := range pts {
						pts[i] = MapCoordinate{Lat: &lat, Lon: &lon}
					}
					return []MapRoute{
						{ID: "r1", Points: pts},
						{ID: "r2", Points: pts},
						{ID: "r3", Points: pts},
						{ID: "r4", Points: pts},
					}
				}(),
			},
			want: "points together",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckMap(&tc.comp)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("CheckMap() = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestStripAssistantFieldsMapMarkerAction(t *testing.T) {
	lat, lon := 39.9, 116.4
	v := &View{
		Sections: []Section{{
			Items: []Component{{
				Type: ComponentMap,
				Alt:  "Map",
				Markers: []MapMarker{
					{ID: "m1", Lat: &lat, Lon: &lon, Label: "Reply", Action: &Action{ID: "a1", Reply: "go"}},
					{ID: "m2", Lat: &lat, Lon: &lon, Label: "Ext", Action: &Action{ID: "a2"}},
				},
			}},
		}},
	}
	dropped := StripAssistantFields(v)
	if len(dropped) != 1 || dropped[0] != "assistant_action" {
		t.Fatalf("dropped = %v", dropped)
	}
	m := v.Sections[0].Items[0].Markers
	if m[0].Action != nil || m[1].Action == nil || m[1].Action.ID != "a2" {
		t.Fatalf("markers = %+v", m)
	}
}
