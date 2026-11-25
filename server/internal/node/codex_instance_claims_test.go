package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// claimsTestNode is a node with a runtime socket and a fake hub (runtimeNode) that also follows Codex app-servers, as
// remoteTestNode's does.
func claimsTestNode(t *testing.T) *Node {
	t.Helper()
	n, _ := runtimeNode(t)
	n.codices = newCodexRegistry()
	n.codices.scan = func() []codexProcess { return nil }
	n.dir = filepath.Join(n.home, ".persona")
	n.configureCodex(proto.CodexConfig{})
	return n
}

// claimHello is a runtime's hello that claims the Codex app-server instances claims name.
func claimHello(claims ...map[string]any) map[string]any {
	return map[string]any{"runtime": "codexd", "name": "Codex daemon", "version": "test", "caps": map[string]any{"send": true},
		"codex_claims": claims}
}

// remoteOf is the node's connection to Codex app-server id, nil when it has none.
func remoteOf(n *Node, id string) *codexRemote {
	n.codices.mu.Lock()
	defer n.codices.mu.Unlock()
	return n.codices.remotes[id]
}

// threadCalls is how many thread/* requests the fake app-server got.
func (f *remoteFixture) threadCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := 0
	for _, m := range f.got {
		if strings.HasPrefix(m.Method, "thread/") {
			calls++
		}
	}
	return calls
}

// conns is how many connections the fake app-server is serving now, and how many it accepted in all.
func (f *remoteFixture) conns() (open, accepted int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.open, f.accepted
}

// runtimeConns is how many runtime connections the node has.
func runtimeConns(n *Node) int {
	n.runtimes.mu.Lock()
	defer n.runtimes.mu.Unlock()
	return len(n.runtimes.conns)
}

// A runtime that serves an app-server's chats itself claims the instance in its hello: the node closes its own
// connection to it before the hello is answered, does not dial it again while the claim holds, and the instance's
// chats are listed only as the runtime's rows, the thread the runtime does not name included (#12571).
func TestCodexClaimLeavesTheInstanceToTheRuntime(t *testing.T) {
	n := claimsTestNode(t)
	home := t.TempDir()
	f, c := connectRemoteFixture(t, n, home, "daemon", remoteRaw, remoteOther)
	spec := c.spec
	legacy := n.codexInventory().LegacyHome
	named, unnamed := codexSourceID(spec.Home, remoteRaw, legacy), codexSourceID(spec.Home, remoteOther, legacy)
	waitFor(t, "both threads listed from the app-server", func() bool {
		l := listed(t, n)
		_, a := l[named]
		_, b := l[unnamed]
		return a && b
	})

	_, dials := f.conns()
	// The home as the runtime knows it: macOS's TempDir is under /var -> /private/var, the node's is resolved.
	rt := dialRuntime(t, n, claimHello(map[string]any{"home": home}))
	if features, _ := json.Marshal(rt.helloR["features"]); !strings.Contains(string(features), `"codex_claims"`) {
		t.Fatalf("features %s", features)
	}
	if remoteOf(n, "daemon") != nil {
		t.Fatal("the node still follows the instance once the hello claiming it was answered")
	}
	select {
	case <-c.done:
	default:
		t.Fatal("the connection to the claimed instance is still open")
	}

	// A request already on the wire when the connection closed is in got once the app-server saw the connection end.
	waitFor(t, "the app-server saw the connection end", func() bool { open, _ := f.conns(); return open == 0 })
	calls := f.threadCalls()
	for range 3 {
		// attachCodexServers is synchronous, and a dial stores its connection before it runs: one shows at once.
		n.attachCodexServers(context.Background(), []proto.CodexServer{spec})
		if remoteOf(n, "daemon") != nil {
			t.Fatal("the node dialled the claimed instance again")
		}
	}
	if got := f.threadCalls(); got != calls {
		t.Fatalf("the claimed app-server got %d thread/* calls after the claim", got-calls)
	}
	if _, accepted := f.conns(); accepted != dials {
		t.Fatalf("the claimed app-server was dialled %d more time(s)", accepted-dials)
	}

	_ = rt.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{{"session_id": "codexd-1", "status": "idle", "codex_thread": remoteRaw}}})
	waitFor(t, "the runtime's row", func() bool { _, ok := listed(t, n)["codexd-1"]; return ok })
	l := listed(t, n)
	if _, ok := l[named]; ok {
		t.Fatal("the thread the runtime names is listed from the claimed instance as well")
	}
	if _, ok := l[unnamed]; ok {
		t.Fatal("a thread the runtime does not name is still listed from the claimed instance")
	}
}

