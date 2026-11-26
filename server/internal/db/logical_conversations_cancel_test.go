package db

import (
	"errors"
	"sync"
	"testing"
)

func TestLogicalConversationIgnoresPreparationFailuresAfterMapping(t *testing.T) {
	d, _ := historyDB(t)
	op := prepareLogicalSwitch(t, d)
	ctx := t.Context()
	rejectLatePreparation := func() {
		t.Helper()
		for _, outcome := range []string{"in_doubt", "rejected"} {
			err := d.ResolveConversationPreparation(ctx, "root-machine", "root-chat", op.OperationID, outcome, "late node response")
			if !errors.Is(err, ErrConversationEpoch) {
				t.Fatalf("late preparation outcome %s: %v", outcome, err)
			}
		}
	}
	rejectLatePreparation()
	if err := d.ActivateConversationSegment(ctx, "root-machine", "root-chat", op.OperationID, 1); err != nil {
		t.Fatal(err)
	}
	rejectLatePreparation()
	c, err := d.LogicalConversation(ctx, "root-machine", "root-chat")
	if err != nil || c == nil || c.Epoch != 2 || c.Switch == nil || c.Switch.Phase != "active" {
		t.Fatalf("late preparation changed the active switch: %+v, %v", c, err)
	}
}

// A switch that never activated its target can be cancelled: its source stays active at the same epoch, the
// prepared target is abandoned for closure, the outcome keeps its reason, and the conversation can switch again
// (#15317).
func TestLogicalConversationCancelBeforeActivationKeepsTheSource(t *testing.T) {
	d, _ := historyDB(t)
	op := prepareLogicalSwitch(t, d)
	ctx := t.Context()
	if err := d.NoteConversationSwitchError(ctx, "root-machine", "root-chat", op.OperationID, "fake node offline"); err != nil {
		t.Fatal(err)
	}
	if got, err := d.ConversationSwitch(ctx, "root-machine", "root-chat", op.OperationID); err != nil || got.Error != "fake node offline" || got.CreatedAt.IsZero() {
		t.Fatalf("latest error and start time: %+v %v", got, err)
	}
	if cancelled, err := d.CancelConversationSwitch(ctx, "root-machine", "root-chat", op.OperationID, 2, "wrong epoch"); err != nil || cancelled {
		t.Fatalf("a cancel for another epoch: cancelled=%v err=%v", cancelled, err)
	}
	cancelled, err := d.CancelConversationSwitch(ctx, "root-machine", "root-chat", op.OperationID, 1, "fake expiry; last error: fake node offline")
	if err != nil || !cancelled {
		t.Fatalf("cancel: cancelled=%v err=%v", cancelled, err)
	}
	got, err := d.ConversationSwitch(ctx, "root-machine", "root-chat", op.OperationID)
	if err != nil || got.Phase != "rejected" || got.Error != "fake expiry; last error: fake node offline" {
		t.Fatalf("cancelled outcome: %+v %v", got, err)
	}
	c, err := d.LogicalConversation(ctx, "root-machine", "root-chat")
	if err != nil {
		t.Fatal(err)
	}
	a, ok := c.Active()
	if !ok || a.ID != "first" || a.FrozenEnd != nil || c.Epoch != 1 || c.Switch != nil || len(c.Segments) != 2 || c.Segments[1].State != "abandoned" || !c.Segments[1].ClosePending {
		t.Fatalf("cancel did not keep the source at its epoch: %+v", c)
	}
	if cancelled, err := d.CancelConversationSwitch(ctx, "root-machine", "root-chat", op.OperationID, 1, "again"); err != nil || cancelled {
		t.Fatalf("a settled switch cancelled again: cancelled=%v err=%v", cancelled, err)
	}
	if err := d.NoteConversationSwitchError(ctx, "root-machine", "root-chat", op.OperationID, "late error"); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.ConversationSwitch(ctx, "root-machine", "root-chat", op.OperationID); got.Error != "fake expiry; last error: fake node offline" {
		t.Fatalf("a late error replaced the outcome's reason: %+v", got)
	}
	next := op
	next.OperationID = "switch-two"
	if err := d.BeginConversationSwitch(ctx, "root-machine", "root-chat", next); err != nil {
		t.Fatalf("the conversation cannot switch again: %v", err)
	}
}

