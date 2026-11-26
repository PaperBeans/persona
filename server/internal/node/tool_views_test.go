package node

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/nodeclient"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

func testMapView(key, title string) wire.View {
	lat1, lon1 := 39.9087, 116.3975
	lat2, lon2 := 39.9841, 116.3075
	dist, dur := 12400, 1500
	return wire.View{
		Title: title,
		Sections: []wire.Section{{
			Key: key + "-sec",
			Items: []wire.Component{{
				Type:  wire.ComponentMap,
				Key:   key,
				Title: title,
				Alt:   title + " route",
				Routes: []wire.MapRoute{{
					ID:              "r1",
					Label:           title,
					Mode:            wire.MapTravelModeDriving,
					DistanceMeters:  &dist,
					DurationSeconds: &dur,
					Points:          []wire.MapCoordinate{{Lat: &lat1, Lon: &lon1}, {Lat: &lat2, Lon: &lon2}},
				}},
				Markers: []wire.MapMarker{
					{ID: "orig", Kind: wire.MapMarkerKindOrigin, Label: "Start", Lat: &lat1, Lon: &lon1},
					{ID: "dest", Kind: wire.MapMarkerKindDestination, Label: "Dest", Lat: &lat2, Lon: &lon2},
				},
			}},
		}},
	}
}

func viewKey(v wire.View) string {
	if len(v.Sections) == 0 || len(v.Sections[0].Items) == 0 {
		return ""
	}
	return v.Sections[0].Items[0].Key
}

