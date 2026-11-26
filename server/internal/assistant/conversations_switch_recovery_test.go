package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// These tests pin the way out of a batch switch that never activates its target (#15317): its retries are bounded
// from the switch's start, it ends refused with its real reason, and restore_batch_id can cancel it while its
// conversation is still on its saved settings.

// conversationSwitchTestSettle waits until no sweep goroutine is advancing a conversation, so a test can change
// logicalSwitchPrepareLimit without racing one.
func conversationSwitchTestSettle(t *testing.T, s *Service) {
	t.Helper()
	waitFor(t, "segment sweeps to finish", func() bool {
		s.logical.mu.Lock()
		defer s.logical.mu.Unlock()
		return len(s.logical.running) == 0
	})
}

func conversationSwitchTestLimit(t *testing.T, s *Service, limit time.Duration) {
	t.Helper()
	conversationSwitchTestSettle(t, s)
	old := logicalSwitchPrepareLimit
	logicalSwitchPrepareLimit = limit
	t.Cleanup(func() {
		conversationSwitchTestSettle(t, s)
		logicalSwitchPrepareLimit = old
	})
}

// conversationSwitchTestUnknownReceipt makes every preparation of the rig end without a receipt: the start is
// lost on the way back, and every later receipt check fails too.
func conversationSwitchTestUnknownReceipt(r *conversationSwitchTestRig) {
	r.f.answerNode("assistant.segment.prepare", func(string, json.RawMessage) (any, error) {
		return nil, errors.New("fake link reset before the reply")
	})
	r.f.answerNode("assistant.segment.receipt", func(string, json.RawMessage) (any, error) {
		return nil, errors.New("fake receipt unavailable")
	})
}

func conversationSwitchTestStart(t *testing.T, r *conversationSwitchTestRig) (conversationSwitchTestReport, conversationSwitchTestItem) {
	t.Helper()
	r.setTurn(proto.StatusIdle)
	code, raw := conversationSwitchTestCall(t, r.s, http.MethodPost, "/api/v1/assistant/conversation-switches", "", `{"conversation_ids":["fresh-chat"],"launcher":"fallback","model":"new-model","effort":"low"}`)
	started := conversationSwitchTestDecode(t, code, raw)
	item := conversationSwitchTestItemFor(t, started, "fresh-chat")
	if item.Outcome != "switching" || item.OperationID == "" {
		t.Fatalf("switch did not start: %+v", started)
	}
	return started, item
}

// conversationSwitchTestWaitSwitch sweeps until fresh-chat's operation reaches phase with an error containing want.
func conversationSwitchTestWaitSwitch(t *testing.T, r *conversationSwitchTestRig, item conversationSwitchTestItem, phase, want string) *db.ConversationSwitch {
	t.Helper()
	var op *db.ConversationSwitch
	waitFor(t, "operation "+phase+" with "+want, func() bool {
		r.s.logicalSweep(context.Background())
		var err error
		op, err = r.d.ConversationSwitch(context.Background(), item.Machine, item.ConversationID, item.OperationID)
		return err == nil && op != nil && op.Phase == phase && strings.Contains(op.Error, want)
	})
	return op
}

func conversationSwitchTestOnSource(t *testing.T, r *conversationSwitchTestRig, item conversationSwitchTestItem, segments int) {
	t.Helper()
	c, err := r.d.LogicalConversation(context.Background(), item.Machine, item.ConversationID)
	if err != nil || c == nil {
		t.Fatalf("logical conversation: %+v %v", c, err)
	}
	a, ok := c.Active()
	if !ok || c.Epoch != item.ExpectedEpoch || c.Switch != nil || a.MachineID != "server" || a.SessionID != "fresh-chat" || len(c.Segments) != segments {
		t.Fatalf("the conversation left its source: %+v", c)
	}
}

