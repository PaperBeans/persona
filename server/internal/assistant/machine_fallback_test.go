package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// machineFallbackTestRig sets up a multi-machine conversation-switch fixture backed by real Postgres,
// with configurable per-machine online status, work-machine flags, SupervisionPolicy support, and
// persisted Postgres ConversationHistory.
type machineFallbackTestRig struct {
	*conversationSwitchTestRig
	mu               sync.Mutex
	online           map[string]bool
	workMachines     map[string]bool
	supervisionOK    map[string]bool
	sends            []DeviceCall
	closed           []string
	preparedByTarget map[string]string
}

func newMachineFallbackRig(t *testing.T) *machineFallbackTestRig {
	t.Helper()
	base := newConversationSwitchTestRig(t)
	r := &machineFallbackTestRig{
		conversationSwitchTestRig: base,
		online:                    map[string]bool{"server": true, "backup-a": true, "backup-b": true, "work-pc": true},
		workMachines:              map[string]bool{"work-pc": true},
		supervisionOK:             map[string]bool{"server": true, "backup-a": true, "backup-b": true, "work-pc": true},
		preparedByTarget:          map[string]string{},
	}

	base.f.mu.Lock()
	base.f.hello.Assistant.Machines = []string{"server", "backup-a", "backup-b", "work-pc"}
	base.f.hello.Assistant.WorkMachines = []string{"work-pc"}
	for _, id := range []string{"server", "backup-a", "backup-b", "work-pc"} {
		if _, ok := base.f.sessions[id]; !ok {
			base.f.sessions[id] = []proto.Session{}
		}
	}
	base.f.mu.Unlock()
	r.syncDevices()

	// Offline machines reject direct node RPCs with apierr.ErrOffline.
	checkOnline := func(machine string) error {
		r.mu.Lock()
		on := r.online[machine]
		r.mu.Unlock()
		if !on {
			return apierr.ErrOffline
		}
		return nil
	}

	base.f.answerNode("assistant.segment.check", func(machine string, raw json.RawMessage) (any, error) {
		if err := checkOnline(machine); err != nil {
			return nil, err
		}
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		engine := "codex"
		if p["launcher"] == "sub" || p["launcher"] == "claude" {
			engine = "claude"
		}
		r.mu.Lock()
		sup := r.supervisionOK[machine]
		r.mu.Unlock()
		return segmentTarget{Registered: true, Engine: engine, SupervisionPolicy: sup, Prepare: true}, nil
	})

	base.f.answerNode("assistant.segment.prepare", func(machine string, raw json.RawMessage) (any, error) {
		if err := checkOnline(machine); err != nil {
			return nil, err
		}
		var p map[string]any
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		r.conversationSwitchTestRig.mu.Lock()
		r.preparations = append(r.preparations, p)
		id := fmt.Sprintf("prepared-%s-%d", machine, len(r.preparations))
		r.conversationSwitchTestRig.mu.Unlock()

		r.mu.Lock()
		r.preparedByTarget[id] = machine
		r.mu.Unlock()

		launcher := fmt.Sprint(p["launcher"])
		agent := "claude"
		if launcher != "sub" && launcher != "claude" {
			agent = "codex"
		}
		r.f.known[id] = true
		r.f.sessions[machine] = append(r.f.sessions[machine], proto.Session{
			SessionID:      id,
			MachineID:      machine,
			Assistant:      true,
			Agent:          agent,
			Launcher:       launcher,
			PermissionMode: fmt.Sprint(p["permission_mode"]),
			Status:         proto.StatusIdle,
			State:          proto.StateIdle,
			Rev:            100,
		})
		return map[string]any{"session_id": id, "prepared": true}, nil
	})

	base.f.answerNode("assistant.segment.retire", func(machine string, raw json.RawMessage) (any, error) {
		if err := checkOnline(machine); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, nil
	})

	base.f.answerNode("assistant.segment.restore", func(machine string, raw json.RawMessage) (any, error) {
		if err := checkOnline(machine); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, nil
	})

	base.f.answerNode("session.close", func(machine string, raw json.RawMessage) (any, error) {
		if err := checkOnline(machine); err != nil {
			return nil, err
		}
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		r.mu.Lock()
		r.closed = append(r.closed, machine+"/"+fmt.Sprint(p["session_id"]))
		r.mu.Unlock()
		return map[string]bool{"ok": true}, nil
	})

	base.f.answerNode(DeviceSessionItems, func(machine string, raw json.RawMessage) (any, error) {
		if err := checkOnline(machine); err != nil {
			return nil, err
		}
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		toSeq := 2
		return historyPage{
			History: "gen-" + fmt.Sprint(p["session_id"]),
			ToSeq:   &toSeq,
			Items: []proto.Item{
				{ID: "i1", Seq: 1, Kind: proto.KindText, Role: "user", Text: "hello"},
				{ID: "i2", Seq: 2, Kind: proto.KindText, Role: "assistant", Text: "world"},
			},
		}, nil
	})

	base.f.answerNode(DeviceAssistantHandoff, func(machine string, raw json.RawMessage) (any, error) {
		if err := checkOnline(machine); err != nil {
			return nil, err
		}
		return mainHandoff{Summary: "live summary"}, nil
	})

	for _, method := range []string{DeviceSessionSend, DeviceAssistantSend} {
		m := method
		base.f.answerNode(m, func(machine string, raw json.RawMessage) (any, error) {
			if err := checkOnline(machine); err != nil {
				return nil, err
			}
			r.mu.Lock()
			r.sends = append(r.sends, DeviceCall{Device: machine, Method: m, Params: append(json.RawMessage(nil), raw...)})
			r.mu.Unlock()
			var p map[string]any
			_ = json.Unmarshal(raw, &p)
			return map[string]any{"ok": true, "session_id": p["session_id"]}, nil
		})
	}

	return r
}