func TestToolViewsStrippedBeforeModelRuntimeAcrossAllCallPaths(t *testing.T) {
	launchKeyHome(t)
	if _, err := ensureLaunchKey(nodeclient.LaunchKeyPath()); err != nil {
		t.Fatal(err)
	}
	n, hub := runtimeNode(t)
	mapView := testMapView("v-route", "Drive Home")
	hub.mu.Lock()
	hub.answer = func(method string, params json.RawMessage) (any, error, bool) {
		if method == "mcp.call" {
			return proto.ToolResult{
				Content:   []proto.Content{{Type: "text", Text: "12.4 km, ~25 min"}},
				ToolViews: []wire.View{mapView},
			}, nil, true
		}
		return nil, nil, false
	}
	hub.mu.Unlock()

	dir, err := os.MkdirTemp("/tmp", "tvs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	ln, err := net.Listen("unix", filepath.Join(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go n.serveSock(ctx, c)
		}
	}()

	sockAsk := func(req map[string]any) string {
		t.Helper()
		c, err := net.Dial("unix", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		b, _ := json.Marshal(req)
		_, _ = c.Write(append(b, '\n'))
		line, err := bufio.NewReader(c).ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		return line
	}

	// 1. serveSock mcp.call for assistant + agent pane fallback: no "tool_views" crosses to MCP client.
	n.panes = map[string]string{"w1:p1": "agent-pane-sess"}
	for _, tc := range []struct {
		name string
		req  map[string]any
		sess string
	}{
		{"assistant_mcp", map[string]any{"kind": "mcp.call", "name": "amap_route", "args": map[string]any{}, "session_id": "asst-sess"}, "asst-sess"},
		{"agent_pane_mcp", map[string]any{"kind": "mcp.call", "name": "amap_route", "args": map[string]any{}, "pane": "w1:p1"}, "agent-pane-sess"},
	} {
		line := sockAsk(tc.req)
		if strings.Contains(line, `"tool_views"`) {
			t.Fatalf("%s leaked tool_views to model runtime: %s", tc.name, line)
		}
		if !n.toolViews.hasSession(tc.sess) {
			t.Fatalf("%s did not record pending views under %q", tc.name, tc.sess)
		}
	}

	// 2. cliCall (personad tool): requires local PID match on darwin/linux.
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		const cliSess = "cli-asst-sess"
		cwd := identity.AssistantDir(n.home)
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		n.procs[cliSess] = &assistantProc{id: cliSess, cwd: cwd, cmd: &exec.Cmd{Process: &os.Process{Pid: os.Getpid()}}}
		n.assistantPrepared = map[string]assistantPrepareState{assistantPrepareDir(cwd): {connectors: []string{"amap"}}}
		line := sockAsk(map[string]any{"kind": "cli.call", "name": "amap_route", "args": map[string]any{}})
		if strings.Contains(line, `"tool_views"`) {
			t.Fatalf("cli.call leaked tool_views to CLI caller: %s", line)
		}
		if !n.toolViews.hasSession(cliSess) {
			t.Fatalf("cli.call did not record pending views under %q", cliSess)
		}
	}

	// 3. scopedA2AMCP: ToolViews zeroed while tracked for session.
	provider, err := helloAt(t, n.runtimeSock, withInstance(dshHello(), "tv-provider"))
	if err != nil {
		t.Fatal(err)
	}
	const rtAgent = "rt-a2a-sess"
	if err := provider.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{{"session_id": rtAgent, "status": "idle"}}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "runtime session listed", func() bool { return listed(t, n)[rtAgent].SessionID == rtAgent })
	var ticket struct{ Token string }
	if err := provider.peer.Call(t.Context(), "runtime.a2a_mcp", map[string]string{"session_id": rtAgent}, &ticket); err != nil {
		t.Fatal(err)
	}
	a2aRes := n.scopedA2AMCP(t.Context(), sockRequest{
		Kind: "mcp.call", SessionID: rtAgent, A2AToken: ticket.Token, Name: "a2a_get", Args: json.RawMessage(`{}`),
	}).(proto.ToolResult)
	if len(a2aRes.ToolViews) != 0 {
		t.Fatalf("scopedA2AMCP leaked ToolViews: %+v", a2aRes.ToolViews)
	}
	if !n.toolViews.hasSession(rtAgent) {
		t.Fatalf("scopedA2AMCP did not record pending views under %q", rtAgent)
	}

	// 4. RuntimeHost.request mcp.call: raw JSON sent to runtime has no "tool_views".
	const rhSess = "rt-mcp-sess"
	if err := provider.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{{"session_id": rtAgent, "status": "idle"}, {"session_id": rhSess, "status": "idle"}}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "rhSess listed", func() bool { return listed(t, n)[rhSess].SessionID == rhSess })
	var rawOut json.RawMessage
	if err := provider.peer.Call(t.Context(), "mcp.call", map[string]any{"name": "amap_route", "session_id": rhSess, "args": map[string]any{}}, &rawOut); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawOut), `"tool_views"`) {
		t.Fatalf("RuntimeHost mcp.call leaked tool_views: %s", rawOut)
	}
	if !n.toolViews.hasSession(rhSess) {
		t.Fatalf("RuntimeHost mcp.call did not record pending views under %q", rhSess)
	}

	// Nil receiver / empty session still strips raw tool_views defensively (Super Rule 2).
	var nilTracker *toolViewTracker
	stripped := nilTracker.stripRaw("", "amap_route", json.RawMessage(`{"content":[{"type":"text","text":"ok"}],"tool_views":[{"title":"x","sections":[]}]}`))
	if strings.Contains(string(stripped), `"tool_views"`) {
		t.Fatalf("stripRaw left tool_views intact: %s", stripped)
	}
}

