package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/supervisionrole"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestGoldenSupervisionInstructions(t *testing.T) {
	golden(t, "supervision_instructions", prompts.RenderSupervisionInstructions())
}

// A bullet must start on its own line. A dropped newline ("...rule.- `next`: ...") renders two rules as one bullet,
// and the size ratchet/golden do not catch it because they are regenerated from the glued text (#12648 review).
func TestSupervisionInstructionsBulletsAreSeparateLines(t *testing.T) {
	raw, err := os.ReadFile("../prompts/assistant/supervision_instructions.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, ".- ") {
			t.Fatalf("two bullets are glued on one line (missing newline): %q", line)
		}
	}
}

func TestSupervisionInstructionsDispatchIsolation(t *testing.T) {
	s, f, todo := newSupervisedService(t)
	ctx := context.Background()
	yes, no := true, false
	setSettings(t, s, func(v *Settings) {
		v.MainSupervisesOwnTasks = &no
		v.Leaders = []string{"lead1"}
		v.BackgroundSupervisor, v.BackgroundSupervisorMachineID = "background", "server"
	})
	f.setSessions("server",
		proto.Session{SessionID: "conv1", Assistant: true},
		proto.Session{SessionID: "lead1", Assistant: true},
		proto.Session{SessionID: "background", Assistant: true},
		proto.Session{SessionID: "side", Assistant: true},
		proto.Session{SessionID: "ordinary", Assistant: true},
		proto.Session{SessionID: "worker"},
		proto.Session{SessionID: "health", Assistant: true, Space: SpaceHealth})
	s.todos.mu.Lock()
	s.todos.todos[todo.ID].Supervision.Supervisor = "side"
	s.todos.mu.Unlock()
	for _, tc := range []struct {
		machine, id string
		role        supervisionrole.Role
	}{
		{machine: "server", id: "conv1", role: supervisionrole.RoleMain()}, {machine: "server", id: "lead1", role: supervisionrole.RoleLeader()},
		{machine: "server", id: "background", role: supervisionrole.RoleBackground()}, {machine: "server", id: "side", role: supervisionrole.RoleSide()},
		{machine: "server", id: "ordinary"}, {machine: "server", id: "worker"},
		{machine: "server", id: "health"}, {machine: "server", id: "gone"},
		{machine: "box", id: "lead1"}, {machine: "box", id: "background"},
		{machine: "box", id: "side"}, {machine: "box", id: "conv1"},
	} {
		t.Run(tc.machine+"/"+tc.id, func(t *testing.T) {
			want := ""
			var noRole supervisionrole.Role // the rows above with no role get no policy
			if tc.role != noRole {
				want = mustSupervisionRole(t, tc.role)
			}
			for _, method := range []string{DeviceAssistantSend, DeviceSessionSend} {
				p := map[string]any{"session_id": tc.id, "text": "New task facts", "supervision_instructions": "caller policy"}
				if err := s.call(ctx, tc.machine, method, p, nil); err != nil {
					t.Fatal(err)
				}
				f.mu.Lock()
				sends := f.sends
				if method == DeviceSessionSend {
					sends = f.agentSent
				}
				got := sends[len(sends)-1]["supervision_instructions"]
				f.mu.Unlock()
				if got != want {
					t.Fatalf("%s policy: got %d bytes, want %d", method, len(got.(string)), len(want))
				}
				if tc.id == "conv1" && tc.machine == "server" && p["is_main"] != true {
					t.Fatalf("%s lost the main conversation's execution identity", method)
				}
			}
			resolved, err := s.SendParams(ctx, SendParams{MachineID: tc.machine, SessionID: tc.id, Params: map[string]any{"supervision_instructions": "untrusted"}})
			if err != nil {
				t.Fatal(err)
			}
			if resolved["supervision_instructions"] != want {
				t.Fatal("app/node model resolution disagrees with manager dispatch")
			}
		})
	}
	policy := mustSupervisionRole(t, supervisionrole.RoleMain())
	setSettings(t, s, func(v *Settings) { v.MainSupervisesOwnTasks = &yes })
	if mustSupervisionInstructions(t, s, ctx, "server", "conv1") != policy {
		t.Fatal("self-supervising main lacks policy")
	}
	if mustSupervisionInstructions(t, s, ctx, "box", "conv1") != "" {
		t.Fatal("main identity leaked to another machine")
	}
	s.todos.mu.Lock()
	s.todos.todos[todo.ID].Supervision.Supervisor = "lead1"
	s.todos.mu.Unlock()
	setSettings(t, s, func(v *Settings) { v.Leaders = nil })
	if got := dispatchedPolicy(t, s, "lead1"); got != mustSupervisionRole(t, supervisionrole.RoleSide()) {
		t.Fatal("former leader with remaining tasks did not become a side supervisor")
	}
	s.todos.mu.Lock()
	s.todos.todos[todo.ID].Supervision.Supervisor = "side"
	s.todos.mu.Unlock()
	if mustSupervisionInstructions(t, s, ctx, "server", "lead1") != "" {
		t.Fatal("former direction without tasks retained policy")
	}
	now := time.Now()
	s.todos.mu.Lock()
	s.todos.todos[todo.ID].DoneAt = &now
	s.todos.mu.Unlock()
	if mustSupervisionInstructions(t, s, ctx, "server", "side") != "" {
		t.Fatal("side chat retained policy after its last task closed")
	}
}

