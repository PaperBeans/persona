package sandbox

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/extension/egress"
)

// nodeProbeScript is what a legacy Node connector does with Node's own clients: fetch and WebSocket to its service
// (service.test:8080, the one host its owner allowed), the same to a host and a port it was not allowed, and a raw
// TCP connection that skips the proxy.
const nodeProbeScript = `
const net = require("node:net");
const say = (what, v) => console.log(what + ": " + v);
async function tryFetch(u) {
  try {
    const r = await fetch(u, { signal: AbortSignal.timeout(15000) });
    return "status " + r.status + " " + (await r.text()).slice(0, 200).replace(/\s+/g, " ");
  } catch (e) {
    return "error " + ((e.cause && (e.cause.code || e.cause.message)) || e.message);
  }
}
function tryWS(u) {
  return new Promise((done) => {
    let ws;
    try { ws = new WebSocket(u); } catch (e) { done("error " + e.message); return; }
    const t = setTimeout(() => { done("error timeout"); try { ws.close(); } catch {} }, 15000);
    ws.onopen = () => ws.send("ping");
    ws.onmessage = (ev) => { clearTimeout(t); done("message " + ev.data); ws.close(); };
    ws.onerror = () => { clearTimeout(t); done("error"); };
  });
}
function tryDirect() {
  return new Promise((done) => {
    const s = net.connect({ host: "192.0.2.1", port: 80 });
    s.setTimeout(5000, () => { s.destroy(); done("error timeout"); });
    s.on("connect", () => { s.destroy(); done("connected"); });
    s.on("error", (e) => done("error " + e.code));
  });
}
(async () => {
  say("node", process.version);
  say("fetch allowed", await tryFetch("http://service.test:8080/health"));
  say("fetch other host", await tryFetch("http://denied.test:8080/health"));
  say("fetch other port", await tryFetch("http://service.test:9090/health"));
  say("ws allowed", await tryWS("ws://service.test:8080/ws"));
  say("ws other host", await tryWS("ws://denied.test:8080/ws"));
  say("direct", await tryDirect());
})();
`

// nodeForSandbox is a node binary of a version whose fetch and WebSocket honor NODE_USE_ENV_PROXY (24.0 and later),
// and the directory to bind for it when it lives outside the system directories; skips the test otherwise.
func nodeForSandbox(t *testing.T) (bin, tools string) {
	t.Helper()
	path, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node here")
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Skip("node: " + err.Error())
	}
	out, err := exec.Command(real, "-p", "process.versions.node").Output()
	if err != nil {
		t.Skip("node --version: " + err.Error())
	}
	version := strings.TrimSpace(string(out))
	major, _ := strconv.Atoi(strings.SplitN(version, ".", 2)[0])
	if major < 24 {
		t.Skip("node " + version + " is older than 24: its fetch has no NODE_USE_ENV_PROXY")
	}
	prefix := filepath.Dir(filepath.Dir(real)) // <prefix>/bin/node
	for _, d := range systemDirs {
		if prefix == d || strings.HasPrefix(prefix, d+"/") {
			return real, ""
		}
	}
	return real, prefix // e.g. a version manager's directory under the home directory, which the sandbox hides
}