// An unconfirmed preparation (in_doubt before activation) is cancelled the same way.
func TestLogicalConversationCancelsAnUnconfirmedPreparation(t *testing.T) {
	d, _ := historyDB(t)
	ctx := t.Context()
	if err := d.AdoptConversation(ctx, "root-machine", "root-chat", "first", "claude", "source-one"); err != nil {
		t.Fatal(err)
	}
	op := ConversationSwitch{OperationID: "switch-one", ExpectedEpoch: 1, SourceSegment: "first", Request: []byte(`{"launcher":"fake-target"}`)}
	if err := d.BeginConversationSwitch(ctx, "root-machine", "root-chat", op); err != nil {
		t.Fatal(err)
	}
	if err := d.ResolveConversationSwitch(ctx, "root-machine", "root-chat", op.OperationID, "in_doubt", "preparation receipt pending"); err != nil {
		t.Fatal(err)
	}
	if cancelled, err := d.CancelConversationSwitch(ctx, "root-machine", "root-chat", op.OperationID, 1, "fake expiry"); err != nil || !cancelled {
		t.Fatalf("cancel: cancelled=%v err=%v", cancelled, err)
	}
	c, err := d.LogicalConversation(ctx, "root-machine", "root-chat")
	if a, ok := c.Active(); err != nil || !ok || a.ID != "first" || c.Epoch != 1 || c.Switch != nil || len(c.Segments) != 1 {
		t.Fatalf("unconfirmed preparation cancel: %+v %v", c, err)
	}
}

// A cancel never undoes an activation: an activated switch reports false and keeps its error, and a cancel racing
// the activation on another connection leaves exactly one of them done.
func TestLogicalConversationCancelNeverUndoesActivation(t *testing.T) {
	d, url := historyDB(t)
	op := prepareLogicalSwitch(t, d)
	other, err := Open(t.Context(), url, ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	start := make(chan struct{})
	var wg sync.WaitGroup
	var activateErr, cancelErr error
	var cancelled bool
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		activateErr = d.ActivateConversationSegment(t.Context(), "root-machine", "root-chat", op.OperationID, 1)
	}()
	go func() {
		defer wg.Done()
		<-start
		cancelled, cancelErr = other.CancelConversationSwitch(t.Context(), "root-machine", "root-chat", op.OperationID, 1, "fake expiry")
	}()
	close(start)
	wg.Wait()
	if cancelErr != nil && !errors.Is(cancelErr, ErrConversationSwitchBusy) {
		t.Fatal(cancelErr)
	}
	if activateErr != nil && !errors.Is(activateErr, ErrConversationEpoch) && !errors.Is(activateErr, ErrConversationSwitchBusy) {
		t.Fatal(activateErr)
	}
	if cancelled == (activateErr == nil) {
		t.Fatalf("activation and cancel both or neither took effect: cancelled=%v activate=%v cancelErr=%v", cancelled, activateErr, cancelErr)
	}
	c, err := d.LogicalConversation(t.Context(), "root-machine", "root-chat")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := c.Active()
	got, _ := d.ConversationSwitch(t.Context(), "root-machine", "root-chat", op.OperationID)
	if cancelled && (c.Epoch != 1 || a.ID != "first" || got.Phase != "rejected") || !cancelled && (c.Epoch != 2 || a.ID != "second" || got.Phase != "active") {
		t.Fatalf("inconsistent result: cancelled=%v conversation=%+v switch=%+v", cancelled, c, got)
	}
	if cancelled {
		return
	}
	if err := d.NoteConversationSwitchError(t.Context(), "root-machine", "root-chat", op.OperationID, "late error"); err != nil {
		t.Fatal(err)
	}
	if again, err := d.CancelConversationSwitch(t.Context(), "root-machine", "root-chat", op.OperationID, 1, "fake expiry"); err != nil || again {
		t.Fatalf("an activated switch was cancelled: %v %v", again, err)
	}
	if after, _ := d.ConversationSwitch(t.Context(), "root-machine", "root-chat", op.OperationID); after.Phase != "active" || after.Error != "" {
		t.Fatalf("an activated switch changed: %+v", after)
	}
}
