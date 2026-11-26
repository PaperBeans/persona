package assistant

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// An Antigravity CLI conversation's archive is kept under its own id, agy-<conversation id>, also when it is made on a
// pane's row, which names that id first among its aliases (#14520): it holds once the pane is gone and the companion
// lists the conversation under that id, the tool's archive is asked for that id, and a restore or a later pane finds
// the same record.

const agyTestConversation = "agy-11111111-2222-4333-8444-555555555555"

// agyTestPaneRow is a live Antigravity CLI pane's row showing agyTestConversation, with no time of its own (#13710).
func agyTestPaneRow(id, pane string, earlier ...string) proto.Session {
	s := proto.Session{MachineID: "mac", SessionID: id, Agent: "agy", PaneID: pane, Title: "Release notes",
		Status: proto.StatusIdle, State: proto.StateIdle}
	s.SetAliases(append([]string{agyTestConversation}, earlier...))
	return s
}

// agyTestConversationRow is the companion's row for agyTestConversation once no pane shows it, last changed at, naming
// no pane (as after the node restarted).
func agyTestConversationRow(at time.Time) proto.Session {
	return proto.Session{MachineID: "mac", SessionID: agyTestConversation, Agent: "agy", SourceKind: proto.SourceRuntime,
		Title: "Release notes", Status: proto.StatusDone, State: proto.StateIdle, UpdatedAt: at}
}