func TestSupervisionRoleKeyIsJSONSafeAndUnambiguous(t *testing.T) {
	if key := supervisionRoleKey("server", "conv1"); strings.ContainsRune(key, '\x00') {
		t.Fatalf("PostgreSQL jsonb rejects this object key: %q", key)
	}
	if supervisionRoleKey("a", "bc") == supervisionRoleKey("ab", "c") {
		t.Fatal("machine and conversation ids have an ambiguous concatenation")
	}
}

func TestSupervisionRoleRefusedIdleSendRestoresLegacyBaseline(t *testing.T) {
	s, f, _ := newSupervisedService(t)
	f.setSessions("server", proto.Session{SessionID: "conv1", Assistant: true, Status: proto.StatusIdle})
	compact := mustSupervisionRole(t, supervisionrole.RoleMain())
	if got := ownerMessage(t, s, "conv1", "stale-idle"); got != compact {
		t.Fatal("the idle send did not propose its compact role")
	}
	// The session row was stale: the node was still running the legacy full policy and refused the fingerprint change.
	f.setSessions("server", proto.Session{SessionID: "conv1", Assistant: true, Status: proto.StatusWorking})
	hubSentID(s, "conv1", "stale-idle", assistantwire.ConversationFailed)
	if got := ownerPolicy(t, s, "conv1"); got != prompts.RenderSupervisionInstructions() {
		t.Fatal("a refused compact baseline survived into the next working send")
	}
}

func TestSupervisionRoleUpgradeWaitsForAnIdlePolicyChange(t *testing.T) {
	s, f, todo := newSupervisedService(t)
	setSettings(t, s, func(v *Settings) {
		v.Leaders = []string{"lead1"}
		v.BackgroundSupervisor, v.BackgroundSupervisorMachineID = "background", "server"
	})
	s.todos.mu.Lock()
	s.todos.todos[todo.ID].Supervision.Supervisor = "side"
	s.todos.mu.Unlock()
	roles := map[string]supervisionrole.Role{
		"conv1": supervisionrole.RoleMain(), "lead1": supervisionrole.RoleLeader(),
		"background": supervisionrole.RoleBackground(), "side": supervisionrole.RoleSide(),
	}
	setStatus := func(status string) {
		sessions := make([]proto.Session, 0, len(roles))
		for id := range roles {
			sessions = append(sessions, proto.Session{SessionID: id, Assistant: true, Status: status})
		}
		f.setSessions("server", sessions...)
	}
	full := prompts.RenderSupervisionInstructions()
	setStatus(proto.StatusWorking)
	for id := range roles {
		if got := ownerPolicy(t, s, id); got != full {
			t.Fatalf("%s: an upgraded working legacy process received a changed policy", id)
		}
	}
	setStatus(proto.StatusIdle)
	for id, role := range roles {
		if got := dispatchedPolicy(t, s, id); got != mustSupervisionRole(t, role) {
			t.Fatalf("%s: the idle send did not select its role policy", id)
		}
	}
	setStatus(proto.StatusWorking)
	for id, role := range roles {
		if got := ownerPolicy(t, s, id); got != mustSupervisionRole(t, role) {
			t.Fatalf("%s: the recorded role was not retained while working", id)
		}
	}
}