func TestConversationSwitchReceiptRetriesAfterDisconnect(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
	}{
		{"offline", apierr.ErrOffline},
		{"request_not_sent", rpc.NotSent(rpc.ErrClosed)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := newConversationSwitchTestRig(t)
			lostReply := errors.New("fake link reset after preparation")
			var receipt any // Both handlers access the receipt under the fake hub's lock.
			r.f.mu.Lock()
			prepare := r.f.nodes["assistant.segment.prepare"]
			r.f.mu.Unlock()
			r.f.answerNode("assistant.segment.prepare", func(machine string, raw json.RawMessage) (any, error) {
				var err error
				receipt, err = prepare(machine, raw)
				if err != nil {
					return nil, err
				}
				return nil, lostReply
			})
			r.f.answerNode("assistant.segment.receipt", func(string, json.RawMessage) (any, error) {
				return map[string]bool{"unknown": true}, nil
			})
			started, item := conversationSwitchTestStart(t, r)
			conversationSwitchTestWaitSwitch(t, r, item, "in_doubt", lostReply.Error())
			preparation := conversationSwitchTestPreparationFor(t, r, item)

			r.f.answerNode("assistant.segment.receipt", func(string, json.RawMessage) (any, error) { return nil, tt.err })
			var op *db.ConversationSwitch
			waitFor(t, "failed receipt check to be recorded", func() bool {
				r.s.logicalSweep(t.Context())
				var err error
				op, err = r.d.ConversationSwitch(t.Context(), item.Machine, item.ConversationID, item.OperationID)
				return err == nil && op != nil && (op.Phase == "rejected" || op.Error == tt.err.Error())
			})
			if op.Phase != "in_doubt" {
				t.Fatalf("receipt error %v left switch %q (%q), want in_doubt", tt.err, op.Phase, op.Error)
			}

			r.f.answerNode("assistant.segment.receipt", func(_ string, raw json.RawMessage) (any, error) {
				var params struct {
					OperationID string `json:"operation_id"`
				}
				if err := json.Unmarshal(raw, &params); err != nil {
					return nil, err
				}
				if params.OperationID != preparation["operation_id"] {
					return nil, errors.New("receipt queried a different preparation")
				}
				return receipt, nil
			})
			conversationSwitchTestDrive(t, r.s, started.BatchID, map[string]string{"fresh-chat": "switched"})
			conversationSwitchTestPreparationFor(t, r, item)
		})
	}
}

func TestConversationSwitchInitialPrepareOfflineIsRefused(t *testing.T) {
	r := newConversationSwitchTestRig(t)
	r.f.answerNode("assistant.segment.prepare", func(string, json.RawMessage) (any, error) {
		return nil, apierr.ErrOffline
	})
	started, item := conversationSwitchTestStart(t, r)
	conversationSwitchTestWaitSwitch(t, r, item, "rejected", "preparation refused")
	conversationSwitchTestDrive(t, r.s, started.BatchID, map[string]string{"fresh-chat": "refused"})
	conversationSwitchTestOnSource(t, r, item, 1)
}

// A preparation whose receipt stays unknown keeps retrying within the limit, keeping its latest error; past the limit,
// counted from the switch's start, it ends refused with that error and the conversation stays on its source.
func TestConversationSwitchUnknownReceiptEndsRefusedWithItsReason(t *testing.T) {
	r := newConversationSwitchTestRig(t)
	conversationSwitchTestUnknownReceipt(r)
	started, item := conversationSwitchTestStart(t, r)

	op := conversationSwitchTestWaitSwitch(t, r, item, "in_doubt", "fake receipt unavailable")
	created := op.CreatedAt
	for range 3 {
		r.s.logicalSweep(context.Background())
		conversationSwitchTestSettle(t, r.s)
	}
	if report := conversationSwitchTestStatus(t, r.s, started.BatchID); conversationSwitchTestItemFor(t, report, "fresh-chat").Outcome != "switching" {
		t.Fatalf("ended before its limit: %+v", report)
	}
	if op, _ = r.d.ConversationSwitch(context.Background(), item.Machine, item.ConversationID, item.OperationID); !op.CreatedAt.Equal(created) {
		t.Fatalf("retries moved the switch's start: %v then %v", created, op.CreatedAt)
	}

	conversationSwitchTestLimit(t, r.s, time.Nanosecond)
	report := conversationSwitchTestDrive(t, r.s, started.BatchID, map[string]string{"fresh-chat": "refused"})
	got := conversationSwitchTestItemFor(t, report, "fresh-chat")
	if !strings.Contains(got.Reason, "could not be confirmed") || !strings.Contains(got.Reason, "last error") || !strings.Contains(got.Reason, "fake receipt unavailable") {
		t.Fatalf("refusal does not name its real cause: %q", got.Reason)
	}
	conversationSwitchTestOnSource(t, r, item, 1)
	conversationSwitchTestNoSyntheticMessage(t, r)
}

