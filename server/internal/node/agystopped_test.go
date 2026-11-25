package node

import (
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// The user's Close on an agy pane lists its conversation as stopped by them (#14520), the plain close (session.close)
// and archive and close's (session.retire with close_terminal) alike: the companion's row for it, listed once the pane
// is gone, is offline with the user's stop cause and keeps what the companion lets it do, where the companion says
// done. Another conversation's row is left as the companion says it, and the mark goes once a pane shows the
// conversation again.
func TestAgyCloseListsItsConversationStopped(t *testing.T) {
	for _, how := range []struct {
		method string
		params map[string]any
	}{
		{"session.close", map[string]any{"session_id": agyTestRow}},
		{"session.retire", map[string]any{"session_id": agyTestRow, "close_terminal": true}},
	} {
		t.Run(how.method, func(t *testing.T) {
			n, herdr, home := herdrNode(t)
			const (
				convRow    = "agy-" + agyTestID
				otherRow   = "agy-" + agyOtherTestID
				secondPane = "w1:p2"
			)
			n.runtimes = newRuntimeRegistry()
			runtime := n.runtimes.add(nil)
			runtime.ready, runtime.hello.Runtime = true, agyAgent
			caps := &proto.SessionCapabilities{Read: true, Send: true}
			n.runtimes.setSessions(runtime, []proto.Session{
				{SessionID: convRow, Agent: agyAgent, Cwd: home, Status: proto.StatusDone, Title: "Release notes", Capabilities: caps},
				{SessionID: otherRow, Agent: agyAgent, Cwd: home, Status: proto.StatusDone, Title: "Login page", Capabilities: caps},
			})
			rows := func() map[string]proto.Session {
				out := map[string]proto.Session{}
				for _, row := range listSessions(t, n) {
					if row.Agent == agyAgent {
						out[row.SessionID] = row
					}
				}
				return out
			}
			herdr.reply("session.snapshot", snapshot(nil, nil, []any{agyTestAgent(agyTestPane, home, agyTestID)}, nil))
			if got := rows(); len(got) != 2 || got[agyTestRow].SessionID == "" || got[otherRow].SessionID == "" {
				t.Fatalf("before the close: %v", got)
			}
			if _, err := request(t, n, how.method, how.params); err != nil {
				t.Fatal(err)
			}
			if calls := herdr.trace("pane.close", "tab.close"); len(calls) != 1 {
				t.Fatalf("the pane was not closed: %v", calls)
			}

			herdr.reply("session.snapshot", snapshot(nil, nil, nil, nil)) // the pane is gone
			got := rows()
			if c := got[convRow]; c.Status != proto.StatusOffline || c.Capabilities == nil || c.Capabilities.StopCause != proto.StopUser ||
				!c.Capabilities.Send || !c.Capabilities.Read {
				t.Fatalf("the closed conversation: %+v %+v", c, c.Capabilities)
			}
			if c := got[otherRow]; c.Status != proto.StatusDone || c.Capabilities == nil || c.Capabilities.StopCause != "" {
				t.Fatalf("another conversation: %+v %+v", c, c.Capabilities)
			}
			if caps.StopCause != "" {
				t.Fatal("the companion's own capabilities changed")
			}

			// Opened in another pane, it runs again: once that pane is gone as well, its row is as the companion says.
			herdr.reply("session.snapshot", snapshot(nil, nil, []any{agyTestAgent(secondPane, home, agyTestID)}, nil))
			rows()
			herdr.reply("session.snapshot", snapshot(nil, nil, nil, nil))
			if c := rows()[convRow]; c.Status != proto.StatusDone || c.Capabilities == nil || c.Capabilities.StopCause != "" {
				t.Fatalf("after another pane ran it: %+v %+v", c, c.Capabilities)
			}
		})
	}
}

// What the conversation does in the minute after the close is the close's own; after it, work, a change or a live
// pane says it runs again, and its mark goes. A pane the close did not take away is one after that minute.
func TestAgyStopMarkGoesWhenItRunsAgain(t *testing.T) {
	const conv = "agy-" + agyTestID
	for _, c := range []struct {
		name    string
		ago     time.Duration
		status  string
		changed bool
		live    map[string]string
		stopped bool
	}{
		{"done after the close", 0, proto.StatusDone, false, nil, true},
		{"working in the close's minute", 0, proto.StatusWorking, false, nil, true},
		{"changed in the close's minute", 0, proto.StatusDone, true, nil, true},
		{"its pane still there in that minute", 0, proto.StatusDone, false, map[string]string{agyTestRow: conv}, true},
		{"working after it", 2 * time.Minute, proto.StatusWorking, false, nil, false},
		{"waiting on the user after it", 2 * time.Minute, proto.StatusBlocked, false, nil, false},
		{"changed after it", 2 * time.Minute, proto.StatusDone, true, nil, false},
		{"its pane still there after it", 2 * time.Minute, proto.StatusDone, false, map[string]string{agyTestRow: conv}, false},
		{"another pane shows it", 0, proto.StatusDone, false, map[string]string{"agy-w1-p2": conv}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			closed := time.Now().Add(-c.ago)
			n := &Node{agyStopped: map[string]agyStop{agyTestID: {row: agyTestRow, at: closed}}}
			row := proto.Session{SessionID: conv, Agent: agyAgent, Status: c.status, UpdatedAt: closed.Add(-time.Hour)}
			if c.changed {
				row.UpdatedAt = time.Now()
			}
			list := []proto.Session{row}
			n.markAgyStopped(list, c.live)
			stopped := list[0].Status == proto.StatusOffline && list[0].Capabilities != nil && list[0].Capabilities.StopCause == proto.StopUser
			if _, kept := n.agyStopped[agyTestID]; stopped != c.stopped || kept != c.stopped {
				t.Fatalf("stopped %v (row %+v), mark kept %v; want %v", stopped, list[0], kept, c.stopped)
			}
		})
	}
}

// Antigravity keeps no archive of its own: the node says so for an agy row no runtime takes, a pane's or a
// conversation's, instead of refusing its id (#14520).
func TestNativeArchiveOfAnAgyRow(t *testing.T) {
	n := &Node{}
	for _, id := range []string{agyTestRow, "agy-" + agyTestID} {
		got, err := n.nativeArchive(t.Context(), id, true)
		if err != nil || got.Native != nativeArchiveUnsupported {
			t.Fatalf("%s: %+v %v", id, got, err)
		}
	}
	if _, err := n.nativeArchive(t.Context(), "not a session", true); err == nil {
		t.Fatal("an invalid id was taken")
	}
}
