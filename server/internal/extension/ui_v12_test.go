package extension

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/extension/uistream"
	"github.com/nwverse/persona/server/internal/i18n"
)

func v12Example(t *testing.T) *View {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "schema", "extension", "examples", "View.v12-map-route.json"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := ParseView(raw)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func mapOf(t *testing.T, v *View) *UIElem {
	t.Helper()
	for i := range v.Sections {
		for j := range v.Sections[i].Items {
			if e := &v.Sections[i].Items[j]; e.Type == wire.ComponentMap {
				return e
			}
		}
	}
	t.Fatal("no map")
	return nil
}

func TestFitV12Example(t *testing.T) {
	page, _ := Slot("nav_page")
	v := v12Example(t)
	want, _ := json.Marshal(v)
	if dropped, err := Fit(v, page, 12); err != nil || len(dropped) > 0 {
		t.Fatalf("dropped %v, %v", dropped, err)
	}
	if got, _ := json.Marshal(v); string(got) != string(want) {
		t.Errorf("v12 app got map changed\nwant %s\n got %s", want, got)
	}
}

func TestFitV12DowngradeBelow12AndUnsupported(t *testing.T) {
	page, _ := Slot("nav_page")
	for _, tc := range []struct {
		name        string
		version     int
		unsupported []string
	}{
		{"v11", 11, nil},
		{"v12 unsupported map", 12, []string{wire.ComponentMap}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := v12Example(t)
			if _, err := FitFor(v, page, tc.version, tc.unsupported, i18n.En); err != nil {
				t.Fatal(err)
			}
			got := items(v)
			// 1 header + 2 routes + 3 markers + 1 link = 7 items
			if len(got) != 7 {
				t.Fatalf("got %d items, want 7: %+v", len(got), got)
			}
			if got[0].Type != wire.ComponentText || got[0].Text != "Home to Office — 28 min · 14.2 km · Via North Fourth Ring" {
				t.Fatalf("header item: %+v", got[0])
			}
			r1 := got[1]
			if r1.Type != wire.ComponentRow || r1.Title != "Fastest" || r1.Subtitle != "Driving" ||
				r1.Value != "28 min · 14.2 km" || r1.Badge == nil || r1.Badge.Text != "Selected" || r1.Badge.Tone != wire.TonePositive ||
				r1.Icon != wire.NavIconCar || r1.Key != "route-home-office/route/r-fastest" {
				t.Fatalf("route 1 row: %+v", r1)
			}
			dest := got[5]
			if dest.Type != wire.ComponentRow || dest.Title != "Office" || dest.Subtitle != "Chaoyang" ||
				dest.Key != "route-home-office/dest" || dest.Action == nil || dest.Action.Values["stop"] != "dest" {
				t.Fatalf("dest marker row: %+v", dest)
			}
			if got[6].Type != wire.ComponentLink || got[6].Title != "Open in Maps" {
				t.Fatalf("link item: %+v", got[6])
			}
		})
	}
}

func TestFitV12UnsupportedSceneKeepsMap(t *testing.T) {
	page, _ := Slot("nav_page")
	lat, lon := 39.9, 116.3
	v := &View{
		Sections: []Section{{
			Items: []UIElem{
				{Type: wire.ComponentMap, Key: "m1", Alt: "Commute", Markers: []wire.MapMarker{{ID: "p1", Lat: &lat, Lon: &lon, Label: "Home"}}},
				{Type: wire.ComponentScene, Key: "s1", Alt: "Room", Shapes: []wire.SceneShape{{Kind: wire.SceneShapeBox, Center: []float64{0, 0}, Size: []float64{1, 1}, Bottom: num(0), Top: num(1)}}, Pins: []wire.ScenePin{{ID: "lamp", At: []float64{0, 0}, Label: "Lamp"}}},
			},
		}},
	}
	if _, err := FitFor(v, page, 12, []string{wire.ComponentScene}, i18n.ZhHans); err != nil {
		t.Fatal(err)
	}
	got := items(v)
	if len(got) != 2 || got[0].Type != wire.ComponentMap || got[1].Type != wire.ComponentRow {
		t.Fatalf("expected map kept and scene downgraded: %+v", got)
	}
}

func TestFitV12AltOnly(t *testing.T) {
	page, _ := Slot("nav_page")
	lat, lon := 39.9, 116.3
	v := &View{
		Sections: []Section{{
			Items: []UIElem{{
				Type:     wire.ComponentMap,
				Alt:      "Beijing overview",
				Viewport: &wire.MapViewport{Center: wire.MapCoordinate{Lat: &lat, Lon: &lon}},
			}},
		}},
	}
	if _, err := Fit(v, page, 11); err != nil {
		t.Fatal(err)
	}
	if got := items(v); len(got) != 1 || got[0].Type != wire.ComponentText || got[0].Text != "Beijing overview" {
		t.Fatalf("items %+v", got)
	}
}

