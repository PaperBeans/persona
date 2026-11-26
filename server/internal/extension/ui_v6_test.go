package extension

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/i18n"
)

func v6Example(t *testing.T, name string) *View {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "schema", "extension", "examples", "View.v6-"+name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := ParseView(raw)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return v
}

var v6Examples = []string{"media", "controls", "charts", "tiles", "inputs"}

func items(v *View) []UIElem {
	var out []UIElem
	for _, s := range v.Sections {
		out = append(out, s.Items...)
	}
	return out
}

func num(x float64) *float64 { return &x }

// An app of UI version 6 gets every example as the extension drew it; every older one gets something it draws,
// without a component newer than itself.
func TestFitV6Examples(t *testing.T) {
	page, _ := Slot("nav_page")
	for _, name := range v6Examples {
		v := v6Example(t, name)
		want, _ := json.Marshal(v)
		if dropped, err := Fit(v, page, 6); err != nil || len(dropped) > 0 {
			t.Fatalf("%s for 6: dropped %v, %v", name, dropped, err)
		}
		if got, _ := json.Marshal(v); string(got) != string(want) {
			t.Errorf("%s: an app of 6 gets it changed\nwant %s\n got %s", name, want, got)
		}
		for version := 1; version <= 5; version++ {
			v := v6Example(t, name)
			if _, err := Fit(v, page, version); err != nil {
				t.Errorf("%s for %d: %v", name, version, err)
				continue
			}
			for _, e := range items(v) {
				if since := wire.ComponentSince[e.Type]; since > version {
					t.Errorf("%s for %d: a %s left", name, version, e.Type)
				}
				if e.Control != nil || len(e.Actions) > 0 || e.Spark != nil || e.IconColor != "" || e.Src != nil || len(e.Tiles) > 0 {
					t.Errorf("%s for %d: a v6 field left on %s: %+v", name, version, e.Type, e)
				}
				if e.Badge != nil && e.Badge.Color != "" || e.Style == "title" || e.Style == "headline" || slices.Contains(newIcons6, e.Icon) {
					t.Errorf("%s for %d: a v6 style left on %s", name, version, e.Type)
				}
				if e.Thumbnail != nil && e.Thumbnail.Ref != "" || e.Avatar != nil && e.Avatar.Ref != "" {
					t.Errorf("%s for %d: a ref picture left", name, version)
				}
				for _, s := range e.Series {
					if s.Color != "" || slices.ContainsFunc(s.Points, func(p ChartPoint) bool { return p.Open != nil || p.Volume != nil }) {
						t.Errorf("%s for %d: a v6 chart left", name, version)
					}
				}
				if e.Type == "field" && FieldLayoutKind(e.Kind) {
					t.Errorf("%s for %d: a %s field left", name, version, e.Kind)
				}
			}
		}
	}
}

// FieldLayoutKind says whether kind is a field kind of UI version 6.
func FieldLayoutKind(kind string) bool {
	return slices.Contains([]string{"date", "time", "datetime", "multichoice", "multiline"}, kind)
}

