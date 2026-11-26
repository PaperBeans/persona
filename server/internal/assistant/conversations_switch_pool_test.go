package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestConversationSwitchBatchLeavesPoolAvailable(t *testing.T) {
	const batchSize, poolSize, parallelism = 13, 8, 4
	release := make(chan struct{})
	var waiting, peak atomic.Int32
	r := newConversationSwitchTestRigWithHub(t, func(inner Hub) Hub {
		return &firstToolsTimingHub{Hub: inner, beforeCall: func(ctx context.Context, call DeviceCall) (json.RawMessage, error) {
			if call.Method == "assistant.segment.prepare" {
				n := waiting.Add(1)
				defer waiting.Add(-1)
				for prev := peak.Load(); prev < n && !peak.CompareAndSwap(prev, n); prev = peak.Load() {
				}
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-release:
				}
			}
			// The rig's prepare handler reads the batch through the same pool, like a node callback to the hub.
			return inner.DeviceCall(ctx, call)
		}}
	})
	var chats []string
	for i := range batchSize {
		chats = append(chats, fmt.Sprintf("idle-chat-%02d", i))
	}
	r.f.mu.Lock()
	for _, chat := range chats {
		r.f.known[chat] = true
		r.f.sessions["server"] = append(r.f.sessions["server"], proto.Session{
			MachineID: "server", SessionID: chat, Assistant: true, Agent: "claude",
			Launcher: "sub", Status: proto.StatusIdle, State: proto.StateIdle, Rev: 100,
		})
	}
	r.f.mu.Unlock()
	body, err := json.Marshal(map[string]any{"conversation_ids": chats, "launcher": "fallback", "model": "new-model"})
	if err != nil {
		t.Fatal(err)
	}
	code, raw := conversationSwitchTestCall(t, r.s, http.MethodPost, "/api/v1/assistant/conversation-switches", "", string(body))
	batch := conversationSwitchTestDecode(t, code, raw)
	if len(batch.Items) != batchSize {
		t.Fatalf("expected %d items, got %+v", batchSize, batch)
	}
	outcomes := make(map[string]string, batchSize)
	for _, item := range batch.Items {
		if item.Outcome != "switching" {
			t.Fatalf("idle chat did not start switching: %+v", item)
		}
		outcomes[item.ConversationID] = "switched"
	}

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	// Observe pool saturation independently so the diagnostic still works when the application's pool is out of slots.
	observer, err := pgx.Connect(ctx, os.Getenv("PERSONA_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close(context.Background())
	swept := make(chan struct{})
	go func() {
		defer close(swept)
		r.s.logicalSweep(ctx)
	}()
	defer func() {
		cancel()
		<-swept
		conversationSwitchTestSettle(t, r.s)
	}()
	var held int
	waitFor(t, "a wave of blocked preparations or pool saturation", func() bool {
		if err := observer.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE pid=ANY($1) AND state='idle in transaction'`, r.d.BackendPIDs()).Scan(&held); err != nil {
			t.Fatal(err)
		}
		return held == poolSize || waiting.Load() >= parallelism && held == 0
	})
	probe, stopProbe := context.WithTimeout(ctx, time.Second)
	defer stopProbe()
	var saved json.RawMessage
	if found, err := r.d.Setting(probe, "assistant.conversation_switches", &saved); err != nil || !found {
		t.Fatalf("settings read during a %d-item switch with MaxConns=%d and %d idle transactions: found=%t, err=%v", batchSize, poolSize, held, found, err)
	}
	if held != 0 {
		t.Fatalf("%d transactions held while node preparation was paused", held)
	}
	close(release)
	<-swept
	conversationSwitchTestDrive(t, r.s, batch.BatchID, outcomes)
	if got := peak.Load(); got != parallelism {
		t.Fatalf("peak concurrent preparations = %d, want %d", got, parallelism)
	}
	if got := len(conversationSwitchTestPreparations(r)); got != batchSize {
		t.Fatalf("started %d preparations for %d switches", got, batchSize)
	}
}

func TestConversationSwitchCancelWhileWaitingForPreparation(t *testing.T) {
	for _, method := range []string{"assistant.segment.prepare", "assistant.segment.receipt"} {
		t.Run(method, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			r := newConversationSwitchTestRigWithHub(t, func(inner Hub) Hub {
				return &firstToolsTimingHub{Hub: inner, beforeCall: func(ctx context.Context, call DeviceCall) (json.RawMessage, error) {
					if call.Method != method {
						return inner.DeviceCall(ctx, call)
					}
					close(entered)
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-release:
						return json.RawMessage(`{"session_id":"late-candidate","prepared":true}`), nil
					}
				}}
			})
			_, item := conversationSwitchTestStart(t, r)
			if method == "assistant.segment.receipt" {
				if err := r.d.PutConversationHandoff(ctx, item.Machine, item.ConversationID, item.OperationID, json.RawMessage(`{"summary":"saved handoff"}`)); err != nil {
					t.Fatal(err)
				}
				if won, err := r.d.ClaimConversationPreparation(ctx, item.Machine, item.ConversationID, item.OperationID); err != nil || !won {
					t.Fatalf("preparation claim: won=%t, err=%v", won, err)
				}
			}
			r.s.logicalSweep(ctx)
			defer func() {
				cancel()
				conversationSwitchTestSettle(t, r.s)
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatalf("never reached %s: %v", method, ctx.Err())
			}
			cancelled, err := r.d.CancelConversationSwitch(ctx, item.Machine, item.ConversationID, item.OperationID, item.ExpectedEpoch, "cancel while preparing")
			if err != nil || !cancelled {
				t.Fatalf("cancel while the node waited: cancelled=%t, err=%v", cancelled, err)
			}
			close(release)
			conversationSwitchTestSettle(t, r.s)
			conversationSwitchTestOnSource(t, r, item, 1)
			candidates, err := r.d.ConversationPreparedCandidates(ctx)
			if err != nil || len(candidates) != 1 || candidates[0].SessionID != "late-candidate" || candidates[0].OperationID != item.OperationID {
				t.Fatalf("late preparation was not retained for cleanup: %+v, %v", candidates, err)
			}
			r.s.logicalSweep(ctx)
			candidates, err = r.d.ConversationPreparedCandidates(ctx)
			if err != nil || len(candidates) != 0 {
				t.Fatalf("abandoned candidate was not cleaned up: %+v, %v", candidates, err)
			}
			for _, call := range conversationSwitchTestAllCalls(r) {
				if call.Method == "session.close" && string(call.Params) == `{"session_id":"late-candidate"}` {
					return
				}
			}
			t.Fatal("late candidate was not closed")
		})
	}
}
