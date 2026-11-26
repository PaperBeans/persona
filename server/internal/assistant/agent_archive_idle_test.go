package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/transcript"
)

type recordingIdleAuditStore struct {
	Store
	mu                   sync.Mutex
	failAuditLoad        bool
	failAuditWrite       bool
	failAllWrites        bool
	failAuditWriteOnCall int
	auditWriteCalls      int
	onPutSetting         func(key string)
	onAuditPersisted     func()
	onPluginAudit        func()
	rows                 []struct {
		plugin, action, actor string
		detail                json.RawMessage
	}
}

func (r *recordingIdleAuditStore) Setting(ctx context.Context, key string, dst any) (bool, error) {
	r.mu.Lock()
	fail := r.failAuditLoad
	r.mu.Unlock()
	if key == idleArchiveAuditKey && fail {
		return false, errors.New("audit store read failed")
	}
	return r.Store.Setting(ctx, key, dst)
}

func (r *recordingIdleAuditStore) PutSetting(ctx context.Context, key string, value any) error {
	r.mu.Lock()
	hook := r.onPutSetting
	r.mu.Unlock()
	if hook != nil {
		hook(key)
	}
	r.mu.Lock()
	failAll := r.failAllWrites
	r.mu.Unlock()
	if failAll {
		return errors.New("store write failed")
	}
	if key == idleArchiveAuditKey {
		r.mu.Lock()
		r.auditWriteCalls++
		callNum := r.auditWriteCalls
		fail := r.failAuditWrite || (r.failAuditWriteOnCall > 0 && callNum == r.failAuditWriteOnCall)
		onPersisted := r.onAuditPersisted
		r.mu.Unlock()
		if fail {
			return errors.New("audit store write failed")
		}
		err := r.Store.PutSetting(ctx, key, value)
		if err == nil && onPersisted != nil {
			onPersisted()
		}
		return err
	}
	return r.Store.PutSetting(ctx, key, value)
}

func (r *recordingIdleAuditStore) AddExtensionAudit(_ context.Context, id, action, actor string, detail json.RawMessage) error {
	r.mu.Lock()
	if action == idleArchiveAuditActionSuccess || action == idleArchiveAuditActionRefused {
		var next idleArchivePluginAuditDetail
		if err := json.Unmarshal(detail, &next); err != nil {
			r.mu.Unlock()
			return err
		}
		for _, row := range r.rows {
			var prior idleArchivePluginAuditDetail
			if json.Unmarshal(row.detail, &prior) == nil && prior.AuditID == next.AuditID {
				r.mu.Unlock()
				return nil
			}
		}
	}
	r.rows = append(r.rows, struct {
		plugin, action, actor string
		detail                json.RawMessage
	}{plugin: id, action: action, actor: actor, detail: append(json.RawMessage(nil), detail...)})
	onAudit := r.onPluginAudit
	r.mu.Unlock()
	if onAudit != nil {
		onAudit()
	}
	return nil
}

func (r *recordingIdleAuditStore) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.rows)
}

func idleArchiveTestSetup(t *testing.T, now time.Time, edit func(*proto.Session)) (*fleetTest, *recordingIdleAuditStore, proto.Session) {
	t.Helper()
	ft, row := closeTestService(t, func(s *proto.Session) {
		s.UpdatedAt = now.Add(-45 * time.Minute)
		s.Rev = 1024
		if edit != nil {
			edit(s)
		}
	})
	rec := &recordingIdleAuditStore{Store: ft.s.store()}
	ft.s.setStore(func() Store { return rec })
	connAt := now.Add(-2 * time.Hour)
	ft.d.mu.Lock()
	ft.d.machines["mac"].connected = connAt
	ft.d.mu.Unlock()
	obsAt := now
	if !row.UpdatedAt.IsZero() && row.UpdatedAt.Before(now) {
		obsAt = row.UpdatedAt
	}
	ft.s.idleArchiveAudit.now = func() time.Time { return obsAt }
	ft.s.observeIdleSessions("mac", []proto.Session{row}, connAt)
	ft.s.idleArchiveAudit.now = func() time.Time { return now }
	t.Cleanup(ft.s.stopIdleArchiveAuditRetry)
	return ft, rec, row
}

func postIdleArchive(s *Service, mid, sid, role, ext, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+mid+"/"+sid+"/archive-idle", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if role != "" {
		r.Header.Set(RoleHeader, role)
	}
	if ext != "" {
		r.Header.Set(ExtensionHeader, ext)
	}
	s.ServeHTTP(w, r)
	return w
}

func getIdleArchiveAudit(s *Service, query, role, ext string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/archive-idle/audit"+query, nil)
	if role != "" {
		r.Header.Set(RoleHeader, role)
	}
	if ext != "" {
		r.Header.Set(ExtensionHeader, ext)
	}
	s.ServeHTTP(w, r)
	return w
}

func TestIdleArchiveClosesEligibleIdleSessionAndWritesAudit(t *testing.T) {
	now := time.Date(2026, 11, 26, 0, 30, 0, 0, time.UTC)
	ft, rec, row := idleArchiveTestSetup(t, now, nil)

	w := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext",
		`{"idle_minutes":30,"reason":"reclaim idle slot"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp assistantwire.IdleArchiveResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.OK || !resp.Archived || resp.AlreadyArchived || !resp.AuditPersisted || resp.AuditID == "" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.ObservedIdleSeconds != 45*60 {
		t.Fatalf("observed_idle_seconds = %d, want %d", resp.ObservedIdleSeconds, 45*60)
	}
	if resp.Close == nil || !resp.Close.Closed || resp.Close.Outcome != archiveCloseClosed {
		t.Fatalf("expected closed terminal, got %+v", resp.Close)
	}
	if retired := retiredOf(ft); len(retired) != 1 || retired[0] != row.SessionID {
		t.Fatalf("retired = %v, want [%s]", retired, row.SessionID)
	}
	if !archivedIDs(t, ft.s)[row.SessionID] {
		t.Fatal("session was not archived")
	}
	if rec.count() != 1 {
		t.Fatalf("plugin_audit rows = %d, want 1", rec.count())
	}

	// Extension reads its own audit entry; another extension cannot see it; full role sees all.
	ownAudit := getIdleArchiveAudit(ft.s, "?machine_id=mac&session_id="+row.SessionID, roleExtension, "guardrail-ext")
	if ownAudit.Code != http.StatusOK {
		t.Fatalf("own audit status = %d: %s", ownAudit.Code, ownAudit.Body.String())
	}
	var ownBody struct {
		Entries []assistantwire.IdleArchiveAuditEntry `json:"entries"`
	}
	if err := json.Unmarshal(ownAudit.Body.Bytes(), &ownBody); err != nil {
		t.Fatal(err)
	}
	if len(ownBody.Entries) != 1 || ownBody.Entries[0].ID != resp.AuditID ||
		ownBody.Entries[0].Outcome != idleArchiveOutcomeArchivedAndClosed ||
		ownBody.Entries[0].Extension != "guardrail-ext" || ownBody.Entries[0].Reason != "reclaim idle slot" {
		t.Fatalf("unexpected own audit entries: %+v", ownBody.Entries)
	}

	otherAudit := getIdleArchiveAudit(ft.s, "", roleExtension, "other-ext")
	if otherAudit.Code != http.StatusOK {
		t.Fatalf("other audit status = %d", otherAudit.Code)
	}
	var otherBody struct {
		Entries []assistantwire.IdleArchiveAuditEntry `json:"entries"`
	}
	if err := json.Unmarshal(otherAudit.Body.Bytes(), &otherBody); err != nil {
		t.Fatal(err)
	}
	if len(otherBody.Entries) != 0 {
		t.Fatalf("other extension leaked entries: %+v", otherBody.Entries)
	}

	fullAudit := getIdleArchiveAudit(ft.s, "?outcome="+idleArchiveOutcomeArchivedAndClosed, roleFull, "")
	if fullAudit.Code != http.StatusOK {
		t.Fatalf("full audit status = %d", fullAudit.Code)
	}
	var fullBody struct {
		Entries []assistantwire.IdleArchiveAuditEntry `json:"entries"`
	}
	if err := json.Unmarshal(fullAudit.Body.Bytes(), &fullBody); err != nil {
		t.Fatal(err)
	}
	if len(fullBody.Entries) != 1 || fullBody.Entries[0].ID != resp.AuditID {
		t.Fatalf("unexpected full audit entries: %+v", fullBody.Entries)
	}
}

func TestIdleArchiveEnforcesStrictIdleDurationBoundary(t *testing.T) {
	now := time.Date(2026, 11, 26, 0, 30, 0, 0, time.UTC)
	cases := []struct {
		name      string
		updatedAt time.Time
		wantCode  string
	}{
		{"exact N minutes rejected", now.Add(-30 * time.Minute), idleArchiveCodeNotIdleLongEnough},
		{"less than N minutes rejected", now.Add(-29*time.Minute - 59*time.Second), idleArchiveCodeNotIdleLongEnough},
		{"future timestamp rejected", now.Add(time.Minute), idleArchiveCodeNotIdleLongEnough},
		{"zero timestamp rejected", time.Time{}, idleArchiveCodeIdleUnverified},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ft, rec, row := idleArchiveTestSetup(t, now, func(s *proto.Session) {
				s.UpdatedAt = tc.updatedAt
			})
			w := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":30}`)
			if w.Code != http.StatusConflict {
				t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body["code"] != tc.wantCode || body["audit_id"] == "" {
				t.Fatalf("got body %+v, want code %s with audit_id", body, tc.wantCode)
			}
			if len(retiredOf(ft)) != 0 || archivedIDs(t, ft.s)[row.SessionID] {
				t.Fatal("refused session must not be archived or retired")
			}
			if rec.count() != 1 {
				t.Fatalf("refusal must write plugin_audit row, got %d", rec.count())
			}
		})
	}
}

