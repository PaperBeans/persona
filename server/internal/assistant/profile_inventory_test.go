package assistant

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestAssistantViewSelectsBeforeDecorating(t *testing.T) {
	f := newFakeHub()
	f.hello.Assistant.MachineID = "box"
	f.hello.Assistant.Machines = []string{"box"}
	f.devices = []Device{{ID: "box", Home: "/home/u"}}
	d := newFleetDevs()
	d.add("box", "Computer")
	st := newFleetMem()
	s := New(Options{Hub: f, Devices: d, Store: func() Store { return st }})
	list := []proto.Session{
		{SessionID: "main", Cwd: "/home/u/.persona/assistant", Status: proto.StatusWorking},
		{SessionID: "archived", Cwd: "/home/u/.persona/assistant", Status: proto.StatusDone},
		{SessionID: "health", Cwd: "/home/u/.persona/health", Status: proto.StatusDone},
		{SessionID: "coding", Cwd: "/code/example", Status: proto.StatusWorking},
		{SessionID: "pane", Cwd: "/home/u/.persona/assistant", PaneID: "pane-1", Status: proto.StatusDone},
	}
	d.setSessions("box", list)
	s.fleet.rep.sessions = map[string]SessionView{
		"main":     {Title: "Persona", Origin: "app"},
		"archived": {Title: "Archived conversation", Archived: true},
	}
	key := [2]string{"box", "coding"}
	s.fleet.hooked = map[[2]string]bool{key: true}
	// Populate all marks first, as a regular inventory publication would.
	full, err := s.sessions(context.Background(), "box")
	if err != nil {
		t.Fatal(err)
	}
	mark := s.fleet.stateSince[key]
	for _, tc := range []struct {
		space    string
		archived bool
		count    int
	}{{"", false, 1}, {"", true, 2}, {SpaceHealth, false, 1}} {
		want := []proto.Session{}
		for _, row := range full {
			if row.Assistant && spaceOf(row) == tc.space && (tc.archived || !row.Archived) {
				want = append(want, row)
			}
		}
		got := s.view(context.Background(), tc.archived, tc.space).Conversations
		// Compare wire values: legacy JSON normalizes nil/empty and UTC timestamps.
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		if string(gotJSON) != string(wantJSON) || len(got) != tc.count {
			t.Fatalf("space=%q archived=%v: got %s, want %s", tc.space, tc.archived, gotJSON, wantJSON)
		}
		if !reflect.DeepEqual(s.fleet.stateSince[key], mark) || !s.fleet.hooked[key] {
			t.Fatal("a Persona-only read removed a coding session's state or hook flag")
		}
	}
	// Requests must not mutate the device layer's shared session values.
	if list[0].Title != "" || list[0].Assistant {
		t.Fatal("view decorated the source inventory in place")
	}
}

