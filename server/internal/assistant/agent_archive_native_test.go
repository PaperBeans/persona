package assistant

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// nativeNode plays a node's session.archive / session.unarchive: what it answers (nil error: done), and what it got.
type nativeNode struct {
	mu     sync.Mutex
	answer func(method string) (any, error)
	got    []string
}

func playNativeNode(ft *fleetTest, machine string) *nativeNode {
	nn := &nativeNode{}
	ft.d.mu.Lock()
	ft.d.machines[machine].call = func(method string, params json.RawMessage) (any, error) {
		if method != deviceSessionArchive && method != deviceSessionUnarchive {
			return map[string]any{}, nil
		}
		var p struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(params, &p)
		nn.mu.Lock()
		nn.got = append(nn.got, method+" "+p.SessionID)
		answer := nn.answer
		nn.mu.Unlock()
		if answer != nil {
			return answer(method)
		}
		return map[string]any{"native": "done", "via": "app_server"}, nil
	}
	ft.d.mu.Unlock()
	return nn
}

func (nn *nativeNode) calls() []string {
	nn.mu.Lock()
	defer nn.mu.Unlock()
	return append([]string(nil), nn.got...)
}

func (nn *nativeNode) set(answer func(method string) (any, error)) {
	nn.mu.Lock()
	nn.answer = answer
	nn.mu.Unlock()
}

