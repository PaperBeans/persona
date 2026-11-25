package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

type recoveryNodeMeta struct {
	name        string
	hostname    string
	bootTime    int64
	connectedAt time.Time
}

type recoveryNodeTable struct {
	mu    sync.Mutex
	nodes map[string]recoveryNodeMeta
}

func attachRecoveryNodes(s *Service) *recoveryNodeTable {
	tbl := &recoveryNodeTable{nodes: map[string]recoveryNodeMeta{}}
	s.nodeRecovery.mu.Lock()
	s.nodeRecovery.nodeInfoHook = func(machine string) (string, string, int64, time.Time, bool) {
		tbl.mu.Lock()
		defer tbl.mu.Unlock()
		m, ok := tbl.nodes[machine]
		return m.name, m.hostname, m.bootTime, m.connectedAt, ok
	}
	s.nodeRecovery.mu.Unlock()
	return tbl
}

func (r *recoveryNodeTable) set(id, name, hostname string, bootTime int64, connectedAt time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nodes[id] = recoveryNodeMeta{
		name:        name,
		hostname:    hostname,
		bootTime:    bootTime,
		connectedAt: connectedAt,
	}
}

func createSupervisedRecoveryTodo(t *testing.T, s *Service, title, machine, session string) Todo {
	t.Helper()
	ctx := context.Background()
	todo, err := s.createTodo(ctx, title, "Recovery test task.", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.superviseTodo(ctx, todo.ID, "conv1", []string{"Checks pass"}, 30); err != nil {
		t.Fatal(err)
	}
	if err := s.linkTodoAgent(ctx, todo.ID, machine, session, title, "/code/app", "claude"); err != nil {
		t.Fatal(err)
	}
	return todoNow(s, todo.ID)
}

func TestNodeRecoveryDisconnectReconnect(t *testing.T) {
	ctx := context.Background()
	s, f := newQuietService(t)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	nodes := attachRecoveryNodes(s)

	base := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	bootBefore := base.Add(-2 * time.Hour).Unix()
	nodes.set("worker", "Worker Node", "worker-host", bootBefore, base.Add(-time.Hour))
	nodes.set("other", "Other Node", "other-host", bootBefore, base.Add(-time.Hour))

	// Record initial online state and then a disconnect.
	s.noteNodeOffline("worker", base)

	// Task 1: resumable Agent on the reconnecting node with a live tab and background monitor.
	tResumable := createSupervisedRecoveryTodo(t, s, "Run integration checks", "worker", "s-resumable")
	// Task 2: waiter Agent on another machine waiting on a CI job tied to the reconnecting node.
	tWaiter := createSupervisedRecoveryTodo(t, s, "Wait for CI on worker", "other", "s-waiter")
	// Task 3: unresumable Agent on the reconnecting node whose tab is closed.
	tClosed := createSupervisedRecoveryTodo(t, s, "Closed worker agent", "worker", "s-closed")
	// Task 4: Agent on the reconnecting node blocked on a needs_you card (cannot auto-wake).
	tNeedsYou := createSupervisedRecoveryTodo(t, s, "Card blocked worker agent", "worker", "s-needs-you")

	f.setSessions("worker",
		proto.Session{
			SessionID: "s-resumable", MachineID: "worker", Status: proto.StatusIdle, State: proto.StateIdle,
			PaneID: "w1:p1", StartedByPersona: true, UpdatedAt: base,
			Background: []proto.BackgroundTask{{ID: "bg1", Kind: "monitor", Description: "watch build queue"}},
		},
		proto.Session{
			SessionID: "s-closed", MachineID: "worker", Status: proto.StatusOffline, State: proto.StateOffline,
			StartedByPersona: true, UpdatedAt: base, Activity: "running migration",
		},
		proto.Session{
			SessionID: "s-needs-you", MachineID: "worker", Status: proto.StatusBlocked, State: proto.StateNeedsYou,
			PaneID: "w1:p2", PendingInteractions: 1, StartedByPersona: true, UpdatedAt: base,
			Activity: "awaiting approval for deploy",
		},
	)
	f.setSessions("other",
		proto.Session{
			SessionID: "s-waiter", MachineID: "other", Status: proto.StatusIdle, State: proto.StateWorking,
			PaneID: "w2:p1", StartedByPersona: true, UpdatedAt: base, BackgroundWaiting: true,
			Background: []proto.BackgroundTask{{
				ID: "bg-ci", Kind: "shell", Description: "CI shard on worker",
				Command: "ssh worker-host run-ci",
			}},
		},
	)

	var wakeMu sync.Mutex
	wakes := map[string]string{}
	f.answerNode(DeviceSessionSend, func(machine string, raw json.RawMessage) (any, error) {
		var p struct {
			SessionID string `json:"session_id"`
			Text      string `json:"text"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		wakeMu.Lock()
		wakes[machine+"/"+p.SessionID] = p.Text
		wakeMu.Unlock()
		return map[string]any{"ok": true}, nil
	})

	// Brief disconnect (< nodeRecoveryOfflineAfter) with unchanged boot time does not trigger a sweep.
	shortBack := base.Add(nodeRecoveryOfflineAfter - time.Minute)
	nodes.set("worker", "Worker Node", "worker-host", bootBefore, shortBack)
	if res := s.nodeConnectedAt(ctx, "worker", shortBack); res.Triggered {
		t.Fatalf("brief disconnect should not trigger recovery sweep: %+v", res)
	}

	// Disconnect again and reconnect after an extended outage (20m > 10m).
	s.noteNodeOffline("worker", shortBack)
	reconnectedAt := shortBack.Add(20 * time.Minute)
	nodes.set("worker", "Worker Node", "worker-host", bootBefore, reconnectedAt)
	res := s.nodeConnectedAt(ctx, "worker", reconnectedAt)
	if !res.Triggered || res.Rebooted || res.Checked != 4 || res.Frozen != 1 || res.Woken != 2 || res.Signalled != 2 {
		t.Fatalf("unexpected disconnect/reconnect sweep result: %+v", res)
	}

	// Resumable Agent on worker and waiter Agent on other should both be woken with outage details.
	wakeMu.Lock()
	resumableMsg := wakes["worker/s-resumable"]
	waiterMsg := wakes["other/s-waiter"]
	wakeMu.Unlock()
	if !strings.Contains(resumableMsg, "20m") || !strings.Contains(resumableMsg, "watch build queue") {
		t.Fatalf("resumable wake message missing outage/step details: %q", resumableMsg)
	}
	if !strings.Contains(waiterMsg, "20m") || !strings.Contains(waiterMsg, "CI shard on worker") {
		t.Fatalf("waiter wake message missing outage/lost job details: %q", waiterMsg)
	}

	// Unresumable closed Agent emits sigOrphaned; needs_you Agent emits sigRecovered.
	s.supervise.mu.Lock()
	gOrphan, okOrphan := s.supervise.pending[tClosed.ID+"|"+sigOrphaned]
	gRecovered, okRecovered := s.supervise.pending[tNeedsYou.ID+"|"+sigRecovered]
	_, hasResumableSig := s.supervise.pending[tResumable.ID+"|"+sigRecovered]
	_, hasWaiterSig := s.supervise.pending[tWaiter.ID+"|"+sigRecovered]
	s.supervise.mu.Unlock()

	if !okOrphan || gOrphan.Minutes != 20 || !strings.Contains(gOrphan.Detail, "running migration") || !strings.Contains(gOrphan.Detail, "20m") {
		t.Fatalf("expected orphaned signal with outage and step, got %+v (ok=%v)", gOrphan, okOrphan)
	}
	if !okRecovered || gRecovered.Minutes != 20 || !strings.Contains(gRecovered.Detail, "awaiting approval for deploy") || !strings.Contains(gRecovered.Detail, "20m") {
		t.Fatalf("expected recovered signal with outage and step, got %+v (ok=%v)", gRecovered, okRecovered)
	}
	if hasResumableSig || hasWaiterSig {
		t.Fatalf("resumed tasks should not emit unresumable signals")
	}

	// Both signals render into the supervision digest cleanly.
	eOrphan, ok := s.digestEntry(ctx, gOrphan, "")
	if !ok || eOrphan.Kind != sigOrphaned {
		t.Fatalf("digestEntry(orphaned) = %+v %v", eOrphan, ok)
	}
	eRecovered, ok := s.digestEntry(ctx, gRecovered, "")
	if !ok || eRecovered.Kind != sigRecovered || eRecovered.Minutes != 20 {
		t.Fatalf("digestEntry(recovered) = %+v %v", eRecovered, ok)
	}
	rendered := prompts.RenderSupervise(prompts.Supervise{Entries: []prompts.SuperviseEntry{eOrphan, eRecovered}})
	if !strings.Contains(rendered, "signal=orphaned") || !strings.Contains(rendered, "signal=recovered") {
		t.Fatalf("supervision digest missing signals:\n%s", rendered)
	}

	// Re-notifying the exact same connection must be idempotent.
	if dup := s.nodeConnectedAt(ctx, "worker", reconnectedAt.Add(time.Second)); dup.Triggered {
		t.Fatalf("duplicate notification for same connection should not re-sweep: %+v", dup)
	}
}

func flushRecoveryDigest(ctx context.Context, s *Service) {
	s.supervise.mu.Lock()
	past := time.Now().Add(-time.Minute)
	for id, g := range s.supervise.pending {
		g.At = past
		s.supervise.pending[id] = g
	}
	s.supervise.mu.Unlock()
	s.superviseTimes = superviseTimings{gather: time.Millisecond, every: time.Millisecond, urgent: time.Millisecond}
	s.sendDigest(ctx)
}

func TestNodeRecoveryReboot(t *testing.T) {
	ctx := context.Background()
	s, f := newQuietService(t)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	nodes := attachRecoveryNodes(s)

	base := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	oldBoot := base.Add(-4 * time.Hour).Unix()
	nodes.set("builder", "Builder Box", "builder-host", oldBoot, base.Add(-time.Hour))
	nodes.set("controller", "Controller Box", "controller-host", oldBoot, base.Add(-time.Hour))

	s.noteNodeOffline("builder", base)

	// Task 1: Agent on rebooted node became interrupted after reboot (cannot auto-resume).
	tInterrupted := createSupervisedRecoveryTodo(t, s, "Build release on builder", "builder", "s-interrupted")
	// Task 2: Waiter Agent on controller waiting on a CI job routed to builder.
	tWaiter := createSupervisedRecoveryTodo(t, s, "Run remote CI on builder", "controller", "s-ci-waiter")
	// Task 3: Agent whose wake fails falls back to sigRecovered.
	tWakeFail := createSupervisedRecoveryTodo(t, s, "Wake failure fallback", "builder", "s-wake-fail")

	f.setSessions("builder",
		proto.Session{
			SessionID: "s-interrupted", MachineID: "builder", Status: proto.StatusInterrupted, State: proto.StateUnknown,
			StartedByPersona: true, UpdatedAt: base, Plan: &proto.PlanInfo{Current: "compile server binary"},
		},
		proto.Session{
			SessionID: "s-wake-fail", MachineID: "builder", Status: proto.StatusIdle, State: proto.StateIdle,
			PaneID: "w1:p9", StartedByPersona: true, UpdatedAt: base, Activity: "syncing cache",
		},
	)
	f.setSessions("controller",
		proto.Session{
			SessionID: "s-ci-waiter", MachineID: "controller", Status: proto.StatusIdle, State: proto.StateWorking,
			PaneID: "w3:p1", StartedByPersona: true, UpdatedAt: base, BackgroundWaiting: true,
			Work: &proto.WorkState{Phase: proto.WorkBlocked, Wait: "waiting for CI job on builder-host"},
		},
	)

	var wakeMu sync.Mutex
	wakes := map[string]string{}
	f.answerNode(DeviceSessionSend, func(machine string, raw json.RawMessage) (any, error) {
		var p struct {
			SessionID string `json:"session_id"`
			Text      string `json:"text"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		if p.SessionID == "s-wake-fail" {
			return nil, errors.New("pane busy")
		}
		wakeMu.Lock()
		wakes[machine+"/"+p.SessionID] = p.Text
		wakeMu.Unlock()
		return map[string]any{"ok": true}, nil
	})

	// Only 2 minutes offline (< 10m), but BootTime is after lastOnline -> reboot detected.
	reconnectedAt := base.Add(2 * time.Minute)
	newBoot := base.Add(1 * time.Minute).Unix()
	nodes.set("builder", "Builder Box", "builder-host", newBoot, reconnectedAt)

	res := s.nodeConnectedAt(ctx, "builder", reconnectedAt)
	if !res.Triggered || !res.Rebooted || res.Checked != 3 || res.Woken != 1 || res.Signalled != 2 {
		t.Fatalf("unexpected reboot sweep result: %+v", res)
	}

	wakeMu.Lock()
	waiterMsg := wakes["controller/s-ci-waiter"]
	wakeMu.Unlock()
	if !strings.Contains(waiterMsg, "rebooted") || !strings.Contains(waiterMsg, "builder-host") {
		t.Fatalf("waiter wake message on reboot missing reboot/target info: %q", waiterMsg)
	}

	// Deliver digest and confirm signals go to supervisor silently (holdPushesParam=true, no owner push).
	flushRecoveryDigest(ctx, s)
	notices := f.notices()
	if len(notices) != 1 {
		t.Fatalf("expected 1 supervision digest notice, got %d: %v", len(notices), notices)
	}
	msg := notices[0]
	for _, want := range []string{tInterrupted.ID, "signal=orphaned", "compile server binary", "rebooted", tWakeFail.ID, "signal=recovered", "syncing cache", "auto-wake failed"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("supervision digest missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, tWaiter.ID) {
		t.Fatalf("successfully woken waiter should not be flagged as unresumable in digest:\n%s", msg)
	}

	f.mu.Lock()
	var convSends []map[string]any
	for _, a := range f.sends {
		if a["session_id"] == "conv1" {
			convSends = append(convSends, a)
		}
	}
	f.mu.Unlock()
	if len(convSends) != 1 || convSends[0][holdPushesParam] != true {
		t.Fatalf("supervision digest must hold owner pushes, got sends=%+v", convSends)
	}
}

// Scenario 3: task moved to another node during the outage.
// When the old node returns: freeze old Agent first, detect takeover by the new
// node, do NOT resume/redo work, release the old Agent, withdraw its leftover
// background waits, and record "taken over by X" in supervision without pushing.
func TestNodeRecoveryMovedToAnotherNode(t *testing.T) {
	ctx := context.Background()
	s, f := newQuietService(t)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	nodes := attachRecoveryNodes(s)

	base := time.Date(2026, 11, 24, 14, 0, 0, 0, time.UTC)
	bootBefore := base.Add(-3 * time.Hour).Unix()
	nodes.set("old-node", "Old Node", "old-host", bootBefore, base.Add(-time.Hour))
	nodes.set("new-node", "New Node", "new-host", bootBefore, base.Add(-time.Hour))

	s.noteNodeOffline("old-node", base)

	// Task originally started on old-node, then during the outage was moved to new-node.
	task := createSupervisedRecoveryTodo(t, s, "Ship feature branch", "old-node", "s-old")
	if err := s.linkTodoAgent(ctx, task.ID, "new-node", "s-new", "Ship feature branch", "/code/app", "claude"); err != nil {
		t.Fatal(err)
	}
	s.todos.mu.Lock()
	for i := range s.todos.todos[task.ID].Agents {
		if s.todos.todos[task.ID].Agents[i].MachineID == "old-node" {
			s.todos.todos[task.ID].Agents[i].LinkedAt = base.Add(-30 * time.Minute)
		} else {
			s.todos.todos[task.ID].Agents[i].LinkedAt = base.Add(5 * time.Minute)
		}
	}
	s.todos.mu.Unlock()

	// Seed a leftover scheduled background wait for the old Agent.
	wakeAt := base.Add(25 * time.Minute)
	s.putWork(ctx, db.SessionWork{
		MachineID: "old-node",
		SessionID: "s-old",
		WakeAt:    &wakeAt,
		Work:      proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitExternal, WakeAt: &wakeAt},
	})

	f.setSessions("old-node", proto.Session{
		SessionID: "s-old", MachineID: "old-node", Status: proto.StatusWorking, State: proto.StateWorking,
		PaneID: "w1:p1", StartedByPersona: true, UpdatedAt: base.Add(-5 * time.Minute),
		Branch: "feat/demo", LastText: "starting tests",
	})
	f.setSessions("new-node", proto.Session{
		SessionID: "s-new", MachineID: "new-node", Status: proto.StatusWorking, State: proto.StateWorking,
		PaneID: "w2:p1", StartedByPersona: true, UpdatedAt: base.Add(12 * time.Minute),
		Branch: "feat/demo", LastText: "pushed fix and running CI",
	})

	var (
		mu          sync.Mutex
		interrupted []string
		stopped     []string
		woken       []string
	)
	f.answerNode("session.interrupt", func(machine string, raw json.RawMessage) (any, error) {
		var p struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(raw, &p)
		mu.Lock()
		interrupted = append(interrupted, machine+"/"+p.SessionID)
		mu.Unlock()
		return map[string]any{"ok": true, "settled": true}, nil
	})
	f.answerNode(deviceSessionStop, func(machine string, raw json.RawMessage) (any, error) {
		var p struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(raw, &p)
		mu.Lock()
		stopped = append(stopped, machine+"/"+p.SessionID)
		mu.Unlock()
		return map[string]any{"ok": true}, nil
	})
	f.answerNode(DeviceSessionSend, func(machine string, raw json.RawMessage) (any, error) {
		var p struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(raw, &p)
		mu.Lock()
		woken = append(woken, machine+"/"+p.SessionID)
		mu.Unlock()
		return map[string]any{"ok": true}, nil
	})

	reconnectedAt := base.Add(20 * time.Minute)
	nodes.set("old-node", "Old Node", "old-host", bootBefore, reconnectedAt)
	res := s.nodeConnectedAt(ctx, "old-node", reconnectedAt)
	if !res.Triggered || res.Frozen != 1 || res.Released != 1 || res.Woken != 0 || res.Conflicts != 0 || res.Signalled != 1 {
		t.Fatalf("unexpected moved-task recovery result: %+v", res)
	}

	mu.Lock()
	gotInt, gotStop, gotWake := append([]string(nil), interrupted...), append([]string(nil), stopped...), append([]string(nil), woken...)
	mu.Unlock()
	if len(gotInt) != 1 || gotInt[0] != "old-node/s-old" {
		t.Fatalf("expected old Agent to be frozen first via session.interrupt, got %v", gotInt)
	}
	if len(gotStop) != 1 || gotStop[0] != "old-node/s-old" {
		t.Fatalf("expected covered old Agent to be stopped, got %v", gotStop)
	}
	if len(gotWake) != 0 {
		t.Fatalf("covered old Agent must not be woken to redo work, got %v", gotWake)
	}

	// Old Agent must be released while new Agent remains active.
	updated := todoNow(s, task.ID)
	var oldReleased, newActive bool
	for _, a := range updated.Agents {
		if a.MachineID == "old-node" && a.SessionID == "s-old" && a.ReleasedAt != nil {
			oldReleased = true
		}
		if a.MachineID == "new-node" && a.SessionID == "s-new" && a.ReleasedAt == nil {
			newActive = true
		}
	}
	if !oldReleased || !newActive {
		t.Fatalf("expected old Agent released and new Agent active, got %+v", updated.Agents)
	}

	// Leftover background wait for old-node/s-old must be retired.
	s.work.mu.Lock()
	we := s.work.entries[[2]string{"old-node", "s-old"}]
	s.work.mu.Unlock()
	if we == nil || we.WakeAt != nil || we.Work.RetiredAt == nil {
		t.Fatalf("expected leftover work wait to be retired, got %+v", we)
	}

	// Supervision digest records "taken over by new-node/s-new" silently.
	flushRecoveryDigest(ctx, s)
	notices := f.notices()
	if len(notices) != 1 || !strings.Contains(notices[0], "taken over by new-node/s-new") {
		t.Fatalf("expected takeover record in supervision digest, got %v", notices)
	}
}

// Scenario 4: both sides pushed commits during the outage.
// When the old node returns: freeze old Agent first, detect diverging progress,
// do NOT auto-pick a side (neither wake nor release), and signal the supervisor.
func TestNodeRecoveryBothSidesPushedConflict(t *testing.T) {
	ctx := context.Background()
	s, f := newQuietService(t)
	s.superviseTimes = superviseTimings{gather: time.Hour, every: time.Hour, urgent: time.Hour}
	nodes := attachRecoveryNodes(s)

	base := time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC)
	bootBefore := base.Add(-3 * time.Hour).Unix()
	nodes.set("node-a", "Node A", "host-a", bootBefore, base.Add(-time.Hour))
	nodes.set("node-b", "Node B", "host-b", bootBefore, base.Add(-time.Hour))

	s.noteNodeOffline("node-a", base)

	task := createSupervisedRecoveryTodo(t, s, "Refactor scheduler", "node-a", "s-a")
	if err := s.linkTodoAgent(ctx, task.ID, "node-b", "s-b", "Refactor scheduler", "/code/app", "claude"); err != nil {
		t.Fatal(err)
	}
	s.todos.mu.Lock()
	for i := range s.todos.todos[task.ID].Agents {
		if s.todos.todos[task.ID].Agents[i].MachineID == "node-a" {
			s.todos.todos[task.ID].Agents[i].LinkedAt = base.Add(-20 * time.Minute)
		} else {
			s.todos.todos[task.ID].Agents[i].LinkedAt = base.Add(6 * time.Minute)
		}
	}
	s.todos.mu.Unlock()

	// Both sides pushed commits on the same branch after outageStart.
	f.setSessions("node-a", proto.Session{
		SessionID: "s-a", MachineID: "node-a", Status: proto.StatusWorking, State: proto.StateWorking,
		PaneID: "w1:p1", StartedByPersona: true, UpdatedAt: base.Add(10 * time.Minute),
		Branch: "feat/scheduler", LastText: "pushed commit abc111 to feat/scheduler",
	})
	f.setSessions("node-b", proto.Session{
		SessionID: "s-b", MachineID: "node-b", Status: proto.StatusIdle, State: proto.StateIdle,
		PaneID: "w2:p1", StartedByPersona: true, UpdatedAt: base.Add(14 * time.Minute),
		Branch: "feat/scheduler", LastText: "pushed commit def222 to feat/scheduler",
	})

	var (
		mu          sync.Mutex
		interrupted []string
		woken       []string
	)
	f.answerNode("session.interrupt", func(machine string, raw json.RawMessage) (any, error) {
		var p struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(raw, &p)
		mu.Lock()
		interrupted = append(interrupted, machine+"/"+p.SessionID)
		mu.Unlock()
		return map[string]any{"ok": true, "settled": true}, nil
	})
	f.answerNode(DeviceSessionSend, func(machine string, raw json.RawMessage) (any, error) {
		var p struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(raw, &p)
		mu.Lock()
		woken = append(woken, machine+"/"+p.SessionID)
		mu.Unlock()
		return map[string]any{"ok": true}, nil
	})

	reconnectedAt := base.Add(20 * time.Minute)
	nodes.set("node-a", "Node A", "host-a", bootBefore, reconnectedAt)
	res := s.nodeConnectedAt(ctx, "node-a", reconnectedAt)
	if !res.Triggered || res.Frozen != 1 || res.Conflicts != 1 || res.Released != 0 || res.Woken != 0 || res.Signalled != 1 {
		t.Fatalf("unexpected conflict recovery result: %+v", res)
	}

	mu.Lock()
	gotInt, gotWake := append([]string(nil), interrupted...), append([]string(nil), woken...)
	mu.Unlock()
	if len(gotInt) != 1 || gotInt[0] != "node-a/s-a" {
		t.Fatalf("expected returning Agent to be frozen first, got %v", gotInt)
	}
	if len(gotWake) != 0 {
		t.Fatalf("conflicting side must not be auto-woken, got %v", gotWake)
	}

	// Neither side should be released automatically.
	updated := todoNow(s, task.ID)
	for _, a := range updated.Agents {
		if a.ReleasedAt != nil {
			t.Fatalf("conflict must not auto-release either Agent: %+v", updated.Agents)
		}
	}

	// Conflict is handed to the supervisor via supervision digest (no owner push).
	flushRecoveryDigest(ctx, s)
	notices := f.notices()
	if len(notices) != 1 {
		t.Fatalf("expected 1 conflict supervision digest, got %d: %v", len(notices), notices)
	}
	for _, want := range []string{"signal=recovered", "diverging progress conflict", "feat/scheduler", "node-a/s-a", "node-b/s-b"} {
		if !strings.Contains(notices[0], want) {
			t.Fatalf("conflict digest missing %q:\n%s", want, notices[0])
		}
	}
}
