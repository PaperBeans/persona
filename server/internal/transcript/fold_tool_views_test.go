package transcript

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/proto"
)

func testSampleView(alt string) wire.View {
	lat, lon := 39.9, 116.3
	return wire.View{
		Fallback: alt,
		Sections: []wire.Section{{
			Items: []wire.Component{{
				Type:    wire.ComponentMap,
				Alt:     alt,
				Markers: []wire.MapMarker{{ID: "p1", Lat: &lat, Lon: &lon, Label: alt}},
			}},
		}},
	}
}

func TestFoldToolViewsCollectsDeduplicatesAndCaps(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	items := []proto.Item{
		{ID: "u1", Seq: 1, TS: ts, Role: "user", Kind: proto.KindText, Text: "route"},
		{ID: "t1", Seq: 2, TS: ts, Role: "assistant", Kind: proto.KindToolUse, ToolUseID: "call-1", ToolName: "mcp__persona__amap_route"},
		{ID: "r1", Seq: 3, TS: ts, Role: "user", Kind: proto.KindToolResult, ToolUseID: "call-1", Text: "ok",
			ToolViews: []wire.View{testSampleView("Route A"), testSampleView("Route B")}},
		{ID: "t2", Seq: 4, TS: ts, Role: "assistant", Kind: proto.KindToolUse, ToolUseID: "call-2", ToolName: "mcp__persona__amap_search"},
		{ID: "r2", Seq: 5, TS: ts, Role: "user", Kind: proto.KindToolResult, ToolUseID: "call-2", Text: "ok",
			ToolViews: []wire.View{testSampleView("POI 1")}},
		{ID: "th1", Seq: 6, TS: ts, Role: "assistant", Kind: proto.KindThinking, Text: "done"},
		{ID: "a1", Seq: 7, TS: ts, Role: "assistant", Kind: proto.KindText, Text: "Here is the route."},
	}
	folded := FoldWork(items)
	if len(folded) != 3 || folded[1].Kind != proto.KindFold || folded[1].Fold == nil {
		t.Fatalf("unexpected folded items: %+v", folded)
	}
	tv := folded[1].Fold.ToolViews
	if len(tv) != 3 {
		t.Fatalf("got %d fold tool views, want 3: %+v", len(tv), tv)
	}
	if tv[0].ItemID != "r1" || tv[0].ToolUseID != "call-1" || tv[0].Tool != "mcp__persona__amap_route" || tv[0].View.Fallback != "Route A" {
		t.Fatalf("unexpected first view: %+v", tv[0])
	}
	if tv[1].ItemID != "r1" || tv[1].View.Fallback != "Route B" {
		t.Fatalf("unexpected second view: %+v", tv[1])
	}
	if tv[2].ItemID != "r2" || tv[2].ToolUseID != "call-2" || tv[2].Tool != "mcp__persona__amap_search" || tv[2].View.Fallback != "POI 1" {
		t.Fatalf("unexpected third view: %+v", tv[2])
	}

	// Empty / absent tool views remain omitted on the wire.
	plain := FoldWork([]proto.Item{
		{ID: "u1", Seq: 1, TS: ts, Role: "user", Kind: proto.KindText, Text: "hi"},
		{ID: "t1", Seq: 2, TS: ts, Role: "assistant", Kind: proto.KindToolUse, ToolUseID: "call-1", ToolName: "Read"},
		{ID: "r1", Seq: 3, TS: ts, Role: "user", Kind: proto.KindToolResult, ToolUseID: "call-1", Text: "ok"},
		{ID: "th1", Seq: 4, TS: ts, Role: "assistant", Kind: proto.KindThinking, Text: "done"},
		{ID: "a1", Seq: 5, TS: ts, Role: "assistant", Kind: proto.KindText, Text: "hello"},
	})
	raw, _ := json.Marshal(plain[1].Fold)
	if plain[1].Fold.ToolViews != nil || strings.Contains(string(raw), "tool_views") {
		t.Fatalf("expected omitted tool_views when empty, got %s", raw)
	}

	// Cap at proto.FoldToolViewsMax and deduplicate identical items.
	var views []wire.View
	for i := range 10 {
		views = append(views, testSampleView(fmt.Sprintf("V%d", i)))
	}
	uses := map[string]proto.Item{"call-cap": {ToolUseID: "call-cap", ToolName: "amap_route"}}
	dupRun := []proto.Item{
		{ID: "r-cap", Kind: proto.KindToolResult, ToolUseID: "call-cap", ToolViews: views[:5]},
		{ID: "r-cap", Kind: proto.KindToolResult, ToolUseID: "call-cap", ToolViews: views[:5]},
		{ID: "r-more", Kind: proto.KindToolResult, ToolUseID: "call-cap", ToolViews: views[5:]},
	}
	capped := FoldToolViews(dupRun, uses)
	if len(capped) != proto.FoldToolViewsMax {
		t.Fatalf("got %d views after cap, want %d", len(capped), proto.FoldToolViewsMax)
	}
	if capped[0].View.Fallback != "V0" || capped[5].View.Fallback != "V5" {
		t.Fatalf("dedup/order broken: %+v", capped)
	}

	// Short unfolded runs keep ToolViews on the unfolded KindToolResult item directly.
	short := FoldWork([]proto.Item{
		{ID: "u1", Seq: 1, TS: ts, Role: "user", Kind: proto.KindText, Text: "hi"},
		{ID: "t1", Seq: 2, TS: ts, Role: "assistant", Kind: proto.KindToolUse, ToolUseID: "call-s", ToolName: "amap_route"},
		{ID: "r1", Seq: 3, TS: ts, Role: "user", Kind: proto.KindToolResult, ToolUseID: "call-s", Text: "ok", ToolViews: []wire.View{testSampleView("Short")}},
		{ID: "a1", Seq: 4, TS: ts, Role: "assistant", Kind: proto.KindText, Text: "done"},
	})
	if len(short) != 4 || short[2].Kind != proto.KindToolResult || len(short[2].ToolViews) != 1 || short[2].ToolViews[0].Fallback != "Short" {
		t.Fatalf("short run lost Item.ToolViews: %+v", short)
	}
}