func TestIdleArchiveHardRefusesProtectedAndBusySessions(t *testing.T) {
	now := time.Date(2026, 11, 26, 0, 30, 0, 0, time.UTC)

	t.Run("main conversation", func(t *testing.T) {
		ft, rec, _ := idleArchiveTestSetup(t, now, nil)
		setSettings(t, ft.s, func(v *Settings) { v.Main = "main-conv" })
		ft.d.setSessions("mac", []proto.Session{{
			MachineID: "mac", SessionID: "main-conv", Assistant: true, Status: proto.StatusIdle,
			UpdatedAt: now.Add(-2 * time.Hour),
		}})
		w := postIdleArchive(ft.s, "mac", "main-conv", roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
		if w.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
		}
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body["code"] != idleArchiveCodeMainConversation {
			t.Fatalf("code = %v, want %s", body["code"], idleArchiveCodeMainConversation)
		}
		if len(retiredOf(ft)) != 0 || rec.count() != 1 {
			t.Fatalf("retired=%v auditCount=%d", retiredOf(ft), rec.count())
		}
	})

	t.Run("direction lead conversation", func(t *testing.T) {
		ft, rec, _ := idleArchiveTestSetup(t, now, nil)
		setSettings(t, ft.s, func(v *Settings) { v.Leaders = []string{"lead-conv"} })
		ft.d.setSessions("mac", []proto.Session{{
			MachineID: "mac", SessionID: "lead-conv", Assistant: true, Status: proto.StatusIdle,
			UpdatedAt: now.Add(-2 * time.Hour),
		}})
		w := postIdleArchive(ft.s, "mac", "lead-conv", roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
		if w.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
		}
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body["code"] != idleArchiveCodeDirectionLead {
			t.Fatalf("code = %v, want %s", body["code"], idleArchiveCodeDirectionLead)
		}
		if len(retiredOf(ft)) != 0 || rec.count() != 1 {
			t.Fatalf("retired=%v auditCount=%d", retiredOf(ft), rec.count())
		}
	})

	t.Run("other assistant conversation", func(t *testing.T) {
		ft, _, _ := idleArchiveTestSetup(t, now, nil)
		ft.d.setSessions("mac", []proto.Session{{
			MachineID: "mac", SessionID: "side-assistant", Assistant: true, Status: proto.StatusIdle,
			UpdatedAt: now.Add(-2 * time.Hour),
		}})
		w := postIdleArchive(ft.s, "mac", "side-assistant", roleFull, "", `{"idle_minutes":10}`)
		if w.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
		}
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body["code"] != idleArchiveCodeAssistantConversation {
			t.Fatalf("code = %v, want %s", body["code"], idleArchiveCodeAssistantConversation)
		}
	})

	t.Run("live pending interaction even when row count stale", func(t *testing.T) {
		ft, _, row := idleArchiveTestSetup(t, now, nil)
		ft.s.fleet.mu.Lock()
		if ft.s.fleet.interactions == nil {
			ft.s.fleet.interactions = map[string]*pending{}
		}
		ft.s.fleet.interactions["ix_open"] = &pending{ix: proto.Interaction{
			ID: "ix_open", MachineID: "mac", SessionID: row.SessionID, Kind: proto.IxPermission,
		}}
		ft.s.fleet.mu.Unlock()
		w := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
		if w.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
		}
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body["code"] != idleArchiveCodeUnresolvedInteraction {
			t.Fatalf("code = %v, want %s", body["code"], idleArchiveCodeUnresolvedInteraction)
		}
	})

	t.Run("open todo session", func(t *testing.T) {
		ft, _, row := idleArchiveTestSetup(t, now, nil)
		ft.s.todos.mu.Lock()
		ft.s.todos.loaded = true
		ft.s.todos.todos = map[string]*Todo{
			"todo_open": {
				ID:     "todo_open",
				Title:  "active task",
				Agents: []TodoAgent{{MachineID: "mac", SessionID: row.SessionID}},
			},
		}
		ft.s.todos.activeBySession = map[[2]string]string{{"mac", row.SessionID}: "todo_open"}
		ft.s.todos.mu.Unlock()
		w := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
		if w.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
		}
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body["code"] != idleArchiveCodeOpenTodo {
			t.Fatalf("code = %v, want %s", body["code"], idleArchiveCodeOpenTodo)
		}
	})

	busyCases := []struct {
		name     string
		edit     func(*proto.Session)
		wantCode string
	}{
		{"working", func(s *proto.Session) { s.Status, s.State = proto.StatusWorking, proto.StateWorking }, idleArchiveCodeSessionBusy},
		{"blocked", func(s *proto.Session) { s.Status = proto.StatusBlocked }, idleArchiveCodeUnresolvedInteraction},
		{"needs you", func(s *proto.Session) { s.State = proto.StateNeedsYou }, idleArchiveCodeUnresolvedInteraction},
		{"pending interaction count", func(s *proto.Session) { s.PendingInteractions = 1 }, idleArchiveCodeUnresolvedInteraction},
		{"starting", func(s *proto.Session) { s.Starting = true }, idleArchiveCodeSessionBusy},
		{"unknown status", func(s *proto.Session) { s.Status = proto.StatusUnknown }, idleArchiveCodeSessionBusy},
		{"queued input", func(s *proto.Session) { s.Queued = []string{"next"} }, idleArchiveCodeSessionBusy},
		{"loop active", func(s *proto.Session) { s.Loop = &proto.LoopInfo{} }, idleArchiveCodeSessionBusy},
		{"running server port", func(s *proto.Session) { s.Ports = []proto.Port{{Port: 8080}} }, idleArchiveCodeSessionBusy},
		{"running subagent", func(s *proto.Session) { s.Subagents = []proto.Subagent{{Running: true}} }, idleArchiveCodeSessionBusy},
		{"running workflow", func(s *proto.Session) { s.Workflows = []proto.Workflow{{Running: true}} }, idleArchiveCodeSessionBusy},
		{"wake pending", func(s *proto.Session) { wake := now.Add(time.Minute); s.Work = &proto.WorkState{WakeAt: &wake} }, idleArchiveCodeSessionBusy},
		{"external tool", func(s *proto.Session) { s.SourceKind = "codex_app" }, idleArchiveCodeExternalSession},
		{"subagent row", func(s *proto.Session) { s.IsSubagent = true }, idleArchiveCodeSubagentSession},
	}
	for _, tc := range busyCases {
		t.Run(tc.name, func(t *testing.T) {
			ft, _, row := idleArchiveTestSetup(t, now, tc.edit)
			w := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
			if w.Code != http.StatusConflict {
				t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
			}
			var body map[string]any
			_ = json.Unmarshal(w.Body.Bytes(), &body)
			if body["code"] != tc.wantCode {
				t.Fatalf("code = %v, want %s", body["code"], tc.wantCode)
			}
			if len(retiredOf(ft)) != 0 || archivedIDs(t, ft.s)[row.SessionID] {
				t.Fatal("refused session was mutated")
			}
		})
	}
}

