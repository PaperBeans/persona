package node

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSegmentPreparationIsEmptyAndReceiptedAndRetirementIsReversible(t *testing.T) {
	f := cliTest(t)
	ctx := t.Context()
	request := map[string]any{"operation_id": "scoped-operation", "launcher": "claude", "cwd": t.TempDir(), "text": "must not become a turn", "supervision_instructions": "the role policy the manager made"}
	raw, err := nodeCall(t, f.n, "assistant.segment.prepare", request)
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		SessionID string `json:"session_id"`
		Prepared  bool   `json:"prepared"`
	}
	if err = json.Unmarshal(raw, &receipt); err != nil || !receipt.Prepared || receipt.SessionID == "" {
		t.Fatalf("preparation: %s %v", raw, err)
	}
	again, err := nodeCall(t, f.n, "assistant.segment.prepare", request)
	if err != nil || string(again) != string(raw) {
		t.Fatalf("replay created another session: %s %v", again, err)
	}
	saved, _ := f.n.state.get(receipt.SessionID)
	// The manager's role policy is installed with the preparation, so the first input (same policy) keeps this process.
	if saved.SegmentOperation != "scoped-operation" || saved.SupervisionInstructions != "the role policy the manager made" {
		t.Fatalf("preparation lost the role policy or the receipt: %+v", saved)
	}
	input, _ := os.ReadFile(filepath.Join(f.dir, "stdin.jsonl"))
	if strings.Contains(string(input), `"type":"user"`) || strings.Contains(string(input), "must not become a turn") {
		t.Fatalf("preparation ran a prompt: %s", input)
	}
	if _, err = nodeCall(t, f.n, "assistant.segment.retire", map[string]any{"session_id": receipt.SessionID}); err != nil {
		t.Fatal(err)
	}
	_, err = nodeCall(t, f.n, "assistant.send", map[string]any{"session_id": receipt.SessionID, "text": "blocked"})
	var refused *rpc.Error
	if !errors.As(err, &refused) || refused.Code != "conversation_retired" {
		t.Fatalf("retired session resumed: %v", err)
	}
	// A stale session snapshot cannot erase a retirement written through update.
	f.n.state.put(receipt.SessionID, saved)
	current, _ := f.n.state.get(receipt.SessionID)
	if !current.SegmentRetired {
		t.Fatal("stale snapshot cleared retirement")
	}
	if _, err = nodeCall(t, f.n, "assistant.segment.restore", map[string]any{"session_id": receipt.SessionID}); err != nil {
		t.Fatal(err)
	}
	current, _ = f.n.state.get(receipt.SessionID)
	if current.SegmentRetired {
		t.Fatal("forward rollback did not restore source")
	}
	answer, err := f.n.assistantSegment(ctx, "assistant.segment.receipt", json.RawMessage(`{"operation_id":"unknown-after-restart"}`))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(answer)
	if !strings.Contains(string(data), `"unknown":true`) {
		t.Fatalf("missing receipt was treated as safe to retry: %s", data)
	}
}