func TestSupervisionInstructionsFirstStartAndWarm(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	yes := true
	setSettings(t, s, func(v *Settings) { v.MainSupervisesOwnTasks = &yes })
	policy := mustSupervisionRole(t, supervisionrole.RoleMain())
	for _, tc := range []struct {
		name, purpose string
		main, want    bool
	}{
		{"main", "", true, true},
		{"side", "", false, false},
		{"purpose alone is not authority", purposeSupervise, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.startOn(ctx, "server", StartParams{Text: "Hello", Purpose: tc.purpose}, tc.main); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			got := f.sends[len(f.sends)-1]["supervision_instructions"]
			f.mu.Unlock()
			if (got == policy) != tc.want || !tc.want && got != "" {
				t.Fatal("first start has the wrong policy")
			}
		})
	}
	// This existing main conversation has not accepted a compact baseline. A policy-only lookup therefore retains the
	// complete legacy policy; the legacy preference still must not demote it to no supervision at all.
	legacy := prompts.RenderSupervisionInstructions()
	setSettings(t, s, func(v *Settings) { v.MainSupervisesOwnTasks = nil })
	if got := mustSupervisionInstructions(t, s, ctx, "server", s.settings(ctx).Main); got != legacy {
		t.Fatal("existing main lost its fail-closed legacy policy with the legacy preference unset")
	}
	no := false
	setSettings(t, s, func(v *Settings) { v.MainSupervisesOwnTasks = &no })
	if got := mustSupervisionInstructions(t, s, ctx, "server", s.settings(ctx).Main); got != legacy {
		t.Fatal("existing main lost its fail-closed legacy policy with the legacy preference off")
	}
	s.noBackground = false
	setSettings(t, s, func(v *Settings) { v.BackgroundSupervisor, v.BackgroundSupervisorMachineID = "bg1", "server" })
	if got := mustSupervisionInstructions(t, s, ctx, "server", "bg1"); got != legacy {
		t.Fatal("the existing legacy background supervisor lost its full-policy fallback")
	}
	setSettings(t, s, func(v *Settings) { v.BackgroundSupervisor, v.BackgroundSupervisorMachineID = "", "" })
	s.warm("")
	f.mu.Lock()
	defer f.mu.Unlock()
	found := false
	for _, call := range f.calls {
		if call.Method != DeviceAssistantWarm {
			continue
		}
		var p map[string]any
		if err := json.Unmarshal(call.Params, &p); err != nil {
			t.Fatal(err)
		}
		found = true
		if p["supervision_instructions"] != policy || p["is_main"] != true {
			t.Fatal("main warm did not use its startup role")
		}
	}
	if !found {
		t.Fatal("no warm dispatch")
	}
}

func TestSupervisionBackgroundStartUsesBackgroundPolicy(t *testing.T) {
	s, f := newQuietService(t)
	s.noBackground = false
	_, err := s.startBackground(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sends) == 0 {
		t.Fatal("background first start was not dispatched")
	}
	send := f.sends[len(f.sends)-1]
	if send["session_id"] != "" && send["session_id"] != nil {
		t.Fatalf("first start unexpectedly resumed %v", send["session_id"])
	}
	if got, want := send["supervision_instructions"], mustSupervisionRole(t, supervisionrole.RoleBackground()); got != want {
		t.Fatalf("background first start received %d bytes, want its %d-byte role policy", len(got.(string)), len(want))
	}
}

