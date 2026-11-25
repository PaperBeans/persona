package node

import (
	"encoding/json"
	"errors"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
