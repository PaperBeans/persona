package node

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestRuntimeHelloAdvertisesAssistantCommandGate(t *testing.T) {
	t.Run("local assistant_environment runtime gets assistant_command_gate", func(t *testing.T) {
		n, _ := runtimeNode(t)
		hello := assistantRuntimeHello()
		hello["caps"].(map[string]any)["assistant_environment"] = true
		host := dialRuntime(t, n, hello)
		features, _ := host.helloR["features"].([]any)
		if !slices.Contains(features, any("assistant_command_gate")) {
			t.Fatalf("assistant_command_gate not advertised: %v", features)
		}
	})

	t.Run("local assistant runtime without assistant_environment omits assistant_command_gate", func(t *testing.T) {
		n, _ := runtimeNode(t)
		host := dialRuntime(t, n, assistantRuntimeHello())
		features, _ := host.helloR["features"].([]any)
		if slices.Contains(features, any("assistant_command_gate")) {
			t.Fatalf("runtime without assistant_environment unexpectedly got assistant_command_gate: %v", features)
		}
	})

	t.Run("remote_agent assistant runtime omits assistant_command_gate", func(t *testing.T) {
		n, _ := runtimeNode(t)
		hello := assistantRuntimeHello()
		hello["caps"].(map[string]any)["assistant_environment"] = true
		hello["caps"].(map[string]any)["remote_agent"] = true
		host := dialRuntime(t, n, hello)
		features, _ := host.helloR["features"].([]any)
		if slices.Contains(features, any("assistant_command_gate")) {
			t.Fatalf("remote_agent runtime unexpectedly got assistant_command_gate: %v", features)
		}
	})

	t.Run("local non-assistant runtime omits assistant_command_gate", func(t *testing.T) {
		n, _ := runtimeNode(t)
		host := dialRuntime(t, n, dshHello())
		features, _ := host.helloR["features"].([]any)
		if slices.Contains(features, any("assistant_command_gate")) {
			t.Fatalf("non-assistant runtime unexpectedly got assistant_command_gate: %v", features)
		}
	})

	t.Run("daemon assistant runtime omits assistant_command_gate", func(t *testing.T) {
		n, _ := runtimeNode(t)
		clientConn, serverConn := net.Pipe()
		t.Cleanup(func() {
			_ = clientConn.Close()
			_ = serverConn.Close()
		})
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		go n.runtimeHost.serveConn(ctx, serverConn, "remote-daemon")

		peer := rpc.New("t", clientConn)
		go func() { _ = peer.Serve(ctx, clientConn) }()
		t.Cleanup(func() { peer.Close(nil) })

		daemonHello := assistantRuntimeHello()
		daemonHello["runtime"] = "remote-daemon"
		daemonHello["caps"].(map[string]any)["assistant_environment"] = true
		var helloR map[string]any
		callCtx, callCancel := context.WithTimeout(ctx, 5*time.Second)
		defer callCancel()
		if err := peer.Call(callCtx, "runtime.hello", daemonHello, &helloR); err != nil {
			t.Fatal(err)
		}
		features, _ := helloR["features"].([]any)
		if slices.Contains(features, any("assistant_command_gate")) {
			t.Fatalf("daemon runtime unexpectedly got assistant_command_gate: %v", features)
		}
	})
}

