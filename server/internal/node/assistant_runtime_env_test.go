package node

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/proto"
)

var gateProxyURLRe = regexp.MustCompile(`^http://(?:127\.0\.0\.1|\[::1\]):\d+/[0-9a-f]{32}$`)

func prepareRuntimeAssistantEnvironment(t *testing.T, n *Node, proxied bool) string {
	t.Helper()
	skills := filepath.Join(n.home, ".persona", "skills")
	t.Setenv("PERSONA_SKILLS_DIR", skills)
	t.Setenv("PERSONA_PLUGIN_SKILLS_DIR", t.TempDir())
	n.dir = filepath.Join(n.home, ".persona")
	script := filepath.Join(skills, "example", "scripts", "ex.sh")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skills, "example", "SKILL.md"), []byte("---\nname: example\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'fake extension command\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := prepareAssistantForTest(t, n, ".persona/assistant", nil, nil)
	g := exampleGate("")
	g.Env["EXAMPLE_ACCOUNT"] = "2"
	g.Env["EXAMPLE_TOKEN"] = "fake-credential-handle=with-equals"
	if proxied {
		g.Env["EXAMPLE_BASE"] = "https://upstream.example.test"
		g.Auth = &proto.GateAuth{Header: "Authorization", Value: "Bearer ${EXAMPLE_TOKEN}"}
	}
	n.mu.Lock()
	n.larkCfg, n.larkCfgAt = proto.AssistantLark{Gates: []proto.CommandGate{g}}, time.Now().Add(time.Hour)
	n.mu.Unlock()
	return cwd
}

func runtimeAssistantEnvironment(t *testing.T, raw json.RawMessage) map[string]string {
	t.Helper()
	var params struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatal(err)
	}
	return params.Env
}

func TestRuntimeAssistantEnvironmentCapable(t *testing.T) {
	n, hub := runtimeNode(t)
	assistantHub(hub)
	cwd := prepareRuntimeAssistantEnvironment(t, n, false)
	hello := assistantRuntimeHello()
	hello["caps"].(map[string]any)["assistant_environment"] = true
	host := dialRuntime(t, n, hello)
	startedAssistant(t, n, host)

	starts, sends := host.got("session.start"), host.got("session.send")
	if len(starts) != 1 || len(sends) != 1 {
		t.Fatalf("start/send counts: %d/%d", len(starts), len(sends))
	}
	env := runtimeAssistantEnvironment(t, starts[0])
	if len(env) == 0 {
		t.Fatalf("runtime assistant start has no extension environment: %s", starts[0])
	}
	if got := runtimeAssistantEnvironment(t, sends[0]); !maps.Equal(got, env) {
		t.Fatalf("start and send environments differ:\nstart %q\nsend  %q", env, got)
	}
	bin := n.assistantBin(cwd)
	if path := env["PATH"]; path != bin+string(os.PathListSeparator)+os.Getenv("PATH") {
		t.Fatalf("PATH %q does not begin with the generated command directory %q", path, bin)
	}
	for name, want := range map[string]string{
		"PERSONA_ASSISTANT_BIN": bin,
		"PERSONA_SPACE":         spaceMain,
		"EXAMPLE_ACCOUNT":       "2",
		"EXAMPLE_TOKEN":         "fake-credential-handle=with-equals",
	} {
		if got := env[name]; got != want {
			t.Fatalf("%s = %q, want %q; environment %q", name, got, want, env)
		}
	}
	if _, err := os.Stat(filepath.Join(bin, "ex")); err != nil {
		t.Fatalf("generated extension CLI is missing: %v", err)
	}

	if _, err := nodeCall(t, n, "session.modes", map[string]any{"session_id": "session-a1", "permission_mode": "plan"}); err != nil {
		t.Fatal(err)
	}
	modes := host.got("session.modes")
	if len(modes) != 1 || !maps.Equal(runtimeAssistantEnvironment(t, modes[0]), env) {
		t.Fatalf("mode-triggered worker creation lost the environment: %s", modes)
	}
}

