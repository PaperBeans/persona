package assistant

import (
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestFleetCountsCodexRootsOnly(t *testing.T) {
	for _, state := range []string{proto.StateWorking, proto.StateNeedsYou, proto.StateUnread} {
		t.Run(state, func(t *testing.T) {
			root := proto.Session{MachineID: "box", SessionID: "root", RootSessionID: "root", Agent: "codex", State: state}
			children := []proto.Session{
				{SessionID: "child", ParentSessionID: "root"},
				{SessionID: "grandchild", ParentSessionID: "child", RootSessionID: "root"},
				{SessionID: "orphan", ParentThreadID: "missing"},
				{SessionID: "root-only", RootSessionID: "missing"},
				{SessionID: "flag-only", IsSubagent: true},
			}
			agents := []fleetInput{{Session: &root}}
			var interactions []proto.Interaction
			for _, child := range children {
				child.MachineID, child.Agent, child.State = "box", "codex", state
				agents = append(agents, fleetInput{Session: &child})
				interactions = append(interactions, proto.Interaction{ID: child.SessionID, MachineID: "box", SessionID: child.SessionID, Kind: proto.IxPermission})
			}
			// Non-Codex ancestry keeps its existing independent-task semantics.
			agents = append(agents, fleetInput{Session: &proto.Session{MachineID: "box", SessionID: "ordinary", Agent: "claude", ParentSessionID: "root", IsSubagent: true, State: state}})
			st := buildFleet(agents, interactions)
			if st.Working+st.NeedsYou+st.Unread != 2 || len(st.Items) != 2 {
				t.Fatalf("children changed root counts: %+v", st)
			}
			for _, item := range st.Items {
				if item.ID != "box/root" && item.ID != "box/ordinary" {
					t.Fatalf("child counted as independent task: %+v", item)
				}
			}
			if st := buildFleet(agents[1:len(agents)-1], interactions); st.Working+st.NeedsYou+st.Unread != 0 {
				t.Fatalf("missing parents made children count: %+v", st)
			}
		})
	}
}

// A runtime's session that another of its sessions started (an agent another Paseo agent created, #4494) is its
// parent's subtask whichever agent it runs: the parent's row stands for it. Unlike a Codex subagent it can need the
// user, and then it counts and is listed like any session.
func TestFleetCountsARuntimeSubtaskOnlyWhileItNeedsYou(t *testing.T) {
	lead := proto.Session{MachineID: "box", SessionID: "lead", Agent: "claude", SourceKind: proto.SourceRuntime, State: proto.StateWorking}
	delegated := func(id, agent, state string) fleetInput {
		return fleetInput{Session: &proto.Session{MachineID: "box", SessionID: id, Agent: agent, SourceKind: proto.SourceRuntime,
			IsSubagent: true, ParentSessionID: "lead", RootSessionID: "lead", State: state}}
	}
	for _, state := range []string{proto.StateWorking, proto.StateUnread, proto.StateIdle} {
		st := buildFleet([]fleetInput{{Session: &lead}, delegated("review", "codex", state), delegated("tests", "claude", state)}, nil)
		if st.Working != 1 || st.NeedsYou+st.Unread != 0 || len(st.Items) != 1 || st.Items[0].ID != "box/lead" {
			t.Fatalf("%s: the agents it created counted beside it: %+v", state, st)
		}
	}
	// One waits for the user: by its row's state, and by a card that reached the hub before that row did.
	card := []proto.Interaction{{ID: "ix-1", MachineID: "box", SessionID: "tests", Kind: proto.IxPermission}}
	for name, c := range map[string]struct {
		asking fleetInput
		cards  []proto.Interaction
	}{
		"its row says so": {delegated("tests", "claude", proto.StateNeedsYou), nil},
		"with its card":   {delegated("tests", "claude", proto.StateNeedsYou), card},
	} {
		st := buildFleet([]fleetInput{{Session: &lead}, delegated("review", "codex", proto.StateWorking), c.asking}, c.cards)
		if st.NeedsYou != 1 || st.Working != 1 || len(st.Items) != 2 || st.Items[0].ID != "box/tests" {
			t.Fatalf("%s: %+v", name, st)
		}
	}
	// A session no runtime lists keeps what it had: ancestry on a Claude row is not a subtask, on a Codex row it is.
	if (proto.Session{Agent: "claude", IsSubagent: true, ParentSessionID: "lead"}).IsSubtask() ||
		!(proto.Session{Agent: "codex", ParentSessionID: "lead", State: proto.StateNeedsYou}).IsSubtask() ||
		(proto.Session{SourceKind: proto.SourceRuntime, Agent: "codex"}).IsSubtask() {
		t.Fatal("IsSubtask outside a runtime's own sessions")
	}
}

func TestFleetDecorationPreservesCodexHierarchy(t *testing.T) {
	ft := newFleetTestNoStore(t)
	child := proto.Session{SessionID: "child", Agent: "codex", SourceKind: "codex_app_server", Status: proto.StatusWorking,
		ParentThreadID: "raw-root", ParentSessionID: "root", RootSessionID: "root", IsSubagent: true, RepoRoot: "/code/project"}
	ft.d.setSessions("mac", []proto.Session{
		{SessionID: "root", Agent: "codex", Status: proto.StatusWorking}, child,
	})
	got := listed(t, ft.s, "mac", "child")
	if got.ParentThreadID != child.ParentThreadID || got.ParentSessionID != child.ParentSessionID || got.RootSessionID != child.RootSessionID || !got.IsSubagent || got.RepoRoot != child.RepoRoot {
		t.Fatalf("decoration lost source hierarchy: %+v", got)
	}
	if st := ft.s.fleetSnapshot(); st.Working != 1 || len(st.Items) != 1 || st.Items[0].ID != "mac/root" {
		t.Fatalf("decorated fleet counted a child: %+v", st)
	}
}