func (r *machineFallbackTestRig) syncDevices() {
	r.mu.Lock()
	devs := make([]Device, 0, 4)
	for _, id := range []string{"server", "backup-a", "backup-b", "work-pc"} {
		on := r.online[id]
		devs = append(devs, Device{
			ID:        id,
			Name:      id,
			Online:    on,
			Reachable: on,
		})
	}
	r.mu.Unlock()
	r.f.mu.Lock()
	r.f.devices = devs
	r.f.mu.Unlock()
}

func (r *machineFallbackTestRig) setOnline(machine string, on bool) {
	r.mu.Lock()
	r.online[machine] = on
	r.mu.Unlock()
	r.syncDevices()
}

func (r *machineFallbackTestRig) seedHistory(t *testing.T, machine, sid, summary string) {
	t.Helper()
	ctx := context.Background()
	err := r.d.PutConversationPage(ctx, db.ConversationPage{
		MachineID:  machine,
		SessionID:  sid,
		History:    "pg-gen-" + sid,
		Revision:   100,
		FromSeq:    0,
		ThroughSeq: 2,
		Tail:       true,
		Items: []proto.Item{
			{ID: "u1", Seq: 1, Kind: proto.KindText, Role: "user", Text: "remember project alpha"},
			{ID: "a1", Seq: 2, Kind: proto.KindText, Role: "assistant", Text: "noted project alpha"},
		},
	})
	if err != nil {
		t.Fatalf("seed conversation page: %v", err)
	}
	if summary != "" {
		if err := r.d.PutConversationSummary(ctx, machine, sid, "pg-gen-"+sid, 100, summary); err != nil {
			t.Fatalf("seed conversation summary: %v", err)
		}
	}
}

func (r *machineFallbackTestRig) sendsOn(machine string) []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]any
	for _, call := range r.sends {
		if call.Device == machine {
			var p map[string]any
			_ = json.Unmarshal(call.Params, &p)
			out = append(out, p)
		}
	}
	return out
}

func (r *machineFallbackTestRig) closedSessions() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.closed))
	copy(out, r.closed)
	return out
}

