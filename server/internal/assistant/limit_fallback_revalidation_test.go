package assistant

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestLimitFallbackExpiredOnFirstIdlePass(t *testing.T) {
	r, sends := newLimitFallbackRig(t)
	ctx := context.Background()
	const id = "main-chat"
	limitRow(r, id, usageLimitTurn(time.Now().Add(2*time.Hour)))
	limitRow(r, id, func(row *proto.Session) {
		row.Background = []proto.BackgroundTask{{ID: "shell-1", Kind: "shell"}}
	})
	r.s.limitFallbackTurnSettled(ctx, "server", id)
	batches, _ := limitFallbackBatches(limitFallbackState(t, r.s))
	if len(batches) != 1 || batches[0].Items[0].Outcome != conversationSwitchQueued {
		t.Fatalf("expected a queued fallback: %+v", batches)
	}
	batch := batches[0]
	// Between passes the quota resets, then the background task finishes.
	reset := time.Now().Add(-time.Second)
	r.s.conversationSwitches.mu.Lock()
	state := limitFallbackState(t, r.s)
	state.Batches[0].Fallback.ResetsAt = reset
	state.Batches[0].Fallback.RestoreAt = reset.Add(time.Minute)
	err := r.s.saveConversationSwitches(ctx, state)
	limitRow(r, id, func(row *proto.Session) {
		row.Background = nil
		row.TurnErrorResetsAt = &reset
	})
	r.s.conversationSwitches.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	report := conversationSwitchTestStatus(t, r.s, batch.ID)
	item := conversationSwitchTestItemFor(t, report, id)
	if item.Outcome != conversationSwitchRefused || !strings.Contains(item.Reason, "usage limit reset") {
		t.Errorf("first idle pass after reset: got %s (%s), want expired refusal", item.Outcome, item.Reason)
	}
	op, err := r.d.ConversationSwitch(ctx, "server", id, item.OperationID)
	if err != nil || op != nil {
		t.Errorf("expired fallback began: op=%+v err=%v", op, err)
	}
	fb := limitFallbackState(t, r.s).Batches[0].Fallback
	if !fb.Done || fb.ContinueTries != 0 || len(sends.to(id)) != 0 {
		t.Errorf("expired fallback submitted a continuation or stayed open: %+v", fb)
	}
}

func TestLimitFallbackRestoreKeepsOwnerSwitchBackToBackup(t *testing.T) {
	r, sends := newLimitFallbackRig(t)
	ctx := context.Background()
	const id = "main-chat"
	limitRow(r, id, usageLimitTurn(time.Now().Add(2*time.Hour)))
	r.s.limitFallbackTurnSettled(ctx, "server", id)
	batches, _ := limitFallbackBatches(limitFallbackState(t, r.s))
	if len(batches) != 1 {
		t.Fatalf("expected a fallback: %+v", batches)
	}
	b := batches[0]
	conversationSwitchTestDrive(t, r.s, b.ID, map[string]string{id: conversationSwitchSwitched})
	// Use the owner's batch route, including a second switch with the backup's exact settings.
	for _, launcher := range []string{"third", "backup"} {
		body := fmt.Sprintf(`{"conversation_ids":["main-chat"],"launcher":%q,"permission_mode":"default"}`, launcher)
		code, raw := conversationSwitchTestCall(t, r.s, "POST", "/api/v1/assistant/conversation-switches", "", body)
		manual := conversationSwitchTestDecode(t, code, raw)
		conversationSwitchTestDrive(t, r.s, manual.BatchID, map[string]string{id: conversationSwitchSwitched})
	}
	owner, err := r.d.LogicalConversation(ctx, "server", id)
	if err != nil {
		t.Fatal(err)
	}
	active, ok := owner.Active()
	if !ok || owner.Epoch != b.Items[0].ExpectedEpoch+3 || !sameConversationExecution(conversationExecutionNow("server", proto.Session{}, owner), b.Items[0].Target) {
		t.Fatalf("expected a new epoch with the original backup settings: %+v", owner)
	}
	forceLimitRestoreDue(t, r.s, b.ID)
	conversationSwitchTestStatus(t, r.s, b.ID)
	// Run another pass in case a return was scheduled. An owner message should
	// go to their explicitly selected physical session with its own handoff.
	r.s.logicalSweep(ctx)
	conversationSwitchTestStatus(t, r.s, b.ID)
	const msgID = "owner-after-reset"
	if err := r.s.call(ctx, "server", DeviceSessionSend, map[string]any{"session_id": id, "text": "keep working here", "client_msg_id": msgID}, nil); err != nil {
		t.Fatal(err)
	}
	after, err := r.d.LogicalConversation(ctx, "server", id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Switch != nil || after.Epoch != owner.Epoch {
		t.Fatalf("automatic return superseded owner at epoch %d: current epoch=%d switch=%+v", owner.Epoch, after.Epoch, after.Switch)
	}
	waitFor(t, "owner input on their chosen backup", func() bool {
		r.s.logicalSweep(ctx)
		return len(sends.to(active.SessionID)) > 0
	})
	got := sends.to(active.SessionID)
	if len(got) != 1 || got[0]["client_msg_id"] != msgID {
		t.Fatalf("owner inputs on their chosen backup: %+v", got)
	}
}

func TestLimitFallbackExpiredReplayKeepsBegunOperation(t *testing.T) {
	r, sends := newLimitFallbackRig(t)
	ctx := context.Background()
	const id = "main-chat"
	limitRow(r, id, usageLimitTurn(time.Now().Add(2*time.Hour)))
	r.s.limitFallbackTurnSettled(ctx, "server", id)

	// Simulate a restart after the operation began and before its updated batch was saved.
	r.s.conversationSwitches.mu.Lock()
	state := limitFallbackState(t, r.s)
	batch := &state.Batches[0]
	item := &batch.Items[0]
	op, err := r.d.ConversationSwitch(ctx, "server", id, item.OperationID)
	if err == nil && op != nil {
		item.Outcome = conversationSwitchQueued
		batch.Fallback.ResetsAt = time.Now().Add(-time.Second)
		batch.Fallback.RestoreAt = time.Now().Add(time.Minute)
		err = r.s.saveConversationSwitches(ctx, state)
	}
	r.s.conversationSwitches.mu.Unlock()
	if err != nil || op == nil {
		t.Fatalf("expected a durable operation: %+v %v", op, err)
	}
	conversationSwitchTestDrive(t, r.s, batch.ID, map[string]string{id: conversationSwitchSwitched})
	if got := conversationSwitchTestPreparations(r); len(got) != 1 {
		t.Fatalf("backup preparations: %+v", got)
	}
	if got := sends.to("prepared-chat-1"); len(got) != 1 || got[0]["client_msg_id"] != batch.Fallback.ContinueID {
		t.Fatalf("backup continuation: %+v", got)
	}
}