func archiveNative(t *testing.T, s *Service, sid string, archived bool) *proto.NativeArchive {
	t.Helper()
	w := archiveRequest(s, "mac", sid, roleFull, archived)
	if w.Code != 200 {
		t.Fatalf("archive %v: %d %s", archived, w.Code, w.Body)
	}
	var body struct {
		OK     bool                 `json:"ok"`
		Native *proto.NativeArchive `json:"native"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || !body.OK {
		t.Fatalf("answer %s: %v", w.Body, err)
	}
	return body.Native
}

func archivedRow(t *testing.T, s *Service, sid string) (proto.Session, bool) {
	t.Helper()
	for _, row := range archiveRows(t, s) {
		if row.SessionID == sid {
			return row, true
		}
	}
	return proto.Session{}, false
}

// Archiving a Codex session archives it in Codex too; once Codex has it archived, the node no longer lists it, and
// Persona keeps showing it as archived and restorable. Restoring it restores it in Codex.
func TestNativeArchiveFollowsPersonaArchive(t *testing.T) {
	ft, rows := archiveTestService(t)
	nn := playNativeNode(ft, "mac")
	if got := archiveNative(t, ft.s, "codex-session", true); got == nil || got.State != nativeArchiveDone || !got.Archived {
		t.Fatalf("native: %+v", got)
	}
	if c := nn.calls(); len(c) != 1 || c[0] != "session.archive codex-session" {
		t.Fatalf("node calls: %v", c)
	}
	ft.sessions("mac", rows[0]) // archived in Codex: gone from the node's list
	row, ok := archivedRow(t, ft.s, "codex-session")
	if !ok || !row.Archived || row.NativeArchive == nil || row.NativeArchive.State != nativeArchiveDone {
		t.Fatalf("archived row: %+v %v", row, ok)
	}
	if got := archiveNative(t, ft.s, "codex-session", false); got == nil || got.State != nativeArchiveDone || got.Archived {
		t.Fatalf("restore: %+v", got)
	}
	if c := nn.calls(); len(c) != 2 || c[1] != "session.unarchive codex-session" {
		t.Fatalf("node calls: %v", c)
	}
	if _, ok := archivedRow(t, ft.s, "codex-session"); ok {
		t.Fatal("restored row still archived")
	}
	// Persona's records survive a restart, the tool's with them.
	restarted, _ := archiveTestService(t)
	restarted.s.setStore(func() Store { return ft.st })
	if err := restarted.s.loadNativeArchives(t.Context()); err != nil {
		t.Fatal(err)
	}
	if v := restarted.s.nativeArchiveView("mac", "codex-session"); v == nil || v.Archived || v.State != nativeArchiveDone {
		t.Fatalf("after a restart: %+v", v)
	}
}

// The machine offline (or Codex refusing for now): Persona's archive succeeds at once and the tool's waits, tried
// again when the machine's list comes in; a busy session stays refused before anything is asked.
func TestNativeArchivePendingIsRetried(t *testing.T) {
	ft, rows := archiveTestService(t)
	defer func(d time.Duration) { nativeArchiveRetry = d }(nativeArchiveRetry)
	nativeArchiveRetry = 0
	nn := playNativeNode(ft, "mac")
	nn.set(func(string) (any, error) { return nil, errors.New("machine offline") })
	got := archiveNative(t, ft.s, "codex-session", true)
	if got == nil || got.State != nativeArchivePending || got.Error == "" {
		t.Fatalf("native: %+v", got)
	}
	if row, ok := archivedRow(t, ft.s, "codex-session"); !ok || row.NativeArchive == nil || row.NativeArchive.State != nativeArchivePending {
		t.Fatalf("pending row: %+v", row)
	}
	nn.set(nil)
	ft.sessions("mac", rows...)
	waitUntil(t, func() bool {
		v := ft.s.nativeArchiveView("mac", "codex-session")
		return v != nil && v.State == nativeArchiveDone && v.Archived
	})

	rows[0].Status = proto.StatusWorking
	ft.d.setSessions("mac", rows)
	before := len(nn.calls())
	if w := archiveRequest(ft.s, "mac", "claude-session", roleFull, true); w.Code != 409 || len(nn.calls()) != before {
		t.Fatalf("busy: %d, calls %v", w.Code, nn.calls())
	}
}

// A tool with no archive of its own (Claude Code), and a node older than native archive: Persona's archive alone.
func TestNativeArchiveUnsupported(t *testing.T) {
	ft, _ := archiveTestService(t)
	nn := playNativeNode(ft, "mac")
	nn.set(func(string) (any, error) {
		return map[string]any{"native": "unsupported", "reason": "claude has no archive of its own"}, nil
	})
	if got := archiveNative(t, ft.s, "claude-session", true); got == nil || got.State != nativeArchiveUnsupported {
		t.Fatalf("claude: %+v", got)
	}
	nn.set(func(method string) (any, error) { return nil, &rpc.Error{Message: "unknown method " + method} })
	if got := archiveNative(t, ft.s, "codex-session", true); got == nil || got.State != nativeArchiveUnsupported {
		t.Fatalf("old node: %+v", got)
	}
	if row, ok := archivedRow(t, ft.s, "codex-session"); !ok || !row.Archived {
		t.Fatalf("Persona's archive: %+v", row)
	}
}

// New activity restores a session in Persona by itself (agent_archives.go); the tool is asked to restore it too.
func TestNativeArchiveFollowsAutomaticRestore(t *testing.T) {
	ft, rows := archiveTestService(t)
	nn := playNativeNode(ft, "mac")
	archiveNative(t, ft.s, "codex-session", true)
	rows[1].UpdatedAt = time.Now()
	ft.sessions("mac", rows...)
	ft.s.decorated("mac") // the list as the apps read it: Persona restores it
	ft.sessions("mac", rows...)
	waitUntil(t, func() bool {
		v := ft.s.nativeArchiveView("mac", "codex-session")
		return v != nil && !v.Archived && v.State == nativeArchiveDone
	})
	if c := nn.calls(); len(c) != 2 || c[1] != "session.unarchive codex-session" {
		t.Fatalf("node calls: %v", c)
	}
}

// Archive and close: Codex refuses while the agent's terminal is open; once the close went through, it is asked again
// at once (not after the retry's wait), and archives it.
func TestNativeArchiveAfterArchiveAndClose(t *testing.T) {
	ft, row := closeTestService(t, func(s *proto.Session) { s.Agent = "codex" })
	nn := playNativeNode(ft, "mac")
	nn.set(func(string) (any, error) {
		if len(retiredOf(ft)) == 0 {
			return nil, &rpc.Error{Message: "Codex still has this session open", Code: "native_archive_live"}
		}
		return map[string]any{"native": "done", "via": "cli"}, nil
	})
	res, err := ft.s.archiveAndCloseAgentSession(t.Context(), "mac", row.SessionID)
	if err != nil || !res.Closed {
		t.Fatalf("archive and close: %+v %v", res, err)
	}
	waitUntil(t, func() bool {
		v := ft.s.nativeArchiveView("mac", row.SessionID)
		return v != nil && v.Archived && v.State == nativeArchiveDone
	})
}