func TestFitV12Card(t *testing.T) {
	card, _ := Slot("overview_card")
	lat, lon := 39.9, 116.3
	v := v12Example(t)
	m := mapOf(t, v)
	m.Routes = nil
	m.Markers = nil
	for k := range 30 {
		m.Markers = append(m.Markers, wire.MapMarker{ID: "p" + strconv.Itoa(k), Lat: &lat, Lon: &lon, Label: "Stop " + strconv.Itoa(k)})
	}
	if _, err := Fit(v, card, 12); err != nil || len(items(v)) != 1 || items(v)[0].Type != wire.ComponentMap {
		t.Fatalf("card at 12: %v %+v", err, items(v))
	}
	if _, err := Fit(v, card, 11); err != nil {
		t.Fatal(err)
	}
	if n := len(items(v)); n < 1 || n > CompactItems {
		t.Fatalf("card at 11 has %d items, want 1..%d", n, CompactItems)
	}
}

func TestFitV12OldApps(t *testing.T) {
	page, _ := Slot("nav_page")
	for _, version := range []int{5, 3, 1} {
		v := v12Example(t)
		if _, err := Fit(v, page, version, i18n.Ja); err != nil {
			t.Fatalf("v%d: %v", version, err)
		}
		if len(items(v)) == 0 {
			t.Fatalf("v%d: nothing left", version)
		}
		for _, e := range items(v) {
			if e.Type == wire.ComponentMap {
				t.Fatalf("v%d: map leaked", version)
			}
		}
	}
}

func TestFitNativeV12(t *testing.T) {
	page, _ := Slot("nav_page")
	v := v12Example(t)
	v.Sections[0].Key = "s"
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	// Native v12 without "map" in unsupported keeps map natively and decodes as UIStreamSnapshot.
	fittedV12, err := FitNativeFor(raw, page, 12, nil, i18n.En)
	if err != nil {
		t.Fatal(err)
	}
	var gotV12 wire.View
	if err := json.Unmarshal(fittedV12, &gotV12); err != nil {
		t.Fatal(err)
	}
	if len(gotV12.Sections[0].Items) != 1 || gotV12.Sections[0].Items[0].Type != wire.ComponentMap {
		t.Fatalf("expected native map at v12: %+v", gotV12.Sections[0].Items)
	}
	snap12, _ := json.Marshal(wire.UIStreamSnapshot{Protocol: 1, Session: "s", Epoch: "e", RootKey: "root", Version: 1, View: gotV12})
	if _, err := uistream.DecodeSnapshot(snap12); err != nil {
		t.Fatalf("v12 snapshot decode: %v", err)
	}

	// Native v11 or unsupported ["map"] downgrades cleanly with valid unique keys on EVERY item.
	for _, tc := range []struct {
		name        string
		version     int
		unsupported []string
	}{
		{"v11", 11, nil},
		{"unsupported map", 12, []string{wire.ComponentMap}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fitted, err := FitNativeFor(raw, page, tc.version, tc.unsupported, i18n.En)
			if err != nil {
				t.Fatal(err)
			}
			var got wire.View
			if err := json.Unmarshal(fitted, &got); err != nil {
				t.Fatal(err)
			}
			for _, s := range got.Sections {
				for _, e := range s.Items {
					if e.Key == "" || utf8.RuneCountInString(e.Key) > graphRowKeyMost {
						t.Fatalf("bad native downgraded key %q in %+v", e.Key, e)
					}
					if e.Type == wire.ComponentMap {
						t.Fatalf("map not downgraded")
					}
				}
			}
			snap, _ := json.Marshal(wire.UIStreamSnapshot{Protocol: 1, Session: "s", Epoch: "e", RootKey: "root", Version: 1, View: got})
			if _, err := uistream.DecodeSnapshot(snap); err != nil {
				t.Fatalf("snapshot decode: %v", err)
			}
		})
	}
}

func TestFitResultMapBelow12OrUnsupported(t *testing.T) {
	page, _ := Slot("nav_page")
	makeRes := func() ActionResult {
		v := v12Example(t)
		return ActionResult{Lines: &FileLines{Path: "a.go", Side: "new", Start: 1, Lines: []string{"a"}}, Items: []UIElem{*mapOf(t, v)}}
	}
	res := makeRes()
	if err := FitResultFor(&res, page, 12, []string{wire.ComponentScene}); err != nil {
		t.Fatalf("v12 unsupported scene: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0].Type != wire.ComponentMap || res.Lines == nil {
		t.Fatalf("v12 did not keep map: %+v", res)
	}
	for _, tc := range []struct {
		version     int
		unsupported []string
	}{{11, nil}, {6, nil}, {12, []string{wire.ComponentMap}}} {
		r := makeRes()
		if err := FitResultFor(&r, page, tc.version, tc.unsupported); err != nil {
			t.Fatalf("v%d %v: %v", tc.version, tc.unsupported, err)
		}
		if r.Items != nil || r.Lines == nil {
			t.Fatalf("v%d %v: want items cleared and lines kept: %+v", tc.version, tc.unsupported, r)
		}
	}
}
