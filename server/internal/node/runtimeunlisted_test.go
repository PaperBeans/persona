package node

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// A runtime that runs Claude Code's command line itself (Paseo) writes transcripts that say sdk-cli, like a script's
// one-shot run, which the offline list leaves out. The transcripts it names as sessions of its own that it does not
// list (unlisted_transcripts: archived there) are listed as sessions not running; one it never named, or names no
// more, is left out as before (#4521).
func TestRuntimeNamesItsUnlistedTranscripts(t *testing.T) {
	const (
		archived = "5d510000-0000-4000-8000-000000000001" // an agent archived in the runtime
		helper   = "5d510000-0000-4000-8000-000000000002" // a one-shot run nobody names
		running  = "5d510000-0000-4000-8000-000000000003" // a session the runtime lists
	)
	t.Setenv("PERSONA_HOME", "")
	n, _ := runtimeNode(t)
	t.Cleanup(freshOffline)
	dir := filepath.Join(n.home, ".claude", "projects", "-src-app")
	for i, id := range []string{archived, helper, running} {
		writeClaudeRun(t, dir, id, time.Duration(i+1)*time.Minute, "sdk-cli", "sdk-cli")
	}
	host := dialRuntime(t, n, paseoHello())
	if f, _ := host.helloR["features"].([]any); !slices.Contains(f, any("unlisted_transcripts")) {
		t.Fatalf("features %v", host.helloR["features"])
	}
	row := map[string]any{"session_id": running, "transcript": running, "agent": "claude", "title": "Fix the build", "cwd": "/src/app", "status": "idle"}
	push := func(unlisted ...string) {
		p := map[string]any{"sessions": []map[string]any{row}}
		if unlisted != nil {
			p["unlisted_transcripts"] = unlisted
		}
		_ = host.peer.Notify("runtime.sessions", p)
	}

	// Named by nobody: both are one-shot runs to the node. The session the runtime lists is its row.
	push()
	waitFor(t, "the runtime's session", func() bool { return listed(t, n)[running].SourceKind == proto.SourceRuntime })
	if got := listed(t, n); got[archived].SessionID != "" || got[helper].SessionID != "" {
		t.Fatalf("one-shot runs listed: %q %q", got[archived].Status, got[helper].Status)
	}

	// Named: a session not running, as any transcript of the user's; the list scans again at once, not when its
	// cached scan runs out. A name that is no transcript's id is dropped.
	push(archived, "not an id", running)
	waitFor(t, "the archived session listed", func() bool { return listed(t, n)[archived].Status == proto.StatusOffline })
	got := listed(t, n)
	if s := got[archived]; s.Agent != "claude" || s.SourceKind != "" || s.Cwd != "/src/app" {
		t.Fatalf("listed as %+v", s)
	}
	if _, ok := got[helper]; ok {
		t.Fatal("a run nobody named is listed")
	}
	if s := got[running]; s.SourceKind != proto.SourceRuntime || s.Status != proto.StatusIdle {
		t.Fatalf("a session it lists and names: %+v", s)
	}

	// A list that says nothing about them (the runtime was just started and has not read its archive) leaves them.
	push()
	waitFor(t, "the list taken", func() bool { return listed(t, n)[running].SourceKind == proto.SourceRuntime })
	if listed(t, n)[archived].Status != proto.StatusOffline {
		t.Fatal("a list without the field took the archived session away")
	}

	// Named no more (it is listed again under another row, or was deleted there): left out again.
	push([]string{}...) // an empty list: it names none
	waitFor(t, "left out again", func() bool { _, ok := listed(t, n)[archived]; return !ok })

	// The runtime gone for a moment (its bridge restarting): what it named stands, and it says the same when it is
	// back, so the session does not leave the list and come back.
	push(archived)
	waitFor(t, "listed again", func() bool { return listed(t, n)[archived].Status == proto.StatusOffline })
	host.conn.Close()
	waitFor(t, "the runtime left", func() bool { _, there := listed(t, n)[running]; return !there })
	freshOffline()
	if listed(t, n)[archived].Status != proto.StatusOffline {
		t.Fatal("the archived session went with the runtime at once")
	}
	// Gone for good: its word no longer stands.
	n.runtimes.mu.Lock()
	u := n.runtimes.unlisted["paseo"]
	if u.gone.IsZero() || !u.set[archived] {
		n.runtimes.mu.Unlock()
		t.Fatalf("kept as %+v", u)
	}
	u.gone = time.Now().Add(-unlistedKeep - time.Second)
	n.runtimes.unlisted["paseo"] = u
	n.runtimes.mu.Unlock()
	if got := n.runtimes.unlistedTranscripts(); got != nil {
		t.Fatalf("still named after it stayed away: %v", got)
	}
	freshOffline()
	if _, ok := listed(t, n)[archived]; ok {
		t.Fatal("a session of a runtime that stayed away is still listed")
	}
}

// What a push may name: ids Claude itself uses, a bounded number of them; and a connection says when they changed.
func TestUnlistedSet(t *testing.T) {
	const a, b = "5d510000-0000-4000-8000-00000000000a", "5d510000-0000-4000-8000-00000000000b"
	if got := unlistedSet(nil); got != nil {
		t.Fatalf("none named: %v", got)
	}
	if got := unlistedSet([]string{"", "../x", "has space"}); got != nil {
		t.Fatalf("no id among them: %v", got)
	}
	if got := unlistedSet([]string{a, "../x", a, b}); len(got) != 2 || !got[a] || !got[b] {
		t.Fatalf("named %v", got)
	}
	many := make([]string, 0, unlistedMax+50)
	for i := range unlistedMax + 50 {
		many = append(many, "5d51"+hex8(i)+"-0000-4000-8000-000000000000")
	}
	// Too many: the last ones named are kept (a runtime names its latest last).
	if got := unlistedSet(many); len(got) != unlistedMax || got[many[0]] || !got[many[len(many)-1]] {
		t.Fatalf("kept %d of %d, the first %v, the last %v", len(got), len(many), got[many[0]], got[many[len(many)-1]])
	}
	reg := newRuntimeRegistry()
	h := reg.add(nil)
	if reg.setUnlisted(h, unlistedSet([]string{a})) || reg.unlistedTranscripts() != nil {
		t.Fatal("a connection that has not said hello named transcripts")
	}
	h.hello, h.ready = runtimeHello{Runtime: "paseo"}, true
	for _, c := range []struct {
		set     []string
		changed bool
	}{{nil, false}, {[]string{a}, true}, {[]string{a}, false}, {[]string{b}, true}, {[]string{a, b}, true}, {[]string{b, a}, false}, {nil, true}, {nil, false}} {
		if got := reg.setUnlisted(h, unlistedSet(c.set)); got != c.changed {
			t.Fatalf("set %v: changed %v, want %v", c.set, got, c.changed)
		}
	}
	// Two connections of one runtime (it reconnected before the old one was gone): the one that leaves takes nothing.
	reg.setUnlisted(h, unlistedSet([]string{a}))
	again := reg.add(nil)
	again.hello, again.ready = h.hello, true
	reg.remove(h)
	if u := reg.unlisted["paseo"]; !u.gone.IsZero() || !u.set[a] {
		t.Fatalf("after the old connection left: %+v", u)
	}
	reg.remove(again)
	if u := reg.unlisted["paseo"]; u.gone.IsZero() || !reg.unlistedTranscripts()[a] {
		t.Fatalf("after the runtime left: %+v", u)
	}
}

// hex8 is i as four hex digits (ids for a test that needs many).
func hex8(i int) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[i>>12&15], digits[i>>8&15], digits[i>>4&15], digits[i&15]})
}