// A prepared target that cannot take over (its source cannot be retired) ends the same way: the target is abandoned
// and closed, and the source is told it is not retired.
func TestConversationSwitchPreparedTargetEndsAndIsClosed(t *testing.T) {
	r := newConversationSwitchTestRig(t)
	r.f.answerNode("assistant.segment.retire", func(string, json.RawMessage) (any, error) {
		return nil, errors.New("fake retire failure")
	})
	started, item := conversationSwitchTestStart(t, r)
	conversationSwitchTestWaitSwitch(t, r, item, "prepared", "fake retire failure")

	conversationSwitchTestLimit(t, r.s, time.Nanosecond)
	report := conversationSwitchTestDrive(t, r.s, started.BatchID, map[string]string{"fresh-chat": "refused"})
	if reason := conversationSwitchTestItemFor(t, report, "fresh-chat").Reason; !strings.Contains(reason, "could not take over") || !strings.Contains(reason, "fake retire failure") {
		t.Fatalf("refusal does not name its real cause: %q", reason)
	}
	conversationSwitchTestOnSource(t, r, item, 2)
	waitFor(t, "the abandoned target to close", func() bool {
		r.s.logicalSweep(context.Background())
		for _, call := range conversationSwitchTestAllCalls(r) {
			if call.Method == "session.close" && strings.Contains(string(call.Params), "prepared-chat-1") {
				return true
			}
		}
		return false
	})
	restored := false
	for _, call := range conversationSwitchTestAllCalls(r) {
		restored = restored || call.Method == "assistant.segment.restore" && strings.Contains(string(call.Params), `"fresh-chat"`)
	}
	if !restored {
		t.Fatal("the source was not told it is not retired")
	}
}

// restore_batch_id cancels an item whose switch has not activated, back to its saved settings; the original item ends
// refused, and nothing is prepared again.
func TestConversationSwitchRestoreCancelsAnUnfinishedItem(t *testing.T) {
	r := newConversationSwitchTestRig(t)
	conversationSwitchTestUnknownReceipt(r)
	started, item := conversationSwitchTestStart(t, r)
	conversationSwitchTestWaitSwitch(t, r, item, "in_doubt", "fake receipt unavailable")
	conversationSwitchTestSettle(t, r.s)

	code, raw := conversationSwitchTestCall(t, r.s, http.MethodPost, "/api/v1/assistant/conversation-switches", "", `{"restore_batch_id":"`+started.BatchID+`","preview":true}`)
	preview := conversationSwitchTestItemFor(t, conversationSwitchTestDecode(t, code, raw), "fresh-chat")
	if preview.Outcome != "ready" || preview.Target != item.Before {
		t.Fatalf("restore preview of an unfinished item: %+v", preview)
	}
	if op, _ := r.d.ConversationSwitch(context.Background(), item.Machine, item.ConversationID, item.OperationID); op.Phase != "in_doubt" {
		t.Fatalf("preview changed the switch: %+v", op)
	}

	code, raw = conversationSwitchTestCall(t, r.s, http.MethodPost, "/api/v1/assistant/conversation-switches", "", `{"restore_batch_id":"`+started.BatchID+`"}`)
	restoring := conversationSwitchTestDecode(t, code, raw)
	got := conversationSwitchTestItemFor(t, restoring, "fresh-chat")
	if restoring.BatchID == "" || restoring.RestoreOf != started.BatchID || got.Outcome != "switched" || got.Target != item.Before || !strings.Contains(got.Reason, "cancelled") {
		t.Fatalf("restore of an unfinished item: %+v", restoring)
	}
	original := conversationSwitchTestItemFor(t, conversationSwitchTestStatus(t, r.s, started.BatchID), "fresh-chat")
	if original.Outcome != "refused" || !strings.Contains(original.Reason, "cancelled by restore batch "+restoring.BatchID) {
		t.Fatalf("original item after the cancel: %+v", original)
	}
	conversationSwitchTestOnSource(t, r, item, 1)
	if again := conversationSwitchTestItemFor(t, conversationSwitchTestStatus(t, r.s, restoring.BatchID), "fresh-chat"); again.Outcome != "switched" {
		t.Fatalf("restore batch moved on: %+v", again)
	}
	if n := len(conversationSwitchTestPreparations(r)); n != 0 {
		t.Fatalf("restore prepared a conversation: %d", n)
	}
}