// What CheckView refuses of UI version 6 that the schema cannot say.
func TestCheckViewV6(t *testing.T) {
	act := &UIAction{ID: "a"}
	row := func(e UIElem) UIElem { e.Type, e.Title = "row", orStr(e.Title, "r"); return e }
	sw := &RowControl{Kind: "switch", Name: "s", Action: UIAction{ID: "s"}}
	candle := func(o, h, l, c float64) ChartPoint {
		return ChartPoint{Label: fmt.Sprint(o, h, l, c), Value: c, Open: &o, High: &h, Low: &l, Close: &c}
	}
	points := func(n int) []ChartPoint {
		out := make([]ChartPoint, n)
		for i := range out {
			out[i] = ChartPoint{Label: fmt.Sprint(i), Value: float64(i)}
		}
		return out
	}
	candles := func(n int) []ChartPoint {
		out := make([]ChartPoint, n)
		for i := range out {
			out[i] = candle(1, 2, 0.5, 1.5)
			out[i].Label = fmt.Sprint(i)
		}
		return out
	}
	tiles := func(n int) []Tile {
		out := make([]Tile, n)
		for i := range out {
			out[i] = Tile{Title: fmt.Sprint(i)}
		}
		return out
	}
	images := func(n int) []UIElem {
		out := make([]UIElem, n)
		for i := range out {
			out[i] = UIElem{Type: "image", Src: &ImageSource{URL: "https://example.com/a.png"}, Alt: "a"}
		}
		return out
	}
	for name, its := range map[string][]UIElem{
		"url and ref":           {{Type: "image", Src: &ImageSource{URL: "https://example.com/a.png", Ref: "a"}, Alt: "a"}},
		"neither url nor ref":   {{Type: "image", Src: &ImageSource{}, Alt: "a"}},
		"extension's path":      {{Type: "image", Src: &ImageSource{Ref: "a", Path: "/api/v1/extensions/x/-/image/abc"}, Alt: "a"}},
		"thumbnail path":        {row(UIElem{Thumbnail: &wire.Thumbnail{Ref: "a", Path: "/api/v1/extensions/x/-/image/abc"}})},
		"thumbnail none":        {row(UIElem{Thumbnail: &wire.Thumbnail{}})},
		"avatar url and ref":    {row(UIElem{Avatar: &wire.Avatar{URL: "https://example.com/a.png", Ref: "a"}})},
		"tile url user":         {{Type: "grid", Tiles: []Tile{{Title: "t", Image: &ImageSource{URL: "https://u:p@example.com/a.png"}}}}},
		"51 pictures":           images(51),
		"badge tone and color":  {row(UIElem{Badge: &Badge{Text: "b", Tone: "danger", Color: "red"}})},
		"spark tone and color":  {row(UIElem{Spark: &Spark{Values: []float64{1, 2}, Tone: "positive", Color: "green"}})},
		"series tone and color": {{Type: "chart", Kind: "line", Series: []ChartSeries{{Tone: "danger", Color: "red", Points: points(2)}}}},
		"series color twice": {{Type: "chart", Kind: "line", Series: []ChartSeries{
			{Name: "a", Color: "red", Points: points(2)}, {Name: "b", Color: "red", Points: points(2)}}}},
		"high below low":     {{Type: "chart", Kind: "candlestick", Series: []ChartSeries{{Points: []ChartPoint{candle(1, 0.5, 2, 1)}}}}},
		"high below close":   {{Type: "chart", Kind: "candlestick", Series: []ChartSeries{{Points: []ChartPoint{candle(1, 1.5, 0.5, 2)}}}}},
		"candle without low": {{Type: "chart", Kind: "candlestick", Series: []ChartSeries{{Points: []ChartPoint{{Label: "a", Value: 1, Open: num(1), High: num(1), Close: num(1)}}}}}},
		"some volumes": {{Type: "chart", Kind: "candlestick", Series: []ChartSeries{{Points: func() []ChartPoint {
			p := candles(2)
			p[0].Volume = num(10)
			return p
		}()}}}},
		"two candle series":           {{Type: "chart", Kind: "candlestick", Series: []ChartSeries{{Name: "a", Points: candles(2)}, {Name: "b", Points: candles(2)}}}},
		"stacked candles":             {{Type: "chart", Kind: "candlestick", Stacked: true, Series: []ChartSeries{{Points: candles(2)}}}},
		"241 candles":                 {{Type: "chart", Kind: "candlestick", Series: []ChartSeries{{Points: candles(241)}}}},
		"121 points of line":          {{Type: "chart", Kind: "line", Series: []ChartSeries{{Points: points(121)}}}},
		"ohlc on a bar":               {{Type: "chart", Kind: "bar", Series: []ChartSeries{{Points: []ChartPoint{{Label: "a", Value: 1, Open: num(1)}}}}}},
		"rising on a line":            {{Type: "chart", Kind: "line", Rising: "red", Series: []ChartSeries{{Points: points(2)}}}},
		"center on a pie":             {{Type: "chart", Kind: "pie", Center: "x", Series: []ChartSeries{{Points: points(3)[1:]}}}},
		"one slice":                   {{Type: "chart", Kind: "donut", Series: []ChartSeries{{Points: []ChartPoint{{Label: "a", Value: 1}}}}}},
		"nine slices":                 {{Type: "chart", Kind: "donut", Series: []ChartSeries{{Points: points(10)[1:]}}}},
		"negative slice":              {{Type: "chart", Kind: "pie", Series: []ChartSeries{{Points: []ChartPoint{{Label: "a", Value: -1}, {Label: "b", Value: 2}}}}}},
		"slices sum to 0":             {{Type: "chart", Kind: "pie", Series: []ChartSeries{{Points: []ChartPoint{{Label: "a"}, {Label: "b"}}}}}},
		"slice label twice":           {{Type: "chart", Kind: "pie", Series: []ChartSeries{{Points: []ChartPoint{{Label: "a", Value: 1}, {Label: "a", Value: 2}}}}}},
		"destructive without confirm": {row(UIElem{Actions: []RowAction{{Title: "删", Role: "destructive", Action: *act}}})},
		"three under a swipe": {row(UIElem{Actions: []RowAction{{Title: "1", Swipe: true, Action: *act}, {Title: "2", Swipe: true, Action: *act},
			{Title: "3", Swipe: true, Action: *act}}})},
		"action title twice":    {row(UIElem{Actions: []RowAction{{Title: "1", Action: *act}, {Title: "1", Action: *act}}})},
		"control without a key": {row(UIElem{Control: sw})},
		"switch with a value":   {row(UIElem{Key: "k", Control: &RowControl{Kind: "switch", Name: "s", Value: num(1), Action: *act}})},
		"slider min above max":  {row(UIElem{Key: "k", Control: &RowControl{Kind: "slider", Name: "s", Value: num(1), Min: num(2), Max: num(1), Action: *act}})},
		"slider value outside":  {row(UIElem{Key: "k", Control: &RowControl{Kind: "slider", Name: "s", Value: num(5), Min: num(0), Max: num(1), Action: *act}})},
		"slider step 0":         {row(UIElem{Key: "k", Control: &RowControl{Kind: "slider", Name: "s", Value: num(0), Min: num(0), Max: num(1), Step: num(0), Action: *act}})},
		"slider too many steps": {row(UIElem{Key: "k", Control: &RowControl{Kind: "slider", Name: "s", Value: num(0), Min: num(0), Max: num(1), Step: num(0.00001), Action: *act}})},
		"stepper without step":  {row(UIElem{Key: "k", Control: &RowControl{Kind: "stepper", Name: "s", Value: num(1), Action: *act}})},
		"stepper value outside": {row(UIElem{Key: "k", Control: &RowControl{Kind: "stepper", Name: "s", Value: num(9), Step: num(1), Max: num(5), Action: *act}})},
		"on for a stepper":      {row(UIElem{Key: "k", Control: &RowControl{Kind: "stepper", Name: "s", On: true, Value: num(1), Step: num(1), Action: *act}})},
		"control name twice":    {{Type: "field", Name: "s", Label: "S", Kind: "text"}, row(UIElem{Key: "k", Control: sw})},
		"control action full": {row(UIElem{Key: "k", Control: &RowControl{Kind: "switch", Name: "s", Action: UIAction{ID: "s",
			Values: map[string]string{"1": "", "2": "", "3": "", "4": "", "5": "", "6": "", "7": "", "8": ""}}}})},
		"tile slider":            {{Type: "grid", Tiles: []Tile{{Key: "t", Title: "t", Control: &RowControl{Kind: "slider", Name: "s", Value: num(0), Min: num(0), Max: num(1), Action: *act}}}}},
		"tile control no key":    {{Type: "grid", Tiles: []Tile{{Title: "t", Control: sw}}}},
		"tile icon and image":    {{Type: "grid", Tiles: []Tile{{Title: "t", Icon: "home", Image: &ImageSource{Ref: "a"}}}}},
		"tile key twice":         {{Type: "text", Key: "k", Text: "x"}, {Type: "grid", Tiles: []Tile{{Key: "k", Title: "t"}}}},
		"tiles past 300":         {{Type: "grid", Tiles: tiles(60)}, {Type: "grid", Tiles: tiles(60)}, {Type: "grid", Tiles: tiles(60)}, {Type: "grid", Tiles: tiles(60)}, {Type: "grid", Tiles: tiles(60)}, {Type: "text", Text: "x"}},
		"reserved name":          {{Type: "field", Name: "~x", Label: "X", Kind: "text"}},
		"reserved control name":  {row(UIElem{Key: "k", Control: &RowControl{Kind: "switch", Name: "~s", Action: *act}})},
		"bad date":               {{Type: "field", Name: "d", Label: "D", Kind: "date", Value: "2026-13-01"}},
		"date written loosely":   {{Type: "field", Name: "d", Label: "D", Kind: "date", Value: "2026-1-1"}},
		"bad time":               {{Type: "field", Name: "d", Label: "D", Kind: "time", Value: "7:30pm"}},
		"datetime with zone":     {{Type: "field", Name: "d", Label: "D", Kind: "datetime", Value: "2026-11-06T07:30+08:00"}},
		"earliest after latest":  {{Type: "field", Name: "d", Label: "D", Kind: "date", Earliest: "2026-12-01", Latest: "2026-11-01"}},
		"earliest on a text":     {{Type: "field", Name: "d", Label: "D", Kind: "text", Earliest: "2026-12-01"}},
		"multichoice no choices": {{Type: "field", Name: "m", Label: "M", Kind: "multichoice"}},
		"multichoice not json":   {{Type: "field", Name: "m", Label: "M", Kind: "multichoice", Value: "a,b", Choices: []UIChoice{{Value: "a"}, {Value: "b"}}}},
		"multichoice not one":    {{Type: "field", Name: "m", Label: "M", Kind: "multichoice", Value: `["c"]`, Choices: []UIChoice{{Value: "a"}}}},
		"multichoice twice":      {{Type: "field", Name: "m", Label: "M", Kind: "multichoice", Value: `["a","a"]`, Choices: []UIChoice{{Value: "a"}}}},
		"long title":             {{Type: "text", Text: strings.Repeat("长", wire.TitleMost+1), Style: "title"}},
	} {
		v := &View{Sections: []Section{{Items: its}}}
		if err := CheckView(v); err == nil {
			t.Errorf("%s: taken", name)
		}
	}
	// and what it takes
	for name, its := range map[string][]UIElem{
		"240 candles":   {{Type: "chart", Kind: "candlestick", Series: []ChartSeries{{Points: candles(240)}}}},
		"50 pictures":   images(50),
		"switch":        {row(UIElem{Key: "k", Control: sw, Action: act})},
		"slider step":   {row(UIElem{Key: "k", Control: &RowControl{Kind: "slider", Name: "s", Value: num(0), Min: num(0), Max: num(1), Action: *act}})},
		"empty dates":   {{Type: "field", Name: "d", Label: "D", Kind: "datetime"}},
		"no choice yet": {{Type: "field", Name: "m", Label: "M", Kind: "multichoice", Choices: []UIChoice{{Value: "a"}}}},
		"300 tiles":     {{Type: "grid", Tiles: tiles(60)}, {Type: "grid", Tiles: tiles(60)}, {Type: "grid", Tiles: tiles(60)}, {Type: "grid", Tiles: tiles(60)}, {Type: "grid", Tiles: tiles(60)}},
	} {
		v := &View{Sections: []Section{{Items: its}}}
		if err := CheckView(v); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func orStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// A card takes switches and steppers, not sliders; a few pictures and tiles.
func TestFitCompactV6(t *testing.T) {
	card, _ := Slot("overview_card")
	act := UIAction{ID: "a"}
	ok := &View{Sections: []Section{{Items: []UIElem{
		{Type: "row", Key: "a", Title: "a", Control: &RowControl{Kind: "switch", Name: "a", Action: act}},
		{Type: "row", Key: "b", Title: "b", Control: &RowControl{Kind: "stepper", Name: "b", Value: num(1), Step: num(1), Action: act},
			Actions: []RowAction{{Title: "x", Action: act}}},
		{Type: "grid", Tiles: []Tile{{Title: "1"}, {Title: "2"}, {Title: "3"}, {Title: "4"}}},
	}}}}
	if _, err := Fit(ok, card, 6); err != nil {
		t.Fatal(err)
	}
	for name, its := range map[string][]UIElem{
		"slider":     {{Type: "row", Key: "a", Title: "a", Control: &RowControl{Kind: "slider", Name: "a", Value: num(0), Min: num(0), Max: num(1), Action: act}}},
		"5 tiles":    {{Type: "grid", Tiles: []Tile{{Title: "1"}, {Title: "2"}, {Title: "3"}, {Title: "4"}, {Title: "5"}}}},
		"7 carousel": {{Type: "carousel", Tiles: []Tile{{Title: "1"}, {Title: "2"}, {Title: "3"}, {Title: "4"}, {Title: "5"}, {Title: "6"}, {Title: "7"}}}},
		"5 pictures": slices.Repeat([]UIElem{{Type: "image", Src: &ImageSource{Ref: "a"}, Alt: "a"}}, 5),
		"13 tiles":   {{Type: "grid", Tiles: []Tile{{Title: "1"}, {Title: "2"}, {Title: "3"}, {Title: "4"}}}, {Type: "grid", Tiles: []Tile{{Title: "1"}, {Title: "2"}, {Title: "3"}, {Title: "4"}}}, {Type: "grid", Tiles: []Tile{{Title: "1"}, {Title: "2"}, {Title: "3"}, {Title: "4"}}}, {Type: "text", Text: "x"}},
	} {
		if _, err := Fit(&View{Sections: []Section{{Items: its}}}, card, 6); err == nil {
			t.Errorf("a card takes %s", name)
		}
	}
	// an older card gets the switch as a row to tap, and no field for a stepper
	old := &View{Sections: []Section{{Items: []UIElem{
		{Type: "row", Key: "a", Title: "a", Control: &RowControl{Kind: "switch", Name: "a", Action: act}},
		{Type: "row", Key: "b", Title: "b", Control: &RowControl{Kind: "stepper", Name: "b", Value: num(1), Step: num(1), Action: act}},
	}}}}
	if _, err := Fit(old, card, 3); err != nil {
		t.Fatal(err)
	}
	if got := items(old); len(got) != 2 || got[0].Value != "关" || got[1].Value != "1" {
		t.Fatalf("v3 card: %+v", got)
	}
	// the hub's own words in the fallback follow the app's language
	en := &View{Sections: []Section{{Items: []UIElem{
		{Type: "row", Key: "a", Title: "a", Control: &RowControl{Kind: "switch", Name: "a", Action: act}},
	}}}}
	if _, err := Fit(en, card, 3, i18n.En); err != nil {
		t.Fatal(err)
	}
	if got := items(en); len(got) != 1 || got[0].Value != "Off" {
		t.Fatalf("v3 card in English: %+v", got)
	}
}

// Controls for an older app: version 4 and 5 tap a row or a button carrying the value; older ones fill a field and
// submit it.
func TestFitControlsV6(t *testing.T) {
	page, _ := Slot("nav_page")
	v := v6Example(t, "controls")
	if _, err := Fit(v, page, 5); err != nil {
		t.Fatal(err)
	}
	got := items(v)
	byTitle := func(title string) []int {
		var out []int
		for i, e := range got {
			if e.Title == title {
				out = append(out, i)
			}
		}
		return out
	}
	// the main light's row keeps its own action (push); the switch is a row of its own under it
	main := got[byTitle("主灯")[0]]
	if main.Action == nil || main.Action.Push == nil {
		t.Fatalf("主灯 lost its action: %+v", main)
	}
	sw := got[byTitle("开关")[0]]
	if sw.Value != "开" || sw.Action == nil || sw.Action.ID != "toggle" || sw.Action.Values["light.main"] != "false" || sw.Action.Values["entity"] != "light.main" {
		t.Fatalf("switch row: %+v %+v", sw, sw.Action)
	}
	// a slider: a row with its value, a number field, a button submitting it
	i := byTitle("亮度")[0]
	if got[i].Value != "60 %" || got[i+1].Type != "field" || got[i+1].Kind != "number" || got[i+1].Value != "60" || got[i+2].Type != "button" || !got[i+2].Action.Submit {
		t.Fatalf("slider: %+v", got[i:i+3])
	}
	if got[byTitle("色温")[0]].Value != "4000 K" {
		t.Fatal("display not used")
	}
	// a disabled switch is only shown
	if d := got[byTitle("台灯")[0]]; d.Action != nil || d.Value != "关" {
		t.Fatalf("disabled: %+v", d)
	}
	// a stepper: − and + carrying the value a step away
	i = byTitle("温度")[0]
	if got[i+1].Title != "−" || got[i+1].Action.Values["temperature"] != "22" || got[i+2].Title != "+" || got[i+2].Action.Values["temperature"] != "23" {
		t.Fatalf("stepper: %+v %+v", got[i+1].Action, got[i+2].Action)
	}
	// clamped to its bounds: at the max, no +
	top := &View{Sections: []Section{{Items: []UIElem{{Type: "row", Key: "t", Title: "t",
		Control: &RowControl{Kind: "stepper", Name: "t", Value: num(30), Max: num(30), Step: num(1), Action: UIAction{ID: "t"}}}}}}}
	if _, err := Fit(top, page, 5); err != nil {
		t.Fatal(err)
	}
	if got := items(top); len(got) != 2 || got[1].Title != "−" {
		t.Fatalf("at the max: %+v", got)
	}

	v = v6Example(t, "controls")
	if _, err := Fit(v, page, 3); err != nil {
		t.Fatal(err)
	}
	got = items(v)
	i = byTitle("主灯")[0]
	if got[i+1].Type != "field" || got[i+1].Kind != "toggle" || got[i+1].Name != "light.main" || got[i+1].Value != "true" ||
		got[i+2].Type != "button" || got[i+2].Title != "应用" || !got[i+2].Action.Submit || got[i+2].Action.Values != nil {
		t.Fatalf("v3 switch: %+v", got[i:i+3])
	}
	i = byTitle("温度")[0]
	if got[i+1].Kind != "number" || got[i+1].Value != "22.5" || got[i+2].Title != "设为该值" {
		t.Fatalf("v3 stepper: %+v", got[i:i+3])
	}
	for _, a := range Actions(v) {
		if len(a.Values) > 0 {
			t.Errorf("v3 gets action values: %+v", a)
		}
	}
}

// Charts for an older app: a candlestick as the line of its closes, an area as a line, a donut and a pie as bars;
// colors as tones once each.
func TestFitChartsV6(t *testing.T) {
	page, _ := Slot("nav_page")
	v := v6Example(t, "charts")
	if _, err := Fit(v, page, 5); err != nil {
		t.Fatal(err)
	}
	got := items(v)
	k, area, donut, pie := got[0], got[1], got[2], got[3]
	if k.Kind != "line" || k.Rising != "" || !strings.HasPrefix(k.Detail, "收盘价") || k.Series[0].Points[2].Value != 10.6 || k.Series[0].Points[2].Close != nil {
		t.Fatalf("candlestick: %+v", k)
	}
	if area.Kind != "line" || area.Stacked || area.Series[0].Color != "" || area.Series[1].Tone != "caution" || area.Series[0].Tone != "" {
		t.Fatalf("area: %+v", area)
	}
	if donut.Kind != "bar" || donut.Center != "" || !strings.HasPrefix(donut.Detail, "¥12,400") || pie.Kind != "bar" {
		t.Fatalf("donut / pie: %+v %+v", donut, pie)
	}
	if row := got[4]; row.Spark != nil || row.Badge.Color != "" || row.Badge.Tone != "danger" {
		t.Fatalf("row: %+v", row)
	}
	if st := got[5].Stats; st[0].Spark != nil || st[1].Spark != nil {
		t.Fatal("stat sparks left")
	}
	// the latest 120 of 240 candles
	pts := make([]ChartPoint, CandlesMostV6)
	for i := range pts {
		c := float64(i)
		pts[i] = ChartPoint{Label: fmt.Sprint(i), Value: c, Open: num(c), High: num(c), Low: num(c), Close: num(c)}
	}
	long := &View{Sections: []Section{{Items: []UIElem{{Type: "chart", Kind: "candlestick", Series: []ChartSeries{{Points: pts}}}}}}}
	if _, err := Fit(long, page, 5); err != nil {
		t.Fatal(err)
	}
	if p := long.Sections[0].Items[0].Series[0].Points; len(p) != PointsMost || p[0].Label != "120" {
		t.Fatalf("%d points from %s", len(p), p[0].Label)
	}
	// two colors on one tone: the second gets the app's own
	two := &View{Sections: []Section{{Items: []UIElem{{Type: "chart", Kind: "bar", Series: []ChartSeries{
		{Name: "a", Color: "orange", Points: []ChartPoint{{Label: "x", Value: 1}}}, {Name: "b", Color: "amber", Points: []ChartPoint{{Label: "x", Value: 1}}}}}}}}}
	if _, err := Fit(two, page, 5); err != nil {
		t.Fatal(err)
	}
	if s := two.Sections[0].Items[0].Series; s[0].Tone != "caution" || s[1].Tone != "" {
		t.Fatalf("tones: %+v", s)
	}
	// a version 1 app draws no chart at all
	v = v6Example(t, "charts")
	if _, err := Fit(v, page, 1); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(items(v), func(e UIElem) bool { return e.Type == "chart" }) {
		t.Fatal("a chart for version 1")
	}
}

const CandlesMostV6 = wire.CandlesMost

// Pictures, tiles and styles for an older app.
func TestFitMediaAndTilesV6(t *testing.T) {
	page, _ := Slot("nav_page")
	v := v6Example(t, "media")
	if _, err := Fit(v, page, 5); err != nil {
		t.Fatal(err)
	}
	got := items(v)
	if got[0].Type != "text" || got[0].Style != "body" {
		t.Fatalf("title: %+v", got[0])
	}
	if got[1].Type != "text" || !strings.Contains(got[1].Text, "前门摄像头的画面") || got[1].Style != "caption" {
		t.Fatalf("a ref picture: %+v", got[1])
	}
	if p := got[3]; p.Type != "row" || p.Thumbnail == nil || p.Thumbnail.URL != "https://example.com/photo.jpg" || p.Title != "一张照片" {
		t.Fatalf("a url picture: %+v", p)
	}
	if a := got[5]; a.Avatar == nil || a.Avatar.Text != "明" || a.Avatar.Ref != "" || a.Badge.Tone != "positive" {
		t.Fatalf("avatar: %+v", a)
	}
	if b := got[6]; b.Thumbnail != nil || b.Badge.Tone != "caution" {
		t.Fatalf("ref thumbnail: %+v", b)
	}
	if c := got[7]; c.Icon != "" || c.IconColor != "" {
		t.Fatalf("new icon: %+v", c)
	}
	if m := got[8]; m.Icon != "" || m.Spark != nil {
		t.Fatalf("metric: %+v", m)
	}
	// version 2: a url picture is a line of text
	v = v6Example(t, "media")
	if _, err := Fit(v, page, 2); err != nil {
		t.Fatal(err)
	}
	if p := items(v)[3]; p.Type != "text" || p.Text != "一张照片" {
		t.Fatalf("v2 picture: %+v", p)
	}

	v = v6Example(t, "tiles")
	if _, err := Fit(v, page, 5); err != nil {
		t.Fatal(err)
	}
	got = items(v)
	if got[0].Type != "text" || got[0].Text != "摄像头" {
		t.Fatalf("carousel title: %+v", got[0])
	}
	if got[1].Type != "row" || got[1].Thumbnail != nil || got[1].Action == nil || got[2].Thumbnail == nil || got[2].Thumbnail.URL != "https://example.com/yard.jpg" {
		t.Fatalf("camera tiles: %+v %+v", got[1], got[2])
	}
	if home := got[3]; home.Badge == nil || home.Badge.Text != "开启" || home.Icon != "" {
		t.Fatalf("active scene: %+v", home)
	}
	// a tile's switch: its own action stays, the switch is a row under it
	living := got[5]
	if living.Title != "客厅" || living.Action == nil || living.Action.Push == nil || living.Badge.Text != "开启" || got[6].Title != "开关" ||
		got[6].Action.Values["room.living"] != "false" {
		t.Fatalf("room tile: %+v / %+v", living, got[6])
	}
	if bed := got[7]; bed.Title != "卧室" || bed.Badge.Text != "1" {
		t.Fatalf("bedroom: %+v", bed)
	}
}

// Fields for an older app, and what it sends back.
func TestFitFieldsV6(t *testing.T) {
	page, _ := Slot("nav_page")
	v := v6Example(t, "inputs")
	if _, err := Fit(v, page, 5); err != nil {
		t.Fatal(err)
	}
	got := items(v)
	if d := got[0]; d.Kind != "text" || d.Placeholder != "YYYY-MM-DD" || d.Value != "2026-11-06" || d.Earliest != "" {
		t.Fatalf("date: %+v", d)
	}
	if d := got[1]; d.Placeholder != "HH:MM" || d.Label != "时间（按你的时区）" || d.Detail != "" {
		t.Fatalf("time: %+v", d)
	}
	if d := got[2]; d.Placeholder != "YYYY-MM-DDTHH:MM" {
		t.Fatalf("datetime: %+v", d)
	}
	if got[3].Type != "text" || got[3].Text != "房间" {
		t.Fatalf("multichoice label: %+v", got[3])
	}
	toggles := got[4:8]
	values := map[string]string{"note": "x"}
	for _, f := range toggles {
		if f.Kind != "toggle" || !strings.HasPrefix(f.Name, "~rooms.") {
			t.Fatalf("toggle: %+v", f)
		}
		values[f.Name] = f.Value
	}
	if toggles[0].Label != "厨房" || toggles[0].Value != "true" || toggles[2].Value != "false" {
		t.Fatalf("toggles: %+v", toggles)
	}
	if m := got[8]; m.Kind != "text" || m.Value != "第一行\n第二行" {
		t.Fatalf("multiline: %+v", m)
	}
	// The app sends the toggles (the user enabled the living room too): the extension gets one JSON array.
	values[toggles[2].Name] = "true"
	out := CollectValues(values)
	if len(out) != 2 || out["note"] != "x" || out["rooms"] != `["bedroom","kitchen","living"]` {
		t.Fatalf("collected %v", out)
	}
	// none chosen is an empty array
	none := map[string]string{}
	for _, f := range toggles {
		none[f.Name] = "false"
	}
	if out := CollectValues(none); out["rooms"] != "[]" || len(out) != 1 {
		t.Fatalf("none: %v", out)
	}
	// a map without the hub's names is the same map
	plain := map[string]string{"a": "1"}
	if out := CollectValues(plain); len(out) != 1 || out["a"] != "1" {
		t.Fatal(out)
	}

	// more than 20 choices: one text field of values with commas
	var choices []UIChoice
	for i := range 30 {
		choices = append(choices, UIChoice{Value: fmt.Sprintf("c%d", i)})
	}
	many := &View{Sections: []Section{{Items: []UIElem{{Type: "field", Name: "tags", Label: "标签", Kind: "multichoice", Value: `["c1","c2"]`, Choices: choices}}}}}
	if _, err := Fit(many, page, 5); err != nil {
		t.Fatal(err)
	}
	f := items(many)[0]
	if f.Name != "~csv:tags" || f.Kind != "text" || f.Value != "c1, c2" || !strings.HasSuffix(f.Label, "（多个用逗号分隔）") {
		t.Fatalf("csv: %+v", f)
	}
	if out := CollectValues(map[string]string{f.Name: "c3， c1,,c3 、c9"}); out["tags"] != `["c1","c3","c9"]` {
		t.Fatalf("csv collected: %v", out)
	}
	// a name too long for the toggles is a text field too
	long := &View{Sections: []Section{{Items: []UIElem{{Type: "field", Name: strings.Repeat("n", 120), Label: "L", Kind: "multichoice", Choices: []UIChoice{{Value: "abcdefgh"}}}}}}}
	if _, err := Fit(long, page, 5); err != nil {
		t.Fatal(err)
	}
	if f := items(long)[0]; !strings.HasPrefix(f.Name, "~csv:") {
		t.Fatalf("long name: %+v", f)
	}
	// down to version 1, the fields still hold
	v = v6Example(t, "inputs")
	if _, err := Fit(v, page, 1); err != nil {
		t.Fatal(err)
	}
}

// A widget draws none of version 6: controls, actions and pictures are left out, a grid is rows.
func TestFieldItemsLegacyTextKinds(t *testing.T) {
	for _, kind := range []string{"", wire.FieldKindText, wire.FieldKindMultiline} {
		got := fieldItems(UIElem{Type: "field", Name: "content", Label: "SPACE.md", Kind: kind, Value: "first\nsecond"}, i18n.Default)
		if len(got) != 1 || got[0].Kind != "text" || got[0].Name != "content" || got[0].Value != "first\nsecond" {
			t.Fatalf("%q: %+v", kind, got)
		}
	}
}

func TestFitWidgetV6(t *testing.T) {
	for _, name := range []string{"controls", "tiles", "media", "charts"} {
		v := v6Example(t, name)
		if _, err := FitWidget(v, "large", 6); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, e := range items(v) {
			if e.Control != nil || len(e.Actions) > 0 || e.Action != nil || len(e.Tiles) > 0 || e.Type == "field" || e.Type == "button" || e.Src != nil {
				t.Errorf("%s: a widget gets %+v", name, e)
			}
		}
	}
}

// The actions a view has include its controls', its rows' extra ones and its tiles' (pushes are checked on them all).
func TestActionsV6(t *testing.T) {
	var ids []string
	for _, a := range Actions(v6Example(t, "controls")) {
		ids = append(ids, a.ID)
	}
	for _, id := range []string{"toggle", "rename", "remove", "brightness", "temperature", "open"} {
		if !slices.Contains(ids, id) {
			t.Errorf("no %s in %v", id, ids)
		}
	}
	ids = nil
	for _, a := range Actions(v6Example(t, "tiles")) {
		ids = append(ids, a.ID)
	}
	for _, id := range []string{"open_camera", "scene", "room_power", "open_room", "room_off"} {
		if !slices.Contains(ids, id) {
			t.Errorf("no %s in %v", id, ids)
		}
	}
}

// An assistant card has no ref pictures and no controls (#2017: nothing serves its pictures, nothing acts at once).
func TestAssistantViewV6(t *testing.T) {
	base := func(e UIElem) *View {
		return &View{Fallback: "x", Sections: []Section{{Items: []UIElem{e}}}}
	}
	if err := wire.CheckAssistantView(base(UIElem{Type: "image", Src: &ImageSource{URL: "https://example.com/a.png"}, Alt: "a"})); err != nil {
		t.Fatal(err)
	}
	for name, e := range map[string]UIElem{
		"ref":       {Type: "image", Src: &ImageSource{Ref: "a"}, Alt: "a"},
		"tile ref":  {Type: "grid", Tiles: []Tile{{Title: "t", Image: &ImageSource{Ref: "a"}}}},
		"control":   {Type: "row", Key: "k", Title: "t", Control: &RowControl{Kind: "switch", Name: "s", Action: UIAction{ID: "s", Reply: "开"}}},
		"thumb ref": {Type: "row", Title: "t", Thumbnail: &wire.Thumbnail{Ref: "a"}},
	} {
		if err := wire.CheckAssistantView(base(e)); err == nil {
			t.Errorf("%s taken", name)
		}
	}
}
