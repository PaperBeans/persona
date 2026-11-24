package assistant

import (
	"context"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Messages from other conversations wait in the manager's queue while the conversation's row says working, and the
// first list that says it stopped sends the next one. In #14351 that list never came: the node held the row working
// with no turn running, so four messages stayed queued for hours. The node now sends it once Claude Code says the
// turn is over (node/assistant_turn_reconcile.go); this pins what the manager does with it.
func TestRelaysQueuedBehindAWorkingRowGoOnTheListThatSaysItStopped(t *testing.T) {
	s, f := newQuietService(t)
	working := proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusWorking}
	f.setSessions("server", working)
	tries := relayTries(f, "conv1")
	at := time.Now().UTC()
	for i, text := range []string{"first", "second", "third", "fourth"} {
		s.queueRelay("conv1", relayMsg{From: "conv2", FromTitle: "Planning", Tool: "conversation_send", Text: text, At: at.Add(time.Duration(i) * time.Second)})
	}
	s.SessionsChanged(context.Background(), DeviceSessions{MachineID: "server", Sessions: []proto.Session{working}})
	if got, q := triesOf(f, tries), s.queuedFor("conv1"); len(got) != 0 || q != 4 || drainingNow(s, "conv1") {
		t.Fatalf("a working row: %d sent, %d queued, want none sent and 4 queued", len(got), q)
	}
	done := working
	done.Status = proto.StatusDone
	f.setSessions("server", done)
	s.SessionsChanged(context.Background(), DeviceSessions{MachineID: "server", Sessions: []proto.Session{done}})
	waitFor(t, "the first queued message to go", func() bool {
		return len(triesOf(f, tries)) == 1 && s.queuedFor("conv1") == 3 && !drainingNow(s, "conv1")
	})
}
