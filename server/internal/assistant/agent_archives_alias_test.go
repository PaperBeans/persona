package assistant

import (
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// An archive kept under an Antigravity CLI pane's id (made while the pane's conversation was not known, or before
// #14520) holds once its conversation is listed under the conversation's own id and that row names the pane's (#14395):
// the row is archived, Archived lists it once and never under the pane's id, and activity after the close lists it
// again, restored under the conversation's id; the pane's record shows again once no row names it.
func TestArchiveUnderAPaneRowHoldsForTheConversationsRow(t *testing.T) {
	const conversation = "agy-11111111-2222-4333-8444-555555555555"
	ft, rows := archiveTestService(t)
	s := ft.s
	closed := time.Now().Add(-time.Hour)
	pane := proto.Session{MachineID: "mac", SessionID: "agy-w1-p1", Agent: "agy", PaneID: "w1:p1", Title: "Release notes",
		Status: proto.StatusIdle, State: proto.StateIdle, UpdatedAt: closed}
	ft.d.setSessions("mac", append(slices.Clone(rows), pane))
	if w := archiveRequest(s, "mac", pane.SessionID, roleFull, true); w.Code != 200 {
		t.Fatal(w.Body)
	}

	// The pane closes: the companion lists the conversation under its own id, naming the pane's row.
	row := proto.Session{MachineID: "mac", SessionID: conversation, Agent: "agy", SourceKind: proto.SourceRuntime, Title: "Release notes",
		Status: proto.StatusDone, State: proto.StateIdle, UpdatedAt: closed}
	row.SetAliases([]string{pane.SessionID})
	ft.d.setSessions("mac", append(slices.Clone(rows), row))
	if got := archivedIDs(t, s); !got[conversation] || got[pane.SessionID] || len(got) != 1 {
		t.Fatalf("Archived after the pane closed: %v", got)
	}
	for _, r := range s.decorated("mac") {
		if r.SessionID == pane.SessionID {
			t.Fatalf("the inventory repeats the conversation: %+v", r)
		}
		if r.SessionID == conversation && !r.Archived {
			t.Fatalf("the conversation came back: %+v", r)
		}
	}

	// Activity after the close restores it, under the conversation's id; the record under the pane's id stays as it was.
	row.UpdatedAt = time.Now()
	ft.d.setSessions("mac", append(slices.Clone(rows), row))
	if got := archivedIDs(t, s); len(got) != 0 {
		t.Fatalf("Archived after new activity: %v", got)
	}
	ft.d.setSessions("mac", rows) // no row names the pane's id any more
	if got := archivedIDs(t, s); !got[pane.SessionID] {
		t.Fatalf("the record went with the row that named it: %v", got)
	}
}