func TestRuntimeAssistantBashRoutesThroughLarkGateAndGrantsProxy(t *testing.T) {
	api := newFakeAPI(t)
	n, hub := runtimeNode(t)
	t.Cleanup(func() { n.gates().close() })
	assistantHub(hub)
	cwd := prepareRuntimeAssistantEnvironment(t, n, true)
	n.mu.Lock()
	n.larkCfg.Gates[0].Env["EXAMPLE_BASE"] = api.URL
	n.larkCfg.Gates[0].Rules = []proto.LarkRule{
		{Method: "GET", Path: "/**", Action: "allow"},
		{Method: "DELETE", Path: "/forbidden", Action: "deny"},
	}
	n.mu.Unlock()

	hello := assistantRuntimeHello()
	hello["caps"].(map[string]any)["assistant_environment"] = true
	host := dialRuntime(t, n, hello)
	sid := startedAssistant(t, n, host)
	startEnv := runtimeAssistantEnvironment(t, host.got("session.start")[0])
	base := startEnv["EXAMPLE_BASE"]
	if !gateProxyURLRe.MatchString(base) {
		t.Fatalf("EXAMPLE_BASE = %q, want local gate proxy address", base)
	}

	// Register a regular (non-assistant) session on the same runtime to verify isolation.
	regularDir := t.TempDir()
	if err := host.peer.Notify("runtime.sessions", map[string]any{"sessions": []proto.Session{
		{SessionID: sid, Title: "Main Assistant", Cwd: cwd, Status: proto.StatusIdle},
		{SessionID: "session-regular", Title: "Regular Agent", Cwd: regularDir, Status: proto.StatusIdle},
	}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "both sessions listed", func() bool {
		_, _, a := n.runtimes.rowOf(sid)
		_, _, b := n.runtimes.rowOf("session-regular")
		return a && b
	})

	resetAsked := func() {
		hub.mu.Lock()
		hub.asked = nil
		hub.mu.Unlock()
	}
	askedSnapshot := func() []proto.Interaction {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		return append([]proto.Interaction(nil), hub.asked...)
	}
	askBash := func(session, toolName, toolUseID, cmd, cmdCwd string) (proto.Resolution, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ix := map[string]any{
			"kind":        "permission",
			"session_id":  session,
			"tool_name":   toolName,
			"tool_use_id": toolUseID,
			"tool_input":  map[string]any{"command": cmd},
			"summary":     cmd,
		}
		if cmdCwd != "" {
			ix["cwd"] = cmdCwd
		}
		var res proto.Resolution
		err := host.peer.Call(ctx, "interaction.create", map[string]any{"interaction": ix}, &res)
		return res, err
	}

	t.Run("classified read auto-allows without raising a card", func(t *testing.T) {
		resetAsked()
		res, err := askBash(sid, "Bash", "call-read", "ex GET /me", "")
		if err != nil {
			t.Fatal(err)
		}
		if res.Allow == nil || !*res.Allow {
			t.Fatalf("read resolution = %+v, want allow=true", res)
		}
		if asked := askedSnapshot(); len(asked) != 0 {
			t.Fatalf("classified read raised %d card(s): %+v", len(asked), asked)
		}
	})

	t.Run("proxied write confirms on phone and issues one-use proxy grant", func(t *testing.T) {
		n.noteModes(sid, proto.ModeDefault, "")
		resetAsked()
		if code, _ := send(t, "POST", base+"/api/v1/conversations/c1/messages", `{"text":"hello"}`); code != http.StatusForbidden {
			t.Fatalf("unapproved write = %d, want 403", code)
		}
		allow := true
		hub.mu.Lock()
		hub.resolve = func(ix proto.Interaction) proto.Resolution {
			return proto.Resolution{Allow: &allow}
		}
		hub.mu.Unlock()

		res, err := askBash(sid, "Bash", "call-write", `ex POST /conversations/c1/messages '{"text":"hello"}'`, "")
		if err != nil {
			t.Fatal(err)
		}
		if res.Allow == nil || !*res.Allow || res.Always {
			t.Fatalf("write resolution = %+v, want allow=true always=false", res)
		}
		asked := askedSnapshot()
		if len(asked) != 1 {
			t.Fatalf("expected 1 confirmation card, got %d", len(asked))
		}
		card := asked[0]
		if card.ToolName != "示例" {
			t.Fatalf("card.ToolName = %q, want gate title %q", card.ToolName, "示例")
		}
		if card.Agent != "" || card.AgentLabel != "" {
			t.Fatalf("gate card kept runtime agent metadata: agent=%q label=%q", card.Agent, card.AgentLabel)
		}
		if !card.OutwardWrite {
			t.Fatalf("expected OutwardWrite=true on send card: %+v", card)
		}
		if !card.Resumable {
			t.Fatalf("expected Resumable=true on runtime gate card")
		}
		if card.Cwd != cwd {
			t.Fatalf("card.Cwd = %q, want %q", card.Cwd, cwd)
		}
		if !slices.Contains(card.AlwaysKeys, "lark:POST /conversations/*/messages") {
			t.Fatalf("card.AlwaysKeys = %v, want lark route key", card.AlwaysKeys)
		}

		// The one-use grant lets this exact request through once, then blocks replay.
		if code, body := send(t, "POST", base+"/api/v1/conversations/c1/messages", `{"text":"hello"}`); code != http.StatusOK || body != `{"ok":true}` {
			t.Fatalf("approved proxied write = %d %s, want 200", code, body)
		}
		if code, _ := send(t, "POST", base+"/api/v1/conversations/c1/messages", `{"text":"hello"}`); code != http.StatusForbidden {
			t.Fatalf("replayed proxied write = %d, want 403", code)
		}
		reqs := api.requests()
		last := reqs[len(reqs)-1]
		if last.Method != "POST" || last.Path != "/api/v1/conversations/c1/messages" || last.Auth != "Bearer fake-credential-handle=with-equals" {
			t.Fatalf("unexpected upstream request: %+v", last)
		}
	})

	t.Run("phone denial and configured deny rules return allow=false with no grant", func(t *testing.T) {
		resetAsked()
		allow := false
		hub.mu.Lock()
		hub.resolve = func(ix proto.Interaction) proto.Resolution {
			return proto.Resolution{Allow: &allow, Message: "do not send"}
		}
		hub.mu.Unlock()

		res, err := askBash(sid, "Bash", "call-deny-phone", `ex POST /conversations/c1/messages '{"text":"nope"}'`, "")
		if err != nil {
			t.Fatal(err)
		}
		if res.Allow == nil || *res.Allow {
			t.Fatalf("phone deny resolution = %+v, want allow=false", res)
		}
		if !strings.Contains(res.Message, "do not send") {
			t.Fatalf("deny message = %q, want user reason", res.Message)
		}
		if code, _ := send(t, "POST", base+"/api/v1/conversations/c1/messages", `{"text":"nope"}`); code != http.StatusForbidden {
			t.Fatalf("denied write still reached proxy: %d", code)
		}

		resetAsked()
		res, err = askBash(sid, "Bash", "call-deny-rule", `ex DELETE /forbidden`, "")
		if err != nil {
			t.Fatal(err)
		}
		if res.Allow == nil || *res.Allow {
			t.Fatalf("rule deny resolution = %+v, want allow=false", res)
		}
		if asked := askedSnapshot(); len(asked) != 0 {
			t.Fatalf("rule deny should not raise a card, got %d", len(asked))
		}
	})

	t.Run("bypassPermissions auto-allows non-gated commands but still gates outbound writes and fails closed on empty command", func(t *testing.T) {
		n.noteModes(sid, proto.ModeBypass, "")
		t.Cleanup(func() { n.noteModes(sid, proto.ModeDefault, "") })

		resetAsked()
		res, err := askBash(sid, "Bash", "call-bypass-ls", "ls -la", "")
		if err != nil {
			t.Fatal(err)
		}
		if res.Allow == nil || !*res.Allow {
			t.Fatalf("bypass non-gated command = %+v, want allow=true", res)
		}
		if asked := askedSnapshot(); len(asked) != 0 {
			t.Fatalf("bypass non-gated command raised card: %+v", asked)
		}

		// Outbound send still asks even in bypassPermissions and grants the proxy token on approval.
		resetAsked()
		allow := true
		hub.mu.Lock()
		hub.resolve = func(ix proto.Interaction) proto.Resolution { return proto.Resolution{Allow: &allow} }
		hub.mu.Unlock()
		res, err = askBash(sid, "Bash", "call-bypass-send", `ex POST /conversations/c1/messages '{"text":"bypass-confirmed"}'`, "")
		if err != nil {
			t.Fatal(err)
		}
		if res.Allow == nil || !*res.Allow {
			t.Fatalf("bypass send resolution = %+v, want allow=true", res)
		}
		if asked := askedSnapshot(); len(asked) != 1 || asked[0].ToolName != "示例" || !asked[0].OutwardWrite {
			t.Fatalf("bypass send cards = %+v, want 1 示例 OutwardWrite card", asked)
		}
		if code, _ := send(t, "POST", base+"/api/v1/conversations/c1/messages", `{"text":"bypass-confirmed"}`); code != http.StatusOK {
			t.Fatalf("bypass-approved write = %d, want 200", code)
		}

		// Empty and truncated Bash commands fail closed to the phone instead of bypassing uninspected.
		for _, tc := range []struct {
			id, cmd string
		}{
			{"call-bypass-empty", "   "},
			{"call-bypass-truncated", "echo " + strings.Repeat("a", 64) + "\n…（已截断，共 30000 字节）"},
		} {
			resetAsked()
			res, err = askBash(sid, "Bash", tc.id, tc.cmd, "")
			if err != nil {
				t.Fatal(err)
			}
			if asked := askedSnapshot(); len(asked) != 1 || asked[0].ToolName != "Bash" {
				t.Fatalf("%s in bypass should fail closed to Bash card, got %+v", tc.id, asked)
			}
		}

		// A runtime reporting a concrete non-bypass mode (e.g. plan) is never silently bypassed by a stale node mode.
		if err := host.peer.Notify("runtime.sessions", map[string]any{"sessions": []proto.Session{
			{SessionID: sid, Title: "Main Assistant", Cwd: cwd, Status: proto.StatusIdle, PermissionMode: "plan"},
			{SessionID: "session-regular", Title: "Regular Agent", Cwd: regularDir, Status: proto.StatusIdle},
		}}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "row permission_mode plan updated", func() bool {
			row, _, ok := n.runtimes.rowOf(sid)
			return ok && row.PermissionMode == "plan"
		})
		resetAsked()
		res, err = askBash(sid, "Bash", "call-bypass-plan-drift", "ls -la", "")
		if err != nil {
			t.Fatal(err)
		}
		if asked := askedSnapshot(); len(asked) != 1 || asked[0].ToolName != "Bash" {
			t.Fatalf("stale node bypass must not override runtime plan mode, got %+v", asked)
		}
		_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": []proto.Session{
			{SessionID: sid, Title: "Main Assistant", Cwd: cwd, Status: proto.StatusIdle},
			{SessionID: "session-regular", Title: "Regular Agent", Cwd: regularDir, Status: proto.StatusIdle},
		}})
		waitFor(t, "row permission_mode reset", func() bool {
			row, _, ok := n.runtimes.rowOf(sid)
			return ok && row.PermissionMode == ""
		})
	})

	t.Run("settings-driven row bypass auto-allows non-gated Bash when node mode is unset without overriding explicit default or weakening larkCheck", func(t *testing.T) {
		if err := host.peer.Notify("runtime.sessions", map[string]any{"sessions": []proto.Session{
			{SessionID: sid, Title: "Main Assistant", Cwd: cwd, Status: proto.StatusIdle, PermissionMode: proto.ModeBypass},
			{SessionID: "session-regular", Title: "Regular Agent", Cwd: regularDir, Status: proto.StatusIdle},
		}}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "row permission_mode bypass updated", func() bool {
			row, _, ok := n.runtimes.rowOf(sid)
			return ok && row.PermissionMode == proto.ModeBypass
		})
		t.Cleanup(func() {
			_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": []proto.Session{
				{SessionID: sid, Title: "Main Assistant", Cwd: cwd, Status: proto.StatusIdle},
				{SessionID: "session-regular", Title: "Regular Agent", Cwd: regularDir, Status: proto.StatusIdle},
			}})
		})

		// Explicit node mode "default" still takes precedence over a row-reported bypass mode.
		n.noteModes(sid, proto.ModeDefault, "")
		allow := true
		hub.mu.Lock()
		hub.resolve = func(ix proto.Interaction) proto.Resolution { return proto.Resolution{Allow: &allow} }
		hub.mu.Unlock()
		resetAsked()
		res, err := askBash(sid, "Bash", "call-explicit-default-ls", "ls -la", "")
		if err != nil {
			t.Fatal(err)
		}
		if res.Allow == nil || !*res.Allow {
			t.Fatalf("explicit default non-gated command = %+v", res)
		}
		if asked := askedSnapshot(); len(asked) != 1 || asked[0].ToolName != "Bash" {
			t.Fatalf("explicit default mode must not be bypassed by row mode, got %+v", asked)
		}

		// Clear both live and persisted mode so node mode is genuinely unset.
		n.mu.Lock()
		delete(n.modes, sid)
		n.mu.Unlock()
		if st, ok := n.state.get(sid); ok {
			st.AppMode = ""
			n.state.put(sid, st)
		}
		resetAsked()
		res, err = askBash(sid, "Bash", "call-row-bypass-ls", "ls -la", "")
		if err != nil {
			t.Fatal(err)
		}
		if res.Allow == nil || !*res.Allow {
			t.Fatalf("row bypass non-gated command = %+v, want allow=true", res)
		}
		if asked := askedSnapshot(); len(asked) != 0 {
			t.Fatalf("row bypass non-gated command raised card: %+v", asked)
		}

		// Even an ordinary (non-outbound) gated write still confirms because larkCheck uses n.modesOf, not row mode.
		resetAsked()
		res, err = askBash(sid, "Bash", "call-row-bypass-write", `ex POST /notes '{"title":"draft"}'`, "")
		if err != nil {
			t.Fatal(err)
		}
		if res.Allow == nil || !*res.Allow {
			t.Fatalf("row bypass gated write = %+v", res)
		}
		if asked := askedSnapshot(); len(asked) != 1 || asked[0].ToolName != "示例" {
			t.Fatalf("row bypass must not weaken larkCheck for gated writes, got %+v", asked)
		}

		// Auto-mode refusal fallback to bypassPermissions auto-allows non-gated ls while still confirming gated writes.
		n.noteModes(sid, "auto", "")
		resetAsked()
		res, err = askBash(sid, "Bash", "call-auto-fallback-ls", "ls -la", "")
		if err != nil {
			t.Fatal(err)
		}
		if res.Allow == nil || !*res.Allow {
			t.Fatalf("auto-fallback non-gated command = %+v, want allow=true", res)
		}
		if asked := askedSnapshot(); len(asked) != 0 {
			t.Fatalf("auto-fallback non-gated command raised card: %+v", asked)
		}
	})

	t.Run("per-call cwd resolves relative @file bodies and enforces space confirmation", func(t *testing.T) {
		subDir := filepath.Join(cwd, "sub")
		if err := os.MkdirAll(subDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(subDir, "body.json"), []byte(`{"text":"from-subdir"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		resetAsked()
		allow := true
		hub.mu.Lock()
		hub.resolve = func(ix proto.Interaction) proto.Resolution { return proto.Resolution{Allow: &allow} }
		hub.mu.Unlock()
		res, err := askBash(sid, "Bash", "call-rel-cwd", `ex POST /conversations/c1/messages @body.json`, "sub")
		if err != nil {
			t.Fatal(err)
		}
		if res.Allow == nil || !*res.Allow {
			t.Fatalf("rel cwd write = %+v", res)
		}
		asked := askedSnapshot()
		if len(asked) != 1 {
			t.Fatalf("expected 1 card, got %d", len(asked))
		}
		if asked[0].Cwd != cwd {
			t.Fatalf("card.Cwd = %q, want session cwd %q", asked[0].Cwd, cwd)
		}
		if !strings.Contains(asked[0].Summary, "from-subdir") {
			t.Fatalf("card summary did not read relative @body.json from sub cwd: %q", asked[0].Summary)
		}

		// A per-call cwd inside the health space must force confirmation even for an ordinary write in bypass mode.
		n.noteModes(sid, proto.ModeBypass, "")
		t.Cleanup(func() { n.noteModes(sid, proto.ModeDefault, "") })
		healthDir := filepath.Join(n.home, ".persona", "health")
		if err := os.MkdirAll(healthDir, 0o755); err != nil {
			t.Fatal(err)
		}
		resetAsked()
		res, err = askBash(sid, "Bash", "call-space-cwd", `ex POST /notes '{"title":"ok"}'`, healthDir)
		if err != nil {
			t.Fatal(err)
		}
		if res.Allow == nil || !*res.Allow {
			t.Fatalf("space cwd write = %+v", res)
		}
		asked = askedSnapshot()
		if len(asked) != 1 {
			t.Fatalf("expected space write confirmation card in bypass mode, got %d", len(asked))
		}
		if asked[0].Cwd != healthDir {
			t.Fatalf("space card.Cwd = %q, want %q", asked[0].Cwd, healthDir)
		}
	})

	t.Run("non-assistant session and non-Bash assistant tool stay on normal runtime card flow", func(t *testing.T) {
		allow := true
		hub.mu.Lock()
		hub.resolve = func(ix proto.Interaction) proto.Resolution { return proto.Resolution{Allow: &allow} }
		hub.mu.Unlock()

		resetAsked()
		res, err := askBash("session-regular", "Bash", "call-reg", `ex POST /conversations/c1/messages '{"text":"reg"}'`, "")
		if err != nil {
			t.Fatal(err)
		}
		if res.Allow == nil || !*res.Allow {
			t.Fatalf("regular resolution = %+v", res)
		}
		if asked := askedSnapshot(); len(asked) != 1 || asked[0].ToolName != "Bash" || asked[0].Agent != "dsh" {
			t.Fatalf("regular session card = %+v, want normal runtime Bash card", asked)
		}

		resetAsked()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var editRes proto.Resolution
		err = host.peer.Call(ctx, "interaction.create", map[string]any{"interaction": map[string]any{
			"kind": "permission", "session_id": sid, "tool_name": "Edit", "tool_use_id": "call-edit",
			"tool_input": map[string]any{"file_path": filepath.Join(cwd, "note.md")}, "summary": "edit note.md",
		}}, &editRes)
		if err != nil {
			t.Fatal(err)
		}
		if asked := askedSnapshot(); len(asked) != 1 || asked[0].ToolName != "Edit" || asked[0].Agent != "dsh" {
			t.Fatalf("non-Bash assistant card = %+v, want normal runtime Edit card", asked)
		}
	})

	t.Run("hub disconnect while waiting on Lark card returns node_gone", func(t *testing.T) {
		n2, hub2 := runtimeNode(t)
		t.Cleanup(func() { n2.gates().close() })
		assistantHub(hub2)
		cwd2 := prepareRuntimeAssistantEnvironment(t, n2, true)
		hello2 := assistantRuntimeHello()
		hello2["caps"].(map[string]any)["assistant_environment"] = true
		host2 := dialRuntime(t, n2, hello2)
		sid2 := startedAssistant(t, n2, host2)
		_ = cwd2

		askedCh := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		t.Cleanup(func() { close(release) })
		hub2.mu.Lock()
		hub2.resolve = func(ix proto.Interaction) proto.Resolution {
			once.Do(func() { close(askedCh) })
			<-release
			return proto.Resolution{Dismiss: true}
		}
		hub2.mu.Unlock()

		errCh := make(chan error, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var res proto.Resolution
			errCh <- host2.peer.Call(ctx, "interaction.create", map[string]any{"interaction": map[string]any{
				"kind": "permission", "session_id": sid2, "tool_name": "Bash", "tool_use_id": "call-drop",
				"tool_input": map[string]any{"command": `ex POST /conversations/c1/messages '{"text":"drop"}'`},
			}}, &res)
		}()

		select {
		case <-askedCh:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for Lark card")
		}
		hub2.mu.Lock()
		dropCard := hub2.asked[0]
		hub2.mu.Unlock()
		if dropCard.ToolName != "示例" || !dropCard.OutwardWrite || !dropCard.Resumable {
			t.Fatalf("expected resumable Lark gate card before drop, got %+v", dropCard)
		}
		hub2.drop()

		var rpcErr *rpc.Error
		select {
		case err := <-errCh:
			if !errors.As(err, &rpcErr) || rpcErr.Code != "node_gone" {
				t.Fatalf("err = %v, want rpc.Error code node_gone", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for node_gone")
		}
	})

	t.Run("non-assistant_environment runtime and remote_agent Bash asks are never auto-bypassed", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			remote bool
		}{
			{name: "dsh without assistant_environment", remote: false},
			{name: "remote_agent with assistant_environment", remote: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				n2, hub2 := runtimeNode(t)
				assistantHub(hub2)
				cwd2 := prepareRuntimeAssistantEnvironment(t, n2, false)
				hello2 := assistantRuntimeHello()
				if tc.remote {
					hello2["caps"].(map[string]any)["assistant_environment"] = true
					hello2["caps"].(map[string]any)["remote_agent"] = true
				}
				host2 := dialRuntime(t, n2, hello2)
				sid2 := startedAssistant(t, n2, host2)
				n2.noteModes(sid2, proto.ModeBypass, "")
				_ = host2.peer.Notify("runtime.sessions", map[string]any{"sessions": []proto.Session{
					{SessionID: sid2, Title: "Main Assistant", Cwd: cwd2, Status: proto.StatusIdle},
				}})
				waitFor(t, "listed", func() bool { _, _, ok := n2.runtimes.rowOf(sid2); return ok })

				allow := true
				hub2.mu.Lock()
				hub2.resolve = func(ix proto.Interaction) proto.Resolution { return proto.Resolution{Allow: &allow} }
				hub2.mu.Unlock()

				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				var res proto.Resolution
				err := host2.peer.Call(ctx, "interaction.create", map[string]any{"interaction": map[string]any{
					"kind": "permission", "session_id": sid2, "tool_name": "Bash", "tool_use_id": "call-danger",
					"tool_input": map[string]any{"command": "rm -rf /tmp/some-dir"}, "summary": "rm -rf /tmp/some-dir",
				}}, &res)
				if err != nil {
					t.Fatal(err)
				}
				hub2.mu.Lock()
				got := append([]proto.Interaction(nil), hub2.asked...)
				hub2.mu.Unlock()
				if len(got) != 1 || got[0].ToolName != "Bash" {
					t.Fatalf("expected ordinary Bash card on phone, got %+v", got)
				}
			})
		}
	})

	t.Run("space session after node restart still confirms writes even when per-call cwd is outside space", func(t *testing.T) {
		n2, hub2 := runtimeNode(t)
		t.Cleanup(func() { n2.gates().close() })
		assistantHub(hub2)
		prepareRuntimeAssistantEnvironment(t, n2, true)
		healthDir := filepath.Join(n2.home, ".persona", "health")
		if err := os.MkdirAll(healthDir, 0o755); err != nil {
			t.Fatal(err)
		}
		hello2 := assistantRuntimeHello()
		hello2["caps"].(map[string]any)["assistant_environment"] = true
		host2 := dialRuntime(t, n2, hello2)
		const spaceSid = "session-health-1"
		_ = host2.peer.Notify("runtime.sessions", map[string]any{"sessions": []proto.Session{
			{SessionID: spaceSid, Title: "Health", Cwd: healthDir, Space: "health", Status: proto.StatusIdle},
		}})
		waitFor(t, "space session listed", func() bool { _, _, ok := n2.runtimes.rowOf(spaceSid); return ok })
		n2.noteModes(spaceSid, proto.ModeBypass, "")
		// Ensure gateProxy.spaces is empty (as immediately after a personad restart).
		gp := n2.gates()
		gp.mu.Lock()
		gp.spaces = nil
		gp.mu.Unlock()

		allow := true
		hub2.mu.Lock()
		hub2.resolve = func(ix proto.Interaction) proto.Resolution { return proto.Resolution{Allow: &allow} }
		hub2.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var res proto.Resolution
		err := host2.peer.Call(ctx, "interaction.create", map[string]any{"interaction": map[string]any{
			"kind": "permission", "session_id": spaceSid, "cwd": t.TempDir(), "tool_name": "Bash", "tool_use_id": "call-space-restart",
			"tool_input": map[string]any{"command": `ex POST /notes '{"title":"health-note"}'`},
		}}, &res)
		if err != nil {
			t.Fatal(err)
		}
		hub2.mu.Lock()
		got := append([]proto.Interaction(nil), hub2.asked...)
		hub2.mu.Unlock()
		if len(got) != 1 || got[0].ToolName != "示例" || got[0].Cwd != healthDir {
			t.Fatalf("space session with outside per-call cwd must confirm in space, got %+v", got)
		}
		if !gp.spaceSession(spaceSid) {
			t.Fatalf("expected gateProxy to latch markSpace(%q)", spaceSid)
		}
	})

	t.Run("blocked agent directory cannot be bypassed by per-call cwd and nil node state does not panic", func(t *testing.T) {
		n2, hub2 := runtimeNode(t)
		blockedDir := t.TempDir()
		n2.setBlockedAgentDirs([]string{blockedDir})
		host2 := dialRuntime(t, n2, dshHello())
		_ = host2.peer.Notify("runtime.sessions", map[string]any{"sessions": []proto.Session{
			{SessionID: "session-blocked", Title: "Secret Bot", Cwd: blockedDir, Status: proto.StatusIdle},
		}})
		waitFor(t, "blocked session listed", func() bool { _, _, ok := n2.runtimes.rowOf("session-blocked"); return ok })

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var res proto.Resolution
		err := host2.peer.Call(ctx, "interaction.create", map[string]any{"interaction": map[string]any{
			"kind": "permission", "session_id": "session-blocked", "cwd": t.TempDir(), "tool_name": "Bash", "tool_use_id": "call-blocked",
			"tool_input": map[string]any{"command": "ls"},
		}}, &res)
		if err != nil {
			t.Fatal(err)
		}
		if !res.Dismiss {
			t.Fatalf("blocked session with outside cwd = %+v, want dismiss=true", res)
		}
		hub2.mu.Lock()
		askedCount := len(hub2.asked)
		hub2.mu.Unlock()
		if askedCount != 0 {
			t.Fatalf("blocked session leaked %d card(s) to hub", askedCount)
		}

		// Verify runtimeAsk itself tolerates a lightweight Node with n.state == nil without panicking.
		n3, hub3 := runtimeNode(t)
		host3 := dialRuntime(t, n3, dshHello())
		_ = host3.peer.Notify("runtime.sessions", map[string]any{"sessions": []proto.Session{
			{SessionID: "session-nil-state", Title: "Plain", Cwd: t.TempDir(), Status: proto.StatusIdle},
		}})
		waitFor(t, "nil-state session listed", func() bool { _, _, ok := n3.runtimes.rowOf("session-nil-state"); return ok })
		h3, _, ok := n3.runtimes.belongs("session-nil-state")
		if !ok || h3 == nil {
			t.Fatal("missing runtime conn for session-nil-state")
		}
		allow := true
		hub3.mu.Lock()
		hub3.resolve = func(ix proto.Interaction) proto.Resolution { return proto.Resolution{Allow: &allow} }
		hub3.mu.Unlock()
		lightNode := &Node{
			peer:     n3.peer,
			runtimes: n3.runtimes,
			home:     n3.home,
			logger:   n3.logger,
		}
		rawParams, err := json.Marshal(map[string]any{"interaction": map[string]any{
			"kind": "permission", "session_id": "session-nil-state", "tool_name": "Bash", "tool_use_id": "call-nil",
			"tool_input": map[string]any{"command": "echo ok"},
		}})
		if err != nil {
			t.Fatal(err)
		}
		resAny, err := lightNode.runtimeAsk(ctx, h3, rawParams)
		if err != nil {
			t.Fatal(err)
		}
		res3, ok := resAny.(proto.Resolution)
		if !ok || res3.Allow == nil || !*res3.Allow {
			t.Fatalf("nil-state runtimeAsk = %#v, want allow=true", resAny)
		}
	})
}