func TestSupervisionStartupBaselineCoversFirstWorkingTurn(t *testing.T) {
	s, f := newQuietService(t)
	s.noBackground = false
	id, err := s.startBackground(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.setSessions("server", proto.Session{SessionID: id, Assistant: true, Status: proto.StatusWorking})
	if got := ownerPolicy(t, s, id); got != mustSupervisionRole(t, supervisionrole.RoleBackground()) {
		t.Fatal("a follow-up during the first turn lost the compact policy the node accepted at startup")
	}
}

func TestSupervisionStartupRejectsUnknownRole(t *testing.T) {
	s, _ := newQuietService(t)
	// Only a supervisionrole.Role names a startup role. A bare word is refused rather than parsed, a stored one too.
	for _, word := range []string{"worker", "MAIN", "main"} {
		ctx := context.WithValue(context.Background(), supervisionStartup{}, word)
		p := map[string]any{}
		if err := s.putSupervisionInstructions(ctx, "server", "", p); err == nil {
			t.Fatalf("startup role %q silently received a policy", word)
		}
		if _, ok := p["supervision_instructions"]; ok {
			t.Fatalf("startup role %q wrote a policy before failing", word)
		}
	}
}

func TestRelayAcknowledgementAndSilentSupervisionInstructions(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	yes := true
	setSettings(t, s, func(v *Settings) { v.MainSupervisesOwnTasks = &yes })
	// #5732 does not start a new background supervisor. Use an existing legacy conversation to test its relay behavior.
	withBackgroundBG1(t, s, f)
	const acknowledgement = "`conversation_send` 中继只需确认时，在本对话写一行正文，不调用 `conversation_send` 回传"
	const relayHeader = "只是确认收到就不要回"
	const silent = "supervision_finish visibility=\"silent\""
	for name, id := range map[string]string{"main": "conv1", "background": "bg1"} {
		t.Run(name, func(t *testing.T) {
			if _, err := s.prepare(ctx, "server"); err != nil {
				t.Fatal(err)
			}
			if md := f.prepared[len(f.prepared)-1]["claude_md"].(string); !strings.Contains(md, acknowledgement) {
				t.Fatal("shared assistant instructions lack the local-only relay acknowledgement rule")
			}

			relay := relayMsg{From: "sender", FromTitle: "Example conversation", Tool: "conversation_send", Text: "The report is ready for reference."}
			if err := s.deliverRelay(ctx, "server", id, relay); err != nil {
				t.Fatal(err)
			}
			send := f.sends[len(f.sends)-1]
			if send[holdPushesParam] == true || !strings.Contains(send["text"].(string), relayHeader) {
				t.Fatal("conversation relay lost its legacy header or was dispatched as supervision")
			}

			event := relayMsg{Tool: superviseTool, Text: "Synthetic supervision event."}
			if err := s.deliverRelay(ctx, "server", id, event); err != nil {
				t.Fatal(err)
			}
			send = f.sends[len(f.sends)-1]
			if send[holdPushesParam] != true || strings.Contains(send["text"].(string), relayHeader) ||
				!strings.Contains(send["supervision_instructions"].(string), silent) {
				t.Fatal("supervision event lost its silent disposition or gained the conversation relay header")
			}
		})
	}
}

func TestSupervisionEventsContainFactsNotPolicy(t *testing.T) {
	policy := prompts.RenderSupervisionInstructions()
	for _, name := range []string{"overdue", "estimate_due", "parked_due", "park_decided", "inbox", "ask", "escalated", "leader_stuck", "recheck", "undecidable", "owner_cannot_answer", "decide", "all_stopped", "pr_conflict"} {
		t.Run(name, func(t *testing.T) {
			p := prompts.Supervise{Leader: true, Side: true, Background: true, Escalated: true, BackgroundEscalated: true,
				LeaderStuck: true, Overdue: true, ParkedDue: true, ParkDecided: true, Asked: true, AskMinutes: 5, PushGrading: true, Decisions: true, Inbox: true,
				Entries: []prompts.SuperviseEntry{{ID: "todo_example", Title: "Example task", Kind: name, Last: "Task evidence"}}}
			text := prompts.RenderSupervise(p)
			for _, banned := range []string{"怎么处理", "task_review", "agent_send", "ask_escalate", "interaction_answer", "supervision_finish", "task_scope", "验收以它", "按任务本身", "Check each participant's stated wait", "Waiting on another stopped participant"} {
				if strings.Contains(text, banned) {
					t.Fatalf("event contains handling policy %q", banned)
				}
			}
			if !strings.Contains(text, "todo_example") || !strings.Contains(text, "Task evidence") || !strings.Contains(text, "signal="+name) {
				t.Fatal("event lost its task, signal or Agent facts")
			}
			if prompts.RenderSupervisionInstructions() != policy {
				t.Fatal("policy bytes vary with task/event/role data")
			}
		})
	}
	for _, background := range []bool{false, true} {
		common := prompts.RenderAssistantClaudeMD(prompts.AssistantClaudeMD{BackgroundSupervisor: background})
		if common != prompts.RenderAssistantClaudeMD(prompts.AssistantClaudeMD{}) {
			t.Fatal("supervisor-mode changes rewrote the directory shared by ordinary chats")
		}
		for _, banned := range []string{"## Task supervision", "visibility: \"silent\"", "cost_of_waiting", "user_check", "独立评审"} {
			if strings.Contains(common, banned) {
				t.Fatalf("shared instructions contain detailed policy %q", banned)
			}
		}
	}
	for _, role := range []prompts.AssistantContext{
		{Leader: true, Owner: "Example user", LeaderName: "Example direction"},
		{Background: true, Owner: "Example user"},
	} {
		text := prompts.RenderAssistantContext(role)
		for _, banned := range []string{"task_review", "agent_send", "leader_progress", "supervision_finish", "todo_create"} {
			if strings.Contains(text, banned) {
				t.Fatalf("role facts repeated handling policy %q", banned)
			}
		}
	}
	// #5732 has no background startup prompt: an existing legacy conversation only finishes its old work.
}

// mustSupervisionRole is the compact policy a conversation of role runs on.
func mustSupervisionRole(t *testing.T, role supervisionrole.Role) string {
	t.Helper()
	text := supervisionPolicy(role)
	if text == "" {
		t.Fatal("empty supervision policy")
	}
	return text
}

func mustSupervisionInstructions(t *testing.T, s *Service, ctx context.Context, machine, id string) string {
	t.Helper()
	text, err := s.supervisionInstructions(ctx, machine, id)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

type supervisionUnavailableStore struct {
	Store
	todoStore
}

func (supervisionUnavailableStore) ListTodos(context.Context) ([]db.TodoRow, error) {
	return nil, errors.New("task ownership unavailable")
}

func TestSupervisionInstructionsUnknownOwnershipDoesNotDemote(t *testing.T) {
	f := newFakeHub().withMachine()
	s := newServiceOn(t, f, supervisionUnavailableStore{Store: &memStore{}})
	f.setSessions("server", proto.Session{SessionID: "side", Assistant: true})
	s.todos.mu.Lock()
	s.todos.loaded = false
	s.todos.mu.Unlock()
	p := map[string]any{"session_id": "side", "text": "New facts"}
	f.mu.Lock()
	before := len(f.sends)
	f.mu.Unlock()
	err := s.call(context.Background(), "server", DeviceAssistantSend, p, nil)
	if err == nil || !strings.Contains(err.Error(), "task ownership unavailable") {
		t.Fatalf("unknown ownership dispatched without supervisor policy: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sends) != before {
		t.Fatal("an ownership lookup failure reached the node")
	}
}

func TestSupervisionInstructionsGuestsNeverBecomeSupervisors(t *testing.T) {
	f, s, _ := guestService(t)
	w := postConversation(t, s, `{"prompt":"Read a connector","purpose":"lark.agent","guest":{"tools":["lark_agent_reply"]}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("guest start: %d", w.Code)
	}
	var result struct {
		Session proto.Session `json:"session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	setSettings(t, s, func(v *Settings) { v.Leaders = []string{result.Session.SessionID} })
	p := map[string]any{"session_id": result.Session.SessionID, "text": "Read again", "supervision_instructions": "untrusted caller policy"}
	if err := s.call(context.Background(), result.Session.MachineID, DeviceAssistantSend, p, nil); err != nil {
		t.Fatal(err)
	}
	if p["supervision_instructions"] != "" {
		t.Fatal("guest inherited supervision instructions")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, send := range f.sends {
		if send["supervision_instructions"] != "" {
			t.Fatal("guest first start or resumed dispatch received supervisor policy")
		}
	}
}

func TestSupervisionPolicySizeBudget(t *testing.T) {
	raw, err := os.ReadFile(promptSizeBaseline)
	if err != nil {
		t.Fatal(err)
	}
	var baseline map[string]int
	if err := json.Unmarshal(raw, &baseline); err != nil {
		t.Fatal(err)
	}
	pieces := promptSizePieces(t)
	if pieces["supervision_instructions"] != len(prompts.RenderSupervisionInstructions()) {
		t.Fatal("the new static fragment is not covered by the size ratchet")
	}
	verdict := decidePromptSizeUpdate(baseline, pieces)
	t.Logf("instruction budget: %d -> %d bytes including the static fragment", verdict.baselineTotal, verdict.currentTotal)
	if !verdict.accept {
		t.Fatal("moving the policy raised the total instruction budget")
	}
}