// Close… on a pane's row (#14520): archive?close=1 with the pane's id closes the pane and archives its conversation,
// and the tool's archive is asked for the conversation's id, which agy answers. The conversation listed again under
// its own id stays archived, once in Archived; Restore there brings it back and asks the tool the same.
func TestAgyCloseArchivesItsConversation(t *testing.T) {
	ft, pane := closeTestService(t, func(s *proto.Session) { *s = agyTestPaneRow("agy-w1-p1", "w1:p1") })
	nn := playNativeNode(ft, "mac")
	nn.set(func(string) (any, error) {
		return map[string]any{"native": "unsupported", "reason": "Antigravity has no archive of its own"}, nil
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/?close=1", nil)
	r.SetPathValue("sid", pane.SessionID)
	r.Header.Set(RoleHeader, roleFull)
	ft.s.setAgentSessionArchive(w, r, "mac", true)
	var body struct {
		Archived bool                 `json:"archived"`
		Native   *proto.NativeArchive `json:"native"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); w.Code != http.StatusOK || err != nil || !body.Archived ||
		body.Native == nil || body.Native.State != nativeArchiveUnsupported {
		t.Fatalf("close: %d %s", w.Code, w.Body)
	}
	if got := retiredOf(ft); len(got) != 1 || got[0] != pane.SessionID {
		t.Fatalf("closed: %v", got)
	}
	for _, c := range nn.calls() {
		if c != "session.archive "+agyTestConversation {
			t.Fatalf("the tool's archive was asked: %v", nn.calls())
		}
	}

	// The pane is gone: the companion lists the conversation under its own id, as last changed before the close, and a
	// node with no memory of the close (restarted since) lists it done, naming nothing.
	ft.d.setSessions("mac", []proto.Session{agyTestConversationRow(time.Now().Add(-time.Minute))})
	rows := archiveRows(t, ft.s)
	if len(rows) != 1 || rows[0].SessionID != agyTestConversation || rows[0].NativeArchive == nil ||
		rows[0].NativeArchive.State != nativeArchiveUnsupported {
		t.Fatalf("Archived after the close: %+v", rows)
	}
	for _, row := range ft.s.decorated("mac") {
		if row.SessionID == agyTestConversation && !row.Archived {
			t.Fatalf("the closed conversation came back: %+v", row)
		}
	}

	if w := archiveRequest(ft.s, "mac", agyTestConversation, roleFull, false); w.Code != http.StatusOK {
		t.Fatal(w.Body)
	}
	if got := archivedIDs(t, ft.s); len(got) != 0 {
		t.Fatalf("Archived after Restore: %v", got)
	}
	waitUntil(t, func() bool {
		c := nn.calls()
		return len(c) > 0 && c[len(c)-1] == "session.unarchive "+agyTestConversation
	})
}

// Close… on the conversation's own row, the companion's (#14520): archive?close=1 closes it through the companion and
// archives it under its id. It stays out of the list when a restarted node lists it again as the companion says (done,
// with no stop mark), and after the manager restarts, from the record it saved.
func TestAgyCloseOnTheConversationsRowSticks(t *testing.T) {
	closeAllowed := true
	ft, row := closeTestService(t, func(s *proto.Session) {
		*s = agyTestConversationRow(time.Now().Add(-time.Hour))
		s.Capabilities = &proto.SessionCapabilities{Read: true, Send: true, Close: &closeAllowed}
	})
	var closed []string
	ft.f.answerNode("session.close", func(_ string, raw json.RawMessage) (any, error) {
		var params map[string]string
		_ = json.Unmarshal(raw, &params)
		closed = append(closed, params["session_id"])
		return map[string]any{"ok": true}, nil
	})
	res, err := ft.s.archiveAndCloseAgentSession(t.Context(), "mac", row.SessionID)
	if err != nil || !res.Closed || len(closed) != 1 || closed[0] != agyTestConversation || len(retiredOf(ft)) != 0 {
		t.Fatalf("close: %+v %v, closed %v, retired %v", res, err, closed, retiredOf(ft))
	}

	again := agyTestConversationRow(time.Now().Add(-time.Hour)) // as a node with no memory of the close lists it
	again.Capabilities = row.Capabilities
	ft.d.setSessions("mac", []proto.Session{again})
	for _, r := range ft.s.decorated("mac") {
		if r.SessionID == agyTestConversation && !r.Archived {
			t.Fatalf("the closed conversation came back: %+v", r)
		}
	}
	if got := archivedIDs(t, ft.s); !got[agyTestConversation] || len(got) != 1 {
		t.Fatalf("Archived after the close: %v", got)
	}

	restarted, _ := archiveTestService(t)
	restarted.s.setStore(func() Store { return ft.st })
	restarted.d.setSessions("mac", []proto.Session{again})
	if got := archivedIDs(t, restarted.s); !got[agyTestConversation] || len(got) != 1 {
		t.Fatalf("Archived after the manager restarted: %v", got)
	}
}

// An archive made on a pane's row with no close follows the conversation: listed under its own id once the pane is
// gone, and in another pane later, it stays archived until work in it, which restores it for good, under that id.
func TestAgyPaneArchiveFollowsItsConversation(t *testing.T) {
	ft, rows := archiveTestService(t)
	s := ft.s
	pane := agyTestPaneRow("agy-w1-p1", "w1:p1")
	ft.d.setSessions("mac", append(slices.Clone(rows), pane))
	if w := archiveRequest(s, "mac", pane.SessionID, roleFull, true); w.Code != http.StatusOK {
		t.Fatal(w.Body)
	}
	if got := archivedIDs(t, s); !got[pane.SessionID] || len(got) != 1 {
		t.Fatalf("Archived while the pane shows it: %v", got)
	}

	// The pane closes: the conversation's own row, with the time of its last change, is no new activity.
	ft.d.setSessions("mac", append(slices.Clone(rows), agyTestConversationRow(time.Now().Add(-time.Hour))))
	if got := archivedIDs(t, s); !got[agyTestConversation] || len(got) != 1 {
		t.Fatalf("Archived once the pane is gone: %v", got)
	}

	// Opened in another pane, which names the first: still archived, as that pane's row.
	again := agyTestPaneRow("agy-w2-p1", "w2:p1", pane.SessionID)
	ft.d.setSessions("mac", append(slices.Clone(rows), again))
	if got := archivedIDs(t, s); !got[again.SessionID] || len(got) != 1 {
		t.Fatalf("Archived with the conversation in another pane: %v", got)
	}

	// Work in it restores it under the conversation's id: its own row is listed again once that pane goes too.
	again.Status, again.State = proto.StatusWorking, proto.StateWorking
	ft.d.setSessions("mac", append(slices.Clone(rows), again))
	s.decorated("mac") // the list as the apps read it: Persona restores it
	ft.d.setSessions("mac", append(slices.Clone(rows), agyTestConversationRow(time.Now().Add(-time.Hour))))
	if got := archivedIDs(t, s); len(got) != 0 {
		t.Fatalf("Archived after work in it: %v", got)
	}
	for _, row := range s.decorated("mac") {
		if row.SessionID == agyTestConversation && row.Archived {
			t.Fatalf("restored conversation archived again: %+v", row)
		}
	}
}

// An archive kept under a pane's own id (made while its conversation was not known, or before #14520) holds for that
// pane's row. Work in the row restores it under the row's archive key, newer than that record, which stays as it was
// and is listed again once no row names the pane's id.
func TestAgyWorkRestoresARowArchivedUnderItsPanesID(t *testing.T) {
	ft, rows := archiveTestService(t)
	s := ft.s
	unknown := proto.Session{MachineID: "mac", SessionID: "agy-w1-p1", Agent: "agy", PaneID: "w1:p1", Title: "Login page",
		Status: proto.StatusIdle, State: proto.StateIdle}
	ft.d.setSessions("mac", append(slices.Clone(rows), unknown))
	if w := archiveRequest(s, "mac", unknown.SessionID, roleFull, true); w.Code != http.StatusOK {
		t.Fatal(w.Body)
	}

	pane := agyTestPaneRow(unknown.SessionID, unknown.PaneID) // the pane's conversation is known now
	ft.d.setSessions("mac", append(slices.Clone(rows), pane))
	if got := archivedIDs(t, s); !got[pane.SessionID] || len(got) != 1 {
		t.Fatalf("Archived before work in it: %v", got)
	}
	pane.Status, pane.State = proto.StatusWorking, proto.StateWorking
	ft.d.setSessions("mac", append(slices.Clone(rows), pane))
	s.decorated("mac")
	pane.Status, pane.State = proto.StatusIdle, proto.StateIdle // done, with still no time of its own
	ft.d.setSessions("mac", append(slices.Clone(rows), pane))
	if got := archivedIDs(t, s); len(got) != 0 {
		t.Fatalf("Archived after work in it: %v", got)
	}

	ft.d.setSessions("mac", rows)
	if got := archivedIDs(t, s); !got[unknown.SessionID] || len(got) != 1 {
		t.Fatalf("the record under the pane's id once no row names it: %v", got)
	}
}

// Force archive's fence for a confirmation-blocked pane's row is kept under the conversation's id as well.
func TestAgyForceArchiveFenceIsTheConversations(t *testing.T) {
	ft, pane := closeTestService(t, func(s *proto.Session) {
		*s = agyTestPaneRow("agy-w1-p1", "w1:p1")
		s.Status, s.State = proto.StatusBlocked, proto.StateNeedsYou
	})
	if _, err := ft.s.confirmationArchiveFenceAt(t.Context(), pane, true); err != nil {
		t.Fatal(err)
	}
	records := *ft.s.agentArchives.snapshot.Load()
	_, conversation := records[runtimeListID("mac", agyTestConversation)]
	_, paneID := records[runtimeListID("mac", pane.SessionID)]
	if !conversation || paneID {
		t.Fatalf("fence under the conversation %v, under the pane %v", conversation, paneID)
	}
}