func TestRuntimeAssistantEnvironmentWithholdsProxyCredentials(t *testing.T) {
	api := newFakeAPI(t)
	n, hub := runtimeNode(t)
	n.runtimeHost.leaseDisconnectGrace = 40 * time.Millisecond
	t.Cleanup(func() { n.gates().close() })
	assistantHub(hub)
	cwd := prepareRuntimeAssistantEnvironment(t, n, true)
	n.mu.Lock()
	n.larkCfg.Gates[0].Env["EXAMPLE_BASE"] = api.URL
	n.mu.Unlock()
	hello := assistantRuntimeHello()
	hello["caps"].(map[string]any)["assistant_environment"] = true
	hello["caps"].(map[string]any)["fork"] = true
	host := dialRuntime(t, n, hello)
	host.answer = func(method string, params json.RawMessage) (any, error) {
		switch method {
		case "session.start":
			return map[string]any{"session": proto.Session{SessionID: "session-a1", Cwd: cwd, Status: proto.StatusIdle}}, nil
		case "session.fork":
			return map[string]any{"session_id": "session-a1-fork", "session": proto.Session{SessionID: "session-a1-fork", Cwd: cwd, Status: proto.StatusIdle}}, nil
		default:
			return map[string]any{"ok": true}, nil
		}
	}
	raw, err := nodeCall(t, n, "assistant.send", map[string]any{"launcher": "dsh", "cwd": cwd, "text": "turn 1"})
	if err != nil {
		t.Fatal(err)
	}
	var started struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(raw, &started)
	if started.SessionID != "session-a1" {
		t.Fatalf("assistant.send = %s", raw)
	}
	if err := host.peer.Notify("runtime.sessions", map[string]any{"sessions": []proto.Session{
		{SessionID: "session-a1", Cwd: cwd, Status: proto.StatusIdle},
	}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "runtime session listed", func() bool {
		_, _, ok := n.runtimes.rowOf("session-a1")
		return ok
	})

	env := runtimeAssistantEnvironment(t, host.got("session.start")[0])
	if got := env["EXAMPLE_ACCOUNT"]; got != "2" {
		t.Fatalf("approved non-secret environment is missing: %q", env)
	}
	if value, ok := env["EXAMPLE_TOKEN"]; ok {
		t.Fatalf("proxied credential material escaped through EXAMPLE_TOKEN=%q", value)
	}
	base := env["EXAMPLE_BASE"]
	if !gateProxyURLRe.MatchString(base) {
		t.Fatalf("EXAMPLE_BASE = %q, want local gate proxy address", base)
	}
	encoded, _ := json.Marshal(env)
	if joined := string(encoded); strings.Contains(joined, "fake-credential-handle") || strings.Contains(joined, api.URL) {
		t.Fatalf("raw credential or direct upstream reached the runtime: %s", joined)
	}

	// Subsequent turns, mode updates, and forks must reuse the exact same (session, command) proxy lease.
	if _, err := nodeCall(t, n, "assistant.send", map[string]any{"session_id": "session-a1", "launcher": "dsh", "cwd": cwd, "text": "turn 2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := nodeCall(t, n, "session.modes", map[string]any{"session_id": "session-a1", "permission_mode": "plan"}); err != nil {
		t.Fatal(err)
	}
	if _, err := nodeCall(t, n, "session.fork", map[string]any{"session_id": "session-a1", "at_item_id": "item-1"}); err != nil {
		t.Fatal(err)
	}
	for label, got := range map[string]map[string]string{
		"turn 1 send": runtimeAssistantEnvironment(t, host.got("session.send")[0]),
		"turn 2 send": runtimeAssistantEnvironment(t, host.got("session.send")[1]),
		"modes":       runtimeAssistantEnvironment(t, host.got("session.modes")[0]),
		"fork":        runtimeAssistantEnvironment(t, host.got("session.fork")[0]),
	} {
		if got["EXAMPLE_BASE"] != base {
			t.Fatalf("%s EXAMPLE_BASE = %q, want reused %q", label, got["EXAMPLE_BASE"], base)
		}
		if _, leaked := got["EXAMPLE_TOKEN"]; leaked {
			t.Fatalf("%s leaked EXAMPLE_TOKEN", label)
		}
	}
	gp := n.gates()
	gp.mu.Lock()
	keyCount, leaseCount := len(gp.keys), len(gp.sessionLeases)
	gp.mu.Unlock()
	if keyCount != 1 || leaseCount != 1 {
		t.Fatalf("expected 1 idempotent proxy lease, got keys=%d sessionLeases=%d", keyCount, leaseCount)
	}

	// The leased proxy URL must inject the withheld credential on allowed reads and still block unconfirmed writes.
	if code, body := send(t, "GET", base+"/api/v1/me", ""); code != http.StatusOK || body != `{"ok":true}` {
		t.Fatalf("proxied read: %d %s", code, body)
	}
	if code, _ := send(t, "POST", base+"/api/v1/conversations/c1/messages", `{"text":"hi"}`); code != http.StatusForbidden {
		t.Fatalf("unconfirmed write through proxy = %d, want 403", code)
	}
	got := api.requests()
	if len(got) != 1 || got[0].Path != "/api/v1/me" || got[0].Auth != "Bearer fake-credential-handle=with-equals" {
		t.Fatalf("upstream requests = %+v", got)
	}
}

func TestRuntimeAssistantEnvironmentRelaySocketEnvProxy(t *testing.T) {
	n, hub := runtimeNode(t)
	n.runtimeHost.leaseDisconnectGrace = 40 * time.Millisecond
	t.Cleanup(func() { n.gates().close() })
	assistantHub(hub)
	cwd := prepareRuntimeAssistantEnvironment(t, n, false)

	var relayed []proto.GateRelayParams
	var relayMu sync.Mutex
	hub.mu.Lock()
	baseAnswer := hub.answer
	hub.answer = func(method string, params json.RawMessage) (any, error, bool) {
		if method == proto.MethodGateRelay {
			var req proto.GateRelayParams
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, err, true
			}
			relayMu.Lock()
			relayed = append(relayed, req)
			relayMu.Unlock()
			return proto.GateRelayAnswer{
				Status: http.StatusOK,
				Header: map[string][]string{"Content-Type": {"application/json"}},
				Body:   []byte(`{"relayed":true}`),
			}, nil, true
		}
		return baseAnswer(method, params)
	}
	hub.mu.Unlock()

	g := exampleGate("")
	g.BaseEnv = "RELAY_BASE"
	g.SocketEnv = "RELAY_SOCKET"
	g.Relay = true
	g.Secret = []string{"RELAY_TOKEN"}
	g.Env = map[string]string{
		"RELAY_SOCKET":  "/run/fake-extension.sock",
		"RELAY_TOKEN":   "fake-relay-secret",
		"RELAY_ACCOUNT": "7",
	}
	cfg := proto.AssistantLark{Gates: []proto.CommandGate{g}}
	relayGates(&cfg)
	n.mu.Lock()
	n.larkCfg, n.larkCfgAt = cfg, time.Now().Add(time.Hour)
	n.mu.Unlock()

	hello := assistantRuntimeHello()
	hello["caps"].(map[string]any)["assistant_environment"] = true
	host := dialRuntime(t, n, hello)
	sid := startedAssistant(t, n, host)

	startEnv := runtimeAssistantEnvironment(t, host.got("session.start")[0])
	base := startEnv["RELAY_BASE"]
	if !gateProxyURLRe.MatchString(base) {
		t.Fatalf("RELAY_BASE = %q, want local gate proxy address (env=%v)", base, startEnv)
	}
	if startEnv["RELAY_ACCOUNT"] != "7" {
		t.Fatalf("RELAY_ACCOUNT = %q, want 7", startEnv["RELAY_ACCOUNT"])
	}
	for _, secretName := range []string{"RELAY_SOCKET", "RELAY_TOKEN"} {
		if v, ok := startEnv[secretName]; ok {
			t.Fatalf("relayed secret/socket %s=%q leaked into runtime env", secretName, v)
		}
	}
	encoded, _ := json.Marshal(startEnv)
	if s := string(encoded); strings.Contains(s, "fake-extension.sock") || strings.Contains(s, "fake-relay-secret") {
		t.Fatalf("raw socket or token leaked into runtime env: %s", s)
	}

	if _, err := nodeCall(t, n, "assistant.send", map[string]any{"session_id": sid, "launcher": "dsh", "cwd": cwd, "text": "turn 2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := nodeCall(t, n, "session.modes", map[string]any{"session_id": sid, "permission_mode": "plan"}); err != nil {
		t.Fatal(err)
	}
	for label, got := range map[string]map[string]string{
		"turn 1 send": runtimeAssistantEnvironment(t, host.got("session.send")[0]),
		"turn 2 send": runtimeAssistantEnvironment(t, host.got("session.send")[1]),
		"modes":       runtimeAssistantEnvironment(t, host.got("session.modes")[0]),
	} {
		if got["RELAY_BASE"] != base {
			t.Fatalf("%s RELAY_BASE = %q, want reused %q", label, got["RELAY_BASE"], base)
		}
	}

	if code, body := send(t, "GET", base+"/api/v1/me", ""); code != http.StatusOK || body != `{"relayed":true}` {
		t.Fatalf("relayed read: %d %s", code, body)
	}
	if code, _ := send(t, "POST", base+"/api/v1/conversations/c1/messages", `{"text":"hi"}`); code != http.StatusForbidden {
		t.Fatalf("unconfirmed relayed write = %d, want 403", code)
	}
	relayMu.Lock()
	defer relayMu.Unlock()
	if len(relayed) != 1 || relayed[0].Command != "ex" || relayed[0].Method != "GET" || relayed[0].Path != "/api/v1/me" {
		t.Fatalf("relayed calls = %+v", relayed)
	}
}

func TestRuntimeAssistantEnvironmentSessionAndCommandIsolationAndCleanup(t *testing.T) {
	api1 := newFakeAPI(t)
	api2 := newFakeAPI(t)
	n, hub := runtimeNode(t)
	n.runtimeHost.leaseDisconnectGrace = 40 * time.Millisecond
	t.Cleanup(func() { n.gates().close() })
	assistantHub(hub)
	cwd := prepareRuntimeAssistantEnvironment(t, n, true)

	// Install a second proxied command gate (custom auth header) and a third non-proxied gate.
	skills := filepath.Join(n.home, ".persona", "skills")
	for _, extra := range []struct{ dir, script string }{
		{"example2", "ex2.sh"},
		{"plain", "plain.sh"},
	} {
		p := filepath.Join(skills, extra.dir, "scripts", extra.script)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skills, extra.dir, "SKILL.md"), []byte("---\nname: "+extra.dir+"\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	g1 := proxiedGate(api1.URL)
	g1.Env["EXAMPLE_ACCOUNT"] = "1"
	g2 := proto.CommandGate{
		Command:    "ex2",
		Script:     "example2/scripts/ex2.sh",
		Title:      "Example 2",
		BaseEnv:    "EXAMPLE2_BASE",
		Secret:     []string{"EXAMPLE2_KEY", "EXAMPLE2_TOKEN"},
		PathPrefix: "/api/v1",
		Env:        map[string]string{"EXAMPLE2_BASE": api2.URL, "EXAMPLE2_KEY": "k2", "EXAMPLE2_TOKEN": "t2"},
		Auth:       &proto.GateAuth{Header: "X-Example-Account", Value: "${EXAMPLE2_KEY}:${EXAMPLE2_TOKEN}"},
		Rules:      []proto.LarkRule{{Method: "GET", Path: "/**", Action: "allow"}},
	}
	g3 := proto.CommandGate{
		Command: "plain",
		Script:  "plain/scripts/plain.sh",
		Title:   "Plain",
		Env:     map[string]string{"PLAIN_VALUE": "direct-value"},
	}
	n.mu.Lock()
	n.larkCfg, n.larkCfgAt = proto.AssistantLark{Gates: []proto.CommandGate{g1, g2, g3}}, time.Now().Add(time.Hour)
	n.mu.Unlock()

	hello := assistantRuntimeHello()
	hello["instance"] = "inst-stable"
	hello["caps"].(map[string]any)["assistant_environment"] = true
	hello["caps"].(map[string]any)["fork"] = true
	hello["caps"].(map[string]any)["close"] = true
	host := dialRuntime(t, n, hello)

	var mu sync.Mutex
	nextID := 0
	failNextStart := false
	host.answer = func(method string, params json.RawMessage) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		switch method {
		case "session.start":
			if failNextStart {
				failNextStart = false
				return nil, errors.New("start refused")
			}
			nextID++
			id := "session-iso-" + string(rune('0'+nextID))
			return map[string]any{"session": proto.Session{SessionID: id, Cwd: cwd, Status: proto.StatusIdle}}, nil
		case "session.fork":
			return map[string]any{"session_id": "session-iso-fork", "session": proto.Session{SessionID: "session-iso-fork", Cwd: cwd, Status: proto.StatusIdle}}, nil
		default:
			return map[string]any{"ok": true}, nil
		}
	}

	startSession := func(text string) string {
		t.Helper()
		raw, err := nodeCall(t, n, "assistant.send", map[string]any{"launcher": "dsh", "cwd": cwd, "text": text})
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(raw, &out)
		return out.SessionID
	}

	s1 := startSession("hello 1")
	s2 := startSession("hello 2")
	starts := host.got("session.start")
	env1 := runtimeAssistantEnvironment(t, starts[0])
	env2 := runtimeAssistantEnvironment(t, starts[1])

	// Non-proxied extension env passes through unchanged without allocating proxy leases.
	if env1["PLAIN_VALUE"] != "direct-value" || env2["PLAIN_VALUE"] != "direct-value" {
		t.Fatalf("non-proxied env lost: s1=%q s2=%q", env1["PLAIN_VALUE"], env2["PLAIN_VALUE"])
	}
	for _, env := range []map[string]string{env1, env2} {
		for _, withheld := range []string{"EXAMPLE_TOKEN", "EXAMPLE2_KEY", "EXAMPLE2_TOKEN"} {
			if v, ok := env[withheld]; ok {
				t.Fatalf("secret %s=%q leaked into env", withheld, v)
			}
		}
	}
	b1ex, b1ex2 := env1["EXAMPLE_BASE"], env1["EXAMPLE2_BASE"]
	b2ex, b2ex2 := env2["EXAMPLE_BASE"], env2["EXAMPLE2_BASE"]
	allBases := []string{b1ex, b1ex2, b2ex, b2ex2}
	seenBases := map[string]bool{}
	for _, b := range allBases {
		if !gateProxyURLRe.MatchString(b) {
			t.Fatalf("unexpected proxy base %q", b)
		}
		if seenBases[b] {
			t.Fatalf("proxy addresses collided across sessions/commands: %v", allBases)
		}
		seenBases[b] = true
	}

	// Multi-turn reuse on s1 and fork isolation.
	if _, err := nodeCall(t, n, "assistant.send", map[string]any{"session_id": s1, "launcher": "dsh", "cwd": cwd, "text": "s1 turn 2"}); err != nil {
		t.Fatal(err)
	}
	if got := runtimeAssistantEnvironment(t, host.got("session.send")[2]); got["EXAMPLE_BASE"] != b1ex || got["EXAMPLE2_BASE"] != b1ex2 {
		t.Fatalf("s1 turn 2 did not reuse leases: got %q/%q want %q/%q", got["EXAMPLE_BASE"], got["EXAMPLE2_BASE"], b1ex, b1ex2)
	}
	if _, err := nodeCall(t, n, "session.fork", map[string]any{"session_id": s1, "at_item_id": "item-1"}); err != nil {
		t.Fatal(err)
	}
	if got := runtimeAssistantEnvironment(t, host.got("session.fork")[0]); got["EXAMPLE_BASE"] != b1ex || got["EXAMPLE2_BASE"] != b1ex2 {
		t.Fatalf("session.fork did not reuse source leases: got %q/%q want %q/%q", got["EXAMPLE_BASE"], got["EXAMPLE2_BASE"], b1ex, b1ex2)
	}
	if _, err := nodeCall(t, n, "assistant.send", map[string]any{"session_id": "session-iso-fork", "launcher": "dsh", "cwd": cwd, "text": "fork turn 1"}); err != nil {
		t.Fatal(err)
	}
	forkEnv := runtimeAssistantEnvironment(t, host.got("session.send")[3])
	bfex, bfex2 := forkEnv["EXAMPLE_BASE"], forkEnv["EXAMPLE2_BASE"]
	if !gateProxyURLRe.MatchString(bfex) || bfex == b1ex || bfex == b2ex || !gateProxyURLRe.MatchString(bfex2) || bfex2 == b1ex2 || bfex2 == b2ex2 {
		t.Fatalf("forked session did not receive isolated leases: fork=%q/%q s1=%q/%q s2=%q/%q", bfex, bfex2, b1ex, b1ex2, b2ex, b2ex2)
	}

	// Failed session.start must release its pending unnamed leases immediately.
	mu.Lock()
	failNextStart = true
	mu.Unlock()
	if _, err := nodeCall(t, n, "assistant.send", map[string]any{"launcher": "dsh", "cwd": cwd, "text": "will fail"}); err == nil {
		t.Fatal("expected session.start failure")
	}
	failedEnv := runtimeAssistantEnvironment(t, host.got("session.start")[2])
	if code, _ := send(t, "GET", failedEnv["EXAMPLE_BASE"]+"/api/v1/me", ""); code != http.StatusNotFound {
		t.Fatalf("failed start proxy URL = %d, want 404", code)
	}

	// Assistant session.close only interrupts/keeps the runtime conversation and must keep s1's leases alive.
	if _, err := nodeCall(t, n, "session.close", map[string]any{"session_id": s1}); err != nil {
		t.Fatal(err)
	}
	for _, keptBase := range []string{b1ex, b1ex2} {
		if code, _ := send(t, "GET", keptBase+"/api/v1/me", ""); code != http.StatusOK {
			t.Fatalf("assistant-closed session lease %q returned %d, want 200", keptBase, code)
		}
	}

	// An explicit runtime-forwarded session.close that retires the worker revokes only s1's leases; s2 and the fork stay live.
	rawClose, _ := json.Marshal(map[string]any{"session_id": s1})
	if _, err, handled := n.runtimeHandle(context.Background(), n.runtimes.byRuntime("dsh"), "session.close", rawClose); err != nil || !handled {
		t.Fatalf("explicit runtime session.close: handled=%v err=%v", handled, err)
	}
	for _, closedBase := range []string{b1ex, b1ex2} {
		if code, _ := send(t, "GET", closedBase+"/api/v1/me", ""); code != http.StatusNotFound {
			t.Fatalf("runtime-closed session lease %q returned %d, want 404", closedBase, code)
		}
	}
	if code, _ := send(t, "GET", b2ex+"/api/v1/me", ""); code != http.StatusOK {
		t.Fatalf("s2 ex lease returned %d after s1 close, want 200", code)
	}
	if code, _ := send(t, "GET", b2ex2+"/api/v1/me", ""); code != http.StatusOK {
		t.Fatalf("s2 ex2 lease returned %d after s1 close, want 200", code)
	}
	if code, _ := send(t, "GET", bfex+"/api/v1/me", ""); code != http.StatusOK {
		t.Fatalf("fork ex lease returned %d after s1 close, want 200", code)
	}
	if reqs := api2.requests(); len(reqs) < 1 || reqs[len(reqs)-1].Account != "k2:t2" {
		t.Fatalf("ex2 upstream auth header mismatch: %+v", reqs)
	}

	// Same-instance reconnect supersedes the old connection and must preserve active session leases.
	host2 := dialRuntime(t, n, hello)
	if err := host2.peer.Notify("runtime.sessions", map[string]any{"sessions": []proto.Session{
		{SessionID: s2, Cwd: cwd, Status: proto.StatusIdle},
		{SessionID: "session-iso-fork", Cwd: cwd, Status: proto.StatusIdle},
	}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "superseded connection removed", func() bool {
		n.runtimes.mu.Lock()
		defer n.runtimes.mu.Unlock()
		return len(n.runtimes.conns) == 1
	})
	if code, _ := send(t, "GET", b2ex+"/api/v1/me", ""); code != http.StatusOK {
		t.Fatalf("s2 lease broken after same-instance reconnect: %d", code)
	}

	// Disconnecting permanently without reconnecting reclaims the remaining session leases once grace expires.
	_ = host2.conn.Close()
	waitFor(t, "runtime leases released on disconnect", func() bool {
		gp := n.gates()
		gp.mu.Lock()
		defer gp.mu.Unlock()
		return len(gp.sessionLeases) == 0 && len(gp.keys) == 0
	})
	for _, goneBase := range []string{b2ex, b2ex2, bfex, bfex2} {
		if code, _ := send(t, "GET", goneBase+"/api/v1/me", ""); code != http.StatusNotFound {
			t.Fatalf("disconnected runtime lease %q returned %d, want 404", goneBase, code)
		}
	}
}

// Persistent ACP workers (such as the Antigravity agy companion) capture env on session.start and keep using
// that startup environment while the worker process stays alive. Assistant session.close (idle or working) and
// non-overlapping same-instance socket reconnects must keep the worker's proxy URLs valid so resumed turns do not 404.
func TestRuntimeAssistantEnvironmentPersistentWorkerCloseAndResume(t *testing.T) {
	api := newFakeAPI(t)
	n, hub := runtimeNode(t)
	n.runtimeHost.leaseDisconnectGrace = 120 * time.Millisecond
	t.Cleanup(func() { n.gates().close() })
	assistantHub(hub)

	var relayCalls atomic.Int32
	hub.mu.Lock()
	prevAnswer := hub.answer
	hub.answer = func(method string, params json.RawMessage) (any, error, bool) {
		if method == proto.MethodGateRelay {
			var req proto.GateRelayParams
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, err, true
			}
			relayCalls.Add(1)
			return proto.GateRelayAnswer{
				Status: http.StatusOK,
				Header: map[string][]string{"Content-Type": {"application/json"}},
				Body:   []byte(`{"relayed":true}`),
			}, nil, true
		}
		return prevAnswer(method, params)
	}
	hub.mu.Unlock()

	cwd := prepareRuntimeAssistantEnvironment(t, n, true)
	skills := filepath.Join(n.home, ".persona", "skills")
	relayScript := filepath.Join(skills, "relayed", "scripts", "relay.sh")
	if err := os.MkdirAll(filepath.Dir(relayScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skills, "relayed", "SKILL.md"), []byte("---\nname: relayed\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(relayScript, []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	g1 := proxiedGate(api.URL)
	gRelay := proto.CommandGate{
		Command:   "relayed",
		Script:    "relayed/scripts/relay.sh",
		Title:     "Relayed",
		BaseEnv:   "RELAY_BASE",
		SocketEnv: "RELAY_SOCKET",
		Relay:     true,
		Env:       map[string]string{"RELAY_SOCKET": "/manager/private/relay.sock"},
		Auth:      &proto.GateAuth{Header: "X-Relay-Auth", Value: "relayed"},
		Rules:     []proto.LarkRule{{Method: "GET", Path: "/**", Action: "allow"}},
	}
	cfg := proto.AssistantLark{Gates: []proto.CommandGate{g1, gRelay}}
	relayGates(&cfg)
	n.mu.Lock()
	n.larkCfg, n.larkCfgAt = cfg, time.Now().Add(time.Hour)
	n.mu.Unlock()

	const sid = "agy-persistent-1"
	const inst1 = "agy-inst-1"

	// Model a persistent ACP worker: startup env is captured once on session.start and NOT overwritten on
	// subsequent session.send or session.interrupt calls while the worker stays alive (matching acpFor in runtime.js).
	var workerMu sync.Mutex
	var workerEnv map[string]string
	bindWorker := func(host *fakeRuntime) {
		host.answer = func(method string, params json.RawMessage) (any, error) {
			var p struct {
				Env map[string]string `json:"env"`
			}
			_ = json.Unmarshal(params, &p)
			switch method {
			case "session.start":
				workerMu.Lock()
				if workerEnv == nil {
					workerEnv = maps.Clone(p.Env)
				}
				workerMu.Unlock()
				return map[string]any{"session": proto.Session{SessionID: sid, Cwd: cwd, Status: proto.StatusIdle}}, nil
			case "session.send":
				// A live ACP worker ignores p.Env when already spawned; a freshly spawned worker captures it.
				workerMu.Lock()
				if workerEnv == nil {
					workerEnv = maps.Clone(p.Env)
				}
				workerMu.Unlock()
				return map[string]any{"ok": true}, nil
			case "session.interrupt":
				return map[string]any{"ok": true, "settled": true}, nil
			default:
				return map[string]any{"ok": true}, nil
			}
		}
	}

	hello1 := assistantRuntimeHello()
	hello1["runtime"], hello1["agent"], hello1["launcher"] = agyRuntimeID, agyRuntimeID, agyRuntimeID
	hello1["instance"] = inst1
	hello1["caps"].(map[string]any)["assistant_environment"] = true
	hello1["caps"].(map[string]any)["close"] = true
	host1 := dialRuntime(t, n, hello1)
	bindWorker(host1)

	if _, err := nodeCall(t, n, "assistant.send", map[string]any{"launcher": agyRuntimeID, "cwd": cwd, "text": "turn 1"}); err != nil {
		t.Fatal(err)
	}
	workerMu.Lock()
	heldBase, heldRelay := workerEnv["EXAMPLE_BASE"], workerEnv["RELAY_BASE"]
	workerMu.Unlock()
	if !gateProxyURLRe.MatchString(heldBase) || !gateProxyURLRe.MatchString(heldRelay) {
		t.Fatalf("worker captured non-proxy URLs: ex=%q relay=%q", heldBase, heldRelay)
	}
	assertWorkerLive := func(phase string) {
		t.Helper()
		if code, body := send(t, "GET", heldBase+"/api/v1/me", ""); code != http.StatusOK {
			t.Fatalf("%s: worker-held EXAMPLE_BASE returned %d (%s), want 200", phase, code, body)
		}
		if code, body := send(t, "POST", heldBase+"/api/v1/conversations/1/messages", `{"text":"hi"}`); code != http.StatusForbidden {
			t.Fatalf("%s: unconfirmed write on worker-held EXAMPLE_BASE returned %d (%s), want 403", phase, code, body)
		}
		if code, body := send(t, "GET", heldRelay+"/items", ""); code != http.StatusOK {
			t.Fatalf("%s: worker-held RELAY_BASE returned %d (%s), want 200", phase, code, body)
		}
	}
	assertWorkerLive("initial start")

	// Publish idle state after turn 1 finishes so step 1 exercises a genuinely idle session.close.
	if err := host1.peer.Notify("runtime.sessions", map[string]any{"sessions": []proto.Session{
		{SessionID: sid, Cwd: cwd, Status: proto.StatusIdle},
	}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "session idle after turn 1", func() bool {
		row, _, ok := n.runtimes.rowOf(sid)
		return ok && row.Status == proto.StatusIdle
	})

	// 1. Idle session.close: no runtime RPC is sent, worker stays alive, and resuming reuses the exact same URLs.
	if _, err := nodeCall(t, n, "session.close", map[string]any{"session_id": sid}); err != nil {
		t.Fatal(err)
	}
	if len(host1.got("session.close")) != 0 || len(host1.got("session.interrupt")) != 0 {
		t.Fatalf("idle assistant close sent unexpected RPCs: close=%d interrupt=%d", len(host1.got("session.close")), len(host1.got("session.interrupt")))
	}
	assertWorkerLive("after idle session.close")
	if _, err := nodeCall(t, n, "assistant.send", map[string]any{"session_id": sid, "launcher": agyRuntimeID, "cwd": cwd, "text": "turn after idle close"}); err != nil {
		t.Fatal(err)
	}
	if env := runtimeAssistantEnvironment(t, host1.got("session.send")[1]); env["EXAMPLE_BASE"] != heldBase || env["RELAY_BASE"] != heldRelay {
		t.Fatalf("send after idle close rotated URLs: got %q/%q want %q/%q", env["EXAMPLE_BASE"], env["RELAY_BASE"], heldBase, heldRelay)
	}
	assertWorkerLive("after resume from idle session.close")

	// 2. Working session.close: interrupts the active turn, keeps the ACP worker alive, and resuming reuses the exact same URLs.
	if err := host1.peer.Notify("runtime.sessions", map[string]any{"sessions": []proto.Session{
		{SessionID: sid, Cwd: cwd, Status: proto.StatusWorking},
	}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "session working", func() bool {
		row, _, ok := n.runtimes.rowOf(sid)
		return ok && row.Status == proto.StatusWorking
	})
	if _, err := nodeCall(t, n, "session.close", map[string]any{"session_id": sid}); err != nil {
		t.Fatal(err)
	}
	if len(host1.got("session.interrupt")) != 1 || len(host1.got("session.close")) != 0 {
		t.Fatalf("working assistant close RPCs: interrupt=%d close=%d, want 1/0", len(host1.got("session.interrupt")), len(host1.got("session.close")))
	}
	if err := host1.peer.Notify("runtime.sessions", map[string]any{"sessions": []proto.Session{
		{SessionID: sid, Cwd: cwd, Status: proto.StatusIdle},
	}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "session idle after interrupt", func() bool {
		row, _, ok := n.runtimes.rowOf(sid)
		return ok && row.Status == proto.StatusIdle
	})
	assertWorkerLive("after working session.close interrupt")
	if _, err := nodeCall(t, n, "assistant.send", map[string]any{"session_id": sid, "launcher": agyRuntimeID, "cwd": cwd, "text": "turn after working close"}); err != nil {
		t.Fatal(err)
	}
	if env := runtimeAssistantEnvironment(t, host1.got("session.send")[2]); env["EXAMPLE_BASE"] != heldBase || env["RELAY_BASE"] != heldRelay {
		t.Fatalf("send after working close rotated URLs: got %q/%q want %q/%q", env["EXAMPLE_BASE"], env["RELAY_BASE"], heldBase, heldRelay)
	}
	assertWorkerLive("after resume from working session.close")
	gp := n.gates()
	gp.mu.Lock()
	keyCount, leaseCount := len(gp.keys), len(gp.sessionLeases)
	gp.mu.Unlock()
	if keyCount != 2 || leaseCount != 2 {
		t.Fatalf("after close/resume: keys=%d leases=%d, want 2/2", keyCount, leaseCount)
	}

	// 3. Non-overlapping transient disconnect followed by same-instance reconnect preserves the surviving worker's URLs.
	_ = host1.conn.Close()
	waitFor(t, "host1 disconnected", func() bool {
		n.runtimes.mu.Lock()
		defer n.runtimes.mu.Unlock()
		return len(n.runtimes.conns) == 0
	})
	assertWorkerLive("during transient disconnect grace")
	host2 := dialRuntime(t, n, hello1)
	bindWorker(host2)
	if err := host2.peer.Notify("runtime.sessions", map[string]any{"sessions": []proto.Session{
		{SessionID: sid, Cwd: cwd, Status: proto.StatusIdle},
	}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "host2 owns session", func() bool {
		_, _, ok := n.runtimes.rowOf(sid)
		return ok
	})
	// Confirm the deferred release entry was cleared on same-instance reconnect and stays live past 2x grace.
	n.runtimeHost.mu.Lock()
	_, stillPending := n.runtimeHost.pendingReleases[agyRuntimeID]
	n.runtimeHost.mu.Unlock()
	if stillPending {
		t.Fatalf("pending disconnect release for %q was not cancelled on same-instance reconnect", agyRuntimeID)
	}
	graceWait := time.NewTimer(260 * time.Millisecond)
	<-graceWait.C
	assertWorkerLive("past grace after same-instance reconnect")
	if _, err := nodeCall(t, n, "assistant.send", map[string]any{"session_id": sid, "launcher": agyRuntimeID, "cwd": cwd, "text": "turn after reconnect"}); err != nil {
		t.Fatal(err)
	}
	if env := runtimeAssistantEnvironment(t, host2.got("session.send")[0]); env["EXAMPLE_BASE"] != heldBase || env["RELAY_BASE"] != heldRelay {
		t.Fatalf("send after same-instance reconnect rotated URLs: got %q/%q want %q/%q", env["EXAMPLE_BASE"], env["RELAY_BASE"], heldBase, heldRelay)
	}

	// 4. Restart with a different instance immediately revokes old URLs and issues fresh URLs to the new worker.
	_ = host2.conn.Close()
	waitFor(t, "host2 disconnected", func() bool {
		n.runtimes.mu.Lock()
		defer n.runtimes.mu.Unlock()
		return len(n.runtimes.conns) == 0
	})
	workerMu.Lock()
	workerEnv = nil // new companion process starts with no live ACP workers
	workerMu.Unlock()
	hello2 := assistantRuntimeHello()
	hello2["runtime"], hello2["agent"], hello2["launcher"] = agyRuntimeID, agyRuntimeID, agyRuntimeID
	hello2["instance"] = "agy-inst-2"
	hello2["caps"].(map[string]any)["assistant_environment"] = true
	host3 := dialRuntime(t, n, hello2)
	bindWorker(host3)
	if code, _ := send(t, "GET", heldBase+"/api/v1/me", ""); code != http.StatusNotFound {
		t.Fatalf("old EXAMPLE_BASE after different-instance restart = %d, want 404", code)
	}
	if code, _ := send(t, "GET", heldRelay+"/items", ""); code != http.StatusNotFound {
		t.Fatalf("old RELAY_BASE after different-instance restart = %d, want 404", code)
	}
	if _, err := nodeCall(t, n, "assistant.send", map[string]any{"session_id": sid, "launcher": agyRuntimeID, "cwd": cwd, "text": "turn on restarted instance"}); err != nil {
		t.Fatal(err)
	}
	workerMu.Lock()
	newBase, newRelay := workerEnv["EXAMPLE_BASE"], workerEnv["RELAY_BASE"]
	workerMu.Unlock()
	if !gateProxyURLRe.MatchString(newBase) || newBase == heldBase || !gateProxyURLRe.MatchString(newRelay) || newRelay == heldRelay {
		t.Fatalf("restarted instance did not receive fresh URLs: got %q/%q old %q/%q", newBase, newRelay, heldBase, heldRelay)
	}
	if code, _ := send(t, "GET", newBase+"/api/v1/me", ""); code != http.StatusOK {
		t.Fatalf("new instance EXAMPLE_BASE returned %d, want 200", code)
	}

	// 5. Overlapping different-instance preemption (external agy taking over from built-in agy) revokes the preempted instance's URLs.
	n.runtimeHost.agy.mu.Lock()
	n.runtimeHost.agy.instance = "agy-inst-2"
	n.runtimeHost.agy.mu.Unlock()
	workerMu.Lock()
	workerEnv = nil
	workerMu.Unlock()
	hello3 := assistantRuntimeHello()
	hello3["runtime"], hello3["agent"], hello3["launcher"] = agyRuntimeID, agyRuntimeID, agyRuntimeID
	hello3["instance"] = "agy-inst-external"
	hello3["caps"].(map[string]any)["assistant_environment"] = true
	host4 := dialRuntime(t, n, hello3)
	bindWorker(host4)
	waitFor(t, "preempted built-in connection removed", func() bool {
		n.runtimes.mu.Lock()
		defer n.runtimes.mu.Unlock()
		return len(n.runtimes.conns) == 1
	})
	if code, _ := send(t, "GET", newBase+"/api/v1/me", ""); code != http.StatusNotFound {
		t.Fatalf("preempted built-in EXAMPLE_BASE returned %d, want 404", code)
	}
	if _, err := nodeCall(t, n, "assistant.send", map[string]any{"session_id": sid, "launcher": agyRuntimeID, "cwd": cwd, "text": "turn after preemption"}); err != nil {
		t.Fatal(err)
	}
	workerMu.Lock()
	extBase := workerEnv["EXAMPLE_BASE"]
	workerMu.Unlock()
	if !gateProxyURLRe.MatchString(extBase) || extBase == newBase {
		t.Fatalf("external takeover did not receive fresh URL: got %q old %q", extBase, newBase)
	}
	if code, _ := send(t, "GET", extBase+"/api/v1/me", ""); code != http.StatusOK {
		t.Fatalf("external takeover EXAMPLE_BASE returned %d, want 200", code)
	}
	if relayCalls.Load() < 5 {
		t.Fatalf("expected relayed GETs to reach hub gate.relay across close/resume/reconnect, got %d", relayCalls.Load())
	}
}

func TestRuntimeAssistantEnvironmentDisconnectBetweenHubConnectionsReleasesKeeperLeases(t *testing.T) {
	api := newFakeAPI(t)
	keeper := newAssistantKeeper()
	// Short: t.TempDir() carries this long test name, and the socket path must stay under the unix limit (108 bytes on Linux).
	sockDir, err := os.MkdirTemp("", "rte")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	shared := NewRuntimeHost("test", filepath.Join(sockDir, "rt.sock"))
	shared.logger = log.New(io.Discard, "", 0)
	shared.leaseDisconnectGrace = 40 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := shared.Listen(ctx); err != nil {
		t.Fatal(err)
	}

	n, hub := runtimeNodeOn(t, shared)
	n.keeper = keeper
	t.Cleanup(func() { n.gates().close() })
	assistantHub(hub)
	_ = prepareRuntimeAssistantEnvironment(t, n, true)
	n.mu.Lock()
	n.larkCfg.Gates[0].Env["EXAMPLE_BASE"] = api.URL
	n.mu.Unlock()

	hello := assistantRuntimeHello()
	hello["caps"].(map[string]any)["assistant_environment"] = true
	host := dialRuntime(t, n, hello)
	startedAssistant(t, n, host)

	base := runtimeAssistantEnvironment(t, host.got("session.start")[0])["EXAMPLE_BASE"]
	if !gateProxyURLRe.MatchString(base) {
		t.Fatalf("EXAMPLE_BASE = %q, want proxy address", base)
	}
	if code, _ := send(t, "GET", base+"/api/v1/me", ""); code != http.StatusOK {
		t.Fatalf("read before detach = %d, want 200", code)
	}

	// Detach simulates the gap between two hub connections on a resident node.
	shared.detach(n)
	_ = host.conn.Close()
	waitFor(t, "keeper leases released while hub detached", func() bool {
		gp := keeper.gp
		if gp == nil {
			return false
		}
		gp.mu.Lock()
		defer gp.mu.Unlock()
		return len(gp.sessionLeases) == 0 && len(gp.keys) == 0
	})
	if code, _ := send(t, "GET", base+"/api/v1/me", ""); code != http.StatusNotFound {
		t.Fatalf("lease after detached runtime disconnect = %d, want 404", code)
	}
}

func TestRuntimeAssistantEnvironmentInSpaceSemantics(t *testing.T) {
	api := newFakeAPI(t)
	n, hub := runtimeNode(t)
	n.runtimeHost.leaseDisconnectGrace = 40 * time.Millisecond
	t.Cleanup(func() { n.gates().close() })
	assistantHub(hub)
	hub.mu.Lock()
	hub.resolve = func(proto.Interaction) proto.Resolution { return proto.Resolution{Allow: boolp(true)} }
	hub.mu.Unlock()

	skills := filepath.Join(n.home, ".persona", "skills")
	t.Setenv("PERSONA_SKILLS_DIR", skills)
	t.Setenv("PERSONA_PLUGIN_SKILLS_DIR", t.TempDir())
	n.dir = filepath.Join(n.home, ".persona")
	script := filepath.Join(skills, "example", "scripts", "ex.sh")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skills, "example", "SKILL.md"), []byte("---\nname: example\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	healthCwd := prepareAssistantForTest(t, n, filepath.Join(".persona", identity.SpaceHealth), nil, nil)
	n.mu.Lock()
	n.larkCfg, n.larkCfgAt = proto.AssistantLark{Gates: []proto.CommandGate{proxiedGate(api.URL, sendAllowed)}}, time.Now().Add(time.Hour)
	n.mu.Unlock()

	hello := assistantRuntimeHello()
	hello["caps"].(map[string]any)["assistant_environment"] = true
	host := dialRuntime(t, n, hello)
	host.answer = func(method string, _ json.RawMessage) (any, error) {
		if method == "session.start" {
			return map[string]any{"session": proto.Session{SessionID: "session-health-1", Cwd: healthCwd, Status: proto.StatusIdle}}, nil
		}
		return map[string]any{"ok": true}, nil
	}

	raw, err := nodeCall(t, n, "assistant.send", map[string]any{"launcher": "dsh", "cwd": healthCwd, "text": "hello health"})
	if err != nil {
		t.Fatal(err)
	}
	var started struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(raw, &started)
	sid := started.SessionID
	if sid != "session-health-1" {
		t.Fatalf("assistant.send = %s", raw)
	}

	env := runtimeAssistantEnvironment(t, host.got("session.start")[0])
	if env["PERSONA_SPACE"] != identity.SpaceHealth {
		t.Fatalf("PERSONA_SPACE = %q, want %q", env["PERSONA_SPACE"], identity.SpaceHealth)
	}
	base := env["EXAMPLE_BASE"]
	if !gateProxyURLRe.MatchString(base) {
		t.Fatalf("EXAMPLE_BASE = %q, want proxy address", base)
	}
	if !n.gates().spaceSession(sid) {
		t.Fatalf("expected gateProxy.markSpace(%q) to be latched", sid)
	}

	// Reads pass without a card; rule-allowed writes still require phone confirmation in a space.
	if code, _ := send(t, "GET", base+"/api/v1/me", ""); code != http.StatusOK {
		t.Fatalf("space read = %d, want 200", code)
	}
	msgURL := base + "/api/v1/conversations/7123/messages"
	if code, body := send(t, "POST", msgURL, `{"text":"hi"}`); code != http.StatusForbidden || !strings.Contains(body, "手机上确认") {
		t.Fatalf("unconfirmed space write = %d %s, want 403", code, body)
	}
	// A grant for another session cannot be consumed by this space session's lease.
	n.gates().grant("ex", "session-other", larkCall{Method: "POST", Path: "/conversations/7123/messages", Body: `{"text":"hi"}`}, proto.LarkLetThrough{Kind: proto.LarkThroughCard})
	if code, _ := send(t, "POST", msgURL, `{"text":"hi"}`); code != http.StatusForbidden {
		t.Fatalf("cross-session grant accepted in space: %d", code)
	}

	// Even when larkCheck runs outside healthCwd (after a cd), spaceSession keeps confirm-writes + empty AlwaysKeys.
	ix := proto.Interaction{SessionID: sid, ExpiresAt: time.Now().Add(time.Minute)}
	if d, _, err := n.larkCheck(context.Background(), sid, t.TempDir(), `ex POST /conversations/7123/messages '{"text":"hi"}'`, ix); err != nil || d != "allow" {
		t.Fatalf("larkCheck = %q (%v), want allow", d, err)
	}
	hub.mu.Lock()
	asked := append([]proto.Interaction(nil), hub.asked...)
	hub.mu.Unlock()
	if len(asked) != 1 || len(asked[0].AlwaysKeys) != 0 {
		t.Fatalf("space confirmation card = %+v, want 1 card with empty AlwaysKeys", asked)
	}
	if code, _ := send(t, "POST", msgURL, `{"text":"hi"}`); code != http.StatusOK {
		t.Fatalf("confirmed space write = %d, want 200", code)
	}
	if code, _ := send(t, "POST", msgURL, `{"text":"hi"}`); code != http.StatusForbidden {
		t.Fatalf("replayed space write = %d, want 403", code)
	}

	// Subsequent turn reuses the same address and preserves space enforcement.
	if _, err := nodeCall(t, n, "assistant.send", map[string]any{"session_id": sid, "launcher": "dsh", "cwd": healthCwd, "text": "turn 2"}); err != nil {
		t.Fatal(err)
	}
	if got := runtimeAssistantEnvironment(t, host.got("session.send")[1]); got["EXAMPLE_BASE"] != base {
		t.Fatalf("turn 2 EXAMPLE_BASE = %q, want reused %q", got["EXAMPLE_BASE"], base)
	}

	// Interrupt-only assistant session.close keeps the proxy lease live and preserves space confirmation enforcement.
	if _, err := nodeCall(t, n, "session.close", map[string]any{"session_id": sid}); err != nil {
		t.Fatal(err)
	}
	if code, _ := send(t, "GET", base+"/api/v1/me", ""); code != http.StatusOK {
		t.Fatalf("space lease after assistant session.close returned %d, want 200", code)
	}
	if code, _ := send(t, "POST", msgURL, `{"text":"hi"}`); code != http.StatusForbidden {
		t.Fatalf("unconfirmed space write after assistant session.close returned %d, want 403", code)
	}
	if _, err := nodeCall(t, n, "assistant.send", map[string]any{"session_id": sid, "launcher": "dsh", "cwd": healthCwd, "text": "turn 3"}); err != nil {
		t.Fatal(err)
	}
	if got := runtimeAssistantEnvironment(t, host.got("session.send")[2]); got["EXAMPLE_BASE"] != base {
		t.Fatalf("turn 3 EXAMPLE_BASE = %q, want reused %q", got["EXAMPLE_BASE"], base)
	}

	// Permanent runtime disconnect revokes the proxy URL while keeping the space latch intact.
	_ = host.conn.Close()
	waitFor(t, "space lease released on disconnect", func() bool {
		gp := n.gates()
		gp.mu.Lock()
		defer gp.mu.Unlock()
		return len(gp.sessionLeases) == 0 && len(gp.keys) == 0
	})
	if code, _ := send(t, "GET", base+"/api/v1/me", ""); code != http.StatusNotFound {
		t.Fatalf("disconnected space lease returned %d, want 404", code)
	}
	if !n.gates().spaceSession(sid) {
		t.Fatalf("space latch cleared on disconnect; must stay latched")
	}
}

func TestGateProxyOpenSessionConcurrentAndMonotonic(t *testing.T) {
	p := newGateProxy()
	t.Cleanup(p.close)

	if _, err := p.openSession("ex", "", false, false); err == nil {
		t.Fatal("openSession with empty session id must fail")
	}

	const workers = 16
	results := make([]string, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := range workers {
		go func(idx int) {
			defer wg.Done()
			base, err := p.openSession("ex", "sess-concurrent", false, false)
			if err != nil {
				t.Errorf("worker %d: %v", idx, err)
				return
			}
			results[idx] = base
		}(i)
	}
	wg.Wait()
	for i := 1; i < workers; i++ {
		if results[i] == "" || results[i] != results[0] {
			t.Fatalf("concurrent openSession returned distinct addresses: %q vs %q", results[0], results[i])
		}
	}

	// Space and remote flags must latch monotonically and never downgrade on subsequent calls.
	base, err := p.openSession("ex", "sess-concurrent", true, false)
	if err != nil || base != results[0] {
		t.Fatalf("latch space: base=%q err=%v", base, err)
	}
	base, err = p.openSession("ex", "sess-concurrent", false, true)
	if err != nil || base != results[0] {
		t.Fatalf("latch remote: base=%q err=%v", base, err)
	}
	base, err = p.openSession("ex", "sess-concurrent", false, false)
	if err != nil || base != results[0] {
		t.Fatalf("re-acquire: base=%q err=%v", base, err)
	}
	key := base[strings.LastIndex(base, "/")+1:]
	p.mu.Lock()
	gk := p.keys[key]
	keyCount, leaseCount := len(p.keys), len(p.sessionLeases)
	p.mu.Unlock()
	if !gk.space || !gk.remote || gk.session != "sess-concurrent" || gk.command != "ex" {
		t.Fatalf("latched gateKey = %+v, want space=true remote=true", gk)
	}
	if keyCount != 1 || leaseCount != 1 {
		t.Fatalf("expected 1 key and 1 lease, got keys=%d leases=%d", keyCount, leaseCount)
	}

	p.releaseSession("sess-concurrent")
	p.mu.Lock()
	keyCount, leaseCount = len(p.keys), len(p.sessionLeases)
	p.mu.Unlock()
	if keyCount != 0 || leaseCount != 0 {
		t.Fatalf("after releaseSession: keys=%d leases=%d, want 0/0", keyCount, leaseCount)
	}

	// Session-level space/remote latches carry forward to subsequent leases for the same session.
	p.markSpace("sess-concurrent")
	p.markRemote("sess-concurrent")
	reopened, err := p.openSession("ex2", "sess-concurrent", false, false)
	if err != nil {
		t.Fatalf("openSession after markSpace/markRemote: %v", err)
	}
	reopenedKey := reopened[strings.LastIndex(reopened, "/")+1:]
	p.mu.Lock()
	reopenedGK := p.keys[reopenedKey]
	p.mu.Unlock()
	if !reopenedGK.space || !reopenedGK.remote {
		t.Fatalf("reopened gateKey = %+v, want space=true remote=true inherited from session latches", reopenedGK)
	}
}

func TestRuntimeAssistantEnvironmentCapabilityBoundary(t *testing.T) {
	n, hub := runtimeNode(t)
	assistantHub(hub)
	prepareRuntimeAssistantEnvironment(t, n, false)
	host := dialRuntime(t, n, assistantRuntimeHello())
	startedAssistant(t, n, host)
	if _, err := nodeCall(t, n, "session.modes", map[string]any{"session_id": "session-a1", "permission_mode": "plan"}); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"session.start", "session.send", "session.modes"} {
		for _, raw := range host.got(method) {
			if strings.Contains(string(raw), `"env":`) {
				t.Fatalf("runtime without assistant_environment received an environment: %s %s", method, raw)
			}
		}
	}
}

// Local runtimes accepting dedicated-worker environments must receive per-session PERSONA_CATEGORIES on subsequent
// turns, session.modes, and session.fork, and omit PERSONA_CATEGORIES when categories_unknown is set rather than
// falling back to the directory's default general value.
func TestRuntimeAssistantEnvironmentHonorsPerSessionCategories(t *testing.T) {
	n, hub := runtimeNode(t)
	assistantHub(hub)
	cwd := prepareRuntimeAssistantEnvironment(t, n, false)
	prepareCategoriesDir(t, n, ".persona/assistant", []string{})
	hello := assistantRuntimeHello()
	hello["caps"].(map[string]any)["assistant_environment"] = true
	hello["caps"].(map[string]any)["fork"] = true
	host := dialRuntime(t, n, hello)
	forkSeq := 0
	host.answer = func(method string, params json.RawMessage) (any, error) {
		switch method {
		case "session.start":
			return map[string]any{"session": proto.Session{SessionID: "session-a1", Cwd: cwd, Status: proto.StatusIdle}}, nil
		case "session.fork":
			forkSeq++
			fid := "session-a1-fork-" + string(rune('0'+forkSeq))
			return map[string]any{"session_id": fid, "session": proto.Session{SessionID: fid, Cwd: cwd, Status: proto.StatusIdle}}, nil
		default:
			return map[string]any{"ok": true}, nil
		}
	}
	raw, err := nodeCall(t, n, "assistant.send", map[string]any{"launcher": "dsh", "cwd": cwd, "text": "turn 1"})
	if err != nil {
		t.Fatal(err)
	}
	var started struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(raw, &started)
	id := started.SessionID
	if id != "session-a1" {
		t.Fatalf("assistant.send = %s", raw)
	}
	if err := host.peer.Notify("runtime.sessions", map[string]any{"sessions": []proto.Session{
		{SessionID: id, Cwd: cwd, Status: proto.StatusIdle},
	}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "runtime session listed", func() bool {
		_, _, ok := n.runtimes.rowOf(id)
		return ok
	})

	// Turn 1 uses the prepared directory's default general categories (PERSONA_CATEGORIES=).
	startEnv := runtimeAssistantEnvironment(t, host.got("session.start")[0])
	if val, ok := startEnv[categoriesEnv]; !ok || val != "" {
		t.Fatalf("session.start %s: ok=%v val=%q, want empty string", categoriesEnv, ok, val)
	}
	turn1Env := runtimeAssistantEnvironment(t, host.got("session.send")[0])
	if val, ok := turn1Env[categoriesEnv]; !ok || val != "" {
		t.Fatalf("turn 1 session.send %s: ok=%v val=%q, want empty string", categoriesEnv, ok, val)
	}

	// Turn 2 with accumulated sensitive categories updates session.send, session.modes, and session.fork env.
	if _, err := nodeCall(t, n, "assistant.send", map[string]any{
		"session_id": id, "launcher": "dsh", "cwd": cwd, "text": "turn 2",
		"categories": []string{"finance", "emotion"},
	}); err != nil {
		t.Fatal(err)
	}
	turn2Env := runtimeAssistantEnvironment(t, host.got("session.send")[1])
	if val, ok := turn2Env[categoriesEnv]; !ok || val != "emotion,finance" {
		t.Fatalf("turn 2 session.send %s: ok=%v val=%q, want emotion,finance", categoriesEnv, ok, val)
	}
	if _, err := nodeCall(t, n, "session.modes", map[string]any{"session_id": id, "permission_mode": "plan"}); err != nil {
		t.Fatal(err)
	}
	modes1Env := runtimeAssistantEnvironment(t, host.got("session.modes")[0])
	if val, ok := modes1Env[categoriesEnv]; !ok || val != "emotion,finance" {
		t.Fatalf("session.modes %s: ok=%v val=%q, want emotion,finance", categoriesEnv, ok, val)
	}
	if _, err := nodeCall(t, n, "session.fork", map[string]any{"session_id": id, "at_item_id": "item-1"}); err != nil {
		t.Fatal(err)
	}
	fork1Env := runtimeAssistantEnvironment(t, host.got("session.fork")[0])
	if val, ok := fork1Env[categoriesEnv]; !ok || val != "emotion,finance" {
		t.Fatalf("session.fork %s: ok=%v val=%q, want emotion,finance", categoriesEnv, ok, val)
	}

	// Turn 3 with categories_unknown omits PERSONA_CATEGORIES across session.send, session.modes, and session.fork.
	if _, err := nodeCall(t, n, "session.send", map[string]any{
		"session_id": id, "text": "turn 3", "categories_unknown": true,
	}); err != nil {
		t.Fatal(err)
	}
	turn3Env := runtimeAssistantEnvironment(t, host.got("session.send")[2])
	if val, ok := turn3Env[categoriesEnv]; ok {
		t.Fatalf("turn 3 session.send %s: got %q, want absent", categoriesEnv, val)
	}
	if _, err := nodeCall(t, n, "session.modes", map[string]any{"session_id": id, "permission_mode": "auto"}); err != nil {
		t.Fatal(err)
	}
	modes2Env := runtimeAssistantEnvironment(t, host.got("session.modes")[1])
	if val, ok := modes2Env[categoriesEnv]; ok {
		t.Fatalf("session.modes unknown %s: got %q, want absent", categoriesEnv, val)
	}
	if _, err := nodeCall(t, n, "session.fork", map[string]any{"session_id": id, "at_item_id": "item-2"}); err != nil {
		t.Fatal(err)
	}
	fork2Env := runtimeAssistantEnvironment(t, host.got("session.fork")[1])
	if val, ok := fork2Env[categoriesEnv]; ok {
		t.Fatalf("session.fork unknown %s: got %q, want absent", categoriesEnv, val)
	}
}
