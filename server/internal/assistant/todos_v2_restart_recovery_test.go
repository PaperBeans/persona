package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

func setRestartRecoveryTodosV2(t *testing.T, s *Service, want, expected bool) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"todos_v2": want, "expected_todos_v2": expected})
	if err != nil {
		t.Fatal(err)
	}
	w, _ := permissionSettingsRequest(t, s, "PUT", "/api/v1/assistant", s.managerIdentity(), "full", string(body))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT todos_v2=%v: code=%d body=%s", want, w.Code, w.Body.String())
	}
}

func setupSupervisedRecoveryTask(t *testing.T, s *Service, f *fakeHub, title, sessionID, source string, doneWhen []string) Todo {
	t.Helper()
	ctx := context.Background()
	task, err := s.createTodo(ctx, title, "Verify restart recovery behavior.", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	s.superviseTodo(ctx, task.ID, "conv1", doneWhen, 0)
	if err := s.linkTodoAgentWithSource(ctx, task.ID, "mac", sessionID, title, "/tmp/work", "claude", "", source, ""); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	if f.sessions == nil {
		f.sessions = make(map[string][]proto.Session)
	}
	f.sessions["mac"] = append(f.sessions["mac"], proto.Session{
		MachineID: "mac",
		SessionID: sessionID,
		Title:     title,
		Cwd:       "/tmp/work",
		Agent:     "claude",
		Status:    proto.StatusIdle,
		UpdatedAt: time.Now().UTC(),
	})
	f.mu.Unlock()
	return todoNow(s, task.ID)
}

func TestTodosV2RestartRecoveryBypassWhenFlagOff(t *testing.T) {
	s, f := newQuietServiceOn(t, nil)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	ctx := context.Background()
	if s.settings(ctx).todosV2On() {
		t.Fatal("expected todos_v2 default to be false")
	}

	task := setupSupervisedRecoveryTask(t, s, f, "Flag-off supervised task", "s-off", "assistant", []string{"All checks pass."})

	if err := s.RecordSupervisedTaskActive(ctx, task.ID, "mac", "s-off", "boot-1"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.todoRecoveryStore().GetTodoRecoveryState(ctx, task.ID); err != nil || found {
		t.Fatalf("todos_v2=false must not persist recovery state: found=%t err=%v", found, err)
	}

	// Even if a persisted row exists (e.g. written while flag was on earlier), sweep is 100% bypassed when off.
	if err := s.todoRecoveryStore().RecordTodoActiveCheckpoint(ctx, db.TodoRecoveryState{
		TodoID:              task.ID,
		MachineID:           "mac",
		SessionID:           "s-off",
		LastActiveBootEpoch: "boot-1",
	}); err != nil {
		t.Fatal(err)
	}
	res, err := s.RunRestartRecoverySweep(ctx, "boot-2", []proto.Session{
		{MachineID: "mac", SessionID: "s-off", Status: proto.StatusIdle},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.RecoveredTodoIDs) != 0 || len(res.EscalatedTodoIDs) != 0 || len(sentToAgent(f)) != 0 {
		t.Fatalf("todos_v2=false must 100%% bypass restart recovery: res=%+v sent=%+v", res, sentToAgent(f))
	}
}

func TestTodosV2RestartRecoveryCrossBootEpochAndSameEpochNoop(t *testing.T) {
	todoStores(t, func(t *testing.T, e *todoEnv, database *db.DB) {
		ctx := context.Background()
		e.s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
		setRestartRecoveryTodosV2(t, e.s, true, false)

		task := setupSupervisedRecoveryTask(t, e.s, e.f, "Interrupted supervised task", "s-rec-1", "assistant", []string{"Tests pass."})
		workingTask := setupSupervisedRecoveryTask(t, e.s, e.f, "Still working task", "s-work-1", "assistant", []string{"Build succeeds."})

		if err := e.s.RecordSupervisedTaskActive(ctx, task.ID, "mac", "s-rec-1", "boot-1"); err != nil {
			t.Fatal(err)
		}
		if err := e.s.RecordSupervisedTaskActive(ctx, workingTask.ID, "mac", "s-work-1", "boot-1"); err != nil {
			t.Fatal(err)
		}

		// Simulate service restart when backed by Postgres by constructing a fresh Service on the same DB.
		svc := e.s
		if database != nil {
			svc = newServiceOn(t, e.f, database)
			svc.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
		}

		// Same boot epoch must not trigger recovery.
		sameRes, err := svc.RunRestartRecoverySweep(ctx, "boot-1", []proto.Session{
			{MachineID: "mac", SessionID: "s-rec-1", Status: proto.StatusIdle},
			{MachineID: "mac", SessionID: "s-work-1", Status: proto.StatusWorking},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(sameRes.RecoveredTodoIDs) != 0 || len(sentToAgent(e.f)) != 0 {
			t.Fatalf("same boot_epoch must not trigger recovery: res=%+v sent=%+v", sameRes, sentToAgent(e.f))
		}

		// New boot epoch recovers the idle interrupted task and skips the still-working task.
		res, err := svc.RunRestartRecoverySweep(ctx, "boot-2", []proto.Session{
			{MachineID: "mac", SessionID: "s-rec-1", Status: proto.StatusIdle},
			{MachineID: "mac", SessionID: "s-work-1", Status: proto.StatusWorking},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.RecoveredTodoIDs) != 1 || res.RecoveredTodoIDs[0] != task.ID {
			t.Fatalf("expected only %s recovered, got %+v", task.ID, res)
		}
		sent := sentToAgent(e.f)
		if len(sent) != 1 || !strings.Contains(sent[0], "attempt 1/2") {
			t.Fatalf("expected 1 resume prompt for attempt 1/2, got %+v", sent)
		}

		recState, found, err := svc.todoRecoveryStore().GetTodoRecoveryState(ctx, task.ID)
		if err != nil || !found {
			t.Fatalf("expected persisted recovery state, found=%t err=%v", found, err)
		}
		if recState.RecoveryAttempts != 1 || recState.LastActiveBootEpoch != "boot-2" || recState.LastRecoveredAt == nil {
			t.Fatalf("unexpected recovery state after attempt 1: %+v", recState)
		}

		// Repeating sweep in the same new boot_epoch must not double-send.
		repeatRes, err := svc.RunRestartRecoverySweep(ctx, "boot-2", []proto.Session{
			{MachineID: "mac", SessionID: "s-rec-1", Status: proto.StatusIdle},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(repeatRes.RecoveredTodoIDs) != 0 || len(sentToAgent(e.f)) != 1 {
			t.Fatalf("repeat sweep in same boot_epoch must not resend: res=%+v sent=%+v", repeatRes, sentToAgent(e.f))
		}
	})
}

func TestTodosV2RestartRecoveryCapEscalatesAfterTwoAttempts(t *testing.T) {
	s, f := newQuietServiceOn(t, nil)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	ctx := context.Background()
	setRestartRecoveryTodosV2(t, s, true, false)

	task := setupSupervisedRecoveryTask(t, s, f, "Flapping supervised task", "s-cap", "assistant", []string{"Delivery complete."})
	if err := s.RecordSupervisedTaskActive(ctx, task.ID, "mac", "s-cap", "boot-1"); err != nil {
		t.Fatal(err)
	}

	idle := []proto.Session{{MachineID: "mac", SessionID: "s-cap", Status: proto.StatusIdle}}

	// Attempt 1 (boot-1 -> boot-2).
	res1, err := s.RunRestartRecoverySweep(ctx, "boot-2", idle)
	if err != nil || len(res1.RecoveredTodoIDs) != 1 || len(res1.EscalatedTodoIDs) != 0 {
		t.Fatalf("attempt 1 unexpected: res=%+v err=%v", res1, err)
	}

	// Attempt 2 (boot-2 -> boot-3).
	res2, err := s.RunRestartRecoverySweep(ctx, "boot-3", idle)
	if err != nil || len(res2.RecoveredTodoIDs) != 1 || len(res2.EscalatedTodoIDs) != 0 {
		t.Fatalf("attempt 2 unexpected: res=%+v err=%v", res2, err)
	}
	if len(sentToAgent(f)) != 2 {
		t.Fatalf("expected 2 resume messages after 2 attempts, got %d", len(sentToAgent(f)))
	}

	// 3rd restart (boot-3 -> boot-4): cap reached -> escalate to supervision, never send a 3rd resume.
	res3, err := s.RunRestartRecoverySweep(ctx, "boot-4", idle)
	if err != nil {
		t.Fatal(err)
	}
	if len(res3.RecoveredTodoIDs) != 0 || len(res3.EscalatedTodoIDs) != 1 || res3.EscalatedTodoIDs[0] != task.ID {
		t.Fatalf("expected escalation at cap, got %+v", res3)
	}
	if len(sentToAgent(f)) != 2 {
		t.Fatalf("must not send a 3rd resume prompt after cap, got %d", len(sentToAgent(f)))
	}

	// Verify supervision signal was recorded.
	b := &s.supervise
	b.mu.Lock()
	g, ok := b.pending[(superviseSignal{Todo: task.ID, Kind: sigRecovered}).id()]
	gotSig := ok && strings.Contains(g.Detail, "2-attempt cap")
	b.mu.Unlock()
	if !gotSig {
		t.Fatalf("expected sigRecovered escalation signal for %s", task.ID)
	}

	// Repeating sweep in boot-4 must not re-escalate.
	res4, err := s.RunRestartRecoverySweep(ctx, "boot-4", idle)
	if err != nil || len(res4.EscalatedTodoIDs) != 0 {
		t.Fatalf("repeat sweep in boot-4 must not re-escalate: res=%+v err=%v", res4, err)
	}

	// Resetting recovery state when normal progress resumes restores attempts to 0.
	if err := s.ResetSupervisedTaskRecovery(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	st, found, err := s.todoRecoveryStore().GetTodoRecoveryState(ctx, task.ID)
	if err != nil || !found || st.RecoveryAttempts != 0 {
		t.Fatalf("expected reset attempts=0, got state=%+v found=%t err=%v", st, found, err)
	}
}

func TestTodosV2RestartRecoveryIgnoresManualTerminals(t *testing.T) {
	s, f := newQuietServiceOn(t, nil)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	ctx := context.Background()
	setRestartRecoveryTodosV2(t, s, true, false)

	task := setupSupervisedRecoveryTask(t, s, f, "Manual terminal task", "s-manual", "manual", []string{"Done."})

	// RecordSupervisedTaskActive must refuse to track manual sessions.
	if err := s.RecordSupervisedTaskActive(ctx, task.ID, "mac", "s-manual", "boot-1"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.todoRecoveryStore().GetTodoRecoveryState(ctx, task.ID); err != nil || found {
		t.Fatalf("manual terminal must never be recorded: found=%t err=%v", found, err)
	}

	// Even if a row is manually injected, sweep must still refuse to touch manual terminals.
	if err := s.todoRecoveryStore().RecordTodoActiveCheckpoint(ctx, db.TodoRecoveryState{
		TodoID:              task.ID,
		MachineID:           "mac",
		SessionID:           "s-manual",
		LastActiveBootEpoch: "boot-1",
	}); err != nil {
		t.Fatal(err)
	}
	res, err := s.RunRestartRecoverySweep(ctx, "boot-2", []proto.Session{
		{MachineID: "mac", SessionID: "s-manual", Status: proto.StatusIdle},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.RecoveredTodoIDs) != 0 || len(res.EscalatedTodoIDs) != 0 || len(sentToAgent(f)) != 0 {
		t.Fatalf("manual terminal must never be recovered or escalated: res=%+v sent=%+v", res, sentToAgent(f))
	}
}

func TestTodosV2RestartRecoveryIgnoresPendingOwnerCardsAndNeedsUser(t *testing.T) {
	s, f := newQuietServiceOn(t, nil)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	ctx := context.Background()
	setRestartRecoveryTodosV2(t, s, true, false)

	// 1. Task in reviewNeedsUser.
	needsUserTask := setupSupervisedRecoveryTask(t, s, f, "Needs user decision", "s-nu", "assistant", []string{"Owner chooses."})
	if err := s.RecordSupervisedTaskActive(ctx, needsUserTask.ID, "mac", "s-nu", "boot-1"); err != nil {
		t.Fatal(err)
	}
	s.updateSupervision(ctx, needsUserTask.ID, func(v *Supervision) bool {
		v.State = reviewNeedsUser
		return true
	})

	// 2. Task with an unanswered pending interaction card on its session.
	cardTask := setupSupervisedRecoveryTask(t, s, f, "Pending permission card", "s-card", "assistant", []string{"Approval granted."})
	if err := s.RecordSupervisedTaskActive(ctx, cardTask.ID, "mac", "s-card", "boot-1"); err != nil {
		t.Fatal(err)
	}
	s.fleet.mu.Lock()
	if s.fleet.interactions == nil {
		s.fleet.interactions = make(map[string]*pending)
	}
	s.fleet.interactions["ix-1"] = &pending{ix: proto.Interaction{
		ID:        "ix-1",
		MachineID: "mac",
		SessionID: "s-card",
		Kind:      proto.IxQuestion,
		ToolName:  "ask_user",
	}}
	s.fleet.mu.Unlock()

	res, err := s.RunRestartRecoverySweep(ctx, "boot-2", []proto.Session{
		{MachineID: "mac", SessionID: "s-nu", Status: proto.StatusIdle},
		{MachineID: "mac", SessionID: "s-card", Status: proto.StatusIdle},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.RecoveredTodoIDs) != 0 || len(res.EscalatedTodoIDs) != 0 || len(sentToAgent(f)) != 0 {
		t.Fatalf("tasks waiting on owner or pending card must be skipped: res=%+v sent=%+v", res, sentToAgent(f))
	}
}

func TestTodosV2RestartRecoveryIgnoresPersonalCompletedAndDoneConcluded(t *testing.T) {
	s, f := newQuietServiceOn(t, nil)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	ctx := context.Background()
	setRestartRecoveryTodosV2(t, s, true, false)

	// 1. Personal unsupervised to-do.
	personal, err := s.createTodo(ctx, "Buy milk", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordSupervisedTaskActive(ctx, personal.ID, "mac", "s-personal", "boot-1"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.todoRecoveryStore().GetTodoRecoveryState(ctx, personal.ID); err != nil || found {
		t.Fatalf("personal todo must not be recorded: found=%t err=%v", found, err)
	}

	// 2. Completed supervised task.
	completed := setupSupervisedRecoveryTask(t, s, f, "Completed task", "s-done", "assistant", []string{"Shipped."})
	if err := s.RecordSupervisedTaskActive(ctx, completed.ID, "mac", "s-done", "boot-1"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("PATCH", "/api/v1/todos/"+completed.ID, strings.NewReader(`{"done":true}`))
	req.Header.Set(RoleHeader, "full")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH done=true: code=%d body=%s", w.Code, w.Body.String())
	}

	// 3. Open supervised task whose completion check already judged verdictDone.
	concluded := setupSupervisedRecoveryTask(t, s, f, "Already concluded task", "s-concluded", "assistant", []string{"Verified."})
	if err := s.RecordSupervisedTaskActive(ctx, concluded.ID, "mac", "s-concluded", "boot-1"); err != nil {
		t.Fatal(err)
	}
	s.storeTaskCheck(ctx, concluded.ID, &TodoCheck{Verdict: verdictDone, Reason: "All criteria met.", At: time.Now().UTC()})

	res, err := s.RunRestartRecoverySweep(ctx, "boot-2", []proto.Session{
		{MachineID: "mac", SessionID: "s-personal", Status: proto.StatusIdle},
		{MachineID: "mac", SessionID: "s-done", Status: proto.StatusIdle},
		{MachineID: "mac", SessionID: "s-concluded", Status: proto.StatusIdle},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.RecoveredTodoIDs) != 0 || len(res.EscalatedTodoIDs) != 0 || len(sentToAgent(f)) != 0 {
		t.Fatalf("personal/completed/concluded tasks must be skipped: res=%+v sent=%+v", res, sentToAgent(f))
	}
}

func TestTodosV2RestartRecoveryLosslessRollbackWhenFlagDisabled(t *testing.T) {
	s, f := newQuietServiceOn(t, nil)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	ctx := context.Background()
	setRestartRecoveryTodosV2(t, s, true, false)

	task := setupSupervisedRecoveryTask(t, s, f, "Rollback task", "s-rb", "assistant", []string{"Checks green."})
	if err := s.RecordSupervisedTaskActive(ctx, task.ID, "mac", "s-rb", "boot-1"); err != nil {
		t.Fatal(err)
	}
	res1, err := s.RunRestartRecoverySweep(ctx, "boot-2", []proto.Session{
		{MachineID: "mac", SessionID: "s-rb", Status: proto.StatusIdle},
	})
	if err != nil || len(res1.RecoveredTodoIDs) != 1 {
		t.Fatalf("expected recovery while todos_v2=true: res=%+v err=%v", res1, err)
	}

	// Disable todos_v2 -> immediate lossless rollback, zero further recovery activity.
	setRestartRecoveryTodosV2(t, s, false, true)
	res2, err := s.RunRestartRecoverySweep(ctx, "boot-3", []proto.Session{
		{MachineID: "mac", SessionID: "s-rb", Status: proto.StatusIdle},
	})
	if err != nil || len(res2.RecoveredTodoIDs) != 0 || len(res2.EscalatedTodoIDs) != 0 {
		t.Fatalf("disabling todos_v2 must immediately bypass recovery: res=%+v err=%v", res2, err)
	}
	if len(sentToAgent(f)) != 1 {
		t.Fatalf("expected no additional messages after disabling todos_v2, got %d", len(sentToAgent(f)))
	}
}