func TestIdleArchiveValidationRolesAndArchiveOnly(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ft, _, row := idleArchiveTestSetup(t, now, nil)

	// Disallowed roles and missing extension header are rejected 403.
	for _, tc := range []struct{ role, ext string }{
		{"readonly", ""},
		{roleAnswer, ""},
		{roleExtension, ""},
	} {
		w := postIdleArchive(ft.s, "mac", row.SessionID, tc.role, tc.ext, `{"idle_minutes":10}`)
		if w.Code != http.StatusForbidden {
			t.Fatalf("role=%q ext=%q: got %d, want 403", tc.role, tc.ext, w.Code)
		}
		aw := getIdleArchiveAudit(ft.s, "", tc.role, tc.ext)
		if aw.Code != http.StatusForbidden {
			t.Fatalf("audit role=%q ext=%q: got %d, want 403", tc.role, tc.ext, aw.Code)
		}
	}

	// Invalid request payloads are rejected 400.
	for _, bad := range []string{
		`{}`,
		`{"idle_minutes":0}`,
		`{"idle_minutes":-5}`,
		`{"idle_minutes":10,"unknown":1}`,
		`{"idle_minutes":10,"reason":"bad\u001bctrl"}`,
	} {
		w := postIdleArchive(ft.s, "mac", row.SessionID, roleFull, "", bad)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body=%s: got %d, want 400", bad, w.Code)
		}
	}

	// close_terminal: false archives without closing terminal.
	w := postIdleArchive(ft.s, "mac", row.SessionID, roleFull, "", `{"idle_minutes":15,"close_terminal":false}`)
	if w.Code != http.StatusOK {
		t.Fatalf("archive-only got %d: %s", w.Code, w.Body.String())
	}
	if len(retiredOf(ft)) != 0 {
		t.Fatalf("close_terminal=false still retired: %v", retiredOf(ft))
	}
	if !archivedIDs(t, ft.s)[row.SessionID] {
		t.Fatal("session was not archived")
	}

	// Immediate retry 1s later (within idle_minutes) is idempotent for archive-only even while live.
	ft.s.idleArchiveAudit.now = func() time.Time { return now.Add(time.Second) }
	idemLive := postIdleArchive(ft.s, "mac", row.SessionID, roleFull, "", `{"idle_minutes":15,"close_terminal":false}`)
	if idemLive.Code != http.StatusOK {
		t.Fatalf("immediate live archive-only retry got %d: %s", idemLive.Code, idemLive.Body.String())
	}
	var idemLiveResp assistantwire.IdleArchiveResponse
	if err := json.Unmarshal(idemLive.Body.Bytes(), &idemLiveResp); err != nil {
		t.Fatal(err)
	}
	if !idemLiveResp.OK || !idemLiveResp.Archived || !idemLiveResp.AlreadyArchived {
		t.Fatalf("unexpected live idempotent response: %+v", idemLiveResp)
	}

	// Already-archived offline session is idempotent within idle_minutes too.
	offline := row
	offline.Status, offline.State, offline.PaneID = proto.StatusOffline, proto.StateOffline, ""
	ft.d.setSessions("mac", []proto.Session{offline})
	idem := postIdleArchive(ft.s, "mac", row.SessionID, roleFull, "", `{"idle_minutes":15}`)
	if idem.Code != http.StatusOK {
		t.Fatalf("idempotent archive got %d: %s", idem.Code, idem.Body.String())
	}
	var idemResp assistantwire.IdleArchiveResponse
	if err := json.Unmarshal(idem.Body.Bytes(), &idemResp); err != nil {
		t.Fatal(err)
	}
	if !idemResp.OK || !idemResp.Archived || !idemResp.AlreadyArchived || idemResp.Close == nil || idemResp.Close.Outcome != archiveCloseNotRunning {
		t.Fatalf("unexpected idempotent response: %+v", idemResp)
	}
}