// An item whose switch activates while the restore waits for confirmation is not cancelled: the re-check after
// confirmation refuses it, and the switch finishes.
func TestConversationSwitchRestoreLeavesASwitchThatActivated(t *testing.T) {
	r := newConversationSwitchTestRig(t)
	conversationSwitchTestUnknownReceipt(r)
	started, item := conversationSwitchTestStart(t, r)
	conversationSwitchTestWaitSwitch(t, r, item, "in_doubt", "fake receipt unavailable")
	conversationSwitchTestSettle(t, r.s)

	confirm := func(string) (bool, string, error) {
		// The receipt is found while the person is deciding, and the switch activates.
		r.f.answerNode("assistant.segment.receipt", func(_ string, raw json.RawMessage) (any, error) {
			model := "new-model"
			r.f.known["late-chat"] = true
			r.f.sessions["server"] = append(r.f.sessions["server"], proto.Session{SessionID: "late-chat", MachineID: "server", Assistant: true, Agent: "codex", Launcher: "fallback", Model: model, RequestedModel: &model, Effort: "low", Status: proto.StatusIdle, State: proto.StateIdle, Rev: 100})
			return map[string]any{"session_id": "late-chat", "prepared": true}, nil
		})
		waitFor(t, "the switch to activate", func() bool {
			r.s.logicalSweep(context.Background())
			c, err := r.d.LogicalConversation(context.Background(), item.Machine, item.ConversationID)
			return err == nil && c != nil && c.Epoch == item.ExpectedEpoch+1
		})
		return true, "", nil
	}
	a := conversationsSwitchArgs{RestoreBatchID: started.BatchID}
	report, outcome, err := r.s.executeConversationSwitches(context.Background(), a, "", r.s.conversationSwitchResolver("server"), confirm)
	if err != nil || outcome != "" {
		t.Fatalf("restore: %v %q", err, outcome)
	}
	if got := conversationSwitchTestItemFor(t, conversationSwitchTestReportFrom(t, report), "fresh-chat"); got.Outcome != "refused" {
		t.Fatalf("an activated switch was cancelled: %+v", report)
	}
	conversationSwitchTestSettle(t, r.s)
	conversationSwitchTestDrive(t, r.s, started.BatchID, map[string]string{"fresh-chat": "switched"})
	c, err := r.d.LogicalConversation(context.Background(), item.Machine, item.ConversationID)
	if a, ok := c.Active(); err != nil || !ok || a.SessionID != "late-chat" {
		t.Fatalf("the activated switch did not finish: %+v %v", c, err)
	}
}

// conversationSwitchTestHold sits between the service and the rig's fake hub. It can hold one node method until
// released, without holding the fake's lock, and it keeps fresh-chat's retirement mark the way the node does: a
// restore is refused as busy while a retire holds the conversation's lock.
type conversationSwitchTestHold struct {
	inner   Hub
	method  string
	entered chan struct{}
	release chan struct{}

	mu       sync.Mutex
	retiring bool
	retired  bool
}

func conversationSwitchTestHoldOn(r *conversationSwitchTestRig, method string) *conversationSwitchTestHold {
	h := &conversationSwitchTestHold{inner: r.s.hub, method: method, entered: make(chan struct{}), release: make(chan struct{})}
	r.s.hub = &firstToolsTimingHub{Hub: r.s.hub, beforeCall: h.call}
	return h
}

func (h *conversationSwitchTestHold) call(ctx context.Context, p DeviceCall) (json.RawMessage, error) {
	fresh := strings.Contains(string(p.Params), `"fresh-chat"`)
	switch {
	case p.Method == "assistant.segment.retire" && fresh:
		h.mu.Lock()
		h.retiring = true
		h.mu.Unlock()
		defer func() {
			h.mu.Lock()
			h.retiring, h.retired = false, true
			h.mu.Unlock()
		}()
	case p.Method == "assistant.segment.restore" && fresh:
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.retiring {
			return nil, errors.New("fake conversation busy")
		}
		h.retired = false
	}
	if p.Method == h.method {
		select {
		case <-h.entered:
		default:
			close(h.entered)
			<-h.release
		}
	}
	return h.inner.DeviceCall(ctx, p)
}

func (h *conversationSwitchTestHold) retirement() (retiring, retired bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.retiring, h.retired
}

// A restore that cancels a prepared switch while that switch's retire of its source is still on the node: the
// cancel's own restore meets the node busy, the retire then lands, and the source must still end up not retired.
func TestConversationSwitchCancelDuringRetireLeavesTheSourceUsable(t *testing.T) {
	r := newConversationSwitchTestRig(t)
	hold := conversationSwitchTestHoldOn(r, "assistant.segment.retire")
	started, item := conversationSwitchTestStart(t, r)
	go r.s.logicalSweep(context.Background())
	select {
	case <-hold.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the switch never retired its source")
	}

	code, raw := conversationSwitchTestCall(t, r.s, http.MethodPost, "/api/v1/assistant/conversation-switches", "", `{"restore_batch_id":"`+started.BatchID+`"}`)
	restoring := conversationSwitchTestDecode(t, code, raw)
	if got := conversationSwitchTestItemFor(t, restoring, "fresh-chat"); got.Outcome != "switched" || !strings.Contains(got.Reason, "cancelled") {
		t.Fatalf("restore during the retire: %+v", restoring)
	}
	close(hold.release)
	waitFor(t, "the source to be usable after the late retire", func() bool {
		retiring, retired := hold.retirement()
		return !retiring && !retired
	})
	conversationSwitchTestSettle(t, r.s)
	if _, retired := hold.retirement(); retired {
		t.Fatal("the late retire left the source retired")
	}
	conversationSwitchTestOnSource(t, r, item, 2)
}