func TestAssistantViewIncludesLogicalOfflinePinnedLeaders(t *testing.T) {
	ctx := context.Background()
	f := newFakeHub()
	f.hello.Assistant.MachineID = "box"
	f.hello.Assistant.Machines = []string{"box"}
	f.devices = []Device{{ID: "box", Home: "/home/u"}}
	d := newFleetDevs()
	d.add("box", "Computer")
	st := newFleetMem()
	leaders := []string{
		"dir-1", "dir-2", "dir-3", "dir-4", "dir-5", "dir-6", "dir-7", "dir-8", "dir-9", "dir-archived",
	}
	if err := st.PutSettings(ctx, map[string]any{
		"assistant.main":    "main",
		"assistant.leaders": leaders,
	}); err != nil {
		t.Fatal(err)
	}
	s := New(Options{Hub: f, Devices: d, Store: func() Store { return st }})

	list := []proto.Session{
		{SessionID: "main", Cwd: "/home/u/.persona/assistant", Status: proto.StatusIdle},
		{SessionID: "dir-1", Cwd: "/home/u/.persona/assistant", Status: proto.StatusIdle},
		{SessionID: "dir-2", Cwd: "/home/u/.persona/assistant", Status: proto.StatusIdle},
		{SessionID: "dir-3", Cwd: "/home/u/.persona/assistant", Status: proto.StatusIdle},
		{SessionID: "dir-4", Cwd: "/home/u/.persona/assistant", Status: proto.StatusIdle},
		{SessionID: "dir-5", Cwd: "/home/u/.persona/assistant", Status: proto.StatusIdle},
		{SessionID: "dir-6", Cwd: "/home/u/.persona/assistant", Status: proto.StatusIdle},
		// Logical conversations whose active physical segment is offline have Cwd=="" and LogicalPlaceholder==true.
		{SessionID: "dir-7", Status: proto.StatusOffline, Assistant: true, LogicalPlaceholder: true},
		{SessionID: "dir-8", Status: proto.StatusOffline, Assistant: true, LogicalPlaceholder: true},
		{SessionID: "dir-9", Status: proto.StatusOffline, Assistant: true, LogicalPlaceholder: true},
		{SessionID: "dir-archived", Status: proto.StatusOffline, Assistant: true, LogicalPlaceholder: true},
		{SessionID: "dir-unknown-space", Status: proto.StatusOffline, Assistant: true, LogicalPlaceholder: true},
		{SessionID: "coding", Cwd: "/code/example", Status: proto.StatusWorking},
		{SessionID: "non-assistant-placeholder", Status: proto.StatusOffline, Assistant: false, LogicalPlaceholder: true},
	}
	d.setSessions("box", list)
	// Record authoritative default space ("") for known default-space logical placeholders; leave dir-unknown-space unnoted.
	s.noteConversationSpaces(ctx, []db.ConversationSpace{
		{MachineID: "box", SessionID: "dir-7", Space: ""},
		{MachineID: "box", SessionID: "dir-8", Space: ""},
		{MachineID: "box", SessionID: "dir-9", Space: ""},
		{MachineID: "box", SessionID: "dir-archived", Space: ""},
	})
	s.fleet.rep.sessions = map[string]SessionView{
		"main":              {Title: "Persona"},
		"dir-7":             {Title: "Direction 7"},
		"dir-8":             {Title: "Direction 8"},
		"dir-9":             {Title: "Direction 9"},
		"dir-archived":      {Title: "Archived direction", Archived: true},
		"dir-unknown-space": {Title: "Unknown space direction"},
	}

	wantIDs := []string{"main", "dir-1", "dir-2", "dir-3", "dir-4", "dir-5", "dir-6", "dir-7", "dir-8", "dir-9"}
	idsOf := func(rows []proto.Session) []string {
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.SessionID)
		}
		return out
	}

	view := s.view(ctx, false)
	if got := idsOf(view.Conversations); !reflect.DeepEqual(got, wantIDs) {
		t.Fatalf("view(withArchived=false).Conversations = %v, want %v", got, wantIDs)
	}
	withArchived := s.view(ctx, true)
	wantWithArchived := append(append([]string{}, wantIDs...), "dir-archived")
	if got := idsOf(withArchived.Conversations); !reflect.DeepEqual(got, wantWithArchived) {
		t.Fatalf("view(withArchived=true).Conversations = %v, want %v", got, wantWithArchived)
	}
}

func TestPartialStateMarksKeepOtherSessions(t *testing.T) {
	s := &Service{}
	start := time.Now()
	s.markStates("box", []proto.Session{{SessionID: "coding", State: proto.StateWorking}}, start)
	key := [2]string{"box", "coding"}
	s.fleet.hooked = map[[2]string]bool{key: true}
	rows := []proto.Session{{SessionID: "main", State: proto.StateWorking}}
	s.fillStateMarks("box", rows, start.Add(time.Minute), false)
	if !s.fleet.stateSince[key].at.Equal(start) || !s.fleet.hooked[key] || !rows[0].StateSince.Equal(start.Add(time.Minute)) {
		t.Fatal("partial decoration lost state continuity")
	}
	s.markStates("box", rows, start.Add(2*time.Minute))
	if _, ok := s.fleet.stateSince[key]; ok || s.fleet.hooked[key] {
		t.Fatal("complete inventories must still forget removed sessions")
	}
}