func TestIdleArchiveAdversarialReviewRegressions(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	t.Run("P0-1 offline or warm-grace computer refuses unverified live session", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			setup func(*fleetTest)
		}{
			{"disconnected computer", func(ft *fleetTest) {
				ft.d.mu.Lock()
				ft.d.machines["mac"].away = true
				ft.d.mu.Unlock()
			}},
			{"warm-start grace active", func(ft *fleetTest) {
				ft.d.mu.Lock()
				ft.d.machines["mac"].shownUntil = now.Add(time.Minute)
				ft.d.mu.Unlock()
			}},
			{"unlisted connection", func(ft *fleetTest) {
				ft.d.mu.Lock()
				ft.d.machines["mac"].unlisted = true
				ft.d.mu.Unlock()
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				ft, _, row := idleArchiveTestSetup(t, now, nil)
				tc.setup(ft)
				w := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
				if w.Code != http.StatusConflict {
					t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
				}
				var body map[string]any
				_ = json.Unmarshal(w.Body.Bytes(), &body)
				if body["code"] != idleArchiveCodeSessionUnverified {
					t.Fatalf("code = %v, want %s", body["code"], idleArchiveCodeSessionUnverified)
				}
				if len(retiredOf(ft)) != 0 || archivedIDs(t, ft.s)[row.SessionID] {
					t.Fatal("unverified live session was archived or retired")
				}
			})
		}
	})

	t.Run("P0-2 refused or failed close rolls back provisional archive", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			retireErr  error
			wantStatus int
			wantCode   string
		}{
			{"node working refusal", &rpc.Error{Code: "not_retirable", Message: "the agent is working"}, http.StatusConflict, idleArchiveCodeCloseRefused},
			{"node generic failure", &rpc.Error{Message: "herdr: no such pane"}, http.StatusBadGateway, idleArchiveCodeCloseFailed},
		} {
			t.Run(tc.name, func(t *testing.T) {
				ft, rec, row := idleArchiveTestSetup(t, now, nil)
				ft.f.mu.Lock()
				ft.f.retireErr = tc.retireErr
				ft.f.mu.Unlock()
				w := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
				if w.Code != tc.wantStatus {
					t.Fatalf("got %d (%s), want %d", w.Code, w.Body.String(), tc.wantStatus)
				}
				var body map[string]any
				_ = json.Unmarshal(w.Body.Bytes(), &body)
				if body["code"] != tc.wantCode || body["audit_persisted"] != true {
					t.Fatalf("unexpected body: %+v", body)
				}
				if archivedIDs(t, ft.s)[row.SessionID] {
					t.Fatal("provisional archive was not rolled back after close refusal/failure")
				}
				if rec.count() != 1 {
					t.Fatalf("plugin_audit rows = %d, want 1", rec.count())
				}
			})
		}
	})

	t.Run("P1-1 unknown main/leader ID returns 404 without oracle and archived hidden space keeps Space", func(t *testing.T) {
		ft, rec, _ := idleArchiveTestSetup(t, now, nil)
		setSettings(t, ft.s, func(v *Settings) {
			v.Main = "secret-main-on-other-host"
			v.Leaders = []string{"secret-lead-on-other-host"}
		})
		for _, probe := range []string{"secret-main-on-other-host", "secret-lead-on-other-host"} {
			w := postIdleArchive(ft.s, "mac", probe, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
			if w.Code != http.StatusNotFound {
				t.Fatalf("probe %q: got %d (%s), want 404", probe, w.Code, w.Body.String())
			}
		}
		if rec.count() != 0 {
			t.Fatalf("oracle probes wrote %d audit entries, want 0", rec.count())
		}

		// Hidden privacy space with Cwd outside space directory preserves Space in archiveSnapshot.
		ft2, rec2, row2 := idleArchiveTestSetup(t, now, func(s *proto.Session) {
			s.Space = "health"
			s.Cwd = "/tmp/unrelated"
		})
		if _, err := ft2.s.setAgentArchive(context.Background(), "mac", row2.SessionID, true); err != nil {
			t.Fatal(err)
		}
		// Drop from live node list so only the archived snapshot remains.
		ft2.d.setSessions("mac", nil)
		w := postIdleArchive(ft2.s, "mac", row2.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
		if w.Code != http.StatusNotFound {
			t.Fatalf("archived hidden-space snapshot returned %d (%s), want 404", w.Code, w.Body.String())
		}
		if rec2.count() != 0 {
			t.Fatalf("hidden-space snapshot probe wrote %d audit entries, want 0", rec2.count())
		}
	})

	t.Run("P1-2 outside direction lead, open responsible todo, and offline desktop app refused", func(t *testing.T) {
		// Outside direction lead
		ft1, _, row1 := idleArchiveTestSetup(t, now, nil)
		ft1.s.directions.mu.Lock()
		ft1.s.directions.loaded = true
		ft1.s.directions.m = map[string]*direction{
			"dir-outside": {
				Outside: &directionOutside{MachineID: "mac", SessionID: row1.SessionID},
			},
		}
		ft1.s.directions.mu.Unlock()
		w1 := postIdleArchive(ft1.s, "mac", row1.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
		var b1 map[string]any
		_ = json.Unmarshal(w1.Body.Bytes(), &b1)
		if w1.Code != http.StatusConflict || b1["code"] != idleArchiveCodeDirectionLead {
			t.Fatalf("outside direction lead: got %d %+v, want 409 %s", w1.Code, b1, idleArchiveCodeDirectionLead)
		}

		// Open-todo Responsible session
		ft2, _, row2 := idleArchiveTestSetup(t, now, nil)
		ft2.s.todos.mu.Lock()
		ft2.s.todos.loaded = true
		ft2.s.todos.todos = map[string]*Todo{
			"todo_resp": {
				ID:          "todo_resp",
				Title:       "external responsible",
				Responsible: &db.TodoResponsible{Kind: db.ResponsibleSession, Name: "worker", MachineID: "mac", SessionID: row2.SessionID},
			},
		}
		ft2.s.todos.mu.Unlock()
		w2 := postIdleArchive(ft2.s, "mac", row2.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
		var b2 map[string]any
		_ = json.Unmarshal(w2.Body.Bytes(), &b2)
		if w2.Code != http.StatusConflict || b2["code"] != idleArchiveCodeOpenTodo {
			t.Fatalf("open responsible todo: got %d %+v, want 409 %s", w2.Code, b2, idleArchiveCodeOpenTodo)
		}

		// Offline desktop-app session (AgentSurface == desktop, SourceKind empty)
		ft3, _, row3 := idleArchiveTestSetup(t, now, func(s *proto.Session) {
			s.Status, s.State, s.PaneID = proto.StatusOffline, proto.StateOffline, ""
			s.SourceKind = ""
			s.AgentSurface = transcript.SurfaceDesktop
		})
		w3 := postIdleArchive(ft3.s, "mac", row3.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
		var b3 map[string]any
		_ = json.Unmarshal(w3.Body.Bytes(), &b3)
		if w3.Code != http.StatusConflict || b3["code"] != idleArchiveCodeExternalSession {
			t.Fatalf("offline desktop session: got %d %+v, want 409 %s", w3.Code, b3, idleArchiveCodeExternalSession)
		}
	})

	t.Run("P1-3 empty or unrecognized live status fails closed", func(t *testing.T) {
		for _, badStatus := range []string{"", "future_busy_status"} {
			ft, _, row := idleArchiveTestSetup(t, now, func(s *proto.Session) {
				s.Status = badStatus
				s.State = proto.StateUnknown
			})
			w := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
			var body map[string]any
			_ = json.Unmarshal(w.Body.Bytes(), &body)
			if w.Code != http.StatusConflict || body["code"] != idleArchiveCodeSessionUnverified {
				t.Fatalf("status=%q: got %d %+v, want 409 %s", badStatus, w.Code, body, idleArchiveCodeSessionUnverified)
			}
			if len(retiredOf(ft)) != 0 || archivedIDs(t, ft.s)[row.SessionID] {
				t.Fatalf("status=%q mutated session", badStatus)
			}
		}
	})

	t.Run("P1-5 audit store failure fails closed and emits no dangling audit_id", func(t *testing.T) {
		// Unloadable audit store blocks before any mutation.
		ft1, rec1, row1 := idleArchiveTestSetup(t, now, nil)
		rec1.mu.Lock()
		rec1.failAuditLoad = true
		rec1.mu.Unlock()
		w1 := postIdleArchive(ft1.s, "mac", row1.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
		if w1.Code != http.StatusServiceUnavailable {
			t.Fatalf("audit load failure got %d (%s), want 503", w1.Code, w1.Body.String())
		}
		if len(retiredOf(ft1)) != 0 || archivedIDs(t, ft1.s)[row1.SessionID] {
			t.Fatal("mutation occurred despite unreadable audit store")
		}

		// Audit write failure on archive-only rolls back archive, emits no native archive wish or plugin_audit row, and returns 503.
		ft2, rec2, row2 := idleArchiveTestSetup(t, now, nil)
		rec2.mu.Lock()
		rec2.failAuditWrite = true
		rec2.mu.Unlock()
		w2 := postIdleArchive(ft2.s, "mac", row2.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10,"close_terminal":false}`)
		if w2.Code != http.StatusServiceUnavailable {
			t.Fatalf("audit write failure on archive-only got %d (%s), want 503", w2.Code, w2.Body.String())
		}
		if archivedIDs(t, ft2.s)[row2.SessionID] {
			t.Fatal("archive-only was not rolled back after audit write failure")
		}
		if rec2.count() != 0 {
			t.Fatalf("rolled-back archive-only wrote %d plugin_audit rows, want 0", rec2.count())
		}
		if got := ft2.s.nativeArchiveView("mac", row2.SessionID); got != nil {
			t.Fatalf("rolled-back archive-only leaked native archive intent: %+v", got)
		}

		// Audit write failure on offline default close_terminal=true also rolls back cleanly with no side effects.
		ftOff, recOff, rowOff := idleArchiveTestSetup(t, now, func(s *proto.Session) {
			s.Status, s.State, s.PaneID = proto.StatusOffline, proto.StateOffline, ""
		})
		recOff.mu.Lock()
		recOff.failAuditWrite = true
		recOff.mu.Unlock()
		wOff := postIdleArchive(ftOff.s, "mac", rowOff.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
		if wOff.Code != http.StatusServiceUnavailable {
			t.Fatalf("audit write failure on offline archive got %d (%s), want 503", wOff.Code, wOff.Body.String())
		}
		if archivedIDs(t, ftOff.s)[rowOff.SessionID] {
			t.Fatal("offline archive was not rolled back after audit write failure")
		}
		if recOff.count() != 0 {
			t.Fatalf("rolled-back offline archive wrote %d plugin_audit rows, want 0", recOff.count())
		}
		if got := ftOff.s.nativeArchiveView("mac", rowOff.SessionID); got != nil {
			t.Fatalf("rolled-back offline archive leaked native archive intent: %+v", got)
		}

		// Audit attempt write failure before live close aborts with 503, never closes terminal, rolls back archive, and skips plugin_audit.
		ftLive, recLive, rowLive := idleArchiveTestSetup(t, now, nil)
		recLive.mu.Lock()
		recLive.failAuditWrite = true
		recLive.mu.Unlock()
		wLive := postIdleArchive(ftLive.s, "mac", rowLive.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10,"reason":"reclaim"}`)
		if wLive.Code != http.StatusServiceUnavailable {
			t.Fatalf("live close with attempt audit failure got %d (%s), want 503", wLive.Code, wLive.Body.String())
		}
		if len(retiredOf(ftLive)) != 0 {
			t.Fatalf("attempt audit failure still retired terminal: %v", retiredOf(ftLive))
		}
		if archivedIDs(t, ftLive.s)[rowLive.SessionID] {
			t.Fatal("attempt audit failure did not roll back provisional archive")
		}
		if recLive.count() != 0 {
			t.Fatalf("failed attempt audit mirrored %d rows to plugin_audit, want 0", recLive.count())
		}
		if got := ftLive.s.nativeArchiveView("mac", rowLive.SessionID); got != nil {
			t.Fatalf("failed attempt audit leaked native archive intent: %+v", got)
		}

		// Audit finalization write failure after durable attempt + terminal close returns 503 (never 200),
		// keeps the durable attempt entry in storage, and queues durable retry that finalizes once storage recovers.
		ftFinal, recFinal, rowFinal := idleArchiveTestSetup(t, now, nil)
		recFinal.mu.Lock()
		recFinal.failAuditWriteOnCall = 2 // 1st write persists close_attempted; 2nd write (final outcome) fails
		recFinal.mu.Unlock()
		wFinal := postIdleArchive(ftFinal.s, "mac", rowFinal.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10,"reason":"reclaim-final"}`)
		if wFinal.Code != http.StatusServiceUnavailable {
			t.Fatalf("post-close finalization audit failure got %d (%s), want 503", wFinal.Code, wFinal.Body.String())
		}
		var respFinal assistantwire.IdleArchiveResponse
		if err := json.Unmarshal(wFinal.Body.Bytes(), &respFinal); err != nil {
			t.Fatalf("decode finalization failure response: %v", err)
		}
		if respFinal.OK || respFinal.Code != idleArchiveCodeAuditFinalizationPending || !respFinal.Archived ||
			respFinal.Close == nil || !respFinal.Close.Closed || respFinal.AuditID == "" ||
			respFinal.AuditPersisted || !respFinal.AuditFinalizationPending {
			t.Fatalf("unexpected post-close finalization failure response: %+v", respFinal)
		}
		if len(retiredOf(ftFinal)) != 1 {
			t.Fatalf("expected 1 retired terminal after persisted attempt, got %v", retiredOf(ftFinal))
		}
		var savedBeforeRetry []assistantwire.IdleArchiveAuditEntry
		if _, err := recFinal.Store.Setting(context.Background(), idleArchiveAuditKey, &savedBeforeRetry); err != nil {
			t.Fatalf("read persisted attempt from store: %v", err)
		}
		if len(savedBeforeRetry) != 1 || savedBeforeRetry[0].ID != respFinal.AuditID ||
			savedBeforeRetry[0].Outcome != idleArchiveOutcomeCloseAttempted ||
			savedBeforeRetry[0].Actor != "extension:guardrail-ext" || savedBeforeRetry[0].Reason != "reclaim-final" {
			t.Fatalf("persisted attempt record = %+v", savedBeforeRetry)
		}
		if recFinal.count() != 0 {
			t.Fatalf("pending finalization already mirrored %d plugin_audit rows, want 0", recFinal.count())
		}

		// Once audit storage recovers, durable retry / audit query flushes the final outcome and mirrors plugin_audit.
		recFinal.mu.Lock()
		recFinal.failAuditWriteOnCall = 0
		recFinal.mu.Unlock()
		aw := getIdleArchiveAudit(ftFinal.s, "?session_id="+rowFinal.SessionID, roleExtension, "guardrail-ext")
		if aw.Code != http.StatusOK {
			t.Fatalf("audit read after recovery got %d: %s", aw.Code, aw.Body.String())
		}
		var auditResp assistantwire.IdleArchiveAuditResponse
		if err := json.Unmarshal(aw.Body.Bytes(), &auditResp); err != nil {
			t.Fatal(err)
		}
		if len(auditResp.Entries) != 1 || auditResp.Entries[0].ID != respFinal.AuditID ||
			auditResp.Entries[0].Outcome != idleArchiveOutcomeArchivedAndClosed ||
			auditResp.Entries[0].CloseOutcome != archiveCloseClosed {
			t.Fatalf("finalized audit entries = %+v", auditResp.Entries)
		}
		if recFinal.count() != 1 {
			t.Fatalf("plugin_audit rows after durable retry flush = %d, want 1", recFinal.count())
		}

		// Audit write failure on refusal emits audit_persisted=false, omits audit_id, and skips plugin_audit.
		ft3, rec3, row3 := idleArchiveTestSetup(t, now, func(s *proto.Session) {
			s.Status, s.State = proto.StatusWorking, proto.StateWorking
		})
		rec3.mu.Lock()
		rec3.failAuditWrite = true
		rec3.mu.Unlock()
		w3 := postIdleArchive(ft3.s, "mac", row3.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
		if w3.Code != http.StatusConflict {
			t.Fatalf("refusal with failing audit got %d, want 409", w3.Code)
		}
		var b3 map[string]any
		_ = json.Unmarshal(w3.Body.Bytes(), &b3)
		if b3["audit_persisted"] != false {
			t.Fatalf("expected audit_persisted=false, got %+v", b3)
		}
		if _, hasID := b3["audit_id"]; hasID {
			t.Fatalf("refusal emitted dangling audit_id on persistence failure: %+v", b3)
		}
		if rec3.count() != 0 {
			t.Fatalf("failed refusal audit mirrored %d rows to plugin_audit, want 0", rec3.count())
		}
	})

	t.Run("R2-P1-2 closing already-archived live session fences exit transcript writes and rolls back provisional ClosedAt on refusal", func(t *testing.T) {
		// (a) Exit transcript write during retireArchived does NOT unarchive an already-archived live session.
		realNow := time.Now().UTC()
		ft, _, row := idleArchiveTestSetup(t, realNow, nil)
		// Pre-archive without closing (ClosedAt is zero).
		wArch := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10,"close_terminal":false}`)
		if wArch.Code != http.StatusOK {
			t.Fatalf("pre-archive failed: %d %s", wArch.Code, wArch.Body.String())
		}
		// During session.retire_guarded, simulate the agent writing an exit transcript line and the node pushing
		// an updated session list before retire returns.
		ft.f.answerNode(deviceSessionRetireGuarded, func(_ string, _ json.RawMessage) (any, error) {
			bumped := row
			bumped.UpdatedAt = time.Now().UTC()
			ft.d.setSessions("mac", []proto.Session{bumped})
			if _, changed, err := ft.s.archivedSessions(context.Background(), "mac", []proto.Session{bumped}, ft.s.archiveMark()); err != nil {
				t.Errorf("mid-close archivedSessions failed: %v", err)
			} else if changed {
				t.Errorf("mid-close exit transcript write unexpectedly changed archive state")
			}
			return map[string]any{"closed": true, "guard_verified": true}, nil
		})
		wClose := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10,"close_terminal":true}`)
		if wClose.Code != http.StatusOK {
			t.Fatalf("close of already-archived live session got %d (%s), want 200", wClose.Code, wClose.Body.String())
		}
		var resp assistantwire.IdleArchiveResponse
		_ = json.Unmarshal(wClose.Body.Bytes(), &resp)
		if !resp.Archived || !resp.AlreadyArchived || resp.Close == nil || !resp.Close.Closed {
			t.Fatalf("unexpected close response: %+v", resp)
		}
		if !archivedIDs(t, ft.s)[row.SessionID] {
			t.Fatal("already-archived live session was unarchived by exit transcript write during close")
		}

		// (b) When node refuses close on an already-archived live session, provisional ClosedAt is cleared
		// while Archived remains true.
		ft2, _, row2 := idleArchiveTestSetup(t, realNow, nil)
		wArch2 := postIdleArchive(ft2.s, "mac", row2.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10,"close_terminal":false}`)
		if wArch2.Code != http.StatusOK {
			t.Fatalf("pre-archive 2 failed: %d %s", wArch2.Code, wArch2.Body.String())
		}
		ft2.f.mu.Lock()
		ft2.f.retireErr = &rpc.Error{Code: "not_retirable", Message: "cannot resume"}
		ft2.f.mu.Unlock()
		wRef := postIdleArchive(ft2.s, "mac", row2.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10,"close_terminal":true}`)
		if wRef.Code != http.StatusConflict {
			t.Fatalf("refused close of already-archived live session got %d (%s), want 409", wRef.Code, wRef.Body.String())
		}
		if !archivedIDs(t, ft2.s)[row2.SessionID] {
			t.Fatal("refused close unexpectedly unarchived already-archived session")
		}
		key2 := runtimeListID("mac", archiveSessionOf(row2))
		ft2.s.agentArchives.lock(context.Background())
		rec2 := ft2.s.agentArchives.records[key2]
		ft2.s.agentArchives.unlock()
		if !rec2.Archived || !rec2.ClosedAt.IsZero() {
			t.Fatalf("expected Archived=true and zero ClosedAt after refused close rollback, got archived=%v closedAt=%v", rec2.Archived, rec2.ClosedAt)
		}
	})
}

func TestIdleArchiveConcurrentRaceAndHiddenSpace(t *testing.T) {
	now := time.Date(2026, 11, 26, 0, 30, 0, 0, time.UTC)
	ft, rec, row := idleArchiveTestSetup(t, now, func(s *proto.Session) {
		s.Space = "health"
	})
	// Health space is hidden from extensions by default -> 404 and zero audit leakage.
	w := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("hidden space extension call got %d: %s, want 404", w.Code, w.Body.String())
	}
	if rec.count() != 0 {
		t.Fatalf("hidden space probe must not record audit entry, got %d", rec.count())
	}

	// Concurrent requests against a visible busy session and audit reader stay race-free.
	ft2, _, row2 := idleArchiveTestSetup(t, now, func(s *proto.Session) {
		s.Status, s.State = proto.StatusWorking, proto.StateWorking
	})
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			res := postIdleArchive(ft2.s, "mac", row2.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
			if res.Code != http.StatusConflict {
				t.Errorf("concurrent refusal got %d, want 409", res.Code)
			}
		}()
		go func() {
			defer wg.Done()
			res := getIdleArchiveAudit(ft2.s, "?limit=10", roleExtension, "guardrail-ext")
			if res.Code != http.StatusOK {
				t.Errorf("concurrent audit read got %d, want 200", res.Code)
			}
		}()
	}
	wg.Wait()
}

func TestIdleArchiveP1AuditReviewFixes(t *testing.T) {
	t.Run("P1-1 concurrent open-todo bind during stageIdleArchiveRecord DB wait refuses close", func(t *testing.T) {
		now := time.Date(2026, 11, 26, 0, 30, 0, 0, time.UTC)
		ft, rec, row := idleArchiveTestSetup(t, now, nil)
		ft.s.todos.mu.Lock()
		ft.s.todos.loaded = true
		ft.s.todos.todos = map[string]*Todo{
			"todo_concurrent": {ID: "todo_concurrent", Title: "concurrent open task"},
		}
		ft.s.todos.activeBySession = map[[2]string]string{}
		ft.s.todos.mu.Unlock()

		stageEntered := make(chan struct{})
		stageRelease := make(chan struct{})
		var once sync.Once
		rec.mu.Lock()
		rec.onPutSetting = func(key string) {
			if key == agentArchivesKey {
				once.Do(func() {
					close(stageEntered)
					<-stageRelease
				})
			}
		}
		rec.mu.Unlock()

		respCh := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			respCh <- postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10,"reason":"race-check"}`)
		}()

		// Initial evaluation passed; handler is now paused inside stageIdleArchiveRecord's PutSetting.
		select {
		case <-stageEntered:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for stageIdleArchiveRecord write")
		}

		// Concurrently bind the session to an open to-do (writes activeBySession).
		linkDone := make(chan error, 1)
		go func() {
			linkDone <- ft.s.linkTodoAgent(context.Background(), "todo_concurrent", "mac", row.SessionID, row.Title, row.Cwd, row.Agent)
		}()
		bindDeadline := time.Now().Add(5 * time.Second)
		for {
			ft.s.todos.mu.RLock()
			bound := ft.s.todos.activeBySession[[2]string{"mac", row.SessionID}] == "todo_concurrent"
			ft.s.todos.mu.RUnlock()
			if bound {
				break
			}
			if time.Now().After(bindDeadline) {
				close(stageRelease)
				t.Fatal("timed out waiting for concurrent activeBySession bind")
			}
			runtime.Gosched()
		}
		close(stageRelease)
		select {
		case err := <-linkDone:
			if err != nil {
				t.Fatalf("concurrent linkTodoAgent failed: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for concurrent linkTodoAgent to finish")
		}

		var w *httptest.ResponseRecorder
		select {
		case w = <-respCh:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for idle-archive response")
		}
		if w.Code != http.StatusConflict {
			t.Fatalf("expected 409 after concurrent open-todo bind, got %d: %s", w.Code, w.Body.String())
		}
		var refusal assistantwire.IdleArchiveRefusalResponse
		if err := json.Unmarshal(w.Body.Bytes(), &refusal); err != nil {
			t.Fatalf("decode refusal: %v", err)
		}
		if refusal.Code != idleArchiveCodeOpenTodo || !refusal.AuditPersisted || refusal.AuditID == "" {
			t.Fatalf("unexpected refusal: %+v", refusal)
		}
		if len(retiredOf(ft)) != 0 {
			t.Fatalf("concurrently linked open-todo session was still retired: %v", retiredOf(ft))
		}
		if archivedIDs(t, ft.s)[row.SessionID] {
			t.Fatal("provisional archive was not rolled back after concurrent open-todo bind")
		}
	})

	t.Run("P1-1 close fence serializes concurrent linkTodoAgent and sends node activity guard", func(t *testing.T) {
		now := time.Date(2026, 11, 26, 0, 30, 0, 0, time.UTC)
		ft, _, row := idleArchiveTestSetup(t, now, func(s *proto.Session) {
			s.Rev = 4096
			s.PaneID = "w2:p3"
		})
		ft.s.todos.mu.Lock()
		ft.s.todos.loaded = true
		ft.s.todos.todos = map[string]*Todo{
			"todo_wait": {ID: "todo_wait", Title: "waits for close fence"},
		}
		ft.s.todos.activeBySession = map[[2]string]string{}
		ft.s.todos.mu.Unlock()

		retireEntered := make(chan struct{})
		retireRelease := make(chan struct{})
		var gotParams sessionRetireParams
		ft.f.answerNode(deviceSessionRetireGuarded, func(_ string, raw json.RawMessage) (any, error) {
			if err := json.Unmarshal(raw, &gotParams); err != nil {
				t.Errorf("unmarshal retire params: %v", err)
			}
			close(retireEntered)
			<-retireRelease
			return map[string]any{"closed": true, "guard_verified": true}, nil
		})

		closeDone := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			closeDone <- postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
		}()
		select {
		case <-retireEntered:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for retire call")
		}

		// Verify manager passed activity version and pane guard to dedicated guarded node retire.
		if !gotParams.CloseTerminal || !gotParams.HasExpectedRev || gotParams.ExpectedRev != 4096 ||
			!gotParams.ExpectedUpdatedAt.Equal(row.UpdatedAt) || gotParams.ExpectedPane != "w2:p3" {
			t.Fatalf("unexpected node retire guard params: %+v", gotParams)
		}
		if !ft.s.idleCloseFenceActive("mac", row.SessionID) {
			t.Fatal("expected close fence active while retire is in flight")
		}

		// A concurrent open-todo link must block on the close fence rather than mutating activeBySession mid-close.
		linkDone := make(chan error, 1)
		go func() {
			linkDone <- ft.s.linkTodoAgent(context.Background(), "todo_wait", "mac", row.SessionID, row.Title, row.Cwd, row.Agent)
		}()
		select {
		case err := <-linkDone:
			t.Fatalf("linkTodoAgent bypassed active close fence mid-retire: %v", err)
		default:
		}
		close(retireRelease)

		var w *httptest.ResponseRecorder
		select {
		case w = <-closeDone:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for close completion")
		}
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		select {
		case err := <-linkDone:
			if err != nil {
				t.Fatalf("linkTodoAgent after fence release failed: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for fenced linkTodoAgent to complete after retire")
		}
	})

	t.Run("P1-2 manager wall clock 5m ahead of node refuses recently-idle live session", func(t *testing.T) {
		nodeNow := time.Date(2026, 11, 26, 0, 30, 0, 0, time.UTC)
		managerNow := nodeNow.Add(5 * time.Minute) // Manager wall clock is 5 minutes fast relative to node.
		connAt := managerNow.Add(-2 * time.Hour)

		ft, _, row := idleArchiveTestSetup(t, managerNow, func(s *proto.Session) {
			// Session actually stopped 10s ago on node clock; managerNow - UpdatedAt looks like 310s (> 1m).
			s.UpdatedAt = nodeNow.Add(-10 * time.Second)
			s.Rev = 200
		})
		ft.d.mu.Lock()
		ft.d.machines["mac"].connected = connAt
		ft.d.mu.Unlock()

		// Simulate real sessions.changed timeline on manager's local clock: working 20s ago, idle 10s ago.
		clockNow := managerNow.Add(-20 * time.Second)
		ft.s.idleArchiveAudit.now = func() time.Time { return clockNow }
		working := row
		working.Status, working.State = proto.StatusWorking, proto.StateWorking
		ft.d.setSessions("mac", []proto.Session{working})
		ft.s.setSessions("mac", []proto.Session{working})

		clockNow = managerNow.Add(-10 * time.Second)
		ft.d.setSessions("mac", []proto.Session{row})
		ft.s.setSessions("mac", []proto.Session{row})

		// At managerNow, cross-machine wall delta is 310s (> 60s), but local monotonic observation is only 10s.
		clockNow = managerNow
		wEarly := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":1}`)
		if wEarly.Code != http.StatusConflict {
			t.Fatalf("skewed clock early close got %d (%s), want 409", wEarly.Code, wEarly.Body.String())
		}
		var refusal assistantwire.IdleArchiveRefusalResponse
		if err := json.Unmarshal(wEarly.Body.Bytes(), &refusal); err != nil {
			t.Fatal(err)
		}
		if refusal.Code != idleArchiveCodeNotIdleLongEnough {
			t.Fatalf("refusal code = %q, want %q", refusal.Code, idleArchiveCodeNotIdleLongEnough)
		}
		if len(retiredOf(ft)) != 0 || archivedIDs(t, ft.s)[row.SessionID] {
			t.Fatal("skewed clock caused premature archive or close")
		}

		// Advancing manager's local clock past 60s of continuous unchanged observation allows guarded close.
		clockNow = managerNow.Add(51 * time.Second) // 61s continuous local idle observation
		wOK := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":1}`)
		if wOK.Code != http.StatusOK {
			t.Fatalf("after sufficient local observation got %d (%s), want 200", wOK.Code, wOK.Body.String())
		}
		var okResp assistantwire.IdleArchiveResponse
		if err := json.Unmarshal(wOK.Body.Bytes(), &okResp); err != nil {
			t.Fatal(err)
		}
		if okResp.ObservedIdleSeconds != 61 {
			t.Fatalf("observed_idle_seconds = %d, want 61", okResp.ObservedIdleSeconds)
		}
	})

	t.Run("P1-2 fresh connection, reconnect, rev bump, and unobserved live session fail closed", func(t *testing.T) {
		now := time.Date(2026, 11, 26, 0, 30, 0, 0, time.UTC)

		// (a) Freshly established node connection with old node UpdatedAt (2h ago) but only 15s of local observation.
		ftFresh, _, rowFresh := idleArchiveTestSetup(t, now, func(s *proto.Session) {
			s.UpdatedAt = now.Add(-2 * time.Hour)
		})
		freshConn := now.Add(-15 * time.Second)
		ftFresh.d.mu.Lock()
		ftFresh.d.machines["mac"].connected = freshConn
		ftFresh.d.mu.Unlock()
		ftFresh.s.idleArchiveAudit.now = func() time.Time { return freshConn }
		ftFresh.s.setSessions("mac", []proto.Session{rowFresh})
		ftFresh.s.idleArchiveAudit.now = func() time.Time { return now }

		wFresh := postIdleArchive(ftFresh.s, "mac", rowFresh.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":1}`)
		if wFresh.Code != http.StatusConflict {
			t.Fatalf("fresh connection got %d (%s), want 409", wFresh.Code, wFresh.Body.String())
		}
		var freshRef assistantwire.IdleArchiveRefusalResponse
		_ = json.Unmarshal(wFresh.Body.Bytes(), &freshRef)
		if freshRef.Code != idleArchiveCodeIdleUnverified {
			t.Fatalf("fresh connection code = %q, want %q", freshRef.Code, idleArchiveCodeIdleUnverified)
		}
		if len(retiredOf(ftFresh)) != 0 || archivedIDs(t, ftFresh.s)[rowFresh.SessionID] {
			t.Fatal("fresh connection mutated session")
		}

		// (b) Reconnect resets previously accumulated 45m local observation window.
		ftReconn, _, rowReconn := idleArchiveTestSetup(t, now, nil)
		reconnAt := now.Add(-5 * time.Second)
		ftReconn.d.mu.Lock()
		ftReconn.d.machines["mac"].connected = reconnAt
		ftReconn.d.mu.Unlock()
		ftReconn.s.idleArchiveAudit.now = func() time.Time { return reconnAt }
		ftReconn.s.setSessions("mac", []proto.Session{rowReconn})
		ftReconn.s.idleArchiveAudit.now = func() time.Time { return now }

		wReconn := postIdleArchive(ftReconn.s, "mac", rowReconn.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":1}`)
		if wReconn.Code != http.StatusConflict {
			t.Fatalf("post-reconnect got %d (%s), want 409", wReconn.Code, wReconn.Body.String())
		}
		var reconnRef assistantwire.IdleArchiveRefusalResponse
		_ = json.Unmarshal(wReconn.Body.Bytes(), &reconnRef)
		if reconnRef.Code != idleArchiveCodeIdleUnverified {
			t.Fatalf("post-reconnect code = %q, want %q", reconnRef.Code, idleArchiveCodeIdleUnverified)
		}

		// (c) Activity version (Rev) change resets local monotonic observation even when UpdatedAt stayed old.
		ftRev, _, rowRev := idleArchiveTestSetup(t, now, nil)
		bumped := rowRev
		bumped.Rev = rowRev.Rev + 1
		ftRev.d.setSessions("mac", []proto.Session{bumped})
		ftRev.s.idleArchiveAudit.now = func() time.Time { return now.Add(-5 * time.Second) }
		ftRev.s.setSessions("mac", []proto.Session{bumped})
		ftRev.s.idleArchiveAudit.now = func() time.Time { return now }

		wRev := postIdleArchive(ftRev.s, "mac", bumped.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":1}`)
		if wRev.Code != http.StatusConflict {
			t.Fatalf("rev bump got %d (%s), want 409", wRev.Code, wRev.Body.String())
		}
		var revRef assistantwire.IdleArchiveRefusalResponse
		_ = json.Unmarshal(wRev.Body.Bytes(), &revRef)
		if revRef.Code != idleArchiveCodeNotIdleLongEnough {
			t.Fatalf("rev bump code = %q, want %q", revRef.Code, idleArchiveCodeNotIdleLongEnough)
		}

		// (d) Live session never observed on manager fails closed with idle_unverified.
		ftUnobs, _, rowUnobs := idleArchiveTestSetup(t, now, nil)
		ftUnobs.s.idleArchiveAudit.obsMu.Lock()
		ftUnobs.s.idleArchiveAudit.liveObs = nil
		ftUnobs.s.idleArchiveAudit.obsMu.Unlock()
		wUnobs := postIdleArchive(ftUnobs.s, "mac", rowUnobs.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":1}`)
		if wUnobs.Code != http.StatusConflict {
			t.Fatalf("unobserved live session got %d (%s), want 409", wUnobs.Code, wUnobs.Body.String())
		}
		var unobsRef assistantwire.IdleArchiveRefusalResponse
		_ = json.Unmarshal(wUnobs.Body.Bytes(), &unobsRef)
		if unobsRef.Code != idleArchiveCodeIdleUnverified {
			t.Fatalf("unobserved code = %q, want %q", unobsRef.Code, idleArchiveCodeIdleUnverified)
		}
	})

	t.Run("P1-3 background durable retry finalizes post-close audit outcome automatically", func(t *testing.T) {
		now := time.Date(2026, 11, 26, 0, 30, 0, 0, time.UTC)
		ft, rec, row := idleArchiveTestSetup(t, now, nil)
		ft.s.idleArchiveAudit.mu.Lock()
		ft.s.idleArchiveAudit.retryInterval = 5 * time.Millisecond
		ft.s.idleArchiveAudit.mu.Unlock()

		persistedCh := make(chan struct{}, 4)
		rec.mu.Lock()
		rec.failAuditWriteOnCall = 2
		rec.onAuditPersisted = func() {
			select {
			case persistedCh <- struct{}{}:
			default:
			}
		}
		rec.mu.Unlock()

		w := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10,"reason":"bg-retry"}`)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("got %d (%s), want 503", w.Code, w.Body.String())
		}
		var resp assistantwire.IdleArchiveResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		// Drain the attempt write signal, then wait for background retry to succeed automatically once write is re-enabled.
		select {
		case <-persistedCh:
		case <-time.After(5 * time.Second):
			t.Fatal("missing initial attempt write signal")
		}
		select {
		case <-persistedCh:
		case <-time.After(5 * time.Second):
			t.Fatal("background durable retry did not finalize audit entry")
		}
		var saved []assistantwire.IdleArchiveAuditEntry
		if _, err := rec.Store.Setting(context.Background(), idleArchiveAuditKey, &saved); err != nil {
			t.Fatal(err)
		}
		if len(saved) != 1 || saved[0].ID != resp.AuditID || saved[0].Outcome != idleArchiveOutcomeArchivedAndClosed {
			t.Fatalf("background finalized entries = %+v", saved)
		}
	})
}

func TestIdleArchiveMixedVersionOldNodeRefusesUnguardedDowngrade(t *testing.T) {
	now := time.Date(2026, 11, 26, 0, 30, 0, 0, time.UTC)

	t.Run("old node without session.retire_guarded fails closed without calling session.retire", func(t *testing.T) {
		ft, rec, row := idleArchiveTestSetup(t, now, nil)
		unguardedCalled := false
		ft.f.answerNode(deviceSessionRetire, func(_ string, _ json.RawMessage) (any, error) {
			unguardedCalled = true
			return map[string]any{"closed": true}, nil
		})
		ft.f.answerNode(deviceSessionRetireGuarded, func(_ string, _ json.RawMessage) (any, error) {
			return nil, &rpc.Error{Message: "unknown method " + deviceSessionRetireGuarded}
		})

		w := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10,"reason":"mixed-version"}`)
		if w.Code != http.StatusConflict {
			t.Fatalf("old node got %d (%s), want 409", w.Code, w.Body.String())
		}
		var refusal assistantwire.IdleArchiveRefusalResponse
		if err := json.Unmarshal(w.Body.Bytes(), &refusal); err != nil {
			t.Fatal(err)
		}
		if refusal.Code != idleArchiveCodeCloseRefused || refusal.CloseOutcome != archiveCloseRefused || !refusal.AuditPersisted || refusal.AuditID == "" {
			t.Fatalf("unexpected refusal: %+v", refusal)
		}
		if unguardedCalled || len(retiredOf(ft)) != 0 {
			t.Fatalf("old node silently degraded to unguarded close: unguardedCalled=%v retired=%v", unguardedCalled, retiredOf(ft))
		}
		if archivedIDs(t, ft.s)[row.SessionID] {
			t.Fatal("provisional archive was not rolled back after old-node refusal")
		}
		if rec.count() != 1 {
			t.Fatalf("plugin_audit rows = %d, want 1", rec.count())
		}
	})

	t.Run("node omitting guard_verified confirmation fails closed", func(t *testing.T) {
		ft, rec, row := idleArchiveTestSetup(t, now, nil)
		ft.f.answerNode(deviceSessionRetireGuarded, func(_ string, _ json.RawMessage) (any, error) {
			return map[string]any{"closed": true}, nil
		})

		w := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
		if w.Code != http.StatusConflict {
			t.Fatalf("unconfirmed node got %d (%s), want 409", w.Code, w.Body.String())
		}
		var refusal assistantwire.IdleArchiveRefusalResponse
		if err := json.Unmarshal(w.Body.Bytes(), &refusal); err != nil {
			t.Fatal(err)
		}
		if refusal.Code != idleArchiveCodeCloseRefused || refusal.CloseOutcome != archiveCloseRefused || !refusal.AuditPersisted {
			t.Fatalf("unexpected refusal: %+v", refusal)
		}
		if archivedIDs(t, ft.s)[row.SessionID] {
			t.Fatal("provisional archive was not rolled back on missing guard_verified")
		}
		if rec.count() != 1 {
			t.Fatalf("plugin_audit rows = %d, want 1", rec.count())
		}
	})

	t.Run("guarded close never degrades to unguarded session.close for capabilities.close", func(t *testing.T) {
		closeAllowed := true
		ft, rec, row := idleArchiveTestSetup(t, now, func(s *proto.Session) {
			s.Capabilities = &proto.SessionCapabilities{Close: &closeAllowed}
		})
		sessionCloseCalled := false
		ft.f.answerNode("session.close", func(_ string, _ json.RawMessage) (any, error) {
			sessionCloseCalled = true
			return map[string]any{"ok": true}, nil
		})

		w := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10}`)
		if w.Code != http.StatusConflict {
			t.Fatalf("runtime close capability got %d (%s), want 409", w.Code, w.Body.String())
		}
		var refusal assistantwire.IdleArchiveRefusalResponse
		if err := json.Unmarshal(w.Body.Bytes(), &refusal); err != nil {
			t.Fatal(err)
		}
		if refusal.Code != idleArchiveCodeCloseRefused || refusal.CloseOutcome != archiveCloseRefused || !refusal.AuditPersisted {
			t.Fatalf("unexpected refusal: %+v", refusal)
		}
		if sessionCloseCalled {
			t.Fatal("guarded idle archive degraded to unguarded session.close")
		}
		if archivedIDs(t, ft.s)[row.SessionID] {
			t.Fatal("provisional archive was not rolled back")
		}
		if rec.count() != 1 {
			t.Fatalf("plugin_audit rows = %d, want 1", rec.count())
		}
	})
}

func TestIdleArchiveAuditFinalizationRecoversAcrossServiceRestart(t *testing.T) {
	now := time.Date(2026, 11, 26, 0, 30, 0, 0, time.UTC)

	startRestartedService := func(t *testing.T, st Store) (*Service, context.CancelFunc, <-chan struct{}) {
		t.Helper()
		f := newFakeHub()
		d := newFleetDevs()
		d.add("mac", "Mac")
		svc := New(Options{Hub: f, Store: func() Store { return st }, Logf: testLogf(t), Devices: d})
		runCtx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			svc.Run(runCtx)
			close(done)
		}()
		t.Cleanup(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Errorf("restarted service did not stop cleanly")
			}
		})
		return svc, cancel, done
	}

	t.Run("persisted pending outbox finalizes audit and plugin_audit after manager restart", func(t *testing.T) {
		ft, rec, row := idleArchiveTestSetup(t, now, nil)
		rec.mu.Lock()
		rec.failAuditWriteOnCall = 2 // 1st write persists close_attempted; 2nd write (finalization) fails
		rec.mu.Unlock()

		w := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10,"reason":"restart-outbox"}`)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("got %d (%s), want 503", w.Code, w.Body.String())
		}
		var resp assistantwire.IdleArchiveResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if resp.OK || resp.Code != idleArchiveCodeAuditFinalizationPending || resp.AuditID == "" || !resp.AuditFinalizationPending {
			t.Fatalf("unexpected 503 response: %+v", resp)
		}
		// Simulate manager crash/restart before background retry completes.
		ft.s.stopIdleArchiveAuditRetry()

		var beforeEntries []assistantwire.IdleArchiveAuditEntry
		if _, err := rec.Store.Setting(context.Background(), idleArchiveAuditKey, &beforeEntries); err != nil {
			t.Fatal(err)
		}
		if len(beforeEntries) != 1 || beforeEntries[0].Outcome != idleArchiveOutcomeCloseAttempted {
			t.Fatalf("expected persisted close_attempted before restart, got %+v", beforeEntries)
		}
		var beforePending []idleArchivePendingAudit
		if _, err := rec.Store.Setting(context.Background(), idleArchiveAuditPendingKey, &beforePending); err != nil {
			t.Fatal(err)
		}
		if len(beforePending) != 1 || beforePending[0].Entry.ID != resp.AuditID || beforePending[0].Entry.Outcome != idleArchiveOutcomeArchivedAndClosed {
			t.Fatalf("expected durable pending outbox before restart, got %+v", beforePending)
		}
		if rec.count() != 0 {
			t.Fatalf("plugin_audit count before restart = %d, want 0", rec.count())
		}

		// Storage recovers and a fresh Service instance starts against the same persistent Store.
		mirroredCh := make(chan struct{}, 2)
		rec.mu.Lock()
		rec.failAuditWriteOnCall = 0
		rec.onPluginAudit = func() {
			select {
			case mirroredCh <- struct{}{}:
			default:
			}
		}
		rec.mu.Unlock()

		svc2, _, _ := startRestartedService(t, rec)
		select {
		case <-mirroredCh:
		case <-time.After(5 * time.Second):
			t.Fatal("restarted service did not finalize and mirror pending audit")
		}

		aw := getIdleArchiveAudit(svc2, "?session_id="+row.SessionID, roleExtension, "guardrail-ext")
		if aw.Code != http.StatusOK {
			t.Fatalf("restarted audit query got %d: %s", aw.Code, aw.Body.String())
		}
		var auditResp assistantwire.IdleArchiveAuditResponse
		if err := json.Unmarshal(aw.Body.Bytes(), &auditResp); err != nil {
			t.Fatal(err)
		}
		if len(auditResp.Entries) != 1 || auditResp.Entries[0].ID != resp.AuditID ||
			auditResp.Entries[0].Outcome != idleArchiveOutcomeArchivedAndClosed ||
			auditResp.Entries[0].CloseOutcome != archiveCloseClosed ||
			auditResp.Entries[0].Reason != "restart-outbox" {
			t.Fatalf("restarted finalized entries = %+v", auditResp.Entries)
		}
		if rec.count() != 1 {
			t.Fatalf("plugin_audit rows after restart = %d, want 1", rec.count())
		}
		rec.mu.Lock()
		var detail idleArchivePluginAuditDetail
		if err := json.Unmarshal(rec.rows[0].detail, &detail); err != nil {
			rec.mu.Unlock()
			t.Fatal(err)
		}
		rec.mu.Unlock()
		if detail.AuditID != resp.AuditID || detail.Outcome != idleArchiveOutcomeArchivedAndClosed || detail.CloseOutcome != archiveCloseClosed {
			t.Fatalf("mirrored plugin_audit detail = %+v", detail)
		}
		var afterPending []idleArchivePendingAudit
		if _, err := rec.Store.Setting(context.Background(), idleArchiveAuditPendingKey, &afterPending); err != nil {
			t.Fatal(err)
		}
		if len(afterPending) != 0 {
			t.Fatalf("pending outbox was not cleared after restart recovery: %+v", afterPending)
		}
	})

	t.Run("orphaned close_attempted reconciles after total DB outage and manager restart", func(t *testing.T) {
		ft, rec, row := idleArchiveTestSetup(t, now, nil)
		// Allow provisional archive + close_attempted write, then simulate total DB outage during close/finalization
		// so neither idleArchiveAuditKey nor idleArchiveAuditPendingKey can be written before manager stops.
		ft.f.answerNode(deviceSessionRetireGuarded, func(_ string, _ json.RawMessage) (any, error) {
			rec.mu.Lock()
			rec.failAllWrites = true
			rec.mu.Unlock()
			return map[string]any{"closed": true, "guard_verified": true}, nil
		})

		w := postIdleArchive(ft.s, "mac", row.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10,"reason":"restart-orphan"}`)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("got %d (%s), want 503", w.Code, w.Body.String())
		}
		var resp assistantwire.IdleArchiveResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		ft.s.stopIdleArchiveAuditRetry()

		var beforePending []idleArchivePendingAudit
		if _, err := rec.Store.Setting(context.Background(), idleArchiveAuditPendingKey, &beforePending); err != nil {
			t.Fatal(err)
		}
		if len(beforePending) != 0 {
			t.Fatalf("expected empty pending outbox under total DB outage, got %+v", beforePending)
		}
		if rec.count() != 0 {
			t.Fatalf("plugin_audit count before restart = %d, want 0", rec.count())
		}

		// DB recovers and a brand-new Service restarts; it must reconcile close_attempted from sessions.archived.
		mirroredCh := make(chan struct{}, 2)
		rec.mu.Lock()
		rec.failAllWrites = false
		rec.onPluginAudit = func() {
			select {
			case mirroredCh <- struct{}{}:
			default:
			}
		}
		rec.mu.Unlock()

		svc2, _, _ := startRestartedService(t, rec)
		select {
		case <-mirroredCh:
		case <-time.After(5 * time.Second):
			t.Fatal("restarted service did not reconcile orphaned close_attempted")
		}

		var afterEntries []assistantwire.IdleArchiveAuditEntry
		if _, err := rec.Store.Setting(context.Background(), idleArchiveAuditKey, &afterEntries); err != nil {
			t.Fatal(err)
		}
		if len(afterEntries) != 1 || afterEntries[0].ID != resp.AuditID ||
			afterEntries[0].Outcome != idleArchiveOutcomeArchivedAndClosed ||
			afterEntries[0].CloseOutcome != archiveCloseClosed ||
			afterEntries[0].Reason != "restart-orphan" {
			t.Fatalf("reconciled audit entries = %+v", afterEntries)
		}
		if rec.count() != 1 {
			t.Fatalf("plugin_audit rows after orphan reconciliation = %d, want 1", rec.count())
		}
		_ = svc2
	})

	t.Run("orphaned close_attempted after rolled-back refusal reconciles to refused on restart", func(t *testing.T) {
		ft2, rec2, row2 := idleArchiveTestSetup(t, now, nil)
		ft2.f.answerNode(deviceSessionRetireGuarded, func(_ string, _ json.RawMessage) (any, error) {
			return nil, &rpc.Error{Code: "not_retirable", Message: "the agent is working"}
		})
		archiveWrites := 0
		rec2.mu.Lock()
		rec2.onPutSetting = func(key string) {
			if key == agentArchivesKey {
				rec2.mu.Lock()
				archiveWrites++
				n := archiveWrites
				rec2.mu.Unlock()
				if n == 2 {
					// Rollback is running; fail audit + pending outbox writes right after rollback.
					rec2.mu.Lock()
					rec2.failAuditWrite = true
					rec2.mu.Unlock()
				}
			} else if key == idleArchiveAuditPendingKey {
				rec2.mu.Lock()
				rec2.failAllWrites = true
				rec2.mu.Unlock()
			}
		}
		rec2.mu.Unlock()

		w := postIdleArchive(ft2.s, "mac", row2.SessionID, roleExtension, "guardrail-ext", `{"idle_minutes":10,"reason":"restart-refused"}`)
		if w.Code != http.StatusConflict {
			t.Fatalf("got %d (%s), want 409", w.Code, w.Body.String())
		}
		ft2.s.stopIdleArchiveAuditRetry()

		if archivedIDs(t, ft2.s)[row2.SessionID] {
			t.Fatal("provisional archive should have been rolled back")
		}
		if rec2.count() != 0 {
			t.Fatalf("plugin_audit count before restart = %d, want 0", rec2.count())
		}

		mirroredCh := make(chan struct{}, 2)
		rec2.mu.Lock()
		rec2.failAuditWrite = false
		rec2.failAllWrites = false
		rec2.onPutSetting = nil
		rec2.onPluginAudit = func() {
			select {
			case mirroredCh <- struct{}{}:
			default:
			}
		}
		rec2.mu.Unlock()

		startRestartedService(t, rec2)
		select {
		case <-mirroredCh:
		case <-time.After(5 * time.Second):
			t.Fatal("restarted service did not reconcile rolled-back close_attempted")
		}

		var afterEntries []assistantwire.IdleArchiveAuditEntry
		if _, err := rec2.Store.Setting(context.Background(), idleArchiveAuditKey, &afterEntries); err != nil {
			t.Fatal(err)
		}
		if len(afterEntries) != 1 || afterEntries[0].Outcome != idleArchiveOutcomeRefused ||
			afterEntries[0].RefusalCode != idleArchiveCodeCloseFailed {
			t.Fatalf("reconciled refused entries = %+v", afterEntries)
		}
		if rec2.count() != 1 {
			t.Fatalf("plugin_audit rows after refused orphan reconciliation = %d, want 1", rec2.count())
		}
	})
}