// The restore batch with its cancel is saved before the cancel runs, so a crash on either side of the cancel ends
// the same way after a restart: the cancel is done once, the restore item is switched and the original refused.
func TestConversationSwitchRestoreCancelSurvivesACrash(t *testing.T) {
	r := newConversationSwitchTestRig(t)
	conversationSwitchTestUnknownReceipt(r)
	started, item := conversationSwitchTestStart(t, r)
	conversationSwitchTestWaitSwitch(t, r, item, "in_doubt", "fake receipt unavailable")
	conversationSwitchTestSettle(t, r.s)

	// A concurrent action holds the epoch lock, so the saved cancel cannot run yet.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	lock, err := pgx.Connect(ctx, os.Getenv("PERSONA_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close(context.Background())
	tx, err := lock.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, `SELECT epoch FROM logical_conversations WHERE machine_id=$1 AND session_id=$2 FOR SHARE`, item.Machine, item.ConversationID); err != nil {
		t.Fatal(err)
	}
	code, raw := conversationSwitchTestCall(t, r.s, http.MethodPost, "/api/v1/assistant/conversation-switches", "", `{"restore_batch_id":"`+started.BatchID+`"}`)
	restoring := conversationSwitchTestDecode(t, code, raw)
	if got := conversationSwitchTestItemFor(t, restoring, "fresh-chat"); restoring.BatchID == "" || got.Outcome != "queued" || got.OperationID != item.OperationID {
		t.Fatalf("the cancel was not saved before it ran: %+v", restoring)
	}
	if op, _ := r.d.ConversationSwitch(context.Background(), item.Machine, item.ConversationID, item.OperationID); op.Phase != "in_doubt" {
		t.Fatalf("cancelled under the lock: %+v", op)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	restarted := withSettings(t, r.f, New(Options{Hub: r.f, Store: func() Store { return r.d }, Logf: testLogf(t)}))
	conversationSwitchTestDrive(t, restarted, restoring.BatchID, map[string]string{"fresh-chat": "switched"})
	reason := "cancelled by restore batch " + restoring.BatchID
	if op, _ := r.d.ConversationSwitch(context.Background(), item.Machine, item.ConversationID, item.OperationID); op.Phase != "rejected" || !strings.Contains(op.Error, reason) {
		t.Fatalf("restart did not finish the saved cancel: %+v", op)
	}
	conversationSwitchTestDrive(t, restarted, started.BatchID, map[string]string{"fresh-chat": "refused"})

	// After the cancel: the save of its outcome is lost. The next run finds the cancel done by this batch.
	var state conversationSwitchState
	if _, err := r.d.Setting(context.Background(), conversationSwitchesKey, &state); err != nil {
		t.Fatal(err)
	}
	for bi := range state.Batches {
		if state.Batches[bi].ID == restoring.BatchID {
			state.Batches[bi].Items[0].Outcome, state.Batches[bi].Items[0].Reason = conversationSwitchQueued, ""
		}
	}
	if err := r.d.PutSetting(context.Background(), conversationSwitchesKey, state); err != nil {
		t.Fatal(err)
	}
	again := withSettings(t, r.f, New(Options{Hub: r.f, Store: func() Store { return r.d }, Logf: testLogf(t)}))
	got := conversationSwitchTestItemFor(t, conversationSwitchTestDrive(t, again, restoring.BatchID, map[string]string{"fresh-chat": "switched"}), "fresh-chat")
	if !strings.Contains(got.Reason, "cancelled") {
		t.Fatalf("a cancel done before the crash was not recognised: %+v", got)
	}
	conversationSwitchTestOnSource(t, r, item, 1)
	if n := len(conversationSwitchTestPreparations(r)); n != 0 {
		t.Fatalf("restore prepared a conversation: %d", n)
	}
}

func conversationSwitchTestReportFrom(t *testing.T, report conversationSwitchReport) conversationSwitchTestReport {
	t.Helper()
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return conversationSwitchTestDecode(t, http.StatusOK, raw)
}