func machineFallbackBatches(t *testing.T, s *Service) []conversationSwitchBatch {
	t.Helper()
	state, _, err := s.loadConversationSwitches(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []conversationSwitchBatch
	for _, b := range state.Batches {
		if b.MachineFallback != nil {
			out = append(out, b)
		}
	}
	return out
}

//  1. Primary offline past threshold -> automatic failover of main chat using Postgres history + summary,
//     draining queued undelivered message so user continues chatting without interruption.
func TestMachineFallbackMovesMainWhenOfflinePastThresholdAndDrainsUndelivered(t *testing.T) {
	r := newMachineFallbackRig(t)
	ctx := context.Background()
	mins := 5
	setSettings(t, r.s, func(cfg *Settings) {
		cfg.MachineID = "server"
		cfg.Main = "main-chat"
		cfg.MainMachineID = "server"
		cfg.Leaders = nil
		cfg.Launcher = "claude"
		cfg.MachineFallbacks = []string{"work-pc", "backup-a", "backup-b"}
		cfg.MachineFallbackMinutes = &mins
	})
	r.seedHistory(t, "server", "main-chat", "We are shipping v2 this week.")

	// Simulate primary going offline and dropping its live session cache.
	r.setOnline("server", false)
	r.f.mu.Lock()
	r.f.sessions["server"] = []proto.Session{}
	r.f.mu.Unlock()

	// Keep an owner message in assistant.undelivered while primary is offline.
	_, err := r.s.KeepUndelivered(ctx, assistantwire.KeepUndeliveredParams{
		MachineID: "server",
		SessionID: "main-chat",
		Code:      "machine_offline",
		Params: map[string]any{
			"client_msg_id": "owner-offline-1",
			"text":          "are you still there?",
		},
	})
	if err != nil {
		t.Fatalf("KeepUndelivered: %v", err)
	}

	base := time.Now().Add(-10 * time.Minute)
	r.s.noteNodeOffline("server", base)
	r.s.machineFallbackSweep(ctx, base.Add(6*time.Minute))

	batches := machineFallbackBatches(t, r.s)
	if len(batches) != 1 {
		t.Fatalf("expected 1 machine fallback batch, got %+v", batches)
	}
	b := batches[0]
	// work-pc must be skipped; backup-a must be selected.
	if b.MachineFallback.TargetMachine != "backup-a" || b.Items[0].Target.Machine != "backup-a" {
		t.Fatalf("expected failover to backup-a (skipping work-pc), got %+v", b)
	}

	conversationSwitchTestDrive(t, r.s, b.ID, map[string]string{"main-chat": conversationSwitchSwitched})

	c, err := r.d.LogicalConversation(ctx, "server", "main-chat")
	if err != nil || c == nil {
		t.Fatalf("logical conversation after failover: %v %+v", err, c)
	}
	active, ok := c.Active()
	if !ok || active.MachineID != "backup-a" || c.Epoch != 2 {
		t.Fatalf("expected active segment on backup-a at epoch 2, got active=%+v epoch=%d", active, c.Epoch)
	}

	// Drain undelivered messages and verify the owner's message reaches backup-a with the handoff summary.
	r.s.undeliveredPass(ctx, time.Now().Add(time.Minute))
	waitFor(t, "undelivered owner message to reach backup-a", func() bool {
		r.s.logicalSweep(ctx)
		return len(r.sendsOn("backup-a")) >= 1
	})
	sent := r.sendsOn("backup-a")
	text := fmt.Sprint(sent[0]["text"])
	if !strings.Contains(text, "are you still there?") || !strings.Contains(text, "We are shipping v2 this week.") {
		t.Fatalf("expected drained message with persisted summary on backup-a, got: %s", text)
	}
}

//  2. Direction leader failover skips machines without SupervisionPolicy, preserves supervision ownership,
//     and drains queued relay supervision messages.
func TestMachineFallbackMovesLeaderPreservesSupervisionAndDrainsRelay(t *testing.T) {
	r := newMachineFallbackRig(t)
	ctx := context.Background()
	mins := 5
	setSettings(t, r.s, func(cfg *Settings) {
		cfg.MachineID = "server"
		cfg.Main = "main-chat"
		cfg.MainMachineID = "server"
		cfg.Leaders = []string{"leader-1"}
		cfg.Launcher = "claude"
		cfg.MachineFallbacks = []string{"backup-a", "backup-b"}
		cfg.MachineFallbackMinutes = &mins
	})
	// backup-a does NOT support SupervisionPolicy; failover must pick backup-b.
	r.mu.Lock()
	r.supervisionOK["backup-a"] = false
	r.mu.Unlock()

	r.seedHistory(t, "server", "leader-1", "Supervising task T-42.")

	// Queue a supervision relay message for leader-1 while server is offline.
	r.s.relays.mu.Lock()
	r.s.relayLocked(ctx)
	r.s.relays.st.Queue["leader-1"] = []relayMsg{{
		From: "todo_42",
		Tool: superviseTool,
		Text: "Task T-42 finished and awaits review.",
		At:   time.Now().Add(-time.Minute),
	}}
	_ = r.d.PutSetting(ctx, relayKey, r.s.relays.st)
	r.s.relays.mu.Unlock()

	r.setOnline("server", false)
	r.f.mu.Lock()
	r.f.sessions["server"] = []proto.Session{}
	r.f.mu.Unlock()

	base := time.Now().Add(-10 * time.Minute)
	r.s.noteNodeOffline("server", base)
	r.s.machineFallbackSweep(ctx, base.Add(6*time.Minute))

	batches := machineFallbackBatches(t, r.s)
	var leaderBatch *conversationSwitchBatch
	for i := range batches {
		if len(batches[i].Items) == 1 && batches[i].Items[0].ConversationID == "leader-1" {
			leaderBatch = &batches[i]
			break
		}
	}
	if leaderBatch == nil {
		t.Fatalf("expected failover batch for leader-1, got %+v", batches)
	}
	if leaderBatch.MachineFallback.TargetMachine != "backup-b" {
		t.Fatalf("expected backup-b (backup-a lacks SupervisionPolicy), got %+v", leaderBatch.MachineFallback)
	}

	conversationSwitchTestDrive(t, r.s, leaderBatch.ID, map[string]string{"leader-1": conversationSwitchSwitched})

	// Supervision ownership remains leader-1 (public logical ID unchanged).
	cfg := r.s.settings(ctx)
	if !r.s.isLeader(ctx, "leader-1") {
		t.Fatalf("leader-1 lost leader status after failover")
	}
	todo := Todo{ID: "todo_42", Supervision: &Supervision{Supervisor: "leader-1"}}
	if got := r.s.effectiveSupervisor(ctx, cfg, todo); got != "leader-1" {
		t.Fatalf("expected effectiveSupervisor leader-1, got %q", got)
	}
	role, err := r.s.logicalRoleRequired(ctx, "server", "leader-1")
	if err != nil || !role {
		t.Fatalf("expected logicalRoleRequired=true for leader-1, got %v %v", role, err)
	}

	// Relay queue drains to backup-b.
	waitFor(t, "queued relay message to reach backup-b", func() bool {
		r.s.logicalSweep(ctx)
		return len(r.sendsOn("backup-b")) >= 1
	})
	sent := r.sendsOn("backup-b")
	if !strings.Contains(fmt.Sprint(sent[0]["text"]), "Task T-42 finished") {
		t.Fatalf("expected relay text on backup-b, got %+v", sent[0])
	}
}

//  3. When the original computer reconnects, there is no split-brain: the retired segment is fenced
//     and closed via ClosePending, and manual user changes (epoch bump) are never overwritten.
func TestMachineFallbackReconnectNoSplitBrainAndEpochFenceProtectsManualChange(t *testing.T) {
	r := newMachineFallbackRig(t)
	ctx := context.Background()
	mins := 5
	setSettings(t, r.s, func(cfg *Settings) {
		cfg.MachineID = "server"
		cfg.Main = "main-chat"
		cfg.MainMachineID = "server"
		cfg.Leaders = nil
		cfg.Launcher = "claude"
		cfg.MachineFallbacks = []string{"backup-a"}
		cfg.MachineFallbackMinutes = &mins
	})
	r.seedHistory(t, "server", "main-chat", "Original context.")
	r.setOnline("server", false)

	base := time.Now().Add(-10 * time.Minute)
	r.s.noteNodeOffline("server", base)
	r.s.machineFallbackSweep(ctx, base.Add(6*time.Minute))

	batches := machineFallbackBatches(t, r.s)
	if len(batches) != 1 {
		t.Fatalf("expected 1 failover batch, got %+v", batches)
	}
	conversationSwitchTestDrive(t, r.s, batches[0].ID, map[string]string{"main-chat": conversationSwitchSwitched})

	// Original computer reconnects.
	r.setOnline("server", true)
	r.s.nodeConnectedAt(ctx, "server", time.Now())
	r.s.logicalSweep(ctx)

	// 1) Retired segment on "server" is rejected by logicalCaller (no split-brain).
	if _, _, _, err := r.s.logicalCaller(ctx, "server", "main-chat"); err == nil {
		t.Fatalf("expected retired source segment on server to be rejected by logicalCaller")
	}
	// 2) Active segment on "backup-a" is accepted by logicalCaller and maps back to ("server", "main-chat").
	c, err := r.d.LogicalConversation(ctx, "server", "main-chat")
	if err != nil || c == nil {
		t.Fatal(err)
	}
	active, _ := c.Active()
	lm, ls, _, err := r.s.logicalCaller(ctx, active.MachineID, active.SessionID)
	if err != nil || lm != "server" || ls != "main-chat" {
		t.Fatalf("expected active backup-a segment to authenticate as server/main-chat, got %s/%s err=%v", lm, ls, err)
	}
	// 3) Old segment on server received session.close via ClosePending.
	waitFor(t, "retired segment server/main-chat to be closed on reconnect", func() bool {
		r.s.logicalSweep(ctx)
		return containsString(r.closedSessions(), "server/main-chat")
	})

	// 4) Epoch fence: a stale failover batch with an outdated ExpectedEpoch is refused and never overwrites.
	r.s.conversationSwitches.mu.Lock()
	state, _, err := r.s.loadConversationSwitches(ctx)
	if err != nil {
		r.s.conversationSwitches.mu.Unlock()
		t.Fatal(err)
	}
	stale := conversationSwitchBatch{
		ID:        "stale-failover",
		CreatedAt: time.Now().UTC(),
		Items: []conversationSwitchItem{{
			Machine:        "server",
			ConversationID: "main-chat",
			ExpectedEpoch:  1, // stale: current epoch is 2
			Before:         conversationExecution{Machine: "server", Launcher: "claude", PermissionMode: "default"},
			Target:         conversationExecution{Machine: "backup-b", Launcher: "claude", PermissionMode: "default"},
			OperationID:    "stale-op",
			Outcome:        conversationSwitchQueued,
		}},
		MachineFallback: &conversationSwitchMachineFallback{SourceMachine: "server", TargetMachine: "backup-b"},
	}
	state.Batches = append(state.Batches, stale)
	r.s.runConversationSwitchesLocked(ctx, &state)
	r.s.conversationSwitches.mu.Unlock()

	var staleOutcome string
	for _, b := range state.Batches {
		if b.ID == "stale-failover" {
			staleOutcome = b.Items[0].Outcome
		}
	}
	if staleOutcome != conversationSwitchRefused {
		t.Fatalf("expected stale epoch failover batch to be refused, got %q", staleOutcome)
	}
}

// 4. Empty assistant.machine_fallbacks preserves existing behavior (no failover, no routine fallback).
func TestMachineFallbackEmptyListNoOp(t *testing.T) {
	r := newMachineFallbackRig(t)
	ctx := context.Background()
	mins := 5
	setSettings(t, r.s, func(cfg *Settings) {
		cfg.MachineID = "server"
		cfg.Main = "main-chat"
		cfg.MainMachineID = "server"
		cfg.MachineFallbacks = nil
		cfg.MachineFallbackMinutes = &mins
	})
	r.seedHistory(t, "server", "main-chat", "Context.")
	r.setOnline("server", false)

	base := time.Now().Add(-30 * time.Minute)
	r.s.noteNodeOffline("server", base)
	r.s.machineFallbackSweep(ctx, base.Add(20*time.Minute))

	if batches := machineFallbackBatches(t, r.s); len(batches) != 0 {
		t.Fatalf("expected no failover batches when machine_fallbacks is empty, got %+v", batches)
	}
	if got := r.s.taskMachine(ctx, Task{ID: "t1"}); got != "server" {
		t.Fatalf("expected taskMachine to remain server when machine_fallbacks is empty, got %q", got)
	}
}

// 5. Offline duration below threshold does not trigger failover or routine fallback.
func TestMachineFallbackBelowThresholdDoesNotMove(t *testing.T) {
	r := newMachineFallbackRig(t)
	ctx := context.Background()
	mins := 10
	setSettings(t, r.s, func(cfg *Settings) {
		cfg.MachineID = "server"
		cfg.Main = "main-chat"
		cfg.MainMachineID = "server"
		cfg.MachineFallbacks = []string{"backup-a"}
		cfg.MachineFallbackMinutes = &mins
	})
	r.seedHistory(t, "server", "main-chat", "Context.")
	r.setOnline("server", false)

	base := time.Now().Add(-5 * time.Minute)
	r.s.noteNodeOffline("server", base)
	r.s.machineFallbackSweep(ctx, base.Add(5*time.Minute))

	if batches := machineFallbackBatches(t, r.s); len(batches) != 0 {
		t.Fatalf("expected no failover below threshold, got %+v", batches)
	}
	if got := r.s.taskMachine(ctx, Task{ID: "t1"}); got != "server" {
		t.Fatalf("expected taskMachine to remain server below threshold, got %q", got)
	}
}

//  6. Unbound default scheduled routines fall back to the first eligible online backup machine past threshold,
//     while explicitly pinned routines (ExecutionMachineID or Cwd) stay pinned.
func TestMachineFallbackScheduleRoutineFallback(t *testing.T) {
	r := newMachineFallbackRig(t)
	ctx := context.Background()
	mins := 5
	setSettings(t, r.s, func(cfg *Settings) {
		cfg.MachineID = "server"
		cfg.MachineFallbacks = []string{"work-pc", "backup-a", "backup-b"}
		cfg.MachineFallbackMinutes = &mins
	})
	r.setOnline("server", false)
	r.s.noteNodeOffline("server", time.Now().Add(-10*time.Minute))

	// Unbound routine falls back to backup-a (skipping work-pc).
	if got := r.s.taskMachine(ctx, Task{ID: "unbound"}); got != "backup-a" {
		t.Fatalf("expected unbound routine to fall back to backup-a, got %q", got)
	}
	// Routine pinned to a specific machine stays pinned.
	if got := r.s.taskMachine(ctx, Task{ID: "pinned-machine", ExecutionMachineID: "server"}); got != "server" {
		t.Fatalf("expected machine-pinned routine to stay on server, got %q", got)
	}
	// Routine pinned to a local directory stays pinned.
	if got := r.s.taskMachine(ctx, Task{ID: "pinned-cwd", Cwd: "/tmp/repo"}); got != "server" {
		t.Fatalf("expected cwd-pinned routine to stay on server, got %q", got)
	}
}

// 7. persona_settings get/set validation for assistant.machine_fallbacks and assistant.machine_fallback_minutes.
func TestMachineFallbackSettingsValidation(t *testing.T) {
	r := newMachineFallbackRig(t)
	ctx := context.Background()

	// Reject work machine.
	if _, err := r.s.setMachineFallbacks(ctx, json.RawMessage(`["work-pc"]`)); err == nil {
		t.Fatalf("expected work machine to be rejected")
	}
	// Reject unknown machine.
	if _, err := r.s.setMachineFallbacks(ctx, json.RawMessage(`["no-such-pc"]`)); err == nil {
		t.Fatalf("expected unknown machine to be rejected")
	}
	// Accept valid ordered list and apply.
	ch, err := r.s.setMachineFallbacks(ctx, json.RawMessage(`["backup-a", "backup-b", "backup-a"]`))
	if err != nil || ch == nil || ch.apply == nil {
		t.Fatalf("setMachineFallbacks valid: ch=%+v err=%v", ch, err)
	}
	if _, err := ch.apply(ctx); err != nil {
		t.Fatalf("apply machine_fallbacks: %v", err)
	}
	if got := machineFallbacksOf(r.s.settings(ctx)); len(got) != 2 || got[0] != "backup-a" || got[1] != "backup-b" {
		t.Fatalf("saved machine_fallbacks: %+v", got)
	}
	// Reject invalid minutes.
	if _, err := r.s.setMachineFallbackMinutes(ctx, json.RawMessage(`0`)); err == nil {
		t.Fatalf("expected 0 minutes to be rejected")
	}
	if _, err := r.s.setMachineFallbackMinutes(ctx, json.RawMessage(`1441`)); err == nil {
		t.Fatalf("expected 1441 minutes to be rejected")
	}
	// Accept valid minutes and apply.
	mch, err := r.s.setMachineFallbackMinutes(ctx, json.RawMessage(`7`))
	if err != nil || mch == nil || mch.apply == nil {
		t.Fatalf("setMachineFallbackMinutes valid: ch=%+v err=%v", mch, err)
	}
	if _, err := mch.apply(ctx); err != nil {
		t.Fatalf("apply machine_fallback_minutes: %v", err)
	}
	if got := machineFallbackMinutesOf(r.s.settings(ctx)); got != 7 {
		t.Fatalf("saved machine_fallback_minutes: got %d, want 7", got)
	}
}

func containsString(list []string, target string) bool {
	for _, v := range list {
		if v == target {
			return true
		}
	}
	return false
}

// Keep unused error import check satisfied if errors is used in future assertions.
var _ = errors.Is
