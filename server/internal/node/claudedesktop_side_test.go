package node

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// A side session of the desktop app (spawnedFrom.sessionId names the local_<id> of the session that started it) is
// drawn under that session (#5935): its row names the parent's transcript in parent_agent_id while both are listed, and
// stays a row of its own. A parent that is archived, missing or not listed, an entry without the id or of another
// shape, and a loop link nothing.
func TestSideSessionsNameTheSessionThatStartedThem(t *testing.T) {
	const (
		parent   = "eeeeeeee-0000-4000-8000-000000000001"
		side     = "eeeeeeee-0000-4000-8000-000000000002" // a /btw side chat of parent
		grand    = "eeeeeeee-0000-4000-8000-000000000003" // a side chat of side: names side, the web nests it under parent
		orphan   = "eeeeeeee-0000-4000-8000-000000000004" // its parent is not in the index
		gone     = "eeeeeeee-0000-4000-8000-000000000005" // archived: not listed
		ofGone   = "eeeeeeee-0000-4000-8000-000000000006" // started from gone
		noID     = "eeeeeeee-0000-4000-8000-000000000007" // an older app's spawnedFrom, without sessionId
		odd      = "eeeeeeee-0000-4000-8000-000000000008" // spawnedFrom of another shape: the entry is still read
		loopA    = "eeeeeeee-0000-4000-8000-000000000009"
		loopB    = "eeeeeeee-0000-4000-8000-00000000000a"
		self     = "eeeeeeee-0000-4000-8000-00000000000b" // started from itself
		unlisted = "eeeeeeee-0000-4000-8000-00000000000c" // started from parentOff, whose transcript is not on this machine
	)
	n, home := testNode(t)
	n.metas, n.bgScans = map[string]quickMeta{}, map[string]*bgScan{}
	dir := filepath.Join(home, ".claude", "projects", "-src-app")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{parent, side, grand, orphan, gone, ofGone, noID, odd, loopA, loopB, self, unlisted} {
		writeEntrypointTranscript(t, dir, id, "claude-desktop", time.Duration(i+1)*time.Minute)
	}
	_, third := useDesktopDirs(t)
	from := func(local string) string {
		return `,"spawnedFrom":{"sessionId":"local_` + local + `","taskId":"task_1","sideSession":true,"userFork":true,"btw":true}`
	}
	writeDesktopEntry(t, third, parent, parent, false, nil, "")
	writeDesktopEntry(t, third, side, side, false, nil, from(parent))
	writeDesktopEntry(t, third, grand, grand, false, nil, from(side))
	writeDesktopEntry(t, third, orphan, orphan, false, nil, from("ffffffff-0000-4000-8000-000000000001"))
	writeDesktopEntry(t, third, gone, gone, true, nil, "")
	writeDesktopEntry(t, third, ofGone, ofGone, false, nil, from(gone))
	writeDesktopEntry(t, third, noID, noID, false, nil, `,"spawnedFrom":{"btw":true,"sideSession":true,"userFork":true}`)
	writeDesktopEntry(t, third, odd, odd, false, nil, `,"spawnedFrom":"local_`+parent+`"`)
	writeDesktopEntry(t, third, loopA, loopA, false, nil, from(loopB))
	writeDesktopEntry(t, third, loopB, loopB, false, nil, from(loopA))
	writeDesktopEntry(t, third, self, self, false, nil, from(self))
	const parentOff = "ffffffff-0000-4000-8000-000000000002"
	writeDesktopEntry(t, third, parentOff, parentOff, false, nil, "") // named by the index, no transcript here
	writeDesktopEntry(t, third, unlisted, unlisted, false, nil, from(parentOff))

	want := map[string]string{parent: "", side: parent, grand: side, orphan: "", ofGone: "", noID: "", odd: "", loopA: "",
		loopB: "", self: "", unlisted: ""}
	for round := range 2 { // the second list comes from the cached scan: the link is set again, on the row's own copy
		rows := map[string]proto.Session{}
		for _, s := range n.sessionList(context.Background(), false) {
			rows[s.SessionID] = s
		}
		if _, ok := rows[gone]; ok {
			t.Fatalf("round %d: the archived session is listed", round)
		}
		for id, p := range want {
			s, ok := rows[id]
			if !ok {
				t.Errorf("round %d: %s is not listed", round, id)
				continue
			}
			if got := s.ParentAgentID(); got != p {
				t.Errorf("round %d: %s parent_agent_id = %q, want %q", round, id, got, p)
			}
			if s.IsChild() || s.IsSubtask() {
				t.Errorf("round %d: %s became a child: a side session stays a session of its own", round, id)
			}
		}
	}
	// The cached scan's rows never carry the link: it is set on each list's copies.
	offlineCache.Lock()
	defer offlineCache.Unlock()
	for _, s := range offlineCache.list {
		if s.ParentAgentID() != "" {
			t.Errorf("the cached row %s carries parent_agent_id %q", s.SessionID, s.ParentAgentID())
		}
	}
}

// The link goes on the wire as the row's own key, and a change of it changes the list's key (a new list is sent).
func TestParentAgentIDOnTheWireAndInTheListKey(t *testing.T) {
	s := proto.Session{MachineID: "m", SessionID: "side", Title: "btw: why"}
	before := listKey([]proto.Session{s})
	s.SetParentAgentID("main")
	b, err := s.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var back proto.Session
	if err := back.UnmarshalJSON(b); err != nil {
		t.Fatal(err)
	}
	if back.ParentAgentID() != "main" {
		t.Fatalf("parent_agent_id after a round trip = %q in %s", back.ParentAgentID(), b)
	}
	if listKey([]proto.Session{s}) == before {
		t.Fatal("the list key did not change with parent_agent_id")
	}
	s.SetParentAgentID("")
	if s.Hierarchy != nil {
		t.Fatal("a hierarchy with nothing in it is kept")
	}
}

func TestLoopsBack(t *testing.T) {
	parents := map[string]string{"a": "b", "b": "a", "c": "a", "d": "e"}
	for id, want := range map[string]bool{"a": true, "b": true, "c": false, "d": false, "x": false} {
		if got := loopsBack(id, parents); got != want {
			t.Errorf("loopsBack(%s) = %v, want %v", id, got, want)
		}
	}
}