// TestLinuxSandboxNodeEgressForReal (#15707): a Node program in the real Linux sandbox, with Node's native fetch and
// WebSocket and no proxy library, reaches through the egress proxy exactly the service its owner allowed it (a
// connector whose address is in its sign-in record, not in a permissions.network entry): another host or port is
// refused by the proxy with the sandbox named, and a direct connection has no network at all. Without
// NODE_USE_ENV_PROXY the same program never reaches the proxy, which is how the bug looked.
func TestLinuxSandboxNodeEgressForReal(t *testing.T) {
	sup := Probe()
	if sup.Bwrap == "" {
		t.Skip("no sandbox here: " + sup.Why)
	}
	node, tools := nodeForSandbox(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		_, msg, err := c.Read(ctx)
		if err != nil {
			return
		}
		_ = c.Write(ctx, websocket.MessageText, append([]byte("echo "), msg...))
		_ = c.Close(websocket.StatusNormalClosure, "")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	px := testProxy(t, strings.TrimPrefix(srv.URL, "http://"))
	var logMu sync.Mutex
	var logged []string
	px.Logf = func(f string, a ...any) {
		logMu.Lock()
		logged = append(logged, fmt.Sprintf(f, a...))
		logMu.Unlock()
	}
	// What the owner allowed it in the admin UI: one host on one port; no permissions.network at all.
	px.Register("connector", egress.Rule{Owner: []egress.Target{{Host: "service.test", Port: "8080"}}})

	pkg, data, run := t.TempDir(), t.TempDir(), t.TempDir()
	script := filepath.Join(pkg, "probe.cjs")
	if err := os.WriteFile(script, []byte(nodeProbeScript), 0o600); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(run, "proxy.sock")
	if err := px.ListenUnix("connector", sock); err != nil {
		t.Fatal(err)
	}
	self, _ := os.Executable()
	plan := Plan{ID: "connector", Command: node, Args: []string{script}, Pkg: pkg, Data: data, Run: run, Tools: tools,
		ProxySocket: sock, Self: self}
	runNode := func(useEnvProxy bool) map[string]string {
		t.Helper()
		name, args, env := Linux(sup.Bwrap, plan)
		cmd := exec.Command(name, args...)
		for _, kv := range append(os.Environ(), env...) {
			if strings.HasPrefix(kv, "NODE_USE_ENV_PROXY=") && !useEnvProxy || strings.HasPrefix(kv, "NODE_OPTIONS=") {
				continue
			}
			cmd.Env = append(cmd.Env, kv)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("node in the sandbox: %v\n%s", err, out)
		}
		t.Logf("NODE_USE_ENV_PROXY=%v:\n%s", useEnvProxy, out)
		got := map[string]string{}
		for line := range strings.SplitSeq(string(out), "\n") {
			if k, v, ok := strings.Cut(line, ": "); ok {
				got[k] = v
			}
		}
		return got
	}

	got := runNode(true)
	if v := got["fetch allowed"]; v != "status 200 ok" {
		t.Errorf("fetch to the allowed service: %q", v)
	}
	if v := got["ws allowed"]; v != "message echo ping" {
		t.Errorf("WebSocket to the allowed service: %q", v)
	}
	for _, k := range []string{"fetch other host", "fetch other port"} {
		if v := got[k]; v == "" || strings.HasPrefix(v, "status 200") {
			t.Errorf("%s was not refused: %q", k, v)
		}
	}
	if v := got["ws other host"]; v == "" || strings.HasPrefix(v, "message") {
		t.Errorf("WebSocket to another host was not refused: %q", v)
	}
	if v := got["direct"]; !strings.HasPrefix(v, "error") {
		t.Errorf("a direct connection left the sandbox: %q", v)
	}
	st := px.Stats("connector")
	// Counted per connection to the proxy: Node may carry the fetch and the WebSocket over one tunnel.
	if st.Allowed < 1 || st.Denied < 3 {
		t.Errorf("proxy counts %+v: Node's own clients did not go through the proxy", st)
	}
	logMu.Lock()
	seen := slices.Clone(logged)
	logMu.Unlock()
	for _, want := range []string{"connector: sandbox egress refused denied.test:8080 ", "connector: sandbox egress refused service.test:9090 "} {
		found := false
		for _, l := range seen {
			found = found || strings.Contains(l, want)
		}
		if !found {
			t.Errorf("no log line %q in %q", want, seen)
		}
	}

	// The bug: without NODE_USE_ENV_PROXY Node ignores HTTP(S)_PROXY, dials straight out of a network namespace
	// with nothing but loopback, and the proxy never hears of it.
	before := px.Stats("connector")
	got = runNode(false)
	if v := got["fetch allowed"]; strings.HasPrefix(v, "status 200") {
		t.Errorf("without NODE_USE_ENV_PROXY the fetch should not leave the sandbox: %q", v)
	}
	if after := px.Stats("connector"); after.Allowed != before.Allowed || after.Denied != before.Denied {
		t.Errorf("without NODE_USE_ENV_PROXY the proxy saw requests: %+v then %+v", before, after)
	}
}