// A claim outlives its runtime's last connection for the keep, so a runtime that restarts does not have the node attach
// to the instance, list its threads under the node's own rows and drop those again once the runtime is back (its own
// rows leave the list meanwhile, as any runtime's do); once the keep passes with no new connection, the node attaches
// to the instance again, with no restart of its own.
func TestCodexClaimOutlivesAShortDisconnect(t *testing.T) {
	n := claimsTestNode(t)
	home := t.TempDir()
	f, c := connectRemoteFixture(t, n, home, "daemon", remoteRaw)
	spec := c.spec
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	attach := func() *codexRemote {
		n.attachCodexServers(ctx, []proto.CodexServer{spec})
		return remoteOf(n, "daemon")
	}
	// left is when the runtime's last connection left, as its claims count it; zero while it is connected.
	left := func() time.Time {
		n.runtimes.mu.Lock()
		defer n.runtimes.mu.Unlock()
		return n.runtimes.codexClaims["codexd"].gone
	}
	hello := claimHello(map[string]any{"endpoint": spec.Endpoint}) // by its endpoint this time

	rt := dialRuntime(t, n, hello)
	if remoteOf(n, "daemon") != nil {
		t.Fatal("the instance was not left to the runtime")
	}
	_ = rt.conn.Close()
	waitFor(t, "the runtime gone", func() bool { return runtimeConns(n) == 0 })
	if left().IsZero() {
		t.Fatal("the claims of a runtime that left do not count its keep")
	}
	if attach() != nil {
		t.Fatal("the claim ended with its runtime's connection")
	}
	rt = dialRuntime(t, n, hello) // back within the keep
	if !left().IsZero() {
		t.Fatal("the claims of a runtime that came back still count the keep of its last connection")
	}
	if attach() != nil {
		t.Fatal("the claim of a runtime that came back ended")
	}

	_ = rt.conn.Close()
	waitFor(t, "the runtime gone again", func() bool { return runtimeConns(n) == 0 })
	if attach() != nil {
		t.Fatal("the claim ended before the keep passed")
	}
	// The keep passes: the runtime left longer ago than codexClaimKeep.
	n.runtimes.mu.Lock()
	claims := n.runtimes.codexClaims["codexd"]
	claims.gone = time.Now().Add(-codexClaimKeep - time.Second)
	n.runtimes.codexClaims["codexd"] = claims
	n.runtimes.mu.Unlock()
	lists := f.count("thread/loaded/list")
	attach()
	waitFor(t, "the node attached again", func() bool {
		r := remoteOf(n, "daemon")
		if r == nil {
			return false
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.ready
	})
	if f.count("thread/loaded/list") <= lists {
		t.Fatal("the instance was not read again once the claim ended")
	}
	if held := n.runtimes.heldCodexClaims(); len(held) != 0 {
		t.Fatalf("an ended claim is still kept: %v", held)
	}
}

// A claim names an instance by its home, its endpoint, or both; one that names no instance here, or that does not
// resolve, changes nothing.
func TestCodexClaimOfAnotherInstanceChangesNothing(t *testing.T) {
	n := claimsTestNode(t)
	home := t.TempDir()
	_, c := connectRemoteFixture(t, n, home, "daemon", remoteRaw)
	spec := c.spec
	dialRuntime(t, n, claimHello(
		map[string]any{"home": t.TempDir()},                                           // another home
		map[string]any{"endpoint": "ws://127.0.0.1:1"},                                // another endpoint
		map[string]any{"home": home, "endpoint": "ws://127.0.0.1:1"},                  // this home, but another endpoint
		map[string]any{"home": "relative/home"},                                       // not a path
		map[string]any{"endpoint": strings.Replace(spec.Endpoint, "ws:", "http:", 1)}, // not an app-server endpoint
	))
	if held := n.runtimes.heldCodexClaims(); len(held) != 5 {
		t.Fatalf("held %v", held)
	}
	n.attachCodexServers(context.Background(), []proto.CodexServer{spec})
	if remoteOf(n, "daemon") != c {
		t.Fatal("a claim that names another instance closed this one")
	}
	select {
	case <-c.done:
		t.Fatal("a claim that names another instance closed this one")
	default:
	}
}

// A daemon's hello claims nothing: its app-servers are another computer's, and the node does not offer it the feature.
func TestCodexClaimIsNotADaemons(t *testing.T) {
	d := startFakeDaemon(t, "tok", map[string]any{"runtime": "acme", "caps": map[string]any{"send": true},
		"codex_claims": []map[string]any{{"home": t.TempDir()}, {"endpoint": "ws://127.0.0.1:1"}}})
	rh, _ := daemonHost(t, map[string]any{"id": "acme", "url": d.url(), "token": "tok"})
	runtimeNodeOn(t, rh)
	waitFor(t, "the daemon's hello", func() bool { res, _, _ := d.hellod(); return res != nil })
	res, _, _ := d.hellod()
	if f, _ := res["features"].([]any); slices.Contains(f, any("codex_claims")) {
		t.Fatalf("a daemon was offered codex_claims: %v", f)
	}
	if held := rh.reg.heldCodexClaims(); len(held) != 0 {
		t.Fatalf("a daemon claimed %v", held)
	}
	// Even a hello that reached the node with claims (the link passes none on) claims nothing for a daemon.
	h := daemonConn(rh, "acme")
	if h == nil {
		t.Fatal("no daemon connection")
	}
	rh.claimCodex(h, []runtimeCodexClaim{{Home: "/"}})
	if held := rh.reg.heldCodexClaims(); len(held) != 0 {
		t.Fatalf("a daemon claimed %v", held)
	}
}

// The node logs when a runtime claims an instance, once while the instance stays in the inventory, and when that claim
// ends; a hello without claims ends the runtime's claims at once, with no keep.
func TestCodexClaimIsLoggedAndEndsWithAHelloWithoutClaims(t *testing.T) {
	n := remoteTestNode(t)
	logs := &syncBuffer{}
	n.logger = log.New(logs, "", 0)
	n.runtimes = newRuntimeRegistry()
	home := t.TempDir()
	// An endpoint nothing listens on: once the claim ends the node dials it and fails, which is all this test needs.
	spec := proto.CodexServer{ID: "daemon", Home: canonicalCodexHome(home, n.home), Endpoint: "ws://127.0.0.1:1"}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h := n.runtimes.add(nil)
	h.mu.Lock()
	h.hello.Runtime = "codexd"
	h.mu.Unlock()
	n.runtimes.setCodexClaims(h, cleanCodexClaims([]runtimeCodexClaim{{Home: home}}))
	n.attachCodexServers(ctx, []proto.CodexServer{spec})
	n.attachCodexServers(ctx, []proto.CodexServer{spec})
	if got := strings.Count(logs.String(), "codex daemon: runtime codexd serves it and claims it"); got != 1 {
		t.Fatalf("claim logged %d times:\n%s", got, logs)
	}
	if remoteOf(n, "daemon") != nil {
		t.Fatal("a claimed instance was dialled")
	}
	// The instance leaves the inventory while claimed: it is no longer noted, and is logged again once it is back.
	n.attachCodexServers(ctx, nil)
	n.codices.mu.Lock()
	_, noted := n.codices.claimed["daemon"]
	n.codices.mu.Unlock()
	if noted {
		t.Fatal("an instance that left the inventory is still noted as claimed")
	}
	n.attachCodexServers(ctx, []proto.CodexServer{spec})
	if got := strings.Count(logs.String(), "codex daemon: runtime codexd serves it and claims it"); got != 2 {
		t.Fatalf("claim logged %d times, want once more after the instance came back:\n%s", got, logs)
	}
	n.runtimes.setCodexClaims(h, nil) // its next hello claims nothing
	n.attachCodexServers(ctx, []proto.CodexServer{spec})
	if !strings.Contains(logs.String(), "codex daemon: runtime codexd no longer claims it; attaching again") {
		t.Fatalf("the end of the claim was not logged:\n%s", logs)
	}
}

// A runtime whose connection closed while its hello was being answered claims for the keep only, as if it had left
// right after: its claims do not stand for good with no connection to end them.
func TestCodexClaimOfAConnectionAlreadyGoneOnlyKeeps(t *testing.T) {
	b := newRuntimeRegistry()
	live := b.add(nil)
	gone := b.add(nil)
	for h, id := range map[*runtimeConn]string{live: "live", gone: "gone"} {
		h.mu.Lock()
		h.hello.Runtime = id
		h.mu.Unlock()
	}
	b.remove(gone) // before its claims are recorded
	b.setCodexClaims(live, []runtimeCodexClaim{{Home: "/a"}})
	b.setCodexClaims(gone, []runtimeCodexClaim{{Home: "/b"}})
	b.mu.Lock()
	l, g := b.codexClaims["live"], b.codexClaims["gone"]
	b.mu.Unlock()
	if !l.gone.IsZero() || g.gone.IsZero() {
		t.Fatalf("live %+v, gone %+v", l, g)
	}
	if held := b.heldCodexClaims(); len(held) != 2 {
		t.Fatalf("within the keep, held %v", held)
	}
	// The keep passes: the connection went longer ago than codexClaimKeep.
	b.mu.Lock()
	g.gone = time.Now().Add(-codexClaimKeep - time.Second)
	b.codexClaims["gone"] = g
	b.mu.Unlock()
	if held := b.heldCodexClaims(); len(held) != 1 || held[0].runtime != "live" {
		t.Fatalf("held %v", held)
	}
}

// A same-instance reconnect that replaces an earlier connection, a stale connection whose hello arrives late, a peer
// already closed before remove runs, or a conflicting hello must never leave claims permanent once no live connection
// remains: once codexClaimKeep passes, the node attaches again.
func TestCodexClaimReconnectAndStaleHelloExpires(t *testing.T) {
	n := claimsTestNode(t)
	home := t.TempDir()
	f, c := connectRemoteFixture(t, n, home, "daemon", remoteRaw)
	spec := c.spec
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	hello := func(instance string, claims ...map[string]any) map[string]any {
		h := claimHello(claims...)
		h["instance"] = instance
		return h
	}
	claim := map[string]any{"endpoint": spec.Endpoint}

	// First connection claims the instance, then the same instance reconnects (closing rt1 as stale).
	rt1 := dialRuntime(t, n, hello("inst-1", claim))
	rt2 := dialRuntime(t, n, hello("inst-1", claim))
	if remoteOf(n, "daemon") != nil {
		t.Fatal("the instance was not left to the reconnected runtime")
	}
	// A conflicting process with another instance is refused and does not disturb the claimant.
	conflictConn, err := net.Dial("unix", n.runtimeSock)
	if err != nil {
		t.Fatal(err)
	}
	conflictPeer := rpc.New("d", conflictConn)
	cctx, ccancel := context.WithCancel(context.Background())
	go func() { _ = conflictPeer.Serve(cctx, conflictConn) }()
	t.Cleanup(func() { ccancel(); _ = conflictConn.Close() })
	var conflictRes map[string]any
	callCtx, callCancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = conflictPeer.Call(callCtx, "runtime.hello", hello("inst-2", claim), &conflictRes)
	callCancel()
	if err == nil {
		t.Fatal("conflicting runtime hello succeeded, want runtime_conflict")
	}
	ccancel()
	_ = conflictConn.Close()

	// Now rt2 disconnects as well. Even before rt1's socket finishes cleanup or if a stale hello updates claims late,
	// no live connection remains so the keep clock must start and never be cleared to zero.
	_ = rt2.conn.Close()
	_ = rt1.conn.Close()
	waitFor(t, "both runtime connections gone", func() bool { return runtimeConns(n) == 0 })

	n.runtimes.mu.Lock()
	gotGone := n.runtimes.codexClaims["codexd"].gone
	n.runtimes.mu.Unlock()
	if gotGone.IsZero() {
		t.Fatal("claims have a zero keep timestamp after reconnected runtime disconnected")
	}

	// Unit-level check of the exact races:
	// 1. A superseded connection still in b.conns (before remove runs) must not block leftCodexClaims from starting
	//    the keep, and a late setCodexClaims from it or its subsequent remove must not clear/extend gone.
	// 2. A connection whose peer is already closed while still in b.conns must not clear an existing gone timestamp.
	b := newRuntimeRegistry()
	h1 := b.add(nil)
	h2 := b.add(nil)
	if stale, err := b.hello(h1, runtimeHello{Runtime: "rt", Instance: "i1"}, nil, nil); err != nil || len(stale) != 0 {
		t.Fatalf("first hello: stale=%d err=%v", len(stale), err)
	}
	if !b.setCodexClaims(h1, []runtimeCodexClaim{{Home: "/a"}}) {
		t.Fatal("first connection's claims were ignored")
	}
	stale, err := b.hello(h2, runtimeHello{Runtime: "rt", Instance: "i1"}, nil, nil)
	if err != nil || len(stale) != 1 || stale[0] != h1 {
		t.Fatalf("reconnect hello: stale=%v err=%v", stale, err)
	}
	if !b.setCodexClaims(h2, []runtimeCodexClaim{{Home: "/a"}}) {
		t.Fatal("reconnected connection's claims were ignored")
	}
	// h2 leaves while stale h1 has not been removed from b.conns yet.
	b.remove(h2)
	b.mu.Lock()
	afterH2 := b.codexClaims["rt"].gone
	b.mu.Unlock()
	if afterH2.IsZero() {
		t.Fatal("stale h1 still in b.conns prevented leftCodexClaims from starting the keep")
	}
	// Backdate gone and verify neither a late setCodexClaims from superseded h1 nor remove(h1) clears/extends it.
	earlier := time.Now().Add(-30 * time.Second)
	b.mu.Lock()
	entry := b.codexClaims["rt"]
	entry.gone = earlier
	b.codexClaims["rt"] = entry
	b.mu.Unlock()
	if b.setCodexClaims(h1, []runtimeCodexClaim{{Home: "/stale"}}) {
		t.Fatal("superseded connection's late setCodexClaims was accepted")
	}
	b.remove(h1)
	b.mu.Lock()
	afterH1 := b.codexClaims["rt"]
	b.mu.Unlock()
	if !afterH1.gone.Equal(earlier) || len(afterH1.claims) != 1 || afterH1.claims[0].Home != "/a" {
		t.Fatalf("stale cleanup altered claims or keep timestamp: %+v", afterH1)
	}
	// A newer connection whose peer closed before remove ran must preserve the existing gone timestamp, never clear it.
	p1, p2 := net.Pipe()
	t.Cleanup(func() { _ = p1.Close(); _ = p2.Close() })
	deadPeer := rpc.New("r", p1)
	deadPeer.Close(errors.New("closed"))
	h3 := b.add(deadPeer)
	if stale, err := b.hello(h3, runtimeHello{Runtime: "rt", Instance: "i1"}, nil, nil); err != nil || len(stale) != 0 {
		t.Fatalf("third hello: stale=%d err=%v", len(stale), err)
	}
	b.setCodexClaims(h3, []runtimeCodexClaim{{Home: "/a"}})
	b.mu.Lock()
	afterDead := b.codexClaims["rt"]
	b.mu.Unlock()
	if !afterDead.gone.Equal(earlier) {
		t.Fatalf("closed peer reset gone to %v, want %v", afterDead.gone, earlier)
	}

	// Back to the live node: once codexClaimKeep passes with no surviving connection, attachment resumes.
	n.runtimes.mu.Lock()
	claims := n.runtimes.codexClaims["codexd"]
	claims.gone = time.Now().Add(-codexClaimKeep - time.Second)
	n.runtimes.codexClaims["codexd"] = claims
	n.runtimes.mu.Unlock()
	lists := f.count("thread/loaded/list")
	n.attachCodexServers(ctx, []proto.CodexServer{spec})
	waitFor(t, "the node attached again after reconnect disconnect and keep expiry", func() bool {
		r := remoteOf(n, "daemon")
		if r == nil {
			return false
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.ready
	})
	if f.count("thread/loaded/list") <= lists {
		t.Fatal("the instance was not read again after keep expiry")
	}
}

// A claim's home and endpoint are compared as the node resolves its own instances': one part names the instances that
// have it, both name only the instance that has both.
func TestCodexClaimantNamesHomeEndpointOrBoth(t *testing.T) {
	n, _ := testNode(t)
	home := canonicalCodexHome(t.TempDir(), n.home)
	s := proto.CodexServer{ID: "a", Home: home, Endpoint: "ws://127.0.0.1:4500/"}
	for _, c := range []struct {
		claim runtimeCodexClaim
		want  string
	}{
		{runtimeCodexClaim{Home: home}, "rt"},
		{runtimeCodexClaim{Endpoint: "ws://127.0.0.1:4500"}, "rt"},
		{runtimeCodexClaim{Home: home, Endpoint: "ws://127.0.0.1:4500"}, "rt"},
		{runtimeCodexClaim{Home: home, Endpoint: "ws://127.0.0.1:4501"}, ""},
		{runtimeCodexClaim{Home: home + "-other"}, ""},
		{runtimeCodexClaim{Endpoint: "ws://localhost:4500"}, ""},
	} {
		held := n.resolveCodexClaims([]heldCodexClaim{{runtime: "rt", runtimeCodexClaim: c.claim}})
		if got := n.codexClaimant(s, held); got != c.want {
			t.Errorf("%+v claims %q, want %q", c.claim, got, c.want)
		}
	}
}

func TestCanonicalCodexEndpoint(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "run")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "app.sock")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias.sock")
	if err := os.Symlink(sock, alias); err != nil {
		t.Skip("no symlinks here:", err)
	}
	real := realPath(sock)
	for _, c := range []struct{ in, want string }{
		{"unix://" + sock, "unix://" + real},
		{"unix://" + alias, "unix://" + real},
		{"unix://~/run/app.sock", "unix://" + real},
		{"unix://" + filepath.Join(dir, "later.sock"), "unix://" + filepath.Join(filepath.Dir(real), "later.sock")},
		{"unix://run/app.sock", ""},
		{"ws://127.0.0.1:4500", "ws://127.0.0.1:4500"},
		{"ws://127.0.0.1:4500/", "ws://127.0.0.1:4500"},
		{"ws://[::1]:4500", "ws://[::1]:4500"},
		{"ws://localhost:4500", ""},
		{"ws://127.0.0.1", ""},
		{"ws://127.0.0.1:4500/rpc", ""},
		{"ws://me@127.0.0.1:4500", ""},
		{"ws://127.0.0.1:4500/?token=x", ""},
		{"wss://127.0.0.1:4500", ""},
		{"http://127.0.0.1:4500", ""},
		{"", ""},
	} {
		if got := canonicalCodexEndpoint(c.in, base); got != c.want {
			t.Errorf("canonicalCodexEndpoint(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCleanCodexClaims(t *testing.T) {
	long := strings.Repeat("x", maxCodexClaimText+1)
	got := cleanCodexClaims([]runtimeCodexClaim{{Home: " /a "}, {}, {Home: " "}, {Home: "/a"}, {Endpoint: long}, {Home: "/b", Endpoint: " unix:///s "}})
	want := []runtimeCodexClaim{{Home: "/a"}, {Home: "/b", Endpoint: "unix:///s"}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	var many []runtimeCodexClaim
	for i := range maxCodexClaims + 4 {
		many = append(many, runtimeCodexClaim{Home: fmt.Sprintf("/h%d", i)})
	}
	if got := cleanCodexClaims(many); len(got) != maxCodexClaims || got[0].Home != "/h0" {
		t.Fatalf("got %d claims, first %+v", len(got), got[0])
	}
	if cleanCodexClaims([]runtimeCodexClaim{{}}) != nil {
		t.Fatal("claims that name nothing were kept")
	}
}