func TestToolViewsAttachCorrelateAcrossTranscriptPagesFoldsAndWatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s-tv.jsonl")
	t0 := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

	claudeLine := func(uuid string, sec int, msg map[string]any) string {
		b, _ := json.Marshal(map[string]any{
			"type":      msg["role"],
			"uuid":      uuid,
			"timestamp": t0.Add(time.Duration(sec) * time.Second).Format(time.RFC3339Nano),
			"message":   msg,
		})
		return string(b)
	}
	initial := strings.Join([]string{
		claudeLine("u0", 1, map[string]any{"role": "user", "content": "old"}),
		claudeLine("tu0", 2, map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "id0", "name": "mcp__persona__amap_search", "input": map[string]any{"q": "coffee"}}}}),
		claudeLine("tr0", 3, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "id0", "content": "Cafe A"}}}),
		claudeLine("a0", 4, map[string]any{"role": "assistant", "content": "Here is Cafe A"}),
		claudeLine("u1", 10, map[string]any{"role": "user", "content": "drive home"}),
		claudeLine("tu1", 11, map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "id1", "name": "mcp__persona__amap_route", "input": map[string]any{"to": "home"}}}}),
		claudeLine("tr1", 12, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "id1", "content": "Route 1"}}}),
		claudeLine("tu2", 13, map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "id2", "name": "Bash", "input": map[string]any{"command": "personad tool --cli amap direction walking"}}}}),
		claudeLine("tr2", 14, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "id2", "content": "Walk 1"}}}),
		claudeLine("tu3", 15, map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "id3", "name": "Read", "input": map[string]any{"file_path": "README.md"}}}}),
		claudeLine("tr3", 16, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "id3", "content": "readme"}}}),
		claudeLine("a1", 17, map[string]any{"role": "assistant", "content": "All set"}),
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	const sid = "s-tv"
	n := &Node{home: dir, paths: map[string]string{}, files: map[string]*transcript.File{sid: transcript.NewFile(path)},
		procs: map[string]*assistantProc{}, agents: map[string]host.Agent{}, state: &stateFile{Sessions: map[string]sessionState{}}}

	// Queue two views (route + cli), then verify BeforeSeq > 0 (allowBind=false) never steals them onto old history!
	routeRes := proto.ToolResult{ToolViews: []wire.View{testMapView("v-drive", "Drive")}}
	cliRes := proto.ToolResult{ToolViews: []wire.View{testMapView("v-walk", "Walk")}}
	n.toolViews.stripResult(sid, "amap_route", &routeRes)
	n.toolViews.stripResult(sid, "amap_cli", &cliRes)
	if len(routeRes.ToolViews) != 0 || len(cliRes.ToolViews) != 0 {
		t.Fatal("stripResult did not zero ToolViews")
	}

	callItems := func(req map[string]any) struct {
		Items []proto.Item `json:"items"`
	} {
		t.Helper()
		b, _ := json.Marshal(req)
		out, err := n.handle(context.Background(), "session.items", b)
		if err != nil {
			t.Fatal(err)
		}
		enc, _ := json.Marshal(out)
		var res struct {
			Items []proto.Item `json:"items"`
		}
		if err := json.Unmarshal(enc, &res); err != nil {
			t.Fatal(err)
		}
		return res
	}

	hist := callItems(map[string]any{"session_id": sid, "before_seq": 5, "limit": 20})
	for _, it := range hist.Items {
		if len(it.ToolViews) != 0 {
			t.Fatalf("historical page stole pending tool_views onto %+v", it)
		}
	}

	// Latest folded page: Fold.ToolViews carries both views in order and preserves them across reopen/ThroughSeq & session.item.
	latest := callItems(map[string]any{"session_id": sid, "turns": 1, "fold_work": true})
	var foldItem *proto.Item
	for i := range latest.Items {
		if latest.Items[i].Kind == proto.KindFold {
			foldItem = &latest.Items[i]
			break
		}
	}
	if foldItem == nil || foldItem.Fold == nil || len(foldItem.Fold.ToolViews) != 2 {
		t.Fatalf("expected fold item with 2 ToolViews, got %+v", latest.Items)
	}
	if viewKey(foldItem.Fold.ToolViews[0].View) != "v-drive" || foldItem.Fold.ToolViews[0].Tool != "mcp__persona__amap_route" {
		t.Fatalf("unexpected first FoldToolView: %+v", foldItem.Fold.ToolViews[0])
	}
	if viewKey(foldItem.Fold.ToolViews[1].View) != "v-walk" || foldItem.Fold.ToolViews[1].Tool != "Bash" {
		t.Fatalf("unexpected second FoldToolView: %+v", foldItem.Fold.ToolViews[1])
	}

	// Expand the fold (through_seq): bound Item.ToolViews come back on tr1 and tr2.
	opened := callItems(map[string]any{"session_id": sid, "from_seq": foldItem.Seq, "through_seq": foldItem.Fold.ThroughSeq})
	foundByViewKey := map[string]string{}
	for _, it := range opened.Items {
		if len(it.ToolViews) > 0 {
			foundByViewKey[viewKey(it.ToolViews[0])] = it.ID
		}
	}
	if foundByViewKey["v-drive"] == "" || foundByViewKey["v-walk"] == "" {
		t.Fatalf("opening fold lost bound Item.ToolViews: %+v", foundByViewKey)
	}

	// session.item (single-item full read) preserves bound Item.ToolViews too.
	singleRaw, err := n.handle(context.Background(), "session.item", json.RawMessage(`{"session_id":"`+sid+`","item_id":"`+foundByViewKey["v-drive"]+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	single := singleRaw.(proto.Item)
	if len(single.ToolViews) != 1 || viewKey(single.ToolViews[0]) != "v-drive" {
		t.Fatalf("session.item lost ToolViews: %+v", single.ToolViews)
	}
}

func TestToolViewsWatchPushSplitBatchesAndSizeAccounting(t *testing.T) {
	n, hub := runtimeNode(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "s-push.jsonl")
	claudeLine := func(uuid, role string, content any) string {
		b, _ := json.Marshal(map[string]any{
			"type":      role,
			"uuid":      uuid,
			"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
			"message":   map[string]any{"role": role, "content": content},
		})
		return string(b)
	}
	initial := claudeLine("u0", "user", "hi") + "\n"
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	const sid = "s-push"
	f := transcript.NewFile(path)
	n.files[sid] = f
	if _, err := f.Refresh(); err != nil {
		t.Fatal(err)
	}

	// Batch 1: only KindToolUse is written and pushed BEFORE mcp.call completes.
	fAppend := func(line string) {
		t.Helper()
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		_, err = file.WriteString(line + "\n")
		_ = file.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	fAppend(claudeLine("tu1", "assistant", []any{map[string]any{"type": "tool_use", "id": "split-tu", "name": "mcp__persona__amap_route", "input": map[string]any{}}}))
	items1, span1, err := f.RefreshSpan()
	if err != nil || len(items1) != 1 {
		t.Fatalf("refresh1 = %+v, %v", items1, err)
	}
	n.pushItems(sid, items1, span1)

	// Tool finishes now and returns ToolViews; next watch batch carries only KindToolResult.
	tr := proto.ToolResult{ToolViews: []wire.View{testMapView("v-split", "Split Push")}}
	n.toolViews.stripResult(sid, "amap_route", &tr)
	fAppend(claudeLine("tr1", "user", []any{map[string]any{"type": "tool_result", "tool_use_id": "split-tu", "content": "Done"}}))
	items2, span2, err := f.RefreshSpan()
	if err != nil || len(items2) != 1 {
		t.Fatalf("refresh2 = %+v, %v", items2, err)
	}
	n.pushItems(sid, items2, span2)

	pushedItems := func(sessionID string) []proto.Item {
		hub.mu.Lock()
		raws := append([]json.RawMessage(nil), hub.items...)
		hub.mu.Unlock()
		var out []proto.Item
		for _, raw := range raws {
			var batch struct {
				SessionID string       `json:"session_id"`
				Items     []proto.Item `json:"items"`
			}
			if json.Unmarshal(raw, &batch) == nil && batch.SessionID == sessionID {
				out = append(out, batch.Items...)
			}
		}
		return out
	}

	waitFor(t, "push delivers attached tool_views", func() bool {
		for _, it := range pushedItems(sid) {
			if it.Kind == proto.KindToolResult && len(it.ToolViews) == 1 && viewKey(it.ToolViews[0]) == "v-split" {
				return true
			}
		}
		return false
	})

	// itemSize accounts for ToolViews so pushParts splits oversized tool-view batches.
	oldMax := pushBytesMax
	pushBytesMax = 600
	t.Cleanup(func() { pushBytesMax = oldMax })
	vBig := testMapView("v-big", strings.Repeat("route ", 60))
	parts := pushParts([]proto.Item{
		{ID: "i1#0", Seq: 1, Kind: proto.KindToolResult, ToolViews: []wire.View{vBig}},
		{ID: "i2#0", Seq: 2, Kind: proto.KindText, Text: "after"},
	}, transcript.Span{History: "h", From: 0, To: 2})
	if len(parts) != 2 {
		t.Fatalf("expected pushParts to split oversized ToolViews batch into 2 parts, got %d", len(parts))
	}
}

func TestToolViewsRuntimeEventsCallsAndAgyPane(t *testing.T) {
	n, hub := runtimeNode(t)
	rt, err := helloAt(t, n.runtimeSock, dshHello())
	if err != nil {
		t.Fatal(err)
	}
	const sid = "rt-live"
	rt.answer = func(method string, params json.RawMessage) (any, error) {
		switch method {
		case "session.items":
			return map[string]any{
				"items": []proto.Item{
					{ID: "rt-th#0", Seq: 1, Role: "assistant", Kind: proto.KindThinking, Text: "Planning route"},
					{ID: "rt-tu#0", Seq: 2, Role: "assistant", Kind: proto.KindToolUse, ToolUseID: "rt-1", ToolName: "mcp__persona__amap_route"},
					{ID: "rt-tr#0", Seq: 3, Role: "user", Kind: proto.KindToolResult, ToolUseID: "rt-1", Text: "10km"},
					{ID: "rt-a#0", Seq: 4, Role: "assistant", Kind: proto.KindText, Text: "Ready"},
				},
				"has_more": false,
			}, nil
		case "session.item":
			return proto.Item{ID: "rt-tr#0", Seq: 3, Role: "user", Kind: proto.KindToolResult, ToolUseID: "rt-1", Text: "10km"}, nil
		}
		return nil, nil
	}
	if err := rt.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{{"session_id": sid, "status": "idle"}}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "rt-live listed", func() bool { return listed(t, n)[sid].SessionID == sid })

	// Runtime events: split session.items notifications + stripResult on runtime mcp.call.
	tr := proto.ToolResult{ToolViews: []wire.View{testMapView("v-rt-event", "Runtime Event")}}
	n.toolViews.stripResult(sid, "amap_route", &tr)
	if err := rt.peer.Notify("session.items", map[string]any{
		"session_id": sid, "from_seq": 0, "to_seq": 1,
		"items": []proto.Item{{ID: "ev-tu#0", Seq: 1, Role: "assistant", Kind: proto.KindToolUse, ToolUseID: "ev-1", ToolName: "amap_route"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := rt.peer.Notify("session.items", map[string]any{
		"session_id": sid, "from_seq": 1, "to_seq": 2,
		"items": []proto.Item{{ID: "ev-tr#0", Seq: 2, Role: "user", Kind: proto.KindToolResult, ToolUseID: "ev-1", Text: "ok"}},
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "runtime event attached tool_views", func() bool {
		hub.mu.Lock()
		raws := append([]json.RawMessage(nil), hub.items...)
		hub.mu.Unlock()
		for _, raw := range raws {
			var batch struct {
				SessionID string       `json:"session_id"`
				Items     []proto.Item `json:"items"`
			}
			if json.Unmarshal(raw, &batch) != nil || batch.SessionID != sid {
				continue
			}
			for _, it := range batch.Items {
				if it.ID == "ev-tr#0" && len(it.ToolViews) == 1 && viewKey(it.ToolViews[0]) == "v-rt-event" {
					return true
				}
			}
		}
		return false
	})

	// Runtime RPC session.items (folded) + session.item through n.handle -> runtimeHandle.
	trCall := proto.ToolResult{ToolViews: []wire.View{testMapView("v-rt-call", "Runtime Call")}}
	n.toolViews.stripResult(sid, "amap_route", &trCall)
	res, err := n.handle(t.Context(), "session.items", json.RawMessage(`{"session_id":"`+sid+`","fold_work":true}`))
	if err != nil {
		t.Fatalf("handle session.items: %v", err)
	}
	enc, _ := json.Marshal(res)
	var folded struct {
		Items []proto.Item `json:"items"`
	}
	if err := json.Unmarshal(enc, &folded); err != nil {
		t.Fatal(err)
	}
	if len(folded.Items) == 0 || folded.Items[0].Fold == nil || len(folded.Items[0].Fold.ToolViews) != 1 || viewKey(folded.Items[0].Fold.ToolViews[0].View) != "v-rt-call" {
		t.Fatalf("runtime folded page missing Fold.ToolViews: %+v", folded.Items)
	}
	singleRes, err := n.handle(t.Context(), "session.item", json.RawMessage(`{"session_id":"`+sid+`","item_id":"rt-tr#0"}`))
	if err != nil {
		t.Fatalf("handle session.item: %v", err)
	}
	singleRaw, _ := json.Marshal(singleRes)
	var single proto.Item
	if err := json.Unmarshal(singleRaw, &single); err != nil || len(single.ToolViews) != 1 || viewKey(single.ToolViews[0]) != "v-rt-call" {
		t.Fatalf("runtime session.item missing bound ToolViews: %s", singleRaw)
	}

	// agypane: Antigravity single-underscore MCP name canonicalization + fold + through_seq.
	var agyTV toolViewTracker
	agyRes := proto.ToolResult{ToolViews: []wire.View{testMapView("v-agy", "Agy Route")}}
	agyTV.stripResult("agy-sess", "amap_route", &agyRes)
	h := agyHistory{
		token: "agy-pane:conv1",
		seq:   5,
		items: []proto.Item{
			{ID: "s1#0", Seq: 1, Role: "user", Kind: proto.KindText, Text: "go"},
			{ID: "s2#0", Seq: 2, Role: "assistant", Kind: proto.KindThinking, Text: "Planning route"},
			{ID: "s3#0", Seq: 3, Role: "assistant", Kind: proto.KindToolUse, ToolUseID: "agy-tu", ToolName: "mcp_persona_deadbeef_amap_route"},
			{ID: "s4#0", Seq: 4, Role: "user", Kind: proto.KindToolResult, ToolUseID: "agy-tu", Text: "12km"},
			{ID: "s5#0", Seq: 5, Role: "assistant", Kind: proto.KindText, Text: "Done"},
		},
	}
	pageEnc, _ := json.Marshal(h.pageWith(agyPageParams{Turns: 1, FoldWork: true}, "agy-sess", &agyTV))
	var agyFolded struct {
		Items []proto.Item `json:"items"`
	}
	if err := json.Unmarshal(pageEnc, &agyFolded); err != nil {
		t.Fatal(err)
	}
	if len(agyFolded.Items) != 3 || agyFolded.Items[1].Fold == nil || len(agyFolded.Items[1].Fold.ToolViews) != 1 || viewKey(agyFolded.Items[1].Fold.ToolViews[0].View) != "v-agy" {
		t.Fatalf("agypane Fold.ToolViews missing: %+v", agyFolded.Items)
	}
	reopenEnc, _ := json.Marshal(h.pageWith(agyPageParams{FromSeq: 2, ThroughSeq: 4}, "agy-sess", &agyTV))
	if !strings.Contains(string(reopenEnc), `"v-agy"`) {
		t.Fatalf("agypane ThroughSeq lost bound tool_views: %s", reopenEnc)
	}
}