func TestCodexSegmentRequiresDurableThreadBeforeReceipt(t *testing.T) {
	for _, tc := range []struct {
		name, title, wantTitle string
		refused                bool
	}{{"named", "Project notes", "Project notes", false}, {"older manager", "", "Persona", false}, {"write failed", "", "", true}} {
		t.Run(tc.name, func(t *testing.T) {
			fakeCodexHome(t, testThread)
			trace := t.TempDir()
			t.Setenv("PERSONA_FAKE_CODEX_TRACE", trace)
			record := filepath.Join(trace, "thread-name-set")
			if tc.refused {
				if err := os.Mkdir(record, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			n := codexTestNode(t)
			withFakeHub(t, n)
			t.Cleanup(func() { n.stopAssistantProcess(testThread) })
			request := map[string]any{"operation_id": "prepare-codex", "launcher": "codex", "cwd": t.TempDir(), "title": tc.title}
			_, err := nodeCall(t, n, "assistant.segment.prepare", request)
			id, received := n.segmentReceipt("prepare-codex")
			if tc.refused {
				var refusal *rpc.Error
				if !errors.As(err, &refusal) || refusal.Code != "missing_generation" || received {
					t.Fatalf("failed write received a preparation receipt: %v, %v", err, received)
				}
			} else {
				if err != nil || !received || id != testThread {
					t.Fatalf("preparation receipt: %q, %v, %v", id, received, err)
				}
				var call struct {
					Params struct{ ThreadID, Name string }
				}
				b, err := os.ReadFile(record)
				if err != nil || json.Unmarshal(b, &call) != nil || call.Params.ThreadID != id || call.Params.Name != tc.wantTitle {
					t.Fatalf("persisted thread name: %s, %v", b, err)
				}
			}
			if _, err := os.Stat(filepath.Join(trace, "turn-start")); !os.IsNotExist(err) {
				t.Fatalf("preparation started a model turn: %v", err)
			}
		})
	}
}

func TestSegmentRetirementWaitsForRuntimeApprovalToSettle(t *testing.T) {
	n, hub := runtimeNode(t)
	assistantHub(hub)
	host := dialRuntime(t, n, assistantRuntimeHello())
	id := startedAssistant(t, n, host)
	for _, status := range []string{proto.StatusBlocked, proto.StatusIdle} {
		if err := host.peer.Notify("runtime.sessions", map[string]any{"sessions": []proto.Session{{SessionID: id, Status: status}}}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "runtime status", func() bool { return listed(t, n)[id].Status == status })
		_, err := nodeCall(t, n, "assistant.segment.retire", map[string]any{"session_id": id})
		if status == proto.StatusBlocked {
			var refused *rpc.Error
			if !errors.As(err, &refused) || refused.Code != assistantwire.CodeConversationBusy {
				t.Fatalf("pending approval was retired: %v", err)
			}
			if st, _ := n.state.get(id); st.SegmentRetired {
				t.Fatal("busy refusal changed authority")
			}
		} else if err != nil {
			t.Fatal(err)
		}
	}
}

func TestSegmentCheckKeepsRuntimeIdentitySeparateWithoutStartingItsCLI(t *testing.T) {
	n, _ := runtimeNode(t)
	hello := assistantRuntimeHello()
	hello["agent"] = "claude"
	runtime := dialRuntime(t, n, hello)
	raw, err := nodeCall(t, n, "assistant.segment.check", map[string]any{"launcher": "dsh"})
	if err != nil {
		t.Fatal(err)
	}
	var target struct {
		Engine      string `json:"engine"`
		Registered  bool   `json:"registered"`
		Supervision bool   `json:"supervision_policy"`
	}
	if json.Unmarshal(raw, &target) != nil || target.Engine != "claude|runtime:dsh" || !target.Registered || target.Supervision {
		t.Fatalf("runtime contract: %s", raw)
	}
	native, err := nodeCall(t, n, "assistant.segment.check", map[string]any{"launcher": "claude"})
	if err != nil || strings.Contains(string(native), target.Engine) {
		t.Fatalf("native and runtime collapsed: %s %v", native, err)
	}
	if len(runtime.got("session.start")) != 0 || len(runtime.got("session.send")) != 0 {
		t.Fatal("checking execution identity started or sent a turn")
	}
}

func segmentInputAcceptedRPC(t *testing.T, n *Node, sessionID, clientMsgID string) bool {
	t.Helper()
	raw, err := nodeCall(t, n, "assistant.segment.input", map[string]any{"session_id": sessionID, "client_msg_id": clientMsgID})
	if err != nil {
		t.Fatalf("assistant.segment.input(%q, %q): %v", sessionID, clientMsgID, err)
	}
	var out struct {
		Accepted bool `json:"accepted"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode assistant.segment.input: %s: %v", raw, err)
	}
	return out.Accepted
}

func TestAssistantSendRecordsSegmentInputReceiptsWithoutLeakingSyntheticIDs(t *testing.T) {
	n, hub := runtimeNode(t)
	assistantHub(hub)
	host := dialRuntime(t, n, assistantRuntimeHello())
	sid := startedAssistant(t, n, host)
	staleSnapshot, _ := n.state.get(sid)

	sendsForText := func(text string) []map[string]any {
		var matched []map[string]any
		for _, raw := range host.got("session.send") {
			var p map[string]any
			if json.Unmarshal(raw, &p) == nil && p["text"] == text {
				matched = append(matched, p)
			}
		}
		return matched
	}

	// 1. Relay via assistant.send with once_id + synthetic segment-once client_msg_id.
	onceReq := map[string]any{"session_id": sid, "text": "relay-body", "once_id": "relay-1", "client_msg_id": "segment-once:relay-1"}
	if _, err := nodeCall(t, n, "assistant.send", onceReq); err != nil {
		t.Fatalf("assistant.send once: %v", err)
	}
	if !segmentInputAcceptedRPC(t, n, sid, "segment-once:relay-1") {
		t.Fatal("assistant.segment.input returned accepted=false for accepted once_id relay")
	}
	if _, err := nodeCall(t, n, "assistant.send", onceReq); err != nil {
		t.Fatalf("assistant.send once replay: %v", err)
	}
	gotOnce := sendsForText("relay-body")
	if len(gotOnce) != 1 {
		t.Fatalf("once_id replay delivered %d sends, want 1", len(gotOnce))
	}
	if gotID, _ := gotOnce[0]["client_msg_id"].(string); gotID != "" {
		t.Fatalf("synthetic segment-once id leaked to runtime: %q", gotID)
	}

	// 2. Queued internal assistant.send without once_id, with synthetic segment-input client_msg_id.
	queuedReq := map[string]any{"session_id": sid, "text": "scheduled-body", "client_msg_id": "segment-input:deadbeef"}
	if _, err := nodeCall(t, n, "assistant.send", queuedReq); err != nil {
		t.Fatalf("assistant.send queued: %v", err)
	}
	if !segmentInputAcceptedRPC(t, n, sid, "segment-input:deadbeef") {
		t.Fatal("assistant.segment.input returned accepted=false for accepted segment-input send")
	}
	if _, err := nodeCall(t, n, "assistant.send", queuedReq); err != nil {
		t.Fatalf("assistant.send queued replay: %v", err)
	}
	gotQueued := sendsForText("scheduled-body")
	if len(gotQueued) != 1 {
		t.Fatalf("queued assistant.send replay delivered %d sends, want 1", len(gotQueued))
	}
	if gotID, _ := gotQueued[0]["client_msg_id"].(string); gotID != "" {
		t.Fatalf("synthetic segment-input id leaked to runtime: %q", gotID)
	}

	// 3. Explicit non-synthetic client_msg_id is still forwarded to the runtime and receipted.
	callerReq := map[string]any{"session_id": sid, "text": "caller-body", "client_msg_id": "caller-msg-1"}
	if _, err := nodeCall(t, n, "assistant.send", callerReq); err != nil {
		t.Fatalf("assistant.send caller: %v", err)
	}
	if !segmentInputAcceptedRPC(t, n, sid, "caller-msg-1") {
		t.Fatal("assistant.segment.input returned accepted=false for caller client_msg_id")
	}
	gotCaller := sendsForText("caller-body")
	if len(gotCaller) != 1 {
		t.Fatalf("caller assistant.send delivered %d sends, want 1", len(gotCaller))
	}
	if gotID, _ := gotCaller[0]["client_msg_id"].(string); gotID != "caller-msg-1" {
		t.Fatalf("caller client_msg_id = %q, want caller-msg-1", gotID)
	}

	// 4. Refused send (retired segment) must never record an accepted receipt.
	if err := host.peer.Notify("runtime.sessions", map[string]any{"sessions": []proto.Session{{SessionID: sid, Status: proto.StatusIdle}}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "runtime idle before retire", func() bool { return listed(t, n)[sid].Status == proto.StatusIdle })
	if _, err := nodeCall(t, n, "assistant.segment.retire", map[string]any{"session_id": sid}); err != nil {
		t.Fatalf("retire: %v", err)
	}
	_, err := nodeCall(t, n, "assistant.send", map[string]any{"session_id": sid, "text": "blocked", "client_msg_id": "segment-input:refused"})
	var refused *rpc.Error
	if !errors.As(err, &refused) || refused.Code != "conversation_retired" {
		t.Fatalf("retired send err = %v, want conversation_retired", err)
	}
	if segmentInputAcceptedRPC(t, n, sid, "segment-input:refused") {
		t.Fatal("refused send was recorded as accepted")
	}
	if segmentInputAcceptedRPC(t, n, sid, "") || segmentInputAcceptedRPC(t, n, "", "caller-msg-1") {
		t.Fatal("empty session_id or client_msg_id reported accepted=true")
	}

	// 5. Stale state.put cannot erase persisted receipts, and restart merge preserves them.
	n.state.put(sid, staleSnapshot)
	persisted, ok := n.state.get(sid)
	if !ok {
		t.Fatal("missing session state after stale put")
	}
	n2, _ := runtimeNode(t)
	n2.state.merge(map[string]sessionState{sid: persisted})
	for _, id := range []string{"segment-once:relay-1", "segment-input:deadbeef", "caller-msg-1"} {
		if !segmentInputAcceptedRPC(t, n2, sid, id) {
			t.Fatalf("restarted node lost accepted segment receipt for %q", id)
		}
	}
}

func TestSessionSendSyntheticSegmentIDRecordsReceiptWithoutTaggingEcho(t *testing.T) {
	t.Run("assistant runtime", func(t *testing.T) {
		n, hub := runtimeNode(t)
		assistantHub(hub)
		host := dialRuntime(t, n, assistantRuntimeHello())
		sid := startedAssistant(t, n, host)

		req := map[string]any{"session_id": sid, "text": "assistant-session-send", "client_msg_id": "segment-input:sess1"}
		if _, err := nodeCall(t, n, "session.send", req); err != nil {
			t.Fatalf("session.send: %v", err)
		}
		if _, err := nodeCall(t, n, "session.send", req); err != nil {
			t.Fatalf("session.send replay: %v", err)
		}
		if !segmentInputAcceptedRPC(t, n, sid, "segment-input:sess1") {
			t.Fatal("assistant.segment.input returned accepted=false for synthetic session.send")
		}
		if _, hasEcho := n.echoes.record(sid, "segment-input:sess1"); hasEcho {
			t.Fatal("synthetic segment-input id polluted n.echoes")
		}
		count := 0
		for _, raw := range host.got("session.send") {
			var p map[string]any
			if json.Unmarshal(raw, &p) == nil && p["text"] == "assistant-session-send" {
				count++
				if gotID, _ := p["client_msg_id"].(string); gotID != "" {
					t.Fatalf("synthetic id leaked to assistant runtime: %q", gotID)
				}
			}
		}
		if count != 1 {
			t.Fatalf("assistant session.send count = %d, want 1", count)
		}
	})

	t.Run("non-assistant runtime", func(t *testing.T) {
		n, _ := runtimeNode(t)
		host := dialRuntime(t, n, map[string]any{
			"runtime": "antigravity",
			"agent":   "agy",
			"name":    "Antigravity",
			"caps":    map[string]any{"send": true},
		})
		sid := "agy-work-1"
		if err := host.peer.Notify("runtime.sessions", map[string]any{"sessions": []proto.Session{{SessionID: sid, Status: proto.StatusIdle}}}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "runtime session listed", func() bool { _, ok := listed(t, n)[sid]; return ok })

		req := map[string]any{"session_id": sid, "text": "work-session-send", "client_msg_id": "segment-input:work1"}
		if _, err := nodeCall(t, n, "session.send", req); err != nil {
			t.Fatalf("session.send: %v", err)
		}
		if _, err := nodeCall(t, n, "session.send", req); err != nil {
			t.Fatalf("session.send replay: %v", err)
		}
		if !segmentInputAcceptedRPC(t, n, sid, "segment-input:work1") {
			t.Fatal("assistant.segment.input returned accepted=false for non-assistant runtime synthetic session.send")
		}
		if _, hasEcho := n.echoes.record(sid, "segment-input:work1"); hasEcho {
			t.Fatal("synthetic segment-input id polluted n.echoes on runtime session")
		}
		count := 0
		for _, raw := range host.got("session.send") {
			var p map[string]any
			if json.Unmarshal(raw, &p) == nil && p["text"] == "work-session-send" {
				count++
				if _, present := p["client_msg_id"]; present {
					t.Fatalf("synthetic client_msg_id leaked to non-assistant runtime: %v", p["client_msg_id"])
				}
			}
		}
		if count != 1 {
			t.Fatalf("non-assistant runtime session.send count = %d, want 1", count)
		}
	})
}

func TestSegmentInputReceiptsAreBoundedAndMergeCleanly(t *testing.T) {
	n, _ := runtimeNode(t)
	sid := "bounded-session"
	n.state.put(sid, sessionState{Launcher: "agy", Cwd: t.TempDir()})
	for i := range 70 {
		n.recordSegmentInput(sid, fmt.Sprintf("segment-input:%03d", i))
	}
	// Repeat an already-recorded ID; list must stay deduplicated and capped.
	n.recordSegmentInput(sid, "segment-input:069")
	st, ok := n.state.get(sid)
	if !ok {
		t.Fatal("missing session state")
	}
	if len(st.SegmentInputs) != segmentInputReceiptCap {
		t.Fatalf("SegmentInputs len = %d, want %d", len(st.SegmentInputs), segmentInputReceiptCap)
	}
	if segmentInputAcceptedRPC(t, n, sid, "segment-input:000") {
		t.Fatal("oldest evicted receipt still reported accepted")
	}
	if !segmentInputAcceptedRPC(t, n, sid, "segment-input:069") {
		t.Fatal("newest receipt missing")
	}

	// Merging hub state into a node that already recorded a local receipt keeps both.
	n2, _ := runtimeNode(t)
	n2.state.put(sid, sessionState{Launcher: "agy", Cwd: t.TempDir()})
	n2.recordSegmentInput(sid, "segment-input:local-after-boot")
	n2.state.merge(map[string]sessionState{sid: {Launcher: "agy", SegmentInputs: []string{"segment-input:hub-before-boot"}}})
	if !segmentInputAcceptedRPC(t, n2, sid, "segment-input:hub-before-boot") || !segmentInputAcceptedRPC(t, n2, sid, "segment-input:local-after-boot") {
		t.Fatal("state.merge failed to union persisted and newly recorded segment receipts")
	}

	// Stale state.put snapshot from before cap rollover must not resurrect evicted receipts or evict current ones.
	n3, _ := runtimeNode(t)
	n3.state.put(sid, sessionState{Launcher: "agy", Cwd: t.TempDir()})
	for i := range segmentInputReceiptCap {
		n3.recordSegmentInput(sid, fmt.Sprintf("segment-input:%03d", i))
	}
	stale, _ := n3.state.get(sid)
	want := make([]string, 0, segmentInputReceiptCap)
	for i := segmentInputReceiptCap; i < 2*segmentInputReceiptCap; i++ {
		id := fmt.Sprintf("segment-input:%03d", i)
		n3.recordSegmentInput(sid, id)
		want = append(want, id)
	}
	n3.state.put(sid, stale)
	afterStale, _ := n3.state.get(sid)
	if !slices.Equal(afterStale.SegmentInputs, want) {
		t.Fatalf("stale state.put corrupted SegmentInputs: got first=%q last=%q len=%d, want first=%q last=%q len=%d",
			afterStale.SegmentInputs[0], afterStale.SegmentInputs[len(afterStale.SegmentInputs)-1], len(afterStale.SegmentInputs),
			want[0], want[len(want)-1], len(want))
	}
}

func TestPreLoadSegmentInputFlushesOnFetchState(t *testing.T) {
	n, hub := runtimeNode(t)
	sid := "pre-load-session"
	var mu sync.Mutex
	var persisted []sessionState
	hub.answer = func(method string, _ json.RawMessage) (any, error, bool) {
		if method == "state.load" {
			return map[string]any{
				"sessions": map[string]sessionState{
					sid: {Launcher: "agy", Cwd: t.TempDir(), SegmentInputs: []string{"segment-once:from-hub"}},
				},
			}, nil, true
		}
		return nil, nil, false
	}
	n.state.persist = func(id string, v sessionState) {
		if id != sid {
			return
		}
		mu.Lock()
		persisted = append(persisted, v)
		mu.Unlock()
	}

	// Receipt accepted before state.load returns has no state record yet.
	n.recordSegmentInput(sid, "segment-input:pre-load")
	if !n.fetchState(t.Context()) {
		t.Fatal("fetchState returned false")
	}
	if !segmentInputAcceptedRPC(t, n, sid, "segment-once:from-hub") || !segmentInputAcceptedRPC(t, n, sid, "segment-input:pre-load") {
		t.Fatal("fetchState did not keep both hub and pre-load receipts")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(persisted) == 0 {
		t.Fatal("fetchState did not flush pre-load segment receipt through state.put")
	}
	last := persisted[len(persisted)-1]
	if !slices.Contains(last.SegmentInputs, "segment-once:from-hub") || !slices.Contains(last.SegmentInputs, "segment-input:pre-load") {
		t.Fatalf("flushed state.put SegmentInputs = %v, want both hub and pre-load receipts", last.SegmentInputs)
	}
}

func TestSyntheticSegmentIDDoesNotResetIdleCompaction(t *testing.T) {
	n, hub := runtimeNode(t)
	assistantHub(hub)
	host := dialRuntime(t, n, assistantRuntimeHello())
	sid := startedAssistant(t, n, host)

	oldTime := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
	n.state.update(sid, func(st *sessionState) bool {
		st.IsMain = true
		st.IdleCompaction = &idleCompactionState{Since: oldTime}
		return true
	})

	noticeText := "🔔 任务「Build check」做完了\n这是 Persona 的任务通知"
	for _, req := range []map[string]any{
		{"session_id": sid, "text": noticeText, "once_id": "relay-idle-1", "client_msg_id": "segment-once:relay-idle-1"},
		{"session_id": sid, "text": noticeText, "client_msg_id": "segment-input:idle-2"},
	} {
		if _, err := nodeCall(t, n, "assistant.send", req); err != nil {
			t.Fatalf("assistant.send %v: %v", req["client_msg_id"], err)
		}
		st, ok := n.state.get(sid)
		if !ok || st.IdleCompaction == nil {
			t.Fatal("missing idle compaction state")
		}
		if !st.IdleCompaction.Since.Equal(oldTime) {
			t.Fatalf("synthetic %v bumped IdleCompaction.Since from %v to %v", req["client_msg_id"], oldTime, st.IdleCompaction.Since)
		}
	}
}
